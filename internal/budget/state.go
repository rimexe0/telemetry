package budget

import (
	"math"
	"sort"
	"time"

	"github.com/rimexe0/telemetry/internal/policy"
)

// HabitValue is a habit of today as devices show it, with what it earns.
type HabitValue struct {
	ID    int64   `json:"id"`
	Name  string  `json:"name"`
	Done  bool    `json:"done"`
	Value float64 `json:"value"`
	// DoneAt is when the tick in force was made, while done.
	DoneAt *time.Time `json:"done_at,omitempty"`
	// DoneBy is "rule" when a habit rule ticked it, "hand" otherwise.
	DoneBy string `json:"done_by,omitempty"`
	// Unticked is set when the owner undid today's tick: their word wins,
	// so a rule does not tick it again today.
	Unticked bool `json:"unticked,omitempty"`
	// Rule is the habit's rule, and Window its window today, resolved.
	Rule   *HabitRule `json:"rule,omitempty"`
	Window *RuleSpan  `json:"window,omitempty"`
}

// Settings are the numbers a device needs to say what a door costs.
type Settings struct {
	WaitMinutes      int     `json:"wait_minutes"`
	WindowMinutes    int     `json:"window_minutes"`
	WindowUseMinutes int     `json:"window_use_minutes"`
	GraceMinutes     int     `json:"grace_minutes"`
	BorrowMultiplier float64 `json:"borrow_multiplier"`
	// RateGrayscaleOff is what keeping grayscale off adds, so a device can
	// say it before the owner decides.
	RateGrayscaleOff float64 `json:"rate_grayscale_off"`
}

// State is today's budget as /v1/device/now gives it in "budget".
type State struct {
	Enabled               bool         `json:"enabled"`
	Day                   string       `json:"day"`
	Budget                float64      `json:"budget"`
	Spent                 float64      `json:"spent"`
	Left                  float64      `json:"left"`
	Earned                float64      `json:"earned"`
	Penalty               float64      `json:"penalty"`
	Debt                  float64      `json:"debt"`
	DebtTomorrow          float64      `json:"debt_tomorrow"`
	Borrowable            float64      `json:"borrowable"`
	Rate                  Rate         `json:"rate"`
	InDistractionTime     bool         `json:"in_distraction_time"`
	NextDistractionTime   *time.Time   `json:"next_distraction_time"`
	BedtimeAt             *time.Time   `json:"bedtime_at"`
	WindDown              bool         `json:"wind_down"`
	GrayscaleDeclined     bool         `json:"grayscale_declined"`
	Door                  *DoorView    `json:"door"`
	DoorAvailable         bool         `json:"door_available"`
	DoorUnavailableReason string       `json:"door_unavailable_reason"`
	NextDoorAt            *time.Time   `json:"next_door_at"`
	Habits                []HabitValue `json:"habits"`
	Reasons               []string     `json:"reasons"`
	Settings              Settings     `json:"settings"`
}

// lastEvents is each habit's last tick or untick on day, in the order
// doneOn reads them, and which habits have any.
func lastEvents(events []HabitEvent, day string) (map[int64]HabitEvent, map[int64]bool) {
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
	last, touched := map[int64]HabitEvent{}, map[int64]bool{}
	for _, event := range ordered {
		last[event.HabitID], touched[event.HabitID] = event, true
	}
	return last, touched
}

// Exhausted reports whether neither budget nor borrowing is left, so nothing
// opens: not a door, and not an opening (with the budget on).
func (s State) Exhausted() bool { return s.Enabled && s.Left <= 0 && s.Borrowable <= 0 }

