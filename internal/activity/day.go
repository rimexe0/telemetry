package activity

import (
	"encoding/json"
	"github.com/rimexe0/telemetry/internal/places"
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

// ThreadMessage is one message the owner sent to a coding agent, in a T3
// Code thread or a Codex thread of the ChatGPT app.
type ThreadMessage struct {
	// Tool is "t3" or "codex".
	Tool    string
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

// AgentTurn is one run of a coding agent in a thread, started by a message
// from the owner. Ended is nil while it runs.
type AgentTurn struct {
	// Tool is "t3" or "codex"; ThreadID is unique across machines.
	Tool      string
	ThreadID  string
	Thread    string
	Project   string
	Root      string
	Requested time.Time
	Ended     *time.Time
}

// AgentTurns reads the coding agents' turns from every collector machine,
// in the order they were requested.
func AgentTurns(coding []Record) []AgentTurn {
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
	turns := []AgentTurn{}
	for _, record := range coding {
		if strings.Contains(record.Source, "aw-watcher-codex") {
			// A Codex turn starts when the owner sends a message. The watcher
			// records it when it ends, so every one seen here has ended.
			var event struct {
				Data struct {
					Stream     string `json:"event_stream"`
					ThreadID   string `json:"thread_id"`
					Title      string `json:"chat_title"`
					Path       string `json:"project_path"`
					IsSubagent bool   `json:"is_subagent"`
				} `json:"data"`
			}
			if json.Unmarshal(record.Payload, &event) == nil && event.Data.Stream == "codex.turn" && !event.Data.IsSubagent {
				project := event.Data.Path
				if index := strings.LastIndex(strings.TrimRight(project, "/"), "/"); index >= 0 {
					project = strings.TrimRight(project, "/")[index+1:]
				}
				turn := AgentTurn{Tool: "codex", ThreadID: "codex\x00" + event.Data.ThreadID, Thread: event.Data.Title, Project: project, Root: event.Data.Path,
					Requested: record.Start.UTC()}
				if record.End != nil {
					ended := record.End.UTC()
					turn.Ended = &ended
				} else {
					turn.Ended = &turn.Requested
				}
				turns = append(turns, turn)
			}
			continue
		}
		if record.Kind != "t3.turn" {
			continue
		}
		var payload t3Turn
		if json.Unmarshal(record.Payload, &payload) != nil {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, payload.RequestedAt)
		thread, ok := threads[record.Source+"\x00"+payload.ThreadID]
		if err != nil || !ok {
			continue
		}
		project := projects[record.Source+"\x00"+thread[1]]
		turn := AgentTurn{Tool: "t3", ThreadID: record.Source + "\x00" + payload.ThreadID, Thread: thread[0], Project: project[0], Root: project[1], Requested: at.UTC()}
		if payload.State != "running" {
			ended := turn.Requested
			if completed, err := time.Parse(time.RFC3339Nano, payload.CompletedAt); err == nil {
				ended = completed.UTC()
			}
			turn.Ended = &ended
		}
		turns = append(turns, turn)
	}
	sort.SliceStable(turns, func(i, j int) bool { return turns[i].Requested.Before(turns[j].Requested) })
	return turns
}

// ThreadMessages lists the owner's messages to coding agents, in time order.
// A message is the only record of which thread was in use.
func ThreadMessages(coding []Record) []ThreadMessage {
	messages := []ThreadMessage{}
	for _, turn := range AgentTurns(coding) {
		messages = append(messages, ThreadMessage{Tool: turn.Tool, At: turn.Requested, Thread: turn.Thread, Project: turn.Project, Root: turn.Root})
	}
	return messages
}

// agentTool names the coding-agent tool an app is: "t3" for T3 Code, "codex"
// for the ChatGPT app on the Mac (its Codex threads), or none.
func agentTool(app string) string {
	switch {
	case strings.HasPrefix(app, "T3 Code") || strings.HasPrefix(app, "com.t3tools."):
		return "t3"
	case app == "ChatGPT" || app == "Codex":
		return "codex"
	}
	return ""
}

// threadRef is the agent thread behind a stretch of time: its label
// ("project · thread"), its project name and its project folder.
type threadRef struct {
	label, project, root string
}

// openCuts are the times a T3 client of the device's kind changed project.
func (c classifier) openCuts(device string) []time.Time {
	cuts := []time.Time{}
	for _, span := range c.open {
		if span.Kind == openKind(device) {
			cuts = append(cuts, span.Start, span.End)
		}
	}
	sort.Slice(cuts, func(i, j int) bool { return cuts[i].Before(cuts[j]) })
	return cuts
}

// threadFor names the agent thread behind a segment. T3 Code's own report of
// the open project wins; the thread inside it is the one messaged nearest in
// time in that project. Without a report, it falls back to the nearest
// message in the tool.
func (c classifier) threadFor(segment Segment) threadRef {
	open := openFor(segment, c.open)
	if open == nil {
		return threadFor(segment, c.messages)
	}
	ref := threadRef{label: open.Project, project: open.Project, root: open.Root}
	var inProject []ThreadMessage
	for _, message := range c.messages {
		if message.Tool == "t3" && message.Project == open.Project {
			inProject = append(inProject, message)
		}
	}
	if near := threadFor(segment, inProject); near.label != "" {
		ref.label = near.label
	}
	return ref
}

// threadFor names the thread a stretch of time in an agent app most likely
// belongs to: the one with the message sent closest to it in that tool.
// Empty when no message is near, because then nothing shows which thread was
// open.
func threadFor(segment Segment, messages []ThreadMessage) threadRef {
	tool := agentTool(segment.App)
	if tool == "" {
		return threadRef{}
	}
	best, bestDistance := threadRef{}, threadReach+1
	index := sort.Search(len(messages), func(i int) bool { return !messages[i].At.Before(segment.Start.Add(-threadReach)) })
	for ; index < len(messages) && !messages[index].At.After(segment.End.Add(threadReach)); index++ {
		message := messages[index]
		if message.Tool != tool {
			continue
		}
		distance := time.Duration(0)
		if message.At.Before(segment.Start) {
			distance = segment.Start.Sub(message.At)
		} else if message.At.After(segment.End) {
			distance = message.At.Sub(segment.End)
		}
		if distance < bestDistance {
			bestDistance = distance
			best = threadRef{label: message.Thread, project: message.Project, root: message.Root}
			if message.Project != "" {
				best.label = message.Project + " · " + message.Thread
			}
		}
	}
	return best
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
	Kind               string  `json:"kind"`
	ActiveMinutes      float64 `json:"active_minutes"`
	DistractionMinutes float64 `json:"distraction_minutes"`
	TaskMinutes        float64 `json:"task_minutes"`
	// Task names the task most of a "task" chunk went to.
	Task     string       `json:"task,omitempty"`
	Items    []DayItem    `json:"items"`
	Segments []DaySegment `json:"segments,omitempty"`
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

// DayTask is one of the owner's open tasks with the time that went to it.
// The owner's attention, the agent's runtime and the time the agent sat
// stopped waiting for the owner are three different measurements.
type DayTask struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	// Tracked means the task has rules, memory or a claim, so Minutes can be
	// other than zero.
	Tracked bool `json:"tracked"`
	// Minutes is the owner's own attention on the task.
	Minutes float64 `json:"minutes"`
	// StartedAt is when the owner first got to it today.
	StartedAt *time.Time `json:"started_at,omitempty"`
	// Returns counts how often the owner came back to it after being
	// elsewhere for a while.
	Returns int `json:"returns"`
	// AgentMinutes is time its coding agent ran; it is not the owner's time.
	AgentMinutes float64 `json:"agent_minutes"`
	// WaitingMinutes is time its agent had stopped and it was the owner's
	// turn, until they came back to it.
	WaitingMinutes float64 `json:"waiting_minutes"`
	// YourTurnSince is set while its agent has stopped and the owner has not
	// come back yet.
	YourTurnSince *time.Time `json:"your_turn_since,omitempty"`
}

// A visit to a task ends after this long away from it; coming back after
// that is a return.
const returnGap = 2 * time.Minute

// taskOfTurn names the one task an agent turn belongs to: by a folder rule,
// or by the task's memory of that project. None when several tasks match.
func (c classifier) taskOfTurn(turn AgentTurn) (int64, bool) {
	var found int64
	count := 0
	for _, task := range c.tasks {
		matched := onTask(Segment{}, turn.Root, task.Rules)
		suffix := strings.ToLower(" · " + turn.Project)
		if turn.Project != "" {
			for _, known := range task.Related {
				matched = matched || strings.HasSuffix(strings.ToLower(known), suffix)
			}
			for _, known := range task.Unrelated {
				if strings.HasSuffix(strings.ToLower(known), suffix) {
					matched = false
				}
			}
		}
		if matched {
			found = task.ID
			count++
		}
	}
	return found, count == 1
}

// agentStats fills in, per task, the agent's runtime, the time it waited for
// the owner, and whether it is waiting now. attention holds each task's
// spans of the owner's attention, sorted and disjoint.
func (c classifier) agentStats(turns []AgentTurn, attention map[int64][]span, from, to time.Time, tasks []DayTask) (running bool) {
	byThread := map[string][]AgentTurn{}
	taskOf := map[string]int64{}
	for _, turn := range turns {
		task, ok := c.taskOfTurn(turn)
		if !ok {
			continue
		}
		byThread[turn.ThreadID] = append(byThread[turn.ThreadID], turn)
		taskOf[turn.ThreadID] = task
	}
	index := map[int64]int{}
	for i := range tasks {
		index[tasks[i].ID] = i
	}
	ran := map[int64][]span{}
	for thread, list := range byThread {
		task := &tasks[index[taskOf[thread]]]
		for i, turn := range list {
			end := to
			if turn.Ended != nil {
				end = *turn.Ended
			} else if to.Sub(turn.Requested) < 2*time.Hour {
				// A turn "running" for hours is a stale record, not an agent at work.
				running = true
			}
			if start, stop := later(turn.Requested, from), earlier(end, to); stop.After(start) {
				ran[task.ID] = append(ran[task.ID], span{start: start, end: stop})
			}
			if turn.Ended == nil || turn.Ended.Before(from) || !turn.Ended.Before(to) {
				continue
			}
			// It became the owner's turn when the agent stopped. That lasts
			// until they send the next message there or their attention is
			// back on the task.
			stopped, back, returned := *turn.Ended, to, false
			if i+1 < len(list) {
				back, returned = earlier(back, later(list[i+1].Requested, stopped)), true
			}
			for _, visit := range attention[task.ID] {
				if visit.end.After(stopped) {
					back, returned = earlier(back, later(visit.start, stopped)), true
					break
				}
			}
			task.WaitingMinutes += back.Sub(stopped).Minutes()
			if !returned && i+1 == len(list) {
				if task.YourTurnSince == nil || stopped.Before(*task.YourTurnSince) {
					since := stopped
					task.YourTurnSince = &since
				}
			}
		}
	}
	for i := range tasks {
		tasks[i].AgentMinutes = round(minutesIn(span{start: from, end: to}, sequential(ran[tasks[i].ID])))
		tasks[i].WaitingMinutes = round(tasks[i].WaitingMinutes)
	}
	return running
}

// Day is one day for the phone's home screen and its details page.
type Day struct {
	Blocks []DayBlock `json:"blocks"`
	Tasks  []DayTask  `json:"tasks"`
	// InPlanMinutes is time on the owner's tasks plus undistracted time in
	// planned blocks that have no rules to check against.
	PlannedMinutes     float64         `json:"planned_minutes"`
	InPlanMinutes      float64         `json:"in_plan_minutes"`
	DistractionMinutes float64         `json:"distraction_minutes"`
	Distraction        []DeviceMinutes `json:"distraction_by_device"`
	Chunks             []Chunk         `json:"chunks"`
	NoData             []Gap           `json:"no_data"`
	// Breaks between stretches of task work, and their totals by kind.
	Breaks           []Break `json:"breaks"`
	RestMinutes      float64 `json:"rest_minutes"`
	ScrollingMinutes float64 `json:"scrolling_minutes"`
	// Outside is time away from home, from leaving to coming back, as the
	// phone's GPS shows it; empty when home is not known.
	Outside        []Away  `json:"outside"`
	OutsideMinutes float64 `json:"outside_minutes"`
}

// Away is a stretch away from home.
type Away struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	// Ongoing means not back yet: End is the last position recorded.
	Ongoing bool `json:"ongoing,omitempty"`
}

// outside finds the stretches away from home in [from, to): from the end of
// one stay at home to the start of the next.
func outside(fixes []places.Fix, home *[2]float64, from, to time.Time) []Away {
	result := []Away{}
	if home == nil || len(fixes) == 0 {
		return result
	}
	var left *time.Time
	for _, stay := range places.Stays(fixes, home) {
		if !stay.Home {
			continue
		}
		if left != nil && stay.Start.After(*left) {
			result = append(result, Away{Start: *left, End: stay.Start})
		}
		// Each stay at home ends with leaving again.
		end := stay.End
		left = &end
	}
	// Left home and not back: out until the last position.
	if left != nil {
		if last := fixes[len(fixes)-1]; last.At.After(*left) && places.Meters(last.Lat, last.Lon, home[0], home[1]) > places.HomeRadius {
			result = append(result, Away{Start: *left, End: last.At, Ongoing: true})
		}
	}
	clipped := []Away{}
	for _, away := range result {
		// Under StayMin is a position wobbling past the edge of home, not going out.
		if away.End.Sub(away.Start) < places.StayMin {
			continue
		}
		away.Start, away.End = later(away.Start, from), earlier(away.End, to)
		if away.End.After(away.Start) {
			clipped = append(clipped, away)
		}
	}
	return clipped
}

// Break is time between two stretches of work on the day's tasks, or since
// the last one when it is recent. Rest is time away from screens; a break
// spent on distractions is scrolling, not rest (the owner's words: breaks
// turn into doomscrolling, which makes everything worse).
type Break struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	// Kind is "rest" (mostly away from the Mac and phone), "scrolling" (on
	// distractions), "screen" (on screens, not distractions) or "unknown"
	// (the phone was not reporting, so being away is not shown).
	Kind               string  `json:"kind"`
	AwayMinutes        float64 `json:"away_minutes"`
	DistractionMinutes float64 `json:"distraction_minutes"`
	// Ongoing means the break has not ended yet.
	Ongoing bool `json:"ongoing,omitempty"`
}

