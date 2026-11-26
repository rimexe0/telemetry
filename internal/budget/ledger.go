package budget

import (
	"math"
	"sort"
	"time"

	"github.com/rimexe0/telemetry/internal/activity"
	"github.com/rimexe0/telemetry/internal/policy"
)

// Inputs are the records a ledger is worked out from.
type Inputs struct {
	// Config is the policy in force, the owner's labels merged in.
	Config    policy.Config
	Habits    []Habit
	Events    []HabitEvent
	Doors     []Door
	Grayscale []GrayscaleEvent
	Segments  []activity.Segment
	Reasons   []string
	// Open are the other stretches that opened distractions: openings
	// from the panel or the assistant, and emergency unlocks from older
	// phone apps. Use during them is charged like use during a door.
	Open []OpenSpan
	// DebtIn is what the first day owes from the day before it: that day's
	// snapshot's debt passed on, or nothing without one.
	DebtIn float64
}

// DayLedger is one budget day's arithmetic.
type DayLedger struct {
	Day   string    `json:"day"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	// Enabled is false for a day the budget was off; it charges nothing.
	Enabled  bool          `json:"enabled"`
	Settings policy.Budget `json:"settings"`
	// PerHabit is what each scheduled habit is worth that day.
	PerHabit  float64 `json:"per_habit"`
	Scheduled []int64 `json:"scheduled"`
	Done      []int64 `json:"done"`
	// Missed is only known once the day's habits close.
	Missed  []int64 `json:"missed"`
	Closed  bool    `json:"closed"`
	Earned  float64 `json:"earned"`
	Penalty float64 `json:"penalty"`
	Debt    float64 `json:"debt"`
	Budget  float64 `json:"budget"`
	Spent   float64 `json:"spent"`
	Left    float64 `json:"left"`
	// DebtPassed is what this day borrowed from the next, multiplied.
	DebtPassed float64 `json:"debt_passed"`
	// Borrowable is what may still be borrowed from the next day.
	Borrowable float64 `json:"borrowable"`
	// ExhaustedAt is when nothing was left to spend or borrow.
	ExhaustedAt *time.Time `json:"exhausted_at,omitempty"`
	// UseMinutes is the distraction use measured, before rates.
	UseMinutes float64 `json:"use_minutes"`
}

// calc holds what every part of one computation shares.
type calc struct {
	in    Inputs
	now   time.Time
	since time.Time
	spans []span
	// doored is the part of spans that a door, an opening or an emergency
	// opened: only that part may borrow from the next day.
	doored []span
	pieces []ratePiece
}

func newCalc(in Inputs, from, now time.Time) *calc {
	since := time.Time{}
	if in.Config.BudgetSince != nil {
		since = *in.Config.BudgetSince
	}
	surcharges := declines(in.Config, in.Grayscale)
	// Only use while distractions were open is charged: in a distraction
	// time, a door, an opening or an emergency. A closed site that stays
	// usable with its feeds hidden, or an app shown its block screen, costs
	// nothing.
	used := uses(in.Config, in.Segments)
	doors := doorSpans(in.Doors, in.Open)
	open := doors
	for _, window := range policy.DistractionWindows(in.Config, from.Add(-24*time.Hour), now.Add(36*time.Hour)) {
		open = append(open, span{window[0], window[1]})
	}
	// Rates run past now so sliding ends can look ahead.
	return &calc{in: in, now: now, since: since, spans: within(used, open), doored: within(used, doors),
		pieces: rates(in.Config, surcharges, from, now.Add(36*time.Hour))}
}

// settingsFor is the budget in force for a day: as of now for today, as the
// day ended for a past day.
func (c *calc) settingsFor(start, end time.Time) policy.Budget {
	at := c.now
	if !at.Before(end) {
		at = end.Add(-time.Second)
	}
	if at.Before(start) {
		at = start
	}
	return c.in.Config.BudgetAt(at)
}

// habitsOf fills a day's habits: what is scheduled, done and, once closed,
// missed, and what each is worth.
func (c *calc) habitsOf(day *DayLedger) {
	done := doneOn(c.in.Events, day.Day)
	list := scheduled(c.in.Habits, day.Day, day.Start, day.End, done)
	day.Scheduled, day.Done, day.Missed = []int64{}, []int64{}, []int64{}
	for _, habit := range list {
		day.Scheduled = append(day.Scheduled, habit.ID)
		if done[habit.ID] {
			day.Done = append(day.Done, habit.ID)
		}
	}
	day.Closed = !c.now.Before(CloseAt(c.in.Config, day.Day))
	if day.Closed {
		for _, habit := range list {
			if !done[habit.ID] {
				day.Missed = append(day.Missed, habit.ID)
			}
		}
	}
	settings := day.Settings
	if !settings.Enabled {
		return
	}
	count := max(len(list), settings.HabitsForMax)
	day.PerHabit = float64(settings.MaxMinutes-settings.BaseMinutes) / float64(count)
}

// days works out each budget day from first through the day of now, in
// order, each owing what the one before borrowed.
func (c *calc) days(first string) []DayLedger {
	config := c.in.Config
	today := DayOf(config, c.now)
	previous := DayLedger{Day: AddDays(first, -1), Start: DayStart(config, AddDays(first, -1)), End: DayStart(config, first)}
	previous.Settings = c.settingsFor(previous.Start, previous.End)
	previous.Enabled = previous.Settings.Enabled && previous.End.After(c.since)
	c.habitsOf(&previous)
	previous.DebtPassed = c.in.DebtIn
	result := []DayLedger{}
	for day := first; day <= today; day = AddDays(day, 1) {
		ledger := DayLedger{Day: day, Start: DayStart(config, day), End: DayEnd(config, day)}
		ledger.Settings = c.settingsFor(ledger.Start, ledger.End)
		ledger.Enabled = ledger.Settings.Enabled && ledger.End.After(c.since)
		c.habitsOf(&ledger)
		if ledger.Enabled {
			c.charge(&ledger, previous)
		}
		result = append(result, ledger)
		previous = ledger
	}
	return result
}

// charge works out a day's budget and walks its use in time order: charged
// to the day at the rate in force until nothing is left, then to the next
// day at the rate times the multiplier.
func (c *calc) charge(day *DayLedger, previous DayLedger) {
	settings := day.Settings
	day.Earned = float64(len(day.Done)) * day.PerHabit
	// A penalty only for a day the budget held from its start, so switching
	// it on mid-day costs nothing.
	if previous.Enabled && previous.Closed && !previous.Start.Before(c.since) {
		day.Penalty = float64(len(previous.Missed)) * previous.PerHabit
	}
	if previous.Enabled {
		day.Debt = previous.DebtPassed
	}
	floor := float64(settings.FloorMinutes)
	day.Budget = math.Max(floor, math.Min(float64(settings.BaseMinutes)+day.Earned, float64(settings.MaxMinutes))-day.Penalty-day.Debt)
	day.Left = day.Budget
	borrowable := math.Max(0, float64(settings.BaseMinutes-settings.FloorMinutes))
	from, to := later(day.Start, c.since), earlier(day.End, c.now)
	for _, s := range c.spans {
		start, end := later(s.start, from), earlier(s.end, to)
		if !end.After(start) {
			continue
		}
		day.UseMinutes += minutes(end.Sub(start))
		for _, piece := range c.pieces {
			a, b := later(piece.from, start), earlier(piece.to, end)
			if !b.After(a) || piece.rate.Total <= 0 {
				continue
			}
			length := minutes(b.Sub(a))
			cost := length * piece.rate.Total
			if day.Left >= cost {
				day.Left -= cost
				day.Spent += cost
				continue
			}
			// The day runs out inside this stretch. The rest is borrowed, but
			// only where a door or opening opened it: a used-up distraction
			// time closes, so what shows after is not borrowing.
			used := day.Left / piece.rate.Total
			day.Spent += day.Left
			day.Left = 0
			over := a.Add(time.Duration(used * float64(time.Minute)))
			borrowed := useMinutes(c.doored, over, b) * piece.rate.Total * settings.BorrowMultiplier
			before := day.DebtPassed
			day.DebtPassed += borrowed
			if day.ExhaustedAt == nil && day.DebtPassed >= borrowable {
				// Nothing more could be borrowed from here on; what was used
				// after is still charged, and the next day's floor holds.
				into := 0.0
				if borrowed > 0 {
					into = (borrowable - before) / (piece.rate.Total * settings.BorrowMultiplier)
				}
				at := a.Add(time.Duration((used + math.Max(0, into)) * float64(time.Minute)))
				day.ExhaustedAt = &at
			}
		}
	}
	day.Borrowable = math.Max(0, borrowable-day.DebtPassed)
	if day.ExhaustedAt == nil && day.Left <= 0 && day.Borrowable <= 0 {
		at := later(day.Start, c.since)
		day.ExhaustedAt = &at
	}
}

// Compute works out the days from first through today and today's state.
func Compute(in Inputs, first string, now time.Time) Result {
	now = now.UTC()
	from := DayStart(in.Config, AddDays(first, -1))
	c := newCalc(in, from, now)
	days := c.days(first)
	result := Result{Days: days, Today: days[len(days)-1]}
	c.state(&result)
	return result
}

// Result is a computation's outcome.
type Result struct {
	Days  []DayLedger
	Today DayLedger
	State State
	// Door and Budget are what the policy needs; nil when the budget is off.
	Door   *policy.DoorFact
	Budget *policy.BudgetFact
}

func round(value float64) float64 { return math.Round(value*10) / 10 }

func sortedDoors(doors []Door) []Door {
	result := append([]Door{}, doors...)
	sort.SliceStable(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
