package mcp_test

import (
	"strings"
	"testing"
)

// sampleGroup is the canned groups.get payload used across these tests.
func sampleGroup() map[string]any {
	return map[string]any{
		"id":       "grp-1",
		"name":     "Trip",
		"currency": "SEK",
		"participants": []map[string]any{
			{"id": "p-me", "name": "Tobias"},
			{"id": "p-anna", "name": "Anna"},
			{"id": "p-erik", "name": "Erik"},
		},
	}
}

func TestListGroupsShowsOnlyYourOwn(t *testing.T) {
	env := setup(t)
	env.seed(t, "alice", "trip", "grp-1", "p-me", "Alice")
	env.seed(t, "bob", "bobs-trip", "grp-2", "p-bob", "Bob")

	text, isErr := call(t, env.connect(t, "alice"), "list_groups", map[string]any{})
	if isErr {
		t.Fatalf("list_groups errored: %s", text)
	}
	if !strings.Contains(text, "1 group") {
		t.Errorf("alice should see exactly 1 group, got: %s", text)
	}
}

// This is the authorization boundary. Spliit will serve any group to anyone who
// knows its ID, so the only thing stopping cross-user access is that we refuse
// to resolve a group the caller has not registered.
func TestToolsRefuseAnotherUsersGroup(t *testing.T) {
	env := setup(t)
	env.spliit.results["groups.get"] = map[string]any{"group": sampleGroup()}
	env.spliit.results["groups.getDetails"] = map[string]any{
		"group":                    sampleGroup(),
		"participantsWithExpenses": []string{},
	}

	alice := env.seed(t, "alice", "trip", "grp-1", "p-me", "Alice")
	env.seed(t, "bob", "bobs-trip", "grp-2", "p-bob", "Bob")

	bob := env.connect(t, "bob")

	// Every way of naming Alice's group must fail for Bob.
	for _, ref := range []string{"trip", alice.ID, "grp-1"} {
		text, isErr := call(t, bob, "get_group", map[string]any{"group": ref})
		if !isErr {
			t.Errorf("bob reached alice's group via %q: %s", ref, text)
			continue
		}
		if !strings.Contains(text, "no group") {
			t.Errorf("expected a not-available message for %q, got: %s", ref, text)
		}
	}

	// Writes must be refused too, not just reads.
	text, isErr := call(t, bob, "create_expense", map[string]any{
		"group": "grp-1", "title": "Sneaky", "amount": "10.00",
	})
	if !isErr {
		t.Errorf("bob created an expense in alice's group: %s", text)
	}
}

func TestCreateExpenseDefaultsToYouAndSplitsEvenly(t *testing.T) {
	env := setup(t)
	env.spliit.results["groups.get"] = map[string]any{"group": sampleGroup()}
	env.spliit.results["groups.expenses.create"] = map[string]any{"expenseId": "exp-1"}

	env.seed(t, "alice", "trip", "grp-1", "p-me", "Tobias")

	text, isErr := call(t, env.connect(t, "alice"), "create_expense", map[string]any{
		"group": "trip", "title": "Dinner", "amount": "450.50",
	})
	if isErr {
		t.Fatalf("create_expense errored: %s", text)
	}

	input := env.spliit.inputFor("groups.expenses.create")
	form, ok := input["expenseFormValues"].(map[string]any)
	if !ok {
		t.Fatalf("no expenseFormValues in %v", input)
	}

	// Amount must arrive as integer minor units, not a float.
	if got := form["amount"]; got != float64(45050) {
		t.Errorf("amount = %v, want 45050 (minor units)", got)
	}
	// Paid by defaults to the pinned participant.
	if got := form["paidBy"]; got != "p-me" {
		t.Errorf("paidBy = %v, want p-me (the participant pinned as you)", got)
	}
	// Shared by everyone, evenly.
	paidFor, ok := form["paidFor"].([]any)
	if !ok || len(paidFor) != 3 {
		t.Fatalf("paidFor = %v, want all 3 participants", form["paidFor"])
	}
	if got := form["splitMode"]; got != "EVENLY" {
		t.Errorf("splitMode = %v, want EVENLY", got)
	}

	if !strings.Contains(text, "Tobias") {
		t.Errorf("confirmation should name the payer, got: %s", text)
	}
}

