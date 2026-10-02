package activity

import (
	"testing"
	"time"
)

func TestWakeTimeIsTheFirstUseAfterALongQuietStretch(t *testing.T) {
	at := func(day, hour, minute int) time.Time { return time.Date(2026, 10, day, hour, minute, 0, 0, time.UTC) }
	phone := func(from, to time.Time) Segment { return Segment{Device: DevicePhone, App: "x", Start: from, End: to} }
	// Up past midnight until 01:30, phone first used at 08:26, a 40-minute lunch away, back at 13:00.
	result := Result{Segments: []Segment{phone(at(1, 22, 0), at(2, 1, 30)), phone(at(2, 8, 26), at(2, 12, 20)), phone(at(2, 13, 0), at(2, 14, 0))}}
	wake := WakeTime(result, at(1, 18, 0), at(2, 14, 0))
	if wake == nil || !wake.Equal(at(2, 8, 26)) {
		t.Fatalf("wake = %v", wake)
	}
	// Still awake since yesterday: no long quiet stretch, so no new day has started.
	if got := WakeTime(Result{Segments: []Segment{phone(at(1, 22, 0), at(2, 1, 30))}}, at(1, 20, 0), at(2, 1, 30)); got != nil {
		t.Fatalf("up past midnight = %v", got)
	}
	// The same quiet stretch with no phone data is a collector that was off, not sleep.
	result.Gaps = []Gap{{Device: DevicePhone, Start: at(2, 2, 0), End: at(2, 8, 0)}}
	if got := WakeTime(result, at(1, 18, 0), at(2, 14, 0)); got != nil {
		t.Fatalf("missing data must not be read as sleep: %v", got)
	}
}
