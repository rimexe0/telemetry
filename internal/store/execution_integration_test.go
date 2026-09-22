package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestPostgresExecutionSessionsRetainLifecycleAndMarkers(t *testing.T) {
	databaseURL := os.Getenv("TELEMETRY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TELEMETRY_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	database, err := OpenPostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	if err := database.ApplyMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	if err := clearIntegrationData(ctx, database); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := clearIntegrationData(ctx, database); err != nil {
			t.Error(err)
		}
	})

	activity, err := database.CreateActivity(ctx, Activity{
		Title:        "Write the outline",
		NextAction:   "Open the outline file",
		ResumeMarker: "Start at the introduction",
	})
	if err != nil {
		t.Fatalf("create activity: %v", err)
	}
	startAt := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	dayPlan, err := database.SavePlan(ctx, startAt.Truncate(24*time.Hour), PlanInput{Blocks: []PlanBlockSpec{{
		ActivityID:      &activity.ID,
		StartTime:       startAt,
		DurationMinutes: 45,
	}}})
	if err != nil {
		t.Fatalf("save plan: %v", err)
	}

	if _, err := database.StartExecutionSession(ctx, ExecutionSessionInput{ActivityID: 99999, StartedAt: &startAt}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown activity error = %v", err)
	}
	started, err := database.StartExecutionSession(ctx, ExecutionSessionInput{
		ActivityID:  activity.ID,
		PlanBlockID: &dayPlan.Blocks[0].ID,
		StartedAt:   &startAt,
	})
	if err != nil {
		t.Fatalf("start execution: %v", err)
	}
	if started.NextActionSnapshot != "Open the outline file" || started.ResumeMarker == nil || *started.ResumeMarker != "Start at the introduction" {
		t.Fatalf("start snapshot = %+v", started)
	}
	if len(started.Events) != 1 || started.Events[0].EventType != "started" {
		t.Fatalf("start events = %+v", started.Events)
	}

	waitingAt := startAt.Add(10 * time.Minute)
	waiting, err := database.TransitionExecutionSession(ctx, started.ID, ExecutionEventInput{EventType: "waiting_external", OccurredAt: &waitingAt, Note: "Waiting for the build"})
	if err != nil || waiting.State != "waiting_external" || waiting.EndedAt != nil {
		t.Fatalf("waiting state = %+v, %v", waiting, err)
	}
	current, err := database.CurrentExecutionSession(ctx)
	if err != nil || current == nil || current.ID != started.ID || current.State != "waiting_external" {
		t.Fatalf("current waiting session = %+v, %v", current, err)
	}

	interruptedAt := startAt.Add(20 * time.Minute)
	interrupted, err := database.TransitionExecutionSession(ctx, started.ID, ExecutionEventInput{EventType: "interrupted", OccurredAt: &interruptedAt})
	if err != nil || interrupted.State != "interrupted" {
		t.Fatalf("interrupted state = %+v, %v", interrupted, err)
	}
	resumedAt := startAt.Add(25 * time.Minute)
	if _, err := database.TransitionExecutionSession(ctx, started.ID, ExecutionEventInput{EventType: "resumed", OccurredAt: &resumedAt}); err != nil {
		t.Fatalf("resume after interruption: %v", err)
	}
	marker := "Resume by checking the first paragraph"
	switchedAt := startAt.Add(30 * time.Minute)
	switched, err := database.TransitionExecutionSession(ctx, started.ID, ExecutionEventInput{
		EventType:    "switched_out",
		OccurredAt:   &switchedAt,
		ResumeMarker: &marker,
		Note:         "Intentional switch",
	})
	if err != nil || switched.State != "interrupted" || switched.ResumeMarker == nil || *switched.ResumeMarker != marker {
		t.Fatalf("switched state = %+v, %v", switched, err)
	}
	returnedAt := startAt.Add(35 * time.Minute)
	if _, err := database.TransitionExecutionSession(ctx, started.ID, ExecutionEventInput{EventType: "resumed", OccurredAt: &returnedAt}); err != nil {
		t.Fatalf("resume after switch: %v", err)
	}
	completedAt := startAt.Add(45 * time.Minute)
	completed, err := database.TransitionExecutionSession(ctx, started.ID, ExecutionEventInput{EventType: "completed", OccurredAt: &completedAt})
	if err != nil || completed.State != "completed" || completed.EndedAt == nil {
		t.Fatalf("completed state = %+v, %v", completed, err)
	}
	if completed.ResumeMarker == nil || *completed.ResumeMarker != marker {
		t.Fatalf("completed resume marker = %+v", completed.ResumeMarker)
	}
	if len(completed.Events) != 7 {
		t.Fatalf("append-only event history = %d: %+v", len(completed.Events), completed.Events)
	}
	if _, err := database.TransitionExecutionSession(ctx, started.ID, ExecutionEventInput{EventType: "resumed", OccurredAt: &completedAt}); !errors.Is(err, ErrInvalidExecutionTransition) {
		t.Fatalf("completed resume error = %v", err)
	}

	abandoned, err := database.StartExecutionSession(ctx, ExecutionSessionInput{ActivityID: activity.ID, StartedAt: &completedAt})
	if err != nil {
		t.Fatalf("start second execution: %v", err)
	}
	abandonedAt := completedAt.Add(5 * time.Minute)
	abandoned, err = database.TransitionExecutionSession(ctx, abandoned.ID, ExecutionEventInput{EventType: "abandoned", OccurredAt: &abandonedAt, Note: "No longer relevant"})
	if err != nil || abandoned.State != "abandoned" || abandoned.EndedAt == nil {
		t.Fatalf("abandoned state = %+v, %v", abandoned, err)
	}
	if completed.State == abandoned.State {
		t.Fatal("completed and abandoned collapsed into one state")
	}

	current, err = database.CurrentExecutionSession(ctx)
	if err != nil || current != nil {
		t.Fatalf("terminal current session = %+v, %v", current, err)
	}
	sessions, err := database.ListExecutionSessions(ctx, 10)
	if err != nil || len(sessions) != 2 || len(sessions[0].Events) != 0 {
		t.Fatalf("recent sessions = %+v, %v", sessions, err)
	}
}
