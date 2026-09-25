package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	ExecutionRolePrimary           = "primary"
	ExecutionRoleWaitingInterleave = "waiting_interleave"

	executionStateActive          = "active"
	executionStateWaitingExternal = "waiting_external"
	executionStateInterrupted     = "interrupted"
	executionStateCompleted       = "completed"
	executionStateAbandoned       = "abandoned"

	executionEventStarted                 = "started"
	executionEventPaused                  = "paused"
	executionEventWaitingExternal         = "waiting_external"
	executionEventExternalReady           = "external_ready"
	executionEventInterrupted             = "interrupted"
	executionEventResumed                 = "resumed"
	executionEventSwitchedOut             = "switched_out"
	executionEventCompleted               = "completed"
	executionEventAbandoned               = "abandoned"
	executionEventStuckUnknownNextAction  = "stuck_unknown_next_action"
	executionEventStuckEmotionalOverwhelm = "stuck_emotional_overwhelm"
	executionEventOverwhelmed             = "overwhelmed"
	executionEventOfflineRecoveryStarted  = "offline_recovery_started"
	executionEventOfflineRecoveryEnded    = "offline_recovery_ended"
	executionEventOwnerExplanation        = "owner_explanation"
)

var (
	ErrInvalidExecutionInput      = errors.New("invalid execution input")
	ErrInvalidExecutionTransition = errors.New("invalid execution transition")
	ErrExecutionConflict          = errors.New("execution state conflict")
)

var executionEventTypes = map[string]struct{}{
	executionEventStarted:                 {},
	executionEventPaused:                  {},
	executionEventWaitingExternal:         {},
	executionEventExternalReady:           {},
	executionEventInterrupted:             {},
	executionEventResumed:                 {},
	executionEventSwitchedOut:             {},
	executionEventCompleted:               {},
	executionEventAbandoned:               {},
	executionEventStuckUnknownNextAction:  {},
	executionEventStuckEmotionalOverwhelm: {},
	executionEventOverwhelmed:             {},
	executionEventOfflineRecoveryStarted:  {},
	executionEventOfflineRecoveryEnded:    {},
	executionEventOwnerExplanation:        {},
}

var executionStates = map[string]struct{}{
	executionStateActive:          {},
	executionStateWaitingExternal: {},
	executionStateInterrupted:     {},
	executionStateCompleted:       {},
	executionStateAbandoned:       {},
}

const (
	defaultExecutionSessionLimit    = 50
	maxExecutionSessionLimit        = 200
	defaultExecutionInterleaveLimit = 20
	maxExecutionInterleaveLimit     = 100
)

type ExecutionSessionInput struct {
	ActivityID       int64      `json:"activity_id"`
	PlanBlockID      *int64     `json:"plan_block_id,omitempty"`
	Role             string     `json:"role,omitempty"`
	PrimarySessionID *int64     `json:"primary_session_id,omitempty"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
}

type ReturnToPrimaryInput struct {
	OccurredAt             *time.Time `json:"occurred_at,omitempty"`
	PrimaryResumeMarker    *string    `json:"primary_resume_marker,omitempty"`
	InterleaveResumeMarker *string    `json:"interleave_resume_marker,omitempty"`
}

type ExecutionReturn struct {
	PrimarySession    ExecutionSession `json:"primary_session"`
	InterleaveSession ExecutionSession `json:"interleave_session"`
}

type ExecutionEventInput struct {
	EventType    string          `json:"event_type"`
	OccurredAt   *time.Time      `json:"occurred_at,omitempty"`
	Note         string          `json:"note,omitempty"`
	ResumeMarker *string         `json:"resume_marker,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
}

type ExecutionSession struct {
	ID                 int64            `json:"id"`
	ActivityID         int64            `json:"activity_id"`
	PlanBlockID        *int64           `json:"plan_block_id,omitempty"`
	Role               string           `json:"role"`
	PrimarySessionID   *int64           `json:"primary_session_id,omitempty"`
	StartedAt          time.Time        `json:"started_at"`
	EndedAt            *time.Time       `json:"ended_at,omitempty"`
	State              string           `json:"state"`
	NextActionSnapshot string           `json:"next_action_snapshot"`
	ResumeMarker       *string          `json:"resume_marker,omitempty"`
	CreatedAt          time.Time        `json:"created_at"`
	UpdatedAt          time.Time        `json:"updated_at"`
	Events             []ExecutionEvent `json:"events,omitempty"`
}

