// Package activity turns retained Mac and phone observations into what the
// owner was actually doing: non-overlapping active segments per device, time
// per app and site, and the periods with no evidence at all. It reads only
// the records it is given and never treats missing data as inactivity.
package activity

import (
	"encoding/json"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Record is one effective observation revision.
type Record struct {
	Source  string          // source instance key
	Device  string          // device ID
	Kind    string          // observation kind
	Start   time.Time       // occurred_at
	End     *time.Time      // ended_at
	Payload json.RawMessage // canonical payload
}

const (
	DeviceMac   = "mac"
	DevicePhone = "phone"
)

// Segment is a span of active use of one app (and, in a browser, one site).
type Segment struct {
	Device string    `json:"device"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	App    string    `json:"app"`
	Title  string    `json:"title,omitempty"`
	Domain string    `json:"domain,omitempty"`
}

func (s Segment) Duration() time.Duration { return s.End.Sub(s.Start) }

// Gap is a period in which a device delivered no records at all. For the Mac
// this includes sleep; for the phone the collector reports every minute, so a
// gap usually means collection stopped.
type Gap struct {
	Device string    `json:"device"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
}

type Result struct {
	Segments []Segment
	Gaps     []Gap
	// LastRecord is the newest record per device, even before From.
	LastRecord map[string]time.Time
}

const (
	maxPhoneSession = time.Hour
	gapThreshold    = 15 * time.Minute
	mergeTolerance  = 2 * time.Second
)

// DeviceOf groups sources into the owner's devices.
func DeviceOf(source string) string {
	switch {
	case strings.HasPrefix(source, "activitywatch:"):
		return DeviceMac
	case strings.HasPrefix(source, "android:"):
		return DevicePhone
	}
	return ""
}

// Build reconstructs activity in [from, to). Records may start before from;
// they are used to know what was already open at from.
func Build(records []Record, from, to time.Time) Result {
	result := Result{LastRecord: map[string]time.Time{}}
	var windows, afk, web []span
	var usage []Record
	var screen []screenState
	covered := map[string][]span{}
	for _, record := range records {
		device := DeviceOf(record.Source)
		if device == "" {
			continue
		}
		at := record.Start
		if record.End != nil && record.End.After(at) {
			at = *record.End
		}
		if at.After(result.LastRecord[device]) {
			result.LastRecord[device] = at
		}
		covered[device] = append(covered[device], span{start: record.Start, end: at})
		switch {
		case strings.Contains(record.Source, "aw-watcher-window"):
			windows = appendAWSpan(windows, record)
		case strings.Contains(record.Source, "aw-watcher-afk"):
			afk = appendAWSpan(afk, record)
		case strings.Contains(record.Source, "aw-watcher-web"):
			web = appendAWSpan(web, record)
		case strings.HasSuffix(record.Source, ":usage"):
			usage = append(usage, record)
		case strings.HasSuffix(record.Source, ":state"):
			var state struct {
				Interactive *bool `json:"screen_interactive"`
			}
			if json.Unmarshal(record.Payload, &state) == nil && state.Interactive != nil {
				screen = append(screen, screenState{record.Start, *state.Interactive})
			}
		}
	}
	segments := macSegments(windows, afk, web)
	segments = append(segments, phoneSegments(usage, screen, to)...)
	result.Segments = clipAndMerge(segments, from, to)
	for _, device := range []string{DeviceMac, DevicePhone} {
		result.Gaps = append(result.Gaps, gaps(device, covered[device], from, to)...)
	}
	sort.Slice(result.Gaps, func(i, j int) bool { return result.Gaps[i].Start.Before(result.Gaps[j].Start) })
	return result
}

type span struct {
	start, end time.Time
	data       map[string]any
}

type screenState struct {
	at          time.Time
	interactive bool
}

func appendAWSpan(spans []span, record Record) []span {
	var event struct {
		Timestamp time.Time      `json:"timestamp"`
		Duration  float64        `json:"duration"`
		Data      map[string]any `json:"data"`
	}
	if json.Unmarshal(record.Payload, &event) != nil || event.Timestamp.IsZero() {
		return spans
	}
	end := event.Timestamp.Add(time.Duration(event.Duration * float64(time.Second)))
	return append(spans, span{event.Timestamp, end, event.Data})
}

func text(data map[string]any, key string) string {
	value, _ := data[key].(string)
	return value
}

// sequential sorts spans and trims overlaps so each moment has one span.
func sequential(spans []span) []span {
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].start.Before(spans[j].start) })
	result := make([]span, 0, len(spans))
	for _, s := range spans {
		if n := len(result); n > 0 && s.start.Before(result[n-1].end) {
			if !s.end.After(result[n-1].end) {
				continue
			}
			s.start = result[n-1].end
		}
		if s.end.After(s.start) {
			result = append(result, s)
		}
	}
	return result
}

func macSegments(windows, afk, web []span) []Segment {
	active := []span{}
	for _, s := range sequential(afk) {
		if text(s.data, "status") == "not-afk" {
			active = append(active, s)
		}
	}
	web = sequential(web)
	segments := []Segment{}
	for _, window := range sequential(windows) {
		app := text(window.data, "app")
		for _, part := range intersect(window, active) {
			if isBrowser(app, text(window.data, "bundle_id")) {
				segments = append(segments, browserSegments(app, text(window.data, "title"), part, web)...)
				continue
			}
			segments = append(segments, Segment{Device: DeviceMac, Start: part.start, End: part.end, App: app, Title: text(window.data, "title")})
		}
	}
	return segments
}

