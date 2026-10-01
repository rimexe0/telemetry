package store

import (
	"testing"
	"time"
)

func TestPostgresFollowUpsApplyCloseAndRevert(t *testing.T) {
	ctx, database := openCleanIntegration(t)
	due := time.Date(2026, 10, 1, 16, 10, 0, 0, time.UTC)
	title, nag := "Hang the laundry", 10
	added, items, err := database.ApplyFollowUpChanges(ctx, []FollowUpChange{{Action: "add", Title: &title, DueAt: &due, NagMinutes: &nag}}, "washing machine, 1 hour")
	if err != nil || len(items) != 1 || items[0].State != FollowUpOpen || *items[0].NagMinutes != 10 || !items[0].DueAt.Equal(due) {
		t.Fatalf("add = %+v, %v", items, err)
	}
	id := items[0].ID
	// The phone marks it done; a second tap changes nothing.
	closed, err := database.CloseFollowUp(ctx, id, "device:android-1")
	if err != nil || closed.State != FollowUpDone || closed.ClosedBy != "device:android-1" || closed.ClosedAt == nil {
		t.Fatalf("close = %+v, %v", closed, err)
	}
	again, err := database.CloseFollowUp(ctx, id, "device:other")
	if err != nil || again.ClosedBy != "device:android-1" || !again.ClosedAt.Equal(*closed.ClosedAt) {
		t.Fatalf("second close = %+v, %v", again, err)
	}
	if _, err := database.CloseFollowUp(ctx, id+99, "device:android-1"); err != ErrNotFound {
		t.Fatalf("unknown follow-up = %v", err)
	}
	// Reopening and moving it is a logged change that a revert undoes.
	open, later := FollowUpOpen, due.Add(time.Hour)
	moved, items, err := database.ApplyFollowUpChanges(ctx, []FollowUpChange{{Action: "update", ID: &id, State: &open, DueAt: &later, ClearNag: true}}, "not done yet")
	if err != nil || items[0].State != FollowUpOpen || items[0].ClosedAt != nil || items[0].NagMinutes != nil || !items[0].DueAt.Equal(later) {
		t.Fatalf("update = %+v, %v", items, err)
	}
	if _, err := database.RevertAgentChange(ctx, moved.ID); err != nil {
		t.Fatal(err)
	}
	listed, _ := database.ListFollowUps(ctx, due.Add(-time.Hour))
	if len(listed) != 1 || listed[0].State != FollowUpDone || listed[0].ClosedBy != "device:android-1" || !listed[0].DueAt.Equal(due) || *listed[0].NagMinutes != 10 {
		t.Fatalf("after reverting the update: %+v", listed)
	}
	// Reverting the add drops it; it is not deleted.
	if _, err := database.RevertAgentChange(ctx, added.ID); err != nil {
		t.Fatal(err)
	}
	listed, _ = database.ListFollowUps(ctx, due.Add(-time.Hour))
	if len(listed) != 1 || listed[0].State != FollowUpDropped {
		t.Fatalf("after reverting the add: %+v", listed)
	}
	bad := "  "
	if _, _, err := database.ApplyFollowUpChanges(ctx, []FollowUpChange{{Action: "add", Title: &bad, DueAt: &due}}, "x"); err == nil {
		t.Fatal("an empty title must be refused")
	}
}
