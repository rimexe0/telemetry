package places

import (
	"sort"
	"time"
)

// Interval is a stretch of time, From before To.
type Interval struct {
	From, To time.Time
}

const (
	// outsideGap is the longest gap between two positions outside that
	// still counts as outside throughout: the phone records nothing while
	// it stays still, such as sitting in a park.
	outsideGap = time.Hour
	// walkingSpeed is the fastest a stretch on foot moves, in meters per
	// second (about 9 km/h, a run included).
	walkingSpeed = 2.5
	// outsideAccuracy is the poorest position that counts.
	outsideAccuracy = 100.0
)

// OutsideIntervals are the stretches between consecutive positions that are
// both at least meters from home. With onFoot, stretches faster than walking
// are left out. The positions are one device's, oldest first, with nothing
// in between that is not this device's (a watch track is split where it was
// not worn).
func OutsideIntervals(fixes []Fix, home [2]float64, meters float64, onFoot bool) []Interval {
	intervals := []Interval{}
	outside := func(fix Fix) bool {
		return fix.Accuracy <= outsideAccuracy && Meters(fix.Lat, fix.Lon, home[0], home[1]) >= meters
	}
	for i := 1; i < len(fixes); i++ {
		a, b := fixes[i-1], fixes[i]
		gap := b.At.Sub(a.At)
		if gap <= 0 || gap > outsideGap || !outside(a) || !outside(b) {
			continue
		}
		if onFoot && Meters(a.Lat, a.Lon, b.Lat, b.Lon)/gap.Seconds() > walkingSpeed {
			continue
		}
		intervals = append(intervals, Interval{From: a.At, To: b.At})
	}
	return intervals
}

// Minutes is the total length of the intervals within [from, to), each
// moment counted once however many devices saw it.
func Minutes(intervals []Interval, from, to time.Time) float64 {
	clipped := []Interval{}
	for _, interval := range intervals {
		if interval.From.Before(from) {
			interval.From = from
		}
		if interval.To.After(to) {
			interval.To = to
		}
		if interval.To.After(interval.From) {
			clipped = append(clipped, interval)
		}
	}
	sort.Slice(clipped, func(i, j int) bool { return clipped[i].From.Before(clipped[j].From) })
	total := time.Duration(0)
	var current *Interval
	for i := range clipped {
		interval := clipped[i]
		if current != nil && !interval.From.After(current.To) {
			if interval.To.After(current.To) {
				current.To = interval.To
			}
			continue
		}
		if current != nil {
			total += current.To.Sub(current.From)
		}
		current = &interval
	}
	if current != nil {
		total += current.To.Sub(current.From)
	}
	return total.Minutes()
}
