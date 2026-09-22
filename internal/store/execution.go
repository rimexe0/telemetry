package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	executionStateActive          = "active"
	executionStateWaitingExternal = "waiting_external"
	executionStateInterrupted     = "interrupted"
	executionStateCompleted       = "completed"
	executionStateAbandoned       = "abandoned"

	executionEventStarted         = "started"
	executionEventPaused          = "paused"
	executionEventWaitingExternal = "waiting_external"
	executionEventInterrupted     = "interrupted"
	executionEventResumed         = "resumed"
	executionEventSwitchedOut     = "switched_out"
	executionEventCompleted       = "completed"
	executionEventAbandoned       = "abandoned"
)

var (
	ErrInvalidExecutionInput      = errors.New("invalid execution input")
	ErrInvalidExecutionTransition = errors.New("invalid execution transition")
)

var executionEventTypes = map[string]struct{}{
	executionEventStarted:         {},
	executionEventPaused:          {},
	executionEventWaitingExternal: {},
	executionEventInterrupted:     {},
	executionEventResumed:         {},
	executionEventSwitchedOut:     {},
	executionEventCompleted:       {},
	executionEventAbandoned:       {},
}

var executionStates = map[string]struct{}{
	executionStateActive:          {},
	executionStateWaitingExternal: {},
	executionStateInterrupted:     {},
	executionStateCompleted:       {},
	executionStateAbandoned:       {},
}

const (
	defaultExecutionSessionLimit = 50
	maxExecutionSessionLimit     = 200
)

