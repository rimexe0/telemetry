package activity

import (
	"fmt"
	"sort"
	"time"
)

// Distraction is the owner's label that blocking follows.
const Distraction = "distraction"

// PlannedBlock is one block of a day's plan as a review needs it.
type PlannedBlock struct {
	ID     int64     `json:"id"`
	Title  string    `json:"title"`
	TaskID *int64    `json:"task_id,omitempty"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	State  string    `json:"state"`
	Closes bool      `json:"closes_distractions"`
}

// CodingWork is agent work on one thread inside a stretch of time.
type CodingWork struct {
	Device       string  `json:"device"`
	Title        string  `json:"title"`
	Project      string  `json:"project,omitempty"`
	MessagesSent int     `json:"messages_sent"`
	AgentMinutes float64 `json:"agent_minutes"`
}

// Observed is what the devices recorded in one stretch of time.
type Observed struct {
	// Minutes is how much of the stretch has passed.
	Minutes float64         `json:"minutes"`
	Devices []DeviceSummary `json:"devices"`
	// DistractionMinutes is active time on things labelled "distraction".
	DistractionMinutes float64      `json:"distraction_minutes"`
	Coding             []CodingWork `json:"coding,omitempty"`
}

// BlockReview puts what was recorded next to what was planned. It states
// evidence only: nothing here says whether the owner followed the plan.
type BlockReview struct {
	PlannedBlock
	// Status is "upcoming", "in_progress" or "past".
	Status   string    `json:"status"`
	Observed *Observed `json:"observed,omitempty"`
}

type PlanReview struct {
	Blocks []BlockReview `json:"blocks"`
	// Outside is the day's time that no block covers.
	Outside Observed `json:"outside_blocks"`
}

// clip keeps the parts of r inside the sorted, disjoint windows.
func (r Result) clip(windows []span) Result {
	clipped := Result{LastRecord: r.LastRecord}
	for _, segment := range r.Segments {
		for _, part := range overlaps(span{start: segment.Start, end: segment.End}, windows) {
			piece := segment
			piece.Start, piece.End = part.start, part.end
			clipped.Segments = append(clipped.Segments, piece)
		}
	}
	for _, gap := range r.Gaps {
		for _, part := range overlaps(span{start: gap.Start, end: gap.End}, windows) {
			clipped.Gaps = append(clipped.Gaps, Gap{Device: gap.Device, Start: part.start, End: part.end})
		}
	}
	return clipped
}

func observe(result Result, windows []span, labels []Label, limit int) Observed {
	observed := Observed{Devices: Summarize(result.clip(windows), limit, labels)}
	for _, window := range windows {
		observed.Minutes += window.end.Sub(window.start).Minutes()
	}
	for _, device := range observed.Devices {
		for _, label := range device.Labels {
			if label.Name == Distraction {
				observed.DistractionMinutes += label.Minutes
			}
		}
	}
	observed.Minutes, observed.DistractionMinutes = round(observed.Minutes), round(observed.DistractionMinutes)
	return observed
}

// ReviewPlan sets the recorded activity in [from, to) against the plan's
// blocks. result must cover [from, to); coding holds the T3 records.
func ReviewPlan(blocks []PlannedBlock, result Result, coding []Record, labels []Label, from, to time.Time, limit int) PlanReview {
	review := PlanReview{Blocks: []BlockReview{}}
	planned := []span{}
	for _, block := range blocks {
		item := BlockReview{PlannedBlock: block, Status: "past"}
		start, end := later(block.Start, from), earlier(block.End, to)
		switch {
		case !end.After(start):
			if !block.Start.Before(to) {
				item.Status = "upcoming"
			}
		default:
			if block.End.After(to) {
				item.Status = "in_progress"
			}
			planned = append(planned, span{start: start, end: end})
			observed := observe(result, []span{{start: start, end: end}}, labels, limit)
			for _, thread := range CodingThreads(coding, start, end) {
				if thread.MessagesSent > 0 || thread.AgentMinutes > 0 {
					observed.Coding = append(observed.Coding, CodingWork{Device: thread.Device, Title: thread.Title, Project: thread.Project,
						MessagesSent: thread.MessagesSent, AgentMinutes: thread.AgentMinutes})
				}
			}
			item.Observed = &observed
		}
		review.Blocks = append(review.Blocks, item)
	}
	sort.Slice(planned, func(i, j int) bool { return planned[i].start.Before(planned[j].start) })
	review.Outside = observe(result, subtract(span{start: from, end: to}, sequential(planned)), labels, limit)
	return review
}

// Insight is one short fact about the day. Names lists raw app or site
// identifiers that appear in Text or Detail, so a device can show its own
// names for them.
type Insight struct {
	Key    string   `json:"key"`
	Text   string   `json:"text"`
	Detail string   `json:"detail,omitempty"`
	Names  []string `json:"names,omitempty"`
}

// Clock formats minutes as "1h 12m" or "41m".
func Clock(minutes float64) string {
	total := int(minutes + 0.5)
	if total < 60 {
		return fmt.Sprintf("%dm", total)
	}
	if total%60 == 0 {
		return fmt.Sprintf("%dh", total/60)
	}
	return fmt.Sprintf("%dh %dm", total/60, total%60)
}

func plural(count int, word string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", count, word)
}

// Insights turns one day's activity into a few plain facts. They describe
// recorded time only; missing data is reported, never read as rest or focus.
func Insights(result Result, coding []CodingThread, labels []Label) []Insight {
	insights := []Insight{}
	segments := append([]Segment(nil), result.Segments...)
	sort.Slice(segments, func(i, j int) bool { return segments[i].Start.Before(segments[j].Start) })
	labelled := false
	for _, label := range labels {
		labelled = labelled || label.Label == Distraction
	}
	type visit struct {
		minutes float64
		visits  int
	}
	distractions := map[string]*visit{}
	var total, run, longest float64
	for _, segment := range segments {
		minutes := segment.Duration().Minutes()
		if LabelFor(labels, segment) != Distraction {
			run += minutes
			longest = max(longest, run)
			continue
		}
		run = 0
		total += minutes
		name := segment.Domain
		if name == "" {
			name = segment.App
		}
		if distractions[name] == nil {
			distractions[name] = &visit{}
		}
		distractions[name].minutes += minutes
		distractions[name].visits++
	}
	switch {
	case !labelled:
		insights = append(insights, Insight{Key: "distractions", Text: "Nothing is labelled a distraction yet", Detail: "Tell the assistant what counts and it shows up here."})
	case total < 1:
		insights = append(insights, Insight{Key: "distractions", Text: "No time on distractions today"})
	default:
		top := ""
		for name, item := range distractions {
			if top == "" || item.minutes > distractions[top].minutes || (item.minutes == distractions[top].minutes && name < top) {
				top = name
			}
		}
		detail := fmt.Sprintf("Mostly %s: %s in %s.", top, Clock(distractions[top].minutes), plural(distractions[top].visits, "visit"))
		if episodes := int(total / 24); episodes >= 1 {
			detail += fmt.Sprintf(" That is %s of a 24-minute show.", plural(episodes, "episode"))
		}
		insights = append(insights, Insight{Key: "distractions", Text: Clock(total) + " on distractions today", Detail: detail, Names: []string{top}})
	}
	if labelled && longest >= 1 {
		insights = append(insights, Insight{Key: "clean_run", Text: "Longest run without a distraction: " + Clock(longest), Detail: "Counted in active time on the Mac and phone."})
	}
	var agentMinutes float64
	messages, threads := 0, 0
	for _, thread := range coding {
		if thread.MessagesSent > 0 || thread.AgentMinutes > 0 {
			agentMinutes += thread.AgentMinutes
			messages += thread.MessagesSent
			threads++
		}
	}
	if threads > 0 {
		insights = append(insights, Insight{Key: "agents", Text: "Agents worked " + Clock(agentMinutes) + " for you",
			Detail: fmt.Sprintf("You sent %s across %s.", plural(messages, "message"), plural(threads, "thread"))})
	}
	names := map[string]string{DeviceMac: "Mac", DevicePhone: "Phone"}
	for _, summary := range Summarize(result, 1, labels) {
		if summary.ActiveMinutes >= 1 && len(summary.Apps) > 0 {
			insights = append(insights, Insight{Key: "screen_" + summary.Device, Text: names[summary.Device] + ": " + Clock(summary.ActiveMinutes) + " of screen time",
				Detail: fmt.Sprintf("Top app: %s, %s.", summary.Apps[0].Name, Clock(summary.Apps[0].Minutes)), Names: []string{summary.Apps[0].Name}})
		}
		if summary.NoDataMinutes >= 30 {
			insights = append(insights, Insight{Key: "no_data_" + summary.Device, Text: names[summary.Device] + " sent nothing for " + Clock(summary.NoDataMinutes),
				Detail: "That time is missing, not empty."})
		}
	}
	return insights
}
