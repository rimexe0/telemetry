package mcpapi

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rimexe0/telemetry/internal/store"
)

const taskRuleNotes = "Rules say what counts as doing a task, so the system can tell without an AI whether time in a planned block went to it. " +
	"Signals: app is a Mac app name or Android package exactly as activity_summary shows it; domain is a site and its subdomains; " +
	"path_prefix is a project folder, and T3 Code time counts when its thread's project is under that folder. " +
	"A block whose task has rules reports time on the task; without rules it can only report time not spent on distractions. " +
	"When the owner says what they will do today, add the tasks, set the day plan, and give each task rules from what they said and from the names in activity_summary and coding_threads. " +
	"The owner's memory is not reliable: do not ask them to recall what they used; look it up, and ask only when you cannot tell. Then tell them what you set."

type TaskRuleOutput struct {
	ID      int64  `json:"id"`
	TaskID  int64  `json:"task_id"`
	Task    string `json:"task,omitempty"`
	Signal  string `json:"signal"`
	Pattern string `json:"pattern"`
	Note    string `json:"note,omitempty"`
	Removed bool   `json:"removed,omitempty"`
}

type TaskRulesInput struct {
	TaskID *int64 `json:"task_id,omitempty" jsonschema:"only this task's rules; default all tasks"`
}

type TaskRulesOutput struct {
	Rules []TaskRuleOutput `json:"rules"`
	Notes string           `json:"notes"`
}

type TaskRuleChangeInput struct {
	Action  string `json:"action" jsonschema:"add or remove"`
	ID      *int64 `json:"id,omitempty" jsonschema:"rule id, for remove"`
	TaskID  *int64 `json:"task_id,omitempty" jsonschema:"task the rule belongs to; required for add"`
	Signal  string `json:"signal,omitempty" jsonschema:"app, domain or path_prefix"`
	Pattern string `json:"pattern,omitempty" jsonschema:"the app name or package, the site, or the folder"`
	Note    string `json:"note,omitempty"`
}

type ChangeTaskRulesInput struct {
	Changes []TaskRuleChangeInput `json:"changes"`
	Reason  string                `json:"reason" jsonschema:"the owner's words where possible"`
}

type ChangeTaskRulesOutput struct {
	Change ChangeOutput     `json:"change"`
	Rules  []TaskRuleOutput `json:"rules"`
}

func taskTitles(ctx context.Context, repository store.Repository) (map[int64]string, error) {
	activities, err := repository.ListActivities(ctx, store.ActivityFilter{Limit: 500})
	if err != nil {
		return nil, err
	}
	titles := map[int64]string{}
	for _, activity := range activities {
		titles[activity.ID] = activity.Title
	}
	return titles, nil
}

func addRuleTools(server *mcp.Server, repository store.Repository) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "task_rules",
		Description: "What counts as doing each task: its apps, sites and project folders. " + taskRuleNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input TaskRulesInput) (*mcp.CallToolResult, TaskRulesOutput, error) {
		rules, err := repository.ListActivityContextRules(ctx, false)
		if err != nil {
			return nil, TaskRulesOutput{}, err
		}
		titles, err := taskTitles(ctx, repository)
		if err != nil {
			return nil, TaskRulesOutput{}, err
		}
		output := TaskRulesOutput{Rules: []TaskRuleOutput{}, Notes: taskRuleNotes}
		for _, rule := range rules {
			if input.TaskID == nil || *input.TaskID == rule.ActivityID {
				output.Rules = append(output.Rules, TaskRuleOutput{ID: rule.ID, TaskID: rule.ActivityID, Task: titles[rule.ActivityID], Signal: rule.Signal, Pattern: rule.Pattern, Note: rule.Note})
			}
		}
		return nil, output, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "change_task_rules",
		Description: "Add or remove rules for what counts as doing a task. " + taskRuleNotes +
			" The owner describing their task in this conversation is the approval. Every change is logged; recent_changes lists it and revert_change undoes it.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input ChangeTaskRulesInput) (*mcp.CallToolResult, ChangeTaskRulesOutput, error) {
		changes := make([]store.TaskRuleChange, 0, len(input.Changes))
		for _, item := range input.Changes {
			change := store.TaskRuleChange{Action: strings.TrimSpace(item.Action), ID: item.ID, Signal: item.Signal, Pattern: item.Pattern, Note: item.Note}
			if item.TaskID != nil {
				change.TaskID = *item.TaskID
			}
			changes = append(changes, change)
		}
		change, written, err := repository.ApplyTaskRuleChanges(ctx, changes, input.Reason)
		if err != nil {
			return nil, ChangeTaskRulesOutput{}, err
		}
		titles, err := taskTitles(ctx, repository)
		if err != nil {
			return nil, ChangeTaskRulesOutput{}, err
		}
		output := ChangeTaskRulesOutput{Rules: []TaskRuleOutput{}}
		if output.Change, err = changeOutput(change); err != nil {
			return nil, ChangeTaskRulesOutput{}, err
		}
		for _, rule := range written {
			output.Rules = append(output.Rules, TaskRuleOutput{ID: rule.ID, TaskID: rule.ActivityID, Task: titles[rule.ActivityID], Signal: rule.Signal,
				Pattern: rule.Pattern, Note: rule.Note, Removed: rule.DisabledAt != nil})
		}
		return nil, output, nil
	})
}
