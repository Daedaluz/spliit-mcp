// Package config loads spliit-mcp configuration from a file, environment
// variables and flags, in that order of increasing precedence.
package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config is the fully resolved server configuration.
type Config struct {
	// Listen is the TCP address the HTTP server binds to.
	Listen string
	// PublicURL is the externally reachable base URL of this server. It is used
	// to build the OIDC redirect URI and the OAuth protected-resource identifier,
	// so it must match what clients actually dial.
	PublicURL string
	// WebDir serves the built SPA when non-empty. Leave empty in the split
	// deployment where nginx serves web/dist.
	WebDir string

	// DatabaseURL selects both driver and target: a postgres:// or postgresql://
	// DSN uses lib/pq, anything else is treated as a SQLite file path.
	DatabaseURL string

	OIDC   OIDCConfig
	Spliit SpliitConfig

	// SessionTTL bounds how long a config-page login stays valid.
	SessionTTL time.Duration
	// SessionCookieSecure sets the Secure attribute on the session cookie. It is
	// derived from PublicURL's scheme unless explicitly overridden.
	SessionCookieSecure bool
}

// OIDCConfig describes the single OIDC provider used for both surfaces: the
// config web UI (relying party) and the MCP endpoint (resource server).
type OIDCConfig struct {
	// Issuer is the OIDC issuer URL; discovery is performed against it.
	Issuer string
	// ClientID and ClientSecret authenticate the config web UI as a relying party.
	ClientID     string
	ClientSecret string
	// Scopes requested during the web login.
	Scopes []string

	// MCPClientID is advertised to MCP clients whose authorization server does
	// not support Dynamic Client Registration. Optional.
	MCPClientID string
	// RequiredScopes, if set, are required on MCP bearer tokens.
	RequiredScopes []string
	// StateSecret derives the keys protecting the OAuth state and PKCE cookie
	// during login. Set it to any stable random string so logins survive a
	// restart; it is required when running more than one replica, since each
	// instance must be able to read a cookie another one wrote.
	StateSecret string
	// SkipAudienceCheck disables validating that a bearer token's `aud` names
	// this server. Only for providers that cannot issue audience-bound tokens;
	// it weakens token-replay protection, so it is off by default.
	SkipAudienceCheck bool
}

// SpliitConfig holds defaults for talking to Spliit instances.
type SpliitConfig struct {
	// DefaultURL is the tRPC base URL seeded as a user's first server row.
	DefaultURL string
	// DefaultName is the display name for that seeded server.
	DefaultName string
	// Timeout bounds a single Spliit HTTP request.
	Timeout time.Duration
}

// DefaultSpliitURL is the tRPC base URL of the public Spliit instance.
const DefaultSpliitURL = "https://spliit.app/api/trpc"

func setDefaults(v *viper.Viper) {
	v.SetDefault("listen", ":8080")
	v.SetDefault("public_url", "http://localhost:8080")
	v.SetDefault("web_dir", "")
	v.SetDefault("database_url", "spliit-mcp.db")

	v.SetDefault("oidc.scopes", []string{"openid", "profile", "email"})
	v.SetDefault("oidc.skip_audience_check", false)

	v.SetDefault("spliit.default_url", DefaultSpliitURL)
	v.SetDefault("spliit.default_name", "spliit.app")
	v.SetDefault("spliit.timeout", 30*time.Second)

	v.SetDefault("session.ttl", 30*24*time.Hour)
}

// Load reads configuration from path (optional), then SPLIIT_MCP_* environment
// variables, and validates the result.
func Load(path string) (*Config, error) {
	v := viper.New()
	setDefaults(v)

	v.SetEnvPrefix("SPLIIT_MCP")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if path != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
	}

	// AutomaticEnv does not discover nested keys that were never set in a file,
	// so bind the ones that matter explicitly.
	for _, key := range []string{
		"oidc.issuer", "oidc.client_id", "oidc.client_secret", "oidc.scopes",
		"oidc.mcp_client_id", "oidc.required_scopes", "oidc.skip_audience_check",
		"oidc.state_secret",
		"spliit.default_url", "spliit.default_name", "spliit.timeout",
		"session.ttl", "session.cookie_secure",
	} {
		if err := v.BindEnv(key); err != nil {
			return nil, fmt.Errorf("bind env %s: %w", key, err)
		}
	}

	cfg := &Config{
		Listen:      v.GetString("listen"),
		PublicURL:   strings.TrimRight(v.GetString("public_url"), "/"),
		WebDir:      v.GetString("web_dir"),
		DatabaseURL: v.GetString("database_url"),
		OIDC: OIDCConfig{
			Issuer:            strings.TrimRight(v.GetString("oidc.issuer"), "/"),
			ClientID:          v.GetString("oidc.client_id"),
			ClientSecret:      v.GetString("oidc.client_secret"),
			Scopes:            v.GetStringSlice("oidc.scopes"),
			MCPClientID:       v.GetString("oidc.mcp_client_id"),
			StateSecret:       v.GetString("oidc.state_secret"),
			RequiredScopes:    v.GetStringSlice("oidc.required_scopes"),
			SkipAudienceCheck: v.GetBool("oidc.skip_audience_check"),
		},
		Spliit: SpliitConfig{
			DefaultURL:  strings.TrimRight(v.GetString("spliit.default_url"), "/"),
			DefaultName: v.GetString("spliit.default_name"),
			Timeout:     v.GetDuration("spliit.timeout"),
		},
		SessionTTL: v.GetDuration("session.ttl"),
	}

	if v.IsSet("session.cookie_secure") {
		cfg.SessionCookieSecure = v.GetBool("session.cookie_secure")
	} else {
		cfg.SessionCookieSecure = strings.HasPrefix(cfg.PublicURL, "https://")
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if c.PublicURL == "" {
		return fmt.Errorf("public_url is required")
	}
	if _, err := url.Parse(c.PublicURL); err != nil {
		return fmt.Errorf("public_url is not a valid URL: %w", err)
	}
	if c.DatabaseURL == "" {
		return fmt.Errorf("database_url is required")
	}
	if c.OIDC.Issuer == "" {
		return fmt.Errorf("oidc.issuer is required")
	}
	if c.OIDC.ClientID == "" {
		return fmt.Errorf("oidc.client_id is required")
	}
	if c.Spliit.DefaultURL == "" {
		return fmt.Errorf("spliit.default_url is required")
	}
	return nil
}

// RedirectURI is the OIDC callback this server registers as a relying party.
func (c *Config) RedirectURI() string { return c.PublicURL + "/auth/callback" }

// MCPResourceURL is the OAuth 2.0 resource identifier for the MCP endpoint. It
// doubles as the expected `aud` value on incoming bearer tokens.
func (c *Config) MCPResourceURL() string { return c.PublicURL + "/mcp" }

// ResourceMetadataURL is where protected-resource metadata is published.
func (c *Config) ResourceMetadataURL() string {
	return c.PublicURL + "/.well-known/oauth-protected-resource"
}
