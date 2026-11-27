package activity

import (
	"testing"
	"time"
)

func TestDayCellsShowDistractionUseAndNoData(t *testing.T) {
	day := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	at := func(hour, minute int) time.Time {
		return day.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
	}
	result := Result{
		Segments: []Segment{
			{Device: DevicePhone, Start: at(1, 0), End: at(2, 0), App: "com.example.feed"},
			{Device: DeviceMac, Start: at(2, 0), End: at(4, 0), App: "Code"},
			// Ten minutes on the feed inside a part of 1 h is not most of it.
			{Device: DevicePhone, Start: at(5, 0), End: at(5, 10), App: "com.example.feed"},
		},
		Gaps: []Gap{
			// The Mac sent nothing 06–12 and the phone 08–12: no data 08–12 only.
			{Device: DeviceMac, Start: at(6, 0), End: at(12, 0)},
			{Device: DevicePhone, Start: at(8, 0), End: at(12, 0)},
		},
	}
	labels := []Label{{Kind: LabelKindApp, Match: "com.example.feed", Label: Distraction}}
	got := DayCells(result, labels, day, day.Add(24*time.Hour), at(20, 0), 24)
	want := ".daa....nnnn........____"
	if got != want {
		t.Fatalf("cells\n got %q\nwant %q", got, want)
	}
	if minutes := DistractionMinutes(result, labels); minutes != 70 {
		t.Fatalf("distraction minutes = %v", minutes)
	}
}
