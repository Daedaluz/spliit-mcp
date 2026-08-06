// Package oidc wires up the two independent authorization surfaces of this
// server against a single OIDC provider:
//
//   - The config web UI acts as a relying party: authorization code + PKCE,
//     resulting in a server-side session cookie.
//   - The MCP endpoint acts as an OAuth 2.0 protected resource: it verifies
//     bearer access tokens presented by MCP clients.
//
// Neither has anything to do with Spliit's own API, which has no authentication
// at all. OIDC here guards the group IDs this server holds on a user's behalf.
package oidc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/zitadel/oidc/v3/pkg/client"
	"github.com/zitadel/oidc/v3/pkg/client/rp"
	"github.com/zitadel/oidc/v3/pkg/client/rs"
	httphelper "github.com/zitadel/oidc/v3/pkg/http"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"

	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"

	appconfig "github.com/daedaluz/spliit-mcp/internal/config"
)

// Provider holds everything derived from OIDC discovery.
type Provider struct {
	cfg *appconfig.Config

	// RelyingParty drives the config web UI login.
	RelyingParty rp.RelyingParty

	// verifier validates JWT access tokens against the issuer's JWKS.
	verifier *op.AccessTokenVerifier
	// resourceServer performs token introspection, used when the provider
	// issues opaque access tokens that cannot be verified locally.
	resourceServer rs.ResourceServer

	// Discovery values republished to MCP clients.
	AuthorizationEndpoint string
	TokenEndpoint         string
	RegistrationEndpoint  string
	ScopesSupported       []string
}

// New performs discovery and builds both surfaces.
func New(ctx context.Context, cfg *appconfig.Config) (*Provider, error) {
	httpClient := &http.Client{Timeout: 30 * time.Second}

	discovery, err := client.Discover(ctx, cfg.OIDC.Issuer, httpClient)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery for %s: %w", cfg.OIDC.Issuer, err)
	}

	// The cookie handler secures the OAuth state and PKCE verifier across the
	// redirect to the provider and back.
	hashKey, encKey, err := stateCookieKeys(cfg.OIDC.StateSecret)
	if err != nil {
		return nil, err
	}

	opts := []httphelper.CookieHandlerOpt{
		httphelper.WithMaxAge(int(stateCookieTTL.Seconds())),
	}
	if !cfg.SessionCookieSecure {
		// Plain HTTP: a Secure cookie would never be sent back, and the callback
		// would fail with a missing state cookie.
		opts = append(opts, httphelper.WithUnsecure())
	}
	cookieHandler := httphelper.NewCookieHandler(hashKey, encKey, opts...)

	relyingParty, err := rp.NewRelyingPartyOIDC(ctx,
		cfg.OIDC.Issuer, cfg.OIDC.ClientID, cfg.OIDC.ClientSecret,
		cfg.RedirectURI(), cfg.OIDC.Scopes,
		rp.WithHTTPClient(httpClient),
		rp.WithPKCE(cookieHandler),
		rp.WithVerifierOpts(rp.WithIssuedAtOffset(5*time.Second)),
	)
	if err != nil {
		return nil, fmt.Errorf("build relying party: %w", err)
	}

	p := &Provider{
		cfg:                   cfg,
		RelyingParty:          relyingParty,
		AuthorizationEndpoint: discovery.AuthorizationEndpoint,
		TokenEndpoint:         discovery.TokenEndpoint,
		RegistrationEndpoint:  discovery.RegistrationEndpoint,
		ScopesSupported:       discovery.ScopesSupported,
	}

	if discovery.JwksURI != "" {
		keySet := rp.NewRemoteKeySet(httpClient, discovery.JwksURI)
		p.verifier = op.NewAccessTokenVerifier(cfg.OIDC.Issuer, keySet)
	}

	// Introspection is the fallback for opaque access tokens. It needs client
	// credentials, so it is only available when a secret was configured.
	if discovery.IntrospectionEndpoint != "" && cfg.OIDC.ClientSecret != "" {
		resourceServer, err := rs.NewResourceServerClientCredentials(ctx,
			cfg.OIDC.Issuer, cfg.OIDC.ClientID, cfg.OIDC.ClientSecret,
			rs.WithClient(httpClient),
		)
		if err != nil {
			return nil, fmt.Errorf("build resource server: %w", err)
		}
		p.resourceServer = resourceServer
	}

	if cfg.OIDC.StateSecret == "" {
		slog.Default().Warn("oidc.state_secret is unset: login state cookies use " +
			"per-process keys, so logins in flight will break on restart and will " +
			"fail entirely if this server runs more than one replica")
	}

	if p.verifier == nil && p.resourceServer == nil {
		return nil, errors.New("provider exposes neither a JWKS nor a usable introspection endpoint; " +
			"MCP bearer tokens cannot be verified")
	}
	return p, nil
}

