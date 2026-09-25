package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPostgresAgentReadyKeepsPrimaryWaitingAndIsRecordedOnce(t *testing.T) {
	databaseURL := os.Getenv("TELEMETRY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TELEMETRY_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	database, err := OpenPostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
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
	work, err := database.CreateActivity(ctx, Activity{Title: "Client work"})
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := database.CreateActivity(ctx, Activity{Title: "Inbox"})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	primary, err := database.StartExecutionSession(ctx, ExecutionSessionInput{ActivityID: work.ID, StartedAt: ptr(base)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.TransitionExecutionSession(ctx, primary.ID, ExecutionEventInput{EventType: executionEventWaitingExternal, OccurredAt: ptr(base.Add(time.Minute))}); err != nil {
		t.Fatal(err)
	}
	interleave, err := database.StartExecutionSession(ctx, ExecutionSessionInput{ActivityID: inbox.ID, Role: ExecutionRoleWaitingInterleave, PrimarySessionID: &primary.ID, StartedAt: ptr(base.Add(2 * time.Minute))})
	if err != nil {
		t.Fatal(err)
	}
	candidate := AgentReadyCandidate{Lifecycle: AgentLifecycle{Tool: "t3code", SessionID: "t1", RunID: "u1", Status: AgentCompleted, At: base.Add(5 * time.Minute), SourceInstanceKey: "t3code:mac:state", SourceRecordKey: "turn:t1:id:u1"}}
	for range 2 {
		if _, err := database.RecordAgentReady(ctx, primary.ID, candidate); err != nil {
			t.Fatalf("record ready: %v", err)
		}
	}
	current, err := database.GetExecutionSession(ctx, primary.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.State != executionStateWaitingExternal {
		t.Fatalf("primary state = %s", current.State)
	}
	ready := 0
	for _, event := range current.Events {
		if event.EventType == executionEventExternalReady {
			ready++
			if !strings.Contains(string(event.Provenance), `"agent_lifecycle"`) || !strings.Contains(string(event.Metadata), `"turn:t1:id:u1"`) {
				t.Fatalf("event provenance/metadata = %s %s", event.Provenance, event.Metadata)
			}
		}
	}
	if ready != 1 {
		t.Fatalf("external_ready events = %d, want 1", ready)
	}
	if attended, err := database.CurrentAttendedExecutionSession(ctx); err != nil || attended == nil || attended.ID != interleave.ID {
		t.Fatalf("attended = %+v, %v", attended, err)
	}
	if _, err := database.ReturnToPrimary(ctx, interleave.ID, ReturnToPrimaryInput{OccurredAt: ptr(base.Add(8 * time.Minute))}); err != nil {
		t.Fatalf("return: %v", err)
	}
	if _, err := database.RecordAgentReady(ctx, primary.ID, AgentReadyCandidate{Lifecycle: AgentLifecycle{Tool: "t3code", SessionID: "t1", RunID: "u2", Status: AgentCompleted, At: base.Add(9 * time.Minute)}}); !errors.Is(err, ErrInvalidExecutionTransition) {
		t.Fatalf("ready on an active primary err = %v", err)
	}
}
