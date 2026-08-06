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

	"github.com/gin-gonic/gin"

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
	// Release mode unless explicitly overridden: debug mode logs every route and
	// prints warnings that are noise in a deployed service.
	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(s.requestLogger())

	r.GET("/healthz", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	// OAuth 2.0 protected resource metadata (RFC 9728). MCP clients fetch this
	// after a 401 to discover which authorization server to use.
	r.GET("/.well-known/oauth-protected-resource", s.ProtectedResourceMetadata)
	r.GET("/.well-known/oauth-protected-resource/mcp", s.ProtectedResourceMetadata)

	// The MCP endpoint. Bearer verification is applied by the caller when
	// wrapping mcpHandler, not here. gin.WrapH keeps the streaming semantics of
	// the SDK's handler intact.
	if s.mcpHandler != nil {
		mcp := gin.WrapH(s.mcpHandler)
		r.Any("/mcp", mcp)
		r.Any("/mcp/*path", mcp)
	}

	// Config web UI login. These are net/http handlers from the OIDC library.
	r.GET("/auth/login", gin.WrapF(s.LoginHandler()))
	r.GET("/auth/callback", gin.WrapF(s.CallbackHandler()))
	r.POST("/auth/logout", s.LogoutHandler)

	api := r.Group("/api", s.RequireSession())
	{
		api.GET("/me", s.GetMe)
		api.PUT("/me", s.UpdateMe)
		api.GET("/config", s.GetConfig)

		api.GET("/servers", s.ListServers)
		api.POST("/servers", s.CreateServer)
		api.PATCH("/servers/:id", s.UpdateServer)
		api.DELETE("/servers/:id", s.DeleteServer)

		api.GET("/groups", s.ListGroups)
		api.POST("/groups/preview", s.PreviewGroup)
		api.POST("/groups", s.CreateGroup)
		api.POST("/groups/new", s.CreateSpliitGroup)
		api.PATCH("/groups/:id", s.UpdateGroup)
		api.DELETE("/groups/:id", s.DeleteGroup)
	}

	if s.cfg.WebDir != "" {
		r.NoRoute(s.serveSPA(s.cfg.WebDir))
	}
	return r
}

// requestLogger emits one structured line per request.
func (s *Server) requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		// Anything the handlers recorded via gin's error list is worth surfacing.
		if len(c.Errors) > 0 {
			s.log.Error("request failed",
				"method", c.Request.Method,
				"path", c.Request.URL.Path,
				"status", c.Writer.Status(),
				"errors", c.Errors.String())
		}
	}
}

// serveSPA serves the built frontend, falling back to index.html so that
// client-side routes resolve on a hard refresh.
func (s *Server) serveSPA(dir string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Never let an unmatched API or auth path fall through to the SPA; a
		// 200 with HTML would be far more confusing than a 404.
		for _, prefix := range []string{"/api/", "/auth/", "/mcp"} {
			if strings.HasPrefix(c.Request.URL.Path, prefix) {
				c.Status(http.StatusNotFound)
				return
			}
		}

		candidate := filepath.Join(dir, filepath.Clean("/"+c.Request.URL.Path))
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			c.File(candidate)
			return
		}
		c.File(filepath.Join(dir, "index.html"))
	}
}

// ProtectedResourceMetadata publishes RFC 9728 metadata for the MCP endpoint.
// Clients fetch it after a 401 to discover which authorization server to use.
func (s *Server) ProtectedResourceMetadata(c *gin.Context) {
	metadata := gin.H{
		"resource":                 s.cfg.MCPResourceURL(),
		"authorization_servers":    []string{s.cfg.OIDC.Issuer},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "spliit-mcp",
	}

	// Clients build their authorization request's `scope` from this list, so it
	// must be advertised even when this server enforces no particular scope.
	// Publishing nothing makes the client request no scopes at all, and
	// providers that mandate one reject the authorization request outright.
	scopes := s.cfg.OIDC.RequiredScopes
	if len(scopes) == 0 {
		scopes = s.cfg.OIDC.Scopes
	}
	if len(scopes) > 0 {
		metadata["scopes_supported"] = scopes
	}

	c.JSON(http.StatusOK, metadata)
}

func writeError(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": message})
}

// serverError logs the underlying cause and returns an opaque 500, so internal
// details (DSNs, upstream URLs) never reach the browser.
func (s *Server) serverError(c *gin.Context, op string, err error) {
	s.log.Error(op, "error", err, "path", c.Request.URL.Path)
	writeError(c, http.StatusInternalServerError, "internal error")
}

// bindJSON decodes a request body, rejecting unknown fields so a typo in the
// SPA surfaces as an error instead of being silently dropped.
func bindJSON(c *gin.Context, dst any) error {
	body := http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}
