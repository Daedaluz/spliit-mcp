package handlers

import (
	"errors"
	"net/http"
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
func (s *Server) LoginHandler() http.HandlerFunc {
	return rp.AuthURLHandler(appoidc.State, s.oidc.RelyingParty)
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

	return rp.CodeExchangeHandler(onSuccess, s.oidc.RelyingParty)
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
