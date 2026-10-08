package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rimexe0/telemetry/internal/activity"
	"github.com/rimexe0/telemetry/internal/places"
	"github.com/rimexe0/telemetry/internal/policy"
)

// TaskClaim is the owner's answer on a device to "what are you working on".
// TaskID is nil for "not work". Context is what was on screen when asked.
type TaskClaim struct {
	ID        int64     `json:"id"`
	TaskID    *int64    `json:"task_id,omitempty"`
	StartedAt time.Time `json:"started_at"`
	DeviceID  string    `json:"device_id"`
	Context   string    `json:"context,omitempty"`
}

// AddTaskClaim records an answer, starting now.
func (p *Postgres) AddTaskClaim(ctx context.Context, claim TaskClaim) (TaskClaim, error) {
	if runes := []rune(claim.Context); len(runes) > 500 {
		claim.Context = string(runes[:500])
	}
	err := p.pool.QueryRow(ctx, `INSERT INTO task_claims (task_id, device_id, context) VALUES ($1, $2, $3) RETURNING id, started_at`,
		claim.TaskID, claim.DeviceID, claim.Context).Scan(&claim.ID, &claim.StartedAt)
	var postgresErr *pgconn.PgError
	if errors.As(err, &postgresErr) && postgresErr.Code == "23503" {
		return TaskClaim{}, ErrNotFound
	}
	return claim, err
}

// ListTaskClaims returns the claims that started in [from, to), oldest first.
func (p *Postgres) ListTaskClaims(ctx context.Context, from, to time.Time) ([]TaskClaim, error) {
	rows, err := p.pool.Query(ctx, `SELECT id, task_id, started_at, device_id, context FROM task_claims
		WHERE started_at >= $1 AND started_at < $2 ORDER BY started_at, id LIMIT 1000`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	claims := []TaskClaim{}
	for rows.Next() {
		var claim TaskClaim
		if err := rows.Scan(&claim.ID, &claim.TaskID, &claim.StartedAt, &claim.DeviceID, &claim.Context); err != nil {
			return nil, err
		}
		claims = append(claims, claim)
	}
	return claims, rows.Err()
}

// LoadDayInput gathers what the day view and the current-task reading need
// for [from, to): recorded activity, labels, agent messages, the owner's
// open tasks with their rules and memory, their claims, their focus
// sessions, and the plan for planDate.
// LoadWakeTime finds when the owner woke, from the records up to at. It is
// nil when the records do not show it.
func LoadWakeTime(ctx context.Context, repository Repository, at time.Time) (*time.Time, error) {
	from := at.Add(-activity.WakeLookback)
	records, _, err := repository.ActivityRecords(ctx, from, at)
	if err != nil {
		return nil, err
	}
	return activity.WakeTime(activity.Build(records, from, at), from, at), nil
}

func LoadDayInput(ctx context.Context, repository Repository, planDate, from, to time.Time, withSegments bool) (activity.DayInput, *Plan, error) {
	return loadDayInput(ctx, repository, planDate, from, to, withSegments, true)
}

// LoadTrackingInput is LoadDayInput without the phone's positions, for
// what is on screen now, which does not need them.
func LoadTrackingInput(ctx context.Context, repository Repository, planDate, from, to time.Time) (activity.DayInput, *Plan, error) {
	return loadDayInput(ctx, repository, planDate, from, to, false, false)
}

func loadDayInput(ctx context.Context, repository Repository, planDate, from, to time.Time, withSegments, withFixes bool) (activity.DayInput, *Plan, error) {
	input := activity.DayInput{From: from, To: to, WithSegments: withSegments}
	records, _, err := repository.ActivityRecords(ctx, from, to)
	if err != nil {
		return input, nil, err
	}
	input.Result = activity.Build(records, from, to)
	labels, err := repository.ListActivityLabels(ctx)
	if err != nil {
		return input, nil, err
	}
	input.Labels = LabelsForSummary(labels)
	coding, err := repository.CodingRecords(ctx, from)
	if err != nil {
		return input, nil, err
	}
	input.Messages, input.Turns = activity.ThreadMessages(coding), activity.AgentTurns(coding)
	input.Open = activity.OpenSpans(coding)
	activities, err := repository.ListActivities(ctx, ActivityFilter{Limit: 500})
	if err != nil {
		return input, nil, err
	}
	rules, err := repository.ListActivityContextRules(ctx, false)
	if err != nil {
		return input, nil, err
	}
	byTask := TaskRules(rules)
	evidence, err := repository.ListTaskEvidence(ctx)
	if err != nil {
		return input, nil, err
	}
	for _, item := range activities {
		if item.Status == "done" || item.Status == "completed" || item.Status == "abandoned" {
			continue
		}
		info := activity.TaskInfo{ID: item.ID, Title: item.Title, NextAction: item.NextAction, Rules: byTask[item.ID]}
		for _, known := range evidence {
			if known.TaskID != item.ID {
				continue
			}
			switch known.Verdict {
			case EvidenceRelated:
				info.Related = append(info.Related, known.Context)
			case EvidenceUnrelated:
				info.Unrelated = append(info.Unrelated, known.Context)
			}
		}
		input.Tasks = append(input.Tasks, info)
	}
	if input.Focus, err = LoadFocusSpans(ctx, repository, from, to); err != nil {
		return input, nil, err
	}
	// A claim made before from can still hold at from.
	claims, err := repository.ListTaskClaims(ctx, from.Add(-12*time.Hour), to)
	if err != nil {
		return input, nil, err
	}
	for _, claim := range claims {
		input.Claims = append(input.Claims, activity.Claim{TaskID: claim.TaskID, Context: claim.Context, Start: claim.StartedAt})
	}
	plan, err := repository.LatestPlan(ctx, planDate)
	if err != nil {
		return input, nil, err
	}
	input.Blocks = PlannedBlocks(plan)
	if !withFixes {
		return input, plan, nil
	}
	// Positions for time outside: two weeks of nights find home, and the
	// half day before from shows a stay already going at from.
	history, err := repository.LocationFixes(ctx, to.Add(-14*24*time.Hour), to)
	if err != nil {
		return input, nil, err
	}
	location, err := OwnerLocation(ctx, repository)
	if err != nil {
		return input, nil, err
	}
	input.Home = places.Home(history, location)
	for _, fix := range history {
		if !fix.At.Before(from.Add(-12 * time.Hour)) {
			input.Fixes = append(input.Fixes, fix)
		}
	}
	return input, plan, nil
}

// OwnerLocation is the owner's time zone from the policy.
func OwnerLocation(ctx context.Context, repository Repository) (*time.Location, error) {
	config := policy.Config{}
	stored, err := repository.LatestPolicy(ctx)
	if err != nil {
		return nil, err
	}
	if stored != nil {
		config = stored.Config
	}
	if err := config.Normalize(); err != nil {
		return nil, err
	}
	return time.LoadLocation(config.Timezone)
}
