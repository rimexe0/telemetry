package policy

import (
	"fmt"
	"time"
)

// The distraction budget and the door (owner decision 2026-11-26, see
// budget-and-habits.md). On 2026-11-25 the owner lost a free day to fighting
// the blocker: eight free emergency unlocks, then the Mac app deleted. The
// budget makes distraction time something earned with habits and spent at a
// rate, and the door is a way out that costs something instead of a fight.
const (
	// ModeDoor opens distractions after the owner went through the door:
	// they saw the cost, waited, and the use is charged to the budget. It
	// overrides holds as the emergency unlock did, bedtime included.
	ModeDoor = "door"
	// ModeBudgetUsed closes a distraction time once the day's budget is
	// spent. Opening again means borrowing, which goes through the door.
	ModeBudgetUsed = "budget_used"
)

// Budget is the owner's distraction budget. Every number is the owner's;
// nothing is seeded, and a disabled budget keeps its numbers so it can be
// switched back on as it was.
type Budget struct {
	Enabled bool `json:"enabled"`
	// BaseMinutes is the day's budget before habits; MaxMinutes the most a
	// day can have; FloorMinutes the least, where penalties and debt stop.
	BaseMinutes  int `json:"base_minutes"`
	MaxMinutes   int `json:"max_minutes"`
	FloorMinutes int `json:"floor_minutes"`
	// HabitsForMax is how many habits reach the full bonus.
	HabitsForMax int `json:"habits_for_max"`
	// The rate a minute of use costs: in distraction times, otherwise in the
	// day, and during bedtime; RateGrayscaleOff is added while grayscale is
	// declined.
	RateDistraction  float64 `json:"rate_distraction"`
	RateDay          float64 `json:"rate_day"`
	RateBedtime      float64 `json:"rate_bedtime"`
	RateGrayscaleOff float64 `json:"rate_grayscale_off"`
	// BorrowMultiplier is what a budget minute taken from tomorrow costs
	// tomorrow.
	BorrowMultiplier float64 `json:"borrow_multiplier"`
	// A door outside distraction times opens a window of WindowMinutes from
	// when it opens, with WindowUseMinutes of use and GraceMinutes more to
	// close or save something. It opens WaitMinutes after "Still want it".
	WindowMinutes    int `json:"window_minutes"`
	WindowUseMinutes int `json:"window_use_minutes"`
	GraceMinutes     int `json:"grace_minutes"`
	WaitMinutes      int `json:"wait_minutes"`
}

// Normalize validates the budget. A disabled budget is not checked.
func (b Budget) Normalize() error {
	if !b.Enabled {
		return nil
	}
	rate := func(value float64) bool { return value >= 0.1 && value <= 100 }
	switch {
	case b.FloorMinutes < 0 || b.FloorMinutes > b.BaseMinutes || b.BaseMinutes > b.MaxMinutes || b.MaxMinutes > 1440:
		return fmt.Errorf("%w: budget needs 0 <= floor_minutes <= base_minutes <= max_minutes <= 1440", ErrInvalidConfig)
	case b.HabitsForMax < 1 || b.HabitsForMax > 50:
		return fmt.Errorf("%w: budget.habits_for_max must be between 1 and 50", ErrInvalidConfig)
	case !rate(b.RateDistraction) || !rate(b.RateDay) || !rate(b.RateBedtime):
		return fmt.Errorf("%w: budget rates must be between 0.1 and 100", ErrInvalidConfig)
	case b.RateGrayscaleOff < 0 || b.RateGrayscaleOff > 100:
		return fmt.Errorf("%w: budget.rate_grayscale_off must be between 0 and 100", ErrInvalidConfig)
	case b.BorrowMultiplier < 1 || b.BorrowMultiplier > 20:
		return fmt.Errorf("%w: budget.borrow_multiplier must be between 1 and 20", ErrInvalidConfig)
	case b.WindowMinutes < 1 || b.WindowMinutes > 1440:
		return fmt.Errorf("%w: budget.window_minutes must be between 1 and 1440", ErrInvalidConfig)
	case b.WindowUseMinutes < 1 || b.WindowUseMinutes > b.WindowMinutes:
		return fmt.Errorf("%w: budget.window_use_minutes must be between 1 and window_minutes", ErrInvalidConfig)
	case b.GraceMinutes < 0 || b.GraceMinutes > 60:
		return fmt.Errorf("%w: budget.grace_minutes must be between 0 and 60", ErrInvalidConfig)
	case b.WaitMinutes < 0 || b.WaitMinutes > 240:
		return fmt.Errorf("%w: budget.wait_minutes must be between 0 and 240", ErrInvalidConfig)
	}
	return nil
}

