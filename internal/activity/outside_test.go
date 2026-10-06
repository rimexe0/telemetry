package activity

import (
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/places"
)

func TestTimeOutsideRunsFromLeavingHomeToComingBack(t *testing.T) {
	at := func(hour, minute int) time.Time { return time.Date(2026, 10, 5, hour, minute, 0, 0, time.UTC) }
	home := [2]float64{38.4000, 17.1000}
	away := [2]float64{38.4500, 17.1900}
	fix := func(when time.Time, where [2]float64) places.Fix {
		return places.Fix{At: when, Lat: where[0], Lon: where[1], Accuracy: 8}
	}
	// One wobbling position 190 m out at 12:00 is not going out.
	fixes := []places.Fix{fix(at(1, 0), home), fix(at(4, 0), home), fix(at(7, 46), away), fix(at(8, 30), away), fix(at(10, 30), home), fix(at(10, 50), home),
		fix(at(12, 0), [2]float64{38.4016, 17.0994}), fix(at(12, 1), home), fix(at(12, 30), home), fix(at(18, 0), away), fix(at(18, 20), away)}
	day := BuildDay(DayInput{Fixes: fixes, Home: &home, From: at(0, 0), To: at(19, 0)})
	if len(day.Outside) != 2 || !day.Outside[0].Start.Equal(at(7, 46)) || !day.Outside[0].End.Equal(at(10, 30)) {
		t.Fatalf("outside = %+v", day.Outside)
	}
	// Out again in the evening and not back yet: until the last position.
	if evening := day.Outside[1]; !evening.Ongoing || !evening.Start.Equal(at(18, 0)) || !evening.End.Equal(at(18, 20)) {
		t.Fatalf("evening = %+v", evening)
	}
	if day.OutsideMinutes != 164+20 {
		t.Fatalf("minutes = %v", day.OutsideMinutes)
	}
	if got := BuildDay(DayInput{Fixes: fixes, From: at(0, 0), To: at(19, 0)}); len(got.Outside) != 0 {
		t.Fatal("no home known, nothing is called outside")
	}
}
