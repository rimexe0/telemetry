package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// Planning integration coverage runs against the same caller-provided isolated
// database as the revision regression. It is skipped without the URL.
func TestPostgresPlanningRetainsVersionsAndStates(t *testing.T) {
	databaseURL := os.Getenv("TELEMETRY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TELEMETRY_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	database, err := OpenPostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
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

	activity, err := database.CreateActivity(ctx, Activity{Title: "Practice piano", NextAction: "Open page 12", Priority: 5})
	if err != nil {
		t.Fatalf("create activity: %v", err)
	}
	if activity.ID == 0 || activity.Status != "active" {
		t.Fatalf("unexpected activity: %+v", activity)
	}
	if _, err := database.CreateActivity(ctx, Activity{Title: "   "}); err == nil {
		t.Fatal("empty title accepted")
	}
	if _, err := database.CreateActivity(ctx, Activity{Title: "X", Status: "someday"}); err == nil {
		t.Fatal("invalid status accepted")
	}
	activity.Status = "waiting"
	activity.NextAction = "Wait for tuner"
	updated, err := database.UpdateActivity(ctx, activity)
	if err != nil || updated.Status != "waiting" {
		t.Fatalf("update activity = %+v, %v", updated, err)
	}
	if _, err := database.UpdateActivity(ctx, Activity{ID: 99999, Title: "Ghost"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown activity update error = %v", err)
	}

	zone := time.FixedZone("test", 3*3600)
	day := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	plan, err := database.SavePlan(ctx, day, PlanInput{Blocks: []PlanBlockSpec{
		{ActivityID: &activity.ID, StartTime: time.Date(2026, 9, 15, 9, 0, 0, 0, zone), DurationMinutes: 50},
		{Title: "Rest", StartTime: time.Date(2026, 9, 15, 10, 0, 0, 0, zone), DurationMinutes: 15, Fixed: true},
	}})
	if err != nil {
		t.Fatalf("save plan: %v", err)
	}
	if len(plan.Blocks) != 2 || plan.Blocks[0].Title != "Practice piano" {
		t.Fatalf("plan blocks = %+v", plan.Blocks)
	}
	unknown := int64(99999)
	if _, err := database.SavePlan(ctx, day, PlanInput{Blocks: []PlanBlockSpec{{ActivityID: &unknown, StartTime: day, DurationMinutes: 10}}}); err == nil {
		t.Fatal("unknown activity accepted in plan")
	}

	firstVersionID := plan.VersionID
	plan2, err := database.SavePlan(ctx, day, PlanInput{Blocks: []PlanBlockSpec{
		{ActivityID: &activity.ID, StartTime: time.Date(2026, 9, 15, 11, 0, 0, 0, zone), DurationMinutes: 30},
	}})
	if err != nil {
		t.Fatalf("save second plan: %v", err)
	}
	if plan2.VersionID == firstVersionID {
		t.Fatal("second save reused the version")
	}
	latest, err := database.LatestPlan(ctx, day)
	if err != nil || latest == nil || latest.VersionID != plan2.VersionID {
		t.Fatalf("latest plan = %+v, %v", latest, err)
	}
	if len(latest.Blocks) != 1 {
		t.Fatalf("latest plan blocks = %+v", latest.Blocks)
	}

	stated, err := database.SetPlanBlockState(ctx, latest.Blocks[0].ID, "done", "went well")
	if err != nil || stated.State != "done" {
		t.Fatalf("set state = %+v, %v", stated, err)
	}
	if _, err := database.SetPlanBlockState(ctx, plan.Blocks[0].ID, "done", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("older version block state error = %v", err)
	}
	if _, err := database.SetPlanBlockState(ctx, latest.Blocks[0].ID, "maybe", ""); err == nil {
		t.Fatal("invalid state accepted")
	}

	activities, err := database.ListActivities(ctx, ActivityFilter{Status: "waiting"})
	if err != nil || len(activities) != 1 || activities[0].ID != activity.ID {
		t.Fatalf("list activities = %+v, %v", activities, err)
	}

	missing, err := database.LatestPlan(ctx, time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC))
	if err != nil || missing != nil {
		t.Fatalf("missing plan = %+v, %v", missing, err)
	}
}