const (
	// A pause shorter than breakMin is part of the work; a gap longer than
	// breakMax is no longer a break between two stretches of work.
	breakMin = 3 * time.Minute
	breakMax = time.Hour
	// Distractions this long make a break scrolling.
	breakScrolling = 2 * time.Minute
)

// breaks finds the breaks in [from, to) from the task work, the time on any
// screen, the time on distractions and the phone's missing data.
func breaks(work, screen, distracted []span, phoneMissing []span, from, to time.Time) []Break {
	result := []Break{}
	add := func(start, end time.Time, ongoing bool) {
		length := end.Sub(start)
		if length < breakMin || length > breakMax {
			return
		}
		window := span{start: start, end: end}
		item := Break{Start: start, End: end, Ongoing: ongoing,
			AwayMinutes:        round(length.Minutes() - minutesIn(window, screen)),
			DistractionMinutes: round(minutesIn(window, distracted))}
		switch {
		case time.Duration(item.DistractionMinutes*float64(time.Minute)) >= breakScrolling:
			item.Kind = "scrolling"
		case minutesIn(window, phoneMissing) > length.Minutes()/3:
			item.Kind = "unknown"
		case item.AwayMinutes >= length.Minutes()*2/3:
			item.Kind = "rest"
		default:
			item.Kind = "screen"
		}
		result = append(result, item)
	}
	for i := 1; i < len(work); i++ {
		add(work[i-1].end, work[i].start, false)
	}
	if n := len(work); n > 0 && work[n-1].end.Before(to) {
		add(work[n-1].end, to, true)
	}
	return result
}

