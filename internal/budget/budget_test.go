package budget

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/activity"
	"github.com/rimexe0/telemetry/internal/policy"
)

var istanbul, _ = time.LoadLocation("Europe/Istanbul")

// at is local time in November 2026; the 26th is a Thursday.
func at(day, hour, minute int) time.Time {
	return time.Date(2026, 11, day, hour, minute, 0, 0, istanbul)
}

// testConfig carries the contract's numbers for tests only; the server
// seeds none of them.
func testConfig() policy.Config {
	since := at(1, 12, 0).UTC()
	config := policy.Config{
		Timezone:         "Europe/Istanbul",
		Distractions:     []policy.TargetGroup{{ID: "video", AndroidPackages: []string{"com.google.android.youtube"}, MacApps: []string{"Steam"}, Domains: []string{"youtube.com"}}},
		Essentials:       []policy.TargetGroup{{ID: "study", Domains: []string{"edu.youtube.com"}}, {ID: "telegram", AndroidPackages: []string{"org.telegram.messenger"}}},
		ClosedByDefault:  true,
		DistractionTimes: []policy.Window{{Start: "19:00", End: "00:00"}},
		OpenAddresses:    []string{"https://music.youtube.com/watch?v=calm"},
		Bedtime:          policy.Bedtime{Enabled: true, Start: "00:00", End: "08:00", WindDownMinutes: 60},
		Budget: policy.Budget{Enabled: true, BaseMinutes: 120, MaxMinutes: 300, FloorMinutes: 30, HabitsForMax: 6,
			RateDistraction: 1, RateDay: 3, RateBedtime: 10, RateGrayscaleOff: 3, BorrowMultiplier: 2,
			WindowMinutes: 60, WindowUseMinutes: 10, GraceMinutes: 1, WaitMinutes: 10},
		BudgetSince: &since,
	}
	if err := config.Normalize(); err != nil {
		panic(err)
	}
	return config
}

func phone(from, to time.Time) activity.Segment {
	return activity.Segment{Device: activity.DevicePhone, App: "com.google.android.youtube", Start: from.UTC(), End: to.UTC()}
}

func site(domain, url string, from, to time.Time) activity.Segment {
	return activity.Segment{Device: activity.DeviceMac, App: "Helium", Domain: domain, URL: url, Start: from.UTC(), End: to.UTC()}
}

func ticks(day string, ids ...int64) []HabitEvent {
	events := []HabitEvent{}
	for _, id := range ids {
		events = append(events, HabitEvent{HabitID: id, Day: day, Action: ActionTick, At: DayStart(testConfig(), day).Add(time.Hour)})
	}
	return events
}

