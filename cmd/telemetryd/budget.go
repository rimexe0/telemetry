package main

import (
	"context"
	"log"
	"time"

	"github.com/rimexe0/telemetry/internal/store"
)

// habitRulesEvery is how often the server checks habit rules itself.
const habitRulesEvery = 5 * time.Minute

// runBudgetDays writes each budget day's snapshot once the day has ended
// (owner decision 2026-11-26), every minute until ctx ends. A day missed
// while the server was down is written when it is back, oldest first.
func runBudgetDays(ctx context.Context, repository store.Repository, budgets store.BudgetStore) {
	check := func() {
		checkCtx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		written, err := store.CloseBudgetDays(checkCtx, repository, budgets, time.Now().UTC())
		if err != nil {
			log.Printf("budget days: %v", err)
			return
		}
		if written > 0 {
			log.Printf("budget days: %d day snapshot(s) written", written)
		}
	}
	// Habit rules are checked against the stored positions as a safety net
	// for the phone, which works them out first.
	lastRules := time.Time{}
	rules := func() {
		if time.Since(lastRules) < habitRulesEvery {
			return
		}
		lastRules = time.Now()
		checkCtx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		ticked, err := store.EvaluateHabitRules(checkCtx, repository, budgets, time.Now().UTC())
		if err != nil {
			log.Printf("habit rules: %v", err)
			return
		}
		if ticked > 0 {
			log.Printf("habit rules: %d habit(s) ticked from the records", ticked)
		}
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		check()
		rules()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