// TokenVerifier returns a verifier for the MCP bearer middleware.
//
// JWT access tokens are verified locally against the issuer's JWKS; opaque
// tokens fall back to introspection. Either way the audience is checked against
// this server's resource identifier, so a token minted for a different service
// at the same issuer cannot be replayed here.
func (p *Provider) TokenVerifier() mcpauth.TokenVerifier {
	return func(ctx context.Context, token string, _ *http.Request) (*mcpauth.TokenInfo, error) {
		if p.verifier != nil {
			info, err := p.verifyJWT(ctx, token)
			if err == nil {
				return info, nil
			}
			// Only fall through to introspection if there is one; otherwise the
			// JWT error is the real answer.
			if p.resourceServer == nil {
				return nil, err
			}
		}
		return p.introspect(ctx, token)
	}
}

func (p *Provider) verifyJWT(ctx context.Context, token string) (*mcpauth.TokenInfo, error) {
	claims, err := op.VerifyAccessToken[*oidc.AccessTokenClaims](ctx, token, p.verifier)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", mcpauth.ErrInvalidToken, err)
	}
	if err := p.checkAudience(claims.Audience); err != nil {
		return nil, err
	}
	if err := p.checkScopes(claims.Scopes); err != nil {
		return nil, err
	}
	return &mcpauth.TokenInfo{
		Scopes:     claims.Scopes,
		Expiration: claims.Expiration.AsTime(),
		UserID:     claims.Subject,
		Extra:      map[string]any{"sub": claims.Subject, "email": claims.Claims["email"]},
	}, nil
}

func (p *Provider) introspect(ctx context.Context, token string) (*mcpauth.TokenInfo, error) {
	resp, err := rs.Introspect[*oidc.IntrospectionResponse](ctx, p.resourceServer, token)
	if err != nil {
		return nil, fmt.Errorf("%w: introspection failed: %s", mcpauth.ErrInvalidToken, err)
	}
	if !resp.Active {
		return nil, fmt.Errorf("%w: token is not active", mcpauth.ErrInvalidToken)
	}
	if err := p.checkAudience(resp.Audience); err != nil {
		return nil, err
	}
	if err := p.checkScopes(resp.Scope); err != nil {
		return nil, err
	}
	return &mcpauth.TokenInfo{
		Scopes:     resp.Scope,
		Expiration: resp.Expiration.AsTime(),
		UserID:     resp.Subject,
		Extra:      map[string]any{"sub": resp.Subject, "email": resp.Email},
	}, nil
}

// checkAudience enforces RFC 8707 audience binding for the MCP resource.
func (p *Provider) checkAudience(audience []string) error {
	if p.cfg.OIDC.SkipAudienceCheck {
		return nil
	}
	resource := p.cfg.MCPResourceURL()
	if slices.Contains(audience, resource) {
		return nil
	}
	// Some providers set the audience to the client ID rather than the
	// resource; accept that only when it is this server's own MCP client.
	if p.cfg.OIDC.MCPClientID != "" && slices.Contains(audience, p.cfg.OIDC.MCPClientID) {
		return nil
	}
	return fmt.Errorf("%w: token audience %v does not include %q "+
		"(set oidc.skip_audience_check only if your provider cannot bind audiences)",
		mcpauth.ErrInvalidToken, audience, resource)
}

