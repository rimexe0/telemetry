package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rimexe0/telemetry/internal/activity"
)

type ActivityLabel struct {
	ID         int64      `json:"id"`
	Kind       string     `json:"kind"`
	Match      string     `json:"match"`
	Label      string     `json:"label"`
	ProposalID *int64     `json:"proposal_id,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	RemovedAt  *time.Time `json:"removed_at,omitempty"`
}

type LabelProposal struct {
	ID           int64                  `json:"id"`
	Changes      []activity.LabelChange `json:"changes"`
	Reason       string                 `json:"reason"`
	ContentHash  string                 `json:"content_hash"`
	Status       string                 `json:"status"`
	CreatedAt    time.Time              `json:"created_at"`
	DecidedAt    *time.Time             `json:"decided_at,omitempty"`
	DecisionNote string                 `json:"decision_note,omitempty"`
}

const maxLabelChanges = 50

func labelProposalHash(changes []activity.LabelChange, reason string) (string, error) {
	encoded, err := json.Marshal(struct {
		Changes []activity.LabelChange `json:"changes"`
		Reason  string                 `json:"reason"`
	}{changes, reason})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// CreateLabelProposal stores a pending proposal. It changes no label.
func (p *Postgres) CreateLabelProposal(ctx context.Context, changes []activity.LabelChange, reason string) (LabelProposal, error) {
	reason = strings.TrimSpace(reason)
	if len(changes) == 0 || len(changes) > maxLabelChanges {
		return LabelProposal{}, fmt.Errorf("%w: propose 1 to %d changes", ErrInvalidProposal, maxLabelChanges)
	}
	if reason == "" || len(reason) > 1000 {
		return LabelProposal{}, fmt.Errorf("%w: reason must be 1 to 1000 characters", ErrInvalidProposal)
	}
	seen := map[string]bool{}
	for i := range changes {
		if err := changes[i].Normalize(); err != nil {
			return LabelProposal{}, fmt.Errorf("%w: change %d: %v", ErrInvalidProposal, i+1, err)
		}
		key := changes[i].Kind + "\x00" + strings.ToLower(changes[i].Match)
		if seen[key] {
			return LabelProposal{}, fmt.Errorf("%w: %s %q appears twice", ErrInvalidProposal, changes[i].Kind, changes[i].Match)
		}
		seen[key] = true
	}
	hash, err := labelProposalHash(changes, reason)
	if err != nil {
		return LabelProposal{}, err
	}
	encoded, err := json.Marshal(changes)
	if err != nil {
		return LabelProposal{}, err
	}
	proposal := LabelProposal{Changes: changes, Reason: reason, ContentHash: hash, Status: "pending"}
	err = p.pool.QueryRow(ctx, `
		INSERT INTO activity_label_proposals (changes, reason, content_hash) VALUES ($1, $2, $3)
		RETURNING id, created_at`, encoded, reason, hash).Scan(&proposal.ID, &proposal.CreatedAt)
	return proposal, err
}

const labelProposalColumns = `id, changes, reason, content_hash, status, created_at, decided_at, decision_note`

func scanLabelProposal(row pgx.Row) (LabelProposal, error) {
	var proposal LabelProposal
	var changes []byte
	if err := row.Scan(&proposal.ID, &changes, &proposal.Reason, &proposal.ContentHash, &proposal.Status, &proposal.CreatedAt, &proposal.DecidedAt, &proposal.DecisionNote); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return proposal, ErrNotFound
		}
		return proposal, err
	}
	return proposal, json.Unmarshal(changes, &proposal.Changes)
}

// ListLabelProposals returns proposals, newest first. An empty status lists
// all of them.
func (p *Postgres) ListLabelProposals(ctx context.Context, status string, limit int) ([]LabelProposal, error) {
	if limit < 1 || limit > 200 {
		return nil, errors.New("limit must be between 1 and 200")
	}
	rows, err := p.pool.Query(ctx, `SELECT `+labelProposalColumns+` FROM activity_label_proposals
		WHERE $1 = '' OR status = $1 ORDER BY id DESC LIMIT $2`, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []LabelProposal{}
	for rows.Next() {
		proposal, err := scanLabelProposal(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, proposal)
	}
	return result, rows.Err()
}

// DecideLabelProposal accepts or rejects a pending proposal. Accepting needs
// the hash of the content the owner reviewed and applies every change.
func (p *Postgres) DecideLabelProposal(ctx context.Context, id int64, accept bool, decision ProposalDecision, provenance json.RawMessage) (LabelProposal, error) {
	if strings.TrimSpace(decision.ContentHash) == "" {
		return LabelProposal{}, fmt.Errorf("%w: content_hash of the reviewed proposal is required", ErrInvalidProposal)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return LabelProposal{}, err
	}
	defer tx.Rollback(ctx)
	proposal, err := scanLabelProposal(tx.QueryRow(ctx, `SELECT `+labelProposalColumns+` FROM activity_label_proposals WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		return proposal, err
	}
	if proposal.Status != "pending" {
		return proposal, fmt.Errorf("%w: proposal is already %s", ErrProposalConflict, proposal.Status)
	}
	if decision.ContentHash != proposal.ContentHash {
		return proposal, fmt.Errorf("%w: content_hash does not match this proposal", ErrProposalConflict)
	}
	status := "rejected"
	if accept {
		status = "accepted"
		for _, change := range proposal.Changes {
			if _, err := tx.Exec(ctx, `UPDATE activity_labels SET removed_at = now()
				WHERE kind = $1 AND lower(match) = lower($2) AND removed_at IS NULL`, change.Kind, change.Match); err != nil {
				return proposal, err
			}
			if change.Action == "set" {
				if _, err := tx.Exec(ctx, `INSERT INTO activity_labels (kind, match, label, proposal_id) VALUES ($1, $2, $3, $4)`,
					change.Kind, change.Match, change.Label, proposal.ID); err != nil {
					return proposal, err
				}
			}
		}
	}
	decided, err := scanLabelProposal(tx.QueryRow(ctx, `UPDATE activity_label_proposals
		SET status = $2, decided_at = now(), decision_note = $3, decision_provenance = $4
		WHERE id = $1 RETURNING `+labelProposalColumns, id, status, strings.TrimSpace(decision.Note), provenance))
	if err != nil {
		return proposal, err
	}
	return decided, tx.Commit(ctx)
}

