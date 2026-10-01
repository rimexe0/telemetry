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

// Planning records are owner-authored intent. They never overwrite history:
// saving a plan appends a new version, and earlier versions stay readable.
var ErrNotFound = errors.New("record not found")

const (
	activityStatusActive    = "active"
	activityStatusWaiting   = "waiting"
	activityStatusBlocked   = "blocked"
	activityStatusDone      = "done"
	activityStatusCompleted = "completed"
	activityStatusAbandoned = "abandoned"
	activityStatusUnknown   = "unknown"
)

var activityStatuses = map[string]struct{}{
	activityStatusActive:    {},
	activityStatusWaiting:   {},
	activityStatusBlocked:   {},
	activityStatusDone:      {},
	activityStatusCompleted: {},
	activityStatusAbandoned: {},
	activityStatusUnknown:   {},
}

var planBlockStates = map[string]struct{}{
	"planned":  {},
	"active":   {},
	"done":     {},
	"skipped":  {},
	"switched": {},
	"blocked":  {},
	"unknown":  {},
}

var activityStates = map[string]struct{}{
	"growth":      {},
	"maintenance": {},
	"parked":      {},
}

var planBlockExplanations = map[string]struct{}{
	"":                   {},
	"distracted":         {},
	"blocked":            {},
	"urgent":             {},
	"intentional_switch": {},
	"unrealistic_plan":   {},
	"finished_early":     {},
	"other":              {},
}

var planBlockActions = map[string]struct{}{
	"planned":     {},
	"state":       {},
	"explanation": {},
	"return":      {},
}

const (
	maxPlanningTextRunes = 500
	maxPlanningBlocks    = 200
	maxPlanningDetails   = 8 << 10
)

type Activity struct {
	ID                    int64           `json:"id"`
	Title                 string          `json:"title"`
	Notes                 string          `json:"notes"`
	NextAction            string          `json:"next_action"`
	ResumeMarker          string          `json:"resume_marker"`
	Status                string          `json:"status"`
	ActivityState         string          `json:"activity_state"`
	Priority              int             `json:"priority"`
	Deadline              *time.Time      `json:"deadline,omitempty"`
	EffortEstimateMinutes *int            `json:"effort_estimate_minutes,omitempty"`
	Details               json.RawMessage `json:"details,omitempty"`
	CreatedAt             time.Time       `json:"created_at"`
	UpdatedAt             time.Time       `json:"updated_at"`
}

type ActivityFilter struct {
	Status string
	Limit  int
}

type PlanBlock struct {
	ID                    int64           `json:"id"`
	ActivityID            *int64          `json:"activity_id,omitempty"`
	SourceBlockID         *int64          `json:"source_block_id,omitempty"`
	Title                 string          `json:"title"`
	StartTime             time.Time       `json:"start_time"`
	DurationMinutes       int             `json:"duration_minutes"`
	Fixed                 bool            `json:"fixed"`
	State                 string          `json:"state"`
	StateNote             string          `json:"state_note"`
	ExplanationCode       string          `json:"explanation_code,omitempty"`
	ExplanationNote       string          `json:"explanation_note,omitempty"`
	ExplanationProvenance json.RawMessage `json:"explanation_provenance,omitempty"`
	LastChangedAt         *time.Time      `json:"last_changed_at,omitempty"`
	Position              int             `json:"position"`
}

type Plan struct {
	PlanDate  string      `json:"plan_date"`
	VersionID int64       `json:"version_id"`
	CreatedAt time.Time   `json:"created_at"`
	Note      string      `json:"note"`
	Blocks    []PlanBlock `json:"blocks"`
}

type PlanInput struct {
	Note   string          `json:"note"`
	Blocks []PlanBlockSpec `json:"blocks"`
}

type PlanBlockSpec struct {
	ActivityID      *int64    `json:"activity_id,omitempty"`
	Title           string    `json:"title"`
	StartTime       time.Time `json:"start_time"`
	DurationMinutes int       `json:"duration_minutes"`
	Fixed           bool      `json:"fixed"`
}

type PlanVersionSummary struct {
	VersionID  int64     `json:"version_id"`
	PlanDate   string    `json:"plan_date"`
	CreatedAt  time.Time `json:"created_at"`
	Note       string    `json:"note"`
	BlockCount int       `json:"block_count"`
}

