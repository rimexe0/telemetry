package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/budget"
	"github.com/rimexe0/telemetry/internal/places"
	"github.com/rimexe0/telemetry/internal/store"
)

// placesRepository adds a made-up map to the budget repository.
type placesRepository struct {
	*budgetRepository
}

func (placesRepository) LatestMapImport(context.Context) (*store.MapImport, error) {
	return &store.MapImport{Source: "test", Places: 1, ImportedAt: time.Now().UTC()}, nil
}

func (placesRepository) NearbyPlaces(_ context.Context, lat, lon, radius float64, limit int) ([]store.MapPlace, error) {
	if places.Meters(lat, lon, 41.0, 29.0) > radius {
		return []store.MapPlace{}, nil
	}
	return []store.MapPlace{{OSMRef: "node/1", Name: "Test pier", Kind: "amenity=ferry_terminal", Lat: 41.0, Lon: 29.0, Meters: 3}}, nil
}

func placesServer(t *testing.T) (*Server, *budgetRepository) {
	t.Helper()
	_, inner := budgetServer(t, budgetPolicy(t))
	server, err := New(placesRepository{inner}, Config{
		IngestToken:     "ingest-secret",
		ReadToken:       "read-secret",
		OwnerAuthorized: func(request *http.Request) bool { return request.Header.Get("X-Test-Owner") == "yes" },
	})
	if err != nil {
		t.Fatal(err)
	}
	return server, inner
}

type placesDayBody struct {
	Date      string              `json:"date"`
	Timezone  string              `json:"timezone"`
	From      time.Time           `json:"from"`
	To        time.Time           `json:"to"`
	Positions []placesDayPosition `json:"positions"`
	Stays     []placesDayStay     `json:"stays"`
	Rules     []placesDayRule     `json:"rules"`
}

func TestPlacesDayHasPositionsStaysAndHabitPlaces(t *testing.T) {
	server, repository := placesServer(t)
	location, _ := time.LoadLocation("Europe/Istanbul")
	yesterday := time.Now().In(location).AddDate(0, 0, -1)
	at := func(hour, minute int) time.Time {
		return time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), hour, minute, 0, 0, location).UTC()
	}
	// Home is a made-up spot 2 km north of the pier.
	home := func(when time.Time) places.Fix { return places.Fix{At: when, Lat: 41.018, Lon: 29.0, Accuracy: 10} }
	fixes := []places.Fix{}
	for minute := 0; minute <= 8*60; minute += 30 {
		fixes = append(fixes, home(at(0, 0).Add(time.Duration(minute)*time.Minute)))
	}
	fixes = append(fixes,
		places.Fix{At: at(8, 10), Lat: 41.009, Lon: 29.0, Accuracy: 15},
		places.Fix{At: at(8, 12), Lat: 41.005, Lon: 29.0, Accuracy: 400},
		pierFix(at(8, 17), 0), pierFix(at(8, 20), 10), pierFix(at(8, 24), 5),
		home(at(8, 40)), home(at(9, 10)),
	)
	repository.fixes = fixes
	archived := time.Now().UTC()
	rule := &budget.HabitRule{Kind: budget.RuleKindPlace, Place: &budget.RulePlace{Name: "Test pier", Lat: 41.0, Lon: 29.0}, RadiusM: 100, Window: budget.RuleWindow{From: "wake", Until: "12:00"}, Minutes: 1}
	repository.habits = []budget.Habit{
		{ID: 1, Name: "Morning walk", Rule: rule},
		{ID: 2, Name: "Read"},
		{ID: 3, Name: "Old walk", Rule: rule, ArchivedAt: &archived},
	}

	day := yesterday.Format("2006-01-02")
	response := ownerCall(server, http.MethodGet, "/v1/places/day?date="+day, "")
	if response.Code != http.StatusOK {
		t.Fatalf("places day = %d %s", response.Code, response.Body)
	}
	var body placesDayBody
	decodeBody(t, response, &body)
	if body.Date != day || body.Timezone != "Europe/Istanbul" || !body.From.Equal(at(0, 0)) || !body.To.Equal(at(0, 0).AddDate(0, 0, 1)) {
		t.Fatalf("day = %s %s %s–%s", body.Date, body.Timezone, body.From, body.To)
	}
	// Every position of the day is sent, imprecise ones too.
	if len(body.Positions) != len(fixes) || body.Positions[len(fixes)-6].Accuracy != 400 {
		t.Fatalf("positions = %+v", body.Positions)
	}
	if len(body.Stays) != 3 {
		t.Fatalf("stays = %+v", body.Stays)
	}
	night, pier, back := body.Stays[0], body.Stays[1], body.Stays[2]
	if !night.Home || len(night.Nearby) != 0 || !night.From.Equal(at(0, 0)) || !night.To.Equal(at(8, 10)) {
		t.Fatalf("night = %+v", night)
	}
	if pier.Home || !pier.From.Equal(at(8, 17)) || !pier.To.Equal(at(8, 40)) || len(pier.Nearby) != 1 || pier.Nearby[0].Name != "Test pier" {
		t.Fatalf("pier = %+v", pier)
	}
	if !back.Home || !back.From.Equal(at(8, 40)) {
		t.Fatalf("back = %+v", back)
	}
	// Only habits with a place rule that are still kept.
	if len(body.Rules) != 1 || body.Rules[0] != (placesDayRule{HabitID: 1, Habit: "Morning walk", Name: "Test pier", Lat: 41.0, Lon: 29.0, RadiusM: 100}) {
		t.Fatalf("rules = %+v", body.Rules)
	}
}

func TestPlacesDayIsEmptyWithoutPositions(t *testing.T) {
	server, _ := placesServer(t)
	response := ownerCall(server, http.MethodGet, "/v1/places/day", "")
	if response.Code != http.StatusOK {
		t.Fatalf("places day = %d %s", response.Code, response.Body)
	}
	if got := response.Body.String(); !strings.Contains(got, `"positions":[]`) || !strings.Contains(got, `"stays":[]`) || !strings.Contains(got, `"rules":[]`) {
		t.Fatalf("empty day = %s", got)
	}
}

func TestPlacesDayNeedsTheOwnerAndARealPastDay(t *testing.T) {
	server, _ := placesServer(t)
	anonymous := deviceCall(server, http.MethodGet, "/v1/places/day", "")
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("without the owner = %d", anonymous.Code)
	}
	if bad := ownerCall(server, http.MethodGet, "/v1/places/day?date=yesterday", ""); bad.Code != http.StatusBadRequest {
		t.Fatalf("bad date = %d", bad.Code)
	}
	future := time.Now().AddDate(0, 0, 3).Format("2006-01-02")
	if later := ownerCall(server, http.MethodGet, "/v1/places/day?date="+future, ""); later.Code != http.StatusBadRequest {
		t.Fatalf("future date = %d", later.Code)
	}
}

func TestMapTilesComeThroughTheServerOnlyForTheOwner(t *testing.T) {
	server, _ := placesServer(t)
	unauthorized := httptest.NewRecorder()
	server.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/v1/places/tiles/1/0/0.png", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("without the owner: %d", unauthorized.Code)
	}
	// A tile outside the zoom level's grid is refused before anything is fetched.
	outside := httptest.NewRequest(http.MethodGet, "/v1/places/tiles/1/2/0.png", nil)
	outside.Header.Set("X-Test-Owner", "yes")
	refused := httptest.NewRecorder()
	server.ServeHTTP(refused, outside)
	if refused.Code != http.StatusBadRequest {
		t.Fatalf("outside the grid: %d", refused.Code)
	}
}
