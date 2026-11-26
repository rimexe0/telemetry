package policy

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// ownerBudget is the shape of the owner's numbers in the contract, written
// out for tests only: the server seeds none of them.
func ownerBudget() Budget {
	return Budget{Enabled: true, BaseMinutes: 120, MaxMinutes: 300, FloorMinutes: 30, HabitsForMax: 6,
		RateDistraction: 1, RateDay: 3, RateBedtime: 10, RateGrayscaleOff: 3, BorrowMultiplier: 2,
		WindowMinutes: 60, WindowUseMinutes: 10, GraceMinutes: 1, WaitMinutes: 10}
}

func budgetConfig() Config {
	config := outsideConfig(OutsideRule{})
	config.DistractionTimes = []Window{{Start: "19:00", End: "00:00"}}
	config.Bedtime = Bedtime{Enabled: true, Start: "00:00", End: "08:00", WindDownMinutes: 60}
	config.Budget = ownerBudget()
	if err := config.Normalize(); err != nil {
		panic(err)
	}
	return config
}

func TestBudgetIsValidatedAndNeedsClosedByDefault(t *testing.T) {
	config := budgetConfig()
	config.ClosedByDefault = false
	if err := config.Normalize(); !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), "closed_by_default") {
		t.Fatalf("budget without closed by default = %v", err)
	}
	for name, change := range map[string]func(*Budget){
		"floor above base":       func(b *Budget) { b.FloorMinutes = 200 },
		"base above max":         func(b *Budget) { b.BaseMinutes = 400 },
		"no habits for max":      func(b *Budget) { b.HabitsForMax = 0 },
		"zero rate":              func(b *Budget) { b.RateDay = 0 },
		"multiplier below one":   func(b *Budget) { b.BorrowMultiplier = 0.5 },
		"use longer than window": func(b *Budget) { b.WindowUseMinutes = 61 },
	} {
		budget := ownerBudget()
		change(&budget)
		if err := budget.Normalize(); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A disabled budget keeps its numbers and asks nothing.
	off := Budget{BaseMinutes: 999}
	if err := off.Normalize(); err != nil {
		t.Fatal(err)
	}
	config = outsideConfig(OutsideRule{})
	config.ClosedByDefault = false
	config.Budget = Budget{BaseMinutes: 120}
	if err := config.Normalize(); err != nil {
		t.Fatalf("a disabled budget must not need closed by default: %v", err)
	}
}

func TestBudgetLooserIsEveryWayOfAskingLess(t *testing.T) {
	current := ownerBudget()
	for name, test := range map[string]struct {
		change func(*Budget)
		looser bool
	}{
		"off":                     {func(b *Budget) { b.Enabled = false }, true},
		"higher base":             {func(b *Budget) { b.BaseMinutes = 130 }, true},
		"higher max":              {func(b *Budget) { b.MaxMinutes = 310 }, true},
		"higher floor":            {func(b *Budget) { b.FloorMinutes = 40 }, true},
		"fewer habits for max":    {func(b *Budget) { b.HabitsForMax = 5 }, true},
		"lower distraction rate":  {func(b *Budget) { b.RateDistraction = 0.5 }, true},
		"lower day rate":          {func(b *Budget) { b.RateDay = 2 }, true},
		"lower bedtime rate":      {func(b *Budget) { b.RateBedtime = 9 }, true},
		"lower grayscale rate":    {func(b *Budget) { b.RateGrayscaleOff = 2 }, true},
		"lower multiplier":        {func(b *Budget) { b.BorrowMultiplier = 1.5 }, true},
		"shorter wait":            {func(b *Budget) { b.WaitMinutes = 5 }, true},
		"longer window allowance": {func(b *Budget) { b.WindowUseMinutes = 15 }, true},
		"longer grace":            {func(b *Budget) { b.GraceMinutes = 2 }, true},
		"shorter window":          {func(b *Budget) { b.WindowMinutes = 45 }, true},
		"the same":                {func(b *Budget) {}, false},
		"lower base":              {func(b *Budget) { b.BaseMinutes = 100 }, false},
		"higher rates":            {func(b *Budget) { b.RateDay = 4; b.RateBedtime = 12 }, false},
		"longer wait":             {func(b *Budget) { b.WaitMinutes = 15 }, false},
		"longer window":           {func(b *Budget) { b.WindowMinutes = 90 }, false},
	} {
		next := current
		test.change(&next)
		if got := current.Looser(next); got != test.looser {
			t.Errorf("%s: looser = %v", name, got)
		}
	}
	if (Budget{}).Looser(Budget{Enabled: true}) {
		t.Fatal("switching an off budget on asks more")
	}
}

