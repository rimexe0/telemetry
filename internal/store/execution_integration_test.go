package store

import (
	"context"
	"encoding/json"
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
	if _, err := database.SavePlan(ctx, startAt.Truncate(24*time.Hour), PlanInput{Blocks: []PlanBlockSpec{{
		ActivityID:      &activity.ID,
		StartTime:       startAt.Add(15 * time.Minute),
		DurationMinutes: 90,
	}}}); err != nil {
		t.Fatalf("save revised plan: %v", err)
	}
	retainedBlocks, err := database.PlanBlocksByIDs(ctx, []int64{dayPlan.Blocks[0].ID})
	if err != nil || len(retainedBlocks) != 1 || retainedBlocks[0].DurationMinutes != 45 || !retainedBlocks[0].StartTime.Equal(startAt) {
		t.Fatalf("retained execution plan block = %+v, %v", retainedBlocks, err)
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
	if len(started.Events) != 1 || started.Events[0].EventType != executionEventStarted || len(started.Events[0].Provenance) == 0 {
		t.Fatalf("start events = %+v", started.Events)
	}

	waitingAt := startAt.Add(10 * time.Minute)
	waiting, err := database.TransitionExecutionSession(ctx, started.ID, ExecutionEventInput{EventType: executionEventWaitingExternal, OccurredAt: &waitingAt, Note: "Waiting for the build", Metadata: json.RawMessage(`{"reason_category":"agent_wait"}`)})
	if err != nil || waiting.State != executionStateWaitingExternal || waiting.EndedAt != nil {
		t.Fatalf("waiting state = %+v, %v", waiting, err)
	}
	var waitingMetadata map[string]string
	if len(waiting.Events) != 2 || json.Unmarshal(waiting.Events[1].Metadata, &waitingMetadata) != nil || waitingMetadata["reason_category"] != "agent_wait" || len(waiting.Events[1].Provenance) == 0 {
		t.Fatalf("waiting event context = %+v", waiting.Events)
	}
	current, err := database.CurrentExecutionSession(ctx)
	if err != nil || current == nil || current.ID != started.ID || current.State != executionStateWaitingExternal {
		t.Fatalf("current waiting session = %+v, %v", current, err)
	}

	interruptedAt := startAt.Add(20 * time.Minute)
	interrupted, err := database.TransitionExecutionSession(ctx, started.ID, ExecutionEventInput{EventType: executionEventInterrupted, OccurredAt: &interruptedAt})
	if err != nil || interrupted.State != executionStateInterrupted {
		t.Fatalf("interrupted state = %+v, %v", interrupted, err)
	}
	resumedAt := startAt.Add(25 * time.Minute)
	resumed, err := database.TransitionExecutionSession(ctx, started.ID, ExecutionEventInput{EventType: executionEventResumed, OccurredAt: &resumedAt})
	if err != nil || resumed.State != executionStateActive {
		t.Fatalf("resumed state = %+v, %v", resumed, err)
	}
	if len(resumed.Events) != 4 || resumed.Events[2].EventType != executionEventInterrupted || resumed.Events[3].EventType != executionEventResumed {
		t.Fatalf("interrupt/resume history = %+v", resumed.Events)
	}

	marker := "Resume by checking the first paragraph"
	switchedAt := startAt.Add(30 * time.Minute)
	switched, err := database.TransitionExecutionSession(ctx, started.ID, ExecutionEventInput{
		EventType:    executionEventSwitchedOut,
		OccurredAt:   &switchedAt,
		ResumeMarker: &marker,
		Note:         "Intentional switch",
	})
	if err != nil || switched.State != executionStateInterrupted || switched.ResumeMarker == nil || *switched.ResumeMarker != marker {
		t.Fatalf("switched state = %+v, %v", switched, err)
	}
	returnedAt := startAt.Add(35 * time.Minute)
	if _, err := database.TransitionExecutionSession(ctx, started.ID, ExecutionEventInput{EventType: executionEventResumed, OccurredAt: &returnedAt}); err != nil {
		t.Fatalf("resume after switch: %v", err)
	}
	stuckUnknownAt := startAt.Add(36 * time.Minute)
	if _, err := database.TransitionExecutionSession(ctx, started.ID, ExecutionEventInput{EventType: executionEventStuckUnknownNextAction, OccurredAt: &stuckUnknownAt, Note: "I do not know the next action"}); err != nil {
		t.Fatalf("stuck unknown event: %v", err)
	}
	stuckOverwhelmAt := startAt.Add(37 * time.Minute)
	if _, err := database.TransitionExecutionSession(ctx, started.ID, ExecutionEventInput{EventType: executionEventStuckEmotionalOverwhelm, OccurredAt: &stuckOverwhelmAt, Note: "I know what to do but feel overwhelmed"}); err != nil {
		t.Fatalf("stuck overwhelm event: %v", err)
	}
	overwhelmedAt := startAt.Add(38 * time.Minute)
	if _, err := database.TransitionExecutionSession(ctx, started.ID, ExecutionEventInput{EventType: executionEventOverwhelmed, OccurredAt: &overwhelmedAt}); err != nil {
		t.Fatalf("overwhelmed event: %v", err)
	}
	recoveryStartAt := startAt.Add(39 * time.Minute)
	if _, err := database.TransitionExecutionSession(ctx, started.ID, ExecutionEventInput{EventType: executionEventOfflineRecoveryStarted, OccurredAt: &recoveryStartAt, Metadata: json.RawMessage(`{"recovery_method":"offline_walk"}`)}); err != nil {
		t.Fatalf("recovery start event: %v", err)
	}
	ownerExplanationAt := startAt.Add(39*time.Minute + 30*time.Second)
	if _, err := database.TransitionExecutionSession(ctx, started.ID, ExecutionEventInput{EventType: executionEventOwnerExplanation, OccurredAt: &ownerExplanationAt, Note: "I am taking a short offline break"}); err != nil {
		t.Fatalf("owner explanation during recovery: %v", err)
	}
	recoveryEndAt := startAt.Add(40 * time.Minute)
	if _, err := database.TransitionExecutionSession(ctx, started.ID, ExecutionEventInput{EventType: executionEventOfflineRecoveryEnded, OccurredAt: &recoveryEndAt}); err != nil {
		t.Fatalf("recovery end event: %v", err)
	}
	recoveryReturnAt := startAt.Add(41 * time.Minute)
	if _, err := database.TransitionExecutionSession(ctx, started.ID, ExecutionEventInput{EventType: executionEventResumed, OccurredAt: &recoveryReturnAt}); err != nil {
		t.Fatalf("resume after recovery: %v", err)
	}
	completedAt := startAt.Add(45 * time.Minute)
	completed, err := database.TransitionExecutionSession(ctx, started.ID, ExecutionEventInput{EventType: executionEventCompleted, OccurredAt: &completedAt})
	if err != nil || completed.State != executionStateCompleted || completed.EndedAt == nil {
		t.Fatalf("completed state = %+v, %v", completed, err)
	}
	if completed.ResumeMarker == nil || *completed.ResumeMarker != marker {
		t.Fatalf("completed resume marker = %+v", completed.ResumeMarker)
	}
	if len(completed.Events) != 14 {
		t.Fatalf("append-only event history = %d: %+v", len(completed.Events), completed.Events)
	}
	readBack, err := database.GetExecutionSession(ctx, started.ID)
	if err != nil || readBack.ResumeMarker == nil || *readBack.ResumeMarker != marker || len(readBack.Events) != 14 || readBack.Events[10].EventType != executionEventOwnerExplanation {
		t.Fatalf("read-back session = %+v, %v", readBack, err)
	}
	var recoveryMetadata map[string]string
	if json.Unmarshal(readBack.Events[9].Metadata, &recoveryMetadata) != nil || recoveryMetadata["recovery_method"] != "offline_walk" {
		t.Fatalf("read-back recovery metadata = %s", readBack.Events[9].Metadata)
	}
	if _, err := database.TransitionExecutionSession(ctx, started.ID, ExecutionEventInput{EventType: executionEventResumed, OccurredAt: &completedAt}); !errors.Is(err, ErrInvalidExecutionTransition) {
		t.Fatalf("completed resume error = %v", err)
	}

	abandoned, err := database.StartExecutionSession(ctx, ExecutionSessionInput{ActivityID: activity.ID, StartedAt: &completedAt})
	if err != nil {
		t.Fatalf("start second execution: %v", err)
	}
	abandonedAt := completedAt.Add(5 * time.Minute)
	abandoned, err = database.TransitionExecutionSession(ctx, abandoned.ID, ExecutionEventInput{EventType: executionEventAbandoned, OccurredAt: &abandonedAt, Note: "No longer relevant"})
	if err != nil || abandoned.State != executionStateAbandoned || abandoned.EndedAt == nil {
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