func habits(ids ...int64) []Habit {
	result := []Habit{}
	for i, id := range ids {
		result = append(result, Habit{ID: id, Name: "habit", Position: i, CreatedAt: at(1, 10, 0).UTC()})
	}
	return result
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func sameTime(a, b time.Time) bool { return a.Sub(b).Abs() < time.Second }

func TestBudgetDayRunsFromBedtimeEndToBedtimeEnd(t *testing.T) {
	config := testConfig()
	for _, test := range []struct {
		at   time.Time
		want string
	}{
		{at(27, 1, 30), "2026-11-26"}, // use at 01:30 counts against the day before
		{at(27, 7, 59), "2026-11-26"},
		{at(27, 8, 0), "2026-11-27"},
		{at(26, 23, 59), "2026-11-26"},
	} {
		if got := DayOf(config, test.at); got != test.want {
			t.Errorf("%v: day %s, want %s", test.at, got, test.want)
		}
	}
	if !DayStart(config, "2026-11-26").Equal(at(26, 8, 0)) || !DayEnd(config, "2026-11-26").Equal(at(27, 8, 0)) {
		t.Fatal("day bounds")
	}
	if start, ok := BedtimeStart(config, "2026-11-26"); !ok || !start.Equal(at(27, 0, 0)) {
		t.Fatalf("bedtime start = %v", start)
	}
	if day, ok := TickDay(config, at(26, 23, 59)); !ok || day != "2026-11-26" {
		t.Fatal("ticking is open until bedtime")
	}
	if _, ok := TickDay(config, at(27, 0, 0)); ok {
		t.Fatal("ticking closes when bedtime starts")
	}
	// With bedtime off the day runs from 04:00 to 04:00 and closes at its end.
	config.Bedtime.Enabled = false
	if DayOf(config, at(27, 3, 59)) != "2026-11-26" || DayOf(config, at(27, 4, 0)) != "2026-11-27" {
		t.Fatal("04:00 boundary")
	}
	if !CloseAt(config, "2026-11-26").Equal(at(27, 4, 0)) {
		t.Fatal("without bedtime the day closes at its end")
	}
}

func TestPerHabitValueEarnedPenaltyFloorAndCap(t *testing.T) {
	config := testConfig()
	for _, test := range []struct {
		name                          string
		habits                        []Habit
		events                        []HabitEvent
		perHabit, earned, penalty, bg float64
	}{
		// Fewer habits than habits_for_max: all done does not reach the max.
		{"three habits, one done, one missed yesterday", habits(1, 2, 3),
			append(append(ticks("2026-11-25", 1, 2), ticks("2026-11-24", 1, 2, 3)...), ticks("2026-11-26", 1)...), 30, 30, 30, 120},
		{"eight habits all done reach the cap", habits(1, 2, 3, 4, 5, 6, 7, 8),
			append(ticks("2026-11-25", 1, 2, 3, 4, 5, 6, 7, 8), ticks("2026-11-26", 1, 2, 3, 4, 5, 6, 7, 8)...), 22.5, 180, 0, 300},
		{"eight missed yesterday stop at the floor", habits(1, 2, 3, 4, 5, 6, 7, 8), nil, 22.5, 0, 180, 30},
		{"no habits", nil, nil, 30, 0, 0, 120},
	} {
		result := Compute(Inputs{Config: config, Habits: test.habits, Events: test.events}, "2026-11-25", at(26, 12, 0))
		today := result.Today
		if !near(today.PerHabit, test.perHabit) || !near(today.Earned, test.earned) || !near(today.Penalty, test.penalty) || !near(today.Budget, test.bg) {
			t.Errorf("%s: per habit %v earned %v penalty %v budget %v", test.name, today.PerHabit, today.Earned, today.Penalty, today.Budget)
		}
	}
}

func TestRatesAndTheGrayscaleSurcharge(t *testing.T) {
	config := testConfig()
	events := []GrayscaleEvent{
		{At: at(26, 15, 0).UTC(), Action: GrayscaleDecline},  // not wanted then: ignored
		{At: at(26, 23, 10).UTC(), Action: GrayscaleDecline}, // in the wind-down
		{At: at(26, 23, 40).UTC(), Action: GrayscaleRestore},
		{At: at(26, 23, 50).UTC(), Action: GrayscaleDecline}, // kept off into bedtime
	}
	surcharges := declines(config, events)
	pieces := rates(config, surcharges, at(26, 8, 0), at(27, 9, 0))
	for _, test := range []struct {
		at    time.Time
		total float64
		kind  string
	}{
		{at(26, 15, 0), 3, RateKindDay},
		{at(26, 20, 0), 1, RateKindDistraction},
		{at(26, 23, 20), 4, RateKindDistraction},
		{at(26, 23, 45), 1, RateKindDistraction},
		{at(27, 0, 30), 13, RateKindBedtime}, // grayscale off at night is 10 + 3
		{at(27, 8, 30), 3, RateKindDay},      // the surcharge ends with bedtime
	} {
		piece, ok := pieceAt(pieces, test.at.UTC())
		if !ok || piece.rate.Total != test.total || piece.rate.Kind != test.kind {
			t.Errorf("%v: rate %+v", test.at, piece.rate)
		}
	}
	// Use is charged at the rate in force, minute by minute.
	segments := []activity.Segment{phone(at(26, 15, 0), at(26, 15, 10)), phone(at(26, 20, 0), at(26, 20, 30)), phone(at(26, 23, 15), at(26, 23, 25))}
	result := Compute(Inputs{Config: config, Segments: segments, Grayscale: events}, "2026-11-26", at(26, 23, 30))
	if !near(result.Today.Spent, 30+30+10*4) || !near(result.Today.UseMinutes, 50) {
		t.Fatalf("spent %v use %v", result.Today.Spent, result.Today.UseMinutes)
	}
	if !result.State.GrayscaleDeclined || result.State.Rate.Total != 4 || result.State.Rate.GrayscaleOff != 3 || result.State.Rate.Base != 1 {
		t.Fatalf("rate now = %+v", result.State.Rate)
	}
}

func TestOnlyMeasuredDistractionUseIsCharged(t *testing.T) {
	config := testConfig()
	afk := site("youtube.com", "https://youtube.com/watch?v=1", at(26, 15, 0), at(26, 15, 30))
	afk.AFKUnknown = true
	segments := []activity.Segment{
		afk, // presence unknown: missing data is not use
		site("edu.youtube.com", "https://edu.youtube.com/lesson", at(26, 16, 0), at(26, 16, 30)),           // essential
		site("music.youtube.com", "https://music.youtube.com/watch?v=calm", at(26, 17, 0), at(26, 17, 30)), // a page kept open
		{Device: activity.DevicePhone, App: "org.telegram.messenger", Start: at(26, 9, 0).UTC(), End: at(26, 10, 0).UTC()},
		{Device: activity.DeviceMac, App: "steam", Start: at(26, 10, 0).UTC(), End: at(26, 10, 5).UTC()}, // app names match without case
		// The phone and the Mac at once are one person: charged once.
		phone(at(26, 11, 0), at(26, 11, 10)), site("www.youtube.com", "https://www.youtube.com/", at(26, 11, 5), at(26, 11, 15)),
	}
	result := Compute(Inputs{Config: config, Segments: segments}, "2026-11-26", at(26, 18, 0))
	if !near(result.Today.UseMinutes, 5+15) || !near(result.Today.Spent, 60) {
		t.Fatalf("use %v spent %v", result.Today.UseMinutes, result.Today.Spent)
	}
	// Nothing measured, nothing charged.
	if empty := Compute(Inputs{Config: config}, "2026-11-26", at(26, 18, 0)); empty.Today.Spent != 0 || empty.Today.Left != 120 {
		t.Fatalf("no data = %+v", empty.Today)
	}
}

func TestBorrowingChargesTomorrowUntilItReachesTheFloor(t *testing.T) {
	config := testConfig()
	// 150 minutes in distraction time with 120 to spend: 30 borrowed at 1x2.
	segments := []activity.Segment{phone(at(26, 19, 0), at(26, 21, 30))}
	result := Compute(Inputs{Config: config, Segments: segments}, "2026-11-26", at(26, 21, 30))
	today := result.Today
	if today.Left != 0 || !near(today.Spent, 120) || !near(today.DebtPassed, 60) || !near(today.Borrowable, 30) || today.ExhaustedAt != nil {
		t.Fatalf("borrowed = %+v", today)
	}
	if !result.Budget.Used || result.State.Left != 0 || result.State.DebtTomorrow != 60 || result.State.Borrowable != 30 {
		t.Fatalf("state = %+v", result.State)
	}
	// 15 more minutes borrow the last 30; tomorrow is down to the floor.
	segments = append(segments, phone(at(26, 21, 30), at(26, 21, 50)))
	result = Compute(Inputs{Config: config, Segments: segments}, "2026-11-26", at(26, 22, 0))
	today = result.Today
	if !near(today.DebtPassed, 100) || today.Borrowable != 0 || today.ExhaustedAt == nil || !sameTime(*today.ExhaustedAt, at(26, 21, 45).UTC()) {
		t.Fatalf("exhausted = %+v at %v", today, today.ExhaustedAt)
	}
	if !result.State.Exhausted() || result.State.DoorAvailable || result.State.DoorUnavailableReason != "Nothing is left to spend or borrow today." {
		t.Fatalf("state = %+v", result.State)
	}
	// The next day owes it; habits ticked that day still earn.
	fresh := []Habit{{ID: 1, Name: "made that morning", CreatedAt: at(27, 9, 0).UTC()}}
	next := Compute(Inputs{Config: config, Segments: segments, Habits: fresh, Events: ticks("2026-11-27", 1)}, "2026-11-26", at(27, 10, 0))
	if !near(next.Today.Debt, 100) || !near(next.Today.Budget, 50) || !near(next.Today.Earned, 30) {
		t.Fatalf("next day = %+v", next.Today)
	}
	if floored := Compute(Inputs{Config: config, Segments: segments}, "2026-11-26", at(27, 10, 0)); !near(floored.Today.Budget, 30) {
		t.Fatalf("floored = %+v", floored.Today)
	}
	// A snapshot's debt carries into the first day worked out.
	if carried := Compute(Inputs{Config: config, DebtIn: 40}, "2026-11-27", at(27, 10, 0)); !near(carried.Today.Budget, 80) || !near(carried.Today.Debt, 40) {
		t.Fatalf("carried = %+v", carried.Today)
	}
}

func TestNothingBeforeTheBudgetWasSwitchedOn(t *testing.T) {
	config := testConfig()
	since := at(26, 12, 0).UTC()
	config.BudgetSince = &since
	segments := []activity.Segment{phone(at(25, 19, 0), at(25, 23, 59)), phone(at(26, 10, 0), at(26, 10, 10)), phone(at(26, 13, 0), at(26, 13, 10))}
	result := Compute(Inputs{Config: config, Habits: habits(1, 2), Segments: segments}, "2026-11-25", at(26, 14, 0))
	if result.Days[0].Enabled || result.Today.Penalty != 0 || result.Today.Debt != 0 || !near(result.Today.Spent, 30) {
		t.Fatalf("today = %+v", result.Today)
	}
}

func TestSlidingEndOfTheDistractionTime(t *testing.T) {
	config := testConfig()
	segments := []activity.Segment{phone(at(26, 15, 0), at(26, 15, 10)), phone(at(26, 19, 0), at(26, 19, 30))}
	// 120 - 30 - 30 = 60 left at 1x: from 21:00 it runs out at 22:00.
	result := Compute(Inputs{Config: config, Segments: segments}, "2026-11-26", at(26, 21, 0))
	if result.Budget == nil || result.Budget.Used || !sameTime(result.Budget.EndsAt, at(26, 22, 0).UTC()) || !result.Budget.DayEnd.Equal(at(27, 8, 0).UTC()) {
		t.Fatalf("budget fact = %+v", result.Budget)
	}
	// Not using distractions keeps moving it later.
	if later := Compute(Inputs{Config: config, Segments: segments}, "2026-11-26", at(26, 21, 30)); !sameTime(later.Budget.EndsAt, at(26, 22, 30).UTC()) {
		t.Fatalf("later = %v", later.Budget.EndsAt)
	}
	// Before the distraction time it counts from the time's start.
	early := Compute(Inputs{Config: config, Segments: segments[:1]}, "2026-11-26", at(26, 16, 0))
	if !sameTime(early.Budget.EndsAt, at(26, 20, 30).UTC()) {
		t.Fatalf("early = %v", early.Budget.EndsAt)
	}
	// It ends with the distraction time when the budget lasts longer.
	if long := Compute(Inputs{Config: config}, "2026-11-26", at(26, 23, 0)); !sameTime(long.Budget.EndsAt, at(27, 8, 0).UTC()) {
		t.Fatalf("long = %v", long.Budget.EndsAt)
	}
	// The policy closes the distraction time at that end.
	decision := policy.Evaluate(config, 1, policy.Facts{Budget: result.Budget}, at(26, 22, 0))
	if decision.Mode != policy.ModeBudgetUsed {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestWindowUseGraceAndTheNextDoor(t *testing.T) {
	config := testConfig()
	door := Door{ID: 7, RequestedAt: at(26, 15, 0).UTC(), OpensAt: at(26, 15, 10).UTC(), WindowEnd: at(26, 16, 10).UTC(), UseMinutes: 10, GraceMinutes: 1}
	for _, test := range []struct {
		name     string
		use      []activity.Segment
		now      time.Time
		state    string
		used     float64
		endsAt   time.Time
		fact     bool
		reason   string
		nextDoor bool
	}{
		{"waiting", nil, at(26, 15, 5), DoorWaiting, 0, at(26, 15, 21), true, "A door is already waiting. It opens at 15:10.", true},
		{"open, 4 used", []activity.Segment{phone(at(26, 15, 10), at(26, 15, 14))}, at(26, 15, 14), DoorOpen, 4, at(26, 15, 21), true, "A door is open until 15:21.", true},
		{"not used: the end moves", nil, at(26, 15, 30), DoorOpen, 0, at(26, 15, 41), true, "A door is open until 15:41.", true},
		{"the window caps it", nil, at(26, 16, 5), DoorOpen, 0, at(26, 16, 10), true, "A door is open until 16:10.", true},
		{"grace", []activity.Segment{phone(at(26, 15, 10), at(26, 15, 20))}, at(26, 15, 20), DoorGrace, 10, at(26, 15, 21), true, "A door is open until 15:21.", true},
		{"ended, next door at the window end", []activity.Segment{phone(at(26, 15, 10), at(26, 15, 25))}, at(26, 15, 25), DoorEnded, 15, at(26, 15, 21), false, "The next door opens at 16:10.", true},
		{"window over", nil, at(26, 16, 30), DoorEnded, 0, at(26, 16, 10), false, "", false},
	} {
		result := Compute(Inputs{Config: config, Doors: []Door{door}, Segments: test.use}, "2026-11-26", test.now)
		view := result.State.Door
		if view == nil || view.State != test.state || !near(view.UsedMinutes, test.used) || !sameTime(view.EndsAt, test.endsAt.UTC()) || view.UseMinutes == nil || *view.UseMinutes != 10 {
			t.Errorf("%s: door = %+v", test.name, view)
			continue
		}
		if (result.Door != nil) != test.fact || test.fact && !sameTime(result.Door.EndsAt, test.endsAt.UTC()) {
			t.Errorf("%s: fact = %+v", test.name, result.Door)
		}
		if test.reason != "" && result.State.DoorUnavailableReason != test.reason {
			t.Errorf("%s: reason %q", test.name, result.State.DoorUnavailableReason)
		}
		if (result.State.NextDoorAt != nil) != test.nextDoor {
			t.Errorf("%s: next door %v", test.name, result.State.NextDoorAt)
		}
	}
	// A cancelled door costs nothing and leaves no wait for the next.
	cancelled := door
	cancelledAt := at(26, 15, 5).UTC()
	cancelled.CancelledAt = &cancelledAt
	result := Compute(Inputs{Config: config, Doors: []Door{cancelled}}, "2026-11-26", at(26, 15, 6))
	if result.State.Door.State != DoorCancelled || result.State.NextDoorAt != nil || !result.State.DoorAvailable || result.Door != nil {
		t.Fatalf("cancelled = %+v", result.State)
	}
}

func TestDoorEndsWhenTheBudgetAndBorrowingRunOut(t *testing.T) {
	config := testConfig()
	config.Budget.FloorMinutes = 110 // 10 to borrow
	// 114 spent at 3x in the morning leaves 6: 2 minutes today, then 10 of
	// tomorrow at 3x2 is 1 2/3 minutes, then a minute of grace.
	segments := []activity.Segment{phone(at(26, 8, 30), at(26, 9, 8))}
	door := Door{ID: 1, RequestedAt: at(26, 15, 0).UTC(), OpensAt: at(26, 15, 10).UTC(), WindowEnd: at(26, 16, 10).UTC(), UseMinutes: 10, GraceMinutes: 1}
	result := Compute(Inputs{Config: config, Doors: []Door{door}, Segments: segments}, "2026-11-26", at(26, 15, 10))
	want := at(26, 15, 14).Add(40 * time.Second).UTC()
	if !near(result.Today.Left, 6) || result.State.Door.State != DoorOpen || !sameTime(result.State.Door.EndsAt, want) {
		t.Fatalf("left %v door %+v, want end %v", result.Today.Left, result.State.Door, want)
	}
	// The window's cost comes off what the evening's distraction time has.
	if !sameTime(result.Budget.EndsAt, at(26, 19, 0).UTC()) {
		t.Fatalf("budget end = %v", result.Budget.EndsAt)
	}
}

func TestNewDoorWindowsAndWhenThereIsNoDoor(t *testing.T) {
	config := testConfig()
	state := func(now time.Time, segments ...activity.Segment) State {
		return Compute(Inputs{Config: config, Segments: segments}, "2026-11-26", now).State
	}
	door, err := NewDoor(config, state(at(26, 15, 0)), at(26, 15, 0))
	if err != nil || !door.OpensAt.Equal(at(26, 15, 10).UTC()) || !door.WindowEnd.Equal(at(26, 16, 10).UTC()) || door.InDistractionTime || door.Borrowing || door.UseMinutes != 10 || door.GraceMinutes != 1 {
		t.Fatalf("door = %+v, %v", door, err)
	}
	// A window running into a distraction time stops at its start.
	if door, err := NewDoor(config, state(at(26, 18, 30)), at(26, 18, 30)); err != nil || !door.WindowEnd.Equal(at(26, 19, 0).UTC()) {
		t.Fatalf("into distraction time = %+v, %v", door, err)
	}
	if _, err := NewDoor(config, state(at(26, 18, 55)), at(26, 18, 55)); !errors.Is(err, ErrDoorUnavailable) || err.Error() != "Distraction time starts at 19:00, before a door would open." {
		t.Fatalf("too close to distraction time = %v", err)
	}
	if _, err := NewDoor(config, state(at(26, 20, 0)), at(26, 20, 0)); !errors.Is(err, ErrDoorUnavailable) || err.Error() != "Distraction time is open now; no door is needed." {
		t.Fatalf("in distraction time with budget left = %v", err)
	}
	// Inside a distraction time with nothing left, a door borrows until the
	// distraction time ends, with no window allowance.
	spent := state(at(26, 21, 0), phone(at(26, 19, 0), at(26, 21, 0)))
	door, err = NewDoor(config, spent, at(26, 21, 0))
	if err != nil || !door.InDistractionTime || !door.Borrowing || !door.WindowEnd.Equal(at(27, 0, 0).UTC()) {
		t.Fatalf("borrowing door = %+v, %v", door, err)
	}
	door.ID = 2
	result := Compute(Inputs{Config: config, Doors: []Door{door}, Segments: []activity.Segment{phone(at(26, 19, 0), at(26, 21, 0))}}, "2026-11-26", at(26, 21, 10))
	// 90 of tomorrow at 1x2 pays 45 minutes, then a minute of grace.
	if view := result.State.Door; view.UseMinutes != nil || view.State != DoorOpen || !sameTime(view.EndsAt, at(26, 21, 56).UTC()) {
		t.Fatalf("borrowing view = %+v", view)
	}
	encoded, _ := json.Marshal(result.State.Door)
	var shape map[string]any
	json.Unmarshal(encoded, &shape)
	if value, ok := shape["use_minutes"]; !ok || value != nil {
		t.Fatalf("use_minutes must be null inside a distraction time: %s", encoded)
	}
	config.Budget.Enabled = false
	if _, err := NewDoor(config, Compute(Inputs{Config: config}, "2026-11-26", at(26, 15, 0)).State, at(26, 15, 0)); !errors.Is(err, ErrDoorUnavailable) || err.Error() != "The budget is off." {
		t.Fatalf("budget off = %v", err)
	}
}

func TestStateShowsTodaysHabitsReasonsAndSettings(t *testing.T) {
	config := testConfig()
	list := habits(1, 2)
	list[1].Name, list[1].Position = "walk", -1
	events := append(ticks("2026-11-25", 1, 2), ticks("2026-11-26", 1)...)
	result := Compute(Inputs{Config: config, Habits: list, Events: events, Reasons: []string{"to make music"}}, "2026-11-26", at(26, 12, 0))
	state := result.State
	want := []HabitValue{{ID: 2, Name: "walk", Done: false, Value: 30}, {ID: 1, Name: "habit", Done: true, Value: 30}}
	if !reflect.DeepEqual(state.Habits, want) || state.Reasons[0] != "to make music" || state.Settings.WaitMinutes != 10 || state.Settings.BorrowMultiplier != 2 {
		t.Fatalf("state = %+v", state)
	}
	if state.Day != "2026-11-26" || state.Budget != 150 || state.Left != 150 || state.InDistractionTime || state.NextDistractionTime == nil ||
		!state.NextDistractionTime.Equal(at(26, 19, 0).UTC()) || state.BedtimeAt == nil || !state.BedtimeAt.Equal(at(27, 0, 0).UTC()) || !state.DoorAvailable || state.Door != nil {
		t.Fatalf("state = %+v", state)
	}
	encoded, _ := json.Marshal(state)
	var shape map[string]any
	json.Unmarshal(encoded, &shape)
	for _, key := range []string{"enabled", "day", "budget", "spent", "left", "earned", "penalty", "debt", "debt_tomorrow", "borrowable", "rate",
		"in_distraction_time", "next_distraction_time", "bedtime_at", "wind_down", "grayscale_declined", "door", "door_available",
		"door_unavailable_reason", "next_door_at", "habits", "reasons", "settings"} {
		if _, ok := shape[key]; !ok {
			t.Errorf("budget is missing %q: %s", key, encoded)
		}
	}
}

func TestArchivedAndWeekdayHabits(t *testing.T) {
	config := testConfig()
	archived := at(26, 10, 0).UTC()
	list := []Habit{
		{ID: 1, Name: "weekdays", Weekdays: Weekdays{1, 2, 3, 4, 5}, CreatedAt: at(1, 0, 0).UTC()},
		{ID: 2, Name: "sundays", Weekdays: Weekdays{0}, CreatedAt: at(1, 0, 0).UTC()},
		{ID: 3, Name: "archived today, not done", CreatedAt: at(1, 0, 0).UTC(), ArchivedAt: &archived},
		{ID: 4, Name: "made tomorrow", CreatedAt: at(27, 9, 0).UTC()},
	}
	result := Compute(Inputs{Config: config, Habits: list}, "2026-11-26", at(27, 1, 0))
	if !reflect.DeepEqual(result.Today.Scheduled, []int64{1}) || !reflect.DeepEqual(result.Today.Missed, []int64{1}) || !result.Today.Closed {
		t.Fatalf("today = %+v", result.Today)
	}
	var days Weekdays
	if err := json.Unmarshal([]byte(`[7, "mon", "Tuesday", 3, 3]`), &days); err != nil || !reflect.DeepEqual(days, Weekdays{0, 1, 2, 3}) {
		t.Fatalf("weekdays = %v, %v", days, err)
	}
	if err := json.Unmarshal([]byte(`[0,1,2,3,4,5,6]`), &days); err != nil || len(days) != 0 {
		t.Fatalf("every day = %v", days)
	}
	if err := json.Unmarshal([]byte(`[9]`), &days); err == nil {
		t.Fatal("a weekday out of range must be refused")
	}
	if encoded, _ := json.Marshal(Habit{}); string(encoded) != `{"id":0,"name":"","position":0,"weekdays":[],"created_at":"0001-01-01T00:00:00Z"}` {
		t.Fatalf("habit = %s", encoded)
	}
}

func TestHeatmapCellsAndRuns(t *testing.T) {
	config := testConfig()
	list := []Habit{
		{ID: 1, Name: "daily", CreatedAt: at(20, 10, 0).UTC()},
		{ID: 2, Name: "mon and wed", Weekdays: Weekdays{1, 3}, CreatedAt: at(20, 10, 0).UTC()},
		{ID: 3, Name: "new", CreatedAt: at(24, 10, 0).UTC()},
	}
	events := []HabitEvent{}
	for _, day := range []string{"2026-11-20", "2026-11-21", "2026-11-23", "2026-11-24", "2026-11-25"} {
		events = append(events, ticks(day, 1)...)
	}
	events = append(events, ticks("2026-11-23", 2)...)
	events = append(events, ticks("2026-11-25", 2)...)
	events = append(events, ticks("2026-11-24", 3)...)
	// A tick undone the same day leaves it undone.
	events = append(events, HabitEvent{HabitID: 3, Day: "2026-11-25", Action: ActionTick, At: at(25, 9, 0).UTC()},
		HabitEvent{HabitID: 3, Day: "2026-11-25", Action: ActionUntick, At: at(25, 9, 5).UTC()})
	heatmap := BuildHeatmap(config, list, events, "2026-11-20", at(26, 21, 0))
	daily, weekly, fresh := heatmap.Habits[0], heatmap.Habits[1], heatmap.Habits[2]
	if want := map[string]string{"2026-11-20": "done", "2026-11-21": "done", "2026-11-22": "missed", "2026-11-23": "done", "2026-11-24": "done", "2026-11-25": "done"}; !reflect.DeepEqual(daily.Days, want) {
		t.Fatalf("daily = %v", daily.Days)
	}
	if daily.CurrentRun != 3 || daily.LongestRun != 3 {
		t.Fatalf("daily runs = %d, %d", daily.CurrentRun, daily.LongestRun)
	}
	// Days it does not apply do not break its run.
	if weekly.Days["2026-11-24"] != "unscheduled" || weekly.Days["2026-11-25"] != "done" || weekly.CurrentRun != 2 || weekly.LongestRun != 2 {
		t.Fatalf("weekly = %+v", weekly)
	}
	if fresh.Days["2026-11-23"] != "none" || fresh.Days["2026-11-24"] != "done" || fresh.Days["2026-11-25"] != "missed" || fresh.CreatedDay != "2026-11-24" || fresh.CurrentRun != 0 {
		t.Fatalf("new = %+v", fresh)
	}
	if heatmap.Combined["2026-11-25"] != 0.67 || heatmap.Combined["2026-11-22"] != 0 {
		t.Fatalf("combined = %v", heatmap.Combined)
	}
	if _, ok := heatmap.Combined["2026-11-26"]; ok {
		t.Fatal("today shows only once something is done")
	}
}

func TestSnapshotOfAClosedDay(t *testing.T) {
	config := testConfig()
	result := Compute(Inputs{Config: config, Habits: habits(1, 2), Events: append(ticks("2026-11-25", 1, 2), ticks("2026-11-26", 1)...), Segments: []activity.Segment{phone(at(26, 19, 0), at(26, 23, 0))}}, "2026-11-26", at(27, 8, 0))
	day := result.Days[0]
	snapshot := SnapshotOf(day)
	if snapshot.Day != "2026-11-26" || snapshot.Budget != 150 || snapshot.Spent != 150 || snapshot.DebtPassed != 180 || !reflect.DeepEqual(snapshot.HabitsMissed, []int64{2}) ||
		!reflect.DeepEqual(snapshot.HabitsDone, []int64{1}) || !snapshot.Settings.Enabled || !snapshot.ClosedAt.Equal(at(27, 8, 0).UTC()) {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}