type PlanBlockEvent struct {
	ID              int64           `json:"id"`
	PlanBlockID     int64           `json:"plan_block_id"`
	State           string          `json:"state"`
	Action          string          `json:"action"`
	ExplanationCode string          `json:"explanation_code,omitempty"`
	Note            string          `json:"note,omitempty"`
	Provenance      json.RawMessage `json:"provenance"`
	CreatedAt       time.Time       `json:"created_at"`
}

type PlanningSettings struct {
	DailyPlanGateEnabled bool      `json:"daily_plan_gate_enabled"`
	UpdatedAt            time.Time `json:"updated_at,omitempty"`
}

func validateActivity(activity *Activity) error {
	activity.Title = strings.TrimSpace(activity.Title)
	if activity.Title == "" {
		return errors.New("title is required")
	}
	if len([]rune(activity.Title)) > maxPlanningTextRunes {
		return fmt.Errorf("title must be at most %d characters", maxPlanningTextRunes)
	}
	if len([]rune(activity.Notes)) > maxPlanningTextRunes {
		return fmt.Errorf("notes must be at most %d characters", maxPlanningTextRunes)
	}
	if len([]rune(activity.NextAction)) > maxPlanningTextRunes {
		return fmt.Errorf("next_action must be at most %d characters", maxPlanningTextRunes)
	}
	if len([]rune(activity.ResumeMarker)) > maxPlanningTextRunes {
		return fmt.Errorf("resume_marker must be at most %d characters", maxPlanningTextRunes)
	}
	if activity.Status == "" {
		activity.Status = activityStatusActive
	}
	if _, ok := activityStatuses[activity.Status]; !ok {
		return errors.New("status must be one of active, waiting, blocked, done, completed, abandoned, unknown")
	}
	if activity.ActivityState == "" {
		activity.ActivityState = "growth"
	}
	if _, ok := activityStates[activity.ActivityState]; !ok {
		return errors.New("activity_state must be one of growth, maintenance, parked")
	}
	if activity.Priority < -1000 || activity.Priority > 1000 {
		return errors.New("priority must be between -1000 and 1000")
	}
	if activity.EffortEstimateMinutes != nil && (*activity.EffortEstimateMinutes < 1 || *activity.EffortEstimateMinutes > 7*1440) {
		return errors.New("effort_estimate_minutes must be between 1 and 10080")
	}
	return validateActivityDetails(activity.Details)
}

func validateActivityDetails(details json.RawMessage) error {
	if len(details) == 0 {
		return nil
	}
	if !json.Valid(details) {
		return errors.New("details must be valid JSON")
	}
	trimmed := strings.TrimSpace(string(details))
	if trimmed != "{}" && (!strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}")) {
		return errors.New("details must be a JSON object")
	}
	if len(details) > maxPlanningDetails {
		return fmt.Errorf("details must be at most %d bytes", maxPlanningDetails)
	}
	return nil
}

func (p *Postgres) CreateActivity(ctx context.Context, activity Activity) (Activity, error) {
	if err := validateActivity(&activity); err != nil {
		return Activity{}, err
	}
	row := p.pool.QueryRow(ctx, `
		INSERT INTO activities (title, notes, next_action, resume_marker, status, activity_state, priority, deadline, effort_estimate_minutes, details)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, created_at, updated_at`,
		activity.Title, activity.Notes, activity.NextAction, activity.ResumeMarker, activity.Status, activity.ActivityState,
		activity.Priority, activity.Deadline, activity.EffortEstimateMinutes, nonEmptyJSON(activity.Details))
	if err := row.Scan(&activity.ID, &activity.CreatedAt, &activity.UpdatedAt); err != nil {
		return Activity{}, err
	}
	return activity, nil
}

