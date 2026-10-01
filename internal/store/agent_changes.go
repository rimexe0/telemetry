package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rimexe0/telemetry/internal/activity"
	"github.com/rimexe0/telemetry/internal/policy"
)

// AgentChange is a change an assistant applied after the owner approved it in
// conversation. Previous holds the replaced state so it can be reverted.
type AgentChange struct {
	ID              int64           `json:"id"`
	Kind            string          `json:"kind"`
	Changes         json.RawMessage `json:"changes"`
	Previous        json.RawMessage `json:"previous"`
	Reason          string          `json:"reason"`
	PolicyVersionID *int64          `json:"policy_version_id,omitempty"`
	RevertOf        *int64          `json:"revert_of,omitempty"`
	AppliedAt       time.Time       `json:"applied_at"`
	RevertedAt      *time.Time      `json:"reverted_at,omitempty"`
	RevertedBy      *int64          `json:"reverted_by,omitempty"`
}

const (
	AgentChangeBlocklist = "blocklist"
	AgentChangeLabels    = "labels"
)

var ErrInvalidAgentChange = errors.New("invalid change")

func validReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 1000 {
		return "", fmt.Errorf("%w: reason must be 1 to 1000 characters", ErrInvalidAgentChange)
	}
	return reason, nil
}

func normalizeBlocklist(changes []policy.BlocklistChange) error {
	if len(changes) == 0 || len(changes) > 50 {
		return fmt.Errorf("%w: give 1 to 50 changes", ErrInvalidAgentChange)
	}
	seen := map[string]bool{}
	for i := range changes {
		if err := changes[i].Normalize(); err != nil {
			return fmt.Errorf("%w: change %d: %v", ErrInvalidAgentChange, i+1, err)
		}
		if seen[changes[i].AndroidPackage] {
			return fmt.Errorf("%w: %s appears twice", ErrInvalidAgentChange, changes[i].AndroidPackage)
		}
		seen[changes[i].AndroidPackage] = true
	}
	return nil
}

func normalizeLabels(changes []activity.LabelChange) error {
	if len(changes) == 0 || len(changes) > 50 {
		return fmt.Errorf("%w: give 1 to 50 changes", ErrInvalidAgentChange)
	}
	seen := map[string]bool{}
	for i := range changes {
		if err := changes[i].Normalize(); err != nil {
			return fmt.Errorf("%w: change %d: %v", ErrInvalidAgentChange, i+1, err)
		}
		key := changes[i].Kind + "\x00" + strings.ToLower(changes[i].Match)
		if seen[key] {
			return fmt.Errorf("%w: %s %q appears twice", ErrInvalidAgentChange, changes[i].Kind, changes[i].Match)
		}
		seen[key] = true
	}
	return nil
}

// applyBlocklistTx applies changes to the policy in force and appends a
// version. It returns each app's previous state as a change that restores it.
func applyBlocklistTx(ctx context.Context, tx pgx.Tx, changes []policy.BlocklistChange, note string) ([]policy.BlocklistChange, int64, error) {
	// Serialize policy writes so the edit applies to the version in force.
	if _, err := tx.Exec(ctx, `LOCK TABLE policy_versions IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return nil, 0, err
	}
	config := policy.Config{}
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT config FROM policy_versions ORDER BY id DESC LIMIT 1`).Scan(&raw)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, err
	}
	if raw != nil {
		if err := json.Unmarshal(raw, &config); err != nil {
			return nil, 0, err
		}
	}
	previous := make([]policy.BlocklistChange, 0, len(changes))
	for _, change := range changes {
		previous = append(previous, policy.BlocklistChange{Action: policy.BlocklistState(config, change.AndroidPackage), AndroidPackage: change.AndroidPackage})
	}
	next := policy.ApplyBlocklist(config, changes)
	if err := next.Normalize(); err != nil {
		return nil, 0, fmt.Errorf("%w: %s", ErrInvalidPolicy, err)
	}
	encoded, err := json.Marshal(next)
	if err != nil {
		return nil, 0, err
	}
	if runes := []rune(note); len(runes) > maxPlanningTextRunes {
		note = string(runes[:maxPlanningTextRunes])
	}
	var version int64
	err = tx.QueryRow(ctx, `INSERT INTO policy_versions (config, note) VALUES ($1::jsonb, $2) RETURNING id`, string(encoded), note).Scan(&version)
	return previous, version, err
}

