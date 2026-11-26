package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rimexe0/telemetry/internal/budget"
	"github.com/rimexe0/telemetry/internal/policy"
)

func budgetGrayscale(action, platform string) budget.GrayscaleEvent {
	return budget.GrayscaleEvent{Action: action, Platform: platform}
}

// loopholeTx records the loophole cases a schedule change opens.
type loopholeTx struct {
	*scheduleTx
	opened int
}

func (tx *loopholeTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	if strings.HasPrefix(sql, `INSERT INTO loophole_cases`) {
		tx.opened++
	}
	return pgconn.CommandTag{}, nil
}

func ownerBudget() policy.Budget {
	return policy.Budget{Enabled: true, BaseMinutes: 120, MaxMinutes: 300, FloorMinutes: 30, HabitsForMax: 6,
		RateDistraction: 1, RateDay: 3, RateBedtime: 10, RateGrayscaleOff: 3, BorrowMultiplier: 2,
		WindowMinutes: 60, WindowUseMinutes: 10, GraceMinutes: 1, WaitMinutes: 10}
}

func budgetTx(t *testing.T, config policy.Config) *loopholeTx {
	t.Helper()
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	return &loopholeTx{scheduleTx: &scheduleTx{config: raw}}
}

// Asking less of the budget waits for the next day and opens a loophole
// case, as other loosening does; asking more applies at once.
func TestBudgetLooseningWaitsAndIsALoophole(t *testing.T) {
	before := policy.Config{Timezone: "Europe/Istanbul", ClosedByDefault: true, DistractionTimes: []policy.Window{{Start: "19:00", End: "00:00"}}, Budget: ownerBudget()}
	looser := ownerBudget()
	looser.BaseMinutes = 150
	tx := budgetTx(t, before)
	previous, config, _, err := applyScheduleTx(context.Background(), tx, ScheduleChange{Budget: &looser}, "more time")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if config.BudgetAt(now).BaseMinutes != 120 || config.BudgetNext == nil || config.BudgetNext.BaseMinutes != 150 || config.BudgetNextFrom == "" {
		t.Fatalf("a looser budget applied at once: %+v next %+v", config.Budget, config.BudgetNext)
	}
	if tx.opened != 1 {
		t.Fatalf("loophole cases opened = %d", tx.opened)
	}
	if previous.Budget == nil || *previous.Budget != before.Budget {
		t.Fatalf("previous = %+v", previous.Budget)
	}
	// The budget in force did not change, so there is nothing to correct.
	if !config.RuleChanged["budget"].IsZero() {
		t.Fatal("a waiting budget is not a change in force")
	}

	stricter := ownerBudget()
	stricter.RateDay = 4
	tx = budgetTx(t, before)
	_, config, _, err = applyScheduleTx(context.Background(), tx, ScheduleChange{Budget: &stricter}, "dearer days")
	if err != nil || config.BudgetAt(now).RateDay != 4 || config.BudgetNext != nil || tx.opened != 0 {
		t.Fatalf("a stricter budget = %+v, %d cases, %v", config.Budget, tx.opened, err)
	}

	// Switching it on applies at once and starts its history.
	off := before
	off.Budget = policy.Budget{}
	tx = budgetTx(t, off)
	on := ownerBudget()
	_, config, _, err = applyScheduleTx(context.Background(), tx, ScheduleChange{Budget: &on}, "start the budget")
	if err != nil || !config.BudgetAt(now).Enabled || config.BudgetSince == nil || tx.opened != 0 {
		t.Fatalf("switching on = %+v since %v, %d cases, %v", config.Budget, config.BudgetSince, tx.opened, err)
	}

	// Without closed by default there is nothing for the budget to open.
	open := before
	open.ClosedByDefault, open.Budget = false, policy.Budget{}
	tx = budgetTx(t, open)
	if _, _, _, err := applyScheduleTx(context.Background(), tx, ScheduleChange{Budget: &on}, "x"); err == nil || !strings.Contains(err.Error(), "closed_by_default") {
		t.Fatalf("budget without closed by default = %v", err)
	}
}

func TestReasonsAreTrimmedAndBounded(t *testing.T) {
	reasons, err := CleanReasons([]string{"  to make music ", "", "to see friends"})
	if err != nil || len(reasons) != 2 || reasons[0] != "to make music" {
		t.Fatalf("reasons = %v, %v", reasons, err)
	}
	if _, err := CleanReasons([]string{strings.Repeat("x", 501)}); err == nil {
		t.Fatal("a long reason must be refused")
	}
	if err := ValidGrayscale(budgetGrayscale("decline", "mac")); err != nil {
		t.Fatal(err)
	}
	if err := ValidGrayscale(budgetGrayscale("decline", "windows")); err == nil {
		t.Fatal("an unknown platform must be refused")
	}
}
