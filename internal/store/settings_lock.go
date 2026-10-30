package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rimexe0/telemetry/internal/policy"
)

// ErrSettingsLocked is a change that would loosen blocking while the
// settings lock holds, or a PIN that is refused.
var ErrSettingsLocked = errors.New("blocking settings are locked")

const (
	// pinTries wrong PINs within pinTryWindow stop further tries until the
	// oldest of them is that old.
	pinTries     = 3
	pinTryWindow = time.Hour
)

var pinPattern = regexp.MustCompile(`^[0-9]{4,12}$`)

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func latestConfig(ctx context.Context, q rowQuerier) (policy.Config, error) {
	config := policy.Config{}
	var raw []byte
	if err := q.QueryRow(ctx, `SELECT config FROM policy_versions ORDER BY id DESC LIMIT 1`).Scan(&raw); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return config, err
	}
	if raw != nil {
		if err := json.Unmarshal(raw, &config); err != nil {
			return config, err
		}
	}
	return config, config.Normalize()
}

// SettingsLockState is where the settings lock stands at an instant.
type SettingsLockState struct {
	// Locked is true while loosening is refused: in a lock time and not
	// unlocked by the PIN.
	Locked bool `json:"locked"`
	// Until is when the lock time ends; nil for a lock at all times.
	Until         *time.Time `json:"until,omitempty"`
	UnlockedUntil *time.Time `json:"unlocked_until,omitempty"`
	PinSet        bool       `json:"pin_set"`
	PinOpen       bool       `json:"pin_open"`
	// TriesLeft is how many wrong PINs may still be entered before tries
	// stop for a while.
	TriesLeft int `json:"tries_left"`
}

func lockState(ctx context.Context, q rowQuerier, config policy.Config, now time.Time) (SettingsLockState, error) {
	state := SettingsLockState{PinOpen: config.PinOpen(now)}
	locked, until := config.Locked(now)
	if !until.IsZero() {
		state.Until = &until
	}
	var unlocked time.Time
	err := q.QueryRow(ctx, `SELECT until FROM settings_unlocks WHERE until > $1 AND unlocked_at <= $1 ORDER BY until DESC LIMIT 1`, now).Scan(&unlocked)
	switch {
	case err == nil:
		state.UnlockedUntil = &unlocked
	case !errors.Is(err, pgx.ErrNoRows):
		return state, err
	}
	state.Locked = locked && state.UnlockedUntil == nil
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM settings_pin WHERE replaced_at IS NULL)`).Scan(&state.PinSet); err != nil {
		return state, err
	}
	var wrong int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM settings_pin_tries WHERE NOT ok AND tried_at > $1`, now.Add(-pinTryWindow)).Scan(&wrong); err != nil {
		return state, err
	}
	state.TriesLeft = max(pinTries-wrong, 0)
	return state, nil
}

// lockedError describes the lock to whoever was refused, the assistant or
// the owner.
func lockedError(config policy.Config, state SettingsLockState) error {
	location, err := time.LoadLocation(config.Timezone)
	if err != nil {
		location = time.UTC
	}
	message := "blocking cannot be loosened now"
	if state.Until != nil {
		message += " (locked until " + state.Until.In(location).Format("15:04") + ")"
	}
	message += "; only the PIN, entered in the panel by the person who holds it, opens the lock"
	if windows := config.SettingsLockAt(time.Now()).PinWindows; len(windows) > 0 {
		times := []string{}
		for _, window := range windows {
			times = append(times, window.Start+"–"+window.End)
		}
		message += ", and only at " + strings.Join(times, ", ")
	}
	return fmt.Errorf("%w: %s", ErrSettingsLocked, message)
}

// refuseIfLocked returns ErrSettingsLocked when a change loosens blocking
// while the lock holds. It also reports whether the PIN has unlocked the
// settings, when changes apply at once instead of waiting a day.
func refuseIfLocked(ctx context.Context, q rowQuerier, loosens bool) (bool, error) {
	config, err := latestConfig(ctx, q)
	if err != nil {
		return false, err
	}
	state, err := lockState(ctx, q, config, time.Now().UTC())
	if err != nil {
		return false, err
	}
	if loosens && state.Locked {
		return false, lockedError(config, state)
	}
	return state.UnlockedUntil != nil, nil
}

