package store

import (
	"errors"
	"testing"

	"github.com/rimexe0/telemetry/internal/activity"
	"github.com/rimexe0/telemetry/internal/policy"
)

func TestPostgresAgentChangesApplyAndRevert(t *testing.T) {
	ctx, database := openCleanIntegration(t)
	if _, err := database.SavePolicy(ctx, policy.Config{Essentials: []policy.TargetGroup{{ID: "com.whatsapp", Label: "com.whatsapp", AndroidPackages: []string{"com.whatsapp"}}}}, "base"); err != nil {
		t.Fatal(err)
	}
	block, err := database.ApplyBlocklistChange(ctx, []policy.BlocklistChange{
		{Action: "block", AndroidPackage: "com.twitter.android"}, {Action: "block", AndroidPackage: "com.whatsapp"},
	}, "owner approved in chat")
	if err != nil {
		t.Fatal(err)
	}
	state := func(pkg string) string {
		latest, err := database.LatestPolicy(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return policy.BlocklistState(latest.Config, pkg)
	}
	if state("com.twitter.android") != "block" || state("com.whatsapp") != "block" {
		t.Fatal("change not applied")
	}
	// A later, unrelated change must survive reverting the first one.
	if _, err := database.ApplyBlocklistChange(ctx, []policy.BlocklistChange{{Action: "block", AndroidPackage: "com.reddit.frontpage"}}, "later"); err != nil {
		t.Fatal(err)
	}
	revert, err := database.RevertAgentChange(ctx, block.ID)
	if err != nil || revert.RevertOf == nil || *revert.RevertOf != block.ID {
		t.Fatalf("revert = %+v, %v", revert, err)
	}
	if state("com.twitter.android") != "unlist" || state("com.whatsapp") != "always_allow" || state("com.reddit.frontpage") != "block" {
		t.Fatalf("revert restored the wrong state")
	}
	if _, err := database.RevertAgentChange(ctx, block.ID); !errors.Is(err, ErrProposalConflict) {
		t.Fatalf("second revert = %v", err)
	}
	if _, err := database.RevertAgentChange(ctx, revert.ID); err != nil || state("com.twitter.android") != "block" {
		t.Fatalf("reverting the revert = %v", err)
	}

	labels, err := database.ApplyLabelChanges(ctx, []activity.LabelChange{{Action: "set", Kind: "site", Match: "youtube.com", Label: "distraction"}}, "owner approved")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RevertAgentChange(ctx, labels.ID); err != nil {
		t.Fatal(err)
	}
	if current, _ := database.ListActivityLabels(ctx); len(current) != 0 {
		t.Fatalf("label revert left %+v", current)
	}
	changes, err := database.ListAgentChanges(ctx, 10)
	if err != nil || len(changes) != 6 || changes[0].RevertOf == nil {
		t.Fatalf("changes = %d, %v", len(changes), err)
	}
}
