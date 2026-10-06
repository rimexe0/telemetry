package mcpapi

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rimexe0/telemetry/internal/places"
	"github.com/rimexe0/telemetry/internal/store"
)

const locationNotes = "From the phone's GPS: it records a position about once a minute, and only after moving 25 m or more, so a stay runs from the first position there " +
	"to the first one somewhere else. GPS is weak indoors: moving inside a building is not seen, and a step just outside may not be either. Positions are usually within " +
	"10 to 25 m, so neighbouring shops can be confused; nearby lists what the map has within 100 m, nearest first, and the nearest is a likely match, not a fact. " +
	"A short stay at a stop or station is usually waiting for transport. Home is where the phone spends the nights. Use this yourself; do not ask the owner where they were or what a place was, since every question pulls them away. " +
	"Places come from OpenStreetMap kept on the owner's server; their location is never sent anywhere. Times are the owner's local time."

type PlacesVisitedOutput struct {
	Stays []VisitedStay `json:"stays" jsonschema:"oldest first"`
	// LeftHome and BackHome are the first departure and the last return in the range.
	LeftHome   string     `json:"left_home,omitempty"`
	BackHome   string     `json:"back_home,omitempty"`
	MovedKm    float64    `json:"moved_km"`
	Positions  int        `json:"positions"`
	HomeKnown  bool       `json:"home_known"`
	Map        *MapStatus `json:"map,omitempty"`
	MissingMap string     `json:"missing_map,omitempty"`
	Timezone   string     `json:"timezone"`
	Notes      string     `json:"notes"`
}

type VisitedStay struct {
	From    string           `json:"from" jsonschema:"local YYYY-MM-DD HH:MM"`
	To      string           `json:"to" jsonschema:"local YYYY-MM-DD HH:MM"`
	Minutes float64          `json:"minutes"`
	Home    bool             `json:"home"`
	Lat     float64          `json:"lat"`
	Lon     float64          `json:"lon"`
	Nearby  []store.MapPlace `json:"nearby" jsonschema:"named places within 100 m, nearest first; empty at home"`
}

type MapStatus struct {
	Places     int    `json:"places"`
	ImportedAt string `json:"imported_at"`
}

type PlacesNearInput struct {
	Lat    float64 `json:"lat"`
	Lon    float64 `json:"lon"`
	Radius float64 `json:"radius,omitempty" jsonschema:"meters, 10 through 1000; default 100"`
	Limit  int     `json:"limit,omitempty" jsonschema:"1 through 50; default 10"`
}

type PlacesNearOutput struct {
	Places []store.MapPlace `json:"places" jsonschema:"nearest first"`
	Notes  string           `json:"notes"`
}

// homeLookback is how many nights are read to find home.
const homeLookback = 14 * 24 * time.Hour

func addLocationTools(server *mcp.Server, repository store.Repository, now func() time.Time) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "places_visited",
		Description: "Where the owner went in a day or range: each stay of five minutes or more with its times, whether it was home, and the named places at it; when they left home and came back; how far they moved. " + locationNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input ActivityRangeInput) (*mcp.CallToolResult, PlacesVisitedOutput, error) {
		from, to, location, err := activityRange(ctx, repository, input, now())
		if err != nil {
			return nil, PlacesVisitedOutput{}, err
		}
		history, err := repository.LocationFixes(ctx, from.Add(-homeLookback), to)
		if err != nil {
			return nil, PlacesVisitedOutput{}, err
		}
		home := places.Home(history, location)
		// A stay already going at the start of the range is read from earlier.
		recent := []places.Fix{}
		inRange := []places.Fix{}
		for _, fix := range history {
			if !fix.At.Before(from.Add(-12 * time.Hour)) {
				recent = append(recent, fix)
			}
			if !fix.At.Before(from) {
				inRange = append(inRange, fix)
			}
		}
		output := PlacesVisitedOutput{Stays: []VisitedStay{}, HomeKnown: home != nil, Positions: len(inRange), Timezone: location.String(), Notes: locationNotes,
			MovedKm: math.Round(places.Distance(inRange)/100) / 10}
		if imported, err := repository.LatestMapImport(ctx); err != nil {
			return nil, PlacesVisitedOutput{}, err
		} else if imported != nil {
			output.Map = &MapStatus{Places: imported.Places, ImportedAt: imported.ImportedAt.In(location).Format("2006-01-02")}
		} else {
			output.MissingMap = "The map has not been imported yet, so stays have no place names."
		}
		local := func(at time.Time) string { return at.In(location).Format("2006-01-02 15:04") }
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
			if wasHome != nil && *wasHome && !stay.Home && output.LeftHome == "" {
				// Left when the stay at home ended, not when the next one began.
				output.LeftHome = local(homeEnd)
			}
			if wasHome != nil && !*wasHome && stay.Home {
				output.BackHome = local(stay.Start)
			}
			home := stay.Home
			wasHome = &home
			item := VisitedStay{From: local(stay.Start), To: local(stay.End), Minutes: math.Round(stay.Minutes()), Home: stay.Home,
				Lat: math.Round(stay.Lat*1e5) / 1e5, Lon: math.Round(stay.Lon*1e5) / 1e5, Nearby: []store.MapPlace{}}
			if !stay.Home && output.Map != nil {
				nearby, err := repository.NearbyPlaces(ctx, stay.Lat, stay.Lon, 100, 5)
				if err != nil {
					return nil, PlacesVisitedOutput{}, err
				}
				item.Nearby = nearby
			}
			output.Stays = append(output.Stays, item)
		}
		return nil, output, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "places_near",
		Description: "The named places around a point, such as a stay's coordinates: shops, cafés, hospitals, stops and so on, with brand, address and opening hours when the map has them. " + locationNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input PlacesNearInput) (*mcp.CallToolResult, PlacesNearOutput, error) {
		radius, limit := input.Radius, input.Limit
		if radius == 0 {
			radius = 100
		}
		if limit == 0 {
			limit = 10
		}
		if radius < 10 || radius > 1000 || limit < 1 || limit > 50 || input.Lat < -90 || input.Lat > 90 || input.Lon < -180 || input.Lon > 180 {
			return nil, PlacesNearOutput{}, errors.New("lat and lon must be valid, radius 10 through 1000 and limit 1 through 50")
		}
		found, err := repository.NearbyPlaces(ctx, input.Lat, input.Lon, radius, limit)
		if err != nil {
			return nil, PlacesNearOutput{}, err
		}
		return nil, PlacesNearOutput{Places: found, Notes: locationNotes}, nil
	})
}