// applyLabelsTx applies label changes and returns, per change, the change
// that restores the previous label (or its absence).
func applyLabelsTx(ctx context.Context, tx pgx.Tx, changes []activity.LabelChange, proposalID *int64) ([]activity.LabelChange, error) {
	previous := make([]activity.LabelChange, 0, len(changes))
	for _, change := range changes {
		var label string
		err := tx.QueryRow(ctx, `SELECT label FROM activity_labels WHERE kind = $1 AND lower(match) = lower($2) AND removed_at IS NULL`,
			change.Kind, change.Match).Scan(&label)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			previous = append(previous, activity.LabelChange{Action: "remove", Kind: change.Kind, Match: change.Match})
		case err != nil:
			return nil, err
		default:
			previous = append(previous, activity.LabelChange{Action: "set", Kind: change.Kind, Match: change.Match, Label: label})
		}
		if _, err := tx.Exec(ctx, `UPDATE activity_labels SET removed_at = now()
			WHERE kind = $1 AND lower(match) = lower($2) AND removed_at IS NULL`, change.Kind, change.Match); err != nil {
			return nil, err
		}
		if change.Action == "set" {
			if _, err := tx.Exec(ctx, `INSERT INTO activity_labels (kind, match, label, proposal_id) VALUES ($1, $2, $3, $4)`,
				change.Kind, change.Match, change.Label, proposalID); err != nil {
				return nil, err
			}
		}
	}
	return previous, nil
}

const agentChangeColumns = `id, kind, changes, previous, reason, policy_version_id, revert_of, applied_at, reverted_at, reverted_by`

func scanAgentChange(row pgx.Row) (AgentChange, error) {
	var change AgentChange
	if err := row.Scan(&change.ID, &change.Kind, &change.Changes, &change.Previous, &change.Reason, &change.PolicyVersionID,
		&change.RevertOf, &change.AppliedAt, &change.RevertedAt, &change.RevertedBy); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return change, ErrNotFound
		}
		return change, err
	}
	return change, nil
}

