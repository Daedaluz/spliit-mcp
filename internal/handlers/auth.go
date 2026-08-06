package handlers

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zitadel/oidc/v3/pkg/client/rp"
	zoidc "github.com/zitadel/oidc/v3/pkg/oidc"

	appoidc "github.com/daedaluz/spliit-mcp/internal/oidc"
	"github.com/daedaluz/spliit-mcp/internal/store"
)

// sessionCookieName holds the config web UI session ID.
const sessionCookieName = "spliit_mcp_session"

// userContextKey is the gin context key holding the authenticated web user.
const userContextKey = "spliit_mcp_user"

// UserFromContext returns the authenticated web user, or nil.
func UserFromContext(c *gin.Context) *store.User {
	value, ok := c.Get(userContextKey)
	if !ok {
		return nil
	}
	user, _ := value.(*store.User)
	return user
}

// LoginHandler starts the authorization code + PKCE flow.
//
// It first bounces the browser to the canonical public URL when it arrived on a
// different host. The login sets a cookie holding the OAuth state and PKCE
// verifier, but the provider always returns to the redirect URI built from
// public_url — so starting at, say, localhost while public_url names a LAN
// address sets the cookie on one host and reads it on another. Cookies are
// host-scoped, so it simply is not sent, and the callback fails with a bare
// "named cookie not present" that says nothing about the real cause.
func (s *Server) LoginHandler() http.HandlerFunc {
	start := rp.AuthURLHandler(appoidc.State, s.oidc.RelyingParty)

	return func(w http.ResponseWriter, r *http.Request) {
		want := publicHost(s.cfg.PublicURL)
		if got := hostnameOf(r.Host); want != "" && got != "" && got != want {
			s.log.Info("redirecting login to the canonical host",
				"got", got, "want", want)
			http.Redirect(w, r, s.cfg.PublicURL+"/auth/login", http.StatusFound)
			return
		}
		start(w, r)
	}
}

// publicHost is the hostname of the configured public URL, without the port.
func publicHost(publicURL string) string {
	parsed, err := url.Parse(publicURL)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

// hostnameOf strips any port from a Host header.
//
// The port is deliberately ignored. Proxies rewrite it — nginx's $host drops it
// entirely — and cookies are not port-scoped anyway, so the hostname alone is
// exactly what decides whether the state cookie comes back.
func hostnameOf(host string) string {
	if host == "" {
		return ""
	}
	if stripped, _, err := net.SplitHostPort(host); err == nil {
		return stripped
	}
	return host
}

// CallbackHandler completes the flow, upserts the user, and issues a session.
func (s *Server) CallbackHandler() http.HandlerFunc {
	onSuccess := func(w http.ResponseWriter, r *http.Request,
		tokens *zoidc.Tokens[*zoidc.IDTokenClaims], _ string, _ rp.RelyingParty,
	) {
		ctx := r.Context()
		claims := appoidc.ClaimsFromIDToken(tokens.IDTokenClaims)

		fail := func(op string, err error) {
			s.log.Error(op, "error", err, "path", r.URL.Path)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}

		// Plenty of providers keep name and email out of the ID token and serve
		// them only from the userinfo endpoint, which leaves the display name
		// falling back to the opaque subject. Fetch them when they are missing.
		if claims.NeedsUserinfo() {
			info, err := rp.Userinfo[*zoidc.UserInfo](ctx,
				tokens.AccessToken, tokens.TokenType,
				tokens.IDTokenClaims.GetSubject(), s.oidc.RelyingParty)
			if err != nil {
				// Not fatal: the user is authenticated either way, they just get
				// a less friendly name until they set one.
				s.log.Warn("fetch userinfo", "error", err, "sub", claims.Subject)
			} else {
				claims.MergeUserinfo(info)
			}
		}

		user, err := s.store.UpsertUser(ctx, claims.Subject,
			s.cfg.OIDC.Issuer, claims.Email, claims.DisplayName)
		if err != nil {
			fail("persist user", err)
			return
		}

		session, err := s.store.CreateSession(ctx, user.Sub, s.cfg.SessionTTL)
		if err != nil {
			fail("create session", err)
			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    session.ID,
			Path:     "/",
			Expires:  session.ExpiresAt,
			HttpOnly: true,
			Secure:   s.cfg.SessionCookieSecure,
			SameSite: http.SameSiteLaxMode,
		})

		http.Redirect(w, r, "/", http.StatusFound)
	}

	exchange := rp.CodeExchangeHandler(onSuccess, s.oidc.RelyingParty)

	return func(w http.ResponseWriter, r *http.Request) {
		// The library's own error for a missing state cookie says nothing about
		// why it is missing, and the usual cause is arriving here on a host other
		// than the one that started the login.
		if _, err := r.Cookie("state"); errors.Is(err, http.ErrNoCookie) {
			host := publicHost(s.cfg.PublicURL)
			s.log.Warn("callback without a login state cookie",
				"host", r.Host, "public_host", host)
			http.Error(w, "This login did not start here, or it expired.\n\n"+
				"Start again at "+s.cfg.PublicURL+"/auth/login and use that same "+
				"address throughout — the state cookie is tied to it.",
				http.StatusBadRequest)
			return
		}
		exchange(w, r)
	}
}

// LogoutHandler ends the session and clears the cookie.
func (s *Server) LogoutHandler(c *gin.Context) {
	if cookie, err := c.Cookie(sessionCookieName); err == nil {
		if err := s.store.DeleteSession(c.Request.Context(), cookie); err != nil {
			s.log.Warn("delete session on logout", "error", err)
		}
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cfg.SessionCookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	c.Status(http.StatusNoContent)
}

// RequireSession rejects requests without a live config web UI session and
// attaches the resolved user to the request context.
func (s *Server) RequireSession() gin.HandlerFunc {
	return func(c *gin.Context) {
		cookie, err := c.Cookie(sessionCookieName)
		if err != nil {
			writeError(c, http.StatusUnauthorized, "not signed in")
			return
		}

		ctx := c.Request.Context()

		session, err := s.store.GetSession(ctx, cookie)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(c, http.StatusUnauthorized, "session expired")
				return
			}
			s.serverError(c, "load session", err)
			return
		}

		user, err := s.store.GetUser(ctx, session.UserSub)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(c, http.StatusUnauthorized, "user no longer exists")
				return
			}
			s.serverError(c, "load user", err)
			return
		}

		c.Set(userContextKey, user)
		c.Next()
	}
}
