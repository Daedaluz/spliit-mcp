package handlers

import (
	"context"
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
//
// On first login the user is seeded with a default Spliit server so the config
// page is immediately usable; without it every new user would have to paste a
// tRPC URL before they could add a single group.
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

		user, err := s.store.UpsertUser(ctx, claims.Subject,
			s.cfg.OIDC.Issuer, claims.Email, claims.DisplayName)
		if err != nil {
			fail("persist user", err)
			return
		}

		if err := s.ensureDefaultServer(ctx, user.Sub); err != nil {
			fail("seed default spliit server", err)
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

// ensureDefaultServer gives a brand new user one registered Spliit instance.
func (s *Server) ensureDefaultServer(ctx context.Context, sub string) error {
	servers, err := s.store.ListServers(ctx, sub)
	if err != nil {
		return err
	}
	if len(servers) > 0 {
		return nil
	}
	_, err = s.store.CreateServer(ctx, sub, s.cfg.Spliit.DefaultName, s.cfg.Spliit.DefaultURL)
	if errors.Is(err, store.ErrConflict) {
		return nil // Raced with a concurrent login; the row exists either way.
	}
	return err
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
