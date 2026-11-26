// Package budget works out the owner's distraction budget (owner decision
// 2026-11-26, budget-and-habits.md): what each day has, earned with the
// owner's own habits, what distraction use has cost at the rate in force,
// what was borrowed from the next day, and when a door or a distraction time
// runs out. It reads only the records it is given, has no storage, and
// never charges missing data.
package budget

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	ActionTick   = "tick"
	ActionUntick = "untick"
)

// Weekdays are the days a habit applies, 0 Sunday to 6 Saturday; none means
// every day. Input also takes 7 for Sunday (ISO) and day names, since the
// devices and the panel count weeks differently.
type Weekdays []int

func (w *Weekdays) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return errors.New("weekdays must be a list")
	}
	names := map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}
	days := Weekdays{}
	for _, item := range raw {
		var number int
		if json.Unmarshal(item, &number) == nil {
			if number < 0 || number > 7 {
				return fmt.Errorf("weekday %d must be 0 (Sunday) to 6 (Saturday)", number)
			}
			days = append(days, number%7)
			continue
		}
		var name string
		if json.Unmarshal(item, &name) != nil {
			return errors.New("each weekday is a number 0 to 6 or a day name")
		}
		key := strings.ToLower(strings.TrimSpace(name))
		if len(key) >= 3 {
			key = key[:3]
		}
		day, ok := names[key]
		if !ok {
			return fmt.Errorf("weekday %q is not a day name", name)
		}
		days = append(days, day)
	}
	*w = days.normalized()
	return nil
}

func (w Weekdays) MarshalJSON() ([]byte, error) {
	return json.Marshal([]int(append(Weekdays{}, w...)))
}

func (w Weekdays) normalized() Weekdays {
	seen := [7]bool{}
	for _, day := range w {
		if day >= 0 && day <= 6 {
			seen[day] = true
		}
	}
	result := Weekdays{}
	for day, in := range seen {
		if in {
			result = append(result, day)
		}
	}
	// All seven is every day, said once.
	if len(result) == 7 {
		return Weekdays{}
	}
	return result
}

// Has reports whether the habit applies on a weekday.
func (w Weekdays) Has(day time.Weekday) bool {
	if len(w) == 0 {
		return true
	}
	for _, value := range w {
		if value == int(day) {
			return true
		}
	}
	return false
}

// Habit is one of the owner's habits. They are the owner's own, made in the
// panel or with their yes in a conversation; nothing is seeded.
type Habit struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Position   int        `json:"position"`
	Weekdays   Weekdays   `json:"weekdays"`
	CreatedAt  time.Time  `json:"created_at"`
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
}

// ValidateName trims a habit's name and refuses an empty or long one.
func ValidateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 100 {
		return "", errors.New("a habit name is 1 to 100 characters")
	}
	return name, nil
}

// HabitEvent is one tick or untick, kept for good.
type HabitEvent struct {
	ID      int64  `json:"id"`
	HabitID int64  `json:"habit_id"`
	Day     string `json:"day"`
	Action  string `json:"action"`
	// At is when the tick was made; ReceivedAt when the server got it. A
	// tick made on a device while the server could not be reached arrives
	// later and still counts for its own day.
	At         time.Time `json:"at"`
	ReceivedAt time.Time `json:"received_at"`
	// Source is "owner" (the panel or the assistant) or "device:<id>".
	Source string `json:"source,omitempty"`
}

// doneOn reports, per habit, whether its last event on day was a tick.
func doneOn(events []HabitEvent, day string) map[int64]bool {
	ordered := []HabitEvent{}
	for _, event := range events {
		if event.Day == day {
			ordered = append(ordered, event)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if !ordered[i].At.Equal(ordered[j].At) {
			return ordered[i].At.Before(ordered[j].At)
		}
		return ordered[i].ID < ordered[j].ID
	})
	done := map[int64]bool{}
	for _, event := range ordered {
		done[event.HabitID] = event.Action == ActionTick
	}
	return done
}

// scheduled lists the habits that apply on a budget day, in the owner's
// order. A habit counts from the day it was made. An archived habit keeps
// the days before its archiving; on the day it was archived it counts only
// if it was done, so archiving never makes a miss.
func scheduled(habits []Habit, day string, start, end time.Time, done map[int64]bool) []Habit {
	weekday := dayDate(day).Weekday()
	result := []Habit{}
	for _, habit := range habits {
		if !habit.CreatedAt.Before(end) || !habit.Weekdays.Has(weekday) {
			continue
		}
		if habit.ArchivedAt != nil && habit.ArchivedAt.Before(end) && (habit.ArchivedAt.Before(start) || !done[habit.ID]) {
			continue
		}
		result = append(result, habit)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Position != result[j].Position {
			return result[i].Position < result[j].Position
		}
		return result[i].ID < result[j].ID
	})
	return result
}

// Heatmap cells.
const (
	CellDone        = "done"
	CellMissed      = "missed"
	CellUnscheduled = "unscheduled"
	CellNone        = "none"
)

// HabitHeatmap is one habit's row: a cell per closed day (and today once
// done), and its runs. Days it does not apply never break a run, and no run
// is ever called broken.
type HabitHeatmap struct {
	ID          int64             `json:"id"`
	Name        string            `json:"name"`
	CreatedDay  string            `json:"created_day"`
	ArchivedDay *string           `json:"archived_day"`
	Days        map[string]string `json:"days"`
	CurrentRun  int               `json:"current_run"`
	LongestRun  int               `json:"longest_run"`
}

type Heatmap struct {
	Habits []HabitHeatmap `json:"habits"`
	// Combined is the share of the day's habits done, 0 to 1.
	Combined map[string]float64 `json:"combined"`
}

// AppliesAt reports whether a habit applied on a budget day and existed at
// at, a moment in that day: made by then, not archived by then, and on the
// day's weekday. A tick that reaches the server late is checked with it.
func (h Habit) AppliesAt(day string, at time.Time) bool {
	return !h.CreatedAt.After(at) && (h.ArchivedAt == nil || h.ArchivedAt.After(at)) && h.Weekdays.Has(dayDate(day).Weekday())
}
