package main

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/rimexe0/telemetry/internal/activity"
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

// intentCommand is the assistant that makes readings: its own command, or
// the judge's.
func intentCommand(getenv func(string) string) []string {
	command := getenv("TELEMETRY_INTENT_COMMAND")
	if strings.TrimSpace(command) == "" {
		command = getenv("TELEMETRY_JUDGE_COMMAND")
	}
	return strings.Fields(command)
}

// runIntents works out what the owner is trying to do from everything the
// collectors saw, every intentEvery while they are on a computer, the
// phone or a coding agent, and keeps the running list. It asks nothing of
// the owner and changes nothing they decided.
func runIntents(ctx context.Context, repository store.Repository, command []string) {
	if len(command) == 0 {
		return
	}
	ticker := time.NewTicker(intentEvery)
	defer ticker.Stop()
	for {
		now := time.Now().UTC()
		if _, err := readIntents(ctx, repository, command, now.Add(-intentWindow), now, now.Add(-intentEvery)); err != nil {
			log.Printf("intents: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// backfillIntents makes the readings for [from, now) that were never made,
// oldest first in intentWindow steps, so each continues the aims of the one
// before. A window with no activity is skipped.
func backfillIntents(ctx context.Context, repository store.Repository, command []string, from time.Time) error {
	now := time.Now().UTC()
	starts, err := repository.IntentReadingStarts(ctx, from, now)
	if err != nil {
		return err
	}
	done := map[int64]bool{}
	for _, start := range starts {
		done[start.Unix()] = true
	}
	made := 0
	for start := from.UTC(); start.Add(intentWindow).Before(now); start = start.Add(intentWindow) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if done[start.Unix()] {
			continue
		}
		end := start.Add(intentWindow)
		read, err := readIntents(ctx, repository, command, start, end, start)
		if err != nil {
			log.Printf("intents backfill %s: %v", start.Format(time.RFC3339), err)
			continue
		}
		if read {
			made++
		}
	}
	log.Printf("intents backfill: %d readings from %s", made, from.Format(time.RFC3339))
	return nil
}

// readIntents makes one reading of [from, to), when something happened
// after activeSince. It reports whether a reading was made.
func readIntents(ctx context.Context, repository store.Repository, command []string, from, to, activeSince time.Time) (bool, error) {
	loadCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	_, location, err := outreachConfig(loadCtx, repository)
	if err != nil || location == nil {
		location = time.UTC
	}
	input, _, err := store.LoadDayInput(loadCtx, repository, to.Truncate(24*time.Hour), from, to, false)
	var known []store.Intent
	var extra []activity.Record
	if err == nil {
		known, err = repository.ListIntents(loadCtx, to.Add(-intentKnown), to)
	}
	if err == nil {
		extra, err = repository.IntentContextRecords(loadCtx, from, to)
	}
	cancel()
	if err != nil {
		return false, err
	}
	recent := false
	for _, segment := range input.Result.Segments {
		if segment.End.After(activeSince) {
			recent = true
			break
		}
	}
	for _, turn := range input.Turns {
		if !turn.ByAgent && turn.Requested.After(activeSince) && turn.Requested.Before(to) {
			recent = true
			break
		}
	}
	if !recent {
		return false, nil
	}
	if len(known) > maxKnown {
		known = known[:maxKnown]
	}
	earlier := make([]intent.Known, 0, len(known))
	for _, item := range known {
		earlier = append(earlier, intent.Known{ID: item.ID, Title: item.Title, Kind: item.Kind, Status: item.Status, NextStep: item.NextStep, LastSeen: item.LastSeen})
	}
	digest := intent.Digest(input, extra, from, to, location)
	askCtx, cancelAsk := context.WithTimeout(ctx, 3*time.Minute)
	reading, output, err := intent.Ask(askCtx, command, intent.Prompt(digest, earlier, to, location), earlier)
	cancelAsk()
	if err != nil {
		return false, err
	}
	saveCtx, cancelSave := context.WithTimeout(ctx, 30*time.Second)
	defer cancelSave()
	saved, err := repository.SaveIntentReading(saveCtx, from, to, digest, output, reading)
	if err != nil {
		return false, err
	}
	log.Printf("intents: reading %d of %s with %d aims (%s)", saved.ID, to.In(location).Format("2006-01-02 15:04"), len(reading.Aims), reading.Note)
	return true, nil
}
