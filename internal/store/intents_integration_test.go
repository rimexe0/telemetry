package store

import (
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/intent"
)

func TestPostgresIntentReadings(t *testing.T) {
	ctx, database := openCleanIntegration(t)
	if latest, err := database.LatestIntentReading(ctx); err != nil || latest != nil {
		t.Fatalf("before any reading = %+v, %v", latest, err)
	}
	start := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	first, err := database.SaveIntentReading(ctx, start, start.Add(2*time.Hour), "digest one", "raw one", intent.Reading{
		Aims: []intent.Aim{
			{Title: "Fix the tablet", Kind: intent.KindWork, Status: intent.StatusWaiting, Evidence: []string{"agent working"}},
			{Title: "Watching car videos", Kind: intent.KindLeisure, Status: intent.StatusActive},
		},
		Now:  1,
		Note: "tablet and videos",
	})
	if err != nil || first.ID == 0 || first.NowIntentID == nil {
		t.Fatalf("first = %+v, %v", first, err)
	}
	listed, err := database.ListIntents(ctx, start, start.Add(6*time.Hour))
	if err != nil || len(listed) != 2 {
		t.Fatalf("listed = %+v, %v", listed, err)
	}
	var tablet, videos Intent
	for _, item := range listed {
		switch item.Title {
		case "Fix the tablet":
			tablet = item
		case "Watching car videos":
			videos = item
		}
	}
	// A reading of the past does not see aims that began after it.
	if before, err := database.ListIntents(ctx, start, start.Add(time.Hour)); err != nil || len(before) != 0 {
		t.Fatalf("before = %+v, %v", before, err)
	}
	if *first.NowIntentID != videos.ID || len(tablet.Evidence) != 1 || len(videos.Evidence) != 0 || videos.Evidence == nil {
		t.Fatalf("tablet %+v videos %+v now %d", tablet, videos, *first.NowIntentID)
	}

	// Three hours later only the tablet continues: it keeps its id, and the
	// videos, not seen in this window, are paused.
	later := start.Add(3 * time.Hour)
	second, err := database.SaveIntentReading(ctx, later, later.Add(2*time.Hour), "digest two", "raw two", intent.Reading{
		Aims: []intent.Aim{{ID: &tablet.ID, Title: "Fix the tablet's touchscreen", Kind: intent.KindWork, Status: intent.StatusActive, NextStep: "flash the driver"}},
		Now:  0,
	})
	if err != nil || second.NowIntentID == nil || *second.NowIntentID != tablet.ID {
		t.Fatalf("second = %+v, %v", second, err)
	}
	listed, err = database.ListIntents(ctx, start, later.Add(3*time.Hour))
	if err != nil || len(listed) != 2 {
		t.Fatalf("listed = %+v, %v", listed, err)
	}
	for _, item := range listed {
		switch item.ID {
		case tablet.ID:
			if item.Title != "Fix the tablet's touchscreen" || item.Status != intent.StatusActive || !item.LastSeen.Equal(later.Add(2*time.Hour)) ||
				!item.FirstSeen.Equal(start.Add(2*time.Hour)) || item.ReadingID != second.ID {
				t.Fatalf("tablet = %+v", item)
			}
		case videos.ID:
			if item.Status != intent.StatusPaused || item.ReadingID != second.ID || !item.LastSeen.Equal(start.Add(2*time.Hour)) {
				t.Fatalf("videos = %+v", item)
			}
		}
	}
	// An id that no longer exists becomes a new intent instead of failing.
	missing := int64(9999)
	if _, err := database.SaveIntentReading(ctx, later, later.Add(2*time.Hour), "digest three", "raw three", intent.Reading{
		Aims: []intent.Aim{{ID: &missing, Title: "Pay rent", Kind: intent.KindLife, Status: intent.StatusActive}}, Now: -1,
	}); err != nil {
		t.Fatal(err)
	}
	if latest, err := database.LatestIntentReading(ctx); err != nil || latest == nil || latest.NowIntentID != nil {
		t.Fatalf("latest = %+v, %v", latest, err)
	}
	// Only recent ones, plus any still active.
	if recent, err := database.ListIntents(ctx, later.Add(3*time.Hour), later.Add(3*time.Hour)); err != nil || len(recent) != 2 {
		t.Fatalf("recent = %+v, %v", recent, err)
	}
}