func TestCreateExpenseRoundsToCents(t *testing.T) {
	env := setup(t)
	env.spliit.results["groups.get"] = map[string]any{"group": sampleGroup()}
	env.spliit.results["groups.expenses.create"] = map[string]any{"expenseId": "exp-1"}
	env.seed(t, "alice", "trip", "grp-1", "p-me", "Tobias")

	session := env.connect(t, "alice")

	// A one-decimal amount would be rejected outright by the upstream helper's
	// strict exponent check; our conversion must accept it.
	if text, isErr := call(t, session, "create_expense", map[string]any{
		"group": "trip", "title": "Coffee", "amount": "10.5",
	}); isErr {
		t.Fatalf("create_expense with 10.5 errored: %s", text)
	}

	form := env.spliit.inputFor("groups.expenses.create")["expenseFormValues"].(map[string]any)
	if got := form["amount"]; got != float64(1050) {
		t.Errorf("amount for 10.5 = %v, want 1050", got)
	}
}

func TestCreateExpenseNamesValidParticipantsOnError(t *testing.T) {
	env := setup(t)
	env.spliit.results["groups.get"] = map[string]any{"group": sampleGroup()}
	env.seed(t, "alice", "trip", "grp-1", "p-me", "Tobias")

	text, isErr := call(t, env.connect(t, "alice"), "create_expense", map[string]any{
		"group": "trip", "title": "Dinner", "amount": "10.00", "paid_by": "Nobody",
	})
	if !isErr {
		t.Fatalf("expected an error for an unknown participant, got: %s", text)
	}
	// The model needs the valid options to retry without another round trip.
	for _, name := range []string{"Tobias", "Anna", "Erik"} {
		if !strings.Contains(text, name) {
			t.Errorf("error should list participant %q, got: %s", name, text)
		}
	}
}

func TestCreateExpenseRejectsBadAmount(t *testing.T) {
	env := setup(t)
	env.spliit.results["groups.get"] = map[string]any{"group": sampleGroup()}
	env.seed(t, "alice", "trip", "grp-1", "p-me", "Tobias")

	session := env.connect(t, "alice")
	for _, amount := range []string{"", "abc", "0"} {
		text, isErr := call(t, session, "create_expense", map[string]any{
			"group": "trip", "title": "Dinner", "amount": amount,
		})
		if !isErr {
			t.Errorf("amount %q was accepted: %s", amount, text)
		}
	}
}

func TestGetBalancesFramesRelativeToYou(t *testing.T) {
	env := setup(t)
	env.spliit.results["groups.get"] = map[string]any{"group": sampleGroup()}
	env.spliit.results["groups.balances.list"] = map[string]any{
		"balances": map[string]any{
			// You are owed 100.00.
			"p-me":   map[string]any{"paid": 30000, "paidFor": 20000, "total": 10000},
			"p-anna": map[string]any{"paid": 0, "paidFor": 10000, "total": -10000},
		},
		"reimbursements": []map[string]any{
			{"from": "p-anna", "to": "p-me", "amount": 10000},
		},
	}
	env.seed(t, "alice", "trip", "grp-1", "p-me", "Tobias")

	text, isErr := call(t, env.connect(t, "alice"), "get_balances", map[string]any{"group": "trip"})
	if isErr {
		t.Fatalf("get_balances errored: %s", text)
	}
	if !strings.Contains(text, "You are owed 100.00") {
		t.Errorf("balance summary = %q, want it to say you are owed 100.00", text)
	}
	// Participant IDs must be translated to names for the model.
	if strings.Contains(text, "p-anna") {
		t.Errorf("raw participant IDs leaked into the summary: %s", text)
	}
}

func TestGroupWithoutPinnedParticipantGivesActionableError(t *testing.T) {
	env := setup(t)
	env.spliit.results["groups.get"] = map[string]any{"group": sampleGroup()}
	// Registered, but nobody pinned as "you".
	env.seed(t, "alice", "trip", "grp-1", "", "")

	text, isErr := call(t, env.connect(t, "alice"), "create_expense", map[string]any{
		"group": "trip", "title": "Dinner", "amount": "10.00",
	})
	if !isErr {
		t.Fatalf("expected an error when no participant is pinned, got: %s", text)
	}
	// The error has to name the fix, or the model has nothing to act on.
	if !strings.Contains(text, "set_active_participant") {
		t.Errorf("error should name the tool that fixes it, got: %s", text)
	}
}

func TestPinnedParticipantRemovedUpstream(t *testing.T) {
	env := setup(t)
	// The pinned ID is no longer among the group's participants, which is what
	// happens when a participant is removed and re-added in Spliit.
	env.spliit.results["groups.get"] = map[string]any{"group": sampleGroup()}
	env.seed(t, "alice", "trip", "grp-1", "p-stale", "Tobias")

	text, isErr := call(t, env.connect(t, "alice"), "create_expense", map[string]any{
		"group": "trip", "title": "Dinner", "amount": "10.00",
	})
	if !isErr {
		t.Fatalf("expected an error for a stale participant, got: %s", text)
	}
	if !strings.Contains(text, "config page") {
		t.Errorf("error should tell the user to re-pick, got: %s", text)
	}
}

