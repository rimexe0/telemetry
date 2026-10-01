package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	AgentChangeTasks = "tasks"
	AgentChangePlan  = "plan"
)

// TaskChange adds a task to the pool or updates fields of an existing one.
// Fields left out stay as they are.
type TaskChange struct {
	Action                string          `json:"action"`
	ID                    *int64          `json:"id,omitempty"`
	Title                 *string         `json:"title,omitempty"`
	Notes                 *string         `json:"notes,omitempty"`
	NextAction            *string         `json:"next_action,omitempty"`
	ResumeMarker          *string         `json:"resume_marker,omitempty"`
	Status                *string         `json:"status,omitempty"`
	ActivityState         *string         `json:"activity_state,omitempty"`
	Priority              *int            `json:"priority,omitempty"`
	Deadline              *time.Time      `json:"deadline,omitempty"`
	ClearDeadline         bool            `json:"clear_deadline,omitempty"`
	EffortEstimateMinutes *int            `json:"effort_estimate_minutes,omitempty"`
	Details               json.RawMessage `json:"details,omitempty"`
}

// taskBefore is a task as it was before a change; Activity is nil for a task
// the change created.
type taskBefore struct {
	ID       int64     `json:"id"`
	Activity *Activity `json:"activity"`
}

// planBefore is the plan for a date before a change; VersionID 0 means the
// date had no plan.
type planBefore struct {
	PlanDate  string          `json:"plan_date"`
	VersionID int64           `json:"version_id"`
	Note      string          `json:"note"`
	Blocks    []ProposedBlock `json:"blocks"`
}

const activityColumns = `id, title, notes, next_action, resume_marker, status, activity_state, priority, deadline, effort_estimate_minutes, details, created_at, updated_at`

func readActivityTx(ctx context.Context, tx pgx.Tx, id int64) (Activity, error) {
	var activity Activity
	var details []byte
	err := tx.QueryRow(ctx, `SELECT `+activityColumns+` FROM activities WHERE id = $1 FOR UPDATE`, id).Scan(
		&activity.ID, &activity.Title, &activity.Notes, &activity.NextAction, &activity.ResumeMarker, &activity.Status, &activity.ActivityState,
		&activity.Priority, &activity.Deadline, &activity.EffortEstimateMinutes, &details, &activity.CreatedAt, &activity.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return activity, ErrNotFound
	}
	if len(details) > 0 && string(details) != "null" {
		activity.Details = details
	}
	return activity, err
}

func writeActivityTx(ctx context.Context, tx pgx.Tx, activity Activity, create bool) (Activity, error) {
	if err := validateActivity(&activity); err != nil {
		return Activity{}, fmt.Errorf("%w: %v", ErrInvalidAgentChange, err)
	}
	if create {
		err := tx.QueryRow(ctx, `
			INSERT INTO activities (title, notes, next_action, resume_marker, status, activity_state, priority, deadline, effort_estimate_minutes, details)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id, created_at, updated_at`,
			activity.Title, activity.Notes, activity.NextAction, activity.ResumeMarker, activity.Status, activity.ActivityState,
			activity.Priority, activity.Deadline, activity.EffortEstimateMinutes, nonEmptyJSON(activity.Details)).Scan(&activity.ID, &activity.CreatedAt, &activity.UpdatedAt)
		return activity, err
	}
	err := tx.QueryRow(ctx, `
		UPDATE activities SET title=$2, notes=$3, next_action=$4, resume_marker=$5, status=$6, activity_state=$7, priority=$8,
			deadline=$9, effort_estimate_minutes=$10, details=$11, updated_at=now()
		WHERE id=$1 RETURNING created_at, updated_at`,
		activity.ID, activity.Title, activity.Notes, activity.NextAction, activity.ResumeMarker, activity.Status, activity.ActivityState,
		activity.Priority, activity.Deadline, activity.EffortEstimateMinutes, nonEmptyJSON(activity.Details)).Scan(&activity.CreatedAt, &activity.UpdatedAt)
	return activity, err
}

func applyTaskChange(activity *Activity, change TaskChange) {
	set := func(target *string, value *string) {
		if value != nil {
			*target = *value
		}
	}
	set(&activity.Title, change.Title)
	set(&activity.Notes, change.Notes)
	set(&activity.NextAction, change.NextAction)
	set(&activity.ResumeMarker, change.ResumeMarker)
	set(&activity.Status, change.Status)
	set(&activity.ActivityState, change.ActivityState)
	if change.Priority != nil {
		activity.Priority = *change.Priority
	}
	if change.Deadline != nil {
		deadline := change.Deadline.UTC()
		activity.Deadline = &deadline
	}
	if change.ClearDeadline {
		activity.Deadline = nil
	}
	if change.EffortEstimateMinutes != nil {
		activity.EffortEstimateMinutes = change.EffortEstimateMinutes
	}
	if len(change.Details) > 0 {
		activity.Details = change.Details
	}
}

