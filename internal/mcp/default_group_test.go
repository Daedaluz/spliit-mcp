package mcp_test

import (
	"strings"
	"testing"
)

func TestCreateExpenseUsesDefaultGroup(t *testing.T) {
	env := setup(t)
	env.spliit.results["groups.get"] = map[string]any{"group": sampleGroup()}
	env.spliit.results["groups.expenses.create"] = map[string]any{"expenseId": "exp-1"}
	env.seed(t, "alice", "trip", "grp-1", "p-me", "Tobias")
	session := env.connect(t, "alice")

	// Without a default, an unnamed group is an error that names the way out.
	text, isErr := call(t, session, "create_expense", map[string]any{"title": "Pizza", "amount": "120"})
	if !isErr || !strings.Contains(text, "set_default_group") {
		t.Fatalf("create_expense with no group and no default: isErr=%v text=%q", isErr, text)
	}

	if text, isErr := call(t, session, "set_default_group", map[string]any{"group": "trip"}); isErr {
		t.Fatalf("set_default_group: %s", text)
	}
	if text, isErr := call(t, session, "create_expense", map[string]any{"title": "Pizza", "amount": "120"}); isErr {
		t.Fatalf("create_expense on the default group: %s", text)
	}
	if got := env.spliit.inputFor("groups.expenses.create")["groupId"]; got != "grp-1" {
		t.Errorf("expense went to group %v, want grp-1", got)
	}

	text, _ = call(t, session, "list_groups", map[string]any{})
	if !strings.Contains(text, "trip") || !strings.Contains(text, "(default)") {
		t.Errorf("list_groups should mark the default, got: %s", text)
	}

	if text, isErr := call(t, session, "set_default_group", map[string]any{"clear": true}); isErr {
		t.Fatalf("clearing the default: %s", text)
	}
	if _, isErr := call(t, session, "create_expense", map[string]any{"title": "Pizza", "amount": "120"}); !isErr {
		t.Error("create_expense with no group should fail once the default is cleared")
	}
}

func TestDefaultGroupIsScopedToUser(t *testing.T) {
	env := setup(t)
	env.seed(t, "alice", "trip", "grp-1", "p-me", "Tobias")
	env.seed(t, "bob", "home", "grp-2", "p-bob", "Bob")

	// Bob cannot point his default at Alice's group by alias or ID.
	for _, ref := range []string{"trip", "grp-1"} {
		if _, isErr := call(t, env.connect(t, "bob"), "set_default_group", map[string]any{"group": ref}); !isErr {
			t.Errorf("bob set alice's group %q as his default", ref)
		}
	}
}

func TestMembershipToolsNeverFallBackToDefault(t *testing.T) {
	env := setup(t)
	env.spliit.results["groups.get"] = map[string]any{"group": sampleGroup()}
	env.seed(t, "alice", "trip", "grp-1", "p-me", "Tobias")
	session := env.connect(t, "alice")
	if text, isErr := call(t, session, "set_default_group", map[string]any{"group": "trip"}); isErr {
		t.Fatalf("set_default_group: %s", text)
	}

	for name, args := range map[string]map[string]any{
		"leave_group":            {},
		"set_active_participant": {"you": "Tobias"},
		"update_group":           {"name": "Renamed"},
	} {
		if text, isErr := call(t, session, name, args); !isErr || !strings.Contains(text, "group") {
			t.Errorf("%s without group: isErr=%v text=%q, want it to require a group", name, isErr, text)
		}
	}
	text, _ := call(t, session, "list_groups", map[string]any{})
	if !strings.Contains(text, "trip") {
		t.Errorf("leave_group without a group must not leave the default; list_groups: %s", text)
	}
}