func TestUnknownGroupSuggestsListGroups(t *testing.T) {
	env := setup(t)
	env.seed(t, "alice", "trip", "grp-1", "p-me", "Tobias")

	text, isErr := call(t, env.connect(t, "alice"), "get_group", map[string]any{"group": "nope"})
	if !isErr {
		t.Fatalf("expected an error for an unknown group, got: %s", text)
	}
	if !strings.Contains(text, "list_groups") {
		t.Errorf("error should point at list_groups, got: %s", text)
	}
}

func TestUpdateExpenseKeepsUnspecifiedFields(t *testing.T) {
	env := setup(t)
	env.spliit.results["groups.get"] = map[string]any{"group": sampleGroup()}
	env.spliit.results["groups.expenses.get"] = map[string]any{
		"expense": map[string]any{ //nolint:dupl // fixture
			"id": "exp-1", "title": "Dinner", "amount": 45050,
			"expenseDate": "2026-01-15T00:00:00Z",
			"categoryId":  7,
			"paidById":    "p-anna",
			"paidBy":      map[string]any{"id": "p-anna", "name": "Anna"},
			"paidFor": []map[string]any{
				{"participantId": "p-anna", "shares": 1,
					"participant": map[string]any{"id": "p-anna", "name": "Anna"}},
			},
			"splitMode": "EVENLY",
		},
	}
	env.spliit.results["groups.expenses.update"] = map[string]any{"expenseId": "exp-1"}
	env.seed(t, "alice", "trip", "grp-1", "p-me", "Tobias")

	// Change only the amount.
	text, isErr := call(t, env.connect(t, "alice"), "update_expense", map[string]any{
		"group": "trip", "expense_id": "exp-1", "amount": "500.00",
	})
	if isErr {
		t.Fatalf("update_expense errored: %s", text)
	}

	form := env.spliit.inputFor("groups.expenses.update")["expenseFormValues"].(map[string]any)
	if got := form["amount"]; got != float64(50000) {
		t.Errorf("amount = %v, want 50000", got)
	}
	// Everything unspecified must survive the round trip.
	if got := form["title"]; got != "Dinner" {
		t.Errorf("title = %v, want the existing Dinner", got)
	}
	if got := form["paidBy"]; got != "p-anna" {
		t.Errorf("paidBy = %v, want the existing p-anna, not defaulted to you", got)
	}
	if got := form["category"]; got != float64(7) {
		t.Errorf("category = %v, want the existing 7", got)
	}
}

func TestMissingBearerTokenIsRejected(t *testing.T) {
	env := setup(t)

	// A request with no Authorization header must not reach the MCP handler.
	resp, err := httpPost(env.url)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

// The SDK only serializes structured output into text when Content is empty, so
// setting a summary suppresses it. Every tool sets one, which is how list_groups
// came back as "1 group(s) available" with no groups in it. Clients that read
// only text content must still receive the data.
func TestResultsCarryTheDataInTextContent(t *testing.T) {
	env := setup(t)
	env.spliit.results["groups.get"] = map[string]any{"group": sampleGroup()}
	env.seed(t, "alice", "trip", "grp-1", "p-me", "Tobias")

	text, isErr := call(t, env.connect(t, "alice"), "list_groups", map[string]any{})
	if isErr {
		t.Fatalf("list_groups errored: %s", text)
	}

	// The summary alone must name the group, not just count it.
	for _, want := range []string{"trip", "Test Group", "Tobias"} {
		if !strings.Contains(text, want) {
			t.Errorf("summary is missing %q: %s", want, text)
		}
	}

	// And the machine-readable payload must be there too.
	if !strings.Contains(text, `"alias":"trip"`) {
		t.Errorf("text content carries no JSON payload: %s", text)
	}
	if !strings.Contains(text, `"spliit_group_id":"grp-1"`) {
		t.Errorf("JSON payload is incomplete: %s", text)
	}
}

// The same applies to every other tool, not just list_groups.
func TestGetBalancesCarriesItsPayload(t *testing.T) {
	env := setup(t)
	env.spliit.results["groups.get"] = map[string]any{"group": sampleGroup()}
	env.spliit.results["groups.balances.list"] = map[string]any{
		"balances": map[string]any{
			"p-me": map[string]any{"paid": 30000, "paidFor": 20000, "total": 10000},
		},
		"reimbursements": []map[string]any{},
	}
	env.seed(t, "alice", "trip", "grp-1", "p-me", "Tobias")

	text, isErr := call(t, env.connect(t, "alice"), "get_balances", map[string]any{"group": "trip"})
	if isErr {
		t.Fatalf("get_balances errored: %s", text)
	}
	if !strings.Contains(text, `"your_net":"100.00"`) {
		t.Errorf("balances payload missing from text content: %s", text)
	}
}
