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
	"github.com/rimexe0/telemetry/internal/policy"
)

type PolicyProposal struct {
	ID                    int64                    `json:"id"`
	Changes               []policy.BlocklistChange `json:"changes"`
	Reason                string                   `json:"reason"`
	ContentHash           string                   `json:"content_hash"`
	Status                string                   `json:"status"`
	CreatedAt             time.Time                `json:"created_at"`
	DecidedAt             *time.Time               `json:"decided_at,omitempty"`
	ResultPolicyVersionID *int64                   `json:"result_policy_version_id,omitempty"`
}

// ErrInvalidPolicyProposal reports a proposal that cannot be stored.
var ErrInvalidPolicyProposal = errors.New("invalid blocklist proposal")

// CreatePolicyProposal stores pending blocklist changes. It changes no policy.
func (p *Postgres) CreatePolicyProposal(ctx context.Context, changes []policy.BlocklistChange, reason string) (PolicyProposal, error) {
	reason = strings.TrimSpace(reason)
	if len(changes) == 0 || len(changes) > 50 {
		return PolicyProposal{}, fmt.Errorf("%w: propose 1 to 50 changes", ErrInvalidPolicyProposal)
	}
	if reason == "" || len(reason) > 1000 {
		return PolicyProposal{}, fmt.Errorf("%w: reason must be 1 to 1000 characters", ErrInvalidPolicyProposal)
	}
	seen := map[string]bool{}
	for i := range changes {
		if err := changes[i].Normalize(); err != nil {
			return PolicyProposal{}, fmt.Errorf("%w: change %d: %v", ErrInvalidPolicyProposal, i+1, err)
		}
		if seen[changes[i].AndroidPackage] {
			return PolicyProposal{}, fmt.Errorf("%w: %s appears twice", ErrInvalidPolicyProposal, changes[i].AndroidPackage)
		}
		seen[changes[i].AndroidPackage] = true
	}
	encoded, err := json.Marshal(changes)
	if err != nil {
		return PolicyProposal{}, err
	}
	digest := sha256.Sum256(append(encoded, []byte("\x00"+reason)...))
	proposal := PolicyProposal{Changes: changes, Reason: reason, ContentHash: hex.EncodeToString(digest[:]), Status: "pending"}
	err = p.pool.QueryRow(ctx, `INSERT INTO policy_proposals (changes, reason, content_hash) VALUES ($1, $2, $3) RETURNING id, created_at`,
		encoded, reason, proposal.ContentHash).Scan(&proposal.ID, &proposal.CreatedAt)
	return proposal, err
}

const policyProposalColumns = `id, changes, reason, content_hash, status, created_at, decided_at, result_policy_version_id`

func scanPolicyProposal(row pgx.Row) (PolicyProposal, error) {
	var proposal PolicyProposal
	var changes []byte
	if err := row.Scan(&proposal.ID, &changes, &proposal.Reason, &proposal.ContentHash, &proposal.Status, &proposal.CreatedAt, &proposal.DecidedAt, &proposal.ResultPolicyVersionID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return proposal, ErrNotFound
		}
		return proposal, err
	}
	return proposal, json.Unmarshal(changes, &proposal.Changes)
}

func (p *Postgres) ListPolicyProposals(ctx context.Context, status string, limit int) ([]PolicyProposal, error) {
	if limit < 1 || limit > 200 {
		return nil, errors.New("limit must be between 1 and 200")
	}
	rows, err := p.pool.Query(ctx, `SELECT `+policyProposalColumns+` FROM policy_proposals WHERE $1 = '' OR status = $1 ORDER BY id DESC LIMIT $2`, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []PolicyProposal{}
	for rows.Next() {
		proposal, err := scanPolicyProposal(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, proposal)
	}
	return result, rows.Err()
}

// DecidePolicyProposal accepts or rejects a pending proposal. Accepting
// applies its changes to the current policy and appends a new version.
func (p *Postgres) DecidePolicyProposal(ctx context.Context, id int64, accept bool, decision ProposalDecision, provenance json.RawMessage) (PolicyProposal, error) {
	if strings.TrimSpace(decision.ContentHash) == "" {
		return PolicyProposal{}, fmt.Errorf("%w: content_hash of the reviewed proposal is required", ErrInvalidProposal)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return PolicyProposal{}, err
	}
	defer tx.Rollback(ctx)
	proposal, err := scanPolicyProposal(tx.QueryRow(ctx, `SELECT `+policyProposalColumns+` FROM policy_proposals WHERE id = $1 FOR UPDATE`, id))
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
	var result *int64
	if accept {
		status = "accepted"
		// Serialize policy writes so the edit applies to the version in force.
		if _, err := tx.Exec(ctx, `LOCK TABLE policy_versions IN SHARE ROW EXCLUSIVE MODE`); err != nil {
			return proposal, err
		}
		config := policy.Config{}
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT config FROM policy_versions ORDER BY id DESC LIMIT 1`).Scan(&raw)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return proposal, err
		}
		if raw != nil {
			if err := json.Unmarshal(raw, &config); err != nil {
				return proposal, err
			}
		}
		next := policy.ApplyBlocklist(config, proposal.Changes)
		if err := next.Normalize(); err != nil {
			return proposal, fmt.Errorf("%w: %s", ErrInvalidPolicy, err)
		}
		encoded, err := json.Marshal(next)
		if err != nil {
			return proposal, err
		}
		var version int64
		note := fmt.Sprintf("Blocklist proposal %d: %s", proposal.ID, proposal.Reason)
		if len([]rune(note)) > maxPlanningTextRunes {
			note = string([]rune(note)[:maxPlanningTextRunes])
		}
		if err := tx.QueryRow(ctx, `INSERT INTO policy_versions (config, note) VALUES ($1::jsonb, $2) RETURNING id`, string(encoded), note).Scan(&version); err != nil {
			return proposal, err
		}
		result = &version
	}
	decided, err := scanPolicyProposal(tx.QueryRow(ctx, `UPDATE policy_proposals
		SET status = $2, decided_at = now(), decision_note = $3, decision_provenance = $4, result_policy_version_id = $5
		WHERE id = $1 RETURNING `+policyProposalColumns, id, status, strings.TrimSpace(decision.Note), provenance, result))
	if err != nil {
		return proposal, err
	}
	return decided, tx.Commit(ctx)
}
