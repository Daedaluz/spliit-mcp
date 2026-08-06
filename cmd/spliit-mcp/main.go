// Command spliit-mcp serves the Spliit MCP endpoint together with the config
// web UI used to manage which groups are available and who "you" are.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/daedaluz/spliit-mcp/internal/config"
	"github.com/daedaluz/spliit-mcp/internal/db"
	"github.com/daedaluz/spliit-mcp/internal/handlers"
	appmcp "github.com/daedaluz/spliit-mcp/internal/mcp"
	appoidc "github.com/daedaluz/spliit-mcp/internal/oidc"
	"github.com/daedaluz/spliit-mcp/internal/spliit"
	"github.com/daedaluz/spliit-mcp/internal/store"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	var configPath string

	root := &cobra.Command{
		Use:          "spliit-mcp",
		Short:        "MCP server for Spliit, with an OIDC-protected config web UI",
		SilenceUsage: true,
	}
	root.PersistentFlags().StringVarP(&configPath, "config", "c", "",
		"path to a config file (env: SPLIIT_MCP_*)")

	serve := &cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServe(cmd.Context(), configPath)
		},
	}

	migrate := &cobra.Command{
		Use:   "migrate",
		Short: "Apply database migrations and exit",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			database, err := db.Open(cmd.Context(), cfg.DatabaseURL)
			if err != nil {
				return err
			}
			defer func() { _ = database.Close() }()
			return database.Migrate(cmd.Context())
		},
	}

	root.AddCommand(serve, migrate)
	return root
}

func runServe(ctx context.Context, configPath string) error {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	database, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	if err := database.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	log.Info("database ready", "dialect", database.Dialect)

	st := store.New(database)

	provider, err := appoidc.New(ctx, cfg)
	if err != nil {
		return err
	}
	log.Info("oidc provider discovered", "issuer", cfg.OIDC.Issuer)

	clients := spliit.NewClients(cfg.Spliit.Timeout)

	mcpServer := appmcp.NewServer(appmcp.Deps{
		Config: cfg, Store: st, Clients: clients, Log: log,
	})

	// The MCP endpoint is an OAuth 2.0 protected resource: the bearer token is
	// verified before the request ever reaches the MCP handler, and the
	// resulting subject is what scopes every tool call.
	mcpHandler := mcpsdk.NewStreamableHTTPHandler(
		func(*http.Request) *mcpsdk.Server { return mcpServer },
		&mcpsdk.StreamableHTTPOptions{},
	)
	protected := auth.RequireBearerToken(provider.TokenVerifier(), &auth.RequireBearerTokenOptions{
		ResourceMetadataURL: cfg.ResourceMetadataURL(),
		Scopes:              cfg.OIDC.RequiredScopes,
	})(mcpHandler)

	srv := handlers.New(cfg, st, provider, clients, protected, log)

	httpServer := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go purgeExpiredSessions(ctx, st, log)

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening",
			"addr", cfg.Listen,
			"public_url", cfg.PublicURL,
			"mcp_resource", cfg.MCPResourceURL())
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}

// purgeExpiredSessions periodically clears out dead web sessions.
func purgeExpiredSessions(ctx context.Context, st *store.Store, log *slog.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := st.DeleteExpiredSessions(ctx)
			if err != nil {
				log.Warn("purge expired sessions", "error", err)
				continue
			}
			if n > 0 {
				log.Info("purged expired sessions", "count", n)
			}
		}
	}
}