func TestLooserBudgetWaitsForTheWakeTimeAndSwitchingOnRecordsSince(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Istanbul")
	now := time.Date(2026, 11, 26, 23, 59, 0, 0, location)
	config := budgetConfig()
	config.Budget = Budget{}
	on := config.WithBudget(ownerBudget(), now)
	if !on.BudgetAt(now).Enabled || on.BudgetSince == nil || !on.BudgetSince.Equal(now) {
		t.Fatalf("switching on applies at once and records when: %+v", on.BudgetSince)
	}
	off := on.WithBudget(Budget{}, now)
	if !off.BudgetAt(now.Add(2 * time.Hour)).Enabled {
		t.Fatal("switching off at night must wait through the night")
	}
	if off.BudgetAt(time.Date(2026, 11, 27, 8, 0, 0, 0, location)).Enabled {
		t.Fatal("switching off takes effect at the wake time after bedtime")
	}
	stricter := ownerBudget()
	stricter.RateDay = 4
	if applied := on.WithBudget(stricter, now); applied.BudgetAt(now).RateDay != 4 || applied.BudgetNext != nil {
		t.Fatal("asking more applies at once")
	}
}

// The door opens through every hold, bedtime included, like the emergency
// unlock it replaces; only from when it opens until its end.
func TestDoorOpensThroughHoldsAndBedtime(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Istanbul")
	at := func(day, hour, minute int) time.Time { return time.Date(2026, 11, day, hour, minute, 0, 0, location) }
	config := budgetConfig()
	config.Outside = OutsideRule{Enabled: true, Minutes: 30, Meters: 100, Holds: []string{HoldLock}}
	if err := config.Normalize(); err != nil {
		t.Fatal(err)
	}
	door := &DoorFact{ID: 7, OpensAt: at(26, 15, 10), WindowEnd: at(26, 16, 10), EndsAt: at(26, 15, 21)}
	facts := Facts{Door: door}
	if waiting := Evaluate(config, 1, facts, at(26, 15, 5)); waiting.Mode != ModeOutside {
		t.Fatalf("during the wait = %+v", waiting)
	}
	open := Evaluate(config, 1, facts, at(26, 15, 15))
	if open.Mode != ModeDoor || open.BlockAll || len(open.BlockedGroups) != 0 || open.Reason != "Opened through the door until 15:21." || open.Basis[0] != "door:7" {
		t.Fatalf("door open = %+v", open)
	}
	if after := Evaluate(config, 1, facts, at(26, 15, 21)); after.Mode != ModeOutside {
		t.Fatalf("after the door's end = %+v", after)
	}
	night := &DoorFact{ID: 8, OpensAt: at(27, 1, 0), WindowEnd: at(27, 2, 0), EndsAt: at(27, 1, 11)}
	inBed := Evaluate(config, 1, Facts{Door: night}, at(27, 1, 5))
	if inBed.Mode != ModeDoor || inBed.LockScreen || inBed.BlockAll {
		t.Fatalf("door in bedtime = %+v", inBed)
	}
	if !inBed.Grayscale {
		t.Fatal("the door does not end the wind-down's grayscale")
	}
	if locked := Evaluate(config, 1, Facts{Door: night}, at(27, 1, 11)); locked.Mode != ModeBedtime || !locked.LockScreen {
		t.Fatalf("bedtime after the door = %+v", locked)
	}
	segments := Timeline(config, 1, Facts{Door: night}, at(27, 0, 30), at(27, 2, 0))
	got := []string{}
	for _, segment := range segments {
		got = append(got, segment.From.In(location).Format("15:04")+" "+segment.Decision.Mode)
	}
	if strings.Join(got, "|") != "00:30 bedtime|01:00 door|01:11 bedtime" {
		t.Fatalf("timeline = %v", got)
	}
}