func (p *Postgres) ListActivities(ctx context.Context, filter ActivityFilter) ([]Activity, error) {
	if filter.Limit <= 0 || filter.Limit > 500 {
		filter.Limit = 200
	}
	query := `SELECT id, title, notes, next_action, resume_marker, status, activity_state, priority, deadline, effort_estimate_minutes, details, created_at, updated_at FROM activities`
	args := []any{}
	if filter.Status != "" {
		if _, ok := activityStatuses[filter.Status]; !ok {
			return nil, errors.New("status must be one of active, waiting, blocked, done, completed, abandoned, unknown")
		}
		query += ` WHERE status = $1`
		args = append(args, filter.Status)
	}
	query += ` ORDER BY (status = 'active') DESC, priority DESC, updated_at DESC, id LIMIT $` + fmt.Sprint(len(args)+1)
	args = append(args, filter.Limit)
	rows, err := p.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	activities := []Activity{}
	for rows.Next() {
		activity, err := scanActivity(rows)
		if err != nil {
			return nil, err
		}
		activities = append(activities, activity)
	}
	return activities, rows.Err()
}

func (p *Postgres) UpdateActivity(ctx context.Context, activity Activity) (Activity, error) {
	if err := validateActivity(&activity); err != nil {
		return Activity{}, err
	}
	row := p.pool.QueryRow(ctx, `
		UPDATE activities SET title=$2, notes=$3, next_action=$4, resume_marker=$5, status=$6, activity_state=$7, priority=$8,
			deadline=$9, effort_estimate_minutes=$10, details=$11, updated_at=now()
		WHERE id=$1
		RETURNING created_at, updated_at`,
		activity.ID, activity.Title, activity.Notes, activity.NextAction, activity.ResumeMarker, activity.Status, activity.ActivityState,
		activity.Priority, activity.Deadline, activity.EffortEstimateMinutes, nonEmptyJSON(activity.Details))
	if err := row.Scan(&activity.CreatedAt, &activity.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Activity{}, ErrNotFound
		}
		return Activity{}, err
	}
	return activity, nil
}

func scanActivity(row pgx.Row) (Activity, error) {
	var activity Activity
	err := row.Scan(&activity.ID, &activity.Title, &activity.Notes, &activity.NextAction, &activity.ResumeMarker,
		&activity.Status, &activity.ActivityState, &activity.Priority, &activity.Deadline, &activity.EffortEstimateMinutes,
		&activity.Details, &activity.CreatedAt, &activity.UpdatedAt)
	return activity, err
}

func nonEmptyJSON(details json.RawMessage) json.RawMessage {
	if len(strings.TrimSpace(string(details))) == 0 {
		return []byte("{}")
	}
	return details
}

// LatestPlan returns the newest saved version for the date. A missing plan is
// not an error: the owner may not have planned that day.
func (p *Postgres) LatestPlan(ctx context.Context, planDate time.Time) (*Plan, error) {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var version Plan
	var versionID int64
	err = tx.QueryRow(ctx, `
		SELECT id, created_at, note FROM plan_versions
		WHERE plan_date = $1
		ORDER BY created_at DESC, id DESC LIMIT 1`, planDate).Scan(&versionID, &version.CreatedAt, &version.Note)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	version.VersionID = versionID
	version.PlanDate = planDate.Format("2006-01-02")
	blocks, err := p.planBlocks(ctx, tx, versionID)
	if err != nil {
		return nil, err
	}
	version.Blocks = blocks
	return &version, tx.Commit(ctx)
}

// PlanBlocksByIDs reads the exact retained plan blocks referenced by execution
// sessions, even after a newer plan version has been saved for the same date.
func (p *Postgres) PlanBlocksByIDs(ctx context.Context, blockIDs []int64) ([]PlanBlock, error) {
	blocks := make([]PlanBlock, 0, len(blockIDs))
	if len(blockIDs) == 0 {
		return blocks, nil
	}
	rows, err := p.pool.Query(ctx, `
		SELECT id, start_time, duration_minutes
		FROM plan_blocks WHERE id = ANY($1) ORDER BY id`, blockIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var block PlanBlock
		if err := rows.Scan(&block.ID, &block.StartTime, &block.DurationMinutes); err != nil {
			return nil, err
		}
		blocks = append(blocks, block)
	}
	return blocks, rows.Err()
}

