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

const planChangeProposalColumns = `
	id, plan_date::text, base_plan_version_id, origin, reason, request, proposed_plan, changes, warnings,
	content_hash, status, created_at, decided_at, decision_note, decision_provenance, result_plan_version_id`

func scanPlanChangeProposal(row pgx.Row) (PlanChangeProposal, error) {
	var proposal PlanChangeProposal
	var request, plan, changes, warnings, provenance []byte
	err := row.Scan(&proposal.ID, &proposal.PlanDate, &proposal.BasePlanVersionID, &proposal.Origin, &proposal.Reason,
		&request, &plan, &changes, &warnings, &proposal.ContentHash, &proposal.Status, &proposal.CreatedAt,
		&proposal.DecidedAt, &proposal.DecisionNote, &provenance, &proposal.ResultPlanVersionID)
	if err != nil {
		return PlanChangeProposal{}, err
	}
	proposal.Request = json.RawMessage(request)
	if len(provenance) > 0 {
		proposal.DecisionProvenance = json.RawMessage(provenance)
	}
	if err := json.Unmarshal(plan, &proposal.ProposedPlan); err != nil {
		return PlanChangeProposal{}, err
	}
	if err := json.Unmarshal(changes, &proposal.Changes); err != nil {
		return PlanChangeProposal{}, err
	}
	if err := json.Unmarshal(warnings, &proposal.Warnings); err != nil {
		return PlanChangeProposal{}, err
	}
	return proposal, nil
}

// CreatePlanChangeProposal prepares and retains a pending proposal. It never
// changes the plan, execution sessions or activities.
func (p *Postgres) CreatePlanChangeProposal(ctx context.Context, planDate time.Time, request PlanChangeRequest) (PlanChangeProposal, error) {
	if len(request.Evidence) > maxPlanningDetails {
		return PlanChangeProposal{}, fmt.Errorf("%w: evidence must be at most %d bytes", ErrInvalidProposal, maxPlanningDetails)
	}
	base, err := p.LatestPlan(ctx, planDate)
	if err != nil {
		return PlanChangeProposal{}, err
	}
	if base == nil {
		return PlanChangeProposal{}, ErrNotFound
	}
	origin, plan, changes, err := PreparePlanChange(*base, request)
	if err != nil {
		return PlanChangeProposal{}, err
	}
	if err := p.resolveActivityTitles(ctx, plan.Blocks); err != nil {
		return PlanChangeProposal{}, fmt.Errorf("%w: %s", ErrInvalidProposal, err)
	}
	warnings, err := p.proposalWarnings(ctx, *base, changes)
	if err != nil {
		return PlanChangeProposal{}, err
	}
	hash, err := ProposalContentHash(base.PlanDate, base.VersionID, plan)
	if err != nil {
		return PlanChangeProposal{}, err
	}
	request.Reason = strings.TrimSpace(request.Reason)
	encodedRequest, err := json.Marshal(request)
	if err != nil {
		return PlanChangeProposal{}, err
	}
	encodedPlan, err := json.Marshal(plan)
	if err != nil {
		return PlanChangeProposal{}, err
	}
	encodedChanges, err := json.Marshal(changes)
	if err != nil {
		return PlanChangeProposal{}, err
	}
	encodedWarnings, err := json.Marshal(warnings)
	if err != nil {
		return PlanChangeProposal{}, err
	}
	return scanPlanChangeProposal(p.pool.QueryRow(ctx, `
		INSERT INTO plan_change_proposals (plan_date, base_plan_version_id, origin, reason, request, proposed_plan, changes, warnings, content_hash)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb, $7::jsonb, $8::jsonb, $9)
		RETURNING `+planChangeProposalColumns,
		planDate, base.VersionID, origin, request.Reason, string(encodedRequest), string(encodedPlan), string(encodedChanges), string(encodedWarnings), hash))
}

