package store

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/budget"
	"github.com/rimexe0/telemetry/internal/policy"
)

func TestPostgresLateTickCorrectsAClosedDay(t *testing.T) {
	ctx, database := openCleanIntegration(t)
	now := time.Now().UTC()
	since := now.Add(-72 * time.Hour)
	config := policy.Config{Timezone: "Europe/Istanbul", ClosedByDefault: true,
		Distractions: []policy.TargetGroup{{ID: "video", AndroidPackages: []string{"com.google.android.youtube"}}},
		Budget:       ownerBudget(), BudgetSince: &since}
	if err := config.Normalize(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.pool.Exec(ctx, `INSERT INTO policy_versions (config, note) VALUES ($1::jsonb, 'test')`, string(raw)); err != nil {
		t.Fatal(err)
	}
	name := "walk"
	_, habits, err := database.ApplyHabitChanges(ctx, []HabitChange{{Action: "add", Name: &name}}, "the owner's habit")
	if err != nil {
		t.Fatal(err)
	}
	id := habits[0].ID
	if _, err := database.pool.Exec(ctx, `UPDATE habits SET created_at = $1`, now.Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if last, err := database.PhoneLastDelivered(ctx); err != nil || last != nil {
		t.Fatalf("phone before any delivery = %v, %v", last, err)
	}
	today := budget.DayOf(config, now)
	yesterday := budget.AddDays(today, -1)
	if _, err := CloseBudgetDays(ctx, database, database, now); err != nil {
		t.Fatal(err)
	}
	closed, err := database.BudgetDay(ctx, yesterday)
	if err != nil || closed == nil || closed.Revision != 1 || len(closed.HabitsMissed) != 1 {
		t.Fatalf("yesterday at its close = %+v, %v", closed, err)
	}
	result, err := LoadBudget(ctx, database, database, config, now)
	if err != nil || result.State.Penalty != 30 {
		t.Fatalf("penalty before = %+v, %v", result.State.Penalty, err)
	}

	at := budget.DayStart(config, yesterday).Add(2 * time.Hour).Truncate(time.Millisecond)
	if err := LateTick(ctx, database, database, id, budget.ActionTick, yesterday, at, "device:phone", now); err != nil {
		t.Fatal(err)
	}
	// Delivered again, it is kept once.
	if err := LateTick(ctx, database, database, id, budget.ActionTick, yesterday, at, "device:phone", now); err != nil {
		t.Fatal(err)
	}
	events, err := database.HabitEvents(ctx, yesterday)
	if err != nil || len(events) != 1 || !events[0].At.Equal(at) || events[0].Day != yesterday || events[0].ReceivedAt.Before(now.Add(-time.Second)) {
		t.Fatalf("events = %+v, %v", events, err)
	}
	if err := LateTick(ctx, database, database, id, budget.ActionTick, yesterday, budget.DayEnd(config, yesterday).Add(time.Minute), "device:phone", now); !errors.Is(err, ErrHabitLateWrongDay) {
		t.Fatalf("a tick for the wrong day = %v", err)
	}

	// The corrected day is the one in force; the first snapshot stays.
	newest, err := database.BudgetDay(ctx, yesterday)
	if err != nil || newest.Revision != 2 || len(newest.HabitsDone) != 1 || len(newest.HabitsMissed) != 0 || newest.Cause == "" {
		t.Fatalf("yesterday now = %+v, %v", newest, err)
	}
	var stored, firstMissed int
	if err := database.pool.QueryRow(ctx, `SELECT count(*), max(cardinality(habits_missed)) FILTER (WHERE revision = 1) FROM budget_days WHERE day = $1::date`, yesterday).Scan(&stored, &firstMissed); err != nil {
		t.Fatal(err)
	}
	if stored != 2 || firstMissed != 1 {
		t.Fatalf("revisions stored = %d, the first missed %d", stored, firstMissed)
	}
	days, err := database.BudgetDays(ctx, 30)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, day := range days {
		if seen[day.Day] {
			t.Fatalf("days list %s twice", day.Day)
		}
		seen[day.Day] = true
		if day.Day == yesterday && day.Revision != 2 {
			t.Fatalf("days show revision %d of yesterday", day.Revision)
		}
	}
	if result, err = LoadBudget(ctx, database, database, config, now); err != nil || result.State.Penalty != 0 {
		t.Fatalf("penalty after = %+v, %v", result.State.Penalty, err)
	}
	// The first snapshot is still written once.
	if saved, err := database.SaveBudgetDay(ctx, *closed); err != nil || saved {
		t.Fatalf("saving the first snapshot again = %v, %v", saved, err)
	}
	if revised, err := database.ReviseBudgetDay(ctx, *newest, "nothing new"); err != nil || revised {
		t.Fatalf("a revision that says the same = %v, %v", revised, err)
	}
	if revised, err := database.ReviseBudgetDay(ctx, budget.Snapshot{Day: "2020-01-01", Settings: ownerBudget()}, "no such day"); err != nil || revised {
		t.Fatalf("revising a day without a snapshot = %v, %v", revised, err)
	}

	// An archived habit still takes a tick made before it was archived.
	archived := true
	if _, _, err := database.ApplyHabitChanges(ctx, []HabitChange{{Action: "update", ID: &id, Archived: &archived}}, "archive"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.RecordHabitEvent(ctx, budget.HabitEvent{HabitID: id, Day: yesterday, Action: budget.ActionUntick, At: at.Add(time.Hour)}); err != nil {
		t.Fatalf("an untick made before archiving = %v", err)
	}
	if _, err := database.RecordHabitEvent(ctx, budget.HabitEvent{HabitID: id, Day: today, Action: budget.ActionTick}); !errors.Is(err, ErrInvalidHabit) {
		t.Fatalf("a tick after archiving = %v", err)
	}

	// The phone's newest delivery; other devices do not count.
	phoneAt, laterAt := now.Add(-time.Hour).Truncate(time.Microsecond), now.Truncate(time.Microsecond)
	if _, err := database.pool.Exec(ctx, `INSERT INTO source_instances (source_instance_key, source_kind, device_id, first_collected_at) VALUES
		('android:phone-1:usage', 'android', 'phone-1', $1), ('telemetry-desktop:mac:collector', 'telemetry-desktop', 'mac', $1)`, phoneAt); err != nil {
		t.Fatal(err)
	}
	if _, err := database.pool.Exec(ctx, `INSERT INTO ingestion_batches (batch_id, device_id, collector_version, request_digest, received_at) VALUES
		('b1', 'phone-1', 't', 'd', $1), ('b2', 'mac', 't', 'd', $2)`, phoneAt, laterAt); err != nil {
		t.Fatal(err)
	}
	if last, err := database.PhoneLastDelivered(ctx); err != nil || last == nil || !last.Equal(phoneAt) {
		t.Fatalf("phone last delivered = %v, %v", last, err)
	}
}
