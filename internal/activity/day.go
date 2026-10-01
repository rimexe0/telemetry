package activity

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

const (
	KindDistraction = "distraction"
	KindTask        = "task"
	KindOther       = "other"

	// A pause this long ends a chunk; a run of the other kind shorter than
	// chunkMinRun stays inside the chunk around it.
	chunkPause  = 5 * time.Minute
	chunkMinRun = 2 * time.Minute

	// T3 Code time is given to the thread whose message was sent closest to
	// it, when that message is within this distance.
	threadReach = 15 * time.Minute
)

// ThreadMessage is one message the owner sent in a T3 Code thread.
type ThreadMessage struct {
	At      time.Time
	Thread  string
	Project string
	// Root is the project's folder on the machine that runs it.
	Root string
}

// TaskRule is one thing that counts as doing a task: an app, a site, or a
// project folder.
type TaskRule struct {
	Signal  string
	Pattern string
}

// onTask reports whether a segment matches any rule. root is the folder of
// the T3 Code project behind the segment, when known.
func onTask(segment Segment, root string, rules []TaskRule) bool {
	for _, rule := range rules {
		switch rule.Signal {
		case "app":
			if strings.EqualFold(segment.App, rule.Pattern) {
				return true
			}
		case "domain":
			if segment.Domain != "" && (segment.Domain == rule.Pattern || strings.HasSuffix(segment.Domain, "."+rule.Pattern)) {
				return true
			}
		case "path_prefix":
			if root != "" && (root == rule.Pattern || strings.HasPrefix(root, rule.Pattern+"/")) {
				return true
			}
		}
	}
	return false
}

// ThreadMessages lists the owner's T3 messages from every collector machine,
// in time order. A message is the only record of which thread was in use.
func ThreadMessages(coding []Record) []ThreadMessage {
	projects, threads := map[string][2]string{}, map[string][2]string{}
	for _, record := range coding {
		switch record.Kind {
		case "t3.project":
			var project struct {
				ID    string `json:"project_id"`
				Title string `json:"title"`
				Root  string `json:"workspace_root"`
			}
			if json.Unmarshal(record.Payload, &project) == nil {
				projects[record.Source+"\x00"+project.ID] = [2]string{project.Title, project.Root}
			}
		case "t3.thread":
			var thread t3Thread
			if json.Unmarshal(record.Payload, &thread) == nil {
				threads[record.Source+"\x00"+thread.ThreadID] = [2]string{thread.Title, thread.ProjectID}
			}
		}
	}
	messages := []ThreadMessage{}
	for _, record := range coding {
		if record.Kind != "t3.turn" {
			continue
		}
		var turn t3Turn
		if json.Unmarshal(record.Payload, &turn) != nil {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, turn.RequestedAt)
		thread, ok := threads[record.Source+"\x00"+turn.ThreadID]
		if err != nil || !ok {
			continue
		}
		project := projects[record.Source+"\x00"+thread[1]]
		messages = append(messages, ThreadMessage{At: at.UTC(), Thread: thread[0], Project: project[0], Root: project[1]})
	}
	sort.SliceStable(messages, func(i, j int) bool { return messages[i].At.Before(messages[j].At) })
	return messages
}

func isT3(app string) bool {
	return strings.HasPrefix(app, "T3 Code") || strings.HasPrefix(app, "com.t3tools.")
}

// threadFor names the thread a stretch of T3 Code time most likely belongs
// to: the one with the message sent closest to it. Empty when no message is
// near, because then nothing shows which thread was open. It also returns
// that thread's project folder.
func threadFor(segment Segment, messages []ThreadMessage) (string, string) {
	best, root, bestDistance := "", "", threadReach+1
	index := sort.Search(len(messages), func(i int) bool { return !messages[i].At.Before(segment.Start.Add(-threadReach)) })
	for ; index < len(messages) && !messages[index].At.After(segment.End.Add(threadReach)); index++ {
		message := messages[index]
		distance := time.Duration(0)
		if message.At.Before(segment.Start) {
			distance = segment.Start.Sub(message.At)
		} else if message.At.After(segment.End) {
			distance = message.At.Sub(segment.End)
		}
		if distance < bestDistance {
			bestDistance = distance
			best, root = message.Thread, message.Root
			if message.Project != "" {
				best = message.Project + " · " + message.Thread
			}
		}
	}
	return best, root
}

