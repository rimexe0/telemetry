package store

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/budget"
	"github.com/rimexe0/telemetry/internal/model"
	"github.com/rimexe0/telemetry/internal/policy"
)

func TestPostgresHabitRulesAreKeptRevertedAndTick(t *testing.T) {
	ctx, database := openCleanIntegration(t)
	// Bedtime off: the budget day starts at 04:00. The test works three hours
	// into today's day, so it reads the same at any hour.
	config := policy.Config{Timezone: "Europe/Istanbul"}
	if err := config.Normalize(); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(config)
	if _, err := database.pool.Exec(ctx, `INSERT INTO policy_versions (config, note) VALUES ($1::jsonb, 'test')`, string(raw)); err != nil {
		t.Fatal(err)
	}
	now := budget.DayStart(config, budget.DayOf(config, time.Now())).Add(3 * time.Hour)
	location, _ := time.LoadLocation("Europe/Istanbul")

	name := "Going outside"
	_, habits, err := database.ApplyHabitChanges(ctx, []HabitChange{{Action: "add", Name: &name}}, "the owner's habit")
	if err != nil || habits[0].Rule != nil {
		t.Fatalf("add = %+v, %v", habits, err)
	}
	id := habits[0].ID
	if _, err := database.pool.Exec(ctx, `UPDATE habits SET created_at = $1`, now.Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	// A made-up place, not the owner's.
	rule := budget.HabitRule{Kind: "place", Place: &budget.RulePlace{Name: "Test pier", Lat: 41.0, Lon: 29.0},
		Window: budget.RuleWindow{From: "wake", Until: now.Add(time.Hour).In(location).Format("15:04")}, Minutes: 2}
	set, habits, err := database.ApplyHabitChanges(ctx, []HabitChange{{Action: "update", ID: &id, Rule: &rule}}, "the owner picked the pier")
	if err != nil || habits[0].Rule == nil || habits[0].Rule.RadiusM != 100 || habits[0].Rule.Place.Name != "Test pier" {
		t.Fatalf("set = %+v, %v", habits, err)
	}
	listed, err := database.ListHabits(ctx)
	if err != nil || len(listed) != 1 || listed[0].Rule == nil || listed[0].Rule.Minutes != 2 {
		t.Fatalf("listed = %+v, %v", listed, err)
	}
	// A rule that can't be worked out is refused, and nothing changes.
	bad := budget.HabitRule{Kind: "place", Window: budget.RuleWindow{From: "wake", Until: "11:00"}}
	if _, _, err := database.ApplyHabitChanges(ctx, []HabitChange{{Action: "update", ID: &id, Rule: &bad}}, "x"); err == nil {
		t.Fatal("a rule without a place must be refused")
	}
	// Reverting the change takes the rule away again, and reverting that
	// brings it back.
	revert, err := database.RevertAgentChange(ctx, set.ID)
	if err != nil {
		t.Fatal(err)
	}
	if listed, _ = database.ListHabits(ctx); listed[0].Rule != nil {
		t.Fatalf("after revert = %+v", listed[0])
	}
	if _, err := database.RevertAgentChange(ctx, revert.ID); err != nil {
		t.Fatal(err)
	}
	if listed, _ = database.ListHabits(ctx); listed[0].Rule == nil || listed[0].Rule.Place.Name != "Test pier" {
		t.Fatalf("after reverting the revert = %+v", listed[0])
	}

	// No positions: nothing is ticked.
	if ticked, err := EvaluateHabitRules(ctx, database, database, now); err != nil || ticked != 0 {
		t.Fatalf("no positions = %d, %v", ticked, err)
	}
	// Positions from the phone: three minutes at the place.
	observations := []model.Observation{}
	for i, offset := range []time.Duration{-30, -29, -28, -27} {
		at := now.Add(offset * time.Minute)
		observations = append(observations, model.Observation{SourceInstanceKey: "android:phone-1:location", SourceKind: "android_location", DeviceID: "phone-1",
			SourceRecordKey: fmt.Sprintf("location:%d", i), Kind: "android.location.fix", OccurredAt: at, CollectedAt: at,
			OriginalPayload: json.RawMessage(fmt.Sprintf(`{"latitude":41.0001,"longitude":29.0,"accuracy_meters":8,"location_time":%q}`, at.Format(time.RFC3339Nano)))})
	}
	if _, err := database.Ingest(ctx, model.Batch{ID: "fixes", DeviceID: "phone-1", CollectorVersion: "test", Observations: observations}); err != nil {
		t.Fatal(err)
	}
	ticked, err := EvaluateHabitRules(ctx, database, database, now)
	if err != nil || ticked != 1 {
		t.Fatalf("reached = %d, %v", ticked, err)
	}
	events, err := database.HabitEvents(ctx, budget.DayOf(config, now))
	if err != nil || len(events) != 1 || events[0].Source != RuleSourceServer || !events[0].At.Equal(now.Add(-28*time.Minute)) {
		t.Fatalf("events = %+v, %v", events, err)
	}
	// The phone's own rule tick arriving later is not kept twice.
	recorded, err := RuleTick(ctx, database, database, id, "", now.Add(-27*time.Minute), "device:phone-1", now)
	if err != nil || recorded {
		t.Fatalf("phone's tick after the server's = %v, %v", recorded, err)
	}
	// The owner unticks it: the rule leaves the day alone.
	if err := TickHabit(ctx, database, database, id, budget.ActionUntick, "", "owner", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if ticked, err := EvaluateHabitRules(ctx, database, database, now.Add(5*time.Minute)); err != nil || ticked != 0 {
		t.Fatalf("after the untick = %d, %v", ticked, err)
	}
	if events, _ = database.HabitEvents(ctx, budget.DayOf(config, now)); len(events) != 2 {
		t.Fatalf("events after the untick = %+v", events)
	}
}