// ApplyTaskChanges adds or updates tasks in the pool at once. It returns the
// logged change; the tasks it wrote are listed in its changes.
func (p *Postgres) ApplyTaskChanges(ctx context.Context, changes []TaskChange, reason string) (AgentChange, []Activity, error) {
	reason, err := validReason(reason)
	if err != nil {
		return AgentChange{}, nil, err
	}
	if len(changes) == 0 || len(changes) > 50 {
		return AgentChange{}, nil, fmt.Errorf("%w: give 1 to 50 changes", ErrInvalidAgentChange)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return AgentChange{}, nil, err
	}
	defer tx.Rollback(ctx)
	previous := []taskBefore{}
	written := []Activity{}
	for i, change := range changes {
		switch change.Action {
		case "add":
			if change.ID != nil {
				return AgentChange{}, nil, fmt.Errorf("%w: change %d adds a task, so it takes no id", ErrInvalidAgentChange, i+1)
			}
			activity := Activity{}
			applyTaskChange(&activity, change)
			created, err := writeActivityTx(ctx, tx, activity, true)
			if err != nil {
				return AgentChange{}, nil, fmt.Errorf("change %d: %w", i+1, err)
			}
			previous = append(previous, taskBefore{ID: created.ID})
			written = append(written, created)
		case "update":
			if change.ID == nil {
				return AgentChange{}, nil, fmt.Errorf("%w: change %d updates a task, so it needs its id", ErrInvalidAgentChange, i+1)
			}
			before, err := readActivityTx(ctx, tx, *change.ID)
			if err != nil {
				return AgentChange{}, nil, fmt.Errorf("change %d: task %d: %w", i+1, *change.ID, err)
			}
			after := before
			applyTaskChange(&after, change)
			updated, err := writeActivityTx(ctx, tx, after, false)
			if err != nil {
				return AgentChange{}, nil, fmt.Errorf("change %d: %w", i+1, err)
			}
			previous = append(previous, taskBefore{ID: before.ID, Activity: &before})
			written = append(written, updated)
		default:
			return AgentChange{}, nil, fmt.Errorf(`%w: change %d action must be "add" or "update"`, ErrInvalidAgentChange, i+1)
		}
	}
	change, err := insertAgentChange(ctx, tx, AgentChangeTasks, written, previous, reason, nil, nil)
	if err != nil {
		return AgentChange{}, nil, err
	}
	return change, written, tx.Commit(ctx)
}

// latestPlanTx reads the plan in force for a date inside tx.
func (p *Postgres) latestPlanTx(ctx context.Context, tx pgx.Tx, planDate time.Time) (planBefore, error) {
	before := planBefore{PlanDate: planDate.Format("2006-01-02"), Blocks: []ProposedBlock{}}
	err := tx.QueryRow(ctx, `SELECT id, note FROM plan_versions WHERE plan_date = $1 ORDER BY created_at DESC, id DESC LIMIT 1`, planDate).
		Scan(&before.VersionID, &before.Note)
	if errors.Is(err, pgx.ErrNoRows) {
		return before, nil
	}
	if err != nil {
		return before, err
	}
	blocks, err := p.planBlocks(ctx, tx, before.VersionID)
	if err != nil {
		return before, err
	}
	for _, block := range blocks {
		before.Blocks = append(before.Blocks, ProposedBlock{ActivityID: block.ActivityID, SourceBlockID: block.SourceBlockID, Title: block.Title,
			StartTime: block.StartTime, DurationMinutes: block.DurationMinutes, Fixed: block.Fixed, Focus: block.Focus, State: block.State})
	}
	return before, nil
}

