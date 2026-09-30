package store

import (
	"errors"
	"testing"

	"github.com/rimexe0/telemetry/internal/activity"
)

func TestPostgresLabelProposalsApplyOnlyOnExactAcceptance(t *testing.T) {
	ctx, database := openCleanIntegration(t)
	proposal, err := database.CreateLabelProposal(ctx, []activity.LabelChange{
		{Action: "set", Kind: "site", Match: "https://www.youtube.com/shorts", Label: "distraction"},
		{Action: "set", Kind: "app", Match: "com.twitter.android", Label: "distraction"},
	}, "owner said these are doomscrolling")
	if err != nil {
		t.Fatal(err)
	}
	if labels, _ := database.ListActivityLabels(ctx); len(labels) != 0 {
		t.Fatalf("a proposal must not apply anything: %+v", labels)
	}
	if _, err := database.DecideLabelProposal(ctx, proposal.ID, true, ProposalDecision{ContentHash: "other"}, nil); !errors.Is(err, ErrProposalConflict) {
		t.Fatalf("wrong hash = %v", err)
	}
	if _, err := database.DecideLabelProposal(ctx, proposal.ID, true, ProposalDecision{ContentHash: proposal.ContentHash}, []byte(`{"decided_by":"owner"}`)); err != nil {
		t.Fatal(err)
	}
	labels, err := database.ListActivityLabels(ctx)
	if err != nil || len(labels) != 2 || labels[0].Match != "com.twitter.android" && labels[1].Match != "youtube.com" {
		t.Fatalf("labels = %+v, %v", labels, err)
	}
	replace, err := database.CreateLabelProposal(ctx, []activity.LabelChange{{Action: "set", Kind: "site", Match: "youtube.com", Label: "rest"}}, "music is rest")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.DecideLabelProposal(ctx, replace.ID, true, ProposalDecision{ContentHash: replace.ContentHash}, nil); err != nil {
		t.Fatal(err)
	}
	labels, _ = database.ListActivityLabels(ctx)
	for _, label := range labels {
		if label.Match == "youtube.com" && label.Label != "rest" {
			t.Fatalf("replacement not applied: %+v", labels)
		}
	}
	if _, err := database.DecideLabelProposal(ctx, replace.ID, false, ProposalDecision{ContentHash: replace.ContentHash}, nil); !errors.Is(err, ErrProposalConflict) {
		t.Fatalf("a decided proposal must not be decided again: %v", err)
	}
}