func executionRole(input ExecutionSessionInput) (string, error) {
	role := strings.TrimSpace(input.Role)
	if role == "" {
		role = ExecutionRolePrimary
	}
	switch role {
	case ExecutionRolePrimary:
		if input.PrimarySessionID != nil {
			return "", fmt.Errorf("%w: primary sessions cannot have a primary_session_id", ErrInvalidExecutionInput)
		}
	case ExecutionRoleWaitingInterleave:
		if input.PrimarySessionID == nil || *input.PrimarySessionID <= 0 {
			return "", fmt.Errorf("%w: waiting_interleave requires a positive primary_session_id", ErrInvalidExecutionInput)
		}
	default:
		return "", fmt.Errorf("%w: role must be primary or waiting_interleave", ErrInvalidExecutionInput)
	}
	return role, nil
}

func executionUniqueConflict(err error) bool {
	var postgresErr *pgconn.PgError
	if !errors.As(err, &postgresErr) || postgresErr.Code != "23505" {
		return false
	}
	return postgresErr.ConstraintName == "execution_sessions_one_open_primary_index" ||
		postgresErr.ConstraintName == "execution_sessions_one_actionable_interleave_index"
}

type ExecutionEvent struct {
	ID         int64           `json:"id"`
	SessionID  int64           `json:"session_id"`
	EventType  string          `json:"event_type"`
	OccurredAt time.Time       `json:"occurred_at"`
	Note       string          `json:"note,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
	Provenance json.RawMessage `json:"provenance,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

var defaultExecutionProvenance = json.RawMessage(`{"recorded_by":"owner","source":"execution_api"}`)

func executionMetadata(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if len(raw) > maxPlanningDetails || !json.Valid(raw) {
		return nil, fmt.Errorf("%w: metadata must be a JSON object no larger than %d bytes", ErrInvalidExecutionInput, maxPlanningDetails)
	}
	trimmed := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}") {
		return nil, fmt.Errorf("%w: metadata must be a JSON object", ErrInvalidExecutionInput)
	}
	return json.RawMessage(trimmed), nil
}

