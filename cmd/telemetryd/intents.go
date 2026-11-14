package main

import (
	"context"
	"log"
	"time"

	"github.com/rimexe0/telemetry/internal/intent"
	"github.com/rimexe0/telemetry/internal/store"
)

const (
	// intentEvery is how often the reading is made while the owner is on a
	// device, and intentWindow how far back each reading looks.
	intentEvery  = 10 * time.Minute
	intentWindow = 2 * time.Hour
	// Aims seen this recently are shown to the next reading to continue.
	intentKnown = 48 * time.Hour
	maxKnown    = 40
)

// runIntents works out what the owner is trying to do from everything the
// collectors saw, every intentEvery while they are on the Mac, the phone
// or a coding agent, and keeps the running list. It asks nothing of the
// owner and changes nothing they decided.
func runIntents(ctx context.Context, repository store.Repository, command []string) {
	if len(command) == 0 {
		return
	}
	check := func() {
		now := time.Now().UTC()
		from := now.Add(-intentWindow)
		loadCtx, cancel := context.WithTimeout(ctx, time.Minute)
		_, location, err := outreachConfig(loadCtx, repository)
		if err != nil || location == nil {
			location = time.UTC
		}
		input, _, err := store.LoadTrackingInput(loadCtx, repository, now.Truncate(24*time.Hour), from, now)
		var known []store.Intent
		if err == nil {
			known, err = repository.ListIntents(loadCtx, now.Add(-intentKnown))
		}
		cancel()
		if err != nil {
			log.Printf("intents: %v", err)
			return
		}
		recent := false
		for _, segment := range input.Result.Segments {
			if segment.End.After(now.Add(-intentEvery)) {
				recent = true
				break
			}
		}
		for _, turn := range input.Turns {
			if !turn.ByAgent && turn.Requested.After(now.Add(-intentEvery)) {
				recent = true
				break
			}
		}
		if !recent {
			return
		}
		if len(known) > maxKnown {
			known = known[:maxKnown]
		}
		earlier := make([]intent.Known, 0, len(known))
		for _, item := range known {
			earlier = append(earlier, intent.Known{ID: item.ID, Title: item.Title, Kind: item.Kind, Status: item.Status, NextStep: item.NextStep, LastSeen: item.LastSeen})
		}
		digest := intent.Digest(input, from, now, location)
		askCtx, cancelAsk := context.WithTimeout(ctx, 3*time.Minute)
		reading, output, err := intent.Ask(askCtx, command, intent.Prompt(digest, earlier, now, location), earlier)
		cancelAsk()
		if err != nil {
			log.Printf("intents: %v", err)
			return
		}
		saveCtx, cancelSave := context.WithTimeout(ctx, 30*time.Second)
		defer cancelSave()
		saved, err := repository.SaveIntentReading(saveCtx, from, now, digest, output, reading)
		if err != nil {
			log.Printf("intents: %v", err)
			return
		}
		log.Printf("intents: reading %d with %d aims (%s)", saved.ID, len(reading.Aims), reading.Note)
	}
	ticker := time.NewTicker(intentEvery)
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
