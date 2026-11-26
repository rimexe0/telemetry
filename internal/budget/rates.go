package budget

import (
	"sort"
	"strings"
	"time"

	"github.com/rimexe0/telemetry/internal/activity"
	"github.com/rimexe0/telemetry/internal/policy"
)

const (
	RateKindDay         = "day"
	RateKindDistraction = "distraction"
	RateKindBedtime     = "bedtime"

	GrayscaleDecline = "decline"
	GrayscaleRestore = "restore"
)

// Rate is what a minute of distraction use costs at an instant: the base
// rate by time of day plus the surcharge while grayscale is declined.
type Rate struct {
	Total        float64 `json:"total"`
	Base         float64 `json:"base"`
	GrayscaleOff float64 `json:"grayscale_off"`
	Kind         string  `json:"kind"`
}

// GrayscaleEvent is a device reporting that the owner kept grayscale off
// while it was wanted (decline), or turned it back on (restore).
type GrayscaleEvent struct {
	ID       int64     `json:"id"`
	At       time.Time `json:"at"`
	Action   string    `json:"action"`
	Platform string    `json:"platform"`
	Source   string    `json:"source,omitempty"`
}

type span struct{ start, end time.Time }

func minutes(d time.Duration) float64 { return d.Minutes() }

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// declines are the stretches the grayscale surcharge applies: from a decline
// while grayscale is wanted until it is restored or no longer wanted (the
// end of that night's bedtime).
func declines(config policy.Config, events []GrayscaleEvent) []span {
	ordered := append([]GrayscaleEvent{}, events...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].At.Before(ordered[j].At) })
	result := []span{}
	for i, event := range ordered {
		if event.Action != GrayscaleDecline || !policy.GrayscaleWanted(config, event.At) {
			continue
		}
		end := wantedUntil(config, event.At)
		for _, next := range ordered[i+1:] {
			if next.Action == GrayscaleRestore && next.At.Before(end) {
				end = next.At
				break
			}
		}
		if n := len(result); n > 0 && !event.At.After(result[n-1].end) {
			result[n-1].end = later(result[n-1].end, end)
			continue
		}
		result = append(result, span{event.At, end})
	}
	return result
}

// wantedUntil is when the grayscale stretch at at ends: the end of bedtime
// after the wind-down.
func wantedUntil(config policy.Config, at time.Time) time.Time {
	if end, in := policy.BedtimeEnds(config, at); in {
		if config.BedtimeAt(at).WindDownMinutes > 0 {
			return end
		}
		return at
	}
	// Winding down: grayscale stays wanted through the bedtime that follows.
	for t := at; t.Before(at.Add(4 * time.Hour)); t = t.Add(time.Minute).Truncate(time.Minute) {
		if !policy.WindingDown(config, t) {
			if end, in := policy.BedtimeEnds(config, t); in {
				return end
			}
			return t
		}
	}
	return at.Add(4 * time.Hour)
}

// ratePiece is a stretch with one rate.
type ratePiece struct {
	from, to time.Time
	rate     Rate
}

