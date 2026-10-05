package mcpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rimexe0/telemetry/internal/activity"
	"github.com/rimexe0/telemetry/internal/store"
)

const planningNotes = "Tasks (the task pool) are what the owner wants to get done; a day plan is a list of time blocks, each optionally tied to a task. " +
	"A task needs no time block: its time is counted whenever the owner's activity matches its rules (task_rules), so on a day with no fixed times just add the tasks and their rules. " +
	"Use blocks only for things that have a real time. " +
	"How to help this owner: the system stands in for executive function that does not work reliably, so carry the remembering, timing and sequencing yourself. " +
	"Keep a day to one to three things; on a bad day make the plan smaller instead of holding them to it. Give the next smallest concrete step, not the importance of the task. " +
	"State facts and the way back; never scold, and never ask them to recall what the records already show. A broken plan continues from where it broke. " +
	"The system also writes to the owner in your name without being asked: a morning message when the records show they woke, a check-in when they have drifted away from the day's tasks for a while, and a note when collection breaks. " +
	"You did not see those being sent, so when the owner's message reads like an answer to something you did not say, read outreach_log first. " +
	"Never ask when their morning starts; the records show when they wake. " +
	"When the day is locked until planned (blocking_schedule), agreeing the day with them and calling set_day_plan is what opens it; an empty list of blocks is a valid plan for a day with no fixed times. " +
	"Distractions are closed during blocks tied to a task and during focus blocks. Times are the owner's local time. " +
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
	Closes    bool   `json:"closes_distractions" jsonschema:"distractions are closed during this block"`
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

type PlanReviewInput struct {
	Date  string `json:"date,omitempty" jsonschema:"YYYY-MM-DD in the owner's time zone; default today"`
	Limit int    `json:"limit,omitempty" jsonschema:"apps and sites listed per device and block, 1 through 20; default 5"`
}

type PlanReviewOutput struct {
	Date       string                 `json:"date"`
	Timezone   string                 `json:"timezone"`
	From       time.Time              `json:"from"`
	To         time.Time              `json:"to"`
	PlanExists bool                   `json:"plan_exists"`
	Blocks     []activity.BlockReview `json:"blocks"`
	Outside    activity.Observed      `json:"outside_blocks"`
	Insights   []activity.Insight     `json:"insights"`
	Tasks      []activity.DayTask     `json:"tasks" jsonschema:"the open tasks with the time recognised as each today, by rules or by the owner's own answer; tracked is false for a task with neither"`
	Notes      string                 `json:"notes"`
}

const planReviewNotes = "This puts recorded activity next to each planned block; it does not judge whether the plan was followed. " +
	"tasks lists each open task: minutes is the owner's own attention on it, agent_minutes its coding agent's runtime, waiting_minutes how long the agent sat stopped until the owner came back, " +
	"returns how often they came back to it after being elsewhere, and your_turn_since is set while its agent has stopped and waits for them. An agent stopping is not the task being done. " +
	"Coming back is the thing that matters: report returns and waiting as facts. A task that is not tracked has no rules or memory yet; read the apps, sites and coding threads against its title yourself and give it rules. " +
	"distraction_minutes is active time on things the owner labelled \"distraction\". no_data_minutes is missing data, never rest or focus. " +
	"Agent minutes are agent runtime, not the owner's attention. Blocks are observed up to now; upcoming blocks have nothing observed."

type PlanBlockInput struct {
	Title   string `json:"title,omitempty" jsonschema:"block title; defaults to the task's title"`
	TaskID  *int64 `json:"task_id,omitempty" jsonschema:"task this block works on; makes it a work block"`
	Start   string `json:"start" jsonschema:"local HH:MM on the plan date"`
	Minutes int    `json:"minutes" jsonschema:"1 to 1440"`
	Fixed   bool   `json:"fixed,omitempty" jsonschema:"cannot be moved, such as an appointment"`
	Focus   bool   `json:"focus,omitempty" jsonschema:"close distractions during this block even though it has no task"`
}

type BlockNowInput struct {
	Minutes int    `json:"minutes" jsonschema:"how long from now, 1 to 1440"`
	TaskID  *int64 `json:"task_id,omitempty" jsonschema:"task to work on; optional"`
	Title   string `json:"title,omitempty" jsonschema:"what the owner is doing; defaults to the task's title, or Focus"`
	Reason  string `json:"reason" jsonschema:"the owner's request, in their words where possible"`
}