// ListActivityLabels returns the labels in force.
func (p *Postgres) ListActivityLabels(ctx context.Context) ([]ActivityLabel, error) {
	rows, err := p.pool.Query(ctx, `SELECT id, kind, match, label, proposal_id, created_at, removed_at
		FROM activity_labels WHERE removed_at IS NULL ORDER BY label, kind, match`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ActivityLabel{}
	for rows.Next() {
		var label ActivityLabel
		if err := rows.Scan(&label.ID, &label.Kind, &label.Match, &label.Label, &label.ProposalID, &label.CreatedAt, &label.RemovedAt); err != nil {
			return nil, err
		}
		result = append(result, label)
	}
	return result, rows.Err()
}

// RemoveActivityLabel is an owner action; the label's history is kept.
func (p *Postgres) RemoveActivityLabel(ctx context.Context, id int64) error {
	tag, err := p.pool.Exec(ctx, `UPDATE activity_labels SET removed_at = now() WHERE id = $1 AND removed_at IS NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// LabelsForSummary converts stored labels for activity summaries.
func LabelsForSummary(labels []ActivityLabel) []activity.Label {
	result := make([]activity.Label, 0, len(labels))
	for _, label := range labels {
		result = append(result, activity.Label{Kind: label.Kind, Match: label.Match, Label: label.Label})
	}
	return result
}
