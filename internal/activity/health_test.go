package activity

import (
	"encoding/json"
	"testing"
	"time"
)

func TestHealthDaysNeverAddsSourcesTogether(t *testing.T) {
	zone, _ := time.LoadLocation("Europe/Istanbul")
	at := time.Date(2026, 10, 31, 0, 0, 0, 0, zone)
	record := func(kind, payload string) Record {
		return Record{Kind: kind, Start: at, Payload: json.RawMessage(payload)}
	}
	records := []Record{
		// The same walk from the phone and from Samsung Health, through
		// Health Connect, and from Samsung Health directly.
		record("health_connect.steps", `{"count":4000,"start_time":"2026-10-31T09:00:00Z","metadata":{"data_origin":"com.android.healthconnect.phone.j0ae"}}`),
		record("health_connect.steps", `{"count":4500,"start_time":"2026-10-31T09:00:00Z","metadata":{"data_origin":"com.sec.android.app.shealth"}}`),
		record("samsung_health.steps", `{"total":4500,"minutes":[{"count":2500,"start":"2026-10-31T09:00:00Z"},{"count":2000,"start":"2026-10-31T09:01:00Z"}]}`),
		// One night from two sources; it ended on 1 November local time.
		record("health_connect.sleep_session", `{"start_time":"2026-10-31T21:00:00Z","end_time":"2026-11-01T05:00:00Z","stages":[{"stage":4,"start_time":"2026-10-31T21:00:00Z","end_time":"2026-10-31T23:00:00Z"},{"stage":1,"start_time":"2026-10-31T23:00:00Z","end_time":"2026-10-31T23:30:00Z"}]}`),
		record("samsung_health.sleep", `{"sessions":[{"start":"2026-10-31T21:10:00Z","end":"2026-11-01T04:50:00Z","stages":[{"stage":"AWAKE","start":"2026-10-31T23:00:00Z","end":"2026-10-31T23:20:00Z"}]}]}`),
		record("health_connect.heart_rate", `{"samples":[{"time":"2026-10-31T10:00:00Z","beats_per_minute":60},{"time":"2026-10-31T10:01:00Z","beats_per_minute":90},{"time":"2026-10-31T10:02:00Z","beats_per_minute":70}]}`),
		record("samsung_health.heart_rate", `{"series":[{"bpm":80,"start":"2026-10-31T10:00:00Z"}]}`),
	}
	days := HealthDays(records, at, at.AddDate(0, 0, 2), zone)
	if len(days) != 2 || days[0].Date != "2026-10-31" || days[1].Date != "2026-11-01" {
		t.Fatalf("days = %+v", days)
	}
	first := days[0]
	if first.Steps == nil || *first.Steps != 4500 || len(first.StepsBySource) != 3 {
		t.Fatalf("steps = %v %v, want the highest source, 4500, not a sum", first.Steps, first.StepsBySource)
	}
	if first.HeartRate == nil || first.HeartRate.Source != "health_connect" || first.HeartRate.Min != 60 || first.HeartRate.Median != 70 || first.HeartRate.Max != 90 {
		t.Fatalf("heart rate = %+v", first.HeartRate)
	}
	if first.Sleep != nil {
		t.Fatalf("the night belongs to the day it ended: %+v", first.Sleep)
	}
	sleep := days[1].Sleep
	if sleep == nil || sleep.InBedMinutes != 480 || sleep.AsleepMinutes != 450 || len(sleep.Sources) != 2 {
		t.Fatalf("sleep = %+v, want one 8-hour night from both sources, 30 minutes awake", sleep)
	}
	if days[1].Steps != nil || days[1].HeartRate != nil {
		t.Fatalf("a day without data has none: %+v", days[1])
	}
}
