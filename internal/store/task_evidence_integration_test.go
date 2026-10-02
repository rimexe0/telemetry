package store

import (
	"errors"
	"testing"
	"time"
)

func TestPostgresTaskEvidenceAndFocusSessions(t *testing.T) {
	ctx, database := openCleanIntegration(t)
	title, other := "Science fair presentation", "Dad's album"
	_, tasks, err := database.ApplyTaskChanges(ctx, []TaskChange{{Action: "add", Title: &title}, {Action: "add", Title: &other}}, "tasks")
	if err != nil {
		t.Fatal(err)
	}
	slides, album := tasks[0].ID, tasks[1].ID

	// The judge's guess is replaced by the owner's answer, and not the other way round.
	if _, err := database.AddTaskEvidence(ctx, TaskEvidence{TaskID: slides, Context: "trendyol.com", Verdict: EvidenceRelated, Source: EvidenceJudge, Reason: "looked like research"}); err != nil {
		t.Fatal(err)
	}
	owner, err := database.AddTaskEvidence(ctx, TaskEvidence{TaskID: slides, Context: "Trendyol.com", Verdict: EvidenceUnrelated, Source: EvidenceOwner, DeviceID: "mac-1"})
	if err != nil || owner.Verdict != EvidenceUnrelated {
		t.Fatalf("owner = %+v, %v", owner, err)
	}
	kept, err := database.AddTaskEvidence(ctx, TaskEvidence{TaskID: slides, Context: "trendyol.com", Verdict: EvidenceRelated, Source: EvidenceJudge})
	if err != nil || kept.Source != EvidenceOwner || kept.Verdict != EvidenceUnrelated {
		t.Fatalf("a judge must not replace the owner's answer: %+v, %v", kept, err)
	}
	// The owner can change their mind; the newest answer is the one in force.
	if _, err := database.AddTaskEvidence(ctx, TaskEvidence{TaskID: slides, Context: "trendyol.com", Verdict: EvidenceRelated, Source: EvidenceOwner}); err != nil {
		t.Fatal(err)
	}
	list, _ := database.ListTaskEvidence(ctx)
	if len(list) != 1 || list[0].Verdict != EvidenceRelated || list[0].Source != EvidenceOwner {
		t.Fatalf("current memory = %+v", list)
	}
	if _, err := database.AddTaskEvidence(ctx, TaskEvidence{TaskID: slides, Context: "x", Verdict: EvidenceUnsure, Source: EvidenceOwner}); !errors.Is(err, ErrInvalidEvidence) {
		t.Fatalf("owner unsure = %v", err)
	}
	if _, err := database.AddTaskEvidence(ctx, TaskEvidence{TaskID: slides + 50, Context: "x", Verdict: EvidenceRelated, Source: EvidenceOwner}); err != ErrNotFound {
		t.Fatalf("unknown task = %v", err)
	}

	// Focus: start, pause, resume, switch task, done.
	started, err := ChangeFocus(ctx, database, FocusStart, slides)
	if err != nil || started.ActivityID != slides || started.State != executionStateActive {
		t.Fatalf("start = %+v, %v", started, err)
	}
	if paused, err := ChangeFocus(ctx, database, FocusPause, 0); err != nil || paused.State != executionStateInterrupted {
		t.Fatalf("pause = %+v, %v", paused, err)
	}
	if resumed, err := ChangeFocus(ctx, database, FocusStart, slides); err != nil || resumed.ID != started.ID || resumed.State != executionStateActive {
		t.Fatalf("starting the same task resumes it: %+v, %v", resumed, err)
	}
	switched, err := ChangeFocus(ctx, database, FocusStart, album)
	if err != nil || switched.ActivityID != album || switched.ID == started.ID {
		t.Fatalf("switch = %+v, %v", switched, err)
	}
	if done, err := ChangeFocus(ctx, database, FocusDone, 0); err != nil || done.EndedAt == nil {
		t.Fatalf("done = %+v, %v", done, err)
	}
	if none, err := ChangeFocus(ctx, database, FocusDone, 0); err != nil || (none != nil && none.EndedAt == nil) {
		t.Fatalf("done with nothing open = %+v, %v", none, err)
	}
	spans, err := LoadFocusSpans(ctx, database, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	// Two stretches for the presentation (before and after the pause) and one for the album.
	bySlides, byAlbum := 0, 0
	for _, span := range spans {
		if span.TaskID == slides {
			bySlides++
		}
		if span.TaskID == album {
			byAlbum++
		}
	}
	if byAlbum != 1 || bySlides < 1 {
		t.Fatalf("spans = %+v", spans)
	}
	activities, _ := database.ListActivities(ctx, ActivityFilter{Limit: 10})
	for _, activity := range activities {
		if activity.Status != "active" {
			t.Fatalf("ending a focus session must not change the task: %+v", activity)
		}
	}
}
