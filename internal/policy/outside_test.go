package policy

import (
	"testing"
	"time"
)

func outsideConfig(rule OutsideRule) Config {
	config := Config{
		Timezone:         "Europe/Istanbul",
		Distractions:     []TargetGroup{{ID: "x", AndroidPackages: []string{"com.twitter.android"}}},
		Essentials:       []TargetGroup{{ID: "telegram", AndroidPackages: []string{"org.telegram.messenger"}}},
		ClosedByDefault:  true,
		DistractionTimes: []Window{{Start: "21:00", End: "00:00"}},
		Outside:          rule,
	}
	if err := config.Normalize(); err != nil {
		panic(err)
	}
	return config
}

func TestOutsideRuleHoldsDistractionTimeUntilMet(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Istanbul")
	evening := time.Date(2026, 10, 15, 21, 30, 0, 0, location)
	config := outsideConfig(OutsideRule{Enabled: true, Minutes: 20, Meters: 200, Holds: []string{HoldDistractions}})

	decision := Evaluate(config, 1, Facts{OutsideMinutes: 12}, evening)
	if decision.Mode != ModeOutside || decision.BlockAll || len(decision.BlockedGroups) != 1 {
		t.Fatalf("12 of 20 minutes: %+v", decision)
	}
	if decision.Reason != "Go outside first: 12 of 20 minutes today." {
		t.Fatalf("reason = %q", decision.Reason)
	}
	if met := Evaluate(config, 1, Facts{OutsideMinutes: 20}, evening); met.Mode != ModeLeisure {
		t.Fatalf("met: %+v", met)
	}
	// An opening from the assistant does not lift it, unless allowed.
	opening := Facts{OutsideMinutes: 0, Openings: []Opening{{ID: 1, Purpose: "please", All: true, Until: evening.Add(time.Hour)}}}
	if held := Evaluate(config, 1, opening, evening); len(held.BlockedGroups) != 1 {
		t.Fatalf("opening lifted the outside rule: %+v", held)
	}
	config.Outside.AllowOpenings = true
	if lifted := Evaluate(config, 1, opening, evening); len(lifted.BlockedGroups) != 0 {
		t.Fatalf("an allowed opening did not lift it: %+v", lifted)
	}
	// An emergency still opens everything.
	emergency := Facts{Emergency: &Emergency{StartedAt: evening.Add(-time.Minute), Until: evening.Add(10 * time.Minute)}}
	if open := Evaluate(config, 1, emergency, evening); open.Mode != ModeEmergency {
		t.Fatalf("emergency: %+v", open)
	}
}

func TestOutsideLockClosesEverythingButEssentialsWithinItsHours(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Istanbul")
	config := outsideConfig(OutsideRule{Enabled: true, Minutes: 15, Meters: 200, Holds: []string{HoldLock, HoldDistractions}, From: "10:00", Until: "18:00"})
	at := func(hour int) time.Time { return time.Date(2026, 10, 15, hour, 0, 0, 0, location) }
	if locked := Evaluate(config, 1, Facts{PlanExists: true}, at(11)); locked.Mode != ModeOutside || !locked.BlockAll {
		t.Fatalf("11:00: %+v", locked)
	}
	if early := Evaluate(config, 1, Facts{}, at(9)); early.Mode == ModeOutside {
		t.Fatalf("before its hours: %+v", early)
	}
	if late := Evaluate(config, 1, Facts{}, at(19)); late.Mode == ModeOutside {
		t.Fatalf("after its hours: %+v", late)
	}
	// A work block still runs as work: a lock would close the tools.
	block := Facts{Blocks: []Block{{ID: 1, Title: "Write", Start: at(10), End: at(12), Kind: BlockKindWork}}}
	if work := Evaluate(config, 1, block, at(11)); work.Mode != ModeWork {
		t.Fatalf("during a work block: %+v", work)
	}
}

func TestOutsideFromWakeNeedsAKnownWake(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Istanbul")
	config := outsideConfig(OutsideRule{Enabled: true, Minutes: 15, Meters: 200, Holds: []string{HoldDistractions}, FromWake: true})
	evening := time.Date(2026, 10, 15, 21, 30, 0, 0, location)
	if open := Evaluate(config, 1, Facts{}, evening); open.Mode == ModeOutside {
		t.Fatalf("no known wake held it: %+v", open)
	}
	woke := time.Date(2026, 10, 15, 9, 0, 0, 0, location)
	if held := Evaluate(config, 1, Facts{WakeAt: &woke}, evening); held.Mode != ModeOutside {
		t.Fatalf("after waking: %+v", held)
	}
}

