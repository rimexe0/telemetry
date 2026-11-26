package budget

import (
	"math"
	"time"

	"github.com/rimexe0/telemetry/internal/policy"
)

// BuildHeatmap gives each habit's days from first through today: done,
// missed (only once the day closed), not scheduled, or before the habit
// existed. Today shows only once done, since it is not settled before
// bedtime. A run counts days done in a row; days the habit does not apply,
// and today while open, neither add to it nor break it.
func BuildHeatmap(config policy.Config, habits []Habit, events []HabitEvent, first string, now time.Time) Heatmap {
	today := DayOf(config, now)
	result := Heatmap{Habits: []HabitHeatmap{}, Combined: map[string]float64{}}
	type dayInfo struct {
		closed    bool
		done      map[int64]bool
		scheduled map[int64]bool
	}
	days := []string{}
	info := map[string]dayInfo{}
	for day := first; day <= today; day = AddDays(day, 1) {
		start, end := DayStart(config, day), DayEnd(config, day)
		done := doneOn(events, day)
		list := scheduled(habits, day, start, end, done)
		in := map[int64]bool{}
		for _, habit := range list {
			in[habit.ID] = true
		}
		info[day] = dayInfo{!now.Before(CloseAt(config, day)), done, in}
		days = append(days, day)
		doneCount := 0
		for id := range in {
			if done[id] {
				doneCount++
			}
		}
		if len(in) > 0 && (info[day].closed || doneCount > 0) {
			result.Combined[day] = math.Round(float64(doneCount)/float64(len(in))*100) / 100
		}
	}
	for _, habit := range habits {
		row := HabitHeatmap{ID: habit.ID, Name: habit.Name, CreatedDay: DayOf(config, habit.CreatedAt), Days: map[string]string{}}
		if habit.ArchivedAt != nil {
			archived := DayOf(config, *habit.ArchivedAt)
			row.ArchivedDay = &archived
		}
		run := 0
		for _, key := range days {
			day := info[key]
			cell := ""
			switch {
			case key < row.CreatedDay:
				cell = CellNone
			case row.ArchivedDay != nil && key > *row.ArchivedDay:
				// After archiving the habit has no days; its history stays.
			case day.scheduled[habit.ID] && day.done[habit.ID]:
				cell = CellDone
				run++
				row.LongestRun = max(row.LongestRun, run)
			case day.scheduled[habit.ID] && day.closed:
				cell = CellMissed
				run = 0
			case day.scheduled[habit.ID]:
				// Today, still open.
			default:
				cell = CellUnscheduled
			}
			if cell != "" {
				row.Days[key] = cell
			}
		}
		row.CurrentRun = run
		result.Habits = append(result.Habits, row)
	}
	return result
}
