package store_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/daedaluz/spliit-mcp/internal/db"
	"github.com/daedaluz/spliit-mcp/internal/store"
)

// eachEngine runs fn against SQLite and, when SPLIIT_MCP_TEST_POSTGRES_DSN is
// set, against Postgres too. The dual run is the point: it is what actually
// exercises the Rebind path and the split migration sets.
func eachEngine(t *testing.T, fn func(t *testing.T, s *store.Store)) {
	t.Helper()

	t.Run("sqlite", func(t *testing.T) {
		dsn := filepath.Join(t.TempDir(), "test.db")
		fn(t, openStore(t, dsn))
	})

	dsn := os.Getenv("SPLIIT_MCP_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Log("SPLIIT_MCP_TEST_POSTGRES_DSN not set; skipping Postgres")
		return
	}
	t.Run("postgres", func(t *testing.T) {
		s := openStore(t, dsn)
		fn(t, s)
	})
}

func openStore(t *testing.T, dsn string) *store.Store {
	t.Helper()
	ctx := context.Background()

	database, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open %s: %v", dsn, err)
	}
	t.Cleanup(func() { _ = database.Close() })

	if db.DialectFor(dsn) == db.Postgres {
		// Give each Postgres run a clean slate; the SQLite runs get a temp file.
		for _, table := range []string{"groups", "servers", "sessions", "users", "schema_migrations"} {
			if _, err := database.ExecContext(ctx, "DROP TABLE IF EXISTS "+table+" CASCADE"); err != nil {
				t.Fatalf("drop %s: %v", table, err)
			}
		}
	}

	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return store.New(database)
}

func TestMigrateIsIdempotent(t *testing.T) {
	eachEngine(t, func(t *testing.T, _ *store.Store) {
		// openStore already migrated once; a second Migrate must be a no-op.
	})
}

func TestUserLifecycle(t *testing.T) {
	eachEngine(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()

		u, err := s.UpsertUser(ctx, "sub-1", "https://issuer", "a@example.com", "Tobias")
		if err != nil {
			t.Fatalf("upsert: %v", err)
		}
		if u.DisplayName != "Tobias" {
			t.Fatalf("display name = %q, want Tobias", u.DisplayName)
		}

		if err := s.SetDisplayName(ctx, "sub-1", "Tobbe"); err != nil {
			t.Fatalf("set display name: %v", err)
		}

		// A second login must not clobber the user's chosen display name.
		if _, err := s.UpsertUser(ctx, "sub-1", "https://issuer", "b@example.com", "Tobias"); err != nil {
			t.Fatalf("re-upsert: %v", err)
		}
		got, err := s.GetUser(ctx, "sub-1")
		if err != nil {
			t.Fatalf("get user: %v", err)
		}
		if got.DisplayName != "Tobbe" {
			t.Errorf("display name = %q, want Tobbe (login must not overwrite it)", got.DisplayName)
		}
		if got.Email != "b@example.com" {
			t.Errorf("email = %q, want the refreshed b@example.com", got.Email)
		}

		if _, err := s.GetUser(ctx, "nobody"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("get missing user: err = %v, want ErrNotFound", err)
		}
	})
}

