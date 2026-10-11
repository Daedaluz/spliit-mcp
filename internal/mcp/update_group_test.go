package mcp_test

import (
	"context"
	"strings"
	"testing"
)

// sampleDetails is the canned groups.getDetails payload: Anna has expenses,
// Erik does not.
func sampleDetails() map[string]any {
	return map[string]any{
		"group":                    sampleGroup(),
		"participantsWithExpenses": []string{"p-me", "p-anna"},
	}
}

// Spliit keys the participant list by ID on update, so a rename must carry the
// existing ID through and an addition must arrive without one. Getting this
// wrong would quietly re-create a participant and orphan their expenses.
func TestUpdateGroupMergesParticipantEdits(t *testing.T) {
	env := setup(t)
	env.spliit.results["groups.getDetails"] = sampleDetails()
	env.spliit.results["groups.update"] = "grp-1"
	after := sampleGroup()
	after["participants"] = []map[string]any{
		{"id": "p-me", "name": "Tobias"},
		{"id": "p-anna", "name": "Anna B"},
		{"id": "p-lisa", "name": "Lisa"},
	}
	env.spliit.results["groups.get"] = map[string]any{"group": after}
	env.seed(t, "alice", "trip", "grp-1", "p-me", "Tobias")

	text, isErr := call(t, env.connect(t, "alice"), "update_group", map[string]any{
		"group":               "trip",
		"add_participants":    []string{"Lisa"},
		"remove_participants": []string{"Erik"},
		"rename_participants": []map[string]string{{"from": "anna", "to": "Anna B"}},
	})
	if isErr {
		t.Fatalf("update_group errored: %s", text)
	}

	input := env.spliit.inputFor("groups.update")
	if input["groupId"] != "grp-1" {
		t.Errorf("wrong group sent upstream: %v", input["groupId"])
	}
	form := input["groupFormValues"].(map[string]any)
	if form["name"] != "Trip" || form["currency"] != "SEK" {
		t.Errorf("untouched name/currency should be preserved, got %v / %v", form["name"], form["currency"])
	}
	parts := form["participants"].([]any)
	got := map[string]any{}
	for _, p := range parts {
		m := p.(map[string]any)
		got[m["name"].(string)] = m["id"]
	}
	if len(parts) != 3 {
		t.Fatalf("expected 3 participants upstream, got %v", got)
	}
	if got["Tobias"] != "p-me" {
		t.Errorf("Tobias should keep ID p-me, got %v", got["Tobias"])
	}
	if got["Anna B"] != "p-anna" {
		t.Errorf("renamed Anna should keep ID p-anna, got %v", got["Anna B"])
	}
	if id, ok := got["Lisa"]; !ok || id != nil {
		t.Errorf("Lisa should be sent without an ID, got %v (present=%v)", id, ok)
	}
	if _, ok := got["Erik"]; ok {
		t.Error("Erik should have been removed")
	}
	for _, want := range []string{"added Lisa", "removed Erik", "Anna renamed to Anna B"} {
		if !strings.Contains(text, want) {
			t.Errorf("summary should mention %q, got: %s", want, text)
		}
	}
}

func TestUpdateGroupRefusesRemovingParticipantsWithExpenses(t *testing.T) {
	env := setup(t)
	env.spliit.results["groups.getDetails"] = sampleDetails()
	env.seed(t, "alice", "trip", "grp-1", "p-me", "Tobias")
	session := env.connect(t, "alice")

	text, isErr := call(t, session, "update_group", map[string]any{
		"group": "trip", "remove_participants": []string{"Anna"},
	})
	if !isErr || !strings.Contains(text, "has expenses") {
		t.Errorf("expected a has-expenses refusal, got (err=%v): %s", isErr, text)
	}

	// Removing yourself would leave the group unwritable through these tools.
	text, isErr = call(t, session, "update_group", map[string]any{
		"group": "trip", "remove_participants": []string{"Tobias"},
	})
	if !isErr || !strings.Contains(text, "set_active_participant") {
		t.Errorf("expected a you-are-this-participant refusal, got (err=%v): %s", isErr, text)
	}

	// An unknown name names the valid options.
	text, isErr = call(t, session, "update_group", map[string]any{
		"group": "trip", "rename_participants": []map[string]string{{"from": "Nobody", "to": "X"}},
	})
	if !isErr || !strings.Contains(text, "Anna") {
		t.Errorf("expected the participant list in the error, got (err=%v): %s", isErr, text)
	}

	for _, ep := range env.spliit.endpoints() {
		if ep == "groups.update" {
			t.Fatal("a refused edit must not reach Spliit")
		}
	}
}

// Renaming the participant pinned as "you" must keep the stored copy of the
// name in step, or list_groups would keep showing the old one.
func TestUpdateGroupRenamingYouUpdatesTheStoredName(t *testing.T) {
	env := setup(t)
	env.spliit.results["groups.getDetails"] = sampleDetails()
	env.spliit.results["groups.update"] = "grp-1"
	after := sampleGroup()
	after["name"] = "Road trip"
	after["participants"] = []map[string]any{
		{"id": "p-me", "name": "Tobbe"},
		{"id": "p-anna", "name": "Anna"},
		{"id": "p-erik", "name": "Erik"},
	}
	env.spliit.results["groups.get"] = map[string]any{"group": after}
	stored := env.seed(t, "alice", "trip", "grp-1", "p-me", "Tobias")

	text, isErr := call(t, env.connect(t, "alice"), "update_group", map[string]any{
		"group": "trip", "name": "Road trip",
		"rename_participants": []map[string]string{{"from": "Tobias", "to": "Tobbe"}},
	})
	if isErr {
		t.Fatalf("update_group errored: %s", text)
	}

	g, err := env.store.GetGroup(context.Background(), "alice", stored.ID)
	if err != nil {
		t.Fatalf("reload group: %v", err)
	}
	if g.ParticipantName != "Tobbe" || g.ParticipantID != "p-me" {
		t.Errorf("stored identity should be Tobbe/p-me, got %s/%s", g.ParticipantName, g.ParticipantID)
	}
	if g.GroupName != "Road trip" {
		t.Errorf("stored group name should follow Spliit, got %q", g.GroupName)
	}
}

func TestUpdateGroupIsScopedToTheCaller(t *testing.T) {
	env := setup(t)
	env.spliit.results["groups.getDetails"] = sampleDetails()
	env.seed(t, "alice", "trip", "grp-1", "p-me", "Tobias")
	env.seedUserOnly(t, "bob", "Bob")

	text, isErr := call(t, env.connect(t, "bob"), "update_group", map[string]any{
		"group": "grp-1", "add_participants": []string{"Mallory"},
	})
	if !isErr {
		t.Fatalf("bob edited alice's group: %s", text)
	}
	for _, ep := range env.spliit.endpoints() {
		if ep == "groups.update" {
			t.Fatal("a refused edit must not reach Spliit")
		}
	}
}
