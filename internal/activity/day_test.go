package activity

import (
	"testing"
	"time"
)

func TestBuildDayCompressesWithoutDroppingTime(t *testing.T) {
	at := func(hour, minute, second int) time.Time {
		return time.Date(2026, 10, 1, hour, minute, second, 0, time.UTC)
	}
	labels := []Label{{Kind: LabelKindApp, Match: "com.twitter.android", Label: Distraction}, {Kind: LabelKindSite, Match: "youtube.com", Label: Distraction}}
	result := Result{LastRecord: map[string]time.Time{}, Segments: []Segment{
		// Phone: X with a 30-second glance at WhatsApp inside it, then a long pause, then Anki.
		{Device: DevicePhone, App: "com.twitter.android", Start: at(9, 0, 0), End: at(9, 10, 0)},
		{Device: DevicePhone, App: "com.whatsapp", Start: at(9, 10, 0), End: at(9, 10, 30)},
		{Device: DevicePhone, App: "com.twitter.android", Start: at(9, 10, 30), End: at(9, 20, 0)},
		{Device: DevicePhone, App: "com.ichi2.anki", Start: at(10, 0, 0), End: at(10, 15, 0)},
		// Mac: code, then YouTube long enough to be its own chunk, then code.
		{Device: DeviceMac, App: "Code", Title: "main.go", Start: at(9, 0, 0), End: at(9, 30, 0)},
		{Device: DeviceMac, App: "Helium", Domain: "www.youtube.com", Title: "lofi", Start: at(9, 30, 0), End: at(9, 45, 0)},
		{Device: DeviceMac, App: "Code", Title: "main.go", Start: at(9, 45, 0), End: at(10, 0, 0)},
	}}
	blocks := []PlannedBlock{
		{ID: 1, Title: "Telemetry", Start: at(9, 0, 0), End: at(10, 0, 0), State: "planned", Closes: true},
		{ID: 2, Title: "Later", Start: at(12, 0, 0), End: at(13, 0, 0), State: "planned"},
	}
	day := BuildDay(blocks, result, labels, nil, at(0, 0, 0), at(10, 30, 0), true)

	var phone, mac []Chunk
	var active float64
	segments := 0
	for _, chunk := range day.Chunks {
		active += chunk.ActiveMinutes
		segments += len(chunk.Segments)
		if chunk.Device == DevicePhone {
			phone = append(phone, chunk)
		} else {
			mac = append(mac, chunk)
		}
	}
	if active != 95 || segments != len(result.Segments) {
		t.Fatalf("chunks must keep all time and every segment: %v minutes, %d segments", active, segments)
	}
	if len(phone) != 2 || phone[0].Kind != KindDistraction || phone[0].ActiveMinutes != 20 || phone[0].DistractionMinutes != 19.5 ||
		len(phone[0].Items) != 2 || phone[0].Items[0].Visits != 2 || phone[1].Kind != KindOther {
		t.Fatalf("phone chunks = %+v", phone)
	}
	if len(mac) != 3 || mac[0].Kind != KindOther || mac[1].Kind != KindDistraction || mac[1].Items[0].Name != "www.youtube.com" || mac[2].Kind != KindOther {
		t.Fatalf("mac chunks = %+v", mac)
	}
	// 09:00-10:00: the Mac was in Code for 45 minutes, but the phone showed X
	// for 19.5 of them, so 25.5 count as in plan.
	if first := day.Blocks[0]; first.Status != "past" || first.InPlanMinutes != 25.5 || first.DistractionMinutes != 34.5 {
		t.Fatalf("first block = %+v", first)
	}
	if second := day.Blocks[1]; second.Status != "upcoming" || second.InPlanMinutes != 0 {
		t.Fatalf("second block = %+v", second)
	}
	if day.PlannedMinutes != 120 || day.InPlanMinutes != 25.5 || day.DistractionMinutes != 34.5 || day.Distraction[0].Minutes != 15 || day.Distraction[1].Minutes != 19.5 {
		t.Fatalf("totals = %+v", day)
	}
	if plain := BuildDay(nil, result, labels, nil, at(0, 0, 0), at(10, 30, 0), false); plain.Chunks[0].Segments != nil || len(plain.Blocks) != 0 {
		t.Fatalf("without segments = %+v", plain.Chunks[0])
	}
}

func TestT3TimeIsNamedByTheNearestMessage(t *testing.T) {
	at := func(hour, minute int) time.Time { return time.Date(2026, 10, 1, hour, minute, 0, 0, time.UTC) }
	turn := func(thread, requested string) Record {
		return Record{Source: "t3:mac:env", Kind: "t3.turn", Payload: []byte(`{"thread_id":"` + thread + `","requested_at":"` + requested + `"}`)}
	}
	messages := ThreadMessages([]Record{
		{Source: "t3:mac:env", Kind: "t3.project", Payload: []byte(`{"project_id":"p","title":"Telemetry"}`)},
		{Source: "t3:mac:env", Kind: "t3.thread", Payload: []byte(`{"thread_id":"a","project_id":"p","title":"Fix collector"}`)},
		{Source: "t3:mac:env", Kind: "t3.thread", Payload: []byte(`{"thread_id":"b","project_id":"p","title":"Phone app"}`)},
		turn("a", "2026-10-01T09:05:00Z"), turn("b", "2026-10-01T09:40:00Z"),
	})
	result := Result{LastRecord: map[string]time.Time{}, Segments: []Segment{
		{Device: DeviceMac, App: "T3 Code (Nightly)", Start: at(9, 0), End: at(9, 10)},
		{Device: DeviceMac, App: "T3 Code (Nightly)", Start: at(9, 12), End: at(9, 20)},
		{Device: DevicePhone, App: "com.t3tools.t3code", Start: at(9, 35), End: at(9, 45)},
		{Device: DeviceMac, App: "T3 Code (Nightly)", Start: at(12, 0), End: at(12, 10)},
		{Device: DeviceMac, App: "Code", Start: at(12, 10), End: at(12, 12)},
	}}
	day := BuildDay(nil, result, nil, messages, at(0, 0), at(13, 0), true)
	details := map[string]float64{}
	for _, chunk := range day.Chunks {
		for _, item := range chunk.Items {
			details[item.Name+"|"+item.Detail] += item.Minutes
		}
	}
	// 09:00-09:20 on the Mac is near the 09:05 message; the phone's stretch
	// holds the 09:40 one; nothing was sent near 12:00, so that stays unnamed.
	if details["T3 Code (Nightly)|Telemetry · Fix collector"] != 18 || details["com.t3tools.t3code|Telemetry · Phone app"] != 10 ||
		details["T3 Code (Nightly)|"] != 10 || details["Code|"] != 2 {
		t.Fatalf("details = %+v", details)
	}
}