func (p *Provider) checkScopes(granted []string) error {
	for _, required := range p.cfg.OIDC.RequiredScopes {
		if !slices.Contains(granted, required) {
			return fmt.Errorf("%w: token is missing required scope %q", mcpauth.ErrInvalidToken, required)
		}
	}
	return nil
}

// Claims carries the identity fields taken from a completed web login.
type Claims struct {
	Subject     string
	Email       string
	DisplayName string
}

// ClaimsFromIDToken extracts the fields used to seed a user row. The display
// name falls back through the usual claims, and finally to the subject, so a
// user always has something to match participants against.
func ClaimsFromIDToken(claims *oidc.IDTokenClaims) Claims {
	out := Claims{Subject: claims.Subject, Email: claims.Email}
	out.DisplayName = pickDisplayName(
		claims.Name, claims.PreferredUsername, claims.GivenName,
		claims.Email, claims.Subject)
	return out
}

// NeedsUserinfo reports whether the ID token left anything worth fetching from
// the userinfo endpoint. The display name having fallen back to the subject is
// the telling case: an opaque UUID matches no Spliit participant and reads as a
// bug to the user.
func (c Claims) NeedsUserinfo() bool {
	return c.Email == "" || c.DisplayName == c.Subject
}

// MergeUserinfo fills in fields the ID token did not carry. Values already
// present win, so a provider that sets them in both places stays consistent.
func (c *Claims) MergeUserinfo(info *oidc.UserInfo) {
	if info == nil {
		return
	}
	if c.Email == "" {
		c.Email = info.Email
	}
	if c.DisplayName == "" || c.DisplayName == c.Subject {
		if name := pickDisplayName(
			info.Name, info.PreferredUsername, info.GivenName,
			info.Email, c.Subject,
		); name != "" {
			c.DisplayName = name
		}
	}
}

// pickDisplayName returns the first non-empty candidate.
func pickDisplayName(candidates ...string) string {
	for _, candidate := range candidates {
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

// stateCookieTTL bounds how long a login may sit half-finished. Ten minutes is
// tight for a flow that can involve consent screens and MFA, and expiry shows
// up as a confusing "named cookie not present" at the callback.
const stateCookieTTL = 30 * time.Minute

// stateCookieKeys derives the signing and encryption keys for the state cookie.
//
// Derived from a configured secret when there is one, because random per-process
// keys mean in-flight logins break on every restart and, worse, fail outright
// behind more than one replica: whichever instance handles the callback cannot
// decrypt a cookie another instance wrote. Random keys stay the default so the
// server runs without configuration, and New logs a warning in that case.
func stateCookieKeys(secret string) (hashKey, encKey []byte, err error) {
	if secret == "" {
		if hashKey, err = randomBytes(32); err != nil {
			return nil, nil, err
		}
		if encKey, err = randomBytes(32); err != nil {
			return nil, nil, err
		}
		return hashKey, encKey, nil
	}

	// Two independent keys from one secret, via distinct domain separators.
	hash := sha256.Sum256([]byte("spliit-mcp/state-cookie/hash\x00" + secret))
	enc := sha256.Sum256([]byte("spliit-mcp/state-cookie/enc\x00" + secret))
	return hash[:], enc[:], nil
}

// State generates an unguessable OAuth state value.
func State() string {
	b, err := randomBytes(24)
	if err != nil {
		// crypto/rand does not fail in practice; panicking beats emitting a
		// predictable state and silently weakening CSRF protection.
		panic(fmt.Sprintf("generate oauth state: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("read random bytes: %w", err)
	}
	return b, nil
}