// intersect returns the parts of s covered by the sorted, disjoint spans,
// carrying s's data.
func intersect(s span, cover []span) []span {
	parts := overlaps(s, cover)
	for i := range parts {
		parts[i].data = s.data
	}
	return parts
}

// overlaps is intersect carrying each covering span's data.
func overlaps(s span, cover []span) []span {
	result := []span{}
	index := sort.Search(len(cover), func(i int) bool { return cover[i].end.After(s.start) })
	for ; index < len(cover) && cover[index].start.Before(s.end); index++ {
		start, end := later(s.start, cover[index].start), earlier(s.end, cover[index].end)
		if end.After(start) {
			result = append(result, span{start, end, cover[index].data})
		}
	}
	return result
}

// browserSegments names browser time by the page open in it. Time without a
// matching tab record keeps the window title.
func browserSegments(app, windowTitle string, part span, web []span) []Segment {
	segments := []Segment{}
	cursor := part.start
	for _, tab := range overlaps(part, web) {
		if tab.start.After(cursor) {
			segments = append(segments, Segment{Device: DeviceMac, Start: cursor, End: tab.start, App: app, Title: windowTitle})
		}
		segments = append(segments, Segment{Device: DeviceMac, Start: tab.start, End: tab.end, App: app, Title: text(tab.data, "title"), Domain: domain(text(tab.data, "url"))})
		cursor = tab.end
	}
	if part.end.After(cursor) {
		segments = append(segments, Segment{Device: DeviceMac, Start: cursor, End: part.end, App: app, Title: windowTitle})
	}
	return segments
}

var browserMarkers = []string{"helium", "chrome", "chromium", "vivaldi", "firefox", "safari", "brave", "arc", "edge", "zen", "orion"}

func isBrowser(app, bundleID string) bool {
	name := strings.ToLower(app + " " + bundleID)
	for _, marker := range browserMarkers {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}

func domain(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	return strings.TrimPrefix(parsed.Hostname(), "www.")
}

// phoneSegments follows foreground apps from usage events. A session ends at
// its pause, the next app, the screen turning off, or after an hour without
// any of those.
func phoneSegments(usage []Record, screen []screenState, to time.Time) []Segment {
	sort.SliceStable(usage, func(i, j int) bool { return usage[i].Start.Before(usage[j].Start) })
	sort.SliceStable(screen, func(i, j int) bool { return screen[i].at.Before(screen[j].at) })
	segments := []Segment{}
	var app string
	var since time.Time
	close := func(at time.Time) {
		if app == "" {
			return
		}
		end := earlier(at, since.Add(maxPhoneSession))
		if off := firstScreenOff(screen, since, end); !off.IsZero() {
			end = off
		}
		if end.After(since) {
			segments = append(segments, Segment{Device: DevicePhone, Start: since, End: end, App: app})
		}
		app = ""
	}
	for _, record := range usage {
		var event struct {
			Package string `json:"package_name"`
		}
		if json.Unmarshal(record.Payload, &event) != nil || event.Package == "" {
			continue
		}
		switch record.Kind {
		case "android.usage.activity_resumed":
			if event.Package != app {
				close(record.Start)
				app, since = event.Package, record.Start
			}
		case "android.usage.activity_paused":
			if event.Package == app {
				close(record.Start)
			}
		}
	}
	close(to)
	return segments
}

func firstScreenOff(screen []screenState, from, to time.Time) time.Time {
	index := sort.Search(len(screen), func(i int) bool { return screen[i].at.After(from) })
	for ; index < len(screen) && screen[index].at.Before(to); index++ {
		if !screen[index].interactive {
			return screen[index].at
		}
	}
	return time.Time{}
}

// clipAndMerge limits segments to [from, to), orders them and joins
// back-to-back segments for the same thing.
func clipAndMerge(segments []Segment, from, to time.Time) []Segment {
	sort.SliceStable(segments, func(i, j int) bool {
		if segments[i].Device != segments[j].Device {
			return segments[i].Device < segments[j].Device
		}
		return segments[i].Start.Before(segments[j].Start)
	})
	result := []Segment{}
	for _, segment := range segments {
		segment.Start, segment.End = later(segment.Start, from), earlier(segment.End, to)
		if !segment.End.After(segment.Start) {
			continue
		}
		if n := len(result); n > 0 {
			last := &result[n-1]
			if last.Device == segment.Device && last.App == segment.App && last.Domain == segment.Domain && last.Title == segment.Title &&
				!segment.Start.After(last.End.Add(mergeTolerance)) {
				last.End = later(last.End, segment.End)
				continue
			}
		}
		result = append(result, segment)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Start.Before(result[j].Start) })
	return result
}

// gaps finds periods of at least gapThreshold covered by no record. A record
// covers its whole span, so one long AFK event is not a gap.
func gaps(device string, spans []span, from, to time.Time) []Gap {
	sort.Slice(spans, func(i, j int) bool { return spans[i].start.Before(spans[j].start) })
	result := []Gap{}
	cursor := from
	for _, s := range spans {
		if s.start.After(to) {
			break
		}
		if s.start.Sub(cursor) >= gapThreshold {
			result = append(result, Gap{Device: device, Start: cursor, End: s.start})
		}
		cursor = later(cursor, s.end)
	}
	if to.Sub(cursor) >= gapThreshold {
		result = append(result, Gap{Device: device, Start: cursor, End: to})
	}
	return result
}

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