func (p *Postgres) ListPlanVersions(ctx context.Context, planDate time.Time) ([]PlanVersionSummary, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT v.id, v.plan_date::text, v.created_at, v.note, COUNT(pb.id)::int
		FROM plan_versions v
		LEFT JOIN plan_blocks pb ON pb.plan_version_id = v.id
		WHERE v.plan_date = $1
		GROUP BY v.id
		ORDER BY v.created_at DESC, v.id DESC`, planDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := make([]PlanVersionSummary, 0)
	for rows.Next() {
		var version PlanVersionSummary
		var date string
		if err := rows.Scan(&version.VersionID, &date, &version.CreatedAt, &version.Note, &version.BlockCount); err != nil {
			return nil, err
		}
		version.PlanDate = date
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

// planBlocksFromInput validates a plan and resolves activity titles.
func (p *Postgres) planBlocksFromInput(ctx context.Context, input PlanInput) ([]ProposedBlock, error) {
	if len(input.Blocks) > maxPlanningBlocks {
		return nil, fmt.Errorf("a plan may contain at most %d blocks", maxPlanningBlocks)
	}
	if len([]rune(input.Note)) > maxPlanningTextRunes {
		return nil, fmt.Errorf("note must be at most %d characters", maxPlanningTextRunes)
	}
	blocks := make([]ProposedBlock, len(input.Blocks))
	for i, spec := range input.Blocks {
		spec.Title = strings.TrimSpace(spec.Title)
		if spec.ActivityID != nil && *spec.ActivityID <= 0 {
			return nil, errors.New("activity_id must reference an activity")
		}
		if spec.Title == "" && spec.ActivityID == nil {
			return nil, fmt.Errorf("block %d needs a title or an activity", i+1)
		}
		if len([]rune(spec.Title)) > maxPlanningTextRunes {
			return nil, fmt.Errorf("block %d title must be at most %d characters", i+1, maxPlanningTextRunes)
		}
		if spec.DurationMinutes < 1 || spec.DurationMinutes > 1440 {
			return nil, fmt.Errorf("block %d duration_minutes must be between 1 and 1440", i+1)
		}
		if spec.StartTime.IsZero() {
			return nil, fmt.Errorf("block %d start_time is required", i+1)
		}
		blocks[i] = ProposedBlock{ActivityID: spec.ActivityID, Title: spec.Title, StartTime: spec.StartTime, DurationMinutes: spec.DurationMinutes, Fixed: spec.Fixed, State: "planned"}
	}
	if err := p.resolveActivityTitles(ctx, blocks); err != nil {
		return nil, err
	}
	return blocks, nil
}

// SavePlan appends a new version for the date inside one transaction. The
// previous version and its block states remain retained.
func (p *Postgres) SavePlan(ctx context.Context, planDate time.Time, input PlanInput) (Plan, error) {
	blocks, err := p.planBlocksFromInput(ctx, input)
	if err != nil {
		return Plan{}, err
	}
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Plan{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockPlanDate(ctx, tx, planDate); err != nil {
		return Plan{}, err
	}
	plan, err := insertPlanVersion(ctx, tx, planDate, input.Note, blocks)
	if err != nil {
		return Plan{}, err
	}
	return plan, tx.Commit(ctx)
}

// resolveActivityTitles rejects unknown activities and fills empty block
// titles from the linked activity.
func (p *Postgres) resolveActivityTitles(ctx context.Context, blocks []ProposedBlock) error {
	activityIDs := make([]int64, 0, len(blocks))
	for _, block := range blocks {
		if block.ActivityID != nil {
			activityIDs = append(activityIDs, *block.ActivityID)
		}
	}
	if len(activityIDs) == 0 {
		return nil
	}
	titles := make(map[int64]string, len(activityIDs))
	rows, err := p.pool.Query(ctx, `SELECT id, title FROM activities WHERE id = ANY($1)`, activityIDs)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		var title string
		if err := rows.Scan(&id, &title); err != nil {
			rows.Close()
			return err
		}
		titles[id] = title
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for i, block := range blocks {
		if block.ActivityID == nil {
			continue
		}
		title, ok := titles[*block.ActivityID]
		if !ok {
			return fmt.Errorf("block %d references unknown activity %d", i+1, *block.ActivityID)
		}
		if block.Title == "" {
			blocks[i].Title = title
		}
	}
	return nil
}

// lockPlanDate serializes version appends for one date so a stale check and
// the following insert cannot interleave with another save.
func lockPlanDate(ctx context.Context, tx pgx.Tx, planDate time.Time) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('telemetry.plan_date'), hashtext($1::text))`, planDate.Format("2006-01-02"))
	return err
}

