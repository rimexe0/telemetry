package activity

import (
	"encoding/json"
	"sort"
	"time"
)

// HealthKinds are the observation kinds HealthDays reads.
var HealthKinds = []string{
	"health_connect.steps", "health_connect.heart_rate", "health_connect.sleep_session",
	"samsung_health.steps", "samsung_health.heart_rate", "samsung_health.sleep",
}

// HealthDay is one local day of sleep, steps and heart rate. Several sources
// report the same steps and nights (the phone and Samsung Health through
// Health Connect, and Samsung Health directly), so they are never added up:
// steps give each source's count and the highest as the day's figure, and
// overlapping nights count once.
type HealthDay struct {
	Date string `json:"date"`
	// Sleep is the night that ended this day, if any was recorded.
	Sleep *Night `json:"sleep,omitempty"`
	// Steps is the highest count of any one source; nil without data.
	Steps         *int           `json:"steps,omitempty"`
	StepsBySource map[string]int `json:"steps_by_source,omitempty"`
	HeartRate     *HeartRateDay  `json:"heart_rate,omitempty"`
}

// Night is a sleep session as its sources recorded it.
type Night struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	// InBedMinutes runs from start to end; AsleepMinutes leaves out stages
	// marked awake or out of bed, and equals InBedMinutes without stages.
	InBedMinutes  float64  `json:"in_bed_minutes"`
	AsleepMinutes float64  `json:"asleep_minutes"`
	Sources       []string `json:"sources"`
}

// HeartRateDay summarises the day's heart-rate samples from one source: the
// one with the most samples.
type HeartRateDay struct {
	Min     int    `json:"min"`
	Median  int    `json:"median"`
	Max     int    `json:"max"`
	Samples int    `json:"samples"`
	Source  string `json:"source"`
}

