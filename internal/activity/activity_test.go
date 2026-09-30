package activity

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)

func at(minutes float64) time.Time { return t0.Add(time.Duration(minutes * float64(time.Minute))) }

func aw(bucket string, start, minutes float64, data string) Record {
	payload := fmt.Sprintf(`{"timestamp":%q,"duration":%g,"data":%s}`, at(start).Format(time.RFC3339Nano), minutes*60, data)
	end := at(start + minutes)
	return Record{Source: "activitywatch:mac-1:" + bucket, Device: "mac-1", Kind: "activitywatch.event", Start: at(start), End: &end, Payload: json.RawMessage(payload)}
}

func phone(kind string, minute float64, payload string) Record {
	return Record{Source: "android:p:" + kind, Device: "p", Kind: map[string]string{"usage": "android.usage.activity_", "state": "android.device_state"}[kind], Start: at(minute), Payload: json.RawMessage(payload)}
}

func usage(event, pkg string, minute float64) Record {
	r := phone("usage", minute, fmt.Sprintf(`{"package_name":%q}`, pkg))
	r.Kind += event
	return r
}

func minutesOf(segments []Segment, device, app, domain string) float64 {
	total := 0.0
	for _, s := range segments {
		if s.Device == device && s.App == app && s.Domain == domain {
			total += s.Duration().Minutes()
		}
	}
	return total
}

func TestMacCountsOnlyNonAFKTimeAndNamesBrowserSites(t *testing.T) {
	records := []Record{
		aw("aw-watcher-window_h", 0, 30, `{"app":"Helium","title":"X"}`),
		aw("aw-watcher-window_h", 30, 30, `{"app":"T3 Code","title":"Telemetry"}`),
		// Away from 20 to 40.
		aw("aw-watcher-afk_h", 0, 20, `{"status":"not-afk"}`),
		aw("aw-watcher-afk_h", 20, 20, `{"status":"afk"}`),
		aw("aw-watcher-afk_h", 40, 20, `{"status":"not-afk"}`),
		aw("aw-watcher-web-chrome_h", 0, 12, `{"url":"https://x.com/home","title":"Home / X"}`),
		aw("aw-watcher-web-chrome_h", 12, 18, `{"url":"https://www.youtube.com/shorts/1","title":"Shorts"}`),
	}
	result := Build(records, at(0), at(60))
	if got := minutesOf(result.Segments, DeviceMac, "Helium", "x.com"); got != 12 {
		t.Fatalf("x.com = %v, want 12", got)
	}
	if got := minutesOf(result.Segments, DeviceMac, "Helium", "youtube.com"); got != 8 {
		t.Fatalf("youtube.com = %v, want 8 (20 minus the afk start)", got)
	}
	if got := minutesOf(result.Segments, DeviceMac, "T3 Code", ""); got != 20 {
		t.Fatalf("T3 Code = %v, want 20 (afk until 40)", got)
	}
	summary := Summarize(result, 10, nil)
	if summary[0].Device != DeviceMac || summary[0].ActiveMinutes != 40 || summary[0].Sites[0].Name != "x.com" {
		t.Fatalf("summary = %+v", summary[0])
	}
}

func TestPhoneSessionsEndAtPauseNextAppOrScreenOff(t *testing.T) {
	records := []Record{
		usage("resumed", "com.twitter.android", 0),
		usage("resumed", "com.whatsapp", 10), // switch without pause
		usage("paused", "com.whatsapp", 12),
		usage("resumed", "com.google.android.youtube", 20),
		phone("state", 25, `{"screen_interactive":false}`),
		usage("resumed", "tw.nekomimi.nekogram", 40),
	}
	result := Build(records, at(0), at(45))
	for app, want := range map[string]float64{"com.twitter.android": 10, "com.whatsapp": 2, "com.google.android.youtube": 5, "tw.nekomimi.nekogram": 5} {
		if got := minutesOf(result.Segments, DevicePhone, app, ""); got != want {
			t.Fatalf("%s = %v, want %v", app, got, want)
		}
	}
}

