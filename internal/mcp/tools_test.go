package mcp_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/daedaluz/spliit-mcp/internal/config"
	"github.com/daedaluz/spliit-mcp/internal/db"
	appmcp "github.com/daedaluz/spliit-mcp/internal/mcp"
	"github.com/daedaluz/spliit-mcp/internal/spliit"
	"github.com/daedaluz/spliit-mcp/internal/store"
)

// fakeSpliit is a minimal stand-in for a Spliit instance's tRPC batch endpoint.
// It records the calls it received so tests can assert what was sent upstream.
type fakeSpliit struct {
	t *testing.T

	// results maps a procedure name to the JSON payload to return.
	results map[string]any

	calls []recordedCall
}

type recordedCall struct {
	endpoint string
	input    json.RawMessage
}

func newFakeSpliit(t *testing.T) (*fakeSpliit, string) {
	t.Helper()

	f := &fakeSpliit{t: t, results: map[string]any{}}
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	return f, server.URL + "/api/trpc"
}

func (f *fakeSpliit) serve(w http.ResponseWriter, r *http.Request) {
	// The client encodes the batch as /api/trpc/<ep1,ep2>?batch=1, with the
	// inputs either in the query (queries) or the body (mutations).
	path := strings.TrimPrefix(r.URL.Path, "/api/trpc/")
	endpoints := strings.Split(path, ",")

	raw := r.URL.Query().Get("input")
	if raw == "" {
		body := make([]byte, r.ContentLength)
		if _, err := r.Body.Read(body); err != nil && len(body) == 0 {
			body = []byte("{}")
		}
		raw = string(body)
	}

	var inputs map[string]struct {
		JSON json.RawMessage `json:"json"`
	}
	if err := json.Unmarshal([]byte(raw), &inputs); err != nil {
		f.t.Errorf("fake spliit: decode input %q: %v", raw, err)
	}

	envelopes := make([]map[string]any, 0, len(endpoints))
	for i, endpoint := range endpoints {
		f.calls = append(f.calls, recordedCall{
			endpoint: endpoint,
			input:    inputs[strconv.Itoa(i)].JSON,
		})

		result, ok := f.results[endpoint]
		if !ok {
			envelopes = append(envelopes, map[string]any{
				"error": map[string]any{
					"message": "fake spliit has no canned result for " + endpoint,
					"code":    -32603,
				},
			})
			continue
		}
		envelopes = append(envelopes, map[string]any{
			"result": map[string]any{"data": map[string]any{"json": result}},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(envelopes); err != nil {
		f.t.Errorf("fake spliit: encode response: %v", err)
	}
}

// inputFor returns the decoded input of the first call to an endpoint.
func (f *fakeSpliit) inputFor(endpoint string) map[string]any {
	f.t.Helper()
	for _, c := range f.calls {
		if c.endpoint == endpoint {
			var out map[string]any
			if err := json.Unmarshal(c.input, &out); err != nil {
				f.t.Fatalf("decode recorded input for %s: %v", endpoint, err)
			}
			return out
		}
	}
	f.t.Fatalf("no call recorded for %s (saw %v)", endpoint, f.endpoints())
	return nil
}

func (f *fakeSpliit) endpoints() []string {
	names := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		names = append(names, c.endpoint)
	}
	return names
}

// testEnv is a running MCP server backed by a real store and a fake Spliit.
type testEnv struct {
	store   *store.Store
	spliit  *fakeSpliit
	url     string
	baseURL string
}

// setup builds the full MCP stack, including the bearer-token middleware. The
// verifier is stubbed so tests can present any subject as an authenticated
// caller, which is exactly what the real OIDC verifier yields.
func setup(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()

	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	st := store.New(database)
	fake, baseURL := newFakeSpliit(t)

	cfg := &config.Config{
		PublicURL: "http://localhost:8080",
		Spliit:    config.SpliitConfig{DefaultURL: baseURL, DefaultName: "test", Timeout: 5 * time.Second},
	}

	mcpServer := appmcp.NewServer(appmcp.Deps{
		Config:  cfg,
		Store:   st,
		Clients: spliit.NewClients(5 * time.Second),
		Log:     slog.New(slog.DiscardHandler),
	})

	// Mirrors production: stateless, so tests exercise the same code path.
	handler := mcpsdk.NewStreamableHTTPHandler(
		func(*http.Request) *mcpsdk.Server { return mcpServer },
		&mcpsdk.StreamableHTTPOptions{Stateless: true},
	)

	// Stand in for the OIDC verifier: the bearer token *is* the subject.
	verifier := func(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		return &auth.TokenInfo{
			UserID:     token,
			Expiration: time.Now().Add(time.Hour),
		}, nil
	}
	protected := auth.RequireBearerToken(verifier, &auth.RequireBearerTokenOptions{})(handler)

	httpServer := httptest.NewServer(protected)
	t.Cleanup(httpServer.Close)

	return &testEnv{store: st, spliit: fake, url: httpServer.URL, baseURL: baseURL}
}

// groupURL renders a browser-style group link on the fake Spliit instance, of
// the shape a user would paste.
func (e *testEnv) groupURL(groupID string) string {
	return strings.TrimSuffix(e.baseURL, "/api/trpc") + "/groups/" + groupID + "/expenses"
}

// connect opens an MCP session authenticated as the given subject.
func (e *testEnv) connect(t *testing.T, subject string) *mcpsdk.ClientSession {
	t.Helper()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "1"}, nil)
	transport := &mcpsdk.StreamableClientTransport{
		Endpoint: e.url,
		HTTPClient: &http.Client{
			Transport: bearerRoundTripper{token: subject, base: http.DefaultTransport},
		},
	}

	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatalf("connect as %s: %v", subject, err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

type bearerRoundTripper struct {
	token string
	base  http.RoundTripper
}

func (b bearerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(clone)
}

// call invokes a tool and returns its text content plus whether it errored.
func call(t *testing.T, session *mcpsdk.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()

	res, err := session.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}

	var text strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	return text.String(), res.IsError
}

// seed registers a user with one server and one group.
func (e *testEnv) seed(t *testing.T, sub, alias, groupID, participantID, participantName string) *store.Group {
	t.Helper()
	ctx := context.Background()

	if _, err := e.store.UpsertUser(ctx, sub, "https://issuer", sub+"@example.com", participantName); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	group, err := e.store.CreateGroup(ctx, &store.Group{
		UserSub: sub, BaseURL: e.baseURL, SpliitGroupID: groupID, Alias: alias,
		ParticipantID: participantID, ParticipantName: participantName,
		GroupName: "Test Group", Currency: "SEK",
	})
	if err != nil {
		t.Fatalf("seed group: %v", err)
	}
	return group
}

// httpPost issues a bare POST with no Authorization header, to check that the
// bearer middleware rejects it before the MCP handler sees it.
func httpPost(url string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	return http.DefaultClient.Do(req)
}