// rates splits [from, to) at every moment the rate can change, so the rest
// of the package never evaluates the policy minute by minute.
func rates(config policy.Config, surcharges []span, from, to time.Time) []ratePiece {
	zone := location(config)
	points := map[int64]time.Time{from.UnixNano(): from}
	add := func(t time.Time) {
		if t.After(from) && t.Before(to) {
			points[t.UnixNano()] = t
		}
	}
	for date := from.In(zone).AddDate(0, 0, -1); !date.After(to.In(zone).AddDate(0, 0, 1)); date = date.AddDate(0, 0, 1) {
		day := date.Format("2006-01-02")
		clocks := []string{}
		for _, bedtime := range []policy.Bedtime{config.Bedtime, deref(config.BedtimeNext)} {
			clocks = append(clocks, bedtime.Start, bedtime.End)
		}
		for _, window := range config.DistractionTimes {
			clocks = append(clocks, window.Start, window.End)
		}
		for _, clock := range clocks {
			if t, err := time.ParseInLocation("2006-01-02 15:04", day+" "+clock, zone); err == nil {
				add(t)
			}
		}
	}
	for _, moment := range []string{config.BedtimeNextFrom, config.BudgetNextFrom} {
		if t, err := time.Parse(time.RFC3339, moment); err == nil {
			add(t)
		} else if t, err := time.ParseInLocation("2006-01-02", moment, zone); err == nil {
			add(t)
		}
	}
	for _, s := range surcharges {
		add(s.start)
		add(s.end)
	}
	ordered := make([]time.Time, 0, len(points))
	for _, point := range points {
		ordered = append(ordered, point)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Before(ordered[j]) })
	pieces := make([]ratePiece, 0, len(ordered))
	for i, start := range ordered {
		end := to
		if i+1 < len(ordered) {
			end = ordered[i+1]
		}
		pieces = append(pieces, ratePiece{start, end, rateAt(config, surcharges, start)})
	}
	return pieces
}

func deref(rule *policy.Bedtime) policy.Bedtime {
	if rule == nil {
		return policy.Bedtime{}
	}
	return *rule
}

// rateAt is the rate in force at an instant: bedtime, then distraction time,
// then the day, plus the grayscale surcharge.
func rateAt(config policy.Config, surcharges []span, at time.Time) Rate {
	settings := config.BudgetAt(at)
	rate := Rate{Kind: RateKindDay, Base: settings.RateDay}
	switch {
	case policy.InBedtime(config, at):
		rate.Kind, rate.Base = RateKindBedtime, settings.RateBedtime
	case policy.DistractionTime(config, at):
		rate.Kind, rate.Base = RateKindDistraction, settings.RateDistraction
	}
	for _, s := range surcharges {
		if !at.Before(s.start) && at.Before(s.end) {
			rate.GrayscaleOff = settings.RateGrayscaleOff
		}
	}
	rate.Total = rate.Base + rate.GrayscaleOff
	return rate
}

func pieceAt(pieces []ratePiece, at time.Time) (ratePiece, bool) {
	index := sort.Search(len(pieces), func(i int) bool { return pieces[i].to.After(at) })
	if index < len(pieces) && !at.Before(pieces[index].from) {
		return pieces[index], true
	}
	return ratePiece{}, false
}

// Counts reports whether a segment is distraction use the budget charges:
// it matches a distraction in force (labels included) and no essential, is
// not a page kept open, and its presence is known. Mac time with no AFK
// record is not charged, since missing data is never assumed to be use.
func Counts(config policy.Config, segment activity.Segment) bool {
	if segment.AFKUnknown {
		return false
	}
	if segment.URL != "" && policy.AddressOpen(config.OpenAddresses, segment.URL) {
		return false
	}
	matches := func(group policy.TargetGroup) bool {
		for _, domain := range group.Domains {
			if segment.Domain != "" && (segment.Domain == domain || strings.HasSuffix(segment.Domain, "."+domain)) {
				return true
			}
		}
		apps := group.MacApps
		if segment.Device == activity.DevicePhone {
			apps = group.AndroidPackages
		}
		for _, app := range apps {
			if strings.EqualFold(app, segment.App) {
				return true
			}
		}
		return false
	}
	essential := map[string]bool{}
	for _, group := range config.Essentials {
		essential[group.ID] = true
		if matches(group) {
			return false
		}
	}
	for _, group := range config.Distractions {
		if !essential[group.ID] && matches(group) {
			return true
		}
	}
	return false
}

// uses is the charged distraction time: the union of counting segments on
// every device, so one person using two screens at once pays once.
func uses(config policy.Config, segments []activity.Segment) []span {
	spans := []span{}
	for _, segment := range segments {
		if segment.End.After(segment.Start) && Counts(config, segment) {
			spans = append(spans, span{segment.Start, segment.End})
		}
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start.Before(spans[j].start) })
	merged := []span{}
	for _, s := range spans {
		if n := len(merged); n > 0 && !s.start.After(merged[n-1].end) {
			merged[n-1].end = later(merged[n-1].end, s.end)
			continue
		}
		merged = append(merged, s)
	}
	return merged
}