func (c *calc) state(result *Result) {
	config, now, today := c.in.Config, c.now, result.Today
	settings := config.BudgetAt(now)
	clock := func(t time.Time) string { return t.In(location(config)).Format("15:04") }
	state := State{Enabled: settings.Enabled && today.Enabled, Day: today.Day, Habits: []HabitValue{}, Reasons: append([]string{}, c.in.Reasons...),
		Settings: Settings{WaitMinutes: settings.WaitMinutes, WindowMinutes: settings.WindowMinutes, WindowUseMinutes: settings.WindowUseMinutes,
			GraceMinutes: settings.GraceMinutes, BorrowMultiplier: settings.BorrowMultiplier, RateGrayscaleOff: settings.RateGrayscaleOff}}
	if piece, ok := pieceAt(c.pieces, now); ok {
		state.Rate = piece.rate
	}
	state.GrayscaleDeclined = state.Rate.GrayscaleOff > 0
	_, state.InDistractionTime = policy.DistractionTimeEnds(config, now)
	if next, ok := policy.NextDistractionTime(config, now); ok {
		next = next.UTC()
		state.NextDistractionTime = &next
	}
	if at, ok := BedtimeStart(config, today.Day); ok {
		state.BedtimeAt = &at
	}
	state.WindDown = policy.WindingDown(config, now)
	done := map[int64]bool{}
	for _, id := range today.Done {
		done[id] = true
	}
	byID := map[int64]Habit{}
	for _, habit := range c.in.Habits {
		byID[habit.ID] = habit
	}
	last, touched := lastEvents(c.in.Events, today.Day)
	for _, id := range today.Scheduled {
		habit := byID[id]
		value := HabitValue{ID: id, Name: habit.Name, Done: done[id], Value: round(today.PerHabit)}
		if event, ok := last[id]; ok && value.Done {
			at := event.At.UTC()
			value.DoneAt, value.DoneBy = &at, "hand"
			if ByRule(event.Source) {
				value.DoneBy = "rule"
			}
		}
		value.Unticked = !value.Done && touched[id]
		if habit.Rule != nil {
			rule := *habit.Rule
			value.Rule = &rule
			if span, ok := SpanOn(config, rule, today.Day, c.in.WakeAt); ok {
				value.Window = &span
			}
		}
		state.Habits = append(state.Habits, value)
	}
	if !state.Enabled {
		state.DoorUnavailableReason = "The budget is off."
		result.State = state
		return
	}
	state.Budget, state.Spent, state.Left = round(today.Budget), round(today.Spent), round(today.Left)
	state.Earned, state.Penalty, state.Debt = round(today.Earned), round(today.Penalty), round(today.Debt)
	state.DebtTomorrow, state.Borrowable = round(today.DebtPassed), round(today.Borrowable)

	// The latest door that still bears on today.
	var latest *Door
	for _, door := range sortedDoors(c.in.Doors) {
		if door.WindowEnd.After(today.Start) || !door.RequestedAt.Before(today.Start) {
			latest = &door
		}
	}
	var view *DoorView
	if latest != nil {
		v := c.view(*latest, today)
		view = &v
		state.Door = view
		if latest.CancelledAt == nil && !latest.InDistractionTime && latest.WindowEnd.After(now) {
			// The next door comes once this one's window ends, however it ended.
			next := latest.WindowEnd
			state.NextDoorAt = &next
		}
	}
	switch {
	case view != nil && view.State == DoorWaiting:
		state.DoorUnavailableReason = "A door is already waiting. It opens at " + clock(view.OpensAt) + "."
	case view != nil && view.Active():
		state.DoorUnavailableReason = "A door is open until " + clock(view.EndsAt) + "."
	case state.NextDoorAt != nil:
		state.DoorUnavailableReason = "The next door opens at " + clock(*state.NextDoorAt) + "."
	case state.InDistractionTime && today.Left > 0:
		state.DoorUnavailableReason = "Distraction time is open now; no door is needed."
	case today.Left <= 0 && today.Borrowable <= 0:
		state.DoorUnavailableReason = "Nothing is left to spend or borrow today."
	default:
		state.DoorAvailable = true
	}
	result.State = state

	if view != nil && view.Active() {
		result.Door = &policy.DoorFact{ID: view.ID, OpensAt: view.OpensAt, WindowEnd: view.WindowEnd, EndsAt: view.EndsAt}
	}
	// The distraction time's sliding end: today's budget, less what an
	// open window may still cost, spent from now on at the rate in force,
	// only while distractions are open in a distraction time.
	available := today.Left
	if result.Door != nil && !latest.InDistractionTime {
		available = math.Max(0, available-charge(c.pieces, later(now, view.OpensAt), view.EndsAt))
	}
	result.Budget = &policy.BudgetFact{Used: today.Left <= 0, DayEnd: today.End,
		EndsAt: runOut(c.pieces, now, today.End, available, 0, 1, distractionRate)}
}
