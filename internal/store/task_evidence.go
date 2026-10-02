package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rimexe0/telemetry/internal/activity"
)

const (
	EvidenceRelated   = "related"
	EvidenceUnrelated = "unrelated"
	// EvidenceUnsure is a judge's "cannot tell"; it means: ask the owner.
	EvidenceUnsure = "unsure"

	EvidenceOwner = "owner"
	EvidenceJudge = "judge"
)

// TaskEvidence is one thing a task has learned: that Context (an app or
// site, with its project for an agent app) is or is not part of it. The
// owner's word outranks a judge's.
type TaskEvidence struct {
	ID        int64     `json:"id"`
	TaskID    int64     `json:"task_id"`
	Context   string    `json:"context"`
	Verdict   string    `json:"verdict"`
	Source    string    `json:"source"`
	Reason    string    `json:"reason,omitempty"`
	DeviceID  string    `json:"device_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

var ErrInvalidEvidence = errors.New("invalid task evidence")

// AddTaskEvidence records an answer and replaces the task's earlier entry
// for the same context, which is kept as history. A judge never replaces
// the owner's own answer; in that case the existing entry is returned.
func (p *Postgres) AddTaskEvidence(ctx context.Context, evidence TaskEvidence) (TaskEvidence, error) {
	evidence.Context = strings.TrimSpace(evidence.Context)
	switch {
	case evidence.Context == "" || len([]rune(evidence.Context)) > 500:
		return TaskEvidence{}, fmt.Errorf("%w: context must be 1 to 500 characters", ErrInvalidEvidence)
	case evidence.Verdict != EvidenceRelated && evidence.Verdict != EvidenceUnrelated && evidence.Verdict != EvidenceUnsure:
		return TaskEvidence{}, fmt.Errorf("%w: verdict must be related, unrelated or unsure", ErrInvalidEvidence)
	case evidence.Source != EvidenceOwner && evidence.Source != EvidenceJudge:
		return TaskEvidence{}, fmt.Errorf("%w: source must be owner or judge", ErrInvalidEvidence)
	case evidence.Source == EvidenceOwner && evidence.Verdict == EvidenceUnsure:
		return TaskEvidence{}, fmt.Errorf("%w: the owner's answer is related or unrelated", ErrInvalidEvidence)
	}
	if runes := []rune(evidence.Reason); len(runes) > 500 {
		evidence.Reason = string(runes[:500])
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return TaskEvidence{}, err
	}
	defer tx.Rollback(ctx)
	var current TaskEvidence
	err = tx.QueryRow(ctx, `SELECT id, task_id, context, verdict, source, reason, device_id, created_at FROM task_evidence
		WHERE task_id = $1 AND lower(context) = lower($2) AND superseded_at IS NULL FOR UPDATE`, evidence.TaskID, evidence.Context).Scan(
		&current.ID, &current.TaskID, &current.Context, &current.Verdict, &current.Source, &current.Reason, &current.DeviceID, &current.CreatedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return TaskEvidence{}, err
	}
	if err == nil {
		if current.Source == EvidenceOwner && evidence.Source == EvidenceJudge {
			return current, nil
		}
		if _, err := tx.Exec(ctx, `UPDATE task_evidence SET superseded_at = now() WHERE id = $1`, current.ID); err != nil {
			return TaskEvidence{}, err
		}
	}
	err = tx.QueryRow(ctx, `INSERT INTO task_evidence (task_id, context, verdict, source, reason, device_id) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id, created_at`,
		evidence.TaskID, evidence.Context, evidence.Verdict, evidence.Source, evidence.Reason, evidence.DeviceID).Scan(&evidence.ID, &evidence.CreatedAt)
	var postgresErr *pgconn.PgError
	if errors.As(err, &postgresErr) && postgresErr.Code == "23503" {
		return TaskEvidence{}, ErrNotFound
	}
	if err != nil {
		return TaskEvidence{}, err
	}
	return evidence, tx.Commit(ctx)
}

// ListTaskEvidence returns what every task currently believes, oldest first.
func (p *Postgres) ListTaskEvidence(ctx context.Context) ([]TaskEvidence, error) {
	rows, err := p.pool.Query(ctx, `SELECT id, task_id, context, verdict, source, reason, device_id, created_at FROM task_evidence
		WHERE superseded_at IS NULL ORDER BY id LIMIT 5000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []TaskEvidence{}
	for rows.Next() {
		var item TaskEvidence
		if err := rows.Scan(&item.ID, &item.TaskID, &item.Context, &item.Verdict, &item.Source, &item.Reason, &item.DeviceID, &item.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, item)
	}
	return list, rows.Err()
}

// LoadFocusSpans reads the sessions that overlap [from, to), with their
// events, and returns their running stretches.
func LoadFocusSpans(ctx context.Context, repository Repository, from, to time.Time) ([]activity.FocusSpan, error) {
	listed, err := repository.ListExecutionSessions(ctx, maxExecutionSessionLimit)
	if err != nil {
		return nil, err
	}
	sessions := []ExecutionSession{}
	for _, session := range listed {
		if !session.StartedAt.Before(to) || (session.EndedAt != nil && !session.EndedAt.After(from)) {
			continue
		}
		full, err := repository.GetExecutionSession(ctx, session.ID)
		if err != nil {
			return nil, err
		}
		if full != nil {
			sessions = append(sessions, *full)
		}
	}
	return FocusSpans(sessions, from, to), nil
}

// FocusSpans gives the stretches in which a primary execution session was
// running, as focus sessions, clipped to [from, to). A paused session is not
// running; an open one runs up to to.
func FocusSpans(sessions []ExecutionSession, from, to time.Time) []activity.FocusSpan {
	spans := []activity.FocusSpan{}
	for _, session := range sessions {
		if session.Role != ExecutionRolePrimary {
			continue
		}
		var running *time.Time
		close := func(at time.Time) {
			if running == nil {
				return
			}
			start, end := *running, at
			if start.Before(from) {
				start = from
			}
			if end.After(to) {
				end = to
			}
			if end.After(start) {
				spans = append(spans, activity.FocusSpan{TaskID: session.ActivityID, Start: start, End: end})
			}
			running = nil
		}
		for _, event := range session.Events {
			at := event.OccurredAt
			switch event.EventType {
			case executionEventStarted, executionEventResumed:
				if running == nil {
					running = &at
				}
			case executionEventPaused, executionEventInterrupted, executionEventSwitchedOut, executionEventOfflineRecoveryStarted,
				executionEventCompleted, executionEventAbandoned:
				close(at)
			}
		}
		close(to)
	}
	return spans
}