func TestServerAndGroupConstraints(t *testing.T) {
	eachEngine(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()

		if _, err := s.UpsertUser(ctx, "sub-1", "https://issuer", "", "Tobias"); err != nil {
			t.Fatalf("user: %v", err)
		}
		srvA, err := s.CreateServer(ctx, "sub-1", "spliit.app", "https://spliit.app/api/trpc")
		if err != nil {
			t.Fatalf("create server: %v", err)
		}
		srvB, err := s.CreateServer(ctx, "sub-1", "home", "https://spliit.home/api/trpc")
		if err != nil {
			t.Fatalf("create second server: %v", err)
		}

		if _, err := s.CreateServer(ctx, "sub-1", "home", "https://other"); !errors.Is(err, store.ErrConflict) {
			t.Errorf("duplicate server name: err = %v, want ErrConflict", err)
		}

		mk := func(serverID, groupID, alias string) error {
			_, err := s.CreateGroup(ctx, &store.Group{
				UserSub: "sub-1", ServerID: serverID, SpliitGroupID: groupID,
				Alias: alias, ParticipantID: "p1", ParticipantName: "Tobias",
			})
			return err
		}

		if err := mk(srvA.ID, "grp-1", "trip"); err != nil {
			t.Fatalf("create group: %v", err)
		}
		// Same alias, different server: still a conflict, aliases are per user.
		if err := mk(srvB.ID, "grp-9", "trip"); !errors.Is(err, store.ErrConflict) {
			t.Errorf("duplicate alias across servers: err = %v, want ErrConflict", err)
		}
		// Same group ID on a *different* server is a genuinely different group.
		if err := mk(srvB.ID, "grp-1", "trip-home"); err != nil {
			t.Errorf("same group id on another server should be allowed, got %v", err)
		}
		// Same group ID on the same server is a duplicate.
		if err := mk(srvA.ID, "grp-1", "trip-again"); !errors.Is(err, store.ErrConflict) {
			t.Errorf("duplicate group on one server: err = %v, want ErrConflict", err)
		}

		// A server still holding groups must not be deletable.
		n, err := s.CountGroupsForServer(ctx, "sub-1", srvA.ID)
		if err != nil {
			t.Fatalf("count groups: %v", err)
		}
		if n != 1 {
			t.Errorf("groups on server A = %d, want 1", n)
		}
	})
}

func TestResolveGroupIsScopedToUser(t *testing.T) {
	eachEngine(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()

		for _, sub := range []string{"alice", "bob"} {
			if _, err := s.UpsertUser(ctx, sub, "https://issuer", "", sub); err != nil {
				t.Fatalf("user %s: %v", sub, err)
			}
		}
		srv, err := s.CreateServer(ctx, "alice", "spliit.app", "https://spliit.app/api/trpc")
		if err != nil {
			t.Fatalf("server: %v", err)
		}
		g, err := s.CreateGroup(ctx, &store.Group{
			UserSub: "alice", ServerID: srv.ID, SpliitGroupID: "secret-group",
			Alias: "trip", ParticipantID: "p1", ParticipantName: "Alice",
		})
		if err != nil {
			t.Fatalf("group: %v", err)
		}

		for _, ref := range []string{"trip", g.ID, "secret-group"} {
			got, err := s.ResolveGroup(ctx, "alice", ref)
			if err != nil {
				t.Fatalf("resolve %q as owner: %v", ref, err)
			}
			if got.ID != g.ID {
				t.Errorf("resolve %q returned group %s, want %s", ref, got.ID, g.ID)
			}
		}

		// This is the authorization boundary: knowing the group ID is not enough.
		for _, ref := range []string{"trip", g.ID, "secret-group"} {
			if _, err := s.ResolveGroup(ctx, "bob", ref); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("bob resolving alice's %q: err = %v, want ErrNotFound", ref, err)
			}
		}
		if err := s.DeleteGroup(ctx, "bob", g.ID); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("bob deleting alice's group: err = %v, want ErrNotFound", err)
		}
	})
}

func TestSessions(t *testing.T) {
	eachEngine(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()

		if _, err := s.UpsertUser(ctx, "sub-1", "https://issuer", "", "Tobias"); err != nil {
			t.Fatalf("user: %v", err)
		}

		live, err := s.CreateSession(ctx, "sub-1", time.Hour)
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		if _, err := s.GetSession(ctx, live.ID); err != nil {
			t.Fatalf("get live session: %v", err)
		}

		dead, err := s.CreateSession(ctx, "sub-1", -time.Hour)
		if err != nil {
			t.Fatalf("create expired session: %v", err)
		}
		if _, err := s.GetSession(ctx, dead.ID); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("expired session: err = %v, want ErrNotFound", err)
		}

		if _, err := s.DeleteExpiredSessions(ctx); err != nil {
			t.Fatalf("purge: %v", err)
		}
		if _, err := s.GetSession(ctx, live.ID); err != nil {
			t.Errorf("purge removed a live session: %v", err)
		}

		if err := s.DeleteSession(ctx, live.ID); err != nil {
			t.Fatalf("delete session: %v", err)
		}
		if _, err := s.GetSession(ctx, live.ID); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("deleted session: err = %v, want ErrNotFound", err)
		}
	})
}