// DayItem totals one app or site inside a chunk.
type DayItem struct {
	Name string `json:"name"`
	// Detail narrows the name: for T3 Code, the project and thread.
	Detail  string  `json:"detail,omitempty"`
	Label   string  `json:"label"`
	Minutes float64 `json:"minutes"`
	Visits  int     `json:"visits"`
}

// DaySegment is one uninterrupted use of an app or site, as recorded.
type DaySegment struct {
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	Name   string    `json:"name"`
	Detail string    `json:"detail,omitempty"`
	Title  string    `json:"title,omitempty"`
	Label  string    `json:"label"`
}

// Chunk is a stretch of continuous use of one device, summarised. Its items
// total everything in it and its segments are the records behind them, so a
// chunk hides detail without dropping any.
type Chunk struct {
	Device string    `json:"device"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	// Kind is "distraction" when most of the chunk was on things the owner
	// labelled so, "task" when most of it matched the rules of the planned
	// block's task, otherwise "other".
	Kind               string       `json:"kind"`
	ActiveMinutes      float64      `json:"active_minutes"`
	DistractionMinutes float64      `json:"distraction_minutes"`
	TaskMinutes        float64      `json:"task_minutes"`
	Items              []DayItem    `json:"items"`
	Segments           []DaySegment `json:"segments,omitempty"`
}

// DayBlock is a planned block with the time recorded inside it.
type DayBlock struct {
	PlannedBlock
	Status string `json:"status"`
	// Tracked means the block's task has rules for what counts as doing it.
	Tracked bool `json:"tracked"`
	// InPlanMinutes, for a tracked block, is time on things that match the
	// task's rules while no device showed a distraction. For an untracked
	// block it is any active time without a distraction, which does not show
	// that the time went to the block's task.
	InPlanMinutes      float64 `json:"in_plan_minutes"`
	DistractionMinutes float64 `json:"distraction_minutes"`
}

type DeviceMinutes struct {
	Device  string  `json:"device"`
	Minutes float64 `json:"minutes"`
}

// Day is one day for the phone's home screen and its details page.
type Day struct {
	Blocks             []DayBlock      `json:"blocks"`
	PlannedMinutes     float64         `json:"planned_minutes"`
	InPlanMinutes      float64         `json:"in_plan_minutes"`
	DistractionMinutes float64         `json:"distraction_minutes"`
	Distraction        []DeviceMinutes `json:"distraction_by_device"`
	Chunks             []Chunk         `json:"chunks"`
	NoData             []Gap           `json:"no_data"`
}

func segmentName(segment Segment) string {
	if segment.Domain != "" {
		return segment.Domain
	}
	return segment.App
}

// classifier decides each segment's kind for one day.
type classifier struct {
	labels   []Label
	messages []ThreadMessage
	blocks   []PlannedBlock
	rules    map[int64][]TaskRule
}

// rulesAt returns the rules of the planned task whose block covers at.
func (c classifier) rulesAt(at time.Time) []TaskRule {
	for _, block := range c.blocks {
		if block.TaskID != nil && !at.Before(block.Start) && at.Before(block.End) {
			return c.rules[*block.TaskID]
		}
	}
	return nil
}

func (c classifier) kindOf(segment Segment) string {
	if LabelFor(c.labels, segment) == Distraction {
		return KindDistraction
	}
	if rules := c.rulesAt(segment.Start); len(rules) > 0 {
		root := ""
		if isT3(segment.App) {
			_, root = threadFor(segment, c.messages)
		}
		if onTask(segment, root, rules) {
			return KindTask
		}
	}
	return KindOther
}

// chunks compresses one device's ordered segments.
func chunks(device string, segments []Segment, c classifier, withSegments bool) []Chunk {
	// Runs: consecutive segments of one kind with no long pause.
	type run struct {
		kind     string
		segments []Segment
		active   time.Duration
	}
	var sessions [][]run
	for _, segment := range segments {
		kind := c.kindOf(segment)
		if n := len(sessions); n == 0 || segment.Start.Sub(lastEnd(sessions[n-1][len(sessions[n-1])-1].segments)) > chunkPause {
			sessions = append(sessions, nil)
		}
		session := &sessions[len(sessions)-1]
		if n := len(*session); n == 0 || (*session)[n-1].kind != kind {
			*session = append(*session, run{kind: kind})
		}
		current := &(*session)[len(*session)-1]
		current.segments = append(current.segments, segment)
		current.active += segment.Duration()
	}
	result := []Chunk{}
	for _, session := range sessions {
		// Short runs join the run before them (the first joins the next),
		// then neighbours of one kind join.
		merged := []run{}
		for _, current := range session {
			if n := len(merged); n > 0 && (current.active < chunkMinRun || merged[n-1].kind == current.kind || merged[n-1].active < chunkMinRun) {
				if merged[n-1].active < chunkMinRun && current.active >= chunkMinRun {
					merged[n-1].kind = current.kind
				}
				merged[n-1].segments = append(merged[n-1].segments, current.segments...)
				merged[n-1].active += current.active
				continue
			}
			merged = append(merged, current)
		}
		joined := []run{}
		for _, current := range merged {
			if n := len(joined); n > 0 && joined[n-1].kind == current.kind {
				joined[n-1].segments = append(joined[n-1].segments, current.segments...)
				joined[n-1].active += current.active
				continue
			}
			joined = append(joined, current)
		}
		for _, current := range joined {
			result = append(result, chunkOf(device, current.segments, c, withSegments))
		}
	}
	return result
}

func lastEnd(segments []Segment) time.Time { return segments[len(segments)-1].End }

func chunkOf(device string, segments []Segment, c classifier, withSegments bool) Chunk {
	chunk := Chunk{Device: device, Start: segments[0].Start, End: lastEnd(segments), Kind: KindOther, Items: []DayItem{}}
	items := map[string]*DayItem{}
	for _, segment := range segments {
		minutes := segment.Duration().Minutes()
		name, label := segmentName(segment), LabelFor(c.labels, segment)
		chunk.ActiveMinutes += minutes
		switch c.kindOf(segment) {
		case KindDistraction:
			chunk.DistractionMinutes += minutes
		case KindTask:
			chunk.TaskMinutes += minutes
		}
		detail := ""
		if isT3(segment.App) {
			detail, _ = threadFor(segment, c.messages)
		}
		key := name + "\x00" + detail
		if items[key] == nil {
			items[key] = &DayItem{Name: name, Detail: detail, Label: label}
		}
		items[key].Minutes += minutes
		items[key].Visits++
		if withSegments {
			chunk.Segments = append(chunk.Segments, DaySegment{Start: segment.Start, End: segment.End, Name: name, Detail: detail, Title: segment.Title, Label: label})
		}
	}
	if chunk.DistractionMinutes*2 > chunk.ActiveMinutes {
		chunk.Kind = KindDistraction
	} else if chunk.TaskMinutes*2 > chunk.ActiveMinutes {
		chunk.Kind = KindTask
	}
	for _, item := range items {
		item.Minutes = round(item.Minutes)
		chunk.Items = append(chunk.Items, *item)
	}
	sort.Slice(chunk.Items, func(i, j int) bool {
		if chunk.Items[i].Minutes != chunk.Items[j].Minutes {
			return chunk.Items[i].Minutes > chunk.Items[j].Minutes
		}
		if chunk.Items[i].Name != chunk.Items[j].Name {
			return chunk.Items[i].Name < chunk.Items[j].Name
		}
		return chunk.Items[i].Detail < chunk.Items[j].Detail
	})
	chunk.ActiveMinutes, chunk.DistractionMinutes, chunk.TaskMinutes = round(chunk.ActiveMinutes), round(chunk.DistractionMinutes), round(chunk.TaskMinutes)
	return chunk
}

// BuildDay sets the plan next to a compressed timeline of [from, to).
// messages, from ThreadMessages, name the thread behind T3 Code time. rules
// holds, per task, what counts as doing it.
func BuildDay(blocks []PlannedBlock, result Result, labels []Label, messages []ThreadMessage, rules map[int64][]TaskRule, from, to time.Time, withSegments bool) Day {
	c := classifier{labels: labels, messages: messages, blocks: blocks, rules: rules}
	day := Day{Blocks: []DayBlock{}, Distraction: []DeviceMinutes{}, Chunks: []Chunk{}, NoData: result.Gaps}
	if day.NoData == nil {
		day.NoData = []Gap{}
	}
	// Time not on a distraction, on any device, counted once.
	var clean, distracted, task []span
	for _, device := range []string{DeviceMac, DevicePhone} {
		var own []Segment
		var minutes float64
		for _, segment := range result.Segments {
			if segment.Device != device {
				continue
			}
			own = append(own, segment)
			switch c.kindOf(segment) {
			case KindDistraction:
				minutes += segment.Duration().Minutes()
				distracted = append(distracted, span{start: segment.Start, end: segment.End})
			case KindTask:
				task = append(task, span{start: segment.Start, end: segment.End})
				fallthrough
			default:
				clean = append(clean, span{start: segment.Start, end: segment.End})
			}
		}
		sort.SliceStable(own, func(i, j int) bool { return own[i].Start.Before(own[j].Start) })
		day.Chunks = append(day.Chunks, chunks(device, own, c, withSegments)...)
		day.Distraction = append(day.Distraction, DeviceMinutes{Device: device, Minutes: round(minutes)})
		day.DistractionMinutes += minutes
	}
	sort.SliceStable(day.Chunks, func(i, j int) bool { return day.Chunks[i].Start.Before(day.Chunks[j].Start) })
	// One person uses both devices: while either shows a distraction, time on
	// the other does not count as in plan.
	distracted = sequential(distracted)
	without := func(spans []span) []span {
		result := []span{}
		for _, part := range sequential(spans) {
			result = append(result, subtract(part, distracted)...)
		}
		return result
	}
	clean, task = without(clean), without(task)
	total := func(window span, cover []span) float64 {
		var minutes float64
		for _, part := range overlaps(window, cover) {
			minutes += part.end.Sub(part.start).Minutes()
		}
		return minutes
	}
	for _, block := range blocks {
		item := DayBlock{PlannedBlock: block, Status: "past", Tracked: block.TaskID != nil && len(rules[*block.TaskID]) > 0}
		day.PlannedMinutes += block.End.Sub(block.Start).Minutes()
		start, end := later(block.Start, from), earlier(block.End, to)
		switch {
		case !block.Start.Before(to):
			item.Status = "upcoming"
		case end.After(start):
			if block.End.After(to) {
				item.Status = "in_progress"
			}
			window := span{start: start, end: end}
			item.InPlanMinutes, item.DistractionMinutes = round(total(window, clean)), round(total(window, distracted))
			if item.Tracked {
				item.InPlanMinutes = round(total(window, task))
			}
			day.InPlanMinutes += item.InPlanMinutes
		}
		day.Blocks = append(day.Blocks, item)
	}
	day.PlannedMinutes, day.InPlanMinutes, day.DistractionMinutes = round(day.PlannedMinutes), round(day.InPlanMinutes), round(day.DistractionMinutes)
	return day
}
