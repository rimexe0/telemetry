package store

import (
	"errors"
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

func TestPostgresAddPlanBlockKeepsThePlanAndRefusesOverlap(t *testing.T) {
	ctx, database := openCleanIntegration(t)
	date := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	at := func(hour, minute int) time.Time { return time.Date(2026, 10, 1, hour, minute, 0, 0, time.UTC) }
	if _, _, err := database.ApplyDayPlan(ctx, date, PlanInput{Note: "day", Blocks: []PlanBlockSpec{
		{Title: "Dentist", StartTime: at(9, 0), DurationMinutes: 60, Fixed: true},
		{Title: "Lunch", StartTime: at(12, 0), DurationMinutes: 30},
	}}, "plan"); err != nil {
		t.Fatal(err)
	}
	change, plan, err := database.AddPlanBlock(ctx, date, PlanBlockSpec{Title: "Focus", StartTime: at(10, 15), DurationMinutes: 45, Focus: true}, "block for 45 minutes")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Blocks) != 3 || plan.Note != "day" || plan.Blocks[1].Title != "Focus" || !plan.Blocks[1].Focus || !plan.Blocks[0].Fixed || plan.Blocks[0].SourceBlockID == nil {
		t.Fatalf("plan = %+v", plan)
	}
	latest, _ := database.LatestPlan(ctx, date)
	if !latest.Blocks[1].Focus || latest.Blocks[0].Focus {
		t.Fatalf("focus must be stored on the new block only: %+v", latest.Blocks)
	}
	var conflict *PlanConflictError
	if _, _, err := database.AddPlanBlock(ctx, date, PlanBlockSpec{Title: "Focus", StartTime: at(11, 45), DurationMinutes: 30, Focus: true}, "again"); !errors.As(err, &conflict) || conflict.Title != "Lunch" {
		t.Fatalf("overlap with Lunch must be refused: %v", err)
	}
	// A block that only touches its neighbours is fine, and so is a day with no plan.
	if _, _, err := database.AddPlanBlock(ctx, date, PlanBlockSpec{Title: "Focus", StartTime: at(11, 0), DurationMinutes: 60, Focus: true}, "touching"); err != nil {
		t.Fatal(err)
	}
	if _, fresh, err := database.AddPlanBlock(ctx, date.AddDate(0, 0, 1), PlanBlockSpec{Title: "Focus", StartTime: at(30, 0), DurationMinutes: 10, Focus: true}, "empty day"); err != nil || len(fresh.Blocks) != 1 {
		t.Fatalf("empty day = %+v, %v", fresh, err)
	}
	if _, err := database.RevertAgentChange(ctx, change.ID); err != nil {
		t.Fatal(err)
	}
	latest, _ = database.LatestPlan(ctx, date)
	if len(latest.Blocks) != 2 || latest.Blocks[0].Title != "Dentist" || latest.Blocks[1].Title != "Lunch" {
		t.Fatalf("revert must restore the plan before the block: %+v", latest.Blocks)
	}
}
