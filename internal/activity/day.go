package activity

import (
	"sort"
	"time"
)

const (
	KindDistraction = "distraction"
	KindOther       = "other"

	// A pause this long ends a chunk; a run of the other kind shorter than
	// chunkMinRun stays inside the chunk around it.
	chunkPause  = 5 * time.Minute
	chunkMinRun = 2 * time.Minute
)

// DayItem totals one app or site inside a chunk.
type DayItem struct {
	Name    string  `json:"name"`
	Label   string  `json:"label"`
	Minutes float64 `json:"minutes"`
	Visits  int     `json:"visits"`
}

// DaySegment is one uninterrupted use of an app or site, as recorded.
type DaySegment struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Name  string    `json:"name"`
	Title string    `json:"title,omitempty"`
	Label string    `json:"label"`
}

// Chunk is a stretch of continuous use of one device, summarised. Its items
// total everything in it and its segments are the records behind them, so a
// chunk hides detail without dropping any.
type Chunk struct {
	Device string    `json:"device"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	// Kind is "distraction" when most of the chunk was on things the owner
	// labelled so, otherwise "other".
	Kind               string       `json:"kind"`
	ActiveMinutes      float64      `json:"active_minutes"`
	DistractionMinutes float64      `json:"distraction_minutes"`
	Items              []DayItem    `json:"items"`
	Segments           []DaySegment `json:"segments,omitempty"`
}

// DayBlock is a planned block with the time recorded inside it.
type DayBlock struct {
	PlannedBlock
	Status string `json:"status"`
	// InPlanMinutes is time active on any device during the block while no
	// device showed a distraction. It does not show that the time went to the
	// block's task.
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

func kindOf(labels []Label, segment Segment) string {
	if LabelFor(labels, segment) == Distraction {
		return KindDistraction
	}
	return KindOther
}

// chunks compresses one device's ordered segments.
func chunks(device string, segments []Segment, labels []Label, withSegments bool) []Chunk {
	// Runs: consecutive segments of one kind with no long pause.
	type run struct {
		kind     string
		segments []Segment
		active   time.Duration
	}
	var sessions [][]run
	for _, segment := range segments {
		kind := kindOf(labels, segment)
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
			result = append(result, chunkOf(device, current.segments, labels, withSegments))
		}
	}
	return result
}

func lastEnd(segments []Segment) time.Time { return segments[len(segments)-1].End }

func chunkOf(device string, segments []Segment, labels []Label, withSegments bool) Chunk {
	chunk := Chunk{Device: device, Start: segments[0].Start, End: lastEnd(segments), Kind: KindOther, Items: []DayItem{}}
	items := map[string]*DayItem{}
	for _, segment := range segments {
		minutes := segment.Duration().Minutes()
		name, label := segmentName(segment), LabelFor(labels, segment)
		chunk.ActiveMinutes += minutes
		if label == Distraction {
			chunk.DistractionMinutes += minutes
		}
		if items[name] == nil {
			items[name] = &DayItem{Name: name, Label: label}
		}
		items[name].Minutes += minutes
		items[name].Visits++
		if withSegments {
			chunk.Segments = append(chunk.Segments, DaySegment{Start: segment.Start, End: segment.End, Name: name, Title: segment.Title, Label: label})
		}
	}
	if chunk.DistractionMinutes*2 > chunk.ActiveMinutes {
		chunk.Kind = KindDistraction
	}
	for _, item := range items {
		item.Minutes = round(item.Minutes)
		chunk.Items = append(chunk.Items, *item)
	}
	sort.Slice(chunk.Items, func(i, j int) bool {
		if chunk.Items[i].Minutes != chunk.Items[j].Minutes {
			return chunk.Items[i].Minutes > chunk.Items[j].Minutes
		}
		return chunk.Items[i].Name < chunk.Items[j].Name
	})
	chunk.ActiveMinutes, chunk.DistractionMinutes = round(chunk.ActiveMinutes), round(chunk.DistractionMinutes)
	return chunk
}

// BuildDay sets the plan next to a compressed timeline of [from, to).
func BuildDay(blocks []PlannedBlock, result Result, labels []Label, from, to time.Time, withSegments bool) Day {
	day := Day{Blocks: []DayBlock{}, Distraction: []DeviceMinutes{}, Chunks: []Chunk{}, NoData: result.Gaps}
	if day.NoData == nil {
		day.NoData = []Gap{}
	}
	// Time not on a distraction, on any device, counted once.
	var clean, distracted []span
	for _, device := range []string{DeviceMac, DevicePhone} {
		var own []Segment
		var minutes float64
		for _, segment := range result.Segments {
			if segment.Device != device {
				continue
			}
			own = append(own, segment)
			if kindOf(labels, segment) == KindDistraction {
				minutes += segment.Duration().Minutes()
				distracted = append(distracted, span{start: segment.Start, end: segment.End})
			} else {
				clean = append(clean, span{start: segment.Start, end: segment.End})
			}
		}
		sort.SliceStable(own, func(i, j int) bool { return own[i].Start.Before(own[j].Start) })
		day.Chunks = append(day.Chunks, chunks(device, own, labels, withSegments)...)
		day.Distraction = append(day.Distraction, DeviceMinutes{Device: device, Minutes: round(minutes)})
		day.DistractionMinutes += minutes
	}
	sort.SliceStable(day.Chunks, func(i, j int) bool { return day.Chunks[i].Start.Before(day.Chunks[j].Start) })
	// One person uses both devices: while either shows a distraction, time on
	// the other does not count as in plan.
	distracted = sequential(distracted)
	undistracted := []span{}
	for _, part := range sequential(clean) {
		undistracted = append(undistracted, subtract(part, distracted)...)
	}
	clean = undistracted
	total := func(window span, cover []span) float64 {
		var minutes float64
		for _, part := range overlaps(window, cover) {
			minutes += part.end.Sub(part.start).Minutes()
		}
		return minutes
	}
	for _, block := range blocks {
		item := DayBlock{PlannedBlock: block, Status: "past"}
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
			day.InPlanMinutes += item.InPlanMinutes
		}
		day.Blocks = append(day.Blocks, item)
	}
	day.PlannedMinutes, day.InPlanMinutes, day.DistractionMinutes = round(day.PlannedMinutes), round(day.InPlanMinutes), round(day.DistractionMinutes)
	return day
}
