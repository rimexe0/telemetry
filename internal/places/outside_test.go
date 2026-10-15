package places

import (
	"testing"
	"time"
)

func TestOutsideMinutesFromPhoneAndWatch(t *testing.T) {
	home := [2]float64{38.3997, 17.0992}
	start := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	at := func(minutes int, lat float64) Fix {
		return Fix{At: start.Add(time.Duration(minutes) * time.Minute), Lat: lat, Lon: 17.0992, Accuracy: 10}
	}
	// A 20-minute walk to about 600 m and back, a minute per position.
	walk := []Fix{at(0, 38.3997)}
	for minute := 1; minute <= 20; minute++ {
		distance := minute
		if minute > 10 {
			distance = 20 - minute
		}
		walk = append(walk, at(minute, 38.3997+float64(distance)*0.0005))
	}
	got := Minutes(OutsideIntervals(walk, home, 200, true), start, start.Add(time.Hour))
	// Outside (200 m or more) from minute 4 to minute 16.
	if got < 11.5 || got > 12.5 {
		t.Fatalf("walk outside = %.1f min, want about 12", got)
	}
	// The phone and watch seeing the same walk count it once.
	both := append(OutsideIntervals(walk, home, 200, true), OutsideIntervals(walk, home, 200, true)...)
	if twice := Minutes(both, start, start.Add(time.Hour)); twice != got {
		t.Fatalf("counted twice: %.1f", twice)
	}
	// A car: 3 km in 2 minutes is not on foot.
	car := []Fix{at(0, 38.42), at(2, 38.447)}
	if Minutes(OutsideIntervals(car, home, 200, true), start, start.Add(time.Hour)) != 0 {
		t.Fatal("a car ride counted on foot")
	}
	if Minutes(OutsideIntervals(car, home, 200, false), start, start.Add(time.Hour)) != 2 {
		t.Fatal("a car ride did not count without on_foot")
	}
	// Sitting in a park: the phone records nothing for 40 minutes.
	park := []Fix{at(0, 38.41), at(40, 38.41)}
	if Minutes(OutsideIntervals(park, home, 200, true), start, start.Add(time.Hour)) != 40 {
		t.Fatal("sitting outside did not count")
	}
	// Only today counts.
	if Minutes(OutsideIntervals(park, home, 200, true), start.Add(30*time.Minute), start.Add(time.Hour)) != 10 {
		t.Fatal("not clipped to the day")
	}
}
