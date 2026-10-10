package mcpapi

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/rimexe0/telemetry/internal/places"
	"github.com/rimexe0/telemetry/internal/store"
)

const placeFollowUpNotes = "A follow-up can come due on arriving at or leaving a place instead of at a time: home, a named place or chain from the map (every Migros near home), " +
	"a kind of place (any pharmacy), or a point. When the owner says \"when I get home\", \"when I'm at X\" or \"next time I pass a pharmacy\", set place and create it; " +
	"do not ask them for a time instead. in_minutes or at, if given too, is the earliest it may come due. The phone checks its own GPS positions and, for home, " +
	"joining the home Wi-Fi; it alerts within a minute or two of arriving. GPS is good to about 25 m, so a place is a building or a shop, never a room."

const (
	// placeSearchMeters is how far from home (or the latest position) named
	// places and kinds are looked for.
	placeSearchMeters = 25000.0
	homeRadius        = 120.0
	pointRadius       = 60.0
)

type FollowUpPlaceInput struct {
	When   string   `json:"when" jsonschema:"arrive or leave"`
	Home   bool     `json:"home,omitempty" jsonschema:"the owner's home, as the phone's nights show it"`
	Name   string   `json:"name,omitempty" jsonschema:"a place name or brand from the map, such as Migros; every match near home counts"`
	Kind   string   `json:"kind,omitempty" jsonschema:"a kind of place from the map, such as pharmacy, supermarket, bakery, hospital, cafe; any of them near home counts"`
	Lat    *float64 `json:"lat,omitempty"`
	Lon    *float64 `json:"lon,omitempty"`
	Radius float64  `json:"radius,omitempty" jsonschema:"meters, 20 through 2000; default 120 for home, 60 for a point, the outline for a map place"`
}

type FollowUpPlaceOutput struct {
	When   string `json:"when"`
	Name   string `json:"name"`
	Home   bool   `json:"home,omitempty"`
	Places int    `json:"places" jsonschema:"how many places are watched"`
	// Nearest is the closest watched place to home, with its distance.
	Nearest string `json:"nearest,omitempty"`
}

func placeOutput(place *store.FollowUpPlace) *FollowUpPlaceOutput {
	if place == nil {
		return nil
	}
	return &FollowUpPlaceOutput{When: place.When, Name: place.Name, Home: place.Home, Places: len(place.Points)}
}

// resolvePlace turns what the owner said into the circles the phone watches.
func resolvePlace(ctx context.Context, repository store.Repository, input FollowUpPlaceInput, now time.Time, location *time.Location) (*store.FollowUpPlace, string, error) {
	when := strings.TrimSpace(input.When)
	if when != store.PlaceArrive && when != store.PlaceLeave {
		return nil, "", errors.New(`place when must be "arrive" or "leave"`)
	}
	name, kind := strings.TrimSpace(input.Name), strings.TrimSpace(input.Kind)
	point := input.Lat != nil && input.Lon != nil
	given := 0
	for _, set := range []bool{input.Home, name != "" || kind != "", point} {
		if set {
			given++
		}
	}
	if given != 1 {
		return nil, "", errors.New("give one of: home, name and/or kind, or lat and lon")
	}
	if input.Radius != 0 && (input.Radius < 20 || input.Radius > 2000) {
		return nil, "", errors.New("radius must be between 20 and 2000 meters")
	}
	radius := func(fallback float64) float64 {
		if input.Radius != 0 {
			return input.Radius
		}
		return fallback
	}
	history, err := repository.LocationFixes(ctx, now.Add(-homeLookback), now)
	if err != nil {
		return nil, "", err
	}
	home := places.Home(history, location)
	switch {
	case input.Home:
		if home == nil {
			return nil, "", errors.New("home is not known yet: the phone has not recorded enough nights")
		}
		return &store.FollowUpPlace{When: when, Name: "home", Home: true, Points: []store.PlacePoint{{Lat: home[0], Lon: home[1], Radius: radius(homeRadius)}}}, "", nil
	case point:
		return &store.FollowUpPlace{When: when, Name: "the point given", Points: []store.PlacePoint{{Lat: *input.Lat, Lon: *input.Lon, Radius: radius(pointRadius)}}}, "", nil
	}
	// Look around home, or around the latest position before home is known.
	var center [2]float64
	switch {
	case home != nil:
		center = *home
	case len(history) > 0:
		latest := history[len(history)-1]
		center = [2]float64{latest.Lat, latest.Lon}
	default:
		return nil, "", errors.New("the phone has no positions yet, so there is nothing to search around")
	}
	found, err := repository.SearchMapPlaces(ctx, store.PlaceQuery{Name: name, Kind: kind, Lat: center[0], Lon: center[1], Within: placeSearchMeters, Limit: store.MaxPlacePoints})
	if err != nil {
		return nil, "", err
	}
	if len(found) == 0 {
		return nil, "", errors.New("the map has no such place within 25 km of home; try another name or kind, or give lat and lon")
	}
	label := strings.TrimSpace(name + " " + kind)
	place := &store.FollowUpPlace{When: when, Name: label}
	for _, item := range found {
		place.Points = append(place.Points, store.PlacePoint{Lat: item.Lat, Lon: item.Lon, Radius: radius(outlineRadius(item))})
	}
	nearest := found[0].Name
	if street := found[0].Details["addr:street"]; street != "" {
		nearest += ", " + street
	}
	nearest += " (" + distanceText(found[0].Meters) + " from home)"
	if home == nil {
		nearest = found[0].Name + " (" + distanceText(found[0].Meters) + " from the latest position)"
	}
	return place, nearest, nil
}

// outlineRadius covers a place drawn as a shape from its middle, with room
// for GPS error; a place drawn as a point gets a fixed circle.
func outlineRadius(place store.MapPlace) float64 {
	if place.Box == nil {
		return pointRadius
	}
	box := place.Box
	half := places.Meters(box[0], box[1], box[2], box[3]) / 2
	return math.Min(math.Max(half+30, pointRadius), 400)
}

func distanceText(meters float64) string {
	if meters < 1000 {
		return fmt.Sprintf("%.0f m", math.Round(meters/10)*10)
	}
	return fmt.Sprintf("%.1f km", meters/1000)
}
