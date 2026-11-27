package budget

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/places"
)

// testPlace is a made-up place for tests, not the owner's.
var testPlace = RulePlace{Name: "Test pier", Lat: 41.0, Lon: 29.0}

func placeRule(from, until string, minutes int) HabitRule {
	place := testPlace
	rule := HabitRule{Kind: RuleKindPlace, Place: &place, Window: RuleWindow{From: from, Until: until}, Minutes: minutes}
	if err := rule.Normalize(); err != nil {
		panic(err)
	}
	return rule
}

// fixAt is a position about meters north of the test place.
func fixAt(t time.Time, meters float64) places.Fix {
	return places.Fix{At: t.UTC(), Lat: testPlace.Lat + meters/111_195, Lon: testPlace.Lon, Accuracy: 10}
}

func TestRuleNormalizeFillsDefaultsAndRefusesNonsense(t *testing.T) {
	place := testPlace
	rule := HabitRule{Kind: "place", Place: &place, Window: RuleWindow{From: "wake", Until: "11:00"}}
	if err := rule.Normalize(); err != nil || rule.RadiusM != 100 || rule.Minutes != 1 {
		t.Fatalf("defaults = %+v, %v", rule, err)
	}
	bad := []HabitRule{
		{Kind: "steps", Place: &place, Window: RuleWindow{From: "wake", Until: "11:00"}},
		{Kind: "place", Window: RuleWindow{From: "wake", Until: "11:00"}},
		{Kind: "place", Place: &RulePlace{Name: "x", Lat: 91, Lon: 0}, Window: RuleWindow{From: "wake", Until: "11:00"}},
		{Kind: "place", Place: &RulePlace{Name: " ", Lat: 41, Lon: 29}, Window: RuleWindow{From: "wake", Until: "11:00"}},
		{Kind: "place", Place: &place, RadiusM: 5, Window: RuleWindow{From: "wake", Until: "11:00"}},
		{Kind: "place", Place: &place, Window: RuleWindow{From: "wake", Until: "11"}},
		{Kind: "place", Place: &place, Window: RuleWindow{From: "sunrise", Until: "11:00"}},
		{Kind: "place", Place: &place, Window: RuleWindow{From: "09:00", Until: "09:00"}},
		{Kind: "place", Place: &place, Minutes: 500, Window: RuleWindow{From: "wake", Until: "11:00"}},
	}
	for i, rule := range bad {
		if err := rule.Normalize(); !errors.Is(err, ErrInvalidRule) {
			t.Fatalf("bad rule %d accepted: %+v", i, rule)
		}
	}
}

func TestRuleFieldTellsAbsentNullAndSetApart(t *testing.T) {
	var input struct {
		Rule RuleField `json:"rule"`
	}
	if err := json.Unmarshal([]byte(`{}`), &input); err != nil || input.Rule.Set {
		t.Fatalf("absent = %+v, %v", input.Rule, err)
	}
	if err := json.Unmarshal([]byte(`{"rule": null}`), &input); err != nil || !input.Rule.Set || input.Rule.Rule != nil {
		t.Fatalf("null = %+v, %v", input.Rule, err)
	}
	if err := json.Unmarshal([]byte(`{"rule": {"kind": "place", "place": {"name": "p", "lat": 41, "lon": 29}, "window": {"from": "wake", "until": "11:00"}}}`), &input); err != nil ||
		!input.Rule.Set || input.Rule.Rule == nil || input.Rule.Rule.Place.Name != "p" {
		t.Fatalf("set = %+v, %v", input.Rule, err)
	}
}

func TestSpanFromWakeFallsBackToTheDayStart(t *testing.T) {
	config := testConfig() // bedtime 00:00 to 08:00
	rule := placeRule("wake", "11:00", 1)
	span, ok := SpanOn(config, rule, "2026-11-26", nil)
	if !ok || span.WakeKnown || !span.From.Equal(at(26, 8, 0).UTC()) || !span.Until.Equal(at(26, 11, 0).UTC()) {
		t.Fatalf("no wake = %+v %v", span, ok)
	}
	wake := at(26, 9, 12).UTC()
	if span, ok = SpanOn(config, rule, "2026-11-26", &wake); !ok || !span.WakeKnown || !span.From.Equal(wake) {
		t.Fatalf("wake 09:12 = %+v %v", span, ok)
	}
	// Up before bedtime ended: the day, and its ticks, start at 08:00.
	early := at(26, 7, 10).UTC()
	if span, ok = SpanOn(config, rule, "2026-11-26", &early); !ok || !span.WakeKnown || !span.From.Equal(at(26, 8, 0).UTC()) {
		t.Fatalf("wake 07:10 = %+v %v", span, ok)
	}
	// Yesterday's wake is not today's.
	old := at(25, 8, 30).UTC()
	if span, ok = SpanOn(config, rule, "2026-11-26", &old); !ok || span.WakeKnown {
		t.Fatalf("yesterday's wake = %+v %v", span, ok)
	}
	// Woken after the window ended: nothing left of it.
	late := at(26, 11, 30).UTC()
	if _, ok = SpanOn(config, rule, "2026-11-26", &late); ok {
		t.Fatal("a wake after the window still left a window")
	}
	fixed := placeRule("09:30", "10:00", 1)
	if span, ok = SpanOn(config, fixed, "2026-11-26", &wake); !ok || !span.From.Equal(at(26, 9, 30).UTC()) || span.WakeKnown {
		t.Fatalf("fixed = %+v %v", span, ok)
	}
}

