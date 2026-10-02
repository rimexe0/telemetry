package store

import (
	"testing"
	"time"
)

func TestPostgresTaskClaimsAreRecordedInOrder(t *testing.T) {
	ctx, database := openCleanIntegration(t)
	title := "Science fair presentation"
	_, tasks, err := database.ApplyTaskChanges(ctx, []TaskChange{{Action: "add", Title: &title}}, "task")
	if err != nil {
		t.Fatal(err)
	}
	task := tasks[0].ID
	first, err := database.AddTaskClaim(ctx, TaskClaim{TaskID: &task, DeviceID: "mac-1", Context: "Keynote"})
	if err != nil || first.ID == 0 || first.StartedAt.IsZero() {
		t.Fatalf("claim = %+v, %v", first, err)
	}
	if _, err := database.AddTaskClaim(ctx, TaskClaim{DeviceID: "mac-1", Context: "Finder"}); err != nil {
		t.Fatalf("not work = %v", err)
	}
	missing := task + 50
	if _, err := database.AddTaskClaim(ctx, TaskClaim{TaskID: &missing, DeviceID: "mac-1"}); err != ErrNotFound {
		t.Fatalf("unknown task = %v", err)
	}
	claims, err := database.ListTaskClaims(ctx, first.StartedAt.Add(-time.Minute), first.StartedAt.Add(time.Minute))
	if err != nil || len(claims) != 2 || *claims[0].TaskID != task || claims[1].TaskID != nil || claims[0].Context != "Keynote" {
		t.Fatalf("listed = %+v, %v", claims, err)
	}
}
