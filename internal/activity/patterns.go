package activity

import (
	"sort"
	"time"
)

// Visit is one stretch of the owner's attention on a task, with what they
// went to when it ended and when they next came back to the task.
type Visit struct {
	Start time.Time
	End   time.Time
	// LeftTo is what was on screen right after: an app or site, with its
	// project for an agent app. Empty when they left the devices.
	LeftTo string
	// LeftKind is "distraction", "task" (another task), "other" or "away".
	LeftKind string
	// Back is when they next returned to the task that day, if they did.
	Back *time.Time
}

// TaskVisits splits a day into visits per task. It reads the same
// classification as BuildDay, so a visit is time the rules, the task's
// memory or the owner's own word put on the task.
func TaskVisits(input DayInput) map[int64][]Visit {
	c := newClassifier(input)
	segments := c.splitAtClaims(input.Result.Segments)
	sort.SliceStable(segments, func(i, j int) bool { return segments[i].Start.Before(segments[j].Start) })
	kinds := make([]string, len(segments))
	tasks := make([]int64, len(segments))
	for i, segment := range segments {
		kinds[i], tasks[i] = c.classify(segment)
	}
	visits := map[int64][]Visit{}
	for i, segment := range segments {
		if kinds[i] != KindTask {
			continue
		}
		list := visits[tasks[i]]
		if n := len(list); n > 0 && segment.Start.Sub(list[n-1].End) <= returnGap {
			list[n-1].End = later(list[n-1].End, segment.End)
		} else {
			list = append(list, Visit{Start: segment.Start, End: segment.End})
		}
		visits[tasks[i]] = list
	}
	for task, list := range visits {
		for i := range list {
			list[i].LeftKind = "away"
			if i+1 < len(list) {
				back := list[i+1].Start
				list[i].Back = &back
			}
			// What came right after the visit ended, on any device.
			for j, segment := range segments {
				if segment.Start.Before(list[i].End) || (kinds[j] == KindTask && tasks[j] == task) {
					continue
				}
				if segment.Start.Sub(list[i].End) <= returnGap {
					list[i].LeftTo, list[i].LeftKind = c.learnContext(segment), kinds[j]
				}
				break
			}
		}
		visits[task] = list
	}
	return visits
}

// Median returns the middle value, or 0 for none.
func Median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	if len(sorted)%2 == 1 {
		return sorted[len(sorted)/2]
	}
	return (sorted[len(sorted)/2-1] + sorted[len(sorted)/2]) / 2
}