func TestSilenceIsReportedNotCountedAsInactivity(t *testing.T) {
	records := []Record{
		phone("state", 0, `{"screen_interactive":true}`),
		phone("state", 1, `{"screen_interactive":true}`),
		phone("state", 50, `{"screen_interactive":true}`),
	}
	result := Build(records, at(0), at(60))
	var phoneGaps, macGaps []Gap
	for _, gap := range result.Gaps {
		if gap.Device == DevicePhone {
			phoneGaps = append(phoneGaps, gap)
		} else {
			macGaps = append(macGaps, gap)
		}
	}
	if len(phoneGaps) != 1 || !phoneGaps[0].Start.Equal(at(1)) || !phoneGaps[0].End.Equal(at(50)) {
		t.Fatalf("phone gaps = %+v", phoneGaps)
	}
	if len(macGaps) != 1 || macGaps[0].End.Sub(macGaps[0].Start) != time.Hour {
		t.Fatalf("a device with no records must show one gap over the whole range: %+v", macGaps)
	}
}

func TestLongRecordCoversItsSpanForSilence(t *testing.T) {
	records := []Record{aw("aw-watcher-afk_h", 0, 50, `{"status":"afk"}`), aw("aw-watcher-afk_h", 50, 10, `{"status":"not-afk"}`)}
	for _, gap := range Build(records, at(0), at(60)).Gaps {
		if gap.Device == DeviceMac {
			t.Fatalf("a 50-minute afk record is evidence, not silence: %+v", gap)
		}
	}
}

func TestMacWindowTimeWithoutAFKRecordsCountsAsUnknown(t *testing.T) {
	records := []Record{
		aw("aw-watcher-window_h", 0, 60, `{"app":"T3 Code","title":"x"}`),
		aw("aw-watcher-afk_h", 10, 10, `{"status":"afk"}`),
		aw("aw-watcher-afk_h", 20, 10, `{"status":"not-afk"}`),
	}
	summary := Summarize(Build(records, at(0), at(60)), 5, nil)[0]
	if summary.ActiveMinutes != 50 || summary.AFKUnknownMinutes != 40 {
		t.Fatalf("active %v unknown %v, want 50 and 40", summary.ActiveMinutes, summary.AFKUnknownMinutes)
	}
}

func TestLabelsPreferSitesOverTheBrowser(t *testing.T) {
	labels := []Label{
		{Kind: LabelKindApp, Match: "helium", Label: "work"},
		{Kind: LabelKindSite, Match: "youtube.com", Label: "distraction"},
		{Kind: LabelKindApp, Match: "com.twitter.android", Label: "distraction"},
	}
	cases := map[string]Segment{
		"distraction": {App: "Helium", Domain: "m.youtube.com"},
		"work":        {App: "Helium", Domain: "github.com"},
		"unlabeled":   {App: "Finder"},
	}
	for want, segment := range cases {
		if got := LabelFor(labels, segment); got != want {
			t.Fatalf("%+v = %s, want %s", segment, got, want)
		}
	}
	change := LabelChange{Action: "set", Kind: "site", Match: "https://www.YouTube.com/shorts/1", Label: " distraction "}
	if err := change.Normalize(); err != nil || change.Match != "youtube.com" || change.Label != "distraction" {
		t.Fatalf("normalized = %+v, %v", change, err)
	}
	result := Build([]Record{
		usage("resumed", "com.twitter.android", 0), usage("paused", "com.twitter.android", 30),
		usage("resumed", "com.whatsapp", 30), usage("paused", "com.whatsapp", 40),
	}, at(0), at(60))
	phone := Summarize(result, 10, labels)[1]
	if len(phone.Labels) != 2 || phone.Labels[0].Name != "distraction" || phone.Labels[0].Minutes != 30 {
		t.Fatalf("labels = %+v", phone.Labels)
	}
}
