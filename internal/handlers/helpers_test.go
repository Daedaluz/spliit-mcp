package handlers_test

import (
	"io"
	"log/slog"

	"github.com/daedaluz/spliit-mcp/internal/config"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func baseConfig() *config.Config {
	return &config.Config{
		PublicURL: "https://example.test",
		OIDC: config.OIDCConfig{
			Issuer:    "https://id.example.test",
			ClientID:  "client",
			Scopes:    []string{"openid", "profile", "email"},
			MCPScopes: []string{"openid", "profile", "email", "offline_access"},
		},
	}
}
