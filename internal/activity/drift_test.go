package activity

import (
	"testing"
	"time"
)

func TestDriftEpisodesSayWhatCameBeforeAndWhetherWorkResumed(t *testing.T) {
	at := func(hour, minute int) time.Time { return time.Date(2026, 10, 20, hour, minute, 0, 0, time.UTC) }
	day := Day{Chunks: []Chunk{
		{Device: DevicePhone, Start: at(10, 0), End: at(10, 30), Kind: "distraction", ActiveMinutes: 28, DistractionMinutes: 26,
			Items: []DayItem{{Name: "Twitter", Minutes: 20}, {Name: "YouTube", Minutes: 6}}},
		{Device: DeviceMac, Start: at(9, 0), End: at(9, 55), Kind: "task", Task: "Telemetry", ActiveMinutes: 50,
			Items: []DayItem{{Name: "T3 Code", Detail: "Telemetry · Fix sleep", Minutes: 40}}},
		{Device: DeviceMac, Start: at(10, 40), End: at(11, 30), Kind: "task", Task: "Telemetry", ActiveMinutes: 45},
		// Too short to be an episode.
		{Device: DevicePhone, Start: at(12, 0), End: at(12, 4), Kind: "distraction", ActiveMinutes: 4},
		// Not followed by work within an hour.
		{Device: DevicePhone, Start: at(22, 0), End: at(23, 0), Kind: "distraction", ActiveMinutes: 55},
	}}
	episodes := DriftEpisodes(day, 10)
	if len(episodes) != 2 {
		t.Fatalf("episodes = %+v", episodes)
	}
	first := episodes[0]
	if first.Before == nil || first.Before.Top != "T3 Code · Telemetry · Fix sleep" || first.Before.GapMinutes != 5 {
		t.Fatalf("before = %+v", first.Before)
	}
	if !first.CameBack || !first.BackToTask || first.After.GapMinutes != 10 || first.On[0].Name != "Twitter" {
		t.Fatalf("first = %+v", first)
	}
	if last := episodes[1]; last.CameBack || last.After != nil {
		t.Fatalf("late episode = %+v", last)
	}
}
