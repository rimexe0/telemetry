package activity

import "time"

// Cell letters for DayCells.
const (
	CellDistraction = 'd'
	CellActive      = 'a'
	CellNoData      = 'n'
	CellIdle        = '.'
	// CellLater is a part of today that has not come yet.
	CellLater = '_'
)

// DayCells draws a local day [dayStart, dayEnd) as count equal parts, one
// letter each, so weeks and months of days read at a glance: "d" mostly on
// distractions (the owner's labels), "a" mostly in use otherwise, "n" mostly
// with no records from any device (unknown, not idle), "." none of them for
// most of it, "_" not yet come (after result's range ends). A part shows the
// kind that covers most of it, when that is at least a third of it.
func DayCells(result Result, labels []Label, dayStart, dayEnd, to time.Time, count int) string {
	if count <= 0 || !dayEnd.After(dayStart) {
		return ""
	}
	var distracted, used []span
	for _, segment := range result.Segments {
		part := span{start: segment.Start, end: segment.End}
		if LabelFor(labels, segment) == Distraction {
			distracted = append(distracted, part)
		} else {
			used = append(used, part)
		}
	}
	distracted = sequential(distracted)
	active := []span{}
	for _, part := range sequential(used) {
		active = append(active, subtract(part, distracted)...)
	}
	// No data means no device sent anything: each device's gaps, overlapped.
	byDevice := map[string][]span{}
	for _, gap := range result.Gaps {
		byDevice[gap.Device] = append(byDevice[gap.Device], span{start: gap.Start, end: gap.End})
	}
	var missing []span
	first := true
	for _, device := range []string{DeviceMac, DevicePhone} {
		gaps := sequential(byDevice[device])
		if first {
			missing, first = gaps, false
			continue
		}
		overlapped := []span{}
		for _, part := range missing {
			overlapped = append(overlapped, intersect(part, gaps)...)
		}
		missing = overlapped
	}
	step := dayEnd.Sub(dayStart) / time.Duration(count)
	cells := make([]byte, count)
	for i := range cells {
		start := dayStart.Add(time.Duration(i) * step)
		end := start.Add(step)
		if i == count-1 {
			end = dayEnd
		}
		if !start.Before(to) {
			cells[i] = CellLater
			continue
		}
		window := span{start: start, end: end}
		length := end.Sub(start).Minutes()
		cells[i] = CellIdle
		threshold, best := length/3, 0.0
		for _, kind := range []struct {
			letter byte
			cover  []span
		}{{CellDistraction, distracted}, {CellActive, active}, {CellNoData, missing}} {
			if minutes := minutesIn(window, kind.cover); minutes >= threshold && minutes > best {
				cells[i], best = kind.letter, minutes
			}
		}
	}
	return string(cells)
}

// DistractionMinutes is the time on things the owner labelled distraction,
// on every device, as BuildDay counts it.
func DistractionMinutes(result Result, labels []Label) float64 {
	var minutes float64
	for _, segment := range result.Segments {
		if LabelFor(labels, segment) == Distraction {
			minutes += segment.Duration().Minutes()
		}
	}
	return round(minutes)
}

// AgentMinutes is the time coding agents ran in [from, to), over the
// threads that had any, as the Apps view's "Agents worked" line counts it.
func AgentMinutes(coding []Record, from, to time.Time) float64 {
	var minutes float64
	for _, thread := range CodingThreads(coding, from, to) {
		if thread.MessagesSent > 0 || thread.AgentMinutes > 0 {
			minutes += thread.AgentMinutes
		}
	}
	return round(minutes)
}
