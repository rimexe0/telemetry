package policy

import (
	"testing"
	"time"
)

func TestOpeningLiftsOnlyFromItsStart(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Istanbul")
	at := time.Date(2026, 10, 26, 15, 0, 0, 0, location)
	config := outsideConfig(OutsideRule{})
	facts := Facts{PlanExists: true, Openings: []Opening{{ID: 3, Purpose: "a call", All: true, From: at.Add(10 * time.Minute), Until: at.Add(40 * time.Minute)}}}

	if waiting := Evaluate(config, 1, facts, at); waiting.Mode != ModeClosed || len(waiting.BlockedGroups) != 1 {
		t.Fatalf("lifted during the wait: %+v", waiting)
	}
	if open := Evaluate(config, 1, facts, at.Add(10*time.Minute)); len(open.BlockedGroups) != 0 {
		t.Fatalf("not lifted after the wait: %+v", open)
	}
	segments := Timeline(config, 1, facts, at, at.Add(time.Hour))
	if len(segments) != 3 || !segments[1].From.Equal(at.Add(10*time.Minute).UTC()) || !segments[1].To.Equal(at.Add(40*time.Minute).UTC()) {
		t.Fatalf("segments: %+v", segments)
	}
}

func TestLooserOpeningRuleWaitsForTheNextDay(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Istanbul")
	now := time.Date(2026, 10, 26, 15, 0, 0, 0, location)
	config := outsideConfig(OutsideRule{})
	current := OpeningRule{WaitMinutes: 10, MaxMinutes: 30, PerDay: 2}
	if next := config.WithOpeningRule(current, now); next.OpeningRuleAt(now) != current || next.OpeningRuleNext != nil {
		t.Fatalf("a first rule waited: %+v", next)
	}
	config = config.WithOpeningRule(current, now)
	for _, looser := range []OpeningRule{{}, {WaitMinutes: 5, MaxMinutes: 30, PerDay: 2}, {WaitMinutes: 10, MaxMinutes: 60, PerDay: 2}, {WaitMinutes: 10, MaxMinutes: 30}} {
		next := config.WithOpeningRule(looser, now)
		if next.OpeningRuleAt(now) != current || next.OpeningRuleNextFrom != "2026-10-27" || next.OpeningRuleAt(now.Add(24*time.Hour)) != looser {
			t.Fatalf("looser %+v applied today: %+v", looser, next)
		}
		// Saving the rule in force again keeps the looser one waiting.
		if again := next.WithOpeningRule(current, now); again.OpeningRuleNext == nil {
			t.Fatalf("resaving dropped the waiting rule: %+v", again)
		}
	}
	stricter := OpeningRule{WaitMinutes: 15, MaxMinutes: 20, PerDay: 1}
	if next := config.WithOpeningRule(stricter, now); next.OpeningRuleAt(now) != stricter || next.OpeningRuleNext != nil {
		t.Fatalf("stricter waited: %+v", next)
	}
}
