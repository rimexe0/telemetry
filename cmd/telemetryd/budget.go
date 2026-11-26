package main

import (
	"context"
	"log"
	"time"

	"github.com/rimexe0/telemetry/internal/store"
)

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
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		check()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
