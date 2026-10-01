package store

import (
	"errors"
	"testing"
)

func TestPostgresTaskRulesApplyAndRevert(t *testing.T) {
	ctx, database := openCleanIntegration(t)
	title := "Telemetry"
	_, tasks, err := database.ApplyTaskChanges(ctx, []TaskChange{{Action: "add", Title: &title}}, "task")
	if err != nil {
		t.Fatal(err)
	}
	task := tasks[0].ID
	added, rules, err := database.ApplyTaskRuleChanges(ctx, []TaskRuleChange{
		{Action: "add", TaskID: task, Signal: "app", Pattern: "T3 Code (Nightly)"},
		{Action: "add", TaskID: task, Signal: "domain", Pattern: "GitHub.com"},
		{Action: "add", TaskID: task, Signal: "path_prefix", Pattern: "/Users/ri/code/personal/Telemetry/"},
	}, "working on Telemetry")
	if err != nil || len(rules) != 3 || rules[1].Pattern != "github.com" || rules[2].Pattern != "/Users/ri/code/personal/Telemetry" {
		t.Fatalf("add = %+v, %v", rules, err)
	}
	// A duplicate is refused and leaves nothing half-applied.
	if _, _, err := database.ApplyTaskRuleChanges(ctx, []TaskRuleChange{
		{Action: "add", TaskID: task, Signal: "domain", Pattern: "docs.go.dev"},
		{Action: "add", TaskID: task, Signal: "app", Pattern: "T3 Code (Nightly)"},
	}, "again"); !errors.Is(err, ErrInvalidAgentChange) {
		t.Fatalf("duplicate = %v", err)
	}
	if _, _, err := database.ApplyTaskRuleChanges(ctx, []TaskRuleChange{{Action: "add", TaskID: task + 50, Signal: "app", Pattern: "Code"}}, "x"); !errors.Is(err, ErrInvalidAgentChange) {
		t.Fatalf("unknown task = %v", err)
	}
	enabled, _ := database.ListActivityContextRules(ctx, false)
	if len(enabled) != 3 {
		t.Fatalf("enabled after refused changes = %+v", enabled)
	}
	removed, _, err := database.ApplyTaskRuleChanges(ctx, []TaskRuleChange{{Action: "remove", TaskID: task, Signal: "domain", Pattern: "github.com"}}, "not github")
	if err != nil {
		t.Fatal(err)
	}
	if enabled, _ = database.ListActivityContextRules(ctx, false); len(enabled) != 2 {
		t.Fatalf("after remove = %+v", enabled)
	}
	if _, err := database.RevertAgentChange(ctx, removed.ID); err != nil {
		t.Fatal(err)
	}
	if enabled, _ = database.ListActivityContextRules(ctx, false); len(enabled) != 3 || len(TaskRules(enabled)[task]) != 3 {
		t.Fatalf("after reverting the remove = %+v", enabled)
	}
	if _, err := database.RevertAgentChange(ctx, added.ID); err != nil {
		t.Fatal(err)
	}
	// The first two added rules are gone; the re-added github.com rule is a new row and stays.
	if enabled, _ = database.ListActivityContextRules(ctx, false); len(enabled) != 1 || enabled[0].Pattern != "github.com" {
		t.Fatalf("after reverting the add = %+v", enabled)
	}
}
