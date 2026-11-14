// Package intent works out what the owner is trying to do from everything
// the collectors saw, without asking them. The owner did not use tasks,
// plans or focus sessions (2026-11-14): they work on several things at once,
// across apps, devices and coding agents, in their own way. So the system
// reads the recent record itself and keeps a running list of aims, each
// with the evidence it was read from.
//
// An aim is an inference, never something the owner said. It is shown as
// what the system thinks, and it is not used to judge or block anything.
package intent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/rimexe0/telemetry/internal/activity"
)

// Kinds of aim. Leisure and rest are named as plainly as work: knowing what
// the owner is doing is not a verdict on it.
const (
	KindWork    = "work"
	KindLife    = "life"
	KindSocial  = "social"
	KindLeisure = "leisure"
	KindRest    = "rest"
	KindUnclear = "unclear"
)

// States of an aim.
const (
	StatusActive  = "active"
	StatusWaiting = "waiting"
	StatusPaused  = "paused"
	StatusDone    = "done"
	StatusDropped = "dropped"
)

var kinds = map[string]bool{KindWork: true, KindLife: true, KindSocial: true, KindLeisure: true, KindRest: true, KindUnclear: true}
var statuses = map[string]bool{StatusActive: true, StatusWaiting: true, StatusPaused: true, StatusDone: true, StatusDropped: true}

// Known is an aim from an earlier reading, shown so the next reading can
// keep the same aim instead of naming it again.
type Known struct {
	ID       int64
	Title    string
	Kind     string
	Status   string
	NextStep string
	LastSeen time.Time
}

// Aim is one thing the reading thinks the owner is trying to do. ID is an
// earlier aim's, or nil for a new one.
type Aim struct {
	ID         *int64   `json:"id"`
	Title      string   `json:"title"`
	Kind       string   `json:"kind"`
	Status     string   `json:"status"`
	NextStep   string   `json:"next_step,omitempty"`
	Evidence   []string `json:"evidence,omitempty"`
	Confidence string   `json:"confidence,omitempty"`
}

// Reading is one answer: the aims it saw or changed, and which of them, by
// position, the owner is on right now (-1 when none or unclear).
type Reading struct {
	Aims []Aim  `json:"intents"`
	Now  int    `json:"now"`
	Note string `json:"note,omitempty"`
}

const (
	// Runs shorter than this are counted, not listed.
	minRun = 20 * time.Second
	// Two runs of the same thing this close are one.
	joinGap = 90 * time.Second
	// The digest keeps the newest lines when there are more.
	maxLines    = 220
	maxTitleLen = 110
)