func (p *Postgres) StartExecutionSession(ctx context.Context, input ExecutionSessionInput) (ExecutionSession, error) {
	role, err := executionRole(input)
	if err != nil {
		return ExecutionSession{}, err
	}
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
	if role == ExecutionRoleWaitingInterleave {
		var primaryRole, primaryState string
		var primaryEndedAt *time.Time
		var primaryStartedAt time.Time
		var waitingAt *time.Time
		var latestPrimaryEventAt *time.Time
		err = tx.QueryRow(ctx, `
			SELECT execution_role, state, ended_at, started_at,
				(SELECT occurred_at FROM execution_events WHERE session_id = execution_sessions.id AND event_type = $2 ORDER BY id DESC LIMIT 1),
				(SELECT MAX(occurred_at) FROM execution_events WHERE session_id = execution_sessions.id)
			FROM execution_sessions WHERE id = $1 FOR UPDATE`, *input.PrimarySessionID, executionEventWaitingExternal).Scan(
			&primaryRole, &primaryState, &primaryEndedAt, &primaryStartedAt, &waitingAt, &latestPrimaryEventAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ExecutionSession{}, ErrNotFound
		}
		if err != nil {
			return ExecutionSession{}, err
		}
		if primaryRole != ExecutionRolePrimary || primaryEndedAt != nil || primaryState != executionStateWaitingExternal {
			return ExecutionSession{}, fmt.Errorf("%w: waiting interleaves require a non-terminal primary waiting for an external event", ErrExecutionConflict)
		}
		if waitingAt == nil || startedAt.Before(primaryStartedAt) || startedAt.Before(*waitingAt) || (latestPrimaryEventAt != nil && startedAt.Before(*latestPrimaryEventAt)) {
			return ExecutionSession{}, fmt.Errorf("%w: interleave start cannot be before the primary entered waiting_external", ErrInvalidExecutionInput)
		}
		var activeInterleave bool
		err = tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM execution_sessions
				WHERE primary_session_id = $1
				  AND execution_role = $2
				  AND ended_at IS NULL
				  AND state IN ($3, $4)
			)`, *input.PrimarySessionID, ExecutionRoleWaitingInterleave, executionStateActive, executionStateWaitingExternal).Scan(&activeInterleave)
		if err != nil {
			return ExecutionSession{}, err
		}
		if activeInterleave {
			return ExecutionSession{}, fmt.Errorf("%w: the primary already has an actionable waiting interleave", ErrExecutionConflict)
		}
	}

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
			activity_id, plan_block_id, execution_role, primary_session_id,
			started_at, state, next_action_snapshot, resume_marker
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at, updated_at`,
		input.ActivityID, input.PlanBlockID, role, input.PrimarySessionID, startedAt,
		executionStateActive, nextAction, resumeMarker).Scan(
		&sessionID, &createdAt, &updatedAt)
	if err != nil {
		if executionUniqueConflict(err) {
			return ExecutionSession{}, ErrExecutionConflict
		}
		return ExecutionSession{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO execution_events (session_id, event_type, occurred_at, metadata, provenance)
		VALUES ($1, $2, $3, $4::jsonb, $5::jsonb)`, sessionID, executionEventStarted, startedAt, `{}`, string(defaultExecutionProvenance)); err != nil {
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
	return p.transitionExecutionSession(ctx, sessionID, input, defaultExecutionProvenance)
}

var agentLifecycleProvenance = json.RawMessage(`{"recorded_by":"system","source":"agent_lifecycle"}`)

// RecordAgentReady records external_ready for a waiting primary from agent
// lifecycle evidence. It uses the same rules as an owner-recorded readiness:
// the primary must still be waiting, the event cannot predate its latest
// event, and only one readiness is kept per waiting cycle.
func (p *Postgres) RecordAgentReady(ctx context.Context, primarySessionID int64, candidate AgentReadyCandidate) (ExecutionSession, error) {
	at := candidate.Lifecycle.At
	return p.transitionExecutionSession(ctx, primarySessionID, ExecutionEventInput{EventType: executionEventExternalReady, OccurredAt: &at, Metadata: candidate.Metadata()}, agentLifecycleProvenance)
}

func (p *Postgres) transitionExecutionSession(ctx context.Context, sessionID int64, input ExecutionEventInput, provenance json.RawMessage) (ExecutionSession, error) {
	if sessionID <= 0 {
		return ExecutionSession{}, fmt.Errorf("%w: session id must be a positive integer", ErrInvalidExecutionInput)
	}
	eventType := strings.TrimSpace(input.EventType)
	if _, ok := executionEventTypes[eventType]; !ok || eventType == executionEventStarted {
		return ExecutionSession{}, fmt.Errorf("%w: event_type is not recognized", ErrInvalidExecutionTransition)
	}
	metadata, err := executionMetadata(input.Metadata)
	if err != nil {
		return ExecutionSession{}, err
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

	var role string
	var primarySessionID *int64
	err = tx.QueryRow(ctx, `
		SELECT execution_role, primary_session_id
		FROM execution_sessions WHERE id = $1`, sessionID).Scan(&role, &primarySessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ExecutionSession{}, ErrNotFound
	}
	if err != nil {
		return ExecutionSession{}, err
	}
	var primaryState string
	var primaryEndedAt *time.Time
	if role == ExecutionRoleWaitingInterleave {
		if primarySessionID == nil {
			return ExecutionSession{}, fmt.Errorf("%w: waiting interleave has no primary session", ErrInvalidExecutionTransition)
		}
		var primaryRole string
		err = tx.QueryRow(ctx, `
			SELECT execution_role, state, ended_at
			FROM execution_sessions WHERE id = $1 FOR UPDATE`, *primarySessionID).Scan(&primaryRole, &primaryState, &primaryEndedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ExecutionSession{}, ErrNotFound
		}
		if err != nil {
			return ExecutionSession{}, err
		}
		if primaryRole != ExecutionRolePrimary {
			return ExecutionSession{}, fmt.Errorf("%w: interleave parent is not a primary session", ErrExecutionConflict)
		}
	}
	var currentState string
	var startedAt time.Time
	var lastRecoveryEventType string
	err = tx.QueryRow(ctx, `
		SELECT state, started_at,
			COALESCE((SELECT event_type FROM execution_events WHERE session_id = execution_sessions.id AND event_type IN ($2, $3) ORDER BY id DESC LIMIT 1), '')
		FROM execution_sessions WHERE id = $1 FOR UPDATE`, sessionID, executionEventOfflineRecoveryStarted, executionEventOfflineRecoveryEnded).Scan(&currentState, &startedAt, &lastRecoveryEventType)
	if errors.Is(err, pgx.ErrNoRows) {
		return ExecutionSession{}, ErrNotFound
	}
	if err != nil {
		return ExecutionSession{}, err
	}
	if occurredAt.Before(startedAt) {
		return ExecutionSession{}, fmt.Errorf("%w: occurred_at cannot be before started_at", ErrInvalidExecutionInput)
	}
	if eventType == executionEventExternalReady {
		if role != ExecutionRolePrimary || currentState != executionStateWaitingExternal {
			return ExecutionSession{}, fmt.Errorf("%w: external_ready requires a primary waiting for an external event", ErrInvalidExecutionTransition)
		}
		var alreadyReady bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM execution_events AS ready
				WHERE ready.session_id = $1
				  AND ready.event_type = $2
				  AND ready.id > COALESCE((
					SELECT MAX(waiting.id) FROM execution_events AS waiting
					WHERE waiting.session_id = $1 AND waiting.event_type = $3
				  ), 0)
			)`, sessionID, executionEventExternalReady, executionEventWaitingExternal).Scan(&alreadyReady); err != nil {
			return ExecutionSession{}, err
		}
		if alreadyReady {
			if err := tx.Commit(ctx); err != nil {
				return ExecutionSession{}, err
			}
			session, err := p.GetExecutionSession(ctx, sessionID)
			if err != nil {
				return ExecutionSession{}, err
			}
			return *session, nil
		}
		var latestEventAt *time.Time
		if err := tx.QueryRow(ctx, `SELECT MAX(occurred_at) FROM execution_events WHERE session_id = $1`, sessionID).Scan(&latestEventAt); err != nil {
			return ExecutionSession{}, err
		}
		if latestEventAt != nil && occurredAt.Before(*latestEventAt) {
			return ExecutionSession{}, fmt.Errorf("%w: external_ready cannot predate the primary's latest event", ErrInvalidExecutionInput)
		}
	}

	nextState, terminal, err := nextExecutionStateWithHistory(currentState, lastRecoveryEventType, eventType)
	if err != nil {
		return ExecutionSession{}, err
	}
	if role == ExecutionRoleWaitingInterleave && nextState == executionStateActive && (primaryState != executionStateWaitingExternal || primaryEndedAt != nil) {
		return ExecutionSession{}, fmt.Errorf("%w: a waiting interleave can resume only while its primary is non-terminal and waiting_external", ErrExecutionConflict)
	}
	if role == ExecutionRolePrimary && (nextState != currentState || terminal) {
		var activeInterleave bool
		err = tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM execution_sessions
				WHERE primary_session_id = $1
				  AND execution_role = $2
				  AND ended_at IS NULL
				  AND state IN ($3, $4)
			)`, sessionID, ExecutionRoleWaitingInterleave, executionStateActive, executionStateWaitingExternal).Scan(&activeInterleave)
		if err != nil {
			return ExecutionSession{}, err
		}
		if activeInterleave {
			return ExecutionSession{}, fmt.Errorf("%w: finish or pause the waiting interleave before changing the primary state", ErrExecutionConflict)
		}
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
		INSERT INTO execution_events (session_id, event_type, occurred_at, note, metadata, provenance)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb)`, sessionID, eventType, occurredAt, note, string(metadata), string(provenance)); err != nil {
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

func (p *Postgres) ReturnToPrimary(ctx context.Context, interleaveSessionID int64, input ReturnToPrimaryInput) (ExecutionReturn, error) {
	if interleaveSessionID <= 0 {
		return ExecutionReturn{}, fmt.Errorf("%w: session id must be a positive integer", ErrInvalidExecutionInput)
	}
	primaryMarker, err := boundedResumeMarker(input.PrimaryResumeMarker)
	if err != nil {
		return ExecutionReturn{}, err
	}
	interleaveMarker, err := boundedResumeMarker(input.InterleaveResumeMarker)
	if err != nil {
		return ExecutionReturn{}, err
	}
	occurredAt := time.Now().UTC()
	if input.OccurredAt != nil {
		occurredAt = input.OccurredAt.UTC()
	}

	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ExecutionReturn{}, err
	}
	defer tx.Rollback(ctx)
	var role string
	var primarySessionID *int64
	err = tx.QueryRow(ctx, `
		SELECT execution_role, primary_session_id
		FROM execution_sessions WHERE id = $1`, interleaveSessionID).Scan(&role, &primarySessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ExecutionReturn{}, ErrNotFound
	}
	if err != nil {
		return ExecutionReturn{}, err
	}
	if role != ExecutionRoleWaitingInterleave || primarySessionID == nil {
		return ExecutionReturn{}, fmt.Errorf("%w: session is not a waiting interleave", ErrInvalidExecutionInput)
	}

	var primaryRole, primaryState string
	var primaryEndedAt *time.Time
	var primaryStartedAt time.Time
	var currentPrimaryMarker *string
	err = tx.QueryRow(ctx, `
		SELECT execution_role, state, ended_at, started_at, resume_marker
		FROM execution_sessions WHERE id = $1 FOR UPDATE`, *primarySessionID).Scan(
		&primaryRole, &primaryState, &primaryEndedAt, &primaryStartedAt, &currentPrimaryMarker)
	if errors.Is(err, pgx.ErrNoRows) {
		return ExecutionReturn{}, ErrNotFound
	}
	if err != nil {
		return ExecutionReturn{}, err
	}
	var interleaveRole string
	var actualPrimaryID *int64
	var interleaveState string
	var interleaveEndedAt *time.Time
	var interleaveStartedAt time.Time
	var currentInterleaveMarker *string
	err = tx.QueryRow(ctx, `
		SELECT execution_role, primary_session_id, state, ended_at, started_at, resume_marker
		FROM execution_sessions WHERE id = $1 FOR UPDATE`, interleaveSessionID).Scan(
		&interleaveRole, &actualPrimaryID, &interleaveState, &interleaveEndedAt,
		&interleaveStartedAt, &currentInterleaveMarker)
	if err != nil {
		return ExecutionReturn{}, err
	}
	if primaryRole != ExecutionRolePrimary || actualPrimaryID == nil || *actualPrimaryID != *primarySessionID || interleaveRole != ExecutionRoleWaitingInterleave {
		return ExecutionReturn{}, fmt.Errorf("%w: primary/interleave relationship is no longer current", ErrExecutionConflict)
	}
	var activationEventID int64
	err = tx.QueryRow(ctx, `
		SELECT id FROM execution_events
		WHERE session_id = $1 AND event_type IN ($2, $3)
		ORDER BY id DESC LIMIT 1`, interleaveSessionID, executionEventStarted, executionEventResumed).Scan(&activationEventID)
	if err != nil {
		return ExecutionReturn{}, err
	}
	var previousReturnMetadata []byte
	err = tx.QueryRow(ctx, `
		SELECT metadata FROM execution_events
		WHERE session_id = $1 AND event_type = $2
		  AND metadata->>'interleave_activation_event_id' = $3
		ORDER BY id DESC LIMIT 1`, *primarySessionID, executionEventResumed, strconv.FormatInt(activationEventID, 10)).Scan(&previousReturnMetadata)
	if err == nil {
		if !returnMarkerMatches(previousReturnMetadata, "primary_resume_marker", primaryMarker) || !returnMarkerMatches(previousReturnMetadata, "interleave_resume_marker", interleaveMarker) {
			return ExecutionReturn{}, fmt.Errorf("%w: this interleave activation was already returned with different markers", ErrExecutionConflict)
		}
		if err := tx.Commit(ctx); err != nil {
			return ExecutionReturn{}, err
		}
		return p.executionReturn(ctx, *primarySessionID, interleaveSessionID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ExecutionReturn{}, err
	}
	if primaryEndedAt != nil {
		return ExecutionReturn{}, fmt.Errorf("%w: primary has already ended", ErrExecutionConflict)
	}
	if occurredAt.Before(primaryStartedAt) || occurredAt.Before(interleaveStartedAt) {
		return ExecutionReturn{}, fmt.Errorf("%w: occurred_at cannot be before either session started", ErrInvalidExecutionInput)
	}
	var latestPrimaryEventAt, latestInterleaveEventAt *time.Time
	if err := tx.QueryRow(ctx, `SELECT MAX(occurred_at) FROM execution_events WHERE session_id = $1`, *primarySessionID).Scan(&latestPrimaryEventAt); err != nil {
		return ExecutionReturn{}, err
	}
	if err := tx.QueryRow(ctx, `SELECT MAX(occurred_at) FROM execution_events WHERE session_id = $1`, interleaveSessionID).Scan(&latestInterleaveEventAt); err != nil {
		return ExecutionReturn{}, err
	}
	if (latestPrimaryEventAt != nil && occurredAt.Before(*latestPrimaryEventAt)) || (latestInterleaveEventAt != nil && occurredAt.Before(*latestInterleaveEventAt)) {
		return ExecutionReturn{}, fmt.Errorf("%w: return cannot predate either session's latest event", ErrInvalidExecutionInput)
	}
	if primaryMarker != nil && markerValue(currentPrimaryMarker) != *primaryMarker && primaryState == executionStateActive {
		return ExecutionReturn{}, fmt.Errorf("%w: a completed return cannot replace the primary resume marker", ErrExecutionConflict)
	}
	if interleaveMarker != nil && markerValue(currentInterleaveMarker) != *interleaveMarker && (primaryState == executionStateActive || interleaveEndedAt != nil || interleaveState == executionStateInterrupted) {
		return ExecutionReturn{}, fmt.Errorf("%w: a completed return cannot replace the interleave resume marker", ErrExecutionConflict)
	}

	if primaryState == executionStateActive {
		if interleaveEndedAt == nil && (interleaveState == executionStateActive || interleaveState == executionStateWaitingExternal) {
			return ExecutionReturn{}, fmt.Errorf("%w: active primary still has an actionable interleave", ErrExecutionConflict)
		}
		if err := tx.Commit(ctx); err != nil {
			return ExecutionReturn{}, err
		}
		return p.executionReturn(ctx, *primarySessionID, interleaveSessionID)
	}
	if primaryEndedAt != nil || (primaryState != executionStateWaitingExternal && primaryState != executionStateInterrupted) {
		return ExecutionReturn{}, fmt.Errorf("%w: primary cannot resume from %s", ErrExecutionConflict, primaryState)
	}
	if interleaveEndedAt == nil && (interleaveState == executionStateActive || interleaveState == executionStateWaitingExternal) {
		nextState, _, err := nextExecutionState(interleaveState, executionEventSwitchedOut)
		if err != nil {
			return ExecutionReturn{}, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE execution_sessions
			SET state = $2,
				resume_marker = CASE WHEN $3 THEN NULLIF($4::text, '') ELSE resume_marker END,
				updated_at = now()
			WHERE id = $1`, interleaveSessionID, nextState, input.InterleaveResumeMarker != nil, interleaveMarker); err != nil {
			return ExecutionReturn{}, err
		}
		childMetadata, _ := json.Marshal(map[string]int64{
			"primary_session_id":             *primarySessionID,
			"interleave_activation_event_id": activationEventID,
		})
		if _, err := tx.Exec(ctx, `
			INSERT INTO execution_events (session_id, event_type, occurred_at, note, metadata, provenance)
			VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb)`, interleaveSessionID,
			executionEventSwitchedOut, occurredAt, "Returned to the primary objective.",
			string(childMetadata), string(defaultExecutionProvenance)); err != nil {
			return ExecutionReturn{}, err
		}
	}
	nextPrimaryState, _, err := nextExecutionState(primaryState, executionEventResumed)
	if err != nil {
		return ExecutionReturn{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE execution_sessions
		SET state = $2,
			resume_marker = CASE WHEN $3 THEN NULLIF($4::text, '') ELSE resume_marker END,
			updated_at = now()
		WHERE id = $1`, *primarySessionID, nextPrimaryState, input.PrimaryResumeMarker != nil, primaryMarker); err != nil {
		return ExecutionReturn{}, err
	}
	parentMetadataValues := map[string]any{
		"interleave_session_id":          interleaveSessionID,
		"interleave_activation_event_id": activationEventID,
	}
	if primaryMarker != nil {
		parentMetadataValues["primary_resume_marker"] = *primaryMarker
	}
	if interleaveMarker != nil {
		parentMetadataValues["interleave_resume_marker"] = *interleaveMarker
	}
	parentMetadata, _ := json.Marshal(parentMetadataValues)
	if _, err := tx.Exec(ctx, `
		INSERT INTO execution_events (session_id, event_type, occurred_at, note, metadata, provenance)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb)`, *primarySessionID,
		executionEventResumed, occurredAt, "Returned from a waiting interleave.",
		string(parentMetadata), string(defaultExecutionProvenance)); err != nil {
		return ExecutionReturn{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ExecutionReturn{}, err
	}
	return p.executionReturn(ctx, *primarySessionID, interleaveSessionID)
}

func boundedResumeMarker(marker *string) (*string, error) {
	if marker == nil {
		return nil, nil
	}
	value := strings.TrimSpace(*marker)
	if len([]rune(value)) > maxPlanningTextRunes {
		return nil, fmt.Errorf("%w: resume_marker must be at most %d characters", ErrInvalidExecutionInput, maxPlanningTextRunes)
	}
	return &value, nil
}

func markerValue(marker *string) string {
	if marker == nil {
		return ""
	}
	return *marker
}

func returnMarkerMatches(raw json.RawMessage, key string, marker *string) bool {
	var values map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return false
	}
	value, exists := values[key]
	if marker == nil {
		return !exists
	}
	if !exists {
		return false
	}
	var recorded string
	return json.Unmarshal(value, &recorded) == nil && recorded == *marker
}

func (p *Postgres) executionReturn(ctx context.Context, primarySessionID, interleaveSessionID int64) (ExecutionReturn, error) {
	primary, err := p.GetExecutionSession(ctx, primarySessionID)
	if err != nil {
		return ExecutionReturn{}, err
	}
	interleave, err := p.GetExecutionSession(ctx, interleaveSessionID)
	if err != nil {
		return ExecutionReturn{}, err
	}
	return ExecutionReturn{PrimarySession: *primary, InterleaveSession: *interleave}, nil
}

func (p *Postgres) CurrentExecutionSession(ctx context.Context) (*ExecutionSession, error) {
	row := p.pool.QueryRow(ctx, `
		SELECT id, activity_id, plan_block_id, execution_role, primary_session_id,
			started_at, ended_at, state,
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

func (p *Postgres) CurrentPrimaryExecutionSession(ctx context.Context) (*ExecutionSession, error) {
	row := p.pool.QueryRow(ctx, `
		SELECT id, activity_id, plan_block_id, execution_role, primary_session_id,
			started_at, ended_at, state, next_action_snapshot, resume_marker, created_at, updated_at
		FROM execution_sessions
		WHERE execution_role = $1 AND ended_at IS NULL
		LIMIT 1`, ExecutionRolePrimary)
	return p.executionSessionWithEvents(ctx, row)
}

func (p *Postgres) CurrentAttendedExecutionSession(ctx context.Context) (*ExecutionSession, error) {
	row := p.pool.QueryRow(ctx, `
		SELECT session.id, session.activity_id, session.plan_block_id,
			session.execution_role, session.primary_session_id, session.started_at,
			session.ended_at, session.state, session.next_action_snapshot,
			session.resume_marker, session.created_at, session.updated_at
		FROM execution_sessions AS session
		WHERE session.ended_at IS NULL AND (
			(session.execution_role = $1 AND session.state = $2 AND EXISTS (
				SELECT 1 FROM execution_sessions AS primary_session
				WHERE primary_session.id = session.primary_session_id
				  AND primary_session.execution_role = $3
				  AND primary_session.state = $4
				  AND primary_session.ended_at IS NULL
			))
			OR (session.execution_role = $3 AND session.state = $2 AND NOT EXISTS (
				SELECT 1 FROM execution_sessions AS interleave
				WHERE interleave.primary_session_id = session.id
				  AND interleave.execution_role = $1
				  AND interleave.state = $2
				  AND interleave.ended_at IS NULL
			))
		)
		ORDER BY CASE WHEN session.execution_role = $1 THEN 0 ELSE 1 END
		LIMIT 1`, ExecutionRoleWaitingInterleave, executionStateActive, ExecutionRolePrimary, executionStateWaitingExternal)
	return p.executionSessionWithEvents(ctx, row)
}

func (p *Postgres) GetExecutionSession(ctx context.Context, sessionID int64) (*ExecutionSession, error) {
	if sessionID <= 0 {
		return nil, fmt.Errorf("%w: session id must be a positive integer", ErrInvalidExecutionInput)
	}
	row := p.pool.QueryRow(ctx, `
		SELECT id, activity_id, plan_block_id, execution_role, primary_session_id,
			started_at, ended_at, state,
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
		SELECT id, activity_id, plan_block_id, execution_role, primary_session_id,
			started_at, ended_at, state,
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

func (p *Postgres) ListExecutionInterleaves(ctx context.Context, primarySessionID int64, limit int) ([]ExecutionSession, error) {
	if primarySessionID <= 0 {
		return nil, fmt.Errorf("%w: primary session id must be a positive integer", ErrInvalidExecutionInput)
	}
	if limit <= 0 {
		limit = defaultExecutionInterleaveLimit
	}
	if limit > maxExecutionInterleaveLimit {
		return nil, fmt.Errorf("%w: execution interleave limit must be between 1 and %d", ErrInvalidExecutionInput, maxExecutionInterleaveLimit)
	}
	var role string
	if err := p.pool.QueryRow(ctx, `SELECT execution_role FROM execution_sessions WHERE id = $1`, primarySessionID).Scan(&role); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if role != ExecutionRolePrimary {
		return nil, fmt.Errorf("%w: interleaves can only be listed for a primary session", ErrInvalidExecutionInput)
	}
	rows, err := p.pool.Query(ctx, `
		SELECT id, activity_id, plan_block_id, execution_role, primary_session_id,
			started_at, ended_at, state, next_action_snapshot, resume_marker, created_at, updated_at
		FROM execution_sessions
		WHERE primary_session_id = $1
		  AND execution_role = $2
		ORDER BY started_at DESC, id DESC
		LIMIT $3`, primarySessionID, ExecutionRoleWaitingInterleave, limit)
	if err != nil {
		return nil, err
	}
	sessions := make([]ExecutionSession, 0)
	for rows.Next() {
		session, err := scanExecutionSession(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for index := range sessions {
		sessions[index].Events, err = p.executionEvents(ctx, sessions[index].ID)
		if err != nil {
			return nil, err
		}
	}
	return sessions, nil
}

func (p *Postgres) executionSessionWithEvents(ctx context.Context, row pgx.Row) (*ExecutionSession, error) {
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

func (p *Postgres) executionEvents(ctx context.Context, sessionID int64) ([]ExecutionEvent, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT id, session_id, event_type, occurred_at, note, metadata, provenance, created_at
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
		var metadata, provenance []byte
		if err := rows.Scan(&event.ID, &event.SessionID, &event.EventType, &event.OccurredAt, &event.Note, &metadata, &provenance, &event.CreatedAt); err != nil {
			return nil, err
		}
		event.Metadata = json.RawMessage(metadata)
		event.Provenance = json.RawMessage(provenance)
		events = append(events, event)
	}
	return events, rows.Err()
}

func scanExecutionSession(row pgx.Row) (ExecutionSession, error) {
	var session ExecutionSession
	err := row.Scan(
		&session.ID, &session.ActivityID, &session.PlanBlockID, &session.Role, &session.PrimarySessionID, &session.StartedAt,
		&session.EndedAt, &session.State, &session.NextActionSnapshot, &session.ResumeMarker,
		&session.CreatedAt, &session.UpdatedAt)
	return session, err
}

func ExecutionSessionStateAt(session ExecutionSession, at time.Time) (string, bool) {
	if session.StartedAt.After(at) || (session.EndedAt != nil && session.EndedAt.Before(at)) {
		return "", false
	}
	if len(session.Events) == 0 {
		return session.State, true
	}
	state := executionStateActive
	lastRecoveryEventType := ""
	lastEventAt := session.StartedAt
	for _, event := range session.Events {
		if event.EventType == executionEventStarted {
			continue
		}
		if event.OccurredAt.Before(lastEventAt) && !event.OccurredAt.After(at) {
			return "unknown", true
		}
		if event.OccurredAt.After(lastEventAt) {
			lastEventAt = event.OccurredAt
		}
		if event.OccurredAt.After(at) {
			continue
		}
		nextState, _, err := nextExecutionStateWithHistory(state, lastRecoveryEventType, event.EventType)
		if err != nil {
			return "unknown", true
		}
		state = nextState
		if event.EventType == executionEventOfflineRecoveryStarted || event.EventType == executionEventOfflineRecoveryEnded {
			lastRecoveryEventType = event.EventType
		}
	}
	return state, true
}

func nextExecutionState(currentState, eventType string) (string, bool, error) {
	return nextExecutionStateWithHistory(currentState, "", eventType)
}

func nextExecutionStateWithHistory(currentState, lastEventType, eventType string) (string, bool, error) {
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
	case executionEventExternalReady:
		if currentState != executionStateWaitingExternal {
			return "", false, fmt.Errorf("%w: external_ready cannot transition from %s", ErrInvalidExecutionTransition, currentState)
		}
		return executionStateWaitingExternal, false, nil
	case executionEventResumed:
		if currentState != executionStateWaitingExternal && currentState != executionStateInterrupted {
			return "", false, fmt.Errorf("%w: resumed cannot transition from %s", ErrInvalidExecutionTransition, currentState)
		}
		return executionStateActive, false, nil
	case executionEventStuckUnknownNextAction, executionEventStuckEmotionalOverwhelm,
		executionEventOverwhelmed, executionEventOwnerExplanation:
		return currentState, false, nil
	case executionEventOfflineRecoveryStarted:
		return executionStateInterrupted, false, nil
	case executionEventOfflineRecoveryEnded:
		if currentState != executionStateInterrupted || lastEventType != executionEventOfflineRecoveryStarted {
			return "", false, fmt.Errorf("%w: offline recovery must end after it starts", ErrInvalidExecutionTransition)
		}
		return executionStateInterrupted, false, nil
	case executionEventCompleted:
		return executionStateCompleted, true, nil
	case executionEventAbandoned:
		return executionStateAbandoned, true, nil
	default:
		return "", false, fmt.Errorf("%w: unknown event %s", ErrInvalidExecutionTransition, eventType)
	}
}
