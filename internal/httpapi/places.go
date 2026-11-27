package httpapi

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rimexe0/telemetry/internal/budget"
	"github.com/rimexe0/telemetry/internal/policy"
	"github.com/rimexe0/telemetry/internal/store"
)

type placesDayPosition struct {
	At       time.Time `json:"at"`
	Lat      float64   `json:"lat"`
	Lon      float64   `json:"lon"`
	Accuracy float64   `json:"accuracy"`
}

type placesDayStay struct {
	From   time.Time        `json:"from"`
	To     time.Time        `json:"to"`
	Lat    float64          `json:"lat"`
	Lon    float64          `json:"lon"`
	Home   bool             `json:"home"`
	Nearby []store.MapPlace `json:"nearby"`
}

type placesDayRule struct {
	HabitID int64   `json:"habit_id"`
	Habit   string  `json:"habit"`
	Name    string  `json:"name"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	RadiusM int     `json:"radius_m"`
}

// placesDay is the panel's map of one local day: the phone's positions,
// the stays worked out from them as for the places_visited tool, and the
// places the habits' rules ask for. Positions are all of them, imprecise
// ones too; the panel decides what to draw.
func (s *Server) placesDay(writer http.ResponseWriter, request *http.Request) {
	if !s.owner(writer, request) {
		return
	}
	config := policy.Config{}
	stored, err := s.repository.LatestPolicy(request.Context())
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	if stored != nil {
		config = stored.Config
	}
	if err := config.Normalize(); err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	location, err := time.LoadLocation(config.Timezone)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	now := time.Now().UTC()
	day := now.In(location)
	if value := request.URL.Query().Get("date"); value != "" {
		if day, err = time.ParseInLocation("2006-01-02", value, location); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_query", "date must be YYYY-MM-DD")
			return
		}
	}
	from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, location).UTC()
	to := time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, location).UTC()
	if to.After(now) {
		to = now
	}
	if !to.After(from) {
		writeError(writer, http.StatusBadRequest, "invalid_query", "date is in the future")
		return
	}
	visited, err := store.LoadVisited(request.Context(), s.repository, from, to, location)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	positions := make([]placesDayPosition, 0, len(visited.Positions))
	for _, fix := range visited.Positions {
		positions = append(positions, placesDayPosition{At: fix.At.UTC(), Lat: fix.Lat, Lon: fix.Lon, Accuracy: fix.Accuracy})
	}
	stays := make([]placesDayStay, 0, len(visited.Stays))
	for _, stay := range visited.Stays {
		stays = append(stays, placesDayStay{From: stay.Start.UTC(), To: stay.End.UTC(), Home: stay.Home, Nearby: stay.Nearby,
			Lat: math.Round(stay.Lat*1e5) / 1e5, Lon: math.Round(stay.Lon*1e5) / 1e5})
	}
	rules := []placesDayRule{}
	if budgets, ok := s.budgetStore(); ok {
		habits, err := budgets.ListHabits(request.Context())
		if err != nil {
			writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
			return
		}
		for _, habit := range habits {
			rule := habit.Rule
			if habit.ArchivedAt != nil || rule == nil || rule.Kind != budget.RuleKindPlace || rule.Place == nil {
				continue
			}
			rules = append(rules, placesDayRule{HabitID: habit.ID, Habit: habit.Name, Name: rule.Place.Name, Lat: rule.Place.Lat, Lon: rule.Place.Lon, RadiusM: rule.RadiusM})
		}
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, map[string]any{
		"date":      from.In(location).Format("2006-01-02"),
		"timezone":  location.String(),
		"from":      from,
		"to":        to,
		"positions": positions,
		"stays":     stays,
		"rules":     rules,
	})
}

// Map tiles for the Places map come through the server, so OpenStreetMap
// never sees the owner's panel address or their browser, only this server
// asking for tiles under a generic name. Tiles are kept in memory for a day.
const tileSource = "https://tile.openstreetmap.org"

type tileCache struct {
	mu    sync.Mutex
	items map[string]tileItem
	order []string
}

type tileItem struct {
	body []byte
	kind string
	at   time.Time
}

const tileCacheSize = 2000

var tiles = &tileCache{items: map[string]tileItem{}}

var tileClient = &http.Client{Timeout: 15 * time.Second}

func (s *Server) placesTile(writer http.ResponseWriter, request *http.Request) {
	if !s.owner(writer, request) {
		return
	}
	z, errZ := strconv.Atoi(request.PathValue("z"))
	x, errX := strconv.Atoi(request.PathValue("x"))
	y, errY := strconv.Atoi(strings.TrimSuffix(request.PathValue("y"), ".png"))
	if errZ != nil || errX != nil || errY != nil || z < 0 || z > 19 || x < 0 || y < 0 || x >= 1<<z || y >= 1<<z {
		writeError(writer, http.StatusBadRequest, "invalid_tile", "no such map tile")
		return
	}
	key := fmt.Sprintf("%d/%d/%d", z, x, y)
	tiles.mu.Lock()
	item, ok := tiles.items[key]
	tiles.mu.Unlock()
	if !ok || time.Since(item.at) > 24*time.Hour {
		outbound, err := http.NewRequestWithContext(request.Context(), http.MethodGet, tileSource+"/"+key+".png", nil)
		if err != nil {
			writeError(writer, http.StatusBadGateway, "tile_unavailable", err.Error())
			return
		}
		outbound.Header.Set("User-Agent", "Telemetry-selfhosted/1 (map tiles for its owner)")
		response, err := tileClient.Do(outbound)
		if err != nil {
			writeError(writer, http.StatusBadGateway, "tile_unavailable", "the map could not be reached")
			return
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK {
			writeError(writer, http.StatusBadGateway, "tile_unavailable", "the map refused this tile")
			return
		}
		item = tileItem{body: body, kind: response.Header.Get("Content-Type"), at: time.Now()}
		tiles.mu.Lock()
		if _, had := tiles.items[key]; !had {
			tiles.order = append(tiles.order, key)
		}
		tiles.items[key] = item
		for len(tiles.order) > tileCacheSize {
			delete(tiles.items, tiles.order[0])
			tiles.order = tiles.order[1:]
		}
		tiles.mu.Unlock()
	}
	if item.kind == "" {
		item.kind = "image/png"
	}
	writer.Header().Set("Content-Type", item.kind)
	writer.Header().Set("Cache-Control", "private, max-age=86400")
	writer.Write(item.body)
}
