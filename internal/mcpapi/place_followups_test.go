package mcpapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/places"
	"github.com/rimexe0/telemetry/internal/store"
)

type placeRepository struct {
	fakeRepository
	fixes []places.Fix
	found []store.MapPlace
	query store.PlaceQuery
}

func (r *placeRepository) LocationFixes(context.Context, time.Time, time.Time) ([]places.Fix, error) {
	return r.fixes, nil
}

func (r *placeRepository) SearchMapPlaces(_ context.Context, query store.PlaceQuery) ([]store.MapPlace, error) {
	r.query = query
	return r.found, nil
}

func TestResolvePlaceFindsHomeAndSearchesAroundIt(t *testing.T) {
	location := time.UTC
	now := time.Date(2026, 10, 10, 19, 0, 0, 0, location)
	repository := &placeRepository{fixes: []places.Fix{
		{At: time.Date(2026, 10, 9, 2, 0, 0, 0, location), Lat: 38.4601, Lon: 17.2201, Accuracy: 10},
		{At: time.Date(2026, 10, 9, 3, 0, 0, 0, location), Lat: 38.4602, Lon: 17.2202, Accuracy: 10},
		{At: time.Date(2026, 10, 10, 12, 0, 0, 0, location), Lat: 38.47, Lon: 17.23, Accuracy: 10},
	}}

	home, _, err := resolvePlace(context.Background(), repository, FollowUpPlaceInput{When: "arrive", Home: true}, now, location)
	if err != nil {
		t.Fatal(err)
	}
	if !home.Home || home.Name != "home" || len(home.Points) != 1 || home.Points[0].Radius != homeRadius || home.Points[0].Lat < 38.46 || home.Points[0].Lat > 38.4603 {
		t.Fatalf("home = %+v", home)
	}

	repository.found = []store.MapPlace{
		{Name: "Migros", Lat: 38.461, Lon: 17.221, Meters: 140, Details: map[string]string{"addr:street": "Market Street"}},
		{Name: "Migros", Lat: 38.5, Lon: 17.3, Meters: 8000, Box: &[4]float64{38.499, 17.299, 38.501, 17.301}},
	}
	shop, nearest, err := resolvePlace(context.Background(), repository, FollowUpPlaceInput{When: "leave", Name: "Migros"}, now, location)
	if err != nil {
		t.Fatal(err)
	}
	if shop.When != "leave" || len(shop.Points) != 2 || shop.Points[0].Radius != pointRadius || shop.Points[1].Radius <= pointRadius {
		t.Fatalf("shop = %+v", shop)
	}
	if !strings.Contains(nearest, "Market Street") || !strings.Contains(nearest, "140 m from home") {
		t.Fatalf("nearest = %q", nearest)
	}
	if repository.query.Lat < 38.46 || repository.query.Lat > 38.4603 || repository.query.Within != placeSearchMeters {
		t.Fatalf("searched around %+v, want home", repository.query)
	}

	if _, _, err := resolvePlace(context.Background(), repository, FollowUpPlaceInput{When: "arrive", Home: true, Kind: "pharmacy"}, now, location); err == nil {
		t.Fatal("home and a kind together should be refused")
	}
	repository.found = nil
	if _, _, err := resolvePlace(context.Background(), repository, FollowUpPlaceInput{When: "arrive", Kind: "pharmacy"}, now, location); err == nil {
		t.Fatal("a kind with no places nearby should be refused")
	}
}

func TestPlaceFollowUpIsDueOnlyOnceReached(t *testing.T) {
	now := time.Now()
	item := store.FollowUp{State: store.FollowUpOpen, DueAt: now.Add(-time.Minute), Place: &store.FollowUpPlace{When: "arrive"}}
	if item.Due(now) {
		t.Fatal("a place follow-up must wait for its place")
	}
	item.TriggeredAt = &now
	if !item.Due(now) {
		t.Fatal("a reached place follow-up is due")
	}
	item.DueAt = now.Add(time.Hour)
	if item.Due(now) {
		t.Fatal("a reached place follow-up still waits for its earliest time")
	}
}
