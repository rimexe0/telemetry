// telemetry-places loads named places from an OpenStreetMap extract into the
// map_places table, so the owner's stays can be matched to what is there on
// their own server. It downloads the extract to a temporary file, reads it
// twice (outlines, then points), replaces the table in one transaction and
// deletes the download. Run it monthly.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/paulmach/osm"
	"github.com/paulmach/osm/osmpbf"
	"github.com/rimexe0/telemetry/internal/store"
)

// Tags that make something a place, in order of preference for its kind.
var placeKeys = []string{"amenity", "shop", "healthcare", "tourism", "leisure", "office", "craft", "public_transport", "railway", "historic", "sport", "building"}

// Tags kept as details.
var detailKeys = []string{"brand", "operator", "cuisine", "opening_hours", "healthcare:speciality", "website",
	"addr:street", "addr:housenumber", "addr:neighbourhood", "addr:district", "addr:city"}

func kindOf(tags osm.Tags) string {
	for _, key := range placeKeys {
		value := tags.Find(key)
		switch {
		case value == "":
		case key == "building" && value == "yes":
			return "building"
		case key == "railway" && value != "station" && value != "tram_stop" && value != "halt" && value != "subway_entrance":
		case key == "public_transport" && value == "stop_position":
		default:
			return key + "=" + value
		}
	}
	return ""
}

func details(tags osm.Tags) map[string]string {
	result := map[string]string{}
	for _, key := range detailKeys {
		if value := tags.Find(key); value != "" {
			result[key] = value
		}
	}
	return result
}

func main() {
	source := flag.String("url", "https://download.geofabrik.de/europe/turkey-latest.osm.pbf", "OpenStreetMap PBF extract to download")
	file := flag.String("file", "", "read this PBF file instead of downloading")
	dir := flag.String("dir", os.TempDir(), "where the download is kept while reading")
	databaseURL := flag.String("database-url", os.Getenv("TELEMETRY_DATABASE_URL"), "PostgreSQL connection URL")
	flag.Parse()
	if *databaseURL == "" {
		log.Fatal("TELEMETRY_DATABASE_URL or -database-url is required")
	}
	ctx := context.Background()
	path, origin := *file, *file
	if path == "" {
		downloaded, err := download(ctx, *source, *dir)
		if err != nil {
			log.Fatal(err)
		}
		defer os.Remove(downloaded)
		path, origin = downloaded, *source
	}
	found, err := read(ctx, path)
	if err != nil {
		log.Fatal(err)
	}
	repository, err := store.OpenPostgres(ctx, *databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer repository.Close()
	if err := repository.ReplaceMapPlaces(ctx, origin, found); err != nil {
		log.Fatal(err)
	}
	log.Printf("imported %d places from %s", len(found), origin)
}

func download(ctx context.Context, url, dir string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	response, err := (&http.Client{Timeout: time.Hour}).Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: %s", url, response.Status)
	}
	out, err := os.CreateTemp(dir, "osm-*.pbf")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, response.Body); err != nil {
		out.Close()
		os.Remove(out.Name())
		return "", err
	}
	return filepath.Clean(out.Name()), out.Close()
}

type outline struct {
	ref, name, kind string
	details         map[string]string
	nodes           []osm.NodeID
}

// read finds named places: points directly, outlines (shops and buildings
// drawn as shapes) at the average of their corners.
func read(ctx context.Context, path string) ([]store.MapPlace, error) {
	outlines := []outline{}
	needed := map[osm.NodeID][2]float64{}
	if err := scan(ctx, path, false, func(object osm.Object) {
		way := object.(*osm.Way)
		name, kind := strings.TrimSpace(way.Tags.Find("name")), kindOf(way.Tags)
		if name == "" || kind == "" {
			return
		}
		item := outline{ref: fmt.Sprintf("way/%d", way.ID), name: name, kind: kind, details: details(way.Tags)}
		for _, node := range way.Nodes {
			item.nodes = append(item.nodes, node.ID)
			needed[node.ID] = [2]float64{}
		}
		outlines = append(outlines, item)
	}); err != nil {
		return nil, err
	}
	found := []store.MapPlace{}
	if err := scan(ctx, path, true, func(object osm.Object) {
		node := object.(*osm.Node)
		if _, ok := needed[node.ID]; ok {
			needed[node.ID] = [2]float64{node.Lat, node.Lon}
		}
		name, kind := strings.TrimSpace(node.Tags.Find("name")), kindOf(node.Tags)
		if name == "" || kind == "" {
			return
		}
		found = append(found, store.MapPlace{OSMRef: fmt.Sprintf("node/%d", node.ID), Name: name, Kind: kind, Details: details(node.Tags), Lat: node.Lat, Lon: node.Lon})
	}); err != nil {
		return nil, err
	}
	for _, item := range outlines {
		if len(item.nodes) == 0 {
			continue
		}
		var lat, lon float64
		box := [4]float64{90, 180, -90, -180}
		for _, id := range item.nodes {
			at := needed[id]
			lat, lon = lat+at[0], lon+at[1]
			box = [4]float64{math.Min(box[0], at[0]), math.Min(box[1], at[1]), math.Max(box[2], at[0]), math.Max(box[3], at[1])}
		}
		count := float64(len(item.nodes))
		found = append(found, store.MapPlace{OSMRef: item.ref, Name: item.name, Kind: item.kind, Details: item.details, Lat: lat / count, Lon: lon / count, Box: &box})
	}
	return found, nil
}

func scan(ctx context.Context, path string, nodes bool, visit func(osm.Object)) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := osmpbf.New(ctx, file, runtime.GOMAXPROCS(-1))
	defer scanner.Close()
	scanner.SkipRelations = true
	scanner.SkipNodes, scanner.SkipWays = !nodes, nodes
	for scanner.Scan() {
		visit(scanner.Object())
	}
	return scanner.Err()
}