func insertPlanVersion(ctx context.Context, tx pgx.Tx, planDate time.Time, note string, specs []ProposedBlock) (Plan, error) {
	var versionID int64
	var createdAt time.Time
	err := tx.QueryRow(ctx, `
		INSERT INTO plan_versions (plan_date, note) VALUES ($1, $2) RETURNING id, created_at`,
		planDate, note).Scan(&versionID, &createdAt)
	if err != nil {
		return Plan{}, err
	}
	blocks := make([]PlanBlock, 0, len(specs))
	for i, spec := range specs {
		block := PlanBlock{
			ActivityID: spec.ActivityID, SourceBlockID: spec.SourceBlockID, Title: spec.Title, StartTime: spec.StartTime,
			DurationMinutes: spec.DurationMinutes, Fixed: spec.Fixed, State: spec.State, Position: i,
		}
		err := tx.QueryRow(ctx, `
			INSERT INTO plan_blocks (plan_version_id, activity_id, source_block_id, title, start_time, duration_minutes, fixed, state, state_note, position)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, '', $9) RETURNING id`,
			versionID, block.ActivityID, block.SourceBlockID, block.Title, block.StartTime, block.DurationMinutes, block.Fixed, block.State, block.Position).Scan(&block.ID)
		if err != nil {
			return Plan{}, err
		}
		blocks = append(blocks, block)
	}
	return Plan{
		PlanDate:  planDate.Format("2006-01-02"),
		VersionID: versionID,
		CreatedAt: createdAt,
		Note:      note,
		Blocks:    blocks,
	}, nil
}

// SetPlanBlockState records what actually happened to one planned block. The
// block must belong to the latest version of the supplied plan date, so editing
// an older snapshot cannot silently rewrite current intent. The event row keeps
// earlier owner explanations available after a later correction.
func (p *Postgres) SetPlanBlockState(ctx context.Context, planDate time.Time, blockID int64, state, stateNote, explanationCode, action string) (PlanBlock, error) {
	if _, ok := planBlockStates[state]; !ok {
		return PlanBlock{}, errors.New("state must be one of planned, active, done, skipped, switched, blocked, unknown")
	}
	if len([]rune(stateNote)) > maxPlanningTextRunes {
		return PlanBlock{}, fmt.Errorf("state_note must be at most %d characters", maxPlanningTextRunes)
	}
	if _, ok := planBlockExplanations[explanationCode]; !ok {
		return PlanBlock{}, errors.New("explanation_code is not recognized")
	}
	if action == "" {
		action = "state"
	}
	if _, ok := planBlockActions[action]; !ok {
		return PlanBlock{}, errors.New("action must be one of planned, state, explanation, return")
	}
	if action == "return" && state != "active" {
		return PlanBlock{}, errors.New("return action must set the block active")
	}
	if explanationCode != "" && action != "explanation" {
		action = "explanation"
	}
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return PlanBlock{}, err
	}
	defer tx.Rollback(ctx)
	row := tx.QueryRow(ctx, `
		WITH latest AS (
			SELECT v.id AS plan_version_id
			FROM plan_versions v
			WHERE v.plan_date = $2
			ORDER BY v.created_at DESC, v.id DESC LIMIT 1
		)
		UPDATE plan_blocks pb SET state = $3, state_note = $4
		WHERE pb.id = $1
			AND pb.plan_version_id = (SELECT plan_version_id FROM latest)
		RETURNING pb.id, pb.activity_id, pb.title, pb.start_time, pb.duration_minutes, pb.fixed, pb.state, pb.state_note, pb.position`,
		blockID, planDate, state, stateNote)
	var block PlanBlock
	err = row.Scan(&block.ID, &block.ActivityID, &block.Title, &block.StartTime,
		&block.DurationMinutes, &block.Fixed, &block.State, &block.StateNote, &block.Position)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlanBlock{}, ErrNotFound
	}
	if err != nil {
		return PlanBlock{}, err
	}
	provenance := json.RawMessage(`{"recorded_by":"owner","source":"panel"}`)
	var event PlanBlockEvent
	var provenanceValue []byte
	if err := tx.QueryRow(ctx, `
		INSERT INTO plan_block_events (plan_block_id, state, action, explanation_code, note, provenance)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb)
		RETURNING id, created_at, provenance`,
		block.ID, state, action, explanationCode, stateNote, string(provenance)).Scan(&event.ID, &event.CreatedAt, &provenanceValue); err != nil {
		return PlanBlock{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PlanBlock{}, err
	}
	event.PlanBlockID = block.ID
	event.State = state
	event.Action = action
	event.ExplanationCode = explanationCode
	event.Note = stateNote
	event.Provenance = json.RawMessage(provenanceValue)
	block.ExplanationCode = explanationCode
	block.ExplanationNote = stateNote
	block.ExplanationProvenance = event.Provenance
	block.LastChangedAt = &event.CreatedAt
	return block, nil
}

