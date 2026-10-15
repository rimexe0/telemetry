package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rimexe0/telemetry/internal/places"
	"github.com/rimexe0/telemetry/internal/policy"
)

// WatchTrackKind is a walk the watch recorded while away from the phone,
// relayed by the phone when they meet again.
const WatchTrackKind = "watch.outside_walk"

// WatchTracks are the watch's walk positions in [from, to), each split where
// the watch was not on the wrist, so only worn stretches count as the owner.
func (p *Postgres) WatchTracks(ctx context.Context, from, to time.Time) ([][]places.Fix, error) {
	rows, err := p.pool.Query(ctx, `SELECT r.projection FROM observations o
		JOIN observation_revisions r ON r.id = o.effective_revision_id
		WHERE o.kind = $1 AND o.ended_at >= $2 AND o.occurred_at < $3 ORDER BY o.occurred_at`, WatchTrackKind, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tracks := [][]places.Fix{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		tracks = append(tracks, wornRuns(raw)...)
	}
	return tracks, rows.Err()
}

// wornRuns reads a walk's points and splits them where the watch was off.
func wornRuns(raw []byte) [][]places.Fix {
	var payload struct {
		Points []struct {
			At       time.Time `json:"t"`
			Lat      float64   `json:"lat"`
			Lon      float64   `json:"lon"`
			Accuracy float64   `json:"acc"`
			Worn     bool      `json:"worn"`
		} `json:"points"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return nil
	}
	runs := [][]places.Fix{}
	current := []places.Fix{}
	for _, point := range payload.Points {
		if !point.Worn {
			if len(current) > 1 {
				runs = append(runs, current)
			}
			current = []places.Fix{}
			continue
		}
		current = append(current, places.Fix{At: point.At.UTC(), Lat: point.Lat, Lon: point.Lon, Accuracy: point.Accuracy})
	}
	if len(current) > 1 {
		runs = append(runs, current)
	}
	return runs
}

// OutsideProgress is today's time outside as a rule counts it.
type OutsideProgress struct {
	Minutes      float64 `json:"minutes"`
	WatchMinutes float64 `json:"watch_minutes"`
	PhoneMinutes float64 `json:"phone_minutes"`
	HomeKnown    bool    `json:"home_known"`
}

// homeLookback is how many nights are read to find home.
const homeLookback = 14 * 24 * time.Hour

// OutsideToday counts today's local time outside under the rule: positions
// at least rule.Meters from home, from the watch (worn) and, unless the rule
// counts the watch only, the phone. Without a known home nothing counts.
func OutsideToday(ctx context.Context, repository Repository, rule policy.OutsideRule, location *time.Location, now time.Time) (OutsideProgress, error) {
	local := now.In(location)
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	progress := OutsideProgress{}
	fixes, err := repository.LocationFixes(ctx, now.Add(-homeLookback), now)
	if err != nil {
		return progress, err
	}
	home := places.Home(fixes, location)
	if home == nil {
		return progress, nil
	}
	progress.HomeKnown = true
	meters := float64(rule.Meters)
	// The phone's positions from an hour before midnight, so a stay already
	// going at midnight counts from midnight.
	phone := []places.Fix{}
	for _, fix := range fixes {
		if !fix.At.Before(dayStart.Add(-time.Hour)) {
			phone = append(phone, fix)
		}
	}
	phoneIntervals := places.OutsideIntervals(phone, *home, meters, rule.OnFoot)
	tracks, err := repository.WatchTracks(ctx, dayStart, now)
	if err != nil {
		return progress, err
	}
	watchIntervals := []places.Interval{}
	for _, track := range tracks {
		watchIntervals = append(watchIntervals, places.OutsideIntervals(track, *home, meters, rule.OnFoot)...)
	}
	progress.WatchMinutes = places.Minutes(watchIntervals, dayStart, now)
	progress.PhoneMinutes = places.Minutes(phoneIntervals, dayStart, now)
	counted := watchIntervals
	if !rule.WatchOnly {
		counted = append(counted, phoneIntervals...)
	}
	progress.Minutes = places.Minutes(counted, dayStart, now)
	return progress, nil
}
