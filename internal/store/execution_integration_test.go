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

func TestPostgresPrimaryWaitingInterleaveAndReturnAreBounded(t *testing.T) {
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

	primaryActivity, err := database.CreateActivity(ctx, Activity{Title: "Paid-work task", NextAction: "Send the draft", ResumeMarker: "Return to the draft"})
	if err != nil {
		t.Fatal(err)
	}
	interleaveActivity, err := database.CreateActivity(ctx, Activity{Title: "Small secondary task", NextAction: "Archive one receipt", ResumeMarker: "Resume at receipt two"})
	if err != nil {
		t.Fatal(err)
	}
	startAt := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	primary, err := database.StartExecutionSession(ctx, ExecutionSessionInput{ActivityID: primaryActivity.ID, StartedAt: &startAt})
	if err != nil || primary.Role != ExecutionRolePrimary || primary.PrimarySessionID != nil {
		t.Fatalf("start primary = %+v, %v", primary, err)
	}
	if _, err := database.TransitionExecutionSession(ctx, primary.ID, ExecutionEventInput{EventType: executionEventExternalReady, OccurredAt: &startAt}); !errors.Is(err, ErrInvalidExecutionTransition) {
		t.Fatalf("external_ready before waiting error = %v", err)
	}
	if _, err := database.StartExecutionSession(ctx, ExecutionSessionInput{ActivityID: primaryActivity.ID, StartedAt: &startAt}); !errors.Is(err, ErrExecutionConflict) {
		t.Fatalf("second open primary error = %v", err)
	}
	if _, err := database.StartExecutionSession(ctx, ExecutionSessionInput{ActivityID: interleaveActivity.ID, Role: ExecutionRoleWaitingInterleave, PrimarySessionID: &primary.ID, StartedAt: &startAt}); !errors.Is(err, ErrExecutionConflict) {
		t.Fatalf("interleave under active primary error = %v", err)
	}
	waitingAt := startAt.Add(5 * time.Minute)
	primary, err = database.TransitionExecutionSession(ctx, primary.ID, ExecutionEventInput{EventType: executionEventWaitingExternal, OccurredAt: &waitingAt, ResumeMarker: stringPointer("Resume primary at the draft")})
	if err != nil || primary.State != executionStateWaitingExternal {
		t.Fatalf("primary waiting = %+v, %v", primary, err)
	}
	interleaveStart := waitingAt.Add(time.Minute)
	tooEarly := waitingAt.Add(-time.Second)
	if _, err := database.StartExecutionSession(ctx, ExecutionSessionInput{ActivityID: interleaveActivity.ID, Role: ExecutionRoleWaitingInterleave, PrimarySessionID: &primary.ID, StartedAt: &tooEarly}); !errors.Is(err, ErrInvalidExecutionInput) {
		t.Fatalf("interleave before waiting error = %v", err)
	}
	type interleaveStartResult struct {
		session ExecutionSession
		err     error
	}
	startResults := make(chan interleaveStartResult, 2)
	for range 2 {
		go func() {
			session, err := database.StartExecutionSession(ctx, ExecutionSessionInput{
				ActivityID:       interleaveActivity.ID,
				Role:             ExecutionRoleWaitingInterleave,
				PrimarySessionID: &primary.ID,
				StartedAt:        &interleaveStart,
			})
			startResults <- interleaveStartResult{session: session, err: err}
		}()
	}
	var interleave ExecutionSession
	startSuccesses, startConflicts := 0, 0
	for range 2 {
		result := <-startResults
		if result.err == nil {
			startSuccesses++
			interleave = result.session
		} else if errors.Is(result.err, ErrExecutionConflict) {
			startConflicts++
		} else {
			t.Fatalf("concurrent interleave start error = %v", result.err)
		}
	}
	if interleave.Role != ExecutionRoleWaitingInterleave || interleave.PrimarySessionID == nil || *interleave.PrimarySessionID != primary.ID || interleave.ResumeMarker == nil || *interleave.ResumeMarker != interleaveActivity.ResumeMarker {
		t.Fatalf("start interleave = %+v", interleave)
	}
	if startSuccesses != 1 || startConflicts != 1 {
		t.Fatalf("concurrent interleave starts = %d succeeded, %d conflicted", startSuccesses, startConflicts)
	}
	if _, err := database.StartExecutionSession(ctx, ExecutionSessionInput{ActivityID: interleaveActivity.ID, Role: ExecutionRoleWaitingInterleave, PrimarySessionID: &primary.ID, StartedAt: &interleaveStart}); !errors.Is(err, ErrExecutionConflict) {
		t.Fatalf("second actionable interleave error = %v", err)
	}
	if _, err := database.StartExecutionSession(ctx, ExecutionSessionInput{ActivityID: interleaveActivity.ID, Role: ExecutionRoleWaitingInterleave, PrimarySessionID: &interleave.ID, StartedAt: &interleaveStart}); !errors.Is(err, ErrExecutionConflict) {
		t.Fatalf("interleave nested under an interleave error = %v", err)
	}

	currentPrimary, err := database.CurrentPrimaryExecutionSession(ctx)
	if err != nil || currentPrimary == nil || currentPrimary.ID != primary.ID || currentPrimary.State != executionStateWaitingExternal {
		t.Fatalf("current primary = %+v, %v", currentPrimary, err)
	}
	attended, err := database.CurrentAttendedExecutionSession(ctx)
	if err != nil || attended == nil || attended.ID != interleave.ID || attended.Role != ExecutionRoleWaitingInterleave {
		t.Fatalf("current attended = %+v, %v", attended, err)
	}
	if _, err := database.TransitionExecutionSession(ctx, primary.ID, ExecutionEventInput{EventType: executionEventResumed, OccurredAt: &interleaveStart}); !errors.Is(err, ErrExecutionConflict) {
		t.Fatalf("resume primary while interleave is active error = %v", err)
	}
	if _, err := database.TransitionExecutionSession(ctx, primary.ID, ExecutionEventInput{EventType: executionEventCompleted, OccurredAt: &interleaveStart}); !errors.Is(err, ErrExecutionConflict) {
		t.Fatalf("complete primary while interleave is active error = %v", err)
	}
	readyAt := interleaveStart.Add(10 * time.Minute)
	type readyResult struct {
		session ExecutionSession
		err     error
	}
	readyResults := make(chan readyResult, 2)
	for range 2 {
		go func() {
			session, err := database.TransitionExecutionSession(ctx, primary.ID, ExecutionEventInput{
				EventType:  executionEventExternalReady,
				OccurredAt: &readyAt,
				Note:       "External work is ready",
			})
			readyResults <- readyResult{session: session, err: err}
		}()
	}
	var readyPrimary ExecutionSession
	for range 2 {
		result := <-readyResults
		if result.err != nil {
			t.Fatalf("record external readiness: %v", result.err)
		}
		readyPrimary = result.session
	}
	readyEvents := 0
	for _, event := range readyPrimary.Events {
		if event.EventType == executionEventExternalReady {
			readyEvents++
			if !event.OccurredAt.Equal(readyAt) || event.Note != "External work is ready" {
				t.Fatalf("external readiness event = %+v", event)
			}
		}
	}
	if readyEvents != 1 || readyPrimary.State != executionStateWaitingExternal {
		t.Fatalf("concurrent ready signals appended %d events or changed primary state: %+v", readyEvents, readyPrimary)
	}

	returnedAt := interleaveStart.Add(20 * time.Minute)
	returnInput := ReturnToPrimaryInput{
		OccurredAt:             &returnedAt,
		PrimaryResumeMarker:    stringPointer("Continue after the agent reply"),
		InterleaveResumeMarker: stringPointer("Resume at receipt two"),
	}
	tooEarly = interleaveStart.Add(-time.Second)
	if _, err := database.ReturnToPrimary(ctx, interleave.ID, ReturnToPrimaryInput{OccurredAt: &tooEarly}); !errors.Is(err, ErrInvalidExecutionInput) {
		t.Fatalf("return before interleave start error = %v", err)
	}
	type returnResult struct {
		value ExecutionReturn
		err   error
	}
	returnResults := make(chan returnResult, 2)
	for range 2 {
		go func() {
			value, err := database.ReturnToPrimary(ctx, interleave.ID, returnInput)
			returnResults <- returnResult{value: value, err: err}
		}()
	}
	var returned ExecutionReturn
	for range 2 {
		result := <-returnResults
		if result.err != nil {
			t.Fatalf("concurrent return to primary error = %v", result.err)
		}
		returned = result.value
	}
	if returned.PrimarySession.State != executionStateActive || returned.PrimarySession.ResumeMarker == nil || *returned.PrimarySession.ResumeMarker != "Continue after the agent reply" || returned.InterleaveSession.State != executionStateInterrupted || returned.InterleaveSession.ResumeMarker == nil || *returned.InterleaveSession.ResumeMarker != "Resume at receipt two" {
		t.Fatalf("return to primary = %+v", returned)
	}
	if len(returned.PrimarySession.Events) != 4 || len(returned.InterleaveSession.Events) != 2 || returned.PrimarySession.Events[2].EventType != executionEventExternalReady || returned.PrimarySession.Events[3].EventType != executionEventResumed || returned.InterleaveSession.Events[1].EventType != executionEventSwitchedOut {
		t.Fatalf("return history was not appended once: primary=%+v interleave=%+v", returned.PrimarySession.Events, returned.InterleaveSession.Events)
	}
	var primaryLink, interleaveLink map[string]json.RawMessage
	var linkedInterleaveID, linkedPrimaryID int64
	if json.Unmarshal(returned.PrimarySession.Events[3].Metadata, &primaryLink) != nil || json.Unmarshal(primaryLink["interleave_session_id"], &linkedInterleaveID) != nil || linkedInterleaveID != interleave.ID || json.Unmarshal(returned.InterleaveSession.Events[1].Metadata, &interleaveLink) != nil || json.Unmarshal(interleaveLink["primary_session_id"], &linkedPrimaryID) != nil || linkedPrimaryID != primary.ID {
		t.Fatalf("return event links = primary %s, interleave %s", returned.PrimarySession.Events[3].Metadata, returned.InterleaveSession.Events[1].Metadata)
	}

	metric := DeriveExecutionSummary(startAt, returnedAt, []ExecutionMetricInput{{Session: returned.PrimarySession}})
	if metric.ReturnCount != 1 || metric.ReturnOpportunityCount != 1 || len(metric.ReturnLatenciesSeconds) != 1 || metric.ReturnLatenciesSeconds[0] != int64((returnedAt.Sub(readyAt)).Seconds()) {
		t.Fatalf("primary return timing = %+v", metric)
	}
	interleaves, err := database.ListExecutionInterleaves(ctx, primary.ID, 10)
	if err != nil || len(interleaves) != 1 || interleaves[0].ID != interleave.ID || interleaves[0].State != executionStateInterrupted {
		t.Fatalf("retained interleave list = %+v, %v", interleaves, err)
	}

	secondWaitAt := returnedAt.Add(time.Minute)
	if _, err := database.TransitionExecutionSession(ctx, primary.ID, ExecutionEventInput{EventType: executionEventWaitingExternal, OccurredAt: &secondWaitAt}); err != nil {
		t.Fatalf("second primary wait: %v", err)
	}
	secondStart := secondWaitAt.Add(time.Minute)
	secondInterleave, err := database.StartExecutionSession(ctx, ExecutionSessionInput{
		ActivityID:       interleaveActivity.ID,
		Role:             ExecutionRoleWaitingInterleave,
		PrimarySessionID: &primary.ID,
		StartedAt:        &secondStart,
	})
	if err != nil {
		t.Fatalf("start next interleave after pausing the first: %v", err)
	}
	secondCompletedAt := secondStart.Add(5 * time.Minute)
	secondInterleave, err = database.TransitionExecutionSession(ctx, secondInterleave.ID, ExecutionEventInput{EventType: executionEventCompleted, OccurredAt: &secondCompletedAt})
	if err != nil || secondInterleave.State != executionStateCompleted {
		t.Fatalf("complete second interleave = %+v, %v", secondInterleave, err)
	}
	interleaveHistory, err := database.ListExecutionInterleaves(ctx, primary.ID, 10)
	if err != nil || len(interleaveHistory) != 2 {
		t.Fatalf("interleave history = %+v, %v", interleaveHistory, err)
	}
	completedInterleaveRetained := false
	for _, item := range interleaveHistory {
		if item.ID == secondInterleave.ID && item.State == executionStateCompleted && item.EndedAt != nil && len(item.Events) == 2 {
			completedInterleaveRetained = true
		}
	}
	if !completedInterleaveRetained {
		t.Fatalf("completed interleave missing from retained history: %+v", interleaveHistory)
	}
	stillWaiting, err := database.CurrentPrimaryExecutionSession(ctx)
	if err != nil || stillWaiting == nil || stillWaiting.ID != primary.ID || stillWaiting.State != executionStateWaitingExternal || stillWaiting.EndedAt != nil {
		t.Fatalf("completed interleave changed primary = %+v, %v", stillWaiting, err)
	}
	primaryResumedAt := secondCompletedAt.Add(time.Minute)
	if _, err := database.TransitionExecutionSession(ctx, primary.ID, ExecutionEventInput{EventType: executionEventResumed, OccurredAt: &primaryResumedAt}); err != nil {
		t.Fatalf("resume primary after completed interleave: %v", err)
	}
	primaryCompletedAt := primaryResumedAt.Add(time.Minute)
	if _, err := database.TransitionExecutionSession(ctx, primary.ID, ExecutionEventInput{EventType: executionEventCompleted, OccurredAt: &primaryCompletedAt}); err != nil {
		t.Fatalf("complete primary after returning: %v", err)
	}
	terminalPrimary, err := database.GetExecutionSession(ctx, primary.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeReplayEvents := len(terminalPrimary.Events)
	replayedReturn, err := database.ReturnToPrimary(ctx, interleave.ID, returnInput)
	if err != nil || len(replayedReturn.PrimarySession.Events) != beforeReplayEvents || replayedReturn.PrimarySession.State != executionStateCompleted {
		t.Fatalf("late duplicate return changed terminal primary history: %+v, %v", replayedReturn, err)
	}
	interleaveAbandonedAt := primaryCompletedAt.Add(time.Minute)
	abandonedInterleave, err := database.TransitionExecutionSession(ctx, interleave.ID, ExecutionEventInput{EventType: executionEventAbandoned, OccurredAt: &interleaveAbandonedAt})
	if err != nil || abandonedInterleave.State != executionStateAbandoned {
		t.Fatalf("terminate interrupted interleave after primary completion = %+v, %v", abandonedInterleave, err)
	}
	if _, err := database.StartExecutionSession(ctx, ExecutionSessionInput{ActivityID: interleaveActivity.ID, Role: ExecutionRoleWaitingInterleave, PrimarySessionID: &primary.ID, StartedAt: &returnedAt}); !errors.Is(err, ErrExecutionConflict) {
		t.Fatalf("interleave under terminal primary error = %v", err)
	}
}

func stringPointer(value string) *string { return &value }