type BlockNowOutput struct {
	Change ChangeOutput  `json:"change"`
	Block  BlockOutput   `json:"block"`
	Plan   DayPlanOutput `json:"plan"`
	Notes  string        `json:"notes"`
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
			Fixed: block.Fixed, Closes: block.ActivityID != nil || block.Focus, State: block.State, StateNote: block.StateNote})
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
		Name: "change_tasks",
		Description: "Add tasks to the pool or update them (next action, status, priority, deadline, effort, details). " +
			"Right after adding a task, give it rules with change_task_rules so its time can be recognised: look up the real app, site and project names in activity_summary and coding_threads instead of asking the owner. " + approvalRule,
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
		Name: "plan_review",
		Description: "The day's plan against what was recorded: for each planned block the active minutes per device, top apps and sites, minutes on distractions, coding threads and missing data, " +
			"plus the time outside any block and a few plain facts about the day. " + planReviewNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input PlanReviewInput) (*mcp.CallToolResult, PlanReviewOutput, error) {
		limit := input.Limit
		if limit == 0 {
			limit = 5
		}
		if limit < 1 || limit > 20 {
			return nil, PlanReviewOutput{}, errors.New("limit must be between 1 and 20")
		}
		from, to, location, err := activityRange(ctx, repository, ActivityRangeInput{Date: input.Date}, now())
		if err != nil {
			return nil, PlanReviewOutput{}, err
		}
		date, _, err := planDate(ctx, repository, input.Date, now())
		if err != nil {
			return nil, PlanReviewOutput{}, err
		}
		plan, err := repository.LatestPlan(ctx, date)
		if err != nil {
			return nil, PlanReviewOutput{}, err
		}
		records, _, err := repository.ActivityRecords(ctx, from, to)
		if err != nil {
			return nil, PlanReviewOutput{}, err
		}
		coding, err := repository.CodingRecords(ctx, from)
		if err != nil {
			return nil, PlanReviewOutput{}, err
		}
		stored, err := repository.ListActivityLabels(ctx)
		if err != nil {
			return nil, PlanReviewOutput{}, err
		}
		labels := store.LabelsForSummary(stored)
		result := activity.Build(records, from, to)
		review := activity.ReviewPlan(store.PlannedBlocks(plan), result, coding, labels, from, to, limit)
		day, _, err := store.LoadDayInput(ctx, repository, date, from, to, false)
		if err != nil {
			return nil, PlanReviewOutput{}, err
		}
		return nil, PlanReviewOutput{Date: date.Format("2006-01-02"), Timezone: location.String(), From: from, To: to, PlanExists: plan != nil,
			Blocks: review.Blocks, Outside: review.Outside, Insights: activity.Insights(result, activity.CodingThreads(coding, from, to), labels),
			Tasks: activity.BuildDay(day).Tasks, Notes: planReviewNotes}, nil
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
				DurationMinutes: block.Minutes, Fixed: block.Fixed, Focus: block.Focus})
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
	mcp.AddTool(server, &mcp.Tool{
		Name: "block_now",
		Description: "Close the owner's distractions from now for a number of minutes, by adding one block to today's plan; the rest of the plan stays as it is. " +
			"A task is optional. It refuses to overlap an unfinished planned block. Devices follow within about a minute. " +
			"To end it early, revert_change with the returned change id. " +
			"The owner asking for it in this conversation (\"block for 30 minutes\") is the approval; it is logged like every change, and recent_changes lists it.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input BlockNowInput) (*mcp.CallToolResult, BlockNowOutput, error) {
		start := now().UTC().Truncate(time.Minute)
		date, location, err := planDate(ctx, repository, "", start)
		if err != nil {
			return nil, BlockNowOutput{}, err
		}
		title := strings.TrimSpace(input.Title)
		if title == "" && input.TaskID == nil {
			title = "Focus"
		}
		change, plan, err := repository.AddPlanBlock(ctx, date, store.PlanBlockSpec{ActivityID: input.TaskID, Title: title, StartTime: start,
			DurationMinutes: input.Minutes, Focus: true}, input.Reason)
		var conflict *store.PlanConflictError
		if errors.As(err, &conflict) {
			return nil, BlockNowOutput{}, fmt.Errorf("not added: it would overlap the planned block %q (%s to %s). Change that block with set_day_plan, or choose fewer minutes",
				conflict.Title, conflict.Start.In(location).Format("15:04"), conflict.End.In(location).Format("15:04"))
		}
		if err != nil {
			return nil, BlockNowOutput{}, err
		}
		output := BlockNowOutput{Plan: dayPlanOutput(&plan, date, location), Notes: "Devices fetch the plan about once a minute, so blocking starts within about a minute."}
		for _, block := range output.Plan.Blocks {
			if block.Start == start.In(location).Format("15:04") && block.Minutes == input.Minutes {
				output.Block = block
			}
		}
		if output.Change, err = changeOutput(change); err != nil {
			return nil, BlockNowOutput{}, err
		}
		return nil, output, nil
	})
}
