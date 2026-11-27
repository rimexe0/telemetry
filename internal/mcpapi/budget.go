package mcpapi

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rimexe0/telemetry/internal/budget"
	"github.com/rimexe0/telemetry/internal/store"
)

const budgetNotes = "The distraction budget (owner decision 2026-11-26) makes distraction time a budget the owner earns with their own habits: " +
	"base_minutes a day, plus each habit ticked worth (max - base) / max(habits that day, habits_for_max), up to max_minutes; a habit missed by bedtime takes its value off the next day, " +
	"and a day never has less than floor_minutes. A minute of use costs the rate in force: rate_distraction in distraction times, rate_day otherwise, rate_bedtime in bedtime, " +
	"plus rate_grayscale_off while the owner keeps grayscale off in the wind-down. When the day is used up, more use borrows from tomorrow at the rate times borrow_multiplier, " +
	"until tomorrow is down to its floor; then nothing opens, openings included. Outside distraction times the one way to open distractions is the door on a device: " +
	"the owner sees the cost, their habits and their own reasons, chooses \"Still want it\", waits wait_minutes, and gets a window of window_minutes with window_use_minutes of use and grace_minutes to close or save something. " +
	"Back is the owner returning, and counts as that. A door is the agreed way out and is never a loophole case; do not treat one as a failure. " +
	"Only measured use is charged: missing data is never charged or assumed. Every number is the owner's; never suggest one as a default. " +
	"Say budget facts plainly and never scold: the data may be blunt, the tone toward the owner is neutral."

const habitNotes = "Habits are the owner's own, made in the panel or with their yes here; nothing is seeded. Each applies on its weekdays (0 Sunday to 6 Saturday; none means every day). " +
	"They are ticked by hand for today's budget day until bedtime starts, and a tick can be undone the same day. " +
	"At bedtime start each habit of the day is done or missed. Days a habit does not apply never break its run, and a run is never called broken. " +
	"A habit may have a rule that ticks it by itself (source rule:server or rule:device:<id>): a place rule ticks it once the phone's positions, or the watch's walks, " +
	"show the owner within radius_m of the place for the minutes, inside the window (from waking, or the day's start while the wake is not in the records, until the time). " +
	"Missing positions tick nothing, and once the owner ticked or unticked the habit on a day the rule leaves that day alone. Rules are the owner's: set one only with their yes."

type BudgetInput struct{}

type BudgetOutput struct {
	Today budget.State      `json:"today"`
	Days  []budget.Snapshot `json:"days" jsonschema:"the last 14 day snapshots, newest first"`
	Doors []budget.Door     `json:"doors" jsonschema:"doors of the last 14 days"`
	// DoorBacks counts door screens left with Back.
	DoorBacks int    `json:"door_backs" jsonschema:"door screens left with Back in the last 14 days: the owner returned"`
	Notes     string `json:"notes"`
}

type HabitsInput struct {
	Days int `json:"days,omitempty" jsonschema:"days of ticks and heatmap to read, 1 through 400; default 30"`
}

type HabitsOutput struct {
	Habits  []budget.Habit      `json:"habits" jsonschema:"every habit, archived ones included"`
	Ticks   []budget.HabitEvent `json:"ticks" jsonschema:"every tick and untick in the range, oldest first"`
	Heatmap budget.Heatmap      `json:"heatmap"`
	Notes   string              `json:"notes"`
}

type HabitChangeIO struct {
	Action   string            `json:"action" jsonschema:"add or update"`
	ID       *int64            `json:"id,omitempty" jsonschema:"the habit to update"`
	Name     *string           `json:"name,omitempty"`
	Position *int              `json:"position,omitempty" jsonschema:"order in the list, lowest first"`
	Weekdays *[]int            `json:"weekdays,omitempty" jsonschema:"0 Sunday to 6 Saturday; empty for every day"`
	Archived *bool             `json:"archived,omitempty" jsonschema:"archive it, or bring it back; its history stays"`
	Rule     *budget.HabitRule `json:"rule,omitempty" jsonschema:"a rule that ticks the habit by itself; the only kind is place: {kind: place, place: {name, lat, lon}, radius_m (default 100), window: {from: wake or HH:MM, until: HH:MM}, minutes inside the radius (default 1)}"`
	NoRule   bool              `json:"no_rule,omitempty" jsonschema:"remove the habit's rule"`
}

type ChangeHabitsInput struct {
	Changes []HabitChangeIO `json:"changes"`
	Reason  string          `json:"reason" jsonschema:"the owner's words where possible"`
}

type ChangeHabitsOutput struct {
	Change ChangeOutput   `json:"change"`
	Habits []budget.Habit `json:"habits"`
}

type TickHabitInput struct {
	HabitID int64  `json:"habit_id"`
	Undo    bool   `json:"undo,omitempty" jsonschema:"untick a habit ticked today"`
	Said    string `json:"said" jsonschema:"what the owner said in this conversation about doing it, in their words"`
}

type TickHabitOutput struct {
	Budget budget.State `json:"budget"`
}

