package mcpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rimexe0/telemetry/internal/store"
)

const planningNotes = "Tasks (the task pool) are what the owner wants to get done; a day plan is a list of time blocks, each optionally tied to a task. " +
	"Work blocks tied to a task are when distractions are blocked. Times are the owner's local time. " +
	"Draft the plan from the owner's rough description and the task pool, show it, and apply it only after they approve it in the conversation. " +
	"Every applied change is logged; recent_changes lists it and revert_change undoes it."

type TaskOutput struct {
	ID            int64      `json:"id"`
	Title         string     `json:"title"`
	Notes         string     `json:"notes,omitempty"`
	NextAction    string     `json:"next_action,omitempty"`
	ResumeMarker  string     `json:"resume_marker,omitempty"`
	Status        string     `json:"status"`
	ActivityState string     `json:"activity_state"`
	Priority      int        `json:"priority"`
	Deadline      *time.Time `json:"deadline,omitempty"`
	EffortMinutes *int       `json:"effort_estimate_minutes,omitempty"`
	Details       any        `json:"details,omitempty"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

func taskOutput(activity store.Activity) TaskOutput {
	output := TaskOutput{ID: activity.ID, Title: activity.Title, Notes: activity.Notes, NextAction: activity.NextAction, ResumeMarker: activity.ResumeMarker,
		Status: activity.Status, ActivityState: activity.ActivityState, Priority: activity.Priority, Deadline: activity.Deadline,
		EffortMinutes: activity.EffortEstimateMinutes, UpdatedAt: activity.UpdatedAt}
	if len(activity.Details) > 0 {
		_ = json.Unmarshal(activity.Details, &output.Details)
	}
	return output
}

type TaskPoolInput struct {
	All bool `json:"all,omitempty" jsonschema:"include done, completed and abandoned tasks; default only open ones"`
}

type TaskPoolOutput struct {
	Tasks []TaskOutput `json:"tasks"`
	Notes string       `json:"notes"`
}

type TaskChangeInput struct {
	Action        string         `json:"action" jsonschema:"add or update"`
	ID            *int64         `json:"id,omitempty" jsonschema:"task id, required for update"`
	Title         *string        `json:"title,omitempty"`
	Notes         *string        `json:"notes,omitempty"`
	NextAction    *string        `json:"next_action,omitempty" jsonschema:"the concrete next physical step"`
	ResumeMarker  *string        `json:"resume_marker,omitempty" jsonschema:"where to pick up after an interruption"`
	Status        *string        `json:"status,omitempty" jsonschema:"active, waiting, blocked, done, completed, abandoned or unknown"`
	ActivityState *string        `json:"activity_state,omitempty" jsonschema:"growth, maintenance or parked"`
	Priority      *int           `json:"priority,omitempty" jsonschema:"-1000 to 1000; higher is more important"`
	Deadline      *string        `json:"deadline,omitempty" jsonschema:"YYYY-MM-DD in the owner's time zone"`
	ClearDeadline bool           `json:"clear_deadline,omitempty"`
	EffortMinutes *int           `json:"effort_estimate_minutes,omitempty"`
	Details       map[string]any `json:"details,omitempty" jsonschema:"free attributes such as importance, energy, hard_to_stop; replaces earlier details"`
}

type ChangeTasksInput struct {
	Changes []TaskChangeInput `json:"changes"`
	Reason  string            `json:"reason" jsonschema:"the owner's request, in their words where possible"`
}

type ChangeTasksOutput struct {
	Change ChangeOutput `json:"change"`
	Tasks  []TaskOutput `json:"tasks"`
}

type DayPlanInput struct {
	Date string `json:"date,omitempty" jsonschema:"YYYY-MM-DD in the owner's time zone; default today"`
}

type BlockOutput struct {
	ID        int64  `json:"id"`
	TaskID    *int64 `json:"task_id,omitempty"`
	Title     string `json:"title"`
	Start     string `json:"start" jsonschema:"local HH:MM"`
	End       string `json:"end" jsonschema:"local HH:MM"`
	Minutes   int    `json:"minutes"`
	Fixed     bool   `json:"fixed"`
	State     string `json:"state"`
	StateNote string `json:"state_note,omitempty"`
}

type DayPlanOutput struct {
	Date      string        `json:"date"`
	Timezone  string        `json:"timezone"`
	Exists    bool          `json:"exists"`
	VersionID int64         `json:"version_id,omitempty"`
	Note      string        `json:"note,omitempty"`
	Blocks    []BlockOutput `json:"blocks"`
	Notes     string        `json:"notes"`
}

type PlanBlockInput struct {
	Title   string `json:"title,omitempty" jsonschema:"block title; defaults to the task's title"`
	TaskID  *int64 `json:"task_id,omitempty" jsonschema:"task this block works on; makes it a work block"`
	Start   string `json:"start" jsonschema:"local HH:MM on the plan date"`
	Minutes int    `json:"minutes" jsonschema:"1 to 1440"`
	Fixed   bool   `json:"fixed,omitempty" jsonschema:"cannot be moved, such as an appointment"`
}

type SetDayPlanInput struct {
	Date   string           `json:"date,omitempty" jsonschema:"YYYY-MM-DD in the owner's time zone; default today"`
	Blocks []PlanBlockInput `json:"blocks" jsonschema:"the whole day's plan; it replaces the current version (earlier versions are kept)"`
	Note   string           `json:"note,omitempty"`
	Reason string           `json:"reason" jsonschema:"the owner's request, in their words where possible"`
}

type SetDayPlanOutput struct {
	Change ChangeOutput  `json:"change"`
	Plan   DayPlanOutput `json:"plan"`
}

// planDate resolves a local date to the stored plan date (local calendar day
// at UTC midnight) and the owner's location.
func planDate(ctx context.Context, repository store.Repository, value string, now time.Time) (time.Time, *time.Location, error) {
	location, err := ownerLocation(ctx, repository)
	if err != nil {
		return time.Time{}, nil, err
	}
	day := now.In(location)
	if value != "" {
		if day, err = time.ParseInLocation("2006-01-02", value, location); err != nil {
			return time.Time{}, nil, errors.New("date must be YYYY-MM-DD")
		}
	}
	return time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC), location, nil
}

func dayPlanOutput(plan *store.Plan, date time.Time, location *time.Location) DayPlanOutput {
	output := DayPlanOutput{Date: date.Format("2006-01-02"), Timezone: location.String(), Blocks: []BlockOutput{}, Notes: planningNotes}
	if plan == nil {
		return output
	}
	output.Exists, output.VersionID, output.Note = true, plan.VersionID, plan.Note
	for _, block := range plan.Blocks {
		start := block.StartTime.In(location)
		output.Blocks = append(output.Blocks, BlockOutput{ID: block.ID, TaskID: block.ActivityID, Title: block.Title, Start: start.Format("15:04"),
			End: start.Add(time.Duration(block.DurationMinutes) * time.Minute).Format("15:04"), Minutes: block.DurationMinutes,
			Fixed: block.Fixed, State: block.State, StateNote: block.StateNote})
	}
	return output
}

func addPlanningTools(server *mcp.Server, repository store.Repository, now func() time.Time) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "task_pool",
		Description: "The owner's tasks with next actions, priority, deadline, effort and free attributes. " + planningNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input TaskPoolInput) (*mcp.CallToolResult, TaskPoolOutput, error) {
		activities, err := repository.ListActivities(ctx, store.ActivityFilter{Limit: 500})
		if err != nil {
			return nil, TaskPoolOutput{}, err
		}
		output := TaskPoolOutput{Tasks: []TaskOutput{}, Notes: planningNotes}
		for _, activity := range activities {
			closed := activity.Status == "done" || activity.Status == "completed" || activity.Status == "abandoned"
			if input.All || !closed {
				output.Tasks = append(output.Tasks, taskOutput(activity))
			}
		}
		return nil, output, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "change_tasks",
		Description: "Add tasks to the pool or update them (next action, status, priority, deadline, effort, details). " + approvalRule,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input ChangeTasksInput) (*mcp.CallToolResult, ChangeTasksOutput, error) {
		location, err := ownerLocation(ctx, repository)
		if err != nil {
			return nil, ChangeTasksOutput{}, err
		}
		changes := make([]store.TaskChange, 0, len(input.Changes))
		for i, item := range input.Changes {
			change := store.TaskChange{Action: strings.TrimSpace(item.Action), ID: item.ID, Title: item.Title, Notes: item.Notes, NextAction: item.NextAction,
				ResumeMarker: item.ResumeMarker, Status: item.Status, ActivityState: item.ActivityState, Priority: item.Priority,
				ClearDeadline: item.ClearDeadline, EffortEstimateMinutes: item.EffortMinutes}
			if item.Deadline != nil {
				day, err := time.ParseInLocation("2006-01-02", *item.Deadline, location)
				if err != nil {
					return nil, ChangeTasksOutput{}, fmt.Errorf("change %d: deadline must be YYYY-MM-DD", i+1)
				}
				change.Deadline = &day
			}
			if item.Details != nil {
				encoded, err := json.Marshal(item.Details)
				if err != nil {
					return nil, ChangeTasksOutput{}, err
				}
				change.Details = encoded
			}
			changes = append(changes, change)
		}
		change, tasks, err := repository.ApplyTaskChanges(ctx, changes, input.Reason)
		if err != nil {
			return nil, ChangeTasksOutput{}, err
		}
		output := ChangeTasksOutput{Tasks: []TaskOutput{}}
		if output.Change, err = changeOutput(change); err != nil {
			return nil, ChangeTasksOutput{}, err
		}
		for _, task := range tasks {
			output.Tasks = append(output.Tasks, taskOutput(task))
		}
		return nil, output, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "day_plan",
		Description: "The current plan for a day: its blocks in local time, each block's task and state. " + planningNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input DayPlanInput) (*mcp.CallToolResult, DayPlanOutput, error) {
		date, location, err := planDate(ctx, repository, input.Date, now())
		if err != nil {
			return nil, DayPlanOutput{}, err
		}
		plan, err := repository.LatestPlan(ctx, date)
		if err != nil {
			return nil, DayPlanOutput{}, err
		}
		return nil, dayPlanOutput(plan, date, location), nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "set_day_plan",
		Description: "Write a day's whole plan as time blocks; it replaces the current version and earlier versions are kept. " + approvalRule,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input SetDayPlanInput) (*mcp.CallToolResult, SetDayPlanOutput, error) {
		date, location, err := planDate(ctx, repository, input.Date, now())
		if err != nil {
			return nil, SetDayPlanOutput{}, err
		}
		plan := store.PlanInput{Note: input.Note, Blocks: []store.PlanBlockSpec{}}
		for i, block := range input.Blocks {
			clock, err := time.Parse("15:04", block.Start)
			if err != nil {
				return nil, SetDayPlanOutput{}, fmt.Errorf("block %d: start must be HH:MM", i+1)
			}
			start := time.Date(date.Year(), date.Month(), date.Day(), clock.Hour(), clock.Minute(), 0, 0, location)
			plan.Blocks = append(plan.Blocks, store.PlanBlockSpec{ActivityID: block.TaskID, Title: block.Title, StartTime: start.UTC(),
				DurationMinutes: block.Minutes, Fixed: block.Fixed})
		}
		change, saved, err := repository.ApplyDayPlan(ctx, date, plan, input.Reason)
		if err != nil {
			return nil, SetDayPlanOutput{}, err
		}
		output := SetDayPlanOutput{Plan: dayPlanOutput(&saved, date, location)}
		if output.Change, err = changeOutput(change); err != nil {
			return nil, SetDayPlanOutput{}, err
		}
		return nil, output, nil
	})
}
