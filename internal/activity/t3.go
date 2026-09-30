package activity

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// CodingThread is one T3 Code thread as seen from its server's records. A
// turn's request time is when the owner sent a message (from any client); the
// time between start and completion is the agent working.
type CodingThread struct {
	Device          string     `json:"device"`
	Environment     string     `json:"environment"`
	ThreadID        string     `json:"thread_id"`
	Title           string     `json:"title"`
	Project         string     `json:"project,omitempty"`
	Workspace       string     `json:"workspace,omitempty"`
	Branch          string     `json:"branch,omitempty"`
	Model           string     `json:"model,omitempty"`
	Provider        string     `json:"provider,omitempty"`
	SessionStatus   string     `json:"session_status,omitempty"`
	WaitingForOwner bool       `json:"waiting_for_owner"`
	RunningNow      bool       `json:"running_now"`
	Archived        bool       `json:"archived"`
	MessagesSent    int        `json:"messages_sent"`
	AgentMinutes    float64    `json:"agent_minutes"`
	FailedTurns     int        `json:"failed_turns"`
	FirstMessageAt  *time.Time `json:"first_message_at,omitempty"`
	LastMessageAt   *time.Time `json:"last_message_at,omitempty"`
	LastActivityAt  time.Time  `json:"last_activity_at"`
}

type t3Thread struct {
	ThreadID      string `json:"thread_id"`
	ProjectID     string `json:"project_id"`
	Title         string `json:"title"`
	Branch        string `json:"branch"`
	Model         string `json:"model"`
	Provider      string `json:"provider_instance"`
	SessionStatus string `json:"session_status"`
	Approvals     int    `json:"pending_approval_count"`
	Inputs        int    `json:"pending_user_input_count"`
	ArchivedAt    string `json:"archived_at"`
	DeletedAt     string `json:"deleted_at"`
	UpdatedAt     string `json:"updated_at"`
}

type t3Turn struct {
	ThreadID    string `json:"thread_id"`
	State       string `json:"state"`
	RequestedAt string `json:"requested_at"`
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at"`
}

// CodingThreads lists threads with messages or agent work in [from, to), or
// that are running or waiting for the owner now, newest activity first.
func CodingThreads(records []Record, from, to time.Time) []CodingThread {
	projects := map[string][2]string{}
	threads := map[string]*CodingThread{}
	raw := map[string]t3Thread{}
	environment := func(source string) string {
		parts := strings.SplitN(source, ":", 3)
		if len(parts) == 3 {
			return parts[2]
		}
		return source
	}
	for _, record := range records {
		switch record.Kind {
		case "t3.project":
			var project struct {
				ID    string `json:"project_id"`
				Title string `json:"title"`
				Root  string `json:"workspace_root"`
			}
			if json.Unmarshal(record.Payload, &project) == nil {
				projects[record.Source+"\x00"+project.ID] = [2]string{project.Title, project.Root}
			}
		case "t3.thread":
			var thread t3Thread
			if json.Unmarshal(record.Payload, &thread) != nil || thread.DeletedAt != "" {
				continue
			}
			key := record.Source + "\x00" + thread.ThreadID
			raw[key] = thread
			updated, _ := time.Parse(time.RFC3339Nano, thread.UpdatedAt)
			threads[key] = &CodingThread{
				Device: record.Device, Environment: environment(record.Source), ThreadID: thread.ThreadID, Title: thread.Title,
				Branch: thread.Branch, Model: thread.Model, Provider: thread.Provider, SessionStatus: thread.SessionStatus,
				WaitingForOwner: thread.Approvals > 0 || thread.Inputs > 0, Archived: thread.ArchivedAt != "", LastActivityAt: updated.UTC(),
			}
		}
	}
	for _, record := range records {
		if record.Kind != "t3.turn" {
			continue
		}
		var turn t3Turn
		if json.Unmarshal(record.Payload, &turn) != nil {
			continue
		}
		thread := threads[record.Source+"\x00"+turn.ThreadID]
		if thread == nil {
			continue
		}
		requested, err := time.Parse(time.RFC3339Nano, turn.RequestedAt)
		if err != nil {
			continue
		}
		if turn.State == "running" {
			thread.RunningNow = true
		}
		if !requested.Before(from) && requested.Before(to) {
			thread.MessagesSent++
			if thread.FirstMessageAt == nil || requested.Before(*thread.FirstMessageAt) {
				at := requested.UTC()
				thread.FirstMessageAt = &at
			}
			if thread.LastMessageAt == nil || requested.After(*thread.LastMessageAt) {
				at := requested.UTC()
				thread.LastMessageAt = &at
			}
			if turn.State == "error" {
				thread.FailedTurns++
			}
		}
		if started, err := time.Parse(time.RFC3339Nano, turn.StartedAt); err == nil {
			end := to
			if completed, err := time.Parse(time.RFC3339Nano, turn.CompletedAt); err == nil {
				end = completed
			} else if turn.State != "running" {
				continue
			}
			start, finish := later(started, from), earlier(end, to)
			if finish.After(start) {
				thread.AgentMinutes += finish.Sub(start).Minutes()
			}
			if finish.After(thread.LastActivityAt) {
				thread.LastActivityAt = finish.UTC()
			}
		}
	}
	result := []CodingThread{}
	for key, thread := range threads {
		if thread.MessagesSent == 0 && thread.AgentMinutes == 0 && !thread.RunningNow && !thread.WaitingForOwner {
			continue
		}
		if project, ok := projects[strings.SplitN(key, "\x00", 2)[0]+"\x00"+raw[key].ProjectID]; ok {
			thread.Project, thread.Workspace = project[0], project[1]
		}
		thread.AgentMinutes = round(thread.AgentMinutes)
		result = append(result, *thread)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].LastActivityAt.After(result[j].LastActivityAt) })
	return result
}
