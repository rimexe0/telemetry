package activity

import (
	"testing"
	"time"
)

func TestBreaksAreRestOnlyAwayFromScreens(t *testing.T) {
	at := func(hour, minute int) time.Time { return time.Date(2026, 10, 6, hour, minute, 0, 0, time.UTC) }
	labels := []Label{{Kind: LabelKindApp, Match: "com.twitter.android", Label: Distraction}}
	tasks := []TaskInfo{{ID: 1, Title: "Science fair presentation", Rules: []TaskRule{{Signal: "domain", Pattern: "docs.google.com"}}}}
	slides := func(from, to time.Time) Segment {
		return Segment{Device: DeviceMac, App: "Helium", Domain: "docs.google.com", Start: from, End: to}
	}
	result := Result{LastRecord: map[string]time.Time{}, Segments: []Segment{
		// 30 minutes of work, 10 away from every screen, 30 more: a rest.
		slides(at(9, 0), at(9, 30)),
		slides(at(9, 40), at(10, 10)),
		// Then 10 minutes, 8 of them on X: scrolling, not rest.
		{Device: DevicePhone, App: "com.twitter.android", Start: at(10, 11), End: at(10, 19)},
		slides(at(10, 20), at(10, 50)),
		// A minute away is part of the work, and two hours away is no break between stretches.
		slides(at(10, 51), at(11, 0)),
		slides(at(13, 0), at(13, 30)),
		// Since then, ten minutes away, still going.
	}}
	day := BuildDay(DayInput{Result: result, Labels: labels, Tasks: tasks, From: at(0, 0), To: at(13, 40)})
	if len(day.Breaks) != 3 {
		t.Fatalf("breaks = %+v", day.Breaks)
	}
	rest, scrolling, ongoing := day.Breaks[0], day.Breaks[1], day.Breaks[2]
	if rest.Kind != "rest" || rest.AwayMinutes != 10 || !rest.Start.Equal(at(9, 30)) {
		t.Fatalf("rest = %+v", rest)
	}
	if scrolling.Kind != "scrolling" || scrolling.DistractionMinutes != 8 {
		t.Fatalf("scrolling = %+v", scrolling)
	}
	if ongoing.Kind != "rest" || !ongoing.Ongoing || ongoing.AwayMinutes != 10 {
		t.Fatalf("ongoing = %+v", ongoing)
	}
	if day.RestMinutes != 20 || day.ScrollingMinutes != 8 {
		t.Fatalf("totals: rest %v, scrolling %v", day.RestMinutes, day.ScrollingMinutes)
	}
	// With the phone not reporting, being away is not shown.
	result.Gaps = []Gap{{Device: DevicePhone, Start: at(9, 30), End: at(9, 40)}}
	if got := BuildDay(DayInput{Result: result, Labels: labels, Tasks: tasks, From: at(0, 0), To: at(13, 40)}).Breaks[0]; got.Kind != "unknown" {
		t.Fatalf("no phone data = %+v", got)
	}
}