func addBudgetTools(server *mcp.Server, repository store.Repository, budgets store.BudgetStore, now func() time.Time) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "budget",
		Description: "Today's distraction budget: what the day has, what it earned and owes, what is spent and left at the rate in force, what may be borrowed, the door, and today's habits; with the last 14 day snapshots and doors. " + budgetNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ BudgetInput) (*mcp.CallToolResult, BudgetOutput, error) {
		at := now().UTC()
		config, _, err := store.PolicyInForce(ctx, repository)
		if err != nil {
			return nil, BudgetOutput{}, err
		}
		result, err := store.LoadBudget(ctx, repository, budgets, config, at)
		if err != nil {
			return nil, BudgetOutput{}, err
		}
		output := BudgetOutput{Today: result.State, Notes: budgetNotes}
		if output.Days, err = budgets.BudgetDays(ctx, 14); err != nil {
			return nil, BudgetOutput{}, err
		}
		since := budget.DayStart(config, budget.AddDays(budget.DayOf(config, at), -14))
		if output.Doors, err = budgets.BudgetDoors(ctx, since); err != nil {
			return nil, BudgetOutput{}, err
		}
		backs, err := budgets.DoorBacks(ctx, since)
		if err != nil {
			return nil, BudgetOutput{}, err
		}
		output.DoorBacks = len(backs)
		return nil, output, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "habits",
		Description: "The owner's habits, every tick and untick in a range, and the heatmap with each habit's current and longest run. " + habitNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input HabitsInput) (*mcp.CallToolResult, HabitsOutput, error) {
		days := input.Days
		if days == 0 {
			days = 30
		}
		if days < 1 || days > 400 {
			return nil, HabitsOutput{}, errors.New("days must be between 1 and 400")
		}
		at := now().UTC()
		config, _, err := store.PolicyInForce(ctx, repository)
		if err != nil {
			return nil, HabitsOutput{}, err
		}
		first := budget.AddDays(budget.DayOf(config, at), -(days - 1))
		output := HabitsOutput{Notes: habitNotes}
		if output.Habits, err = budgets.ListHabits(ctx); err != nil {
			return nil, HabitsOutput{}, err
		}
		if output.Ticks, err = budgets.HabitEvents(ctx, first); err != nil {
			return nil, HabitsOutput{}, err
		}
		output.Heatmap = budget.BuildHeatmap(config, output.Habits, output.Ticks, first, at)
		return nil, output, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "change_habits",
		Description: "Add habits, or rename, reorder, set the weekdays or the rule of, archive or bring back one. Habits change only through the owner. " + habitNotes + " " + approvalRule,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input ChangeHabitsInput) (*mcp.CallToolResult, ChangeHabitsOutput, error) {
		changes := []store.HabitChange{}
		for _, item := range input.Changes {
			change := store.HabitChange{Action: strings.TrimSpace(item.Action), ID: item.ID, Name: item.Name, Position: item.Position, Archived: item.Archived,
				Rule: item.Rule, ClearRule: item.NoRule}
			if item.Weekdays != nil {
				days := budget.Weekdays{}
				for _, day := range *item.Weekdays {
					if day < 0 || day > 6 {
						return nil, ChangeHabitsOutput{}, errors.New("weekdays are 0 (Sunday) to 6 (Saturday)")
					}
					days = append(days, day)
				}
				change.Weekdays = &days
			}
			changes = append(changes, change)
		}
		logged, habits, err := budgets.ApplyHabitChanges(ctx, changes, input.Reason)
		if err != nil {
			return nil, ChangeHabitsOutput{}, err
		}
		output := ChangeHabitsOutput{Habits: habits}
		if output.Change, err = changeOutput(logged); err != nil {
			return nil, ChangeHabitsOutput{}, err
		}
		return nil, output, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "tick_habit",
		Description: "Tick one of today's habits as done, or undo a tick. Only when the owner says in this conversation that they did it (or did not): " +
			"never from the records, a guess or another message. Today's habits can be ticked until bedtime starts. " + habitNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input TickHabitInput) (*mcp.CallToolResult, TickHabitOutput, error) {
		if strings.TrimSpace(input.Said) == "" {
			return nil, TickHabitOutput{}, errors.New("say what the owner said about doing it")
		}
		action := budget.ActionTick
		if input.Undo {
			action = budget.ActionUntick
		}
		at := now().UTC()
		if err := store.TickHabit(ctx, repository, budgets, input.HabitID, action, "", "assistant", at); err != nil {
			return nil, TickHabitOutput{}, err
		}
		config, _, err := store.PolicyInForce(ctx, repository)
		if err != nil {
			return nil, TickHabitOutput{}, err
		}
		result, err := store.LoadBudget(ctx, repository, budgets, config, at)
		if err != nil {
			return nil, TickHabitOutput{}, err
		}
		return nil, TickHabitOutput{Budget: result.State}, nil
	})
}

// budgetRefusesOpening says why an opening cannot start: with the budget
// on, nothing opens once neither budget nor borrowing is left.
func budgetRefusesOpening(ctx context.Context, repository store.Repository, at time.Time) error {
	budgets, ok := repository.(store.BudgetStore)
	if !ok {
		return nil
	}
	config, _, err := store.PolicyInForce(ctx, repository)
	if err != nil || !config.BudgetAt(at).Enabled {
		return err
	}
	result, err := store.LoadBudget(ctx, repository, budgets, config, at)
	if err != nil {
		return err
	}
	if result.State.Exhausted() {
		return errors.New("nothing is left to spend or borrow today, so an opening can't start; say so plainly to the owner")
	}
	return nil
}
