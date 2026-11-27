package store

import (
	"context"
	"time"

	"github.com/rimexe0/telemetry/internal/places"
)

// HomeLookback is how many nights are read to find home.
const HomeLookback = 14 * 24 * time.Hour

// VisitedStay is one stay in a range, cut to the range's start, with the
// named places within 100 m of it (none at home or before the map is
// imported).
type VisitedStay struct {
	places.Stay
	Nearby []MapPlace
}

// Visited is where the phone was in a range: the positions in it, its
// stays, and when the owner first left home and last came back.
type Visited struct {
	Positions []places.Fix
	Stays     []VisitedStay
	Home      *[2]float64
	// LeftHome and BackHome are nil when the range has no such moment.
	LeftHome, BackHome *time.Time
	// Map is nil before the first map import.
	Map *MapImport
}

// LoadVisited reads the positions of [from, to) and works out the stays,
// with home found from the nights before. A stay already going at the start
// is read from up to 12 hours earlier and starts at from.
func LoadVisited(ctx context.Context, repository Repository, from, to time.Time, location *time.Location) (Visited, error) {
	history, err := repository.LocationFixes(ctx, from.Add(-HomeLookback), to)
	if err != nil {
		return Visited{}, err
	}
	home := places.Home(history, location)
	recent := []places.Fix{}
	output := Visited{Positions: []places.Fix{}, Stays: []VisitedStay{}, Home: home}
	for _, fix := range history {
		if !fix.At.Before(from.Add(-12 * time.Hour)) {
			recent = append(recent, fix)
		}
		if !fix.At.Before(from) {
			output.Positions = append(output.Positions, fix)
		}
	}
	if output.Map, err = repository.LatestMapImport(ctx); err != nil {
		return Visited{}, err
	}
	var wasHome *bool
	var homeEnd time.Time
	for _, stay := range places.Stays(recent, home) {
		if stay.Home {
			homeEnd = stay.End
		}
		if !stay.End.After(from) {
			wasHome = &stay.Home
			continue
		}
		if stay.Start.Before(from) {
			stay.Start = from
		}
		if wasHome != nil && *wasHome && !stay.Home && output.LeftHome == nil {
			// Left when the stay at home ended, not when the next one began.
			left := homeEnd
			output.LeftHome = &left
		}
		if wasHome != nil && !*wasHome && stay.Home {
			back := stay.Start
			output.BackHome = &back
		}
		isHome := stay.Home
		wasHome = &isHome
		item := VisitedStay{Stay: stay, Nearby: []MapPlace{}}
		if !stay.Home && output.Map != nil {
			nearby, err := repository.NearbyPlaces(ctx, stay.Lat, stay.Lon, 100, 5)
			if err != nil {
				return Visited{}, err
			}
			item.Nearby = nearby
		}
		output.Stays = append(output.Stays, item)
	}
	return output, nil
}
