package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/zitadel/oidc/v3/pkg/client/rp"
	zoidc "github.com/zitadel/oidc/v3/pkg/oidc"

	appoidc "github.com/daedaluz/spliit-mcp/internal/oidc"
	"github.com/daedaluz/spliit-mcp/internal/store"
)

// sessionCookieName holds the config web UI session ID.
const sessionCookieName = "spliit_mcp_session"

type contextKey struct{ name string }

var userContextKey = contextKey{"user"}

// UserFromContext returns the authenticated web user, or nil.
func UserFromContext(ctx context.Context) *store.User {
	u, _ := ctx.Value(userContextKey).(*store.User)
	return u
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

		user, err := s.store.UpsertUser(ctx, claims.Subject,
			s.cfg.OIDC.Issuer, claims.Email, claims.DisplayName)
		if err != nil {
			s.serverError(w, r, "persist user", err)
			return
		}

		if err := s.ensureDefaultServer(ctx, user.Sub); err != nil {
			s.serverError(w, r, "seed default spliit server", err)
			return
		}

		session, err := s.store.CreateSession(ctx, user.Sub, s.cfg.SessionTTL)
		if err != nil {
			s.serverError(w, r, "create session", err)
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
func (s *Server) LogoutHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie(sessionCookieName); err == nil {
			if err := s.store.DeleteSession(r.Context(), cookie.Value); err != nil {
				s.log.Warn("delete session on logout", "error", err)
			}
		}
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    "",
			Path:     "/",
			Expires:  time.Unix(0, 0),
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   s.cfg.SessionCookieSecure,
			SameSite: http.SameSiteLaxMode,
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

// RequireSession rejects requests without a live config web UI session and
// attaches the resolved user to the request context.
func (s *Server) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "not signed in")
			return
		}

		session, err := s.store.GetSession(r.Context(), cookie.Value)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusUnauthorized, "session expired")
				return
			}
			s.serverError(w, r, "load session", err)
			return
		}

		user, err := s.store.GetUser(r.Context(), session.UserSub)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusUnauthorized, "user no longer exists")
				return
			}
			s.serverError(w, r, "load user", err)
			return
		}

		ctx := context.WithValue(r.Context(), userContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
