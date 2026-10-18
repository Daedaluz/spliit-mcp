package mcp_test

import (
	"slices"
	"strings"
	"testing"
)

// dupEnv has one existing expense: "Pizza", 120.00 SEK paid by Anna on
// 2026-02-01.
func dupEnv(t *testing.T) *testEnv {
	t.Helper()
	env := setup(t)
	env.spliit.results["groups.get"] = map[string]any{"group": sampleGroup()}
	env.spliit.results["groups.expenses.create"] = map[string]any{"expenseId": "exp-new"}
	env.spliit.results["groups.expenses.list"] = expensesPage([]map[string]any{
		expense("e-pizza", "Pizza", "p-anna", "Anna", 12000, "p-me", "p-anna"),
	}, false, 0)
	env.seed(t, "alice", "trip", "grp-1", "p-me", "Tobias")
	return env
}

func created(env *testEnv) bool {
	return slices.Contains(env.spliit.endpoints(), "groups.expenses.create")
}

func TestCreateExpenseStopsOnLikelyDuplicate(t *testing.T) {
	env := dupEnv(t)

	// Same amount a day later, under a different title: still the same pizza.
	text, isErr := call(t, env.connect(t, "alice"), "create_expense", map[string]any{
		"group": "trip", "title": "pizza dinner", "amount": "120", "date": "2026-02-02",
	})
	if !isErr {
		t.Fatalf("a likely duplicate was recorded: %s", text)
	}
	if created(env) {
		t.Fatal("groups.expenses.create was called for a likely duplicate")
	}
	for _, want := range []string{"Pizza", "120.00", "2026-02-01", "Anna", "e-pizza", "Ask the user", "allow_duplicate=true"} {
		if !strings.Contains(text, want) {
			t.Errorf("duplicate report should mention %q, got: %s", want, text)
		}
	}
}

func TestCreateExpenseAllowDuplicateRecords(t *testing.T) {
	env := dupEnv(t)

	text, isErr := call(t, env.connect(t, "alice"), "create_expense", map[string]any{
		"group": "trip", "title": "Pizza", "amount": "120", "date": "2026-02-01", "allow_duplicate": true,
	})
	if isErr || !created(env) {
		t.Fatalf("allow_duplicate=true should record it: isErr=%v text=%s", isErr, text)
	}
}

func TestCreateExpenseNotADuplicate(t *testing.T) {
	for name, args := range map[string]map[string]any{
		"different amount": {"amount": "121", "date": "2026-02-01"},
		"outside window":   {"amount": "120", "date": "2026-02-10"},
	} {
		t.Run(name, func(t *testing.T) {
			env := dupEnv(t)
			args["group"], args["title"] = "trip", "Pizza"
			if text, isErr := call(t, env.connect(t, "alice"), "create_expense", args); isErr || !created(env) {
				t.Fatalf("should be recorded: isErr=%v text=%s", isErr, text)
			}
		})
	}
}

// A failing lookup must not block recording (that would take create_expense
// down whenever listing breaks), but the result has to say the check was skipped.
func TestCreateExpenseRecordsWhenDuplicateCheckFails(t *testing.T) {
	env := dupEnv(t)
	delete(env.spliit.results, "groups.expenses.list")

	text, isErr := call(t, env.connect(t, "alice"), "create_expense", map[string]any{
		"group": "trip", "title": "Pizza", "amount": "120", "date": "2026-02-01",
	})
	if isErr || !created(env) {
		t.Fatalf("should be recorded despite the failed check: isErr=%v text=%s", isErr, text)
	}
	if !strings.Contains(text, "Could not check for duplicates") {
		t.Errorf("result should say the duplicate check was skipped: %s", text)
	}
}