// useMinutes is the charged use within [from, to).
func useMinutes(spans []span, from, to time.Time) float64 {
	total := 0.0
	for _, s := range spans {
		start, end := later(s.start, from), earlier(s.end, to)
		if end.After(start) {
			total += minutes(end.Sub(start))
		}
	}
	return total
}

// useReached is when the use from from on reaches amount minutes, if it has.
func useReached(spans []span, from, to time.Time, amount float64) (time.Time, bool) {
	total := 0.0
	for _, s := range spans {
		start, end := later(s.start, from), earlier(s.end, to)
		if !end.After(start) {
			continue
		}
		length := minutes(end.Sub(start))
		if total+length >= amount {
			return start.Add(time.Duration((amount - total) * float64(time.Minute))), true
		}
		total += length
	}
	return time.Time{}, false
}

// runOut is when use from from on, without a break while usable, spends
// left and then borrow (each borrowed budget minute costing multiplier),
// at the rates in force. It returns limit when they last that long.
func runOut(pieces []ratePiece, from, limit time.Time, left, borrow, multiplier float64, usable func(Rate) bool) time.Time {
	if multiplier < 1 {
		multiplier = 1
	}
	for _, piece := range pieces {
		start, end := later(piece.from, from), earlier(piece.to, limit)
		if !end.After(start) || !usable(piece.rate) || piece.rate.Total <= 0 {
			continue
		}
		length := minutes(end.Sub(start))
		if cost := length * piece.rate.Total; left >= cost {
			left -= cost
			continue
		}
		used := left / piece.rate.Total
		left = 0
		start = start.Add(time.Duration(used * float64(time.Minute)))
		length -= used
		cost := length * piece.rate.Total * multiplier
		if borrow >= cost {
			borrow -= cost
			continue
		}
		return start.Add(time.Duration(borrow / (piece.rate.Total * multiplier) * float64(time.Minute)))
	}
	return limit
}

// charge is what continuous use over [from, to) costs at the rates in force.
func charge(pieces []ratePiece, from, to time.Time) float64 {
	total := 0.0
	for _, piece := range pieces {
		start, end := later(piece.from, from), earlier(piece.to, to)
		if end.After(start) {
			total += minutes(end.Sub(start)) * piece.rate.Total
		}
	}
	return total
}

func anyRate(Rate) bool { return true }

func distractionRate(rate Rate) bool { return rate.Kind == RateKindDistraction }

// OpenSpan is a stretch in which something other than a door opened
// distractions.
type OpenSpan struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// doorSpans is when doors and the other openings had distractions open. A
// door cancelled before it opened opened nothing.
func doorSpans(doors []Door, others []OpenSpan) []span {
	out := []span{}
	for _, door := range doors {
		end := door.WindowEnd
		if door.CancelledAt != nil {
			end = earlier(end, *door.CancelledAt)
		}
		if end.After(door.OpensAt) {
			out = append(out, span{door.OpensAt, end})
		}
	}
	for _, other := range others {
		if other.To.After(other.From) {
			out = append(out, span{other.From, other.To})
		}
	}
	return out
}

// within is the part of spans that falls inside any of cover, merged.
func within(spans, cover []span) []span {
	out := []span{}
	for _, s := range spans {
		for _, c := range cover {
			start, end := later(s.start, c.start), earlier(s.end, c.end)
			if end.After(start) {
				out = append(out, span{start, end})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].start.Before(out[j].start) })
	merged := []span{}
	for _, s := range out {
		if n := len(merged); n > 0 && !s.start.After(merged[n-1].end) {
			merged[n-1].end = later(merged[n-1].end, s.end)
			continue
		}
		merged = append(merged, s)
	}
	return merged
}
