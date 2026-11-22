package policy

import (
	"testing"
	"time"
)

// The owner switched bedtime (00:00-08:00) off at 23:59 and it took effect at
// midnight, a minute later. The next day now starts when the next bedtime
// ends, so that night is still kept.
func TestLooseningWaitsForTheWakeTimeAfterTheNextBedtime(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Istanbul")
	at := func(day, hour, minute int) time.Time { return time.Date(2026, 11, day, hour, minute, 0, 0, location) }
	config := outsideConfig(OutsideRule{})
	config.Bedtime = Bedtime{Enabled: true, Start: "00:00", End: "08:00", WindDownMinutes: 65}
	if err := config.Normalize(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{"a minute before bedtime", at(17, 23, 59), at(18, 8, 0)},
		{"in the afternoon", at(17, 15, 0), at(18, 8, 0)},
		{"during bedtime, after its start", at(18, 3, 0), at(19, 8, 0)},
		{"a minute before it ends", at(18, 7, 59), at(19, 8, 0)},
	} {
		if got := config.NextDay(test.now); got != test.want.UTC().Format(time.RFC3339) {
			t.Errorf("%s: next day %s, want %s", test.name, got, test.want.UTC().Format(time.RFC3339))
		}
	}

	now := at(17, 23, 59)
	off := Bedtime{Enabled: false, Start: "00:00", End: "08:00", WindDownMinutes: 65}
	changed := config.WithBedtime(off, now)
	for _, check := range []time.Time{at(18, 0, 0), at(18, 2, 0), at(18, 7, 59)} {
		if !changed.BedtimeAt(check).Enabled {
			t.Fatalf("bedtime off at %v, before the wake time after the night", check)
		}
	}
	if changed.BedtimeAt(at(18, 8, 0)).Enabled {
		t.Fatal("bedtime still on at 08:00 the next morning")
	}

	// The same holds for every rule that waits: here the extension rule's
	// longer quiet time.
	config.Extension = ExtensionRule{Enabled: true, QuietSeconds: 30}
	slower := config.WithExtensionRule(ExtensionRule{Enabled: true, QuietSeconds: 600}, now)
	if slower.ExtensionRuleAt(at(18, 1, 0)).QuietSeconds != 30 || slower.ExtensionRuleAt(at(18, 8, 0)).QuietSeconds != 600 {
		t.Fatal("the extension rule did not wait for the wake time")
	}
}

func TestWithoutBedtimeTheNextDayIsMidnight(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Istanbul")
	config := outsideConfig(OutsideRule{})
	if got := config.NextDay(time.Date(2026, 11, 17, 15, 0, 0, 0, location)); got != "2026-11-18" {
		t.Fatalf("next day %s", got)
	}
	// A rule set before 2026-11-22 waits for a date, and starts at its midnight.
	if Started(config.Timezone, "2026-11-18", time.Date(2026, 11, 17, 23, 59, 0, 0, location)) || !Started(config.Timezone, "2026-11-18", time.Date(2026, 11, 18, 0, 0, 0, 0, location)) {
		t.Fatal("a dated rule starts at that date's midnight")
	}
}

func TestTheExtensionRuleCannotBeSwitchedOff(t *testing.T) {
	on := ExtensionRule{Enabled: true, QuietSeconds: 30}
	if !on.SwitchesOff(ExtensionRule{}) || on.SwitchesOff(ExtensionRule{Enabled: true, QuietSeconds: 60}) || (ExtensionRule{}).SwitchesOff(ExtensionRule{}) {
		t.Fatal("SwitchesOff")
	}
}

func TestPlannedWakeIsTheEndOfBedtime(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Istanbul")
	config := outsideConfig(OutsideRule{})
	if config.PlannedWake("2026-11-22") != nil {
		t.Fatal("a planned wake without a bedtime")
	}
	config.Bedtime = Bedtime{Enabled: true, Start: "00:00", End: "08:00"}
	if wake := config.PlannedWake("2026-11-22"); wake == nil || !wake.Equal(time.Date(2026, 11, 22, 8, 0, 0, 0, location)) {
		t.Fatalf("planned wake %v", wake)
	}
}