func insertAgentChange(ctx context.Context, tx pgx.Tx, kind string, changes, previous any, reason string, version *int64, revertOf *int64) (AgentChange, error) {
	encodedChanges, err := json.Marshal(changes)
	if err != nil {
		return AgentChange{}, err
	}
	encodedPrevious, err := json.Marshal(previous)
	if err != nil {
		return AgentChange{}, err
	}
	return scanAgentChange(tx.QueryRow(ctx, `INSERT INTO agent_changes (kind, changes, previous, reason, policy_version_id, revert_of)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+agentChangeColumns, kind, encodedChanges, encodedPrevious, reason, version, revertOf))
}

// ApplyBlocklistChange applies owner-approved blocklist edits at once.
func (p *Postgres) ApplyBlocklistChange(ctx context.Context, changes []policy.BlocklistChange, reason string) (AgentChange, error) {
	reason, err := validReason(reason)
	if err != nil {
		return AgentChange{}, err
	}
	if err := normalizeBlocklist(changes); err != nil {
		return AgentChange{}, err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return AgentChange{}, err
	}
	defer tx.Rollback(ctx)
	previous, version, err := applyBlocklistTx(ctx, tx, changes, "Assistant change: "+reason)
	if err != nil {
		return AgentChange{}, err
	}
	change, err := insertAgentChange(ctx, tx, AgentChangeBlocklist, changes, previous, reason, &version, nil)
	if err != nil {
		return AgentChange{}, err
	}
	return change, tx.Commit(ctx)
}

// ApplyLabelChanges applies owner-approved label edits at once.
func (p *Postgres) ApplyLabelChanges(ctx context.Context, changes []activity.LabelChange, reason string) (AgentChange, error) {
	reason, err := validReason(reason)
	if err != nil {
		return AgentChange{}, err
	}
	if err := normalizeLabels(changes); err != nil {
		return AgentChange{}, err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return AgentChange{}, err
	}
	defer tx.Rollback(ctx)
	previous, err := applyLabelsTx(ctx, tx, changes, nil)
	if err != nil {
		return AgentChange{}, err
	}
	change, err := insertAgentChange(ctx, tx, AgentChangeLabels, changes, previous, reason, nil, nil)
	if err != nil {
		return AgentChange{}, err
	}
	return change, tx.Commit(ctx)
}

// ListAgentChanges returns applied changes, newest first.
func (p *Postgres) ListAgentChanges(ctx context.Context, limit int) ([]AgentChange, error) {
	if limit < 1 || limit > 100 {
		return nil, errors.New("limit must be between 1 and 100")
	}
	rows, err := p.pool.Query(ctx, `SELECT `+agentChangeColumns+` FROM agent_changes ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AgentChange{}
	for rows.Next() {
		change, err := scanAgentChange(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, change)
	}
	return result, rows.Err()
}

// RevertAgentChange restores what one change replaced, for exactly the apps
// or labels it touched, and records the revert as a new change.
func (p *Postgres) RevertAgentChange(ctx context.Context, id int64) (AgentChange, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return AgentChange{}, err
	}
	defer tx.Rollback(ctx)
	original, err := scanAgentChange(tx.QueryRow(ctx, `SELECT `+agentChangeColumns+` FROM agent_changes WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		return AgentChange{}, err
	}
	if original.RevertedAt != nil {
		return AgentChange{}, fmt.Errorf("%w: change %d was already reverted by change %d", ErrProposalConflict, id, *original.RevertedBy)
	}
	reason := fmt.Sprintf("Revert of change %d (%s)", original.ID, original.Reason)
	if runes := []rune(reason); len(runes) > 1000 {
		reason = string(runes[:1000])
	}
	var revert AgentChange
	switch original.Kind {
	case AgentChangeBlocklist:
		var restore []policy.BlocklistChange
		if err := json.Unmarshal(original.Previous, &restore); err != nil {
			return AgentChange{}, err
		}
		previous, version, err := applyBlocklistTx(ctx, tx, restore, reason)
		if err != nil {
			return AgentChange{}, err
		}
		revert, err = insertAgentChange(ctx, tx, original.Kind, restore, previous, reason, &version, &original.ID)
		if err != nil {
			return AgentChange{}, err
		}
	case AgentChangeLabels:
		var restore []activity.LabelChange
		if err := json.Unmarshal(original.Previous, &restore); err != nil {
			return AgentChange{}, err
		}
		previous, err := applyLabelsTx(ctx, tx, restore, nil)
		if err != nil {
			return AgentChange{}, err
		}
		revert, err = insertAgentChange(ctx, tx, original.Kind, restore, previous, reason, nil, &original.ID)
		if err != nil {
			return AgentChange{}, err
		}
	case AgentChangeTasks:
		written, previous, err := revertTasks(ctx, tx, original.Previous)
		if err != nil {
			return AgentChange{}, err
		}
		if revert, err = insertAgentChange(ctx, tx, original.Kind, written, previous, reason, nil, &original.ID); err != nil {
			return AgentChange{}, err
		}
	case AgentChangePlan:
		note := reason
		if runes := []rune(note); len(runes) > maxPlanningTextRunes {
			note = string(runes[:maxPlanningTextRunes])
		}
		written, previous, err := p.revertPlan(ctx, tx, original.Previous, note)
		if err != nil {
			return AgentChange{}, err
		}
		if revert, err = insertAgentChange(ctx, tx, original.Kind, written, previous, reason, nil, &original.ID); err != nil {
			return AgentChange{}, err
		}
	default:
		return AgentChange{}, fmt.Errorf("%w: unknown kind %q", ErrInvalidAgentChange, original.Kind)
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_changes SET reverted_at = now(), reverted_by = $2 WHERE id = $1`, original.ID, revert.ID); err != nil {
		return AgentChange{}, err
	}
	return revert, tx.Commit(ctx)
}
