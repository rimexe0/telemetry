// Package places turns the phone's recorded positions into stays and trips,
// and finds home, so an assistant can see where the owner went without
// asking them. The phone records a GPS position about once a minute, and
// only after moving 25 m or more: a stay shows as the last position before
// standing still and the first one after moving on. Indoors GPS is weak, so
// movement inside a building is not seen, and a short step outside may not
// be either.
package places

import (
	"math"
	"sort"
	"time"
)

// Fix is one recorded position.
type Fix struct {
	At       time.Time
	Lat, Lon float64
	Accuracy float64
}

// Stay is time spent in one spot.
type Stay struct {
	Start, End time.Time
	Lat, Lon   float64
	Fixes      int
	// Home means the stay is where the owner spends the nights.
	Home bool
}

// Minutes is the length of the stay.
func (s Stay) Minutes() float64 { return s.End.Sub(s.Start).Minutes() }

const (
	// Positions within StayRadius of a stay's first one belong to it.
	StayRadius = 150.0
	// A stay shorter than StayMin is passing through.
	StayMin = 5 * time.Minute
	// HomeRadius is how close to home a stay must be to count as home.
	HomeRadius = 150.0
)

// Meters is the distance between two points on the earth.
func Meters(lat1, lon1, lat2, lon2 float64) float64 {
	const radius = 6371000.0
	p1, p2 := lat1*math.Pi/180, lat2*math.Pi/180
	dp, dl := p2-p1, (lon2-lon1)*math.Pi/180
	a := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * radius * math.Asin(math.Sqrt(a))
}

// Stays groups positions, in time order, into stays of at least StayMin with
// at least two positions.
// Imprecise positions (over 100 m) are left out. A stay runs from its first
// position to the first position somewhere else, since the phone records
// nothing while standing still.
func Stays(fixes []Fix, home *[2]float64) []Stay {
	sorted := []Fix{}
	for _, fix := range fixes {
		if fix.Accuracy <= 100 {
			sorted = append(sorted, fix)
		}
	}
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At.Before(sorted[j].At) })
	stays := []Stay{}
	var current *Stay
	var sumLat, sumLon float64
	finish := func(end time.Time) {
		if current == nil {
			return
		}
		current.End = end
		current.Lat, current.Lon = sumLat/float64(current.Fixes), sumLon/float64(current.Fixes)
		// One position followed by a gap is not a stay: the gap may be GPS
		// lost on the way, not standing still.
		if current.End.Sub(current.Start) >= StayMin && current.Fixes >= 2 {
			if home != nil && Meters(current.Lat, current.Lon, home[0], home[1]) <= HomeRadius {
				current.Home = true
			}
			stays = append(stays, *current)
		}
		current = nil
	}
	for _, fix := range sorted {
		if current != nil && Meters(current.Lat, current.Lon, fix.Lat, fix.Lon) <= StayRadius {
			current.Fixes++
			sumLat, sumLon = sumLat+fix.Lat, sumLon+fix.Lon
			current.End = fix.At
			continue
		}
		if current != nil {
			// Moved on: the stay lasted until this position.
			finish(fix.At)
		}
		current = &Stay{Start: fix.At, End: fix.At, Lat: fix.Lat, Lon: fix.Lon, Fixes: 1}
		sumLat, sumLon = fix.Lat, fix.Lon
	}
	if current != nil {
		finish(current.End)
	}
	return stays
}

// Home is where the phone was most often between midnight and 06:00 local
// time, on a grid of about 100 m, or nil with no night positions.
func Home(fixes []Fix, location *time.Location) *[2]float64 {
	type cell struct{ lat, lon int }
	counts := map[cell]int{}
	sums := map[cell][3]float64{}
	for _, fix := range fixes {
		if fix.Accuracy > 100 || fix.At.In(location).Hour() >= 6 {
			continue
		}
		key := cell{int(math.Round(fix.Lat * 1000)), int(math.Round(fix.Lon * 1000))}
		counts[key]++
		sum := sums[key]
		sums[key] = [3]float64{sum[0] + fix.Lat, sum[1] + fix.Lon, sum[2] + 1}
	}
	best, most := cell{}, 0
	for key, count := range counts {
		if count > most || (count == most && (key.lat < best.lat || (key.lat == best.lat && key.lon < best.lon))) {
			best, most = key, count
		}
	}
	if most == 0 {
		return nil
	}
	sum := sums[best]
	return &[2]float64{sum[0] / sum[2], sum[1] / sum[2]}
}

// Distance is the length of the path through the positions, in meters.
func Distance(fixes []Fix) float64 {
	total := 0.0
	for i := 1; i < len(fixes); i++ {
		total += Meters(fixes[i-1].Lat, fixes[i-1].Lon, fixes[i].Lat, fixes[i].Lon)
	}
	return total
}