func segmentName(segment Segment) string {
	if segment.Domain != "" {
		return segment.Domain
	}
	return segment.App
}

// TaskInfo is one of the owner's open tasks with what counts as doing it:
// its rules, and its memory of what the owner or the judge said is and is
// not part of it.
type TaskInfo struct {
	ID         int64
	Title      string
	NextAction string
	Rules      []TaskRule
	Related    []string
	Unrelated  []string
}

// FocusSpan is a stretch in which the owner chose to do one task only.
type FocusSpan struct {
	TaskID int64
	Start  time.Time
	End    time.Time
}

// Claim is the owner's own word about one thing on screen: from Start, while
// they stay at the Mac and until the next claim, Context (an app or site,
// with its project and thread for an agent app) is part of a task (TaskID),
// or of nothing on the plan (TaskID nil). It only settles time that no rule
// decides, and only for that thing: other things keep their own reading.
type Claim struct {
	TaskID  *int64
	Context string
	Start   time.Time
	End     *time.Time
}

// DayInput is everything BuildDay and Track read.
type DayInput struct {
	Blocks []PlannedBlock
	Result Result
	Labels []Label
	// Messages, from ThreadMessages, name the thread behind T3 Code time.
	Messages []ThreadMessage
	// Turns, from AgentTurns, say when each task's agent ran and stopped.
	Turns  []AgentTurn
	Tasks  []TaskInfo
	Claims []Claim
	Focus  []FocusSpan
	// Open, from OpenSpans, says which project T3 Code showed; without it
	// the project is guessed from Messages.
	Open     []OpenSpan
	From, To time.Time
	// WithSegments keeps every record behind each chunk.
	WithSegments bool
	// Fixes are the phone's positions from before From to To, and Home is
	// where it spends the nights; together they give time outside.
	Fixes []places.Fix
	Home  *[2]float64
}