// HealthDays builds a row for every local day in [from, to) in location.
func HealthDays(records []Record, from, to time.Time, location *time.Location) []HealthDay {
	steps := map[string]map[string]int{}   // date -> source -> count
	beats := map[string]map[string][]int{} // date -> source -> samples
	nights := []Night{}
	day := func(at time.Time) string { return at.In(location).Format("2006-01-02") }
	addSteps := func(at time.Time, source string, count int) {
		if count <= 0 {
			return
		}
		date := day(at)
		if steps[date] == nil {
			steps[date] = map[string]int{}
		}
		steps[date][source] += count
	}
	addBeat := func(at time.Time, source string, bpm int) {
		if bpm <= 0 {
			return
		}
		date := day(at)
		if beats[date] == nil {
			beats[date] = map[string][]int{}
		}
		beats[date][source] = append(beats[date][source], bpm)
	}
	for _, record := range records {
		switch record.Kind {
		case "health_connect.steps":
			var payload struct {
				Count    int       `json:"count"`
				Start    time.Time `json:"start_time"`
				Metadata struct {
					Origin string `json:"data_origin"`
				} `json:"metadata"`
			}
			if json.Unmarshal(record.Payload, &payload) == nil {
				addSteps(payload.Start, "health_connect:"+originName(payload.Metadata.Origin), payload.Count)
			}
		case "samsung_health.steps":
			var payload struct {
				Minutes []struct {
					Count int       `json:"count"`
					Start time.Time `json:"start"`
				} `json:"minutes"`
			}
			if json.Unmarshal(record.Payload, &payload) == nil {
				for _, minute := range payload.Minutes {
					addSteps(minute.Start, "samsung_health", minute.Count)
				}
			}
		case "health_connect.heart_rate":
			var payload struct {
				Samples []struct {
					Time time.Time `json:"time"`
					BPM  int       `json:"beats_per_minute"`
				} `json:"samples"`
			}
			if json.Unmarshal(record.Payload, &payload) == nil {
				for _, sample := range payload.Samples {
					addBeat(sample.Time, "health_connect", sample.BPM)
				}
			}
		case "samsung_health.heart_rate":
			var payload struct {
				Series []struct {
					BPM   int       `json:"bpm"`
					Start time.Time `json:"start"`
				} `json:"series"`
			}
			if json.Unmarshal(record.Payload, &payload) == nil {
				for _, sample := range payload.Series {
					addBeat(sample.Start, "samsung_health", sample.BPM)
				}
			}
		case "health_connect.sleep_session":
			var payload struct {
				Start  time.Time `json:"start_time"`
				End    time.Time `json:"end_time"`
				Stages []struct {
					Stage int       `json:"stage"`
					Start time.Time `json:"start_time"`
					End   time.Time `json:"end_time"`
				} `json:"stages"`
			}
			if json.Unmarshal(record.Payload, &payload) == nil && payload.End.After(payload.Start) {
				awake := 0.0
				for _, stage := range payload.Stages {
					// Health Connect: 1 awake, 3 out of bed, 7 awake in bed.
					if stage.Stage == 1 || stage.Stage == 3 || stage.Stage == 7 {
						awake += stage.End.Sub(stage.Start).Minutes()
					}
				}
				nights = append(nights, night(payload.Start, payload.End, awake, "health_connect"))
			}
		case "samsung_health.sleep":
			var payload struct {
				Sessions []struct {
					Start  time.Time `json:"start"`
					End    time.Time `json:"end"`
					Stages []struct {
						Stage string    `json:"stage"`
						Start time.Time `json:"start"`
						End   time.Time `json:"end"`
					} `json:"stages"`
				} `json:"sessions"`
			}
			if json.Unmarshal(record.Payload, &payload) == nil {
				for _, session := range payload.Sessions {
					if !session.End.After(session.Start) {
						continue
					}
					awake := 0.0
					for _, stage := range session.Stages {
						if stage.Stage == "AWAKE" {
							awake += stage.End.Sub(stage.Start).Minutes()
						}
					}
					nights = append(nights, night(session.Start, session.End, awake, "samsung_health"))
				}
			}
		}
	}
	byDate := map[string]Night{}
	for _, merged := range mergeNights(nights) {
		date := day(merged.End)
		// Two separate sleeps ending the same day: the longer is the night.
		if known, ok := byDate[date]; !ok || merged.InBedMinutes > known.InBedMinutes {
			byDate[date] = merged
		}
	}
	days := []HealthDay{}
	start := from.In(location)
	for current := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, location); current.Before(to); current = current.AddDate(0, 0, 1) {
		date := current.Format("2006-01-02")
		row := HealthDay{Date: date}
		if sleep, ok := byDate[date]; ok {
			row.Sleep = &sleep
		}
		if counts := steps[date]; len(counts) > 0 {
			best := 0
			for _, count := range counts {
				best = max(best, count)
			}
			row.Steps, row.StepsBySource = &best, counts
		}
		if sources := beats[date]; len(sources) > 0 {
			source, samples := "", []int(nil)
			for name, values := range sources {
				if len(values) > len(samples) || (len(values) == len(samples) && name < source) {
					source, samples = name, values
				}
			}
			sorted := append([]int(nil), samples...)
			sort.Ints(sorted)
			row.HeartRate = &HeartRateDay{Min: sorted[0], Median: sorted[len(sorted)/2], Max: sorted[len(sorted)-1], Samples: len(sorted), Source: source}
		}
		days = append(days, row)
	}
	return days
}

func night(start, end time.Time, awake float64, source string) Night {
	inBed := end.Sub(start).Minutes()
	return Night{Start: start.UTC(), End: end.UTC(), InBedMinutes: round(inBed), AsleepMinutes: round(max(inBed-awake, 0)), Sources: []string{source}}
}

// mergeNights joins sessions that overlap, which are the same night from
// different sources: the longest session's times stand, the sources are
// listed together.
func mergeNights(nights []Night) []Night {
	sort.Slice(nights, func(i, j int) bool { return nights[i].Start.Before(nights[j].Start) })
	merged := []Night{}
	for _, current := range nights {
		if last := len(merged) - 1; last >= 0 && current.Start.Before(merged[last].End) {
			group := merged[last]
			if current.InBedMinutes > group.InBedMinutes {
				current.Sources = append(group.Sources, current.Sources...)
				group = current
			} else {
				group.Sources = append(group.Sources, current.Sources...)
			}
			group.Sources = uniqueStrings(group.Sources)
			merged[last] = group
			continue
		}
		merged = append(merged, current)
	}
	return merged
}

func uniqueStrings(values []string) []string {
	seen, result := map[string]bool{}, []string{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

// originName shortens a Health Connect data origin to the app it names.
func originName(origin string) string {
	switch {
	case origin == "com.sec.android.app.shealth":
		return "samsung_health"
	case len(origin) > len("com.android.healthconnect.phone") && origin[:len("com.android.healthconnect.phone")] == "com.android.healthconnect.phone":
		return "phone"
	case origin == "":
		return "unknown"
	}
	return origin
}
