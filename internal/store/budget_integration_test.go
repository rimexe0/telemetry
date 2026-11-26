package store

import (
	"errors"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/budget"
)

func TestPostgresHabitsDoorsGrayscaleAndDays(t *testing.T) {
	ctx, database := openCleanIntegration(t)
	name, other := "walk", "stretch"
	weekdays := budget.Weekdays{1, 3}
	logged, habits, err := database.ApplyHabitChanges(ctx, []HabitChange{{Action: "add", Name: &name}, {Action: "add", Name: &other, Weekdays: &weekdays}}, "the owner's two habits")
	if err != nil || len(habits) != 2 || habits[1].Weekdays[1] != 3 || habits[1].Position != 1 {
		t.Fatalf("add = %+v, %v", habits, err)
	}
	renamed := "long walk"
	changed, _, err := database.ApplyHabitChanges(ctx, []HabitChange{{Action: "update", ID: &habits[0].ID, Name: &renamed}}, "rename")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RevertAgentChange(ctx, changed.ID); err != nil {
		t.Fatal(err)
	}
	listed, _ := database.ListHabits(ctx)
	if listed[0].Name != "walk" {
		t.Fatalf("revert of the rename = %+v", listed)
	}
	// Reverting the add archives the habits; nothing is deleted.
	if _, err := database.RevertAgentChange(ctx, logged.ID); err != nil {
		t.Fatal(err)
	}
	if listed, _ = database.ListHabits(ctx); len(listed) != 2 || listed[0].ArchivedAt == nil {
		t.Fatalf("after reverting the add = %+v", listed)
	}
	if _, err := database.RecordHabitEvent(ctx, budget.HabitEvent{HabitID: listed[0].ID, Day: "2026-11-26", Action: budget.ActionTick}); !errors.Is(err, ErrInvalidHabit) {
		t.Fatalf("ticking an archived habit = %v", err)
	}
	archived := false
	if _, _, err := database.ApplyHabitChanges(ctx, []HabitChange{{Action: "update", ID: &listed[0].ID, Archived: &archived}}, "back"); err != nil {
		t.Fatal(err)
	}
	tick, err := database.RecordHabitEvent(ctx, budget.HabitEvent{HabitID: listed[0].ID, Day: "2026-11-26", Action: budget.ActionTick, Source: "device:phone"})
	if err != nil || tick.Day != "2026-11-26" {
		t.Fatalf("tick = %+v, %v", tick, err)
	}
	if _, err := database.RecordHabitEvent(ctx, budget.HabitEvent{HabitID: listed[0].ID, Day: "2026-11-26", Action: budget.ActionUntick}); err != nil {
		t.Fatal(err)
	}
	if events, _ := database.HabitEvents(ctx, "2026-11-26"); len(events) != 2 || events[1].Action != budget.ActionUntick {
		t.Fatalf("events = %+v", events)
	}

	now := time.Now().UTC().Truncate(time.Second)
	door, err := database.CreateDoor(ctx, budget.Door{RequestedAt: now, OpensAt: now.Add(10 * time.Minute), WindowEnd: now.Add(70 * time.Minute), UseMinutes: 10, GraceMinutes: 1, Source: "device:phone"})
	if err != nil || door.ID == 0 {
		t.Fatalf("door = %+v, %v", door, err)
	}
	if _, err := database.CreateDoor(ctx, budget.Door{RequestedAt: now, OpensAt: now.Add(10 * time.Minute), WindowEnd: now.Add(70 * time.Minute)}); !errors.Is(err, ErrDoorConflict) {
		t.Fatalf("a second door = %v", err)
	}
	cancelled, err := database.CancelDoor(ctx, door.ID, now.Add(time.Minute))
	if err != nil || cancelled.CancelledAt == nil {
		t.Fatalf("cancel = %+v, %v", cancelled, err)
	}
	if again, err := database.CancelDoor(ctx, door.ID, now.Add(2*time.Minute)); err != nil || !again.CancelledAt.Equal(*cancelled.CancelledAt) {
		t.Fatalf("cancelling twice = %+v, %v", again, err)
	}
	open, err := database.CreateDoor(ctx, budget.Door{RequestedAt: now, OpensAt: now, WindowEnd: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CancelDoor(ctx, open.ID, now.Add(time.Minute)); !errors.Is(err, ErrDoorNotWaiting) {
		t.Fatalf("cancelling an open door = %v", err)
	}
	if doors, _ := database.BudgetDoors(ctx, now.Add(-time.Hour)); len(doors) != 2 {
		t.Fatalf("doors = %+v", doors)
	}
	if _, err := database.RecordDoorBack(ctx, budget.DoorBack{Source: "device:phone"}); err != nil {
		t.Fatal(err)
	}
	if backs, _ := database.DoorBacks(ctx, now.Add(-time.Hour)); len(backs) != 1 {
		t.Fatalf("backs = %+v", backs)
	}
	// A door is the agreed way out: it opens no loophole case.
	if cases, _ := database.LoopholeCases(ctx, false, 10); len(cases) != 0 {
		t.Fatalf("doors opened loophole cases: %+v", cases)
	}

	if _, err := database.RecordGrayscale(ctx, budget.GrayscaleEvent{Action: budget.GrayscaleDecline, Platform: "linux"}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.RecordGrayscale(ctx, budget.GrayscaleEvent{Action: "off", Platform: "linux"}); !errors.Is(err, ErrInvalidHabit) {
		t.Fatalf("a bad grayscale action = %v", err)
	}
	if events, _ := database.GrayscaleEvents(ctx, now.Add(-time.Hour)); len(events) != 1 || events[0].Platform != "linux" {
		t.Fatalf("grayscale = %+v", events)
	}

	if reasons, _ := database.Reasons(ctx); len(reasons) != 0 {
		t.Fatalf("no reasons are seeded: %v", reasons)
	}
	if _, err := database.SaveReasons(ctx, []string{"to make music"}); err != nil {
		t.Fatal(err)
	}
	if reasons, _ := database.Reasons(ctx); len(reasons) != 1 || reasons[0] != "to make music" {
		t.Fatalf("reasons = %v", reasons)
	}

	snapshot := budget.Snapshot{Day: "2026-11-25", Budget: 150, Spent: 140, DebtPassed: 20, HabitsDone: []int64{1}, HabitsMissed: []int64{2}, ClosedAt: now}
	if saved, err := database.SaveBudgetDay(ctx, snapshot); err != nil || !saved {
		t.Fatalf("save = %v, %v", saved, err)
	}
	snapshot.Budget = 1
	if saved, _ := database.SaveBudgetDay(ctx, snapshot); saved {
		t.Fatal("a day's snapshot is written once")
	}
	if day, _ := database.BudgetDay(ctx, "2026-11-25"); day == nil || day.Budget != 150 || day.DebtPassed != 20 || day.HabitsMissed[0] != 2 {
		t.Fatalf("day = %+v", day)
	}
	if days, _ := database.BudgetDays(ctx, 14); len(days) != 1 {
		t.Fatalf("days = %+v", days)
	}
}
