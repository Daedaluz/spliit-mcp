package mcp_test

import (
	"context"
	"strings"
	"testing"
)

// seedUserOnly registers a user and a server but no groups, which is the state
// a fresh account is in before joining anything.
func (e *testEnv) seedUserOnly(t *testing.T, sub, displayName string) {
	t.Helper()
	ctx := context.Background()

	if _, err := e.store.UpsertUser(ctx, sub, "https://issuer", sub+"@example.com", displayName); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := e.store.CreateServer(ctx, sub, "test", e.baseURL); err != nil {
		t.Fatalf("seed server: %v", err)
	}
}

func TestInspectGroupListsParticipantsWithoutJoining(t *testing.T) {
	env := setup(t)
	env.spliit.results["groups.get"] = map[string]any{"group": sampleGroup()}
	env.seedUserOnly(t, "alice", "Tobias")

	session := env.connect(t, "alice")

	text, isErr := call(t, session, "inspect_group", map[string]any{"group_id": "grp-1"})
	if isErr {
		t.Fatalf("inspect_group errored: %s", text)
	}
	for _, name := range []string{"Tobias", "Anna", "Erik"} {
		if !strings.Contains(text, name) {
			t.Errorf("inspect_group should list %q, got: %s", name, text)
		}
	}

	// Inspecting must not join: the group is still unavailable to other tools.
	if _, isErr := call(t, session, "get_group", map[string]any{"group": "grp-1"}); !isErr {
		t.Error("inspect_group joined the group as a side effect")
	}
}

func TestInspectGroupAcceptsAFullURL(t *testing.T) {
	env := setup(t)
	env.spliit.results["groups.get"] = map[string]any{"group": sampleGroup()}
	env.seedUserOnly(t, "alice", "Tobias")

	text, isErr := call(t, env.connect(t, "alice"), "inspect_group", map[string]any{
		"group_id": "https://spliit.app/groups/grp-1/expenses",
	})
	if isErr {
		t.Fatalf("inspect_group with a URL errored: %s", text)
	}

	// The ID must have been extracted from the URL before hitting Spliit.
	if got := env.spliit.inputFor("groups.get")["groupId"]; got != "grp-1" {
		t.Errorf("groupId sent upstream = %v, want grp-1", got)
	}
}

func TestJoinGroupRequiresNamingYourself(t *testing.T) {
	env := setup(t)
	env.spliit.results["groups.get"] = map[string]any{"group": sampleGroup()}
	env.seedUserOnly(t, "alice", "Tobias")

	session := env.connect(t, "alice")

	// Empty "you" must be refused rather than silently joining without an
	// identity, which would leave a group no write tool could use.
	text, isErr := call(t, session, "join_group", map[string]any{
		"group_id": "grp-1", "you": "",
	})
	if !isErr {
		t.Fatalf("join_group without an identity was accepted: %s", text)
	}
	if !strings.Contains(text, "inspect_group") {
		t.Errorf("error should point at inspect_group, got: %s", text)
	}

	// A name that is not in the group must be refused, and list the real ones.
	text, isErr = call(t, session, "join_group", map[string]any{
		"group_id": "grp-1", "you": "Nobody",
	})
	if !isErr {
		t.Fatalf("join_group with an unknown participant was accepted: %s", text)
	}
	for _, name := range []string{"Tobias", "Anna", "Erik"} {
		if !strings.Contains(text, name) {
			t.Errorf("error should list %q, got: %s", name, text)
		}
	}
}

func TestJoinGroupPinsTheActiveParticipant(t *testing.T) {
	env := setup(t)
	env.spliit.results["groups.get"] = map[string]any{"group": sampleGroup()}
	env.spliit.results["groups.expenses.create"] = map[string]any{"expenseId": "exp-1"}
	env.seedUserOnly(t, "alice", "Tobias")

	session := env.connect(t, "alice")

	text, isErr := call(t, session, "join_group", map[string]any{
		"group_id": "grp-1", "you": "Anna", "alias": "trip",
	})
	if isErr {
		t.Fatalf("join_group errored: %s", text)
	}
	if !strings.Contains(text, "Anna") {
		t.Errorf("confirmation should name who you joined as, got: %s", text)
	}

	// The group is now usable, and "you" defaults to the participant chosen at
	// join time rather than the one matching the display name.
	if text, isErr := call(t, session, "create_expense", map[string]any{
		"group": "trip", "title": "Dinner", "amount": "100.00",
	}); isErr {
		t.Fatalf("create_expense after join errored: %s", text)
	}

	form := env.spliit.inputFor("groups.expenses.create")["expenseFormValues"].(map[string]any)
	if got := form["paidBy"]; got != "p-anna" {
		t.Errorf("paidBy = %v, want p-anna (the participant chosen at join)", got)
	}
}

func TestJoinGroupRefusesDuplicates(t *testing.T) {
	env := setup(t)
	env.spliit.results["groups.get"] = map[string]any{"group": sampleGroup()}
	env.seedUserOnly(t, "alice", "Tobias")

	session := env.connect(t, "alice")
	args := map[string]any{"group_id": "grp-1", "you": "Tobias", "alias": "trip"}

	if text, isErr := call(t, session, "join_group", args); isErr {
		t.Fatalf("first join errored: %s", text)
	}
	if text, isErr := call(t, session, "join_group", args); !isErr {
		t.Errorf("joining twice was accepted: %s", text)
	}
}