// Looser reports whether next asks less of the owner in any way: off, a
// higher base, maximum or floor, fewer habits for the maximum, a lower rate,
// multiplier or wait, a longer window allowance or grace, or a shorter
// window (the next door comes sooner). It then waits for the next day.
func (b Budget) Looser(next Budget) bool {
	if !b.Enabled {
		return false
	}
	if !next.Enabled {
		return true
	}
	return next.BaseMinutes > b.BaseMinutes || next.MaxMinutes > b.MaxMinutes || next.FloorMinutes > b.FloorMinutes ||
		next.HabitsForMax < b.HabitsForMax ||
		next.RateDistraction < b.RateDistraction || next.RateDay < b.RateDay || next.RateBedtime < b.RateBedtime ||
		next.RateGrayscaleOff < b.RateGrayscaleOff || next.BorrowMultiplier < b.BorrowMultiplier || next.WaitMinutes < b.WaitMinutes ||
		next.WindowUseMinutes > b.WindowUseMinutes || next.GraceMinutes > b.GraceMinutes || next.WindowMinutes < b.WindowMinutes
}

// BudgetAt is the budget in force at an instant.
func (c Config) BudgetAt(at time.Time) Budget {
	return ruleAt(c.Timezone, c.Budget, c.BudgetNext, c.BudgetNextFrom, at)
}

// WithBudget sets the budget as of now; a looser one waits for the next day
// (see NextDay). Switching it on records when, so days before it are never
// charged and bring no debt or penalty.
func (c Config) WithBudget(next Budget, now time.Time) Config {
	wasOn := c.BudgetAt(now).Enabled
	c.Budget, c.BudgetNext, c.BudgetNextFrom = withRule(c.Timezone, c.Budget, c.BudgetNext, c.BudgetNextFrom, next, Budget.Looser, now, c.NextDay(now))
	if !wasOn && c.BudgetAt(now).Enabled {
		since := now.UTC()
		c.BudgetSince = &since
	}
	return c
}

// DoorFact is a door the owner went through, reduced to what the decision
// needs. Only a door waiting or open is given.
type DoorFact struct {
	ID int64 `json:"id"`
	// OpensAt is after the owner's wait.
	OpensAt time.Time `json:"opens_at"`
	// WindowEnd is the most the door lasts; EndsAt is its sliding end, the
	// earliest of the window end and when the window's use or the budget
	// would run out with use from now on.
	WindowEnd time.Time `json:"window_end"`
	EndsAt    time.Time `json:"ends_at"`
}

// BudgetFact is the state of today's budget a decision needs.
type BudgetFact struct {
	// Used is true when nothing of today's budget is left.
	Used bool `json:"used,omitempty"`
	// EndsAt is when today's budget would run out with use from now on, or
	// from the next distraction time today: the sliding end. A distraction
	// time closes then.
	EndsAt time.Time `json:"ends_at"`
	// DayEnd is when the budget day ends; a distraction time after it starts
	// with the next day's budget.
	DayEnd time.Time `json:"day_end"`
}

// budgetUsed reports whether a distraction time at at is closed because
// today's budget is spent.
func budgetUsed(config Config, facts Facts, at time.Time) bool {
	fact := facts.Budget
	if fact == nil || !config.BudgetAt(at).Enabled || !at.Before(fact.DayEnd) {
		return false
	}
	return fact.Used || !at.Before(fact.EndsAt)
}

// InBedtime reports whether at falls in the bedtime in force then.
func InBedtime(config Config, at time.Time) bool {
	_, in := bedtimeEnd(config.BedtimeAt(at), configLocation(config), at)
	return in
}

// BedtimeEnds is when the bedtime that at falls in ends.
func BedtimeEnds(config Config, at time.Time) (time.Time, bool) {
	return bedtimeEnd(config.BedtimeAt(at), configLocation(config), at)
}

// WindingDown reports whether at falls in the wind-down before bedtime.
func WindingDown(config Config, at time.Time) bool {
	return windingDown(config.BedtimeAt(at), configLocation(config), at)
}

// GrayscaleWanted reports whether devices are asked to turn grayscale at
// at: the wind-down, and bedtime when there is a wind-down.
func GrayscaleWanted(config Config, at time.Time) bool {
	rule := config.BedtimeAt(at)
	location := configLocation(config)
	_, in := bedtimeEnd(rule, location, at)
	return in && rule.WindDownMinutes > 0 || windingDown(rule, location, at)
}

// DistractionTimeEnds is when the distraction time at falls in ends.
func DistractionTimeEnds(config Config, at time.Time) (time.Time, bool) {
	return distractionTime(config, configLocation(config), at)
}

// NextDistractionTime is when the next distraction time after at starts.
func NextDistractionTime(config Config, at time.Time) (time.Time, bool) {
	return nextDistractionTime(config, configLocation(config), at)
}

func configLocation(config Config) *time.Location {
	location, err := time.LoadLocation(config.Timezone)
	if err != nil {
		return time.UTC
	}
	return location
}
