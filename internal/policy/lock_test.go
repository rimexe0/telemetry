package policy

import (
	"testing"
	"time"
)

func TestSettingsLockHoldsInItsTimesAndThePinOnlyInItsOwn(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Istanbul")
	at := func(hour, minute int) time.Time { return time.Date(2026, 10, 30, hour, minute, 0, 0, location) }
	config := outsideConfig(OutsideRule{})
	config.SettingsLock = SettingsLock{Enabled: true, Windows: []Window{{Start: "09:00", End: "18:00"}, {Start: "22:00", End: "02:00"}},
		PinWindows: []Window{{Start: "18:30", End: "18:45"}}, UnlockMinutes: 15}
	if err := config.Normalize(); err != nil {
		t.Fatal(err)
	}
	if locked, until := config.Locked(at(10, 0)); !locked || !until.Equal(at(18, 0)) {
		t.Fatalf("10:00 = %v until %v", locked, until)
	}
	if locked, _ := config.Locked(at(19, 0)); locked {
		t.Fatal("locked outside its times")
	}
	if locked, until := config.Locked(at(1, 0)); !locked || !until.Equal(at(2, 0)) {
		t.Fatalf("past midnight = %v until %v", locked, until)
	}
	if config.PinOpen(at(10, 0)) || !config.PinOpen(at(18, 40)) {
		t.Fatal("the PIN works only in its own times")
	}
	config.SettingsLock.Windows = nil
	if locked, until := config.Locked(at(19, 0)); !locked || !until.IsZero() {
		t.Fatal("a lock with no times holds at all times")
	}
}

func TestLooserSettingsLock(t *testing.T) {
	current := SettingsLock{Enabled: true, Windows: []Window{{Start: "09:00", End: "18:00"}}, PinWindows: []Window{{Start: "18:30", End: "18:45"}}, UnlockMinutes: 15}
	for _, looser := range []SettingsLock{
		{},
		{Enabled: true, Windows: []Window{{Start: "10:00", End: "18:00"}}, PinWindows: current.PinWindows, UnlockMinutes: 15},
		{Enabled: true, Windows: current.Windows, UnlockMinutes: 15},
		{Enabled: true, Windows: current.Windows, PinWindows: current.PinWindows, UnlockMinutes: 30},
	} {
		if !current.Looser(looser) {
			t.Fatalf("not looser: %+v", looser)
		}
	}
	stricter := SettingsLock{Enabled: true, Windows: append([]Window{{Start: "20:00", End: "23:00"}}, current.Windows...), PinWindows: current.PinWindows, UnlockMinutes: 10}
	if current.Looser(stricter) {
		t.Fatalf("stricter counted as looser: %+v", stricter)
	}
}

func TestBedtimeLocksScreensAndKeepsEssentials(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Istanbul")
	at := func(hour, minute int) time.Time { return time.Date(2026, 10, 30, hour, minute, 0, 0, location) }
	config := outsideConfig(OutsideRule{})
	config.Bedtime = Bedtime{Enabled: true, Start: "23:30", End: "07:00"}
	if err := config.Normalize(); err != nil {
		t.Fatal(err)
	}
	for _, instant := range []time.Time{at(23, 45), at(3, 0)} {
		decision := Evaluate(config, 1, Facts{PlanExists: true, Openings: []Opening{{ID: 1, Purpose: "x", All: true, Until: instant.Add(time.Hour)}}}, instant)
		if decision.Mode != ModeBedtime || !decision.LockScreen || !decision.BlockAll || len(decision.AlwaysAllowed.AndroidPackages) == 0 {
			t.Fatalf("%v: %+v", instant, decision)
		}
	}
	if decision := Evaluate(config, 1, Facts{PlanExists: true}, at(7, 0)); decision.Mode == ModeBedtime || decision.LockScreen {
		t.Fatalf("after bedtime: %+v", decision)
	}
	emergency := Facts{Emergency: &Emergency{StartedAt: at(2, 55), Until: at(3, 5)}}
	if decision := Evaluate(config, 1, emergency, at(3, 0)); decision.Mode != ModeEmergency {
		t.Fatalf("emergency: %+v", decision)
	}
	segments := Timeline(config, 1, Facts{PlanExists: true}, at(23, 0), at(23, 59))
	if len(segments) != 2 || !segments[1].From.Equal(at(23, 30).UTC()) || !segments[1].Decision.LockScreen {
		t.Fatalf("timeline: %+v", segments)
	}
}

func TestLooserBedtime(t *testing.T) {
	current := Bedtime{Enabled: true, Start: "23:30", End: "07:00"}
	for _, looser := range []Bedtime{{}, {Enabled: true, Start: "00:00", End: "07:00"}, {Enabled: true, Start: "23:30", End: "06:30"}} {
		if !current.Looser(looser) {
			t.Fatalf("not looser: %+v", looser)
		}
	}
	for _, stricter := range []Bedtime{{Enabled: true, Start: "23:00", End: "07:00"}, {Enabled: true, Start: "23:30", End: "08:00"}} {
		if current.Looser(stricter) {
			t.Fatalf("stricter counted as looser: %+v", stricter)
		}
	}
}

