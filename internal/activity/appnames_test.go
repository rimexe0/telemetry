package activity

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAppNamesNameOnlyThePhonesPackages(t *testing.T) {
	at := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	end := at.Add(10 * time.Minute)
	records := []Record{
		{Source: "android:p:usage", Kind: "android.usage.activity_resumed", Start: at, Payload: json.RawMessage(`{"package_name":"com.twitter.android"}`)},
		{Source: "android:p:usage", Kind: "android.usage.activity_paused", Start: end, Payload: json.RawMessage(`{"package_name":"com.twitter.android"}`)},
	}
	result := Build(records, at, end)
	result.Segments = append(result.Segments, Segment{Device: DeviceMac, Start: at, End: end, App: "com.twitter.android"})
	names := AppNames{"com.twitter.android": "X"}

	summaries := Summarize(result, 10, nil)
	names.Summaries(summaries)
	for _, summary := range summaries {
		want := ""
		if summary.Device == DevicePhone {
			want = "X"
		}
		if len(summary.Apps) != 1 || summary.Apps[0].Name != "com.twitter.android" || summary.Apps[0].AppName != want {
			t.Fatalf("%s apps = %+v, want the package kept and app_name %q", summary.Device, summary.Apps, want)
		}
	}

	names.Segments(result.Segments)
	if result.Segments[0].AppName != "X" || result.Segments[0].App != "com.twitter.android" || result.Segments[1].AppName != "" {
		t.Fatalf("segments = %+v", result.Segments)
	}

	review := PlanReview{Blocks: []BlockReview{{Observed: &Observed{Devices: Summarize(result, 10, nil)}}, {}}, Outside: Observed{Devices: Summarize(result, 10, nil)}}
	names.Review(&review)
	if review.Blocks[0].Observed.Devices[1].Apps[0].AppName != "X" || review.Outside.Devices[1].Apps[0].AppName != "X" {
		t.Fatalf("review = %+v", review)
	}

	day := Day{Chunks: []Chunk{
		{Device: DevicePhone, Items: []DayItem{{Name: "com.twitter.android"}, {Name: "com.unknown"}}, Segments: []DaySegment{{Name: "com.twitter.android"}}},
		{Device: DeviceMac, Items: []DayItem{{Name: "com.twitter.android"}}},
	}}
	names.Day(&day)
	if day.Chunks[0].Items[0].AppName != "X" || day.Chunks[0].Items[1].AppName != "" || day.Chunks[0].Segments[0].AppName != "X" || day.Chunks[1].Items[0].AppName != "" {
		t.Fatalf("day = %+v", day)
	}

	got := names.Insights([]Insight{{Names: []string{"com.twitter.android"}}, {Names: []string{"youtube.com"}}})
	if len(got) != 1 || got["com.twitter.android"] != "X" {
		t.Fatalf("insight names = %v", got)
	}
	// Without names nothing changes and nothing fails.
	AppNames(nil).Summaries(summaries)
}
