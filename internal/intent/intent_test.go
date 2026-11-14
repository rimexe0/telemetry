package intent

import (
	"strings"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/activity"
)

func TestDigestJoinsRunsAndNamesAgents(t *testing.T) {
	from := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	at := func(minutes, seconds int) time.Time {
		return from.Add(time.Duration(minutes)*time.Minute + time.Duration(seconds)*time.Second)
	}
	stopped := at(40, 0)
	input := activity.DayInput{
		Result: activity.Result{Segments: []activity.Segment{
			{Device: "mac", App: "Helium", Domain: "youtube.com", Title: "How to recalibrate a touchscreen - YouTube", Start: at(0, 0), End: at(5, 0)},
			{Device: "mac", App: "Telegram", Title: "Telegram", Start: at(5, 0), End: at(5, 10)},
			{Device: "mac", App: "Helium", Domain: "youtube.com", Title: "How to recalibrate a touchscreen - YouTube", Start: at(5, 30), End: at(12, 0)},
			{Device: "phone", App: "WhatsApp", Start: at(20, 0), End: at(24, 0)},
			// Before the window: left out.
			{Device: "mac", App: "Finder", Title: "Downloads", Start: from.Add(-time.Hour), End: from.Add(-50 * time.Minute)},
		}},
		Turns: []activity.AgentTurn{
			{Tool: "t3", ThreadID: "a", Thread: "Fix touch input", Project: "fuckingstupidtablet", Requested: at(13, 0), Ended: &stopped},
			{Tool: "t3", ThreadID: "a", Thread: "Fix touch input", Project: "fuckingstupidtablet", Requested: at(41, 0)},
			{Tool: "t3", ThreadID: "b", Thread: "Subagent", Project: "meva", Requested: at(15, 0), Ended: &stopped, ByAgent: true},
		},
	}
	digest := Digest(input, nil, from, at(60, 0), time.UTC)
	for _, want := range []string{
		"09:00-09:12 mac Helium youtube.com — How to recalibrate a touchscreen - YouTube",
		"09:20-09:24 phone WhatsApp",
		"Brief glances (under 20 s each, summed): mac Telegram 10s",
		"fuckingstupidtablet · Fix touch input: 2 sent, last at 09:41; agent working now",
		"meva · Subagent: 0 sent; agent stopped at 09:40",
	} {
		if !strings.Contains(digest, want) {
			t.Errorf("digest lacks %q:\n%s", want, digest)
		}
	}
	if strings.Contains(digest, "Finder") {
		t.Errorf("time before the window is left out:\n%s", digest)
	}
}

func TestParseKeepsOnlyWhatFits(t *testing.T) {
	known := []Known{{ID: 4, Title: "Fix the tablet"}}
	output := "thinking...\n" + `{"intents":[` +
		`{"id":4,"title":"Fix the tablet's touchscreen","kind":"work","status":"waiting","evidence":["agent working"]},` +
		`{"id":99,"title":"Watching car videos","kind":"Leisure","status":"active","confidence":"HIGH"},` +
		`{"id":null,"title":"","kind":"work","status":"active"},` +
		`{"id":null,"title":"Something","kind":"strange","status":"active"},` +
		`{"id":4,"title":"Same id twice","kind":"work","status":"paused"}` +
		`],"now":1,"note":"Tablet work while videos play."}`
	reading, err := Parse(output, known)
	if err != nil {
		t.Fatal(err)
	}
	if len(reading.Aims) != 4 {
		t.Fatalf("aims: %+v", reading.Aims)
	}
	if reading.Aims[0].ID == nil || *reading.Aims[0].ID != 4 {
		t.Errorf("a known id is kept: %+v", reading.Aims[0])
	}
	if reading.Aims[1].ID != nil || reading.Aims[1].Kind != KindLeisure || reading.Aims[1].Confidence != "high" {
		t.Errorf("an unknown id makes a new aim: %+v", reading.Aims[1])
	}
	if reading.Aims[2].Kind != KindUnclear {
		t.Errorf("an unknown kind is unclear: %+v", reading.Aims[2])
	}
	if reading.Aims[3].ID != nil {
		t.Errorf("an id used twice is new the second time: %+v", reading.Aims[3])
	}
	if reading.Now != 1 || reading.Note != "Tablet work while videos play." {
		t.Errorf("now %d note %q", reading.Now, reading.Note)
	}
	if _, err := Parse("no json here", known); err == nil {
		t.Error("no reading is an error")
	}
}