// A claim ends when the Mac has been left for this long.
const claimPause = 5 * time.Minute

type claimSpan struct {
	span
	task    *int64
	context string
}

// contextOf names what a segment shows, the way Tracking.Context does.
func (c classifier) contextOf(segment Segment) string {
	name := segmentName(segment)
	if detail := c.threadFor(segment).label; detail != "" {
		name += " · " + detail
	}
	return name
}

// LearnContext is the level at which an answer about a segment is
// remembered: the app or site, and for an agent app its project, so one
// answer covers every thread of that project.
func (c classifier) learnContext(segment Segment) string {
	name := segmentName(segment)
	if project := c.threadFor(segment).project; project != "" {
		name += " · " + project
	}
	return name
}

// learned returns what the task's memory says about a segment: "related",
// "unrelated" or "" when it has nothing. The most specific entry wins.
func (c classifier) learned(task TaskInfo, segment Segment) string {
	for _, context := range []string{c.contextOf(segment), c.learnContext(segment), segmentName(segment)} {
		for _, known := range task.Unrelated {
			if strings.EqualFold(known, context) {
				return "unrelated"
			}
		}
		for _, known := range task.Related {
			if strings.EqualFold(known, context) {
				return "related"
			}
		}
	}
	return ""
}

// related reports whether a segment counts as the task by its rules or its
// memory. A memory of "unrelated" overrides a rule: the owner said so.
func (c classifier) related(task TaskInfo, segment Segment) bool {
	switch c.learned(task, segment) {
	case "related":
		return true
	case "unrelated":
		return false
	}
	return onTask(segment, c.threadFor(segment).root, task.Rules)
}

