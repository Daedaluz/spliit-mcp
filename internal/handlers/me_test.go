package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daedaluz/spliit-mcp/internal/db"
	"github.com/daedaluz/spliit-mcp/internal/handlers"
	"github.com/daedaluz/spliit-mcp/internal/store"
)

// meEnv is a config API backed by a real SQLite store, signed in as alice,
// who has one group. Bob exists too, with a group of his own.
type meEnv struct {
	routes             http.Handler
	cookie             string
	aliceGroup, bobsID string
}

func newMeEnv(t *testing.T) *meEnv {
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

	env := &meEnv{}
	for _, sub := range []string{"alice", "bob"} {
		if _, err := st.UpsertUser(ctx, sub, "https://issuer", "", sub); err != nil {
			t.Fatalf("user: %v", err)
		}
		g, err := st.CreateGroup(ctx, &store.Group{
			UserSub: sub, BaseURL: "https://spliit.app/api/trpc", SpliitGroupID: "grp-" + sub,
			Alias: "g-" + sub, ParticipantID: "p", ParticipantName: sub,
		})
		if err != nil {
			t.Fatalf("group: %v", err)
		}
		if sub == "alice" {
			env.aliceGroup = g.ID
		} else {
			env.bobsID = g.ID
		}
	}
	sess, err := st.CreateSession(ctx, "alice", time.Hour)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	env.cookie = sess.ID
	env.routes = handlers.New(baseConfig(), st, nil, nil, nil, discardLogger()).Routes()
	return env
}

func (e *meEnv) do(t *testing.T, method, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, "/api/me", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "spliit_mcp_session", Value: e.cookie})
	rec := httptest.NewRecorder()
	e.routes.ServeHTTP(rec, req)

	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestUpdateMeDefaultGroup(t *testing.T) {
	env := newMeEnv(t)

	code, out := env.do(t, http.MethodPut, `{"default_group_id":"`+env.aliceGroup+`"}`)
	if code != http.StatusOK || out["default_group_id"] != env.aliceGroup {
		t.Fatalf("set default: %d %v", code, out)
	}
	// Setting only the default leaves the display name alone.
	if out["display_name"] != "alice" {
		t.Errorf("display_name = %v, want it unchanged", out["display_name"])
	}
	if _, out := env.do(t, http.MethodGet, ""); out["default_group_id"] != env.aliceGroup {
		t.Errorf("GET /api/me default_group_id = %v, want %s", out["default_group_id"], env.aliceGroup)
	}

	// Another user's group is not found, and the default is unchanged.
	if code, _ := env.do(t, http.MethodPut, `{"default_group_id":"`+env.bobsID+`"}`); code != http.StatusNotFound {
		t.Errorf("defaulting to bob's group: status %d, want 404", code)
	}

	// Updating the name alone leaves the default alone.
	code, out = env.do(t, http.MethodPut, `{"display_name":"Alice A."}`)
	if code != http.StatusOK || out["display_name"] != "Alice A." || out["default_group_id"] != env.aliceGroup {
		t.Errorf("rename: %d %v", code, out)
	}

	if code, out := env.do(t, http.MethodPut, `{"default_group_id":""}`); code != http.StatusOK || out["default_group_id"] != "" {
		t.Errorf("clear default: %d %v", code, out)
	}
	if code, _ := env.do(t, http.MethodPut, `{}`); code != http.StatusBadRequest {
		t.Errorf("empty update: status %d, want 400", code)
	}
}