func TestParseMovesNowPastDroppedAims(t *testing.T) {
	reading, err := Parse(`{"intents":[{"title":"","status":"active"},{"title":"Pay rent","kind":"life","status":"active"}],"now":1}`, nil)
	if err != nil || reading.Now != 0 || len(reading.Aims) != 1 {
		t.Fatalf("%+v %v", reading, err)
	}
}

func TestDigestAddsContextWithoutContents(t *testing.T) {
	from := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	record := func(kind string, at time.Time, payload string) activity.Record {
		return activity.Record{Source: "x", Kind: kind, Start: at, Payload: []byte(payload)}
	}
	input := activity.DayInput{From: from, To: from.Add(2 * time.Hour), Result: activity.Result{Segments: []activity.Segment{
		{Device: "mac", Machine: "catchy", App: "Konsole", Title: "~", Start: from, End: from.Add(5 * time.Minute)},
		{Device: "mac", Machine: "mac-20134be9", App: "Krita", Start: from, End: from.Add(5 * time.Minute)},
	}}}
	context := []activity.Record{
		record("health_connect.sleep_session", from.Add(-8*time.Hour), `{"start_time":"2026-11-14T01:12:00Z","end_time":"2026-11-14T04:45:00Z"}`),
		record("android.calendar_event", from.Add(3*time.Hour), `{"title":"CDAS Appointment","start":"2026-11-14T12:20:00Z","end":"2026-11-14T12:30:00Z"}`),
		record("desktop.site_block", from.Add(10*time.Minute), `{"site":"x.com","mode":"work"}`),
		record("desktop.site_block", from.Add(11*time.Minute), `{"site":"x.com","mode":"work"}`),
		record("desktop.site_block", from.Add(11*time.Minute+5*time.Second), `{"site":"x.com","mode":"work"}`),
		record("android.policy_block", from.Add(12*time.Minute), `{"package":"com.twitter.android"}`),
		record("android.policy_bypass", from.Add(13*time.Minute), `{"cause":"blocking_turned_off"}`),
		record("android.notification.posted", from.Add(14*time.Minute), `{"package_name":"com.whatsapp","title":"Mom","text":"secret message text"}`),
		record("android.notification.posted", from.Add(15*time.Minute), `{"package_name":"cyou.rime.telemetry.collector","title":"Telemetry collection active"}`),
		// Outside the window: not counted.
		record("desktop.site_block", from.Add(-time.Hour), `{"site":"reddit.com"}`),
	}
	digest := Digest(input, context, from, from.Add(2*time.Hour), time.UTC)
	for _, want := range []string{
		"09:00-09:05 catchy Konsole — ~",
		"09:00-09:05 mac Krita",
		"Sleep recorded since the evening before: 01:12-04:45",
		"Calendar (now and the next 12 h): 12:20-12:30 CDAS Appointment",
		"Blocked (minutes in which each was blocked): x.com ×2, com.twitter.android ×1",
		"Blocking turned off: 09:13 phone (blocking_turned_off)",
		"Notifications received (app from sender or chat; contents not shown): com.whatsapp from Mom ×1",
	} {
		if !strings.Contains(digest, want) {
			t.Errorf("digest lacks %q:\n%s", want, digest)
		}
	}
	for _, never := range []string{"secret message text", "reddit.com", "Telemetry collection active"} {
		if strings.Contains(digest, never) {
			t.Errorf("digest must not contain %q:\n%s", never, digest)
		}
	}
}