// claimedFor reports whether the owner said, for this sitting, that what the
// segment shows is part of the task.
func (c classifier) claimedFor(task int64, segment Segment) bool {
	if segment.Device != DeviceMac {
		return false
	}
	for _, claim := range c.claims {
		if claim.task != nil && *claim.task == task && !segment.Start.Before(claim.start) && segment.Start.Before(claim.end) && claim.context == c.contextOf(segment) {
			return true
		}
	}
	return false
}

func (c classifier) task(id int64) TaskInfo {
	for _, task := range c.tasks {
		if task.ID == id {
			return task
		}
	}
	return TaskInfo{ID: id}
}

// focusAt returns the focus session covering a moment, if any.
func (c classifier) focusAt(at time.Time) *FocusSpan {
	for i := range c.focus {
		if !at.Before(c.focus[i].Start) && at.Before(c.focus[i].End) {
			return &c.focus[i]
		}
	}
	return nil
}

// Verdicts on what is on screen during a focus session.
const (
	FocusOn      = "on"
	FocusOff     = "off"
	FocusUnknown = "unknown"
)

// focusVerdict judges a segment against the task in focus. In a focus
// session only the task itself counts: another planned task is off too.
// What nothing decides stays unknown, to be judged or asked about; it is
// never assumed to be drift.
func (c classifier) focusVerdict(task TaskInfo, segment Segment) string {
	if LabelFor(c.labels, segment) == Distraction {
		return FocusOff
	}
	if c.claimedFor(task.ID, segment) {
		return FocusOn
	}
	switch c.learned(task, segment) {
	case "related":
		return FocusOn
	case "unrelated":
		return FocusOff
	}
	if onTask(segment, c.threadFor(segment).root, task.Rules) {
		return FocusOn
	}
	for _, other := range c.tasks {
		if other.ID != task.ID && c.related(other, segment) {
			return FocusOff
		}
	}
	return FocusUnknown
}

// classifier decides each segment's kind, and its task, for one day.
type classifier struct {
	labels   []Label
	messages []ThreadMessage
	blocks   []PlannedBlock
	tasks    []TaskInfo
	claims   []claimSpan
	focus    []FocusSpan
	open     []OpenSpan
}

func newClassifier(input DayInput) classifier {
	c := classifier{labels: input.Labels, messages: input.Messages, blocks: input.Blocks, tasks: input.Tasks, focus: input.Focus, open: input.Open}
	var mac []Segment
	for _, segment := range input.Result.Segments {
		if segment.Device == DeviceMac {
			mac = append(mac, segment)
		}
	}
	sort.SliceStable(mac, func(i, j int) bool { return mac[i].Start.Before(mac[j].Start) })
	claims := append([]Claim(nil), input.Claims...)
	sort.SliceStable(claims, func(i, j int) bool { return claims[i].Start.Before(claims[j].Start) })
	for i, claim := range claims {
		// It lasts while the owner stays at the Mac.
		end := claim.Start.Add(claimPause)
		for _, segment := range mac {
			if segment.End.Before(claim.Start) {
				continue
			}
			if segment.Start.After(end) {
				break
			}
			end = later(end, segment.End.Add(claimPause))
		}
		if claim.End != nil {
			end = earlier(end, *claim.End)
		}
		if i+1 < len(claims) {
			end = earlier(end, claims[i+1].Start)
		}
		if end = earlier(end, input.To); end.After(claim.Start) {
			c.claims = append(c.claims, claimSpan{span: span{start: claim.Start, end: end}, task: claim.TaskID, context: claim.Context})
		}
	}
	return c
}

// splitAtClaims cuts segments where a claim or a focus session starts or
// ends, so either takes effect from that moment inside one long window.
func (c classifier) splitAtClaims(segments []Segment) []Segment {
	var macCuts, cuts []time.Time
	for _, claim := range c.claims {
		macCuts = append(macCuts, claim.start, claim.end)
	}
	for _, span := range c.focus {
		cuts = append(cuts, span.Start, span.End)
	}
	sort.Slice(macCuts, func(i, j int) bool { return macCuts[i].Before(macCuts[j]) })
	sort.Slice(cuts, func(i, j int) bool { return cuts[i].Before(cuts[j]) })
	result := []Segment{}
	for _, segment := range segments {
		parts := []Segment{segment}
		split := func(list []time.Time) {
			for _, cut := range list {
				last := parts[len(parts)-1]
				if cut.After(last.Start) && cut.Before(last.End) {
					first, second := last, last
					first.End, second.Start = cut, cut
					parts = append(parts[:len(parts)-1], first, second)
				}
			}
		}
		split(cuts)
		// T3 Code time changes project where its client says so.
		if agentTool(segment.App) == "t3" {
			var again []Segment
			for _, part := range parts {
				parts = []Segment{part}
				split(c.openCuts(segment.Device))
				again = append(again, parts...)
			}
			parts = again
		}
		if segment.Device == DeviceMac {
			var again []Segment
			for _, part := range parts {
				parts = []Segment{part}
				split(macCuts)
				again = append(again, parts...)
			}
			parts = again
		}
		result = append(result, parts...)
	}
	return result
}

