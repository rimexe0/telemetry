package store

import (
	"context"
	"time"

	"github.com/rimexe0/telemetry/internal/budget"
	"github.com/rimexe0/telemetry/internal/places"
)

// Habit rules tick a habit by themselves (owner request 2026-11-26). The
// phone works its rules out first and sends the tick like a late tick, with
// when the rule was met; the server works them out again from the stored
// positions as a safety net (EvaluateHabitRules). Either way the owner's
// word wins: once a habit was ticked or unticked on a day, a rule leaves
// that day alone.

// RuleSourceServer marks a tick the server's own check of a rule made.
const RuleSourceServer = budget.RuleSourcePrefix + ":server"

// RuleTick keeps a tick a habit rule made at at. It is kept only when the
// habit still has a rule and nothing was ticked or unticked for it on the
// day yet; recorded says whether it was. Otherwise it is LateTick: the day
// at falls in, while that day's habits were open.
func RuleTick(ctx context.Context, repository Repository, budgets BudgetStore, id int64, day string, at time.Time, source string, now time.Time) (bool, error) {
	config, _, err := PolicyInForce(ctx, repository)
	if err != nil {
		return false, err
	}
	habits, err := budgets.ListHabits(ctx)
	if err != nil {
		return false, err
	}
	var habit *budget.Habit
	for i := range habits {
		if habits[i].ID == id {
			habit = &habits[i]
		}
	}
	if habit == nil {
		return false, ErrNotFound
	}
	// A rule removed since the phone worked it out ticks nothing.
	if habit.Rule == nil {
		return false, nil
	}
	madeFor, _ := budget.TickDay(config, at.UTC())
	events, err := budgets.HabitEvents(ctx, madeFor)
	if err != nil {
		return false, err
	}
	for _, event := range events {
		if event.HabitID == id && event.Day == madeFor {
			return false, nil
		}
	}
	if !budget.ByRule(source) {
		source = budget.RuleSourcePrefix + ":" + source
	}
	if err := LateTick(ctx, repository, budgets, id, budget.ActionTick, day, at, source, now); err != nil {
		return false, err
	}
	return true, nil
}

// ruleLookBack is how many budget days back the server checks rules: today,
// and yesterday for a server that was down through a window.
const ruleLookBack = 1

// EvaluateHabitRules checks each habit rule against the stored phone
// positions and the watch's worn walks, for today and yesterday, and ticks a
// habit whose rule was met and that nobody ticked or unticked that day. It
// returns how many it ticked. Missing positions tick nothing.
func EvaluateHabitRules(ctx context.Context, repository Repository, budgets BudgetStore, now time.Time) (int, error) {
	habits, err := budgets.ListHabits(ctx)
	if err != nil {
		return 0, err
	}
	ruled := []budget.Habit{}
	for _, habit := range habits {
		if habit.Rule != nil && habit.ArchivedAt == nil {
			ruled = append(ruled, habit)
		}
	}
	if len(ruled) == 0 {
		return 0, nil
	}
	config, _, err := PolicyInForce(ctx, repository)
	if err != nil {
		return 0, err
	}
	today := budget.DayOf(config, now)
	first := budget.AddDays(today, -ruleLookBack)
	events, err := budgets.HabitEvents(ctx, first)
	if err != nil {
		return 0, err
	}
	type habitDay struct {
		day string
		id  int64
	}
	touched := map[habitDay]bool{}
	for _, event := range events {
		touched[habitDay{event.Day, event.HabitID}] = true
	}
	ticked := 0
	for day := first; day <= today; day = budget.AddDays(day, 1) {
		var wake *time.Time
		wakeRead := false
		for _, habit := range ruled {
			if touched[habitDay{day, habit.ID}] {
				continue
			}
			rule := *habit.Rule
			if rule.Window.From == budget.RuleFromWake && !wakeRead {
				// The wake of the day: read at the day's close, or now.
				at := budget.CloseAt(config, day)
				if at.After(now) {
					at = now
				}
				if wake, err = LoadWakeTime(ctx, repository, at); err != nil {
					return ticked, err
				}
				wakeRead = true
			}
			span, ok := budget.SpanOn(config, rule, day, wake)
			if !ok || !span.From.Before(now) {
				continue
			}
			until := span.Until
			if until.After(now) {
				until = now
			}
			tracks, err := ruleTracks(ctx, repository, span.From, until)
			if err != nil {
				return ticked, err
			}
			reached := budget.PlaceReached(rule, tracks, span.From, until)
			if reached == nil || !habit.AppliesAt(day, *reached) {
				continue
			}
			recorded, err := RuleTick(ctx, repository, budgets, habit.ID, day, *reached, RuleSourceServer, now)
			if err != nil {
				return ticked, err
			}
			if recorded {
				ticked++
			}
		}
	}
	return ticked, nil
}

// ruleTracks are the phone's positions and the watch's worn walks around
// [from, until), one track per device stretch.
func ruleTracks(ctx context.Context, repository Repository, from, until time.Time) ([][]places.Fix, error) {
	// A little before the window, so a stay already going counts from its start.
	since := from.Add(-15 * time.Minute)
	phone, err := repository.LocationFixes(ctx, since, until)
	if err != nil {
		return nil, err
	}
	walks, err := repository.WatchTracks(ctx, since, until)
	if err != nil {
		return nil, err
	}
	return append([][]places.Fix{phone}, walks...), nil
}
