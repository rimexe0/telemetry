package activity

import (
	"testing"
	"time"
)

func TestT3TimeFollowsTheProjectTheClientShows(t *testing.T) {
	at := func(hour, minute int) time.Time { return time.Date(2026, 10, 2, hour, minute, 0, 0, time.UTC) }
	end := func(hour, minute int) *time.Time { value := at(hour, minute); return &value }
	telemetry, acme := int64(1), int64(5)
	coding := []Record{
		{Source: "t3:mac:env", Kind: "t3.project", Payload: []byte(`{"project_id":"p","title":"Telemetry","workspace_root":"/Users/ri/code/personal/Telemetry"}`)},
		{Source: "t3:mac:env", Kind: "t3.thread", Payload: []byte(`{"thread_id":"a","project_id":"p","title":"Clarify next steps"}`)},
		{Source: "t3:fedora:env2", Kind: "t3.project", Payload: []byte(`{"project_id":"x","title":"acme","workspace_root":"/home/rime/code/work/acme"}`)},
		{Source: "t3:fedora:env2", Kind: "t3.thread", Payload: []byte(`{"thread_id":"e","project_id":"x","title":"Login screen","worktree_path":"/home/rime/.t3/worktrees/acme/login"}`)},
		// Many messages in the Telemetry thread: guessing from messages alone gives it everything.
		{Source: "t3:mac:env", Kind: "t3.turn", Payload: []byte(`{"thread_id":"a","requested_at":"2026-10-02T14:00:00Z","state":"completed","completed_at":"2026-10-02T14:01:00Z"}`)},
		{Source: "t3:mac:env", Kind: "t3.turn", Payload: []byte(`{"thread_id":"a","requested_at":"2026-10-02T14:20:00Z","state":"completed","completed_at":"2026-10-02T14:21:00Z"}`)},
		// The Mac's client shows Telemetry (from the Mac's server), then an
		// Acme worktree (from Fedora's server), without sending anything.
		{Source: "t3:mac:env", Kind: "t3.open", Start: at(14, 0), End: end(14, 10), Payload: []byte(`{"client_kind":"desktop-renderer","cwd":"/Users/ri/code/personal/Telemetry"}`)},
		{Source: "t3:fedora:env2", Kind: "t3.open", Start: at(14, 10), End: end(14, 25), Payload: []byte(`{"client_kind":"desktop-renderer","cwd":"/home/rime/.t3/worktrees/acme/login"}`)},
		// The phone's client is somewhere else and must not count for the Mac.
		{Source: "t3:mac:env", Kind: "t3.open", Start: at(14, 0), End: end(14, 30), Payload: []byte(`{"client_kind":"mobile","cwd":"/Users/ri/code/personal/Telemetry"}`)},
	}
	tasks := []TaskInfo{
		{ID: telemetry, Title: "Telemetry", Rules: []TaskRule{{Signal: "path_prefix", Pattern: "/Users/ri/code/personal/Telemetry"}}},
		{ID: acme, Title: "Acme", Rules: []TaskRule{{Signal: "path_prefix", Pattern: "/home/rime/code/work/acme"}, {Signal: "path_prefix", Pattern: "/home/rime/.t3/worktrees/acme"}}},
	}
	result := Result{LastRecord: map[string]time.Time{}, Segments: []Segment{
		// One unbroken window: the title never changes.
		{Device: DeviceMac, App: "T3 Code (Nightly)", Title: "T3 Code (Nightly)", Start: at(14, 0), End: at(14, 25)},
	}}
	input := DayInput{Result: result, Messages: ThreadMessages(coding), Turns: AgentTurns(coding), Open: OpenSpans(coding), Tasks: tasks, From: at(0, 0), To: at(14, 25)}
	day := BuildDay(input)
	if day.Tasks[0].Minutes != 10 || day.Tasks[1].Minutes != 15 {
		t.Fatalf("tasks = %+v", day.Tasks)
	}
	details := map[string]float64{}
	for _, chunk := range day.Chunks {
		for _, item := range chunk.Items {
			details[item.Detail] += item.Minutes
		}
	}
	if details["Telemetry · Clarify next steps"] != 10 || details["acme"] != 15 {
		t.Fatalf("details = %v", details)
	}
	if now := Track(input); now.Task != "Acme" || now.Context != "T3 Code (Nightly) · acme" {
		t.Fatalf("now = %+v", now)
	}
	// Without the client's report, the same window is guessed from messages.
	input.Open = nil
	if guessed := BuildDay(input); guessed.Tasks[0].Minutes != 25 || guessed.Tasks[1].Minutes != 0 {
		t.Fatalf("guessed = %+v", guessed.Tasks)
	}
}
