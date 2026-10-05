package main

import (
	"context"
	"log"
	"syscall"
	"time"

	"github.com/rimexe0/telemetry/internal/silence"
	"github.com/rimexe0/telemetry/internal/store"
)

// runSilenceChecker opens and closes collection incidents every minute until
// ctx ends (#28).
func runSilenceChecker(ctx context.Context, repository store.Repository) {
	check := func() {
		checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		evidence, err := repository.CollectionEvidence(checkCtx)
		if err != nil {
			log.Printf("silence check: %v", err)
			return
		}
		var disk syscall.Statfs_t
		if syscall.Statfs("/", &disk) == nil {
			free := uint64(disk.Bavail) * uint64(disk.Bsize)
			evidence.ServerFreeBytes = &free
		}
		now := time.Now().UTC()
		opened, err := repository.SyncCollectionIncidents(checkCtx, silence.Evaluate(evidence, now), now)
		if err != nil {
			log.Printf("silence check: %v", err)
			return
		}
		for _, incident := range opened {
			log.Printf("collection incident %d: %s %s since %s", incident.ID, incident.Device, incident.Kind, incident.StartedAt.Format(time.RFC3339))
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
