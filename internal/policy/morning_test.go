package policy

import (
	"testing"
	"time"
)

func TestMorningHoldsFromWakeUntilScannedOrItsEnd(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Istanbul")
	at := func(hour, minute int) time.Time { return time.Date(2026, 10, 26, hour, minute, 0, 0, location) }
	config := outsideConfig(OutsideRule{})
	config.Morning = MorningRule{Enabled: true, FromWake: true, Until: "12:00", LockAll: true}
	if err := config.Normalize(); err != nil {
		t.Fatal(err)
	}
	wake := at(8, 0)
	facts := Facts{PlanExists: true, WakeAt: &wake}

	if before := Evaluate(config, 1, facts, at(7, 30)); before.Mode == ModeMorning {
		t.Fatalf("held before waking: %+v", before)
	}
	held := Evaluate(config, 1, facts, at(8, 30))
	if held.Mode != ModeMorning || !held.BlockAll {
		t.Fatalf("after waking: %+v", held)
	}
	// A work block does not get past it, and an opening does not lift it.
	work := facts
	work.Blocks = []Block{{ID: 1, Title: "write", Start: at(8, 0), End: at(10, 0), Kind: BlockKindWork, HasTask: true}}
	work.Openings = []Opening{{ID: 1, Purpose: "x", All: true, Until: at(11, 0)}}
	if decision := Evaluate(config, 1, work, at(8, 30)); decision.Mode != ModeMorning || !decision.BlockAll {
		t.Fatalf("work block or opening got past the morning: %+v", decision)
	}
	// The emergency unlock still opens everything.
	emergency := facts
	emergency.Emergency = &Emergency{StartedAt: at(8, 20), Until: at(8, 40)}
	if decision := Evaluate(config, 1, emergency, at(8, 30)); decision.Mode != ModeEmergency {
		t.Fatalf("emergency: %+v", decision)
	}
	// A scan after waking opens the day; one from before does not.
	early := at(7, 0)
	stale := facts
	stale.MorningScannedAt = &early
	if decision := Evaluate(config, 1, stale, at(8, 30)); decision.Mode != ModeMorning {
		t.Fatalf("a scan before waking opened the day: %+v", decision)
	}
	scanned := at(8, 20)
	facts.MorningScannedAt = &scanned
	if decision := Evaluate(config, 1, facts, at(8, 30)); decision.Mode == ModeMorning {
		t.Fatalf("scanned: %+v", decision)
	}
	// It always gives up at its end.
	facts.MorningScannedAt = nil
	if decision := Evaluate(config, 1, facts, at(12, 0)); decision.Mode == ModeMorning {
		t.Fatalf("after its end: %+v", decision)
	}
	// With no known wake nothing is held.
	if decision := Evaluate(config, 1, Facts{PlanExists: true}, at(9, 0)); decision.Mode == ModeMorning {
		t.Fatalf("no wake: %+v", decision)
	}
}

func TestMorningFromAFixedTimeAndWithoutEssentials(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Istanbul")
	config := outsideConfig(OutsideRule{})
	config.Morning = MorningRule{Enabled: true, From: "07:00", Until: "11:00", LockAll: true}
	config.Essentials = nil
	if err := config.Normalize(); err != nil {
		t.Fatal(err)
	}
	decision := Evaluate(config, 1, Facts{PlanExists: true}, time.Date(2026, 10, 26, 7, 30, 0, 0, location))
	// With nothing essential only the distractions close.
	if decision.Mode != ModeMorning || decision.BlockAll || len(decision.BlockedGroups) != 1 {
		t.Fatalf("decision: %+v", decision)
	}
}

func TestMorningRuleNeedsAnEnd(t *testing.T) {
	for _, rule := range []MorningRule{
		{Enabled: true, FromWake: true},
		{Enabled: true, Until: "11:00"},
		{Enabled: true, From: "11:00", Until: "10:00"},
	} {
		if err := rule.Normalize(); err == nil {
			t.Fatalf("accepted %+v", rule)
		}
	}
	if err := (MorningRule{FromWake: true}).Normalize(); err != nil {
		t.Fatalf("a disabled rule keeps its settings: %v", err)
	}
}

func TestLooserMorningRuleWaitsForTheNextDay(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Istanbul")
	now := time.Date(2026, 10, 26, 9, 0, 0, 0, location)
	config := outsideConfig(OutsideRule{})
	current := MorningRule{Enabled: true, FromWake: true, Until: "12:00", LockAll: true}
	config = config.WithMorning(current, now)

	for _, looser := range []MorningRule{
		{},
		{Enabled: true, FromWake: true, Until: "11:00", LockAll: true},
		{Enabled: true, FromWake: true, Until: "12:00"},
		{Enabled: true, From: "06:00", Until: "12:00", LockAll: true},
	} {
		next := config.WithMorning(looser, now)
		if next.MorningAt(now) != current || next.MorningNextFrom != "2026-10-27" || next.MorningAt(now.Add(24*time.Hour)) != looser {
			t.Fatalf("looser %+v applied today: %+v", looser, next)
		}
	}
	stricter := MorningRule{Enabled: true, FromWake: true, Until: "13:00", LockAll: true}
	if next := config.WithMorning(stricter, now); next.MorningAt(now) != stricter || next.MorningNext != nil {
		t.Fatalf("stricter waited: %+v", next)
	}
}