// Inside a distraction time distractions stay open only until the budget's
// sliding end; a later distraction time belongs to the next budget day.
func TestDistractionTimeClosesAtTheBudgetsEnd(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Istanbul")
	at := func(day, hour, minute int) time.Time { return time.Date(2026, 11, day, hour, minute, 0, 0, location) }
	config := budgetConfig()
	facts := Facts{Budget: &BudgetFact{EndsAt: at(26, 21, 30), DayEnd: at(27, 8, 0)}}
	if open := Evaluate(config, 1, facts, at(26, 20, 0)); open.Mode != ModeLeisure || len(open.BlockedGroups) != 0 {
		t.Fatalf("with budget left = %+v", open)
	}
	used := Evaluate(config, 1, facts, at(26, 21, 30))
	if used.Mode != ModeBudgetUsed || used.Reason != "Today's distraction time is used up." || len(used.BlockedGroups) == 0 {
		t.Fatalf("budget used = %+v", used)
	}
	if next := Evaluate(config, 1, facts, at(27, 19, 30)); next.Mode != ModeLeisure {
		t.Fatalf("the next day's distraction time = %+v", next)
	}
	// Daytime stays closed as before.
	if day := Evaluate(config, 1, facts, at(26, 15, 0)); day.Mode != ModeClosed {
		t.Fatalf("daytime = %+v", day)
	}
	spent := Facts{Budget: &BudgetFact{Used: true, EndsAt: at(26, 18, 0), DayEnd: at(27, 8, 0)}}
	if closed := Evaluate(config, 1, spent, at(26, 19, 0)); closed.Mode != ModeBudgetUsed {
		t.Fatalf("nothing left = %+v", closed)
	}
	// A borrowing door opens it again.
	spent.Door = &DoorFact{ID: 3, OpensAt: at(26, 19, 10), WindowEnd: at(27, 0, 0), EndsAt: at(26, 19, 40)}
	segments := Timeline(config, 1, spent, at(26, 18, 0), at(26, 23, 0))
	got := []string{}
	for _, segment := range segments {
		got = append(got, segment.From.In(location).Format("15:04")+" "+segment.Decision.Mode)
	}
	if strings.Join(got, "|") != "18:00 closed|19:00 budget_used|19:10 door|19:40 budget_used" {
		t.Fatalf("timeline = %v", got)
	}
	timeline := Timeline(config, 1, facts, at(26, 18, 0), at(26, 23, 0))
	got = got[:0]
	for _, segment := range timeline {
		got = append(got, segment.From.In(location).Format("15:04")+" "+segment.Decision.Mode)
	}
	if strings.Join(got, "|") != "18:00 closed|19:00 leisure|21:30 budget_used" {
		t.Fatalf("timeline = %v", got)
	}
}

// A disabled budget changes nothing, even with budget facts present.
func TestDisabledBudgetChangesNothing(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Istanbul")
	at := func(hour, minute int) time.Time { return time.Date(2026, 11, 26, hour, minute, 0, 0, location) }
	config := budgetConfig()
	config.Budget.Enabled = false
	facts := Facts{Budget: &BudgetFact{Used: true, EndsAt: at(19, 0), DayEnd: at(23, 59)}}
	if open := Evaluate(config, 1, facts, at(20, 0)); open.Mode != ModeLeisure {
		t.Fatalf("disabled budget = %+v", open)
	}
	before := Timeline(config, 1, Facts{}, at(12, 0), at(23, 0))
	after := Timeline(config, 1, facts, at(12, 0), at(23, 0))
	if len(before) != len(after) {
		t.Fatalf("timeline changed: %v vs %v", before, after)
	}
	for i := range before {
		if !sameDecision(before[i].Decision, after[i].Decision) || !before[i].From.Equal(after[i].From) || !before[i].To.Equal(after[i].To) {
			t.Fatalf("segment %d changed", i)
		}
	}
}

func TestGrayscaleWantedDuringWindDownAndBedtime(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Istanbul")
	at := func(day, hour, minute int) time.Time { return time.Date(2026, 11, day, hour, minute, 0, 0, location) }
	config := budgetConfig()
	for _, test := range []struct {
		at   time.Time
		want bool
	}{{at(26, 22, 59), false}, {at(26, 23, 0), true}, {at(27, 3, 0), true}, {at(27, 8, 0), false}} {
		if got := GrayscaleWanted(config, test.at); got != test.want {
			t.Errorf("%v: %v", test.at, got)
		}
	}
	if !InBedtime(config, at(27, 1, 0)) || InBedtime(config, at(26, 23, 0)) || !WindingDown(config, at(26, 23, 30)) {
		t.Fatal("bedtime helpers")
	}
}
