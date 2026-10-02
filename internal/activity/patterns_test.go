package activity

import (
	"testing"
	"time"
)

func TestTaskVisitsSayWhatTheOwnerLeftForAndWhenTheyCameBack(t *testing.T) {
	at := func(hour, minute int) time.Time { return time.Date(2026, 10, 2, hour, minute, 0, 0, time.UTC) }
	slides := int64(4)
	tasks := []TaskInfo{{ID: slides, Title: "Science fair presentation", Related: []string{"docs.google.com"}}}
	labels := []Label{{Kind: LabelKindSite, Match: "x.com", Label: Distraction}}
	mac := func(app, domain string, from, to time.Time) Segment {
		return Segment{Device: DeviceMac, App: app, Domain: domain, Start: from, End: to}
	}
	segments := []Segment{
		mac("Helium", "docs.google.com", at(14, 0), at(14, 20)),
		mac("Finder", "", at(14, 20), at(14, 21)), // a short glance does not end the visit
		mac("Helium", "docs.google.com", at(14, 21), at(14, 40)),
		mac("Helium", "x.com", at(14, 40), at(14, 55)),
		mac("Helium", "docs.google.com", at(14, 55), at(15, 10)),
		// then nothing on any device
	}
	visits := TaskVisits(DayInput{Result: Result{Segments: segments}, Labels: labels, Tasks: tasks, From: at(0, 0), To: at(16, 0)})[slides]
	if len(visits) != 2 {
		t.Fatalf("visits = %+v", visits)
	}
	first, second := visits[0], visits[1]
	if !first.Start.Equal(at(14, 0)) || !first.End.Equal(at(14, 40)) || first.LeftTo != "x.com" || first.LeftKind != KindDistraction || first.Back == nil || !first.Back.Equal(at(14, 55)) {
		t.Fatalf("first = %+v", first)
	}
	if second.LeftKind != "away" || second.LeftTo != "" || second.Back != nil {
		t.Fatalf("second = %+v", second)
	}
	if Median([]float64{40, 15}) != 27.5 || Median([]float64{3, 1, 2}) != 2 || Median(nil) != 0 {
		t.Fatal("median")
	}
}
