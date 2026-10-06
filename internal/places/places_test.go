package places

import (
	"testing"
	"time"
)

func TestStaysAndHomeFromYesterdaysShape(t *testing.T) {
	istanbul, _ := time.LoadLocation("Europe/Istanbul")
	at := func(day, hour, minute int) time.Time { return time.Date(2026, 10, day, hour, minute, 0, 0, istanbul) }
	home := [2]float64{38.4000, 17.1000}
	hospital := [2]float64{38.4500, 17.1900}
	fix := func(when time.Time, where [2]float64) Fix {
		return Fix{At: when, Lat: where[0], Lon: where[1], Accuracy: 8}
	}
	fixes := []Fix{
		// Nights at home.
		fix(at(4, 2, 0), home), fix(at(4, 4, 0), home), fix(at(5, 3, 35), home), fix(at(5, 5, 0), home),
		// Leaves at 07:46, through two points on the way.
		fix(at(5, 7, 46), [2]float64{38.4210, 17.1300}), fix(at(5, 8, 15), [2]float64{38.4400, 17.1600}),
		// Half an hour at the hospital, one imprecise position ignored.
		fix(at(5, 8, 52), hospital), fix(at(5, 9, 10), hospital), {At: at(5, 9, 15), Lat: 38.5, Lon: 17.3, Accuracy: 400}, fix(at(5, 9, 22), hospital),
		// On the way back, then home.
		fix(at(5, 9, 27), [2]float64{38.4558, 17.1931}), fix(at(5, 10, 53), home), fix(at(5, 21, 40), home),
	}
	found := Home(fixes, istanbul)
	if found == nil || Meters(found[0], found[1], home[0], home[1]) > 20 {
		t.Fatalf("home = %v", found)
	}
	stays := Stays(fixes, found)
	if len(stays) != 3 {
		t.Fatalf("stays = %+v", stays)
	}
	if !stays[0].Home || !stays[0].End.Equal(at(5, 7, 46)) {
		t.Fatalf("night at home = %+v", stays[0])
	}
	if stays[1].Home || !stays[1].Start.Equal(at(5, 8, 52)) || !stays[1].End.Equal(at(5, 9, 27)) || stays[1].Fixes != 3 {
		t.Fatalf("hospital = %+v", stays[1])
	}
	if !stays[2].Home || !stays[2].Start.Equal(at(5, 10, 53)) {
		t.Fatalf("back home = %+v", stays[2])
	}
	if Home(nil, istanbul) != nil {
		t.Fatal("no nights, no home")
	}
}
