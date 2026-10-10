package places

import (
	"testing"
	"time"
)

func TestReachedFollowsArrivalsAndDepartures(t *testing.T) {
	home := []Circle{{Lat: 38.46, Lon: 17.22, Radius: 120}}
	start := time.Date(2026, 10, 10, 17, 0, 0, 0, time.UTC)
	away := Fix{At: start.Add(-time.Minute), Lat: 38.47, Lon: 17.22, Accuracy: 10}
	at := func(minutes int, lat float64) Fix {
		return Fix{At: start.Add(time.Duration(minutes) * time.Minute), Lat: lat, Lon: 17.22, Accuracy: 10}
	}
	trip := []Fix{at(5, 38.468), at(20, 38.4601)}

	if got := Reached(true, home, &away, trip, start, start.Add(time.Hour)); got == nil || !got.Equal(trip[1].At) {
		t.Fatalf("arrival = %v, want %v", got, trip[1].At)
	}
	// Set at home: staying home is not arriving.
	inside := at(-1, 38.4601)
	if got := Reached(true, home, &inside, []Fix{at(5, 38.46005)}, start, start.Add(time.Hour)); got != nil {
		t.Fatalf("arrival while home = %v", got)
	}
	// Not before 18:00: arrived 17:20, still home at 18:05.
	if got := Reached(true, home, &away, trip, start.Add(time.Hour), start.Add(65*time.Minute)); got == nil || !got.Equal(start.Add(65*time.Minute)) {
		t.Fatalf("arrival held back = %v", got)
	}
	// Leaving home.
	out := []Fix{at(5, 38.4601), at(10, 38.47)}
	if got := Reached(false, home, nil, out, start, start.Add(time.Hour)); got == nil || !got.Equal(out[1].At) {
		t.Fatalf("departure = %v", got)
	}
	// A poor fix or edge wobble decides nothing.
	wobble := []Fix{at(5, 38.46+150/111320.0), {At: start.Add(6 * time.Minute), Lat: 38.47, Lon: 17.22, Accuracy: 500}}
	if got := Reached(false, home, &inside, wobble, start, start.Add(time.Hour)); got != nil {
		t.Fatalf("wobble = %v", got)
	}
}
