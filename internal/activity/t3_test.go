package activity

import (
	"encoding/json"
	"testing"
)

func t3Record(kind, payload string) Record {
	return Record{Source: "t3:mac-1:env-1", Device: "mac-1", Kind: kind, Start: at(0), Payload: json.RawMessage(payload)}
}

func TestCodingThreadsCountMessagesAgentTimeAndWaiting(t *testing.T) {
	records := []Record{
		t3Record("t3.project", `{"project_id":"p1","title":"Telemetry","workspace_root":"/code/telemetry"}`),
		t3Record("t3.thread", `{"thread_id":"a","project_id":"p1","title":"Fix collector","updated_at":"2026-09-30T10:40:00Z","pending_approval_count":1,"model":"claude-opus-5-5"}`),
		t3Record("t3.thread", `{"thread_id":"old","project_id":"p1","title":"Old work","updated_at":"2026-09-01T10:00:00Z"}`),
		t3Record("t3.turn", `{"thread_id":"a","state":"completed","requested_at":"2026-09-30T10:00:00Z","started_at":"2026-09-30T10:00:00Z","completed_at":"2026-09-30T10:20:00Z"}`),
		t3Record("t3.turn", `{"thread_id":"a","state":"running","requested_at":"2026-09-30T10:50:00Z","started_at":"2026-09-30T10:50:00Z"}`),
		t3Record("t3.turn", `{"thread_id":"old","state":"completed","requested_at":"2026-09-01T10:00:00Z","started_at":"2026-09-01T10:00:00Z","completed_at":"2026-09-01T10:05:00Z"}`),
	}
	threads := CodingThreads(records, at(0), at(60))
	if len(threads) != 1 {
		t.Fatalf("only the active thread belongs in the window: %+v", threads)
	}
	thread := threads[0]
	if thread.Project != "Telemetry" || thread.MessagesSent != 2 || thread.AgentMinutes != 30 || !thread.RunningNow || !thread.WaitingForOwner || thread.Environment != "env-1" {
		t.Fatalf("thread = %+v", thread)
	}
}
