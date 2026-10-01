package store

import (
	"testing"
	"time"
)

func TestPostgresAgentTasksAndPlansApplyAndRevert(t *testing.T) {
	ctx, database := openCleanIntegration(t)
	title, next := "Write the collector", "Open blocker.go"
	added, tasks, err := database.ApplyTaskChanges(ctx, []TaskChange{{Action: "add", Title: &title, NextAction: &next}}, "owner approved")
	if err != nil || len(tasks) != 1 {
		t.Fatalf("add = %+v, %v", tasks, err)
	}
	id := tasks[0].ID
	done := "done"
	updated, _, err := database.ApplyTaskChanges(ctx, []TaskChange{{Action: "update", ID: &id, Status: &done}}, "finished")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RevertAgentChange(ctx, updated.ID); err != nil {
		t.Fatal(err)
	}
	listed, _ := database.ListActivities(ctx, ActivityFilter{Limit: 10})
	if len(listed) != 1 || listed[0].Status != "active" || listed[0].NextAction != next {
		t.Fatalf("after reverting the update: %+v", listed)
	}
	if _, err := database.RevertAgentChange(ctx, added.ID); err != nil {
		t.Fatal(err)
	}
	listed, _ = database.ListActivities(ctx, ActivityFilter{Limit: 10})
	if len(listed) != 1 || listed[0].Status != "abandoned" {
		t.Fatalf("reverting an add keeps the task, abandoned: %+v", listed)
	}

	date := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	start := time.Date(2026, 10, 1, 6, 30, 0, 0, time.UTC)
	first, plan, err := database.ApplyDayPlan(ctx, date, PlanInput{Blocks: []PlanBlockSpec{{ActivityID: &id, StartTime: start, DurationMinutes: 90}}}, "morning plan")
	if err != nil || plan.Blocks[0].Title != title {
		t.Fatalf("first plan = %+v, %v", plan, err)
	}
	second, _, err := database.ApplyDayPlan(ctx, date, PlanInput{Blocks: []PlanBlockSpec{{Title: "Shower", StartTime: start, DurationMinutes: 20}}}, "changed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RevertAgentChange(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	latest, err := database.LatestPlan(ctx, date)
	if err != nil || len(latest.Blocks) != 1 || latest.Blocks[0].Title != title || latest.Blocks[0].ActivityID == nil {
		t.Fatalf("revert must restore the first plan: %+v, %v", latest, err)
	}
	if _, err := database.RevertAgentChange(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	latest, _ = database.LatestPlan(ctx, date)
	if latest == nil || len(latest.Blocks) != 0 {
		t.Fatalf("reverting the first plan leaves an empty version: %+v", latest)
	}
	versions, _ := database.ListPlanVersions(ctx, date)
	if len(versions) != 4 {
		t.Fatalf("every version must be kept: %d", len(versions))
	}
}
