package store

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rimexe0/telemetry/internal/places"
)

// MapPlace is a named place from OpenStreetMap: a shop, café, hospital, stop
// and so on. Details holds what the map says about it, such as brand,
// cuisine, address and opening hours.
type MapPlace struct {
	OSMRef  string            `json:"osm_ref"`
	Name    string            `json:"name"`
	Kind    string            `json:"kind"`
	Details map[string]string `json:"details,omitempty"`
	Lat     float64           `json:"lat"`
	Lon     float64           `json:"lon"`
	// Box is the outline's bounding box (min lat, min lon, max lat, max
	// lon) for a place drawn as a shape; nil for a point.
	Box *[4]float64 `json:"-"`
	// Meters is the distance from the point asked about: to the outline's
	// box for a shape, so 0 when inside it.
	Meters float64 `json:"meters"`
}

// MapImport is the last time the places were refreshed.
type MapImport struct {
	Source     string    `json:"source"`
	Places     int       `json:"places"`
	ImportedAt time.Time `json:"imported_at"`
}

// ReplaceMapPlaces swaps in a new set of places in one transaction, so
// readers see either the old set or the new one.
func (p *Postgres) ReplaceMapPlaces(ctx context.Context, source string, imported []MapPlace) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM map_places`); err != nil {
		return err
	}
	rows := make([][]any, 0, len(imported))
	for _, place := range imported {
		details, err := json.Marshal(place.Details)
		if err != nil {
			return err
		}
		row := []any{place.OSMRef, place.Name, place.Kind, string(details), place.Lat, place.Lon, nil, nil, nil, nil}
		if box := place.Box; box != nil {
			row[6], row[7], row[8], row[9] = box[0], box[2], box[1], box[3]
		}
		rows = append(rows, row)
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"map_places"}, []string{"osm_ref", "name", "kind", "details", "lat", "lon", "min_lat", "max_lat", "min_lon", "max_lon"},
		pgx.CopyFromRows(rows)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO map_imports (source, places) VALUES ($1, $2)`, source, len(imported)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// metersPerDegree is the length of a degree of latitude.
const metersPerDegree = 111320.0

// NearbyPlaces lists the places within radius meters of a point, nearest
// first.
func (p *Postgres) NearbyPlaces(ctx context.Context, lat, lon, radius float64, limit int) ([]MapPlace, error) {
	dLat := radius / metersPerDegree
	dLon := radius / (metersPerDegree * math.Max(math.Cos(lat*math.Pi/180), 0.01))
	// Points near the spot, and outlines whose box comes near it.
	rows, err := p.pool.Query(ctx, `SELECT osm_ref, name, kind, details, lat, lon, min_lat, max_lat, min_lon, max_lon FROM map_places
		WHERE (lat BETWEEN $1 AND $2 AND lon BETWEEN $3 AND $4)
		   OR (min_lat IS NOT NULL AND min_lat <= $2 AND max_lat >= $1 AND min_lon <= $4 AND max_lon >= $3)`, lat-dLat, lat+dLat, lon-dLon, lon+dLon)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found := []MapPlace{}
	for rows.Next() {
		var place MapPlace
		var details []byte
		var minLat, maxLat, minLon, maxLon *float64
		if err := rows.Scan(&place.OSMRef, &place.Name, &place.Kind, &details, &place.Lat, &place.Lon, &minLat, &maxLat, &minLon, &maxLon); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(details, &place.Details)
		toLat, toLon := place.Lat, place.Lon
		if minLat != nil && maxLat != nil && minLon != nil && maxLon != nil {
			// The nearest point of the box: the spot itself when inside.
			toLat, toLon = math.Min(math.Max(lat, *minLat), *maxLat), math.Min(math.Max(lon, *minLon), *maxLon)
		}
		if place.Meters = math.Round(places.Meters(lat, lon, toLat, toLon)); place.Meters <= radius {
			found = append(found, place)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].Meters < found[j].Meters })
	if len(found) > limit {
		found = found[:limit]
	}
	return found, nil
}

// LatestMapImport is nil before the first import.
func (p *Postgres) LatestMapImport(ctx context.Context) (*MapImport, error) {
	var item MapImport
	err := p.pool.QueryRow(ctx, `SELECT source, places, imported_at FROM map_imports ORDER BY id DESC LIMIT 1`).Scan(&item.Source, &item.Places, &item.ImportedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// LocationFixes are the phones' recorded positions in [from, to), oldest
// first.
func (p *Postgres) LocationFixes(ctx context.Context, from, to time.Time) ([]places.Fix, error) {
	rows, err := p.pool.Query(ctx, `SELECT o.occurred_at, r.projection FROM observations o
		JOIN observation_revisions r ON r.id = o.effective_revision_id
		WHERE o.source_instance_key LIKE 'android:%:location' AND o.occurred_at >= $1 AND o.occurred_at < $2
		ORDER BY o.occurred_at`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fixes := []places.Fix{}
	for rows.Next() {
		var at time.Time
		var raw []byte
		if err := rows.Scan(&at, &raw); err != nil {
			return nil, err
		}
		var payload struct {
			Time     string   `json:"location_time"`
			Lat      *float64 `json:"latitude"`
			Lon      *float64 `json:"longitude"`
			Accuracy float64  `json:"accuracy_meters"`
		}
		if json.Unmarshal(raw, &payload) != nil || payload.Lat == nil || payload.Lon == nil {
			continue
		}
		if parsed, err := time.Parse(time.RFC3339Nano, payload.Time); err == nil {
			at = parsed
		}
		fixes = append(fixes, places.Fix{At: at.UTC(), Lat: *payload.Lat, Lon: *payload.Lon, Accuracy: payload.Accuracy})
	}
	return fixes, rows.Err()
}