func (c classifier) title(id int64) string {
	for _, task := range c.tasks {
		if task.ID == id {
			return task.Title
		}
	}
	return ""
}

// matches lists the tasks whose rules a segment meets.
func (c classifier) matches(segment Segment) []int64 {
	ids := []int64{}
	for _, task := range c.tasks {
		if c.related(task, segment) {
			ids = append(ids, task.ID)
		}
	}
	return ids
}

// classify returns a segment's kind and, for task time, the task. The order
// is: a distraction is always a distraction; then the planned block's task;
// then the one task whose rules match; then, for time no rule decides, the
// owner's own claim. The owner does several things at once, so one answer
// never overrides what the rules recognise.
func (c classifier) classify(segment Segment) (string, int64) {
	if LabelFor(c.labels, segment) == Distraction {
		return KindDistraction, 0
	}
	if span := c.focusAt(segment.Start); span != nil {
		if c.focusVerdict(c.task(span.TaskID), segment) == FocusOn {
			return KindTask, span.TaskID
		}
		return KindOther, 0
	}
	matched := c.matches(segment)
	for _, block := range c.blocks {
		if block.TaskID == nil || segment.Start.Before(block.Start) || !segment.Start.Before(block.End) {
			continue
		}
		for _, id := range matched {
			if id == *block.TaskID {
				return KindTask, id
			}
		}
	}
	if len(matched) == 1 {
		return KindTask, matched[0]
	}
	if segment.Device == DeviceMac {
		for _, claim := range c.claims {
			if !segment.Start.Before(claim.start) && segment.Start.Before(claim.end) && claim.context == c.contextOf(segment) {
				// Several matching tasks: the claim chooses only among them.
				if claim.task == nil || (len(matched) > 1 && !contains(matched, *claim.task)) {
					return KindOther, 0
				}
				return KindTask, *claim.task
			}
		}
	}
	return KindOther, 0
}

