package activity

import (
	"strings"
	"testing"
	"time"
)

func TestReviewPlanSplitsTheDayByBlock(t *testing.T) {
	at := func(hour, minute int) time.Time { return time.Date(2026, 10, 1, hour, minute, 0, 0, time.UTC) }
	result := Result{LastRecord: map[string]time.Time{}, Segments: []Segment{
		{Device: DeviceMac, App: "Code", Start: at(9, 0), End: at(9, 40)},
		{Device: DeviceMac, App: "Helium", Domain: "www.youtube.com", Start: at(9, 40), End: at(10, 10)},
		{Device: DevicePhone, App: "com.twitter.android", Start: at(9, 50), End: at(9, 55)},
		{Device: DeviceMac, App: "Code", Start: at(10, 10), End: at(10, 30)},
	}, Gaps: []Gap{{Device: DevicePhone, Start: at(8, 0), End: at(9, 30)}}}
	labels := []Label{{Kind: LabelKindSite, Match: "youtube.com", Label: Distraction}, {Kind: LabelKindApp, Match: "com.twitter.android", Label: Distraction}}
	coding := []Record{
		{Source: "t3:mac:env", Device: "mac", Kind: "t3.thread", Payload: []byte(`{"thread_id":"a","title":"Fix it","updated_at":"2026-10-01T09:30:00Z"}`)},
		{Source: "t3:mac:env", Device: "mac", Kind: "t3.turn", Payload: []byte(`{"thread_id":"a","state":"completed","requested_at":"2026-10-01T09:10:00Z","started_at":"2026-10-01T09:10:00Z","completed_at":"2026-10-01T09:25:00Z"}`)},
	}
	blocks := []PlannedBlock{
		{ID: 1, Title: "Telemetry", Start: at(9, 0), End: at(10, 0), State: "planned", Closes: true},
		{ID: 2, Title: "Lunch", Start: at(10, 20), End: at(11, 0), State: "planned"},
		{ID: 3, Title: "Later", Start: at(14, 0), End: at(15, 0), State: "planned"},
	}
	review := ReviewPlan(blocks, result, coding, labels, at(8, 0), at(10, 30), 5)
	first := review.Blocks[0]
	if first.Status != "past" || first.Observed.Minutes != 60 || first.Observed.DistractionMinutes != 25 {
		t.Fatalf("first block = %+v", first.Observed)
	}
	if mac := first.Observed.Devices[0]; mac.ActiveMinutes != 60 || mac.Apps[0].Name != "Code" || mac.Apps[0].Minutes != 40 {
		t.Fatalf("mac in first block = %+v", mac)
	}
	if phone := first.Observed.Devices[1]; phone.ActiveMinutes != 5 || phone.NoDataMinutes != 30 {
		t.Fatalf("phone in first block = %+v", phone)
	}
	if len(first.Observed.Coding) != 1 || first.Observed.Coding[0].AgentMinutes != 15 || first.Observed.Coding[0].MessagesSent != 1 {
		t.Fatalf("coding = %+v", first.Observed.Coding)
	}
	if second := review.Blocks[1]; second.Status != "in_progress" || second.Observed.Minutes != 10 || second.Observed.Devices[0].ActiveMinutes != 10 {
		t.Fatalf("second block = %+v %+v", second.Status, second.Observed)
	}
	if third := review.Blocks[2]; third.Status != "upcoming" || third.Observed != nil {
		t.Fatalf("an upcoming block has nothing observed: %+v", third)
	}
	// Outside the blocks: 08:00-09:00 and 10:00-10:20.
	if out := review.Outside; out.Minutes != 80 || out.Devices[0].ActiveMinutes != 20 || out.DistractionMinutes != 10 || out.Devices[1].NoDataMinutes != 60 {
		t.Fatalf("outside = %+v", out)
	}
}

func TestInsightsStateTheDayPlainly(t *testing.T) {
	at := func(hour, minute int) time.Time { return time.Date(2026, 10, 1, hour, minute, 0, 0, time.UTC) }
	result := Result{LastRecord: map[string]time.Time{}, Segments: []Segment{
		{Device: DeviceMac, App: "Code", Start: at(9, 0), End: at(10, 30)},
		{Device: DevicePhone, App: "com.twitter.android", Start: at(10, 30), End: at(10, 50)},
		{Device: DeviceMac, App: "Code", Start: at(10, 50), End: at(11, 20)},
		{Device: DevicePhone, App: "com.twitter.android", Start: at(11, 20), End: at(11, 50)},
	}, Gaps: []Gap{{Device: DevicePhone, Start: at(12, 0), End: at(13, 0)}}}
	labels := []Label{{Kind: LabelKindApp, Match: "com.twitter.android", Label: Distraction}}
	coding := []CodingThread{{Title: "a", MessagesSent: 3, AgentMinutes: 70}, {Title: "idle"}}
	byKey := map[string]Insight{}
	for _, insight := range Insights(result, coding, labels) {
		byKey[insight.Key] = insight
	}
	if got := byKey["distractions"]; got.Text != "50m on distractions today" || !strings.Contains(got.Detail, "com.twitter.android: 50m in 2 visits") ||
		!strings.Contains(got.Detail, "2 episodes") || got.Names[0] != "com.twitter.android" {
		t.Fatalf("distractions = %+v", got)
	}
	if got := byKey["clean_run"].Text; got != "Longest run without a distraction: 1h 30m" {
		t.Fatalf("clean run = %q", got)
	}
	if got := byKey["agents"]; got.Text != "Agents worked 1h 10m for you" || got.Detail != "You sent 3 messages across 1 thread." {
		t.Fatalf("agents = %+v", got)
	}
	if got := byKey["screen_mac"]; got.Text != "Mac: 2h of screen time" || got.Detail != "Top app: Code, 2h." {
		t.Fatalf("mac = %+v", got)
	}
	if got := byKey["no_data_phone"].Text; got != "Phone sent nothing for 1h" {
		t.Fatalf("no data = %q", got)
	}
	if got := Insights(result, nil, nil)[0]; got.Text != "Nothing is labelled a distraction yet" {
		t.Fatalf("without labels = %+v", got)
	}
}