// Digest writes what the owner did between from and to as short timeline
// lines: screen time on each computer and the phone (app, site, window or
// page title), the coding agents they worked with, and from records the
// context: time away from home (input.Fixes and input.Home), last sleep,
// calendar events, blocked attempts, and who sent notifications. It carries
// names and titles only, never page or message contents.
func Digest(input activity.DayInput, context []activity.Record, from, to time.Time, location *time.Location) string {
	if location == nil {
		location = time.UTC
	}
	clock := func(at time.Time) string { return at.In(location).Format("15:04") }

	type run struct {
		device, app, domain, title string
		start, end                 time.Time
	}
	segments := append([]activity.Segment{}, input.Result.Segments...)
	sort.Slice(segments, func(i, j int) bool { return segments[i].Start.Before(segments[j].Start) })
	runs := []run{}
	for _, segment := range segments {
		if !segment.End.After(from) || !segment.Start.Before(to) {
			continue
		}
		start, end := segment.Start, segment.End
		if start.Before(from) {
			start = from
		}
		if end.After(to) {
			end = to
		}
		title := strings.TrimSpace(segment.Title)
		matched := false
		for index := len(runs) - 1; index >= 0 && index >= len(runs)-3; index-- {
			last := &runs[index]
			if last.device == deviceName(segment) && last.app == segment.App && last.domain == segment.Domain && last.title == title &&
				start.Sub(last.end) <= joinGap {
				if end.After(last.end) {
					last.end = end
				}
				matched = true
				break
			}
		}
		if !matched {
			runs = append(runs, run{device: deviceName(segment), app: segment.App, domain: segment.Domain, title: title, start: start, end: end})
		}
	}

	lines := []string{}
	short := map[string]time.Duration{}
	for _, item := range runs {
		length := item.end.Sub(item.start)
		name := item.app
		if item.domain != "" {
			name += " " + item.domain
		}
		if length < minRun {
			short[item.device+" "+name] += length
			continue
		}
		line := fmt.Sprintf("%s-%s %s %s", clock(item.start), clock(item.end), item.device, name)
		if item.title != "" && item.title != item.app && item.title != item.domain {
			line += " — " + cut(item.title, maxTitleLen)
		}
		lines = append(lines, line)
	}
	if len(lines) > maxLines {
		lines = append([]string{fmt.Sprintf("(%d earlier lines left out)", len(lines)-maxLines)}, lines[len(lines)-maxLines:]...)
	}

	var out strings.Builder
	fmt.Fprintf(&out, "Screen time from %s to %s (start-end device app [site] — window or page title; device is mac, phone, or another computer's name):\n", clock(from), clock(to))
	if len(lines) == 0 {
		out.WriteString("(nothing on screen)\n")
	}
	for _, line := range lines {
		out.WriteString(line + "\n")
	}
	if len(short) > 0 {
		names := make([]string, 0, len(short))
		for name := range short {
			names = append(names, name)
		}
		sort.Slice(names, func(i, j int) bool { return short[names[i]] > short[names[j]] })
		if len(names) > 15 {
			names = names[:15]
		}
		parts := []string{}
		for _, name := range names {
			parts = append(parts, fmt.Sprintf("%s %ds", name, int(short[name].Seconds())))
		}
		out.WriteString("Brief glances (under 20 s each, summed): " + strings.Join(parts, ", ") + "\n")
	}

	// The coding agents: one line per thread the owner wrote to or that
	// ran in the window.
	type thread struct {
		title, project, tool string
		sent                 int
		lastSent             time.Time
		running              bool
		stopped              *time.Time
	}
	threads := map[string]*thread{}
	order := []string{}
	get := func(id, title, project, tool string) *thread {
		if threads[id] == nil {
			threads[id] = &thread{title: title, project: project, tool: tool}
			order = append(order, id)
		}
		return threads[id]
	}
	for _, turn := range input.Turns {
		ended := turn.Ended
		if turn.Requested.After(to) || (ended != nil && ended.Before(from)) {
			continue
		}
		item := get(turn.ThreadID, turn.Thread, turn.Project, turn.Tool)
		if !turn.ByAgent && !turn.Requested.Before(from) {
			item.sent++
			if turn.Requested.After(item.lastSent) {
				item.lastSent = turn.Requested
			}
		}
		if ended == nil {
			item.running = true
		} else if item.stopped == nil || ended.After(*item.stopped) {
			item.stopped = ended
		}
	}
	if len(order) > 0 {
		out.WriteString("Coding agents (project · thread title: messages the owner sent here, agent state):\n")
		sort.SliceStable(order, func(i, j int) bool { return threads[order[i]].lastSent.After(threads[order[j]].lastSent) })
		for _, id := range order {
			item := threads[id]
			project := item.project
			if project == "" {
				project = "no project"
			}
			line := fmt.Sprintf("%s · %s: %d sent", project, cut(item.title, maxTitleLen), item.sent)
			if item.sent > 0 {
				line += ", last at " + clock(item.lastSent)
			}
			switch {
			case item.running:
				line += "; agent working now"
			case item.stopped != nil:
				line += "; agent stopped at " + clock(*item.stopped)
			}
			out.WriteString(line + "\n")
		}
	}
	out.WriteString(contextLines(input, context, from, to, clock))
	return out.String()
}

// deviceName names where a segment happened: "phone", "mac", or another
// computer by its own name.
func deviceName(segment activity.Segment) string {
	if segment.Device != activity.DeviceMac || segment.Machine == "" || strings.HasPrefix(segment.Machine, "mac") {
		return segment.Device
	}
	return segment.Machine
}