func TestLeaveGroupUnlinksLocallyOnly(t *testing.T) {
	env := setup(t)
	env.spliit.results["groups.get"] = map[string]any{"group": sampleGroup()}
	env.seed(t, "alice", "trip", "grp-1", "p-me", "Tobias")

	session := env.connect(t, "alice")

	text, isErr := call(t, session, "leave_group", map[string]any{"group": "trip"})
	if isErr {
		t.Fatalf("leave_group errored: %s", text)
	}
	// The group ID is the only way back, so it must appear in the confirmation.
	if !strings.Contains(text, "grp-1") {
		t.Errorf("confirmation should include the group ID to rejoin with, got: %s", text)
	}

	if _, isErr := call(t, session, "get_group", map[string]any{"group": "trip"}); !isErr {
		t.Error("group is still reachable after leave_group")
	}

	// Nothing may have been deleted upstream.
	for _, c := range env.spliit.endpoints() {
		if strings.Contains(c, "delete") {
			t.Errorf("leave_group called %s upstream; it must only unlink locally", c)
		}
	}
}

func TestSetActiveParticipant(t *testing.T) {
	env := setup(t)
	env.spliit.results["groups.get"] = map[string]any{"group": sampleGroup()}
	env.spliit.results["groups.expenses.create"] = map[string]any{"expenseId": "exp-1"}
	env.seed(t, "alice", "trip", "grp-1", "p-me", "Tobias")

	session := env.connect(t, "alice")

	text, isErr := call(t, session, "set_active_participant", map[string]any{
		"group": "trip", "you": "Erik",
	})
	if isErr {
		t.Fatalf("set_active_participant errored: %s", text)
	}

	if text, isErr := call(t, session, "create_expense", map[string]any{
		"group": "trip", "title": "Dinner", "amount": "100.00",
	}); isErr {
		t.Fatalf("create_expense errored: %s", text)
	}
	form := env.spliit.inputFor("groups.expenses.create")["expenseFormValues"].(map[string]any)
	if got := form["paidBy"]; got != "p-erik" {
		t.Errorf("paidBy = %v, want p-erik after re-pinning", got)
	}
}

// A stale pin is the realistic failure: Spliit mints a new participant ID when
// one is removed and re-added, and set_active_participant is the fix.
func TestSetActiveParticipantRepairsAStalePin(t *testing.T) {
	env := setup(t)
	env.spliit.results["groups.get"] = map[string]any{"group": sampleGroup()}
	env.spliit.results["groups.expenses.create"] = map[string]any{"expenseId": "exp-1"}
	env.seed(t, "alice", "trip", "grp-1", "p-gone", "Tobias")

	session := env.connect(t, "alice")

	if _, isErr := call(t, session, "create_expense", map[string]any{
		"group": "trip", "title": "Dinner", "amount": "10.00",
	}); !isErr {
		t.Fatal("expected a stale-pin error before repair")
	}

	if text, isErr := call(t, session, "set_active_participant", map[string]any{
		"group": "trip", "you": "Tobias",
	}); isErr {
		t.Fatalf("set_active_participant errored: %s", text)
	}

	if text, isErr := call(t, session, "create_expense", map[string]any{
		"group": "trip", "title": "Dinner", "amount": "10.00",
	}); isErr {
		t.Errorf("create_expense still failing after repair: %s", text)
	}
}

func TestJoinGroupIsScopedToTheCaller(t *testing.T) {
	env := setup(t)
	env.spliit.results["groups.get"] = map[string]any{"group": sampleGroup()}
	env.seedUserOnly(t, "alice", "Tobias")
	env.seedUserOnly(t, "bob", "Bob")

	// Alice joins.
	if text, isErr := call(t, env.connect(t, "alice"), "join_group", map[string]any{
		"group_id": "grp-1", "you": "Tobias", "alias": "trip",
	}); isErr {
		t.Fatalf("alice join errored: %s", text)
	}

	// Bob must not inherit it, and must not be able to remove it.
	bob := env.connect(t, "bob")
	if text, isErr := call(t, bob, "leave_group", map[string]any{"group": "trip"}); !isErr {
		t.Errorf("bob removed alice's group: %s", text)
	}

	text, isErr := call(t, bob, "list_groups", map[string]any{})
	if isErr {
		t.Fatalf("list_groups errored: %s", text)
	}
	if !strings.Contains(text, "no groups") {
		t.Errorf("bob should see no groups, got: %s", text)
	}
}

func TestGetServerInfoReturnsConnectableURLs(t *testing.T) {
	env := setup(t)
	env.seedUserOnly(t, "alice", "Tobias")

	text, isErr := call(t, env.connect(t, "alice"), "get_server_info", map[string]any{})
	if isErr {
		t.Fatalf("get_server_info errored: %s", text)
	}

	// The endpoint must be the configured public URL, not the listen address:
	// it is what another machine has to dial.
	if !strings.Contains(text, "http://localhost:8080/mcp") {
		t.Errorf("should report the MCP endpoint, got: %s", text)
	}
	if !strings.Contains(text, "claude mcp add") {
		t.Errorf("should include a ready-to-paste command, got: %s", text)
	}
}

func TestGetServerInfoRequiresIdentity(t *testing.T) {
	env := setup(t)

	// These URLs describe a server whose purpose is guarding group IDs, so even
	// this much is gated on a verified caller.
	resp, err := httpPost(env.url)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
