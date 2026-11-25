package store

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rimexe0/telemetry/internal/policy"
)

// scheduleTx answers the queries a schedule change makes, without a database.
type scheduleTx struct {
	pgx.Tx
	config        []byte
	unlockedUntil *time.Time
}

type scheduleRow struct {
	values []any
	err    error
}

func (r scheduleRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i, target := range dest {
		switch target := target.(type) {
		case *[]byte:
			*target = r.values[i].([]byte)
		case *time.Time:
			*target = r.values[i].(time.Time)
		case *bool:
			*target = r.values[i].(bool)
		case *int:
			*target = r.values[i].(int)
		case *int64:
			*target = r.values[i].(int64)
		default:
			return fmt.Errorf("unexpected scan target %T", target)
		}
	}
	return nil
}

func (tx *scheduleTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (tx *scheduleTx) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	switch {
	case strings.HasPrefix(sql, `SELECT config FROM policy_versions`):
		return scheduleRow{values: []any{tx.config}}
	case strings.HasPrefix(sql, `SELECT until FROM settings_unlocks`):
		if tx.unlockedUntil == nil {
			return scheduleRow{err: pgx.ErrNoRows}
		}
		return scheduleRow{values: []any{*tx.unlockedUntil}}
	case strings.HasPrefix(sql, `SELECT EXISTS (SELECT 1 FROM settings_pin`):
		return scheduleRow{values: []any{true}}
	case strings.HasPrefix(sql, `SELECT count(*) FROM settings_pin_tries`):
		return scheduleRow{values: []any{0}}
	case strings.HasPrefix(sql, `SELECT id FROM loophole_cases`):
		return scheduleRow{err: pgx.ErrNoRows}
	case strings.HasPrefix(sql, `INSERT INTO policy_versions`):
		return scheduleRow{values: []any{int64(2)}}
	}
	return scheduleRow{err: fmt.Errorf("unexpected query %q", sql)}
}

// Loophole case 1 (2026-11-25): the settings lock was switched off after the
// PIN, and the change log recorded the lock as already off, so reverting it
// would have left the lock off. The log must keep each rule as it was.
func TestScheduleChangeLogsTheRulesItReplaced(t *testing.T) {
	before := policy.Config{
		Timezone:     "Europe/Istanbul",
		SettingsLock: policy.SettingsLock{Enabled: true, UnlockMinutes: 15},
		Bedtime:      policy.Bedtime{Enabled: true, Start: "00:00", End: "08:00", WindDownMinutes: 65},
		Extension:    policy.ExtensionRule{Enabled: true, QuietSeconds: 60},
		OpeningRule:  policy.OpeningRule{WaitMinutes: 10, MaxMinutes: 30, PerDay: 2},
		Morning:      policy.MorningRule{Enabled: true, FromWake: true, Until: "12:00"},
	}
	raw, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	unlocked := time.Now().UTC().Add(10 * time.Minute)
	tx := &scheduleTx{config: raw, unlockedUntil: &unlocked}
	change := ScheduleChange{
		SettingsLock: &policy.SettingsLock{Enabled: false, UnlockMinutes: 15},
		Bedtime:      &policy.Bedtime{Enabled: true, Start: "23:30", End: "08:00", WindDownMinutes: 65},
		Extension:    &policy.ExtensionRule{Enabled: true, QuietSeconds: 30},
		OpeningRule:  &policy.OpeningRule{WaitMinutes: 0, MaxMinutes: 60, PerDay: 5},
		Morning:      &policy.MorningRule{Enabled: false, Until: "12:00"},
	}
	previous, config, _, err := applyScheduleTx(context.Background(), tx, change, "after the PIN")
	if err != nil {
		t.Fatal(err)
	}
	if config.SettingsLockAt(time.Now()).Enabled {
		t.Fatalf("after the PIN the lock should be off at once: %+v", config.SettingsLock)
	}
	if previous.SettingsLock == nil || !reflect.DeepEqual(*previous.SettingsLock, before.SettingsLock) {
		t.Errorf("previous settings lock = %+v, want %+v", previous.SettingsLock, before.SettingsLock)
	}
	if previous.Bedtime == nil || *previous.Bedtime != before.Bedtime {
		t.Errorf("previous bedtime = %+v, want %+v", previous.Bedtime, before.Bedtime)
	}
	if previous.Extension == nil || *previous.Extension != before.Extension {
		t.Errorf("previous extension = %+v, want %+v", previous.Extension, before.Extension)
	}
	if previous.OpeningRule == nil || *previous.OpeningRule != before.OpeningRule {
		t.Errorf("previous opening rule = %+v, want %+v", previous.OpeningRule, before.OpeningRule)
	}
	if previous.Morning == nil || *previous.Morning != before.Morning {
		t.Errorf("previous morning = %+v, want %+v", previous.Morning, before.Morning)
	}
}