type ExecutionSessionInput struct {
	ActivityID  int64      `json:"activity_id"`
	PlanBlockID *int64     `json:"plan_block_id,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
}

type ExecutionEventInput struct {
	EventType    string     `json:"event_type"`
	OccurredAt   *time.Time `json:"occurred_at,omitempty"`
	Note         string     `json:"note,omitempty"`
	ResumeMarker *string    `json:"resume_marker,omitempty"`
}

type ExecutionSession struct {
	ID                 int64            `json:"id"`
	ActivityID         int64            `json:"activity_id"`
	PlanBlockID        *int64           `json:"plan_block_id,omitempty"`
	StartedAt          time.Time        `json:"started_at"`
	EndedAt            *time.Time       `json:"ended_at,omitempty"`
	State              string           `json:"state"`
	NextActionSnapshot string           `json:"next_action_snapshot"`
	ResumeMarker       *string          `json:"resume_marker,omitempty"`
	CreatedAt          time.Time        `json:"created_at"`
	UpdatedAt          time.Time        `json:"updated_at"`
	Events             []ExecutionEvent `json:"events,omitempty"`
}

type ExecutionEvent struct {
	ID         int64     `json:"id"`
	SessionID  int64     `json:"session_id"`
	EventType  string    `json:"event_type"`
	OccurredAt time.Time `json:"occurred_at"`
	Note       string    `json:"note,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

func (p *Postgres) StartExecutionSession(ctx context.Context, input ExecutionSessionInput) (ExecutionSession, error) {
	if input.ActivityID <= 0 {
		return ExecutionSession{}, fmt.Errorf("%w: activity_id must be a positive integer", ErrInvalidExecutionInput)
	}
	if input.PlanBlockID != nil && *input.PlanBlockID <= 0 {
		return ExecutionSession{}, fmt.Errorf("%w: plan_block_id must be a positive integer", ErrInvalidExecutionInput)
	}
	startedAt := time.Now().UTC()
	if input.StartedAt != nil {
		startedAt = input.StartedAt.UTC()
	}

	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ExecutionSession{}, err
	}
	defer tx.Rollback(ctx)

	var nextAction string
	var resumeMarker *string
	err = tx.QueryRow(ctx, `
		SELECT next_action, NULLIF(resume_marker, '')
		FROM activities WHERE id = $1`, input.ActivityID).Scan(&nextAction, &resumeMarker)
	if errors.Is(err, pgx.ErrNoRows) {
		return ExecutionSession{}, ErrNotFound
	}
	if err != nil {
		return ExecutionSession{}, err
	}

	if input.PlanBlockID != nil {
		var blockActivityID *int64
		err = tx.QueryRow(ctx, `SELECT activity_id FROM plan_blocks WHERE id = $1`, *input.PlanBlockID).Scan(&blockActivityID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ExecutionSession{}, ErrNotFound
		}
		if err != nil {
			return ExecutionSession{}, err
		}
		if blockActivityID != nil && *blockActivityID != input.ActivityID {
			return ExecutionSession{}, fmt.Errorf("%w: plan_block_id belongs to another activity", ErrInvalidExecutionInput)
		}
	}

	var sessionID int64
	var createdAt, updatedAt time.Time
	err = tx.QueryRow(ctx, `
		INSERT INTO execution_sessions (
			activity_id, plan_block_id, started_at, state, next_action_snapshot, resume_marker
		) VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at, updated_at`,
		input.ActivityID, input.PlanBlockID, startedAt, executionStateActive, nextAction, resumeMarker).Scan(
		&sessionID, &createdAt, &updatedAt)
	if err != nil {
		return ExecutionSession{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO execution_events (session_id, event_type, occurred_at)
		VALUES ($1, $2, $3)`, sessionID, executionEventStarted, startedAt); err != nil {
		return ExecutionSession{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ExecutionSession{}, err
	}

	session, err := p.GetExecutionSession(ctx, sessionID)
	if err != nil {
		return ExecutionSession{}, err
	}
	return *session, nil
}

func (p *Postgres) TransitionExecutionSession(ctx context.Context, sessionID int64, input ExecutionEventInput) (ExecutionSession, error) {
	if sessionID <= 0 {
		return ExecutionSession{}, fmt.Errorf("%w: session id must be a positive integer", ErrInvalidExecutionInput)
	}
	eventType := strings.TrimSpace(input.EventType)
	if _, ok := executionEventTypes[eventType]; !ok || eventType == executionEventStarted {
		return ExecutionSession{}, fmt.Errorf("%w: event_type must be one of paused, waiting_external, interrupted, resumed, switched_out, completed, abandoned", ErrInvalidExecutionTransition)
	}
	note := strings.TrimSpace(input.Note)
	if len([]rune(note)) > maxPlanningTextRunes {
		return ExecutionSession{}, fmt.Errorf("%w: note must be at most %d characters", ErrInvalidExecutionInput, maxPlanningTextRunes)
	}
	var resumeMarker any
	if input.ResumeMarker != nil {
		value := strings.TrimSpace(*input.ResumeMarker)
		if len([]rune(value)) > maxPlanningTextRunes {
			return ExecutionSession{}, fmt.Errorf("%w: resume_marker must be at most %d characters", ErrInvalidExecutionInput, maxPlanningTextRunes)
		}
		resumeMarker = value
	}
	occurredAt := time.Now().UTC()
	if input.OccurredAt != nil {
		occurredAt = input.OccurredAt.UTC()
	}

	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ExecutionSession{}, err
	}
	defer tx.Rollback(ctx)

	var currentState string
	var startedAt time.Time
	err = tx.QueryRow(ctx, `
		SELECT state, started_at
		FROM execution_sessions WHERE id = $1 FOR UPDATE`, sessionID).Scan(&currentState, &startedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ExecutionSession{}, ErrNotFound
	}
	if err != nil {
		return ExecutionSession{}, err
	}
	if occurredAt.Before(startedAt) {
		return ExecutionSession{}, fmt.Errorf("%w: occurred_at cannot be before started_at", ErrInvalidExecutionInput)
	}

	nextState, terminal, err := nextExecutionState(currentState, eventType)
	if err != nil {
		return ExecutionSession{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE execution_sessions
		SET state = $2,
			ended_at = CASE WHEN $3 THEN $4 ELSE ended_at END,
			resume_marker = CASE WHEN $5 THEN NULLIF($6::text, '') ELSE resume_marker END,
			updated_at = now()
		WHERE id = $1`, sessionID, nextState, terminal, occurredAt, input.ResumeMarker != nil, resumeMarker); err != nil {
		return ExecutionSession{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO execution_events (session_id, event_type, occurred_at, note)
		VALUES ($1, $2, $3, $4)`, sessionID, eventType, occurredAt, note); err != nil {
		return ExecutionSession{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ExecutionSession{}, err
	}

	session, err := p.GetExecutionSession(ctx, sessionID)
	if err != nil {
		return ExecutionSession{}, err
	}
	return *session, nil
}

func (p *Postgres) CurrentExecutionSession(ctx context.Context) (*ExecutionSession, error) {
	row := p.pool.QueryRow(ctx, `
		SELECT id, activity_id, plan_block_id, started_at, ended_at, state,
			next_action_snapshot, resume_marker, created_at, updated_at
		FROM execution_sessions
		WHERE ended_at IS NULL
		  AND state IN ('active', 'waiting_external', 'interrupted')
		ORDER BY updated_at DESC, id DESC
		LIMIT 1`)
	session, err := scanExecutionSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	session.Events, err = p.executionEvents(ctx, session.ID)
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (p *Postgres) GetExecutionSession(ctx context.Context, sessionID int64) (*ExecutionSession, error) {
	if sessionID <= 0 {
		return nil, fmt.Errorf("%w: session id must be a positive integer", ErrInvalidExecutionInput)
	}
	row := p.pool.QueryRow(ctx, `
		SELECT id, activity_id, plan_block_id, started_at, ended_at, state,
			next_action_snapshot, resume_marker, created_at, updated_at
		FROM execution_sessions WHERE id = $1`, sessionID)
	session, err := scanExecutionSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	session.Events, err = p.executionEvents(ctx, session.ID)
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (p *Postgres) ListExecutionSessions(ctx context.Context, limit int) ([]ExecutionSession, error) {
	if limit <= 0 {
		limit = defaultExecutionSessionLimit
	}
	if limit > maxExecutionSessionLimit {
		return nil, fmt.Errorf("%w: execution session limit must be between 1 and %d", ErrInvalidExecutionInput, maxExecutionSessionLimit)
	}
	rows, err := p.pool.Query(ctx, `
		SELECT id, activity_id, plan_block_id, started_at, ended_at, state,
			next_action_snapshot, resume_marker, created_at, updated_at
		FROM execution_sessions
		ORDER BY started_at DESC, id DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sessions := make([]ExecutionSession, 0)
	for rows.Next() {
		session, err := scanExecutionSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

func (p *Postgres) executionEvents(ctx context.Context, sessionID int64) ([]ExecutionEvent, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT id, session_id, event_type, occurred_at, note, created_at
		FROM execution_events
		WHERE session_id = $1
		ORDER BY id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]ExecutionEvent, 0)
	for rows.Next() {
		var event ExecutionEvent
		if err := rows.Scan(&event.ID, &event.SessionID, &event.EventType, &event.OccurredAt, &event.Note, &event.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func scanExecutionSession(row pgx.Row) (ExecutionSession, error) {
	var session ExecutionSession
	err := row.Scan(
		&session.ID, &session.ActivityID, &session.PlanBlockID, &session.StartedAt,
		&session.EndedAt, &session.State, &session.NextActionSnapshot, &session.ResumeMarker,
		&session.CreatedAt, &session.UpdatedAt)
	return session, err
}

func nextExecutionState(currentState, eventType string) (string, bool, error) {
	if _, ok := executionStates[currentState]; !ok {
		return "", false, fmt.Errorf("%w: unknown current state %s", ErrInvalidExecutionTransition, currentState)
	}
	if currentState == executionStateCompleted || currentState == executionStateAbandoned {
		return "", false, fmt.Errorf("%w: terminal session cannot receive %s", ErrInvalidExecutionTransition, eventType)
	}
	switch eventType {
	case executionEventPaused, executionEventInterrupted, executionEventSwitchedOut:
		if currentState != executionStateActive && currentState != executionStateWaitingExternal {
			return "", false, fmt.Errorf("%w: %s cannot transition from %s", ErrInvalidExecutionTransition, eventType, currentState)
		}
		return executionStateInterrupted, false, nil
	case executionEventWaitingExternal:
		if currentState != executionStateActive {
			return "", false, fmt.Errorf("%w: waiting_external cannot transition from %s", ErrInvalidExecutionTransition, currentState)
		}
		return executionStateWaitingExternal, false, nil
	case executionEventResumed:
		if currentState != executionStateWaitingExternal && currentState != executionStateInterrupted {
			return "", false, fmt.Errorf("%w: resumed cannot transition from %s", ErrInvalidExecutionTransition, currentState)
		}
		return executionStateActive, false, nil
	case executionEventCompleted:
		return executionStateCompleted, true, nil
	case executionEventAbandoned:
		return executionStateAbandoned, true, nil
	default:
		return "", false, fmt.Errorf("%w: unknown event %s", ErrInvalidExecutionTransition, eventType)
	}
}
