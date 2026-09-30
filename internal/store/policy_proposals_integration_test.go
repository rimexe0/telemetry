package store

import (
	"errors"
	"testing"

	"github.com/rimexe0/telemetry/internal/policy"
)

func TestPostgresPolicyProposalAppendsAVersionOnAcceptance(t *testing.T) {
	ctx, database := openCleanIntegration(t)
	base, err := database.SavePolicy(ctx, policy.Config{Essentials: []policy.TargetGroup{{ID: "com.whatsapp", Label: "com.whatsapp", AndroidPackages: []string{"com.whatsapp"}}}}, "base")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreatePolicyProposal(ctx, []policy.BlocklistChange{{Action: "block", AndroidPackage: "x.com"}}, "sites"); !errors.Is(err, ErrInvalidPolicyProposal) {
		t.Fatalf("a website must be refused: %v", err)
	}
	proposal, err := database.CreatePolicyProposal(ctx, []policy.BlocklistChange{
		{Action: "block", AndroidPackage: "com.twitter.android"}, {Action: "block", AndroidPackage: "com.reddit.frontpage"},
	}, "owner named them")
	if err != nil {
		t.Fatal(err)
	}
	if latest, _ := database.LatestPolicy(ctx); latest.ID != base.ID {
		t.Fatal("a proposal must not change the policy")
	}
	decided, err := database.DecidePolicyProposal(ctx, proposal.ID, true, ProposalDecision{ContentHash: proposal.ContentHash}, []byte(`{"decided_by":"owner"}`))
	if err != nil || decided.ResultPolicyVersionID == nil {
		t.Fatalf("decided = %+v, %v", decided, err)
	}
	latest, err := database.LatestPolicy(ctx)
	if err != nil || latest.ID != *decided.ResultPolicyVersionID || len(latest.Config.Distractions) != 2 || len(latest.Config.Essentials) != 1 {
		t.Fatalf("latest = %+v, %v", latest, err)
	}
}