func (p *Postgres) planBlocks(ctx context.Context, tx pgx.Tx, versionID int64) ([]PlanBlock, error) {
	rows, err := tx.Query(ctx, `
		SELECT pb.id, pb.activity_id, pb.source_block_id, pb.title, pb.start_time, pb.duration_minutes, pb.fixed,
			pb.state, pb.state_note, pb.position,
			COALESCE(event.explanation_code, ''),
			COALESCE(event.note, pb.state_note),
			COALESCE(event.provenance, '{"recorded_by":"owner","source":"legacy_state"}'::jsonb),
			event.created_at
		FROM plan_blocks pb
		LEFT JOIN LATERAL (
			SELECT e.explanation_code, e.note, e.provenance, e.created_at
			FROM plan_block_events e
			WHERE e.plan_block_id = pb.id
			ORDER BY e.id DESC
			LIMIT 1
		) event ON true
		WHERE pb.plan_version_id = $1 ORDER BY pb.position, pb.id`, versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	blocks := []PlanBlock{}
	for rows.Next() {
		var block PlanBlock
		var provenance []byte
		if err := rows.Scan(&block.ID, &block.ActivityID, &block.SourceBlockID, &block.Title, &block.StartTime,
			&block.DurationMinutes, &block.Fixed, &block.State, &block.StateNote, &block.Position,
			&block.ExplanationCode, &block.ExplanationNote, &provenance, &block.LastChangedAt); err != nil {
			return nil, err
		}
		block.ExplanationProvenance = json.RawMessage(provenance)
		blocks = append(blocks, block)
	}
	return blocks, rows.Err()
}

func (p *Postgres) ListPlanBlockEvents(ctx context.Context, planDate time.Time, blockID int64) ([]PlanBlockEvent, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT e.id, e.plan_block_id, e.state, e.action, e.explanation_code, e.note, e.provenance, e.created_at
		FROM plan_block_events e
		JOIN plan_blocks pb ON pb.id = e.plan_block_id
		JOIN plan_versions v ON v.id = pb.plan_version_id
		WHERE e.plan_block_id = $1 AND v.plan_date = $2
		ORDER BY e.id`, blockID, planDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]PlanBlockEvent, 0)
	for rows.Next() {
		var event PlanBlockEvent
		var provenance []byte
		if err := rows.Scan(&event.ID, &event.PlanBlockID, &event.State, &event.Action,
			&event.ExplanationCode, &event.Note, &provenance, &event.CreatedAt); err != nil {
			return nil, err
		}
		event.Provenance = json.RawMessage(provenance)
		events = append(events, event)
	}
	return events, rows.Err()
}

func (p *Postgres) GetPlanningSettings(ctx context.Context) (PlanningSettings, error) {
	var settings PlanningSettings
	err := p.pool.QueryRow(ctx, `
		SELECT daily_plan_gate_enabled, updated_at
		FROM planning_settings WHERE id = 1`).Scan(&settings.DailyPlanGateEnabled, &settings.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlanningSettings{}, nil
	}
	return settings, err
}

func (p *Postgres) UpdatePlanningSettings(ctx context.Context, dailyPlanGateEnabled bool) (PlanningSettings, error) {
	var settings PlanningSettings
	err := p.pool.QueryRow(ctx, `
		INSERT INTO planning_settings (id, daily_plan_gate_enabled, updated_at)
		VALUES (1, $1, now())
		ON CONFLICT (id) DO UPDATE SET daily_plan_gate_enabled = EXCLUDED.daily_plan_gate_enabled, updated_at = now()
		RETURNING daily_plan_gate_enabled, updated_at`, dailyPlanGateEnabled).Scan(
		&settings.DailyPlanGateEnabled, &settings.UpdatedAt)
	return settings, err
}