func TestPlaceReachedNeedsTheMinutesInsideTheWindow(t *testing.T) {
	rule := placeRule("wake", "11:00", 3)
	from, until := at(26, 8, 0), at(26, 11, 0)
	walk := []places.Fix{
		fixAt(at(26, 8, 40), 400), // at home
		fixAt(at(26, 8, 44), 60),  // inside
		fixAt(at(26, 8, 45), 20),
		fixAt(at(26, 8, 46), 10),
		fixAt(at(26, 8, 47), 30),
		fixAt(at(26, 8, 50), 400),
	}
	reached := PlaceReached(rule, [][]places.Fix{walk}, from, until)
	if reached == nil || !reached.Equal(at(26, 8, 47).UTC()) {
		t.Fatalf("reached = %v", reached)
	}
	// Two minutes inside is not three.
	if got := PlaceReached(rule, [][]places.Fix{walk[:4]}, from, until); got != nil {
		t.Fatalf("two minutes reached = %v", got)
	}
	// Outside the radius the whole time.
	near := []places.Fix{fixAt(at(26, 9, 0), 150), fixAt(at(26, 9, 5), 140), fixAt(at(26, 9, 10), 130)}
	if got := PlaceReached(rule, [][]places.Fix{near}, from, until); got != nil {
		t.Fatalf("outside reached = %v", got)
	}
	// Before the window, and after it, does not count.
	early := []places.Fix{fixAt(at(26, 7, 0), 10), fixAt(at(26, 7, 30), 10), fixAt(at(26, 7, 50), 10)}
	if got := PlaceReached(rule, [][]places.Fix{early}, from, until); got != nil {
		t.Fatalf("before the window reached = %v", got)
	}
	// A stay running into the window counts from the window's start.
	across := []places.Fix{fixAt(at(26, 7, 55), 10), fixAt(at(26, 8, 5), 10)}
	if got := PlaceReached(rule, [][]places.Fix{across}, from, until); got == nil || !got.Equal(at(26, 8, 3).UTC()) {
		t.Fatalf("across the start = %v", got)
	}
	// A long gap is missing data, not a stay.
	gap := []places.Fix{fixAt(at(26, 9, 0), 10), fixAt(at(26, 9, 40), 10)}
	if got := PlaceReached(rule, [][]places.Fix{gap}, from, until); got != nil {
		t.Fatalf("a 40-minute gap reached = %v", got)
	}
	// A poor position does not count.
	poor := []places.Fix{fixAt(at(26, 9, 0), 10), fixAt(at(26, 9, 5), 10)}
	poor[1].Accuracy = 300
	if got := PlaceReached(rule, [][]places.Fix{poor}, from, until); got != nil {
		t.Fatalf("a poor position reached = %v", got)
	}
	// No positions, nothing.
	if got := PlaceReached(rule, nil, from, until); got != nil {
		t.Fatalf("no positions reached = %v", got)
	}
}

func TestPlaceReachedCountsPhoneAndWatchOnce(t *testing.T) {
	rule := placeRule("wake", "11:00", 4)
	from, until := at(26, 8, 0), at(26, 11, 0)
	phone := []places.Fix{fixAt(at(26, 9, 0), 10), fixAt(at(26, 9, 2), 10)}
	watch := []places.Fix{fixAt(at(26, 9, 1), 10), fixAt(at(26, 9, 3), 10)}
	// 09:00 to 09:03 seen by either is three minutes, not four.
	if got := PlaceReached(rule, [][]places.Fix{phone, watch}, from, until); got != nil {
		t.Fatalf("overlap counted twice: %v", got)
	}
	watch = append(watch, fixAt(at(26, 9, 5), 10))
	if got := PlaceReached(rule, [][]places.Fix{phone, watch}, from, until); got == nil || !got.Equal(at(26, 9, 4).UTC()) {
		t.Fatalf("phone and watch = %v", got)
	}
}

func TestStateShowsRuleTicksAndUnticks(t *testing.T) {
	config := testConfig()
	created := at(1, 9, 0).UTC()
	rule := placeRule("wake", "11:00", 1)
	habits := []Habit{{ID: 1, Name: "Going outside", CreatedAt: created, Rule: &rule}, {ID: 2, Name: "Read", CreatedAt: created}}
	tickAt := at(26, 8, 47).UTC()
	events := []HabitEvent{
		{ID: 1, HabitID: 1, Day: "2026-11-26", Action: ActionTick, At: tickAt, Source: "rule:server"},
		{ID: 2, HabitID: 2, Day: "2026-11-26", Action: ActionTick, At: at(26, 9, 0).UTC(), Source: "owner"},
		{ID: 3, HabitID: 2, Day: "2026-11-26", Action: ActionUntick, At: at(26, 9, 5).UTC(), Source: "owner"},
	}
	wake := at(26, 8, 20).UTC()
	state := Compute(Inputs{Config: config, Habits: habits, Events: events, WakeAt: &wake}, "2026-11-25", at(26, 10, 0)).State
	if len(state.Habits) != 2 {
		t.Fatalf("habits = %+v", state.Habits)
	}
	outside, read := state.Habits[0], state.Habits[1]
	if !outside.Done || outside.DoneBy != "rule" || outside.DoneAt == nil || !outside.DoneAt.Equal(tickAt) || outside.Rule == nil ||
		outside.Window == nil || !outside.Window.From.Equal(wake) || !outside.Window.WakeKnown {
		t.Fatalf("rule habit = %+v", outside)
	}
	if read.Done || !read.Unticked || read.Rule != nil || read.Window != nil || read.DoneAt != nil {
		t.Fatalf("unticked habit = %+v", read)
	}
}
