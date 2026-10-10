package main

import (
	"context"
	"log"
	"time"

	"github.com/rimexe0/telemetry/internal/changes"
	"github.com/rimexe0/telemetry/internal/places"
	"github.com/rimexe0/telemetry/internal/store"
)

// placeLookback is how long before a reminder was set its last position may
// be, to know where the owner was when they asked.
const placeLookback = 6 * time.Hour

// runPlaceReminders checks the uploaded positions against place follow-ups
// every minute. The phone checks them itself, at once and offline; this
// catches what it missed, and covers a phone app too old to check, a few
// minutes late as the positions arrive.
func runPlaceReminders(ctx context.Context, repository store.Repository, feed *changes.Feed) {
	check := func() {
		checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		now := time.Now().UTC()
		items, err := repository.ListFollowUps(checkCtx, now)
		if err != nil {
			log.Printf("place reminders: %v", err)
			return
		}
		for _, item := range items {
			if item.State != store.FollowUpOpen || item.Place == nil || item.TriggeredAt != nil {
				continue
			}
			fixes, err := repository.LocationFixes(checkCtx, item.CreatedAt.Add(-placeLookback), now)
			if err != nil {
				log.Printf("place reminders: %v", err)
				return
			}
			var last *places.Fix
			after := []places.Fix{}
			for i, fix := range fixes {
				if fix.At.Before(item.CreatedAt) {
					last = &fixes[i]
				} else {
					after = append(after, fix)
				}
			}
			circles := make([]places.Circle, 0, len(item.Place.Points))
			for _, point := range item.Place.Points {
				circles = append(circles, places.Circle{Lat: point.Lat, Lon: point.Lon, Radius: point.Radius})
			}
			at := places.Reached(item.Place.When == store.PlaceArrive, circles, last, after, item.DueAt, now)
			if at == nil {
				continue
			}
			if _, err := repository.TriggerFollowUp(checkCtx, item.ID, *at); err != nil {
				log.Printf("place reminders: follow-up %d: %v", item.ID, err)
				continue
			}
			log.Printf("place reminder %d reached at %s", item.ID, at.Format(time.RFC3339))
			if feed != nil {
				feed.Changed()
			}
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