func (p *Postgres) proposalWarnings(ctx context.Context, base Plan, changes []PlanChange) ([]ProposalWarning, error) {
	blockIDs := make([]int64, 0, len(base.Blocks))
	for _, block := range base.Blocks {
		blockIDs = append(blockIDs, block.ID)
	}
	sessions := []ExecutionSession{}
	activities := map[int64]Activity{}
	if len(blockIDs) > 0 {
		rows, err := p.pool.Query(ctx, `
			SELECT s.id, s.activity_id, s.plan_block_id, NULLIF(s.resume_marker, ''), a.resume_marker
			FROM execution_sessions s JOIN activities a ON a.id = s.activity_id
			WHERE s.ended_at IS NULL AND s.plan_block_id = ANY($1)
			ORDER BY s.id`, blockIDs)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var session ExecutionSession
			var activityMarker string
			if err := rows.Scan(&session.ID, &session.ActivityID, &session.PlanBlockID, &session.ResumeMarker, &activityMarker); err != nil {
				rows.Close()
				return nil, err
			}
			sessions = append(sessions, session)
			activities[session.ActivityID] = Activity{ID: session.ActivityID, ResumeMarker: activityMarker}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return ResumeWarnings(changes, sessions, activities), nil
}

func (p *Postgres) GetPlanChangeProposal(ctx context.Context, planDate time.Time, id int64) (PlanChangeProposal, error) {
	proposal, err := scanPlanChangeProposal(p.pool.QueryRow(ctx, `SELECT `+planChangeProposalColumns+`
		FROM plan_change_proposals WHERE id = $1 AND plan_date = $2`, id, planDate))
	if errors.Is(err, pgx.ErrNoRows) {
		return PlanChangeProposal{}, ErrNotFound
	}
	return proposal, err
}

func (p *Postgres) ListPlanChangeProposals(ctx context.Context, planDate time.Time, limit int) ([]PlanChangeProposal, error) {
	if limit <= 0 || limit > maxProposalListLimit {
		return nil, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidProposal, maxProposalListLimit)
	}
	rows, err := p.pool.Query(ctx, `SELECT `+planChangeProposalColumns+`
		FROM plan_change_proposals WHERE plan_date = $1 ORDER BY id DESC LIMIT $2`, planDate, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	proposals := []PlanChangeProposal{}
	for rows.Next() {
		proposal, err := scanPlanChangeProposal(rows)
		if err != nil {
			return nil, err
		}
		proposals = append(proposals, proposal)
	}
	return proposals, rows.Err()
}

// AcceptPlanChangeProposal appends the reviewed plan as a new version. The
// caller must echo the reviewed content hash. Accepting again with the same
// hash returns the recorded result without saving another version. If the
// date has a newer version than the proposal's base, the proposal becomes
// stale and nothing is saved.
func (p *Postgres) AcceptPlanChangeProposal(ctx context.Context, planDate time.Time, id int64, decision ProposalDecision, provenance json.RawMessage) (PlanChangeProposal, error) {
	if strings.TrimSpace(decision.ContentHash) == "" {
		return PlanChangeProposal{}, fmt.Errorf("%w: content_hash of the reviewed proposal is required", ErrInvalidProposal)
	}
	if len([]rune(decision.Note)) > maxPlanningTextRunes {
		return PlanChangeProposal{}, fmt.Errorf("%w: note must be at most %d characters", ErrInvalidProposal, maxPlanningTextRunes)
	}
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return PlanChangeProposal{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockPlanDate(ctx, tx, planDate); err != nil {
		return PlanChangeProposal{}, err
	}
	proposal, err := scanPlanChangeProposal(tx.QueryRow(ctx, `SELECT `+planChangeProposalColumns+`
		FROM plan_change_proposals WHERE id = $1 AND plan_date = $2 FOR UPDATE`, id, planDate))
	if errors.Is(err, pgx.ErrNoRows) {
		return PlanChangeProposal{}, ErrNotFound
	}
	if err != nil {
		return PlanChangeProposal{}, err
	}
	if decision.ContentHash != proposal.ContentHash {
		return proposal, fmt.Errorf("%w: content_hash does not match this proposal", ErrProposalConflict)
	}
	switch proposal.Status {
	case ProposalAccepted:
		return proposal, nil
	case ProposalRejected:
		return proposal, fmt.Errorf("%w: proposal was rejected", ErrProposalConflict)
	case ProposalStale:
		return proposal, ErrProposalStale
	}
	var latestID int64
	if err := tx.QueryRow(ctx, `
		SELECT id FROM plan_versions WHERE plan_date = $1
		ORDER BY created_at DESC, id DESC LIMIT 1`, planDate).Scan(&latestID); err != nil {
		return PlanChangeProposal{}, err
	}
	if latestID != proposal.BasePlanVersionID {
		proposal, err = scanPlanChangeProposal(tx.QueryRow(ctx, `
			UPDATE plan_change_proposals SET status = $2, decided_at = now(), decision_note = 'base plan version was superseded'
			WHERE id = $1 RETURNING `+planChangeProposalColumns, id, ProposalStale))
		if err != nil {
			return PlanChangeProposal{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return PlanChangeProposal{}, err
		}
		return proposal, ErrProposalStale
	}
	plan, err := insertPlanVersion(ctx, tx, planDate, proposal.ProposedPlan.Note, proposal.ProposedPlan.Blocks)
	if err != nil {
		return PlanChangeProposal{}, err
	}
	proposal, err = scanPlanChangeProposal(tx.QueryRow(ctx, `
		UPDATE plan_change_proposals
		SET status = $2, decided_at = now(), decision_note = $3, decision_provenance = $4::jsonb, result_plan_version_id = $5
		WHERE id = $1 RETURNING `+planChangeProposalColumns,
		id, ProposalAccepted, decision.Note, string(provenance), plan.VersionID))
	if err != nil {
		return PlanChangeProposal{}, err
	}
	return proposal, tx.Commit(ctx)
}

// RejectPlanChangeProposal leaves the plan unchanged. Rejecting an already
// rejected proposal returns it unchanged; accepted or stale proposals cannot
// be rejected.
func (p *Postgres) RejectPlanChangeProposal(ctx context.Context, planDate time.Time, id int64, decision ProposalDecision, provenance json.RawMessage) (PlanChangeProposal, error) {
	if len([]rune(decision.Note)) > maxPlanningTextRunes {
		return PlanChangeProposal{}, fmt.Errorf("%w: note must be at most %d characters", ErrInvalidProposal, maxPlanningTextRunes)
	}
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return PlanChangeProposal{}, err
	}
	defer tx.Rollback(ctx)
	proposal, err := scanPlanChangeProposal(tx.QueryRow(ctx, `SELECT `+planChangeProposalColumns+`
		FROM plan_change_proposals WHERE id = $1 AND plan_date = $2 FOR UPDATE`, id, planDate))
	if errors.Is(err, pgx.ErrNoRows) {
		return PlanChangeProposal{}, ErrNotFound
	}
	if err != nil {
		return PlanChangeProposal{}, err
	}
	switch proposal.Status {
	case ProposalRejected:
		return proposal, nil
	case ProposalAccepted, ProposalStale:
		return proposal, fmt.Errorf("%w: a %s proposal cannot be rejected", ErrProposalConflict, proposal.Status)
	}
	proposal, err = scanPlanChangeProposal(tx.QueryRow(ctx, `
		UPDATE plan_change_proposals SET status = $2, decided_at = now(), decision_note = $3, decision_provenance = $4::jsonb
		WHERE id = $1 RETURNING `+planChangeProposalColumns, id, ProposalRejected, decision.Note, string(provenance)))
	if err != nil {
		return PlanChangeProposal{}, err
	}
	return proposal, tx.Commit(ctx)
}