// SettingsLock returns where the settings lock stands now.
func (p *Postgres) SettingsLock(ctx context.Context) (SettingsLockState, error) {
	config, err := latestConfig(ctx, p.pool)
	if err != nil {
		return SettingsLockState{}, err
	}
	return lockState(ctx, p.pool, config, time.Now().UTC())
}

func hashPin(salt, pin string) string {
	sum := sha256.Sum256([]byte(salt + ":" + pin))
	return hex.EncodeToString(sum[:])
}

// checkPin records a try of the PIN and reports whether it matched. Tries
// stop for a while after too many wrong ones.
func checkPin(ctx context.Context, tx pgx.Tx, pin string, now time.Time) error {
	var wrong int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM settings_pin_tries WHERE NOT ok AND tried_at > $1`, now.Add(-pinTryWindow)).Scan(&wrong); err != nil {
		return err
	}
	if wrong >= pinTries {
		return fmt.Errorf("%w: too many wrong PINs; try again within the hour after the first of them", ErrSettingsLocked)
	}
	var salt, stored string
	err := tx.QueryRow(ctx, `SELECT salt, pin_hash FROM settings_pin WHERE replaced_at IS NULL ORDER BY id DESC LIMIT 1`).Scan(&salt, &stored)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: no PIN is set", ErrSettingsLocked)
	}
	if err != nil {
		return err
	}
	ok := subtle.ConstantTimeCompare([]byte(hashPin(salt, pin)), []byte(stored)) == 1
	if _, err := tx.Exec(ctx, `INSERT INTO settings_pin_tries (tried_at, ok) VALUES ($1, $2)`, now, ok); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: wrong PIN, %d tries left this hour", ErrSettingsLocked, max(pinTries-wrong-1, 0))
	}
	return nil
}

// SetSettingsPin sets the PIN the first time, or replaces it when the
// current one is given. A wrong current PIN counts as a wrong try.
func (p *Postgres) SetSettingsPin(ctx context.Context, current, next string) error {
	if !pinPattern.MatchString(next) {
		return fmt.Errorf("%w: the PIN must be 4 to 12 digits", ErrInvalidAgentChange)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `LOCK TABLE settings_pin IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return err
	}
	var set bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM settings_pin WHERE replaced_at IS NULL)`).Scan(&set); err != nil {
		return err
	}
	now := time.Now().UTC()
	if set {
		if err := checkPin(ctx, tx, current, now); err != nil {
			// A wrong try is kept even though the change is refused.
			if errors.Is(err, ErrSettingsLocked) && strings.Contains(err.Error(), "wrong PIN") {
				_ = tx.Commit(ctx)
			}
			return err
		}
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return err
	}
	salt := hex.EncodeToString(random)
	if _, err := tx.Exec(ctx, `UPDATE settings_pin SET replaced_at = $1 WHERE replaced_at IS NULL`, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO settings_pin (salt, pin_hash, set_at) VALUES ($1, $2, $3)`, salt, hashPin(salt, next), now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// UnlockSettings lifts the settings lock for its unlock minutes when the
// PIN is right and entered during the PIN times.
func (p *Postgres) UnlockSettings(ctx context.Context, pin string) (time.Time, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return time.Time{}, err
	}
	defer tx.Rollback(ctx)
	config, err := latestConfig(ctx, tx)
	if err != nil {
		return time.Time{}, err
	}
	now := time.Now().UTC()
	lock := config.SettingsLockAt(now)
	if !lock.Enabled {
		return time.Time{}, fmt.Errorf("%w: the settings lock is off", ErrSettingsLocked)
	}
	if !config.PinOpen(now) {
		state, err := lockState(ctx, tx, config, now)
		if err != nil {
			return time.Time{}, err
		}
		return time.Time{}, lockedError(config, state)
	}
	if err := checkPin(ctx, tx, pin, now); err != nil {
		if strings.Contains(err.Error(), "wrong PIN") {
			_ = tx.Commit(ctx)
		}
		return time.Time{}, err
	}
	until := now.Add(time.Duration(lock.UnlockMinutes) * time.Minute)
	if _, err := tx.Exec(ctx, `INSERT INTO settings_unlocks (unlocked_at, until) VALUES ($1, $2)`, now, until); err != nil {
		return time.Time{}, err
	}
	return until, tx.Commit(ctx)
}
