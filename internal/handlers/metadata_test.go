package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/daedaluz/spliit-mcp/internal/config"
	"github.com/daedaluz/spliit-mcp/internal/handlers"
)

// metadata renders the protected-resource document for a config.
func metadata(t *testing.T, cfg *config.Config) map[string]any {
	t.Helper()

	srv := handlers.New(cfg, nil, nil, nil, nil, discardLogger())
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	return out
}

func scopes(t *testing.T, doc map[string]any) []string {
	t.Helper()
	raw, _ := doc["scopes_supported"].([]any)
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		out = append(out, s.(string))
	}
	return out
}

// A client that receives no refresh token has to send the user through a full
// login every time the access token expires, which is what offline_access
// prevents.
func TestMetadataAdvertisesOfflineAccess(t *testing.T) {
	cfg := baseConfig()
	got := scopes(t, metadata(t, cfg))

	if !slices.Contains(got, "offline_access") {
		t.Errorf("scopes_supported = %v, want offline_access so clients can refresh", got)
	}
	if !slices.Contains(got, "openid") {
		t.Errorf("scopes_supported = %v, want openid", got)
	}
}

// Scopes must never be empty: a client builds its `scope` parameter from this,
// and providers reject an authorization request that asks for none.
func TestMetadataNeverAdvertisesNoScopes(t *testing.T) {
	cfg := baseConfig()
	cfg.OIDC.MCPScopes = nil
	cfg.OIDC.RequiredScopes = nil

	if got := scopes(t, metadata(t, cfg)); len(got) == 0 {
		t.Error("scopes_supported is empty; clients would request no scopes at all")
	}
}

// Anything strictly required has to be requested, or every token would be
// rejected for lacking it.
func TestMetadataIncludesRequiredScopes(t *testing.T) {
	cfg := baseConfig()
	cfg.OIDC.RequiredScopes = []string{"spliit:write"}

	if got := scopes(t, metadata(t, cfg)); !slices.Contains(got, "spliit:write") {
		t.Errorf("scopes_supported = %v, want the required spliit:write", got)
	}
}

func TestMetadataReportsTheResource(t *testing.T) {
	doc := metadata(t, baseConfig())

	if doc["resource"] != "https://example.test/mcp" {
		t.Errorf("resource = %v, want the public URL's /mcp", doc["resource"])
	}
	servers, _ := doc["authorization_servers"].([]any)
	if len(servers) != 1 || servers[0] != "https://id.example.test" {
		t.Errorf("authorization_servers = %v, want the configured issuer", servers)
	}
}