func contains(ids []int64, id int64) bool {
	for _, item := range ids {
		if item == id {
			return true
		}
	}
	return false
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
		kind, _ := c.classify(segment)
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
	perTask := map[int64]float64{}
	for _, segment := range segments {
		minutes := segment.Duration().Minutes()
		name, label := segmentName(segment), LabelFor(c.labels, segment)
		chunk.ActiveMinutes += minutes
		switch kind, task := c.classify(segment); kind {
		case KindDistraction:
			chunk.DistractionMinutes += minutes
		case KindTask:
			chunk.TaskMinutes += minutes
			perTask[task] += minutes
		}
		detail := c.threadFor(segment).label
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
		var most int64
		for id, minutes := range perTask {
			if most == 0 || minutes > perTask[most] || (minutes == perTask[most] && id < most) {
				most = id
			}
		}
		chunk.Task = c.title(most)
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

func minutesIn(window span, cover []span) float64 {
	var minutes float64
	for _, part := range overlaps(window, cover) {
		minutes += part.end.Sub(part.start).Minutes()
	}
	return minutes
}

// BuildDay sets the plan and the owner's tasks next to a compressed
// timeline of [From, To). A task needs no time block: its time is whatever
// matched its rules, or was claimed for it, anywhere in the day.
func BuildDay(input DayInput) Day {
	c := newClassifier(input)
	input.Result.Segments = c.splitAtClaims(input.Result.Segments)
	from, to := input.From, input.To
	day := Day{Blocks: []DayBlock{}, Tasks: []DayTask{}, Distraction: []DeviceMinutes{}, Chunks: []Chunk{}, NoData: input.Result.Gaps, Breaks: []Break{},
		Outside: outside(input.Fixes, input.Home, input.From, input.To)}
	for _, away := range day.Outside {
		day.OutsideMinutes += away.End.Sub(away.Start).Minutes()
	}
	day.OutsideMinutes = round(day.OutsideMinutes)
	if day.NoData == nil {
		day.NoData = []Gap{}
	}
	// Time not on a distraction, on any device, counted once.
	var clean, distracted []span
	perTask := map[int64][]span{}
	for _, device := range []string{DeviceMac, DevicePhone} {
		var own []Segment
		var minutes float64
		for _, segment := range input.Result.Segments {
			if segment.Device != device {
				continue
			}
			own = append(own, segment)
			part := span{start: segment.Start, end: segment.End}
			switch kind, task := c.classify(segment); kind {
			case KindDistraction:
				minutes += segment.Duration().Minutes()
				distracted = append(distracted, part)
			case KindTask:
				perTask[task] = append(perTask[task], part)
				fallthrough
			default:
				clean = append(clean, part)
			}
		}
		sort.SliceStable(own, func(i, j int) bool { return own[i].Start.Before(own[j].Start) })
		day.Chunks = append(day.Chunks, chunks(device, own, c, input.WithSegments)...)
		day.Distraction = append(day.Distraction, DeviceMinutes{Device: device, Minutes: round(minutes)})
		day.DistractionMinutes += minutes
	}
	sort.SliceStable(day.Chunks, func(i, j int) bool { return day.Chunks[i].Start.Before(day.Chunks[j].Start) })
	// One person uses both devices: while either shows a distraction, time on
	// the other does not count.
	distracted = sequential(distracted)
	without := func(spans []span) []span {
		result := []span{}
		for _, part := range sequential(spans) {
			result = append(result, subtract(part, distracted)...)
		}
		return result
	}
	clean = without(clean)
	whole := span{start: from, end: to}
	var counted []span
	claimed := map[int64]bool{}
	for _, claim := range c.claims {
		if claim.task != nil {
			claimed[*claim.task] = true
		}
	}
	for _, task := range input.Tasks {
		spans := without(perTask[task.ID])
		perTask[task.ID] = spans
		counted = append(counted, spans...)
		item := DayTask{ID: task.ID, Title: task.Title, Tracked: len(task.Rules)+len(task.Related) > 0 || claimed[task.ID], Minutes: round(minutesIn(whole, spans))}
		// Visits: stretches of attention with no more than a short break between them.
		visits := 0
		var last time.Time
		for _, visit := range spans {
			if visits == 0 || visit.start.Sub(last) > returnGap {
				visits++
			}
			last = visit.end
		}
		if visits > 0 {
			started := spans[0].start
			item.StartedAt, item.Returns = &started, visits-1
		}
		day.Tasks = append(day.Tasks, item)
	}
	c.agentStats(input.Turns, perTask, from, to, day.Tasks)
	rules := map[int64]bool{}
	for _, task := range input.Tasks {
		rules[task.ID] = len(task.Rules) > 0
	}
	for _, block := range input.Blocks {
		item := DayBlock{PlannedBlock: block, Status: "past", Tracked: block.TaskID != nil && rules[*block.TaskID]}
		// A focus block with no task only closes distractions; it plans nothing.
		focusOnly := block.TaskID == nil && block.Closes
		if !focusOnly {
			day.PlannedMinutes += block.End.Sub(block.Start).Minutes()
		}
		start, end := later(block.Start, from), earlier(block.End, to)
		switch {
		case !block.Start.Before(to):
			item.Status = "upcoming"
		case end.After(start):
			if block.End.After(to) {
				item.Status = "in_progress"
			}
			window := span{start: start, end: end}
			item.DistractionMinutes = round(minutesIn(window, distracted))
			switch {
			case item.Tracked:
				item.InPlanMinutes = round(minutesIn(window, perTask[*block.TaskID]))
			case !focusOnly:
				item.InPlanMinutes = round(minutesIn(window, clean))
				counted = append(counted, overlaps(window, clean)...)
			}
		}
		day.Blocks = append(day.Blocks, item)
	}
	day.InPlanMinutes = minutesIn(whole, sequential(counted))
	var work, screen, phoneMissing []span
	for _, spans := range perTask {
		work = append(work, spans...)
	}
	for _, segment := range input.Result.Segments {
		screen = append(screen, span{start: segment.Start, end: segment.End})
	}
	for _, gap := range input.Result.Gaps {
		if gap.Device == DevicePhone {
			phoneMissing = append(phoneMissing, span{start: gap.Start, end: gap.End})
		}
	}
	day.Breaks = breaks(sequential(work), sequential(screen), distracted, sequential(phoneMissing), from, to)
	for _, item := range day.Breaks {
		switch item.Kind {
		case "rest":
			day.RestMinutes += item.AwayMinutes
		case "scrolling":
			day.ScrollingMinutes += item.DistractionMinutes
		}
	}
	day.RestMinutes, day.ScrollingMinutes = round(day.RestMinutes), round(day.ScrollingMinutes)
	day.PlannedMinutes, day.InPlanMinutes, day.DistractionMinutes = round(day.PlannedMinutes), round(day.InPlanMinutes), round(day.DistractionMinutes)
	return day
}

// Tracking is what the owner is on at the Mac right now, so a device can
// tell them what is being tracked.
type Tracking struct {
	// State is "away" (nothing recent on the Mac), "distraction", "task" or
	// "other" (something that is not one of the open tasks).
	State  string     `json:"state"`
	TaskID *int64     `json:"task_id,omitempty"`
	Task   string     `json:"task,omitempty"`
	Since  *time.Time `json:"since,omitempty"`
	// Context is what is on screen: the app or site and, for an agent app,
	// its project and thread. LearnContext is what an answer about it would
	// be remembered as.
	Context      string `json:"context,omitempty"`
	LearnContext string `json:"learn_context,omitempty"`
	// Excused means the owner said this thing is a one-off outside the plan.
	Excused bool `json:"excused,omitempty"`
	// AgentRunning means a task's agent is at work, so being elsewhere is
	// filling a wait.
	AgentRunning bool `json:"agent_running"`
	// YourTurn lists the tasks whose agent has stopped and is waiting for
	// the owner.
	YourTurn []TurnRef `json:"your_turn"`
	// Focus is set while a focus session is running.
	Focus *FocusNow `json:"focus,omitempty"`
}

// TurnRef is a task whose agent stopped at Since and waits for the owner.
type TurnRef struct {
	TaskID int64     `json:"task_id"`
	Task   string    `json:"task"`
	Since  time.Time `json:"since"`
}

// FocusNow is the running focus session and how what is on screen stands
// against its task.
type FocusNow struct {
	TaskID     int64     `json:"task_id"`
	Task       string    `json:"task"`
	NextAction string    `json:"next_action,omitempty"`
	Since      time.Time `json:"since"`
	// On is "on", "off", "unknown" or "away" (nothing recent on the Mac).
	On string `json:"on"`
	// Context is what is on screen; LearnContext is what an answer about it
	// would be remembered as; Title is the window or page title.
	Context      string `json:"context,omitempty"`
	LearnContext string `json:"learn_context,omitempty"`
	Title        string `json:"title,omitempty"`
	// OffSince is when the current stretch away from the task began.
	OffSince *time.Time `json:"off_since,omitempty"`
}

type TaskRef struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

// Activity older than this is not "now".
const trackRecent = 3 * time.Minute

// Track reads the Mac's most recent activity in input (up to input.To), and
// which tasks' agents are running or waiting.
func Track(input DayInput) Tracking {
	c := newClassifier(input)
	segments := c.splitAtClaims(input.Result.Segments)
	var mac []Segment
	attention := map[int64][]span{}
	for _, segment := range segments {
		if segment.Device == DeviceMac {
			mac = append(mac, segment)
		}
		if kind, task := c.classify(segment); kind == KindTask {
			attention[task] = append(attention[task], span{start: segment.Start, end: segment.End})
		}
	}
	sort.SliceStable(mac, func(i, j int) bool { return mac[i].Start.Before(mac[j].Start) })
	tasks := make([]DayTask, len(input.Tasks))
	for i, task := range input.Tasks {
		tasks[i] = DayTask{ID: task.ID, Title: task.Title}
		attention[task.ID] = sequential(attention[task.ID])
	}
	tracking := Tracking{State: "away", YourTurn: []TurnRef{}}
	tracking.AgentRunning = c.agentStats(input.Turns, attention, input.From, input.To, tasks)
	for _, task := range tasks {
		if task.YourTurnSince != nil {
			tracking.YourTurn = append(tracking.YourTurn, TurnRef{TaskID: task.ID, Task: task.Title, Since: *task.YourTurnSince})
		}
	}
	if span := c.focusAt(input.To.Add(-time.Nanosecond)); span != nil {
		info := c.task(span.TaskID)
		tracking.Focus = &FocusNow{TaskID: info.ID, Task: info.Title, NextAction: info.NextAction, Since: span.Start, On: "away"}
	}
	if len(mac) == 0 || input.To.Sub(lastEnd(mac)) > trackRecent {
		return tracking
	}
	last := mac[len(mac)-1]
	if focus := tracking.Focus; focus != nil {
		info := c.task(focus.TaskID)
		focus.On, focus.Context, focus.LearnContext, focus.Title = c.focusVerdict(info, last), c.contextOf(last), c.learnContext(last), last.Title
		if focus.On != FocusOn {
			// The stretch away from the task that leads up to now.
			off := last.Start
			for i := len(mac) - 1; i >= 0; i-- {
				if mac[i].Start.Before(focus.Since) || c.focusVerdict(info, mac[i]) == FocusOn || off.Sub(mac[i].End) > claimPause {
					break
				}
				off = mac[i].Start
			}
			focus.OffSince = &off
		}
	}
	kind, task := c.classify(last)
	// The run of the same kind and task leading up to now.
	since := last.Start
	for i := len(mac) - 1; i >= 0; i-- {
		otherKind, otherTask := c.classify(mac[i])
		if otherKind != kind || otherTask != task || since.Sub(mac[i].End) > claimPause {
			break
		}
		since = mac[i].Start
	}
	tracking.State, tracking.Since, tracking.Context, tracking.LearnContext = kind, &since, c.contextOf(last), c.learnContext(last)
	if kind == KindTask {
		tracking.TaskID, tracking.Task = &task, c.title(task)
	}
	for _, claim := range c.claims {
		if claim.task == nil && claim.context == tracking.Context && !last.Start.Before(claim.start) && last.Start.Before(claim.end) {
			tracking.Excused = true
		}
	}
	return tracking
}