func TestLooserOutsideRulesWaitForTheNextDay(t *testing.T) {
	base := OutsideRule{Enabled: true, Minutes: 20, Meters: 200, Holds: []string{HoldDistractions, HoldLock}, OnFoot: true, Until: "20:00"}
	for name, next := range map[string]OutsideRule{
		"off":            {},
		"fewer minutes":  {Enabled: true, Minutes: 10, Meters: 200, Holds: base.Holds, OnFoot: true, Until: "20:00"},
		"shorter":        {Enabled: true, Minutes: 20, Meters: 100, Holds: base.Holds, OnFoot: true, Until: "20:00"},
		"one hold less":  {Enabled: true, Minutes: 20, Meters: 200, Holds: []string{HoldDistractions}, OnFoot: true, Until: "20:00"},
		"any transport":  {Enabled: true, Minutes: 20, Meters: 200, Holds: base.Holds, Until: "20:00"},
		"gives up early": {Enabled: true, Minutes: 20, Meters: 200, Holds: base.Holds, OnFoot: true, Until: "12:00"},
		"openings":       {Enabled: true, Minutes: 20, Meters: 200, Holds: base.Holds, OnFoot: true, Until: "20:00", AllowOpenings: true},
	} {
		if !base.Looser(next) {
			t.Errorf("%s should count as looser", name)
		}
	}
	tighter := OutsideRule{Enabled: true, Minutes: 30, Meters: 300, Holds: base.Holds, OnFoot: true, WatchOnly: true, Until: "22:00"}
	if base.Looser(tighter) {
		t.Error("a stricter rule counted as looser")
	}
	if (OutsideRule{}).Looser(base) {
		t.Error("switching it on counted as looser")
	}

	location, _ := time.LoadLocation("Europe/Istanbul")
	config := outsideConfig(base)
	off := OutsideRule{}
	config.OutsideNext, config.OutsideNextFrom = &off, "2026-10-16"
	if rule := config.OutsideAt(time.Date(2026, 10, 15, 23, 59, 0, 0, location)); !rule.Enabled {
		t.Fatal("the looser rule applied the same day")
	}
	if rule := config.OutsideAt(time.Date(2026, 10, 16, 0, 0, 0, 0, location)); rule.Enabled {
		t.Fatal("the looser rule did not apply the next day")
	}
}

func TestWithOutsideDefersLooserRulesAndAppliesStricterOnes(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Istanbul")
	now := time.Date(2026, 10, 15, 11, 0, 0, 0, location)
	rule := OutsideRule{Enabled: true, Minutes: 20, Meters: 200, Holds: []string{HoldDistractions}}
	config := outsideConfig(OutsideRule{})

	// Switching it on applies at once.
	config = config.WithOutside(rule, now)
	if !config.OutsideAt(now).Enabled || config.OutsideNext != nil {
		t.Fatalf("on: %+v", config)
	}
	// Switching it off waits for tomorrow; today keeps the rule.
	config = config.WithOutside(OutsideRule{}, now)
	if !config.OutsideAt(now).Enabled || config.OutsideNextFrom != "2026-10-16" {
		t.Fatalf("off today: %+v", config)
	}
	// Another unrelated schedule change today does not cancel the waiting rule.
	config = config.WithOutside(config.OutsideAt(now), now)
	if config.OutsideNext == nil || config.OutsideNextFrom != "2026-10-16" {
		t.Fatalf("the waiting rule was lost: %+v", config)
	}
	tomorrow := now.Add(24 * time.Hour)
	if config.OutsideAt(tomorrow).Enabled {
		t.Fatal("off did not apply tomorrow")
	}
	// Tomorrow the waiting rule settles.
	settled := config.WithOutside(config.OutsideAt(tomorrow), tomorrow)
	if settled.OutsideNext != nil || settled.Outside.Enabled {
		t.Fatalf("not settled: %+v", settled)
	}
	// A stricter rule while a looser one waits replaces both at once.
	stricter := rule
	stricter.Minutes = 30
	config = config.WithOutside(stricter, now)
	if config.OutsideAt(now).Minutes != 30 || config.OutsideNext != nil {
		t.Fatalf("stricter: %+v", config)
	}
}
