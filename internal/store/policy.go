package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rimexe0/telemetry/internal/policy"
)

var ErrInvalidPolicy = errors.New("invalid policy")

type PolicyVersion struct {
	ID        int64         `json:"id"`
	Config    policy.Config `json:"config"`
	Note      string        `json:"note,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
}

// LatestPolicy returns nil when the owner has not saved a policy yet.
func (p *Postgres) LatestPolicy(ctx context.Context) (*PolicyVersion, error) {
	var version PolicyVersion
	var raw []byte
	err := p.pool.QueryRow(ctx, `SELECT id, config, note, created_at FROM policy_versions ORDER BY id DESC LIMIT 1`).Scan(&version.ID, &raw, &version.Note, &version.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &version.Config); err != nil {
		return nil, err
	}
	return &version, nil
}

// SavePolicy appends a new version; earlier versions stay readable. The
// outside, opening and morning rules keep their rule that looser ones wait
// for the next day, as when they are set with the blocking schedule.
func (p *Postgres) SavePolicy(ctx context.Context, config policy.Config, note string) (PolicyVersion, error) {
	latest, err := p.LatestPolicy(ctx)
	if err != nil {
		return PolicyVersion{}, err
	}
	if latest != nil {
		now := time.Now().UTC()
		outside, openingRule, morning := config.OutsideAt(now), config.OpeningRuleAt(now), config.MorningAt(now)
		stored := latest.Config
		config.Outside, config.OutsideNext, config.OutsideNextFrom = stored.Outside, stored.OutsideNext, stored.OutsideNextFrom
		config.OpeningRule, config.OpeningRuleNext, config.OpeningRuleNextFrom = stored.OpeningRule, stored.OpeningRuleNext, stored.OpeningRuleNextFrom
		config.Morning, config.MorningNext, config.MorningNextFrom = stored.Morning, stored.MorningNext, stored.MorningNextFrom
		config = config.WithOutside(outside, now).WithOpeningRule(openingRule, now).WithMorning(morning, now)
	}
	if err := config.Normalize(); err != nil {
		return PolicyVersion{}, fmt.Errorf("%w: %s", ErrInvalidPolicy, err)
	}
	note = strings.TrimSpace(note)
	if len([]rune(note)) > maxPlanningTextRunes {
		return PolicyVersion{}, fmt.Errorf("%w: note must be at most %d characters", ErrInvalidPolicy, maxPlanningTextRunes)
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return PolicyVersion{}, err
	}
	version := PolicyVersion{Config: config, Note: note}
	err = p.pool.QueryRow(ctx, `INSERT INTO policy_versions (config, note) VALUES ($1::jsonb, $2) RETURNING id, created_at`, string(raw), note).Scan(&version.ID, &version.CreatedAt)
	return version, err
}