func TestExtensionRuleOnlyWhileSitesAreBlocked(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Istanbul")
	config := outsideConfig(OutsideRule{})
	config.Distractions[0].Domains = []string{"x.com"}
	config.Extension = ExtensionRule{Enabled: true, QuietSeconds: 60}
	if err := config.Normalize(); err != nil {
		t.Fatal(err)
	}
	closed := Evaluate(config, 1, Facts{PlanExists: true}, time.Date(2026, 10, 30, 15, 0, 0, 0, location))
	if closed.Mode != ModeClosed || closed.RequireExtensionSeconds != 60 {
		t.Fatalf("closed: %+v", closed)
	}
	free := Evaluate(config, 1, Facts{PlanExists: true}, time.Date(2026, 10, 30, 21, 30, 0, 0, location))
	if free.Mode != ModeLeisure || free.RequireExtensionSeconds != 0 {
		t.Fatalf("distraction time: %+v", free)
	}
	if !(ExtensionRule{Enabled: true, QuietSeconds: 60}).Looser(ExtensionRule{Enabled: true, QuietSeconds: 120}) ||
		!(ExtensionRule{Enabled: true, QuietSeconds: 60}).Looser(ExtensionRule{}) ||
		(ExtensionRule{Enabled: true, QuietSeconds: 60}).Looser(ExtensionRule{Enabled: true, QuietSeconds: 30}) {
		t.Fatal("looser")
	}
}

func TestCorrectingARuleBeforeItTakesEffectAppliesAtOnce(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Istanbul")
	at := func(hour, minute int) time.Time { return time.Date(2026, 10, 31, hour, minute, 0, 0, location) }
	config := outsideConfig(OutsideRule{})
	// A 20-hour bedtime saved at 09:42 by mistake.
	mistake := Bedtime{Enabled: true, Start: "12:00", End: "08:00"}
	config.Bedtime = mistake
	config.RuleChanged = map[string]time.Time{"bedtime": at(9, 42)}
	fixed := Bedtime{Enabled: true, Start: "00:00", End: "08:00"}
	if !mistake.Looser(fixed) {
		t.Fatal("the fix asks less")
	}
	if !config.Correcting("bedtime", at(9, 57)) {
		t.Fatal("a fix before the bedtime ever started must apply at once")
	}
	if config.Correcting("bedtime", at(12, 30)) {
		t.Fatal("once it has held, asking less waits like any loosening")
	}
	// Other rules get only the ten minutes.
	config.RuleChanged["opening_rule"] = at(9, 0)
	if !config.Correcting("opening_rule", at(9, 9)) || config.Correcting("opening_rule", at(9, 11)) {
		t.Fatal("ten minutes to correct other rules")
	}
	if config.Correcting("morning", at(9, 0)) {
		t.Fatal("a rule never changed is not being corrected")
	}
}

func TestBedtimeTookEffect(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Istanbul")
	at := func(day, hour int) time.Time { return time.Date(2026, 10, day, hour, 0, 0, 0, location) }
	rule := Bedtime{Enabled: true, Start: "23:00", End: "07:00"}
	if BedtimeTookEffect(rule, "Europe/Istanbul", at(31, 10), at(31, 22)) {
		t.Fatal("not yet started")
	}
	if !BedtimeTookEffect(rule, "Europe/Istanbul", at(31, 10), at(31, 23)) {
		t.Fatal("started at 23:00")
	}
	if !BedtimeTookEffect(rule, "Europe/Istanbul", at(31, 2), at(31, 3)) {
		t.Fatal("set while already in its hours")
	}
}

func TestWindDownTurnsScreensGrayBeforeBedtime(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Istanbul")
	at := func(hour, minute int) time.Time { return time.Date(2026, 10, 31, hour, minute, 0, 0, location) }
	config := outsideConfig(OutsideRule{})
	config.Bedtime = Bedtime{Enabled: true, Start: "00:00", End: "08:00", WindDownMinutes: 60}
	if err := config.Normalize(); err != nil {
		t.Fatal(err)
	}
	facts := Facts{PlanExists: true}
	if Evaluate(config, 1, facts, at(22, 59)).Grayscale {
		t.Fatal("gray before the wind-down")
	}
	windDown := Evaluate(config, 1, facts, at(23, 0))
	if !windDown.Grayscale || windDown.Mode == ModeBedtime || windDown.LockScreen {
		t.Fatalf("wind-down only grays the screen: %+v", windDown)
	}
	if bed := Evaluate(config, 1, facts, at(3, 0)); !bed.Grayscale || bed.Mode != ModeBedtime {
		t.Fatalf("bedtime stays gray: %+v", bed)
	}
	if Evaluate(config, 1, facts, at(8, 0)).Grayscale {
		t.Fatal("gray after bedtime")
	}
	emergency := Facts{PlanExists: true, Emergency: &Emergency{StartedAt: at(23, 10), Until: at(23, 20)}}
	if Evaluate(config, 1, emergency, at(23, 15)).Grayscale {
		t.Fatal("gray during an emergency unlock")
	}
	segments := Timeline(config, 1, facts, at(22, 0), at(23, 30))
	if len(segments) != 2 || !segments[1].From.Equal(at(23, 0).UTC()) || !segments[1].Decision.Grayscale {
		t.Fatalf("timeline: %+v", segments)
	}
	if !(Bedtime{Enabled: true, Start: "00:00", End: "08:00", WindDownMinutes: 60}).Looser(Bedtime{Enabled: true, Start: "00:00", End: "08:00", WindDownMinutes: 30}) {
		t.Fatal("a shorter wind-down asks less")
	}
}
