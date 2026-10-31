package policy

import (
	"reflect"
	"sort"
	"time"
)

// Segment is one stretch of time with a single decision.
type Segment struct {
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Decision Decision  `json:"decision"`
}

// Timeline evaluates every point where the known facts can change the
// decision and merges equal neighbours. It assumes the facts stay as given;
// a device follows it offline and replaces it when fresh facts arrive.
func Timeline(config Config, version int64, facts Facts, from, to time.Time) []Segment {
	if !to.After(from) {
		return []Segment{}
	}
	points := map[int64]time.Time{from.UnixNano(): from}
	add := func(t time.Time) {
		if t.After(from) && t.Before(to) {
			points[t.UnixNano()] = t
		}
	}
	for _, block := range facts.Blocks {
		add(block.Start)
		add(block.End)
	}
	for _, gate := range facts.Gates {
		add(gate.ActiveFrom)
		add(gate.Until)
	}
	if facts.Urgent != nil {
		add(facts.Urgent.Until)
	}
	for _, opening := range facts.Openings {
		add(opening.From)
		add(opening.Until)
	}
	if facts.MorningScannedAt != nil {
		add(*facts.MorningScannedAt)
	}
	if facts.Emergency != nil {
		add(facts.Emergency.StartedAt)
		add(facts.Emergency.Until)
	}
	if facts.WakeAt != nil {
		add(*facts.WakeAt)
	}
	if facts.BreakSince != nil {
		add(*facts.BreakSince)
	}
	if config.PlanGate.Enabled && config.PlanGate.EndsAt != "" {
		if location, err := time.LoadLocation(config.Timezone); err == nil {
			for day := from.In(location); !day.After(to); day = day.AddDate(0, 0, 1) {
				if end, err := time.ParseInLocation("2006-01-02 15:04", day.Format("2006-01-02")+" "+config.PlanGate.EndsAt, location); err == nil {
					add(end)
				}
			}
		}
	}
	if location, err := time.LoadLocation(config.Timezone); err == nil {
		// The outside rule's hours, and the day a looser rule starts.
		for _, rule := range []OutsideRule{config.Outside, derefOutside(config.OutsideNext)} {
			for day := from.In(location); !day.After(to); day = day.AddDate(0, 0, 1) {
				for _, clock := range []string{rule.From, rule.Until} {
					if clock == "" {
						continue
					}
					if t, err := time.ParseInLocation("2006-01-02 15:04", day.Format("2006-01-02")+" "+clock, location); err == nil {
						add(t)
					}
				}
			}
		}
		// The morning rule's hours, and the days looser rules start.
		for _, rule := range []MorningRule{config.Morning, derefMorning(config.MorningNext)} {
			for day := from.In(location); !day.After(to); day = day.AddDate(0, 0, 1) {
				for _, clock := range []string{rule.From, rule.Until} {
					if clock == "" {
						continue
					}
					if t, err := time.ParseInLocation("2006-01-02 15:04", day.Format("2006-01-02")+" "+clock, location); err == nil {
						add(t)
					}
				}
			}
		}
		for _, rule := range []Bedtime{config.Bedtime, derefBedtime(config.BedtimeNext)} {
			// A wind-down before a midnight bedtime starts the day before it.
			for day := from.In(location).AddDate(0, 0, -1); !day.After(to.AddDate(0, 0, 1)); day = day.AddDate(0, 0, 1) {
				for _, clock := range []string{rule.Start, rule.End} {
					if clock == "" {
						continue
					}
					if t, err := time.ParseInLocation("2006-01-02 15:04", day.Format("2006-01-02")+" "+clock, location); err == nil {
						add(t)
						// The wind-down starts before bedtime.
						if clock == rule.Start && rule.WindDownMinutes > 0 {
							add(t.Add(-time.Duration(rule.WindDownMinutes) * time.Minute))
						}
					}
				}
			}
		}
		for _, date := range []string{config.OutsideNextFrom, config.MorningNextFrom, config.BedtimeNextFrom, config.ExtensionNextFrom} {
			if date == "" {
				continue
			}
			if t, err := time.ParseInLocation("2006-01-02", date, location); err == nil {
				add(t)
			}
		}
		if config.ClosedByDefault {
			for day := from; !day.After(to.Add(24 * time.Hour)); day = day.Add(24 * time.Hour) {
				for _, bound := range windowBounds(config, location, day) {
					add(bound[0])
					add(bound[1])
				}
			}
		}
		// A reason that says "tomorrow 10:35" says "10:35" from midnight on.
		for day := from.In(location); !day.After(to); day = day.AddDate(0, 0, 1) {
			add(time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, location).AddDate(0, 0, 1))
		}
	}
	ordered := make([]time.Time, 0, len(points))
	for _, point := range points {
		ordered = append(ordered, point)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Before(ordered[j]) })

	segments := []Segment{}
	for i, start := range ordered {
		end := to
		if i+1 < len(ordered) {
			end = ordered[i+1]
		}
		decision := Evaluate(config, version, facts, start)
		if n := len(segments); n > 0 && sameDecision(segments[n-1].Decision, decision) {
			segments[n-1].To = end
			continue
		}
		segments = append(segments, Segment{From: start.UTC(), To: end.UTC(), Decision: decision})
	}
	return segments
}

func sameDecision(a, b Decision) bool {
	a.At, b.At = time.Time{}, time.Time{}
	return reflect.DeepEqual(a, b)
}

func derefOutside(rule *OutsideRule) OutsideRule {
	if rule == nil {
		return OutsideRule{}
	}
	return *rule
}

func derefMorning(rule *MorningRule) MorningRule {
	if rule == nil {
		return MorningRule{}
	}
	return *rule
}

func derefBedtime(rule *Bedtime) Bedtime {
	if rule == nil {
		return Bedtime{}
	}
	return *rule
}
