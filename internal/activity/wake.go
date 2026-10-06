package activity

import (
	"sort"
	"time"
)

// A stretch this long with no use of any device is read as sleep.
const sleepGap = 3 * time.Hour

// Waking is use that goes on: wakeUse within wakeWithin of its start. A
// glance at the phone in the night (the time, an alarm) is part of sleep;
// on 2026-10-06 a few seconds at 07:00 sent the morning message two hours
// before the owner got up.
const (
	wakeUse    = 2 * time.Minute
	wakeWithin = 15 * time.Minute
)

// WakeLookback is how far back the records are read to find when the owner
// woke: a long day plus the sleep before it.
const WakeLookback = 20 * time.Hour

// WakeTime returns when the owner last started using a device after a long
// quiet stretch in result: the start of their day, which is not midnight for
// someone who is up past it. It is nil when there was no such stretch, and
// when the quiet stretch is one the phone has no data for: a collector that
// was off is not a person asleep.
func WakeTime(result Result, from, to time.Time) *time.Time {
	segments := append([]Segment(nil), result.Segments...)
	sort.SliceStable(segments, func(i, j int) bool { return segments[i].Start.Before(segments[j].Start) })
	var wake *time.Time
	// What came before the first record is not known, so it is not a quiet stretch.
	var quiet time.Time
	for index, segment := range segments {
		if index > 0 && segment.Start.Sub(quiet) >= sleepGap && !missing(result.Gaps, quiet, segment.Start) {
			if !sustained(segments[index:], segment.Start) {
				// A glance: the quiet stretch goes on.
				continue
			}
			at := segment.Start
			wake = &at
		}
		quiet = later(quiet, segment.End)
	}
	return wake
}

// missing reports whether the phone has no data for more than half an hour
// of a stretch.
func missing(gaps []Gap, from, to time.Time) bool {
	var total time.Duration
	for _, gap := range gaps {
		if gap.Device != DevicePhone {
			continue
		}
		if start, end := later(gap.Start, from), earlier(gap.End, to); end.After(start) {
			total += end.Sub(start)
		}
	}
	return total > 30*time.Minute
}

// sustained reports whether segments, in start order from start, hold at
// least wakeUse of use within wakeWithin.
func sustained(segments []Segment, start time.Time) bool {
	limit := start.Add(wakeWithin)
	var used time.Duration
	for _, segment := range segments {
		if !segment.Start.Before(limit) {
			break
		}
		used += earlier(segment.End, limit).Sub(segment.Start)
	}
	return used >= wakeUse
}
