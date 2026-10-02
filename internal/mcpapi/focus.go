package mcpapi

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rimexe0/telemetry/internal/store"
)

const focusNotes = "A focus session is a stretch in which the owner does one task only; distractions are closed while it runs and anything that is not part of the task counts as leaving it. " +
	"Other tasks need no session: the owner multitasks on those, and that is fine. The owner starts a focus session themselves or agrees a time with you. " +
	"Ending a session does not mark the task done; only the owner says when a task is done. An agent finishing its turn means it is the owner's turn, not that the task is done."

const memoryNotes = "Each task remembers what the owner said is and is not part of it: an app or a site, and for a coding agent its project. " +
	"The memory belongs to this owner and this task and must stay flexible: it is evidence, not a rule, the newest answer wins, and the owner may do a task in a new and unexpected way. " +
	"Never treat something unfamiliar as drift on your own; when you cannot tell whether it belongs to the task, say unsure so the owner is asked."

type FocusInput struct {
	Action string `json:"action,omitempty" jsonschema:"start, pause, resume or done; leave out to read the open session"`
	TaskID int64  `json:"task_id,omitempty" jsonschema:"task to focus on; required for start"`
}

type FocusOutput struct {
	Open   bool       `json:"open"`
	TaskID int64      `json:"task_id,omitempty"`
	Task   string     `json:"task,omitempty"`
	State  string     `json:"state,omitempty" jsonschema:"active or interrupted (paused)"`
	Since  *time.Time `json:"since,omitempty"`
	Notes  string     `json:"notes"`
}

type TaskMemoryInput struct {
	TaskID *int64 `json:"task_id,omitempty" jsonschema:"only this task; default all tasks"`
}

type MemoryEntry struct {
	TaskID  int64     `json:"task_id"`
	Task    string    `json:"task,omitempty"`
	Context string    `json:"context" jsonschema:"what was on screen: an app or site, with its project for a coding agent"`
	Verdict string    `json:"verdict" jsonschema:"related, unrelated or unsure"`
	Source  string    `json:"source" jsonschema:"owner or judge"`
	Reason  string    `json:"reason,omitempty"`
	At      time.Time `json:"at"`
}

type TaskMemoryOutput struct {
	Entries []MemoryEntry `json:"entries"`
	Notes   string        `json:"notes"`
}

type RememberInput struct {
	TaskID  int64  `json:"task_id"`
	Context string `json:"context" jsonschema:"exactly as tracking or the day shows it, such as trendyol.com or T3 Code (Nightly) · Telemetry"`
	Verdict string `json:"verdict" jsonschema:"related or unrelated"`
	Reason  string `json:"reason" jsonschema:"the owner's words"`
}

type RememberOutput struct {
	Entry MemoryEntry `json:"entry"`
}

func focusOutput(ctx context.Context, repository store.Repository, session *store.ExecutionSession) (FocusOutput, error) {
	output := FocusOutput{Notes: focusNotes}
	if session == nil || session.EndedAt != nil {
		return output, nil
	}
	titles, err := taskTitles(ctx, repository)
	if err != nil {
		return output, err
	}
	since := session.StartedAt
	output.Open, output.TaskID, output.Task, output.State, output.Since = true, session.ActivityID, titles[session.ActivityID], session.State, &since
	return output, nil
}

func addFocusTools(server *mcp.Server, repository store.Repository) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "focus_session",
		Description: "Start, pause, resume or end the owner's focus session, or call it with no action to read the one that is open. " + focusNotes + " The owner asking for it in this conversation is the approval.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input FocusInput) (*mcp.CallToolResult, FocusOutput, error) {
		action := strings.TrimSpace(input.Action)
		var session *store.ExecutionSession
		var err error
		if action == "" {
			session, err = repository.CurrentPrimaryExecutionSession(ctx)
		} else {
			if action == store.FocusStart && input.TaskID <= 0 {
				return nil, FocusOutput{}, errors.New("start needs a task_id")
			}
			session, err = store.ChangeFocus(ctx, repository, action, input.TaskID)
		}
		if err != nil {
			return nil, FocusOutput{}, err
		}
		output, err := focusOutput(ctx, repository, session)
		return nil, output, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "task_memory",
		Description: "What each task has learned about how the owner does it. " + memoryNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input TaskMemoryInput) (*mcp.CallToolResult, TaskMemoryOutput, error) {
		evidence, err := repository.ListTaskEvidence(ctx)
		if err != nil {
			return nil, TaskMemoryOutput{}, err
		}
		titles, err := taskTitles(ctx, repository)
		if err != nil {
			return nil, TaskMemoryOutput{}, err
		}
		output := TaskMemoryOutput{Entries: []MemoryEntry{}, Notes: memoryNotes}
		for _, item := range evidence {
			if input.TaskID == nil || *input.TaskID == item.TaskID {
				output.Entries = append(output.Entries, MemoryEntry{TaskID: item.TaskID, Task: titles[item.TaskID], Context: item.Context, Verdict: item.Verdict,
					Source: item.Source, Reason: item.Reason, At: item.CreatedAt})
			}
		}
		return nil, output, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "remember_for_task",
		Description: "Record that something is or is not part of a task, when the owner tells you so. It replaces the task's earlier entry for the same thing; the old one is kept as history. " +
			memoryNotes + " The owner saying it in this conversation is the approval.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input RememberInput) (*mcp.CallToolResult, RememberOutput, error) {
		if input.Verdict != store.EvidenceRelated && input.Verdict != store.EvidenceUnrelated {
			return nil, RememberOutput{}, errors.New("verdict must be related or unrelated")
		}
		saved, err := repository.AddTaskEvidence(ctx, store.TaskEvidence{TaskID: input.TaskID, Context: input.Context, Verdict: input.Verdict,
			Source: store.EvidenceOwner, Reason: input.Reason})
		if err != nil {
			return nil, RememberOutput{}, err
		}
		titles, err := taskTitles(ctx, repository)
		if err != nil {
			return nil, RememberOutput{}, err
		}
		return nil, RememberOutput{Entry: MemoryEntry{TaskID: saved.TaskID, Task: titles[saved.TaskID], Context: saved.Context, Verdict: saved.Verdict,
			Source: saved.Source, Reason: saved.Reason, At: saved.CreatedAt}}, nil
	})
}
