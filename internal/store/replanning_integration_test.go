package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"
)

func TestPostgresReplanProposalLifecycle(t *testing.T) {
	databaseURL := os.Getenv("TELEMETRY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TELEMETRY_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	database, err := OpenPostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.ApplyMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	if err := clearIntegrationData(ctx, database); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := clearIntegrationData(ctx, database); err != nil {
			t.Error(err)
		}
	})

	zone := time.FixedZone("Istanbul", 3*3600)
	clock := func(hour, minute int) time.Time { return time.Date(2026, 9, 25, hour, minute, 0, 0, zone) }
	day := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	writing, err := database.CreateActivity(ctx, Activity{Title: "Write report", NextAction: "Draft summary"})
	if err != nil {
		t.Fatal(err)
	}
	reading, err := database.CreateActivity(ctx, Activity{Title: "Read paper", NextAction: "Open section 3"})
	if err != nil {
		t.Fatal(err)
	}
	base, err := database.SavePlan(ctx, day, PlanInput{Blocks: []PlanBlockSpec{
		{Title: "Breakfast", StartTime: clock(8, 0), DurationMinutes: 30},
		{ActivityID: &writing.ID, StartTime: clock(9, 0), DurationMinutes: 60},
		{Title: "Standup", StartTime: clock(10, 30), DurationMinutes: 30, Fixed: true},
		{ActivityID: &reading.ID, StartTime: clock(11, 0), DurationMinutes: 45},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SetPlanBlockState(ctx, day, base.Blocks[0].ID, "done", "", "", "state"); err != nil {
		t.Fatal(err)
	}
	session, err := database.StartExecutionSession(ctx, ExecutionSessionInput{ActivityID: writing.ID, PlanBlockID: &base.Blocks[1].ID, StartedAt: ptr(clock(9, 0))})
	if err != nil {
		t.Fatal(err)
	}

	request := PlanChangeRequest{BasePlanVersionID: base.VersionID, Reason: "Doctor ran late",
		Disruption: &DisruptionInput{Start: clock(9, 15), End: ptr(clock(10, 0)), AvailableUntil: ptr(clock(18, 0))}}
	proposal, err := database.CreatePlanChangeProposal(ctx, day, request)
	if err != nil {
		t.Fatalf("create proposal: %v", err)
	}
	if proposal.Status != ProposalPending || proposal.BasePlanVersionID != base.VersionID || proposal.ContentHash == "" {
		t.Fatalf("proposal = %+v", proposal)
	}
	if len(proposal.Warnings) != 1 || proposal.Warnings[0].Code != "missing_resume_marker" || *proposal.Warnings[0].SessionID != session.ID {
		t.Fatalf("warnings = %+v", proposal.Warnings)
	}
	stored, err := database.GetPlanChangeProposal(ctx, day, proposal.ID)
	if err != nil || stored.ContentHash != proposal.ContentHash || len(stored.Changes) != len(proposal.Changes) {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
	if versions, _ := database.ListPlanVersions(ctx, day); len(versions) != 1 {
		t.Fatalf("creating a proposal saved a version: %d", len(versions))
	}

	provenance := json.RawMessage(`{"decided_by":"owner","source":"integration_test"}`)
	if _, err := database.AcceptPlanChangeProposal(ctx, day, proposal.ID, ProposalDecision{ContentHash: "not-reviewed"}, provenance); !errors.Is(err, ErrProposalConflict) {
		t.Fatalf("wrong hash err = %v", err)
	}

	// Two concurrent accepts of the same reviewed content append one version.
	var wait sync.WaitGroup
	results := make([]error, 2)
	for i := range results {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, results[i] = database.AcceptPlanChangeProposal(ctx, day, proposal.ID, ProposalDecision{ContentHash: proposal.ContentHash}, provenance)
		}()
	}
	wait.Wait()
	for _, err := range results {
		if err != nil {
			t.Fatalf("accept: %v", err)
		}
	}
	versions, err := database.ListPlanVersions(ctx, day)
	if err != nil || len(versions) != 2 {
		t.Fatalf("versions = %+v, %v", versions, err)
	}
	accepted, err := database.GetPlanChangeProposal(ctx, day, proposal.ID)
	if err != nil || accepted.Status != ProposalAccepted || accepted.ResultPlanVersionID == nil || *accepted.ResultPlanVersionID != versions[0].VersionID {
		t.Fatalf("accepted = %+v, %v", accepted, err)
	}

	latest, err := database.LatestPlan(ctx, day)
	if err != nil {
		t.Fatal(err)
	}
	byTitle := map[string]PlanBlock{}
	for _, block := range latest.Blocks {
		byTitle[block.Title] = block
	}
	if byTitle["Breakfast"].State != "done" || !byTitle["Standup"].StartTime.Equal(clock(10, 30)) {
		t.Fatalf("finished or fixed block changed: %+v", latest.Blocks)
	}
	if !byTitle["Write report"].StartTime.Equal(clock(11, 0)) || byTitle["Write report"].DurationMinutes != 60 || *byTitle["Write report"].SourceBlockID != base.Blocks[1].ID {
		t.Fatalf("moved block = %+v", byTitle["Write report"])
	}
	// The old version and the execution history are retained unchanged.
	if old, err := database.PlanBlocksByIDs(ctx, []int64{base.Blocks[1].ID}); err != nil || len(old) != 1 || !old[0].StartTime.Equal(clock(9, 0)) {
		t.Fatalf("old block = %+v, %v", old, err)
	}
	if current, err := database.GetExecutionSession(ctx, session.ID); err != nil || current.EndedAt != nil || current.State != "active" {
		t.Fatalf("session changed = %+v, %v", current, err)
	}

	// A proposal prepared against the superseded version cannot be applied.
	if _, err := database.CreatePlanChangeProposal(ctx, day, request); !errors.Is(err, ErrProposalStale) {
		t.Fatalf("stale create err = %v", err)
	}
	next := PlanChangeRequest{BasePlanVersionID: latest.VersionID, Reason: "Keep it", Disruption: &DisruptionInput{Start: clock(12, 0), AvailableUntil: ptr(clock(18, 0))}}
	stale, err := database.CreatePlanChangeProposal(ctx, day, next)
	if err != nil {
		t.Fatal(err)
	}
	rejectable, err := database.CreatePlanChangeProposal(ctx, day, next)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		rejected, err := database.RejectPlanChangeProposal(ctx, day, rejectable.ID, ProposalDecision{Note: "not now"}, provenance)
		if err != nil || rejected.Status != ProposalRejected {
			t.Fatalf("reject = %+v, %v", rejected, err)
		}
	}
	if _, err := database.SavePlan(ctx, day, PlanInput{Note: "manual edit", Blocks: []PlanBlockSpec{{Title: "Only", StartTime: clock(13, 0), DurationMinutes: 30}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.AcceptPlanChangeProposal(ctx, day, stale.ID, ProposalDecision{ContentHash: stale.ContentHash}, provenance); !errors.Is(err, ErrProposalStale) {
		t.Fatalf("stale accept err = %v", err)
	}
	if marked, _ := database.GetPlanChangeProposal(ctx, day, stale.ID); marked.Status != ProposalStale {
		t.Fatalf("stale status = %s", marked.Status)
	}
	if versions, _ := database.ListPlanVersions(ctx, day); len(versions) != 3 {
		t.Fatalf("stale or rejected proposal saved a version: %d", len(versions))
	}
	if listed, err := database.ListPlanChangeProposals(ctx, day, 10); err != nil || len(listed) != 3 || listed[0].ID != rejectable.ID {
		t.Fatalf("list = %d, %v", len(listed), err)
	}
}
