// Package handlers serves the config web UI's JSON API, the OIDC login routes,
// the OAuth protected-resource metadata, and the MCP endpoint itself.
package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/daedaluz/spliit-mcp/internal/config"
	appoidc "github.com/daedaluz/spliit-mcp/internal/oidc"
	"github.com/daedaluz/spliit-mcp/internal/spliit"
	"github.com/daedaluz/spliit-mcp/internal/store"
)

// Server holds the dependencies shared by every HTTP handler.
type Server struct {
	cfg     *config.Config
	store   *store.Store
	oidc    *appoidc.Provider
	clients *spliit.Clients
	log     *slog.Logger

	// mcpHandler serves the MCP endpoint, already wrapped in bearer auth.
	mcpHandler http.Handler
}

// New builds the HTTP server.
func New(
	cfg *config.Config, st *store.Store, provider *appoidc.Provider,
	clients *spliit.Clients, mcpHandler http.Handler, log *slog.Logger,
) *Server {
	return &Server{
		cfg: cfg, store: st, oidc: provider,
		clients: clients, mcpHandler: mcpHandler, log: log,
	}
}

// Routes returns the fully wired router.
func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// OAuth 2.0 protected resource metadata (RFC 9728). MCP clients fetch this
	// after a 401 to discover which authorization server to use.
	r.Get("/.well-known/oauth-protected-resource", s.ProtectedResourceMetadata)
	r.Get("/.well-known/oauth-protected-resource/mcp", s.ProtectedResourceMetadata)

	// The MCP endpoint. Bearer verification is applied by the caller when
	// wrapping mcpHandler, not here.
	if s.mcpHandler != nil {
		r.Handle("/mcp", s.mcpHandler)
		r.Handle("/mcp/*", s.mcpHandler)
	}

	// Config web UI login.
	r.Get("/auth/login", s.LoginHandler())
	r.Get("/auth/callback", s.CallbackHandler())
	r.Post("/auth/logout", s.LogoutHandler())

	r.Route("/api", func(api chi.Router) {
		api.Use(s.RequireSession)

		api.Get("/me", s.GetMe)
		api.Put("/me", s.UpdateMe)

		api.Get("/servers", s.ListServers)
		api.Post("/servers", s.CreateServer)
		api.Patch("/servers/{id}", s.UpdateServer)
		api.Delete("/servers/{id}", s.DeleteServer)

		api.Get("/groups", s.ListGroups)
		api.Post("/groups/preview", s.PreviewGroup)
		api.Post("/groups", s.CreateGroup)
		api.Patch("/groups/{id}", s.UpdateGroup)
		api.Delete("/groups/{id}", s.DeleteGroup)
	})

	if s.cfg.WebDir != "" {
		r.NotFound(s.serveSPA(s.cfg.WebDir))
	}
	return r
}

// serveSPA serves the built frontend, falling back to index.html so that
// client-side routes resolve on a hard refresh.
func (s *Server) serveSPA(dir string) http.HandlerFunc {
	fileServer := http.FileServer(http.Dir(dir))

	return func(w http.ResponseWriter, r *http.Request) {
		// Never let an unmatched API or auth path fall through to the SPA; a
		// 200 with HTML would be far more confusing than a 404.
		for _, prefix := range []string{"/api/", "/auth/", "/mcp"} {
			if strings.HasPrefix(r.URL.Path, prefix) {
				http.NotFound(w, r)
				return
			}
		}

		candidate := filepath.Join(dir, filepath.Clean("/"+r.URL.Path))
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			fileServer.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	}
}

// ProtectedResourceMetadata publishes RFC 9728 metadata for the MCP endpoint.
// Clients fetch it after a 401 to discover which authorization server to use.
func (s *Server) ProtectedResourceMetadata(w http.ResponseWriter, _ *http.Request) {
	metadata := map[string]any{
		"resource":                 s.cfg.MCPResourceURL(),
		"authorization_servers":    []string{s.cfg.OIDC.Issuer},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "spliit-mcp",
	}
	// Omit rather than publish a null when no scopes are required.
	if len(s.cfg.OIDC.RequiredScopes) > 0 {
		metadata["scopes_supported"] = s.cfg.OIDC.RequiredScopes
	}
	writeJSON(w, http.StatusOK, metadata)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// The status line is already sent, so there is nothing to recover to.
		slog.Default().Error("write json response", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// serverError logs the underlying cause and returns an opaque 500, so internal
// details (DSNs, upstream URLs) never reach the browser.
func (s *Server) serverError(w http.ResponseWriter, r *http.Request, op string, err error) {
	s.log.Error(op, "error", err, "path", r.URL.Path)
	writeError(w, http.StatusInternalServerError, "internal error")
}

func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}