// ApplyDayPlan saves a new plan version for the date at once. Earlier
// versions stay; the logged change keeps the version it replaced.
func (p *Postgres) ApplyDayPlan(ctx context.Context, planDate time.Time, input PlanInput, reason string) (AgentChange, Plan, error) {
	reason, err := validReason(reason)
	if err != nil {
		return AgentChange{}, Plan{}, err
	}
	blocks, err := p.planBlocksFromInput(ctx, input)
	if err != nil {
		return AgentChange{}, Plan{}, fmt.Errorf("%w: %v", ErrInvalidAgentChange, err)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return AgentChange{}, Plan{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockPlanDate(ctx, tx, planDate); err != nil {
		return AgentChange{}, Plan{}, err
	}
	before, err := p.latestPlanTx(ctx, tx, planDate)
	if err != nil {
		return AgentChange{}, Plan{}, err
	}
	plan, err := insertPlanVersion(ctx, tx, planDate, strings.TrimSpace(input.Note), blocks)
	if err != nil {
		return AgentChange{}, Plan{}, err
	}
	change, err := insertAgentChange(ctx, tx, AgentChangePlan, plan, before, reason, nil, nil)
	if err != nil {
		return AgentChange{}, Plan{}, err
	}
	return change, plan, tx.Commit(ctx)
}

// PlanConflictError reports the planned block a new block would overlap.
type PlanConflictError struct {
	Title string
	Start time.Time
	End   time.Time
}

func (e *PlanConflictError) Error() string {
	return fmt.Sprintf("overlaps the planned block %q (%s to %s)", e.Title, e.Start.UTC().Format(time.RFC3339), e.End.UTC().Format(time.RFC3339))
}

// AddPlanBlock adds one block to the date's plan and keeps every other block
// as it is, in a new plan version. It refuses a block that overlaps an
// unfinished block instead of moving anything.
func (p *Postgres) AddPlanBlock(ctx context.Context, planDate time.Time, spec PlanBlockSpec, reason string) (AgentChange, Plan, error) {
	reason, err := validReason(reason)
	if err != nil {
		return AgentChange{}, Plan{}, err
	}
	added, err := p.planBlocksFromInput(ctx, PlanInput{Blocks: []PlanBlockSpec{spec}})
	if err != nil {
		return AgentChange{}, Plan{}, fmt.Errorf("%w: %v", ErrInvalidAgentChange, err)
	}
	block := added[0]
	end := block.StartTime.Add(time.Duration(block.DurationMinutes) * time.Minute)
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return AgentChange{}, Plan{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockPlanDate(ctx, tx, planDate); err != nil {
		return AgentChange{}, Plan{}, err
	}
	before, err := p.latestPlanTx(ctx, tx, planDate)
	if err != nil {
		return AgentChange{}, Plan{}, err
	}
	blocks := []ProposedBlock{}
	if before.VersionID != 0 {
		current, err := p.planBlocks(ctx, tx, before.VersionID)
		if err != nil {
			return AgentChange{}, Plan{}, err
		}
		for _, existing := range current {
			existingEnd := existing.StartTime.Add(time.Duration(existing.DurationMinutes) * time.Minute)
			finished := existing.State == "done" || existing.State == "skipped" || existing.State == "switched"
			if !finished && block.StartTime.Before(existingEnd) && existing.StartTime.Before(end) {
				return AgentChange{}, Plan{}, &PlanConflictError{Title: existing.Title, Start: existing.StartTime, End: existingEnd}
			}
			blocks = append(blocks, carried(existing))
		}
	}
	position := len(blocks)
	for i, existing := range blocks {
		if block.StartTime.Before(existing.StartTime) {
			position = i
			break
		}
	}
	blocks = append(blocks[:position], append([]ProposedBlock{block}, blocks[position:]...)...)
	plan, err := insertPlanVersion(ctx, tx, planDate, before.Note, blocks)
	if err != nil {
		return AgentChange{}, Plan{}, err
	}
	change, err := insertAgentChange(ctx, tx, AgentChangePlan, plan, before, reason, nil, nil)
	if err != nil {
		return AgentChange{}, Plan{}, err
	}
	return change, plan, tx.Commit(ctx)
}

// revertTasks restores each task to its state before the change. A task the
// change created is marked abandoned: tasks are never deleted.
func revertTasks(ctx context.Context, tx pgx.Tx, raw json.RawMessage) (any, any, error) {
	var restore []taskBefore
	if err := json.Unmarshal(raw, &restore); err != nil {
		return nil, nil, err
	}
	previous := []taskBefore{}
	written := []Activity{}
	for _, item := range restore {
		current, err := readActivityTx(ctx, tx, item.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("task %d: %w", item.ID, err)
		}
		target := current
		if item.Activity != nil {
			target = *item.Activity
		} else {
			target.Status = activityStatusAbandoned
		}
		updated, err := writeActivityTx(ctx, tx, target, false)
		if err != nil {
			return nil, nil, err
		}
		previous = append(previous, taskBefore{ID: current.ID, Activity: &current})
		written = append(written, updated)
	}
	return written, previous, nil
}

// revertPlan saves the replaced plan's blocks as a new version. A date that
// had no plan gets an empty version: versions are never deleted.
func (p *Postgres) revertPlan(ctx context.Context, tx pgx.Tx, raw json.RawMessage, reason string) (any, any, error) {
	var restore planBefore
	if err := json.Unmarshal(raw, &restore); err != nil {
		return nil, nil, err
	}
	planDate, err := time.Parse("2006-01-02", restore.PlanDate)
	if err != nil {
		return nil, nil, err
	}
	if err := lockPlanDate(ctx, tx, planDate); err != nil {
		return nil, nil, err
	}
	current, err := p.latestPlanTx(ctx, tx, planDate)
	if err != nil {
		return nil, nil, err
	}
	note := restore.Note
	if restore.VersionID == 0 {
		note = reason
	}
	plan, err := insertPlanVersion(ctx, tx, planDate, note, restore.Blocks)
	if err != nil {
		return nil, nil, err
	}
	return plan, current, nil
}