// contextLines words the records around the screen time.
func contextLines(input activity.DayInput, records []activity.Record, from, to time.Time, clock func(time.Time) string) string {
	lines := []string{}
	away := []string{}
	for _, stretch := range activity.Outside(input) {
		if stretch.End.Before(from) || stretch.Start.After(to) {
			continue
		}
		text := clock(stretch.Start) + "-" + clock(stretch.End)
		if stretch.Ongoing {
			text += " (not back yet)"
		}
		away = append(away, text)
	}
	if len(away) > 0 {
		lines = append(lines, "Away from home: "+strings.Join(away, ", "))
	}

	type count struct {
		name string
		n    int
	}
	tally := func(counts map[string]int, limit int) string {
		list := []count{}
		for name, n := range counts {
			list = append(list, count{name, n})
		}
		sort.Slice(list, func(i, j int) bool {
			return list[i].n > list[j].n || (list[i].n == list[j].n && list[i].name < list[j].name)
		})
		if len(list) > limit {
			list = list[:limit]
		}
		parts := []string{}
		for _, item := range list {
			parts = append(parts, fmt.Sprintf("%s ×%d", item.name, item.n))
		}
		return strings.Join(parts, ", ")
	}
	slept := [][2]time.Time{}
	blocked, notified := map[string]int{}, map[string]int{}
	blockedMinutes := map[string]bool{}
	bypasses, events := []string{}, []string{}
	seenEvents := map[string]bool{}
	for _, record := range records {
		var payload map[string]any
		if json.Unmarshal(record.Payload, &payload) != nil {
			continue
		}
		field := func(key string) string { value, _ := payload[key].(string); return strings.TrimSpace(value) }
		inWindow := !record.Start.Before(from) && record.Start.Before(to)
		switch record.Kind {
		case "health_connect.sleep_session":
			start, errStart := time.Parse(time.RFC3339, field("start_time"))
			end, errEnd := time.Parse(time.RFC3339, field("end_time"))
			if errStart == nil && errEnd == nil && end.Before(to) && end.After(from.Add(-16*time.Hour)) {
				slept = append(slept, [2]time.Time{start, end})
			}
		case "android.calendar_event":
			start, errStart := time.Parse(time.RFC3339, field("start"))
			end, errEnd := time.Parse(time.RFC3339, field("end"))
			key := field("title") + field("start")
			if errStart != nil || errEnd != nil || field("title") == "" || seenEvents[key] || end.Before(from) || start.After(to.Add(12*time.Hour)) {
				continue
			}
			seenEvents[key] = true
			events = append(events, clock(start)+"-"+clock(end)+" "+cut(field("title"), 60))
		case "desktop.site_block", "desktop.policy_block", "android.policy_block":
			if !inWindow {
				continue
			}
			name := field("site")
			if name == "" {
				name = field("app")
			}
			if name == "" {
				name = field("package")
			}
			// A covered tab is reported every few seconds; count minutes.
			if minute := name + record.Start.Truncate(time.Minute).String(); name != "" && !blockedMinutes[minute] {
				blockedMinutes[minute] = true
				blocked[name]++
			}
		case "android.policy_bypass":
			if inWindow {
				bypasses = append(bypasses, clock(record.Start)+" phone ("+field("cause")+")")
			}
		case "android.notification.posted":
			app := field("package_name")
			if !inWindow || app == "" || strings.HasPrefix(app, "cyou.rime.telemetry") || app == "android" {
				continue
			}
			name := app
			if title := field("title"); title != "" {
				name += " from " + cut(title, 40)
			}
			notified[name]++
		}
	}
	if len(slept) > 0 {
		sort.Slice(slept, func(i, j int) bool { return slept[i][0].Before(slept[j][0]) })
		parts := []string{}
		for _, session := range slept {
			parts = append(parts, clock(session[0])+"-"+clock(session[1]))
		}
		lines = append(lines, "Sleep recorded since the evening before: "+strings.Join(parts, ", "))
	}
	if len(events) > 0 {
		lines = append(lines, "Calendar (now and the next 12 h): "+strings.Join(events, "; "))
	}
	if len(blocked) > 0 {
		lines = append(lines, "Blocked (minutes in which each was blocked): "+tally(blocked, 12))
	}
	if len(bypasses) > 0 {
		lines = append(lines, "Blocking turned off: "+strings.Join(bypasses, ", "))
	}
	if len(notified) > 0 {
		lines = append(lines, "Notifications received (app from sender or chat; contents not shown): "+tally(notified, 12))
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// Prompt asks for the reading. known are the aims from earlier readings
// that may continue now.
func Prompt(digest string, known []Known, now time.Time, location *time.Location) string {
	if location == nil {
		location = time.UTC
	}
	lines := []string{
		"You keep track of what one person is trying to do, from what their computer and phone showed. They have ADHD, do several things at once, switch often, and work in their own, often unexpected way. They do not write plans; you work it out for them. Refer to them as \"they\", never \"he\" or \"she\", including in the note.",
		"",
		"Group what you see into aims: the things they are actually trying to get done or are spending time on. An aim is defined by its purpose, not by the app: one aim can span a browser, a coding agent, a chat app and the phone (for example a YouTube tutorial, a ChatGPT question and a coding thread all about one 3D printer).",
		"Coding agent threads are strong evidence of an aim, but not the only one. Name non-coding aims too: errands, appointments, buying something, talking to someone, learning something.",
		"Leisure and rest are aims too: name them plainly and specifically (\"Watching YouTube: car videos\", \"Scrolling X\"), without judging them.",
		"Write each title in a few plain words that say the purpose and are specific to this person's thing (\"Fix the tablet's touchscreen\", not \"Coding\" or \"Browsing\").",
		"Use only what the evidence shows. When the purpose is not clear, say what is visible and set kind \"unclear\" and confidence \"low\".",
		"Brief glances alone are not an aim; use them only as support for an aim the longer lines show.",
		"",
		"Earlier aims (reuse the id when the same aim continues, even under another app):",
	}
	if len(known) == 0 {
		lines = append(lines, "(none)")
	}
	for _, aim := range known {
		line := fmt.Sprintf("- id %d: %s (%s, %s, last seen %s)", aim.ID, aim.Title, aim.Kind, aim.Status, aim.LastSeen.In(location).Format("Mon 15:04"))
		if aim.NextStep != "" {
			line += " next: " + aim.NextStep
		}
		lines = append(lines, line)
	}
	lines = append(lines, "",
		"It is now "+now.In(location).Format("Monday 15:04")+".",
		digest,
		"Answer with the aims seen in this record, and earlier aims whose state changed. Leave out earlier aims nothing here touches.",
		"status: \"active\" (they are on it), \"waiting\" (a coding agent is working on it for them), \"paused\" (left for now), \"done\" (the record shows it finished), \"dropped\" (clearly abandoned).",
		"kind: work, life, social, leisure, rest or unclear. next_step: the concrete next thing for that aim, when the record shows it; otherwise empty.",
		"evidence: up to 4 short items from the record above that show the aim. now: the position (0-based) in your list of the aim they are on right now, or -1.",
		"",
		"Do not use any tools. Answer with exactly one line of JSON and nothing else:",
		`{"intents":[{"id":<earlier id or null>,"title":"...","kind":"...","status":"...","next_step":"...","evidence":["..."],"confidence":"high|low"}],"now":<position or -1>,"note":"one short sentence on what the record shows overall"}`,
	)
	return strings.Join(lines, "\n")
}

// Parse reads the reading from the last JSON line of the output and drops
// what does not fit: aims without a title, unknown kinds and states, and
// ids that are not among the known ones.
func Parse(output string, known []Known) (Reading, error) {
	ids := map[int64]bool{}
	for _, aim := range known {
		ids[aim.ID] = true
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		start, end := strings.Index(line, "{"), strings.LastIndex(line, "}")
		if start < 0 || end <= start || !strings.Contains(line, "intents") {
			continue
		}
		var raw Reading
		if json.Unmarshal([]byte(line[start:end+1]), &raw) != nil {
			continue
		}
		reading := Reading{Now: -1, Note: strings.TrimSpace(raw.Note)}
		seen := map[int64]bool{}
		for position, aim := range raw.Aims {
			aim.Title = strings.TrimSpace(aim.Title)
			aim.Kind = strings.ToLower(strings.TrimSpace(aim.Kind))
			aim.Status = strings.ToLower(strings.TrimSpace(aim.Status))
			aim.Confidence = strings.ToLower(strings.TrimSpace(aim.Confidence))
			aim.NextStep = strings.TrimSpace(aim.NextStep)
			if aim.Title == "" || !statuses[aim.Status] {
				continue
			}
			if !kinds[aim.Kind] {
				aim.Kind = KindUnclear
			}
			if aim.ID != nil && (!ids[*aim.ID] || seen[*aim.ID]) {
				aim.ID = nil
			}
			if aim.ID != nil {
				seen[*aim.ID] = true
			}
			if len(aim.Evidence) > 6 {
				aim.Evidence = aim.Evidence[:6]
			}
			if position == raw.Now {
				reading.Now = len(reading.Aims)
			}
			reading.Aims = append(reading.Aims, aim)
		}
		return reading, nil
	}
	return Reading{}, errors.New("the assistant gave no reading")
}

// Ask runs the command with the prompt as its last argument and reads its
// answer.
func Ask(ctx context.Context, command []string, prompt string, known []Known) (Reading, string, error) {
	if len(command) == 0 {
		return Reading{}, "", errors.New("no command is configured")
	}
	output, err := exec.CommandContext(ctx, command[0], append(append([]string{}, command[1:]...), prompt)...).Output()
	if err != nil {
		return Reading{}, string(output), fmt.Errorf("intent command: %w", err)
	}
	reading, err := Parse(string(output), known)
	return reading, string(output), err
}

func cut(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if runes := []rune(text); len(runes) > limit {
		return string(runes[:limit-1]) + "…"
	}
	return text
}
