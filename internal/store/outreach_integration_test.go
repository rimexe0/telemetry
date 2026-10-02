package store

import (
	"testing"
	"time"
)

func TestPostgresLockUntilPlannedAndOutreachLog(t *testing.T) {
	ctx, database := openCleanIntegration(t)
	on := true
	locked, config, err := database.ApplyBlockingSchedule(ctx, ScheduleChange{LockUntilPlanned: &on}, "locked until i plan the day out")
	if err != nil || !config.PlanGate.FromWake || !config.PlanGate.LockAll {
		t.Fatalf("lock = %+v, %v", config.PlanGate, err)
	}
	// A day with no fixed times is planned with no blocks.
	date := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	if _, _, err := database.ApplyDayPlan(ctx, date, PlanInput{Note: "sick day, album only"}, "planned"); err != nil {
		t.Fatalf("empty plan: %v", err)
	}
	if plan, err := database.LatestPlan(ctx, date); err != nil || plan == nil {
		t.Fatalf("the empty plan must exist: %+v, %v", plan, err)
	}
	if _, err := database.RevertAgentChange(ctx, locked.ID); err != nil {
		t.Fatal(err)
	}
	if stored, _ := database.LatestPolicy(ctx); stored.Config.PlanGate.FromWake || stored.Config.PlanGate.LockAll {
		t.Fatalf("after revert = %+v", stored.Config.PlanGate)
	}

	sent, err := database.AddOutreach(ctx, Outreach{Kind: "morning", Message: "You woke at 08:26.", Facts: "woke 08:26", Delivered: true})
	if err != nil || sent.ID == 0 || sent.CreatedAt.IsZero() {
		t.Fatalf("add = %+v, %v", sent, err)
	}
	if _, err := database.AddOutreach(ctx, Outreach{Kind: "off_plan", Facts: "resting"}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.AddOutreach(ctx, Outreach{Kind: "nagging"}); err == nil {
		t.Fatal("an unknown kind must be refused")
	}
	entries, err := database.ListOutreach(ctx, time.Now().Add(-time.Hour))
	if err != nil || len(entries) != 2 || entries[0].Kind != "off_plan" || entries[0].Message != "" || !entries[1].Delivered {
		t.Fatalf("list = %+v, %v", entries, err)
	}
	if none, _ := database.ListOutreach(ctx, time.Now().Add(time.Hour)); len(none) != 0 {
		t.Fatalf("since the future = %+v", none)
	}
}
