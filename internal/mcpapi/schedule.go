package mcpapi

import (
	"context"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rimexe0/telemetry/internal/policy"
	"github.com/rimexe0/telemetry/internal/store"
)

const scheduleNotes = "When closed_by_default is on, everything labelled \"distraction\" is closed at all times, on every device, whether or not a plan exists; " +
	"it opens only during the daily distraction times (local HH:MM, may run past midnight) and during an opening. " +
	"open_addresses are single pages that stay open while their site is closed, such as one music stream on YouTube; they apply in the browser only. " +
	"The owner chose fixed daily times and ruled out a daily budget of minutes: a budget spent early leaves nothing for the rest of the day. " +
	"An opening lifts the block on named sites or apps for a stated purpose for a limited time; use open_now when the owner needs something that is closed, " +
	"and say back what you opened and until when. Times are the owner's local time. " +
	"lock_until_planned closes everything except what is labelled \"essential\" on every device from the moment the owner wakes (taken from the records, never asked) " +
	"until the day has a plan; switched on during a day, it starts at the next waking; set_day_plan for that day opens it, and a day with no fixed times is planned with an empty list of blocks once its tasks are agreed. " +
	"It cannot be turned on until the owner's way to reach you is labelled essential."

const outsideNotes = "The outside rule makes the owner go outside before things open: until they have been outside for minutes today (positions at least meters from home), " +
	"it holds \"distractions\" closed (distraction times included), \"lock\" closes everything but the essentials, or both. It counts the watch's walks (only while worn) " +
	"and, unless watch_only, the phone's positions; on_foot leaves out cars and buses. from/from_wake and until bound its hours each day. Openings do not lift it unless allow_openings; " +
	"an emergency unlock on the phone still does. The owner asked for it to be hard to get around: a change that asks less in any way, switching it off or reverting included, " +
	"takes effect only the next day; one that asks more applies at once. Every number is the owner's to choose; never suggest one as a default. " +
	"Tell them their progress from outside.today, and never shame them for not having gone out."

const openingNotes = "opening_rule is the owner's: an opening starts wait_minutes after it is made, lasts at most max_minutes, and at most per_day are made each day " +
	"(one ended before it started does not count; zero leaves that part out). It applies to open_now too, so say back when it starts as well as when it ends. " +
	"morning closes everything but the essentials (lock_all) or the distractions from waking (from_wake) or a set time (from) until the owner scans the printed morning code, " +
	"and gives up at until each day. Only the owner's scan opens it; neither you nor an opening can. The code is printed from the panel. " +
	"As with the outside rule, a change to either that asks less waits for the next day; one that asks more applies at once."

type WindowIO struct {
	Start string `json:"start" jsonschema:"local HH:MM"`
	End   string `json:"end" jsonschema:"local HH:MM; at or before start means past midnight"`
}

type OpeningOutput struct {
	ID         int64    `json:"id"`
	Purpose    string   `json:"purpose"`
	Sites      []string `json:"sites"`
	Apps       []string `json:"apps"`
	Everything bool     `json:"everything"`
	From       string   `json:"from" jsonschema:"local HH:MM it starts, after the owner's wait"`
	Until      string   `json:"until" jsonschema:"local HH:MM"`
}

type OpeningRuleIO struct {
	WaitMinutes int `json:"wait_minutes" jsonschema:"minutes from asking until an opening starts, 0 through 240"`
	MaxMinutes  int `json:"max_minutes" jsonschema:"longest an opening may last, 0 for no limit, up to 1440"`
	PerDay      int `json:"per_day" jsonschema:"openings each day, 0 for no limit, up to 50"`
}

type MorningRuleIO struct {
	Enabled  bool   `json:"enabled"`
	FromWake bool   `json:"from_wake,omitempty" jsonschema:"start holding when the owner wakes"`
	From     string `json:"from,omitempty" jsonschema:"local HH:MM to start holding instead of waking"`
	Until    string `json:"until" jsonschema:"local HH:MM after which it gives up for the day; required"`
	LockAll  bool   `json:"lock_all,omitempty" jsonschema:"close everything but the essentials, not only distractions"`
}

type OpeningRuleOutput struct {
	Rule      OpeningRuleIO  `json:"rule" jsonschema:"in force today"`
	Next      *OpeningRuleIO `json:"next,omitempty"`
	NextFrom  string         `json:"next_from,omitempty" jsonschema:"local date the waiting rule starts"`
	UsedToday int            `json:"used_today" jsonschema:"openings made today that count toward per_day"`
}

type MorningOutput struct {
	Rule      MorningRuleIO  `json:"rule" jsonschema:"in force today"`
	Next      *MorningRuleIO `json:"next,omitempty"`
	NextFrom  string         `json:"next_from,omitempty" jsonschema:"local date the waiting rule starts"`
	ScannedAt string         `json:"scanned_at,omitempty" jsonschema:"local time of the latest scan today"`
}

type BlockingScheduleInput struct{}

type BlockingScheduleOutput struct {
	ClosedByDefault  bool              `json:"closed_by_default"`
	DistractionTimes []WindowIO        `json:"distraction_times"`
	OpenAddresses    []string          `json:"open_addresses"`
	LockUntilPlanned bool              `json:"lock_until_planned"`
	Essentials       []string          `json:"essentials" jsonschema:"what stays open while the day is locked"`
	Openings         []OpeningOutput   `json:"openings" jsonschema:"openings in force now"`
	Timezone         string            `json:"timezone"`
	Outside          OutsideOutput     `json:"outside"`
	OpeningRule      OpeningRuleOutput `json:"opening_rule"`
	Morning          MorningOutput     `json:"morning"`
	Notes            string            `json:"notes"`
}

type OutsideRuleIO struct {
	Enabled       bool     `json:"enabled"`
	Minutes       int      `json:"minutes,omitempty" jsonschema:"minutes outside needed each day, 1 through 600"`
	Meters        int      `json:"meters,omitempty" jsonschema:"how far from home counts as outside, 30 through 50000"`
	Holds         []string `json:"holds,omitempty" jsonschema:"what stays shut until met: distractions, lock, or both"`
	FromWake      bool     `json:"from_wake,omitempty" jsonschema:"start holding when the owner wakes"`
	From          string   `json:"from,omitempty" jsonschema:"local HH:MM to start holding; empty for midnight or waking"`
	Until         string   `json:"until,omitempty" jsonschema:"local HH:MM after which it gives up for the day; empty for the whole day"`
	WatchOnly     bool     `json:"watch_only,omitempty" jsonschema:"count only walks the watch proves, worn"`
	OnFoot        bool     `json:"on_foot,omitempty" jsonschema:"leave out stretches faster than walking"`
	AllowOpenings bool     `json:"allow_openings,omitempty" jsonschema:"let openings lift it"`
}

type OutsideOutput struct {
	Rule OutsideRuleIO `json:"rule" jsonschema:"in force today"`
	// Next is a looser rule waiting for its day.
	Next     *OutsideRuleIO         `json:"next,omitempty"`
	NextFrom string                 `json:"next_from,omitempty" jsonschema:"local date the waiting rule starts"`
	Today    *store.OutsideProgress `json:"today,omitempty" jsonschema:"time outside so far today, as the rule in force counts it"`
	Notes    string                 `json:"notes"`
}

func outsideIO(rule policy.OutsideRule) OutsideRuleIO {
	return OutsideRuleIO{Enabled: rule.Enabled, Minutes: rule.Minutes, Meters: rule.Meters, Holds: append([]string{}, rule.Holds...), FromWake: rule.FromWake,
		From: rule.From, Until: rule.Until, WatchOnly: rule.WatchOnly, OnFoot: rule.OnFoot, AllowOpenings: rule.AllowOpenings}
}

type SetBlockingScheduleInput struct {
	ClosedByDefault     *bool          `json:"closed_by_default,omitempty"`
	DistractionTimes    *[]WindowIO    `json:"distraction_times,omitempty" jsonschema:"the whole list of daily distraction times; replaces the current one"`
	AddOpenAddresses    []string       `json:"add_open_addresses,omitempty" jsonschema:"page addresses to keep open, such as https://www.youtube.com/watch?v=..."`
	RemoveOpenAddresses []string       `json:"remove_open_addresses,omitempty"`
	LockUntilPlanned    *bool          `json:"lock_until_planned,omitempty" jsonschema:"lock everything but the essentials from waking until the day is planned"`
	Outside             *OutsideRuleIO `json:"outside,omitempty" jsonschema:"the whole outside rule; replaces the current one"`
	OpeningRule         *OpeningRuleIO `json:"opening_rule,omitempty" jsonschema:"the whole opening rule; replaces the current one"`
	Morning             *MorningRuleIO `json:"morning,omitempty" jsonschema:"the whole morning rule; replaces the current one"`
	Reason              string         `json:"reason" jsonschema:"the owner's words where possible"`
}

type SetBlockingScheduleOutput struct {
	Change   ChangeOutput           `json:"change"`
	Schedule BlockingScheduleOutput `json:"schedule"`
}

type OpenNowInput struct {
	Minutes    int      `json:"minutes" jsonschema:"how long from now, 1 through 240"`
	Sites      []string `json:"sites,omitempty" jsonschema:"sites to open, such as youtube.com"`
	Apps       []string `json:"apps,omitempty" jsonschema:"Android packages or Mac app names to open"`
	Everything bool     `json:"everything,omitempty" jsonschema:"open every distraction; only when the owner asks for that"`
	Purpose    string   `json:"purpose" jsonschema:"what the owner needs it for, in their words"`
}

type OpenNowOutput struct {
	Change  ChangeOutput  `json:"change"`
	Opening OpeningOutput `json:"opening"`
	Notes   string        `json:"notes"`
}

func openingOutput(opening store.Opening, location *time.Location) OpeningOutput {
	return OpeningOutput{ID: opening.ID, Purpose: opening.Purpose, Sites: opening.Sites, Apps: opening.Apps, Everything: opening.Everything,
		From: opening.StartsAt.In(location).Format("15:04"), Until: opening.Until.In(location).Format("15:04")}
}

func morningIO(rule policy.MorningRule) MorningRuleIO {
	return MorningRuleIO{Enabled: rule.Enabled, FromWake: rule.FromWake, From: rule.From, Until: rule.Until, LockAll: rule.LockAll}
}

func scheduleOutput(ctx context.Context, repository store.Repository, config policy.Config, now time.Time) (BlockingScheduleOutput, error) {
	if err := config.Normalize(); err != nil {
		return BlockingScheduleOutput{}, err
	}
	location, err := time.LoadLocation(config.Timezone)
	if err != nil {
		return BlockingScheduleOutput{}, err
	}
	output := BlockingScheduleOutput{ClosedByDefault: config.ClosedByDefault, DistractionTimes: []WindowIO{}, OpenAddresses: []string{}, Openings: []OpeningOutput{},
		LockUntilPlanned: config.PlanGate.FromWake, Essentials: []string{}, Timezone: config.Timezone, Notes: scheduleNotes + " " + openingNotes}
	labels, err := repository.ListActivityLabels(ctx)
	if err != nil {
		return BlockingScheduleOutput{}, err
	}
	for _, label := range labels {
		if label.Label == policy.LabelEssential {
			output.Essentials = append(output.Essentials, label.Match)
		}
	}
	for _, window := range config.DistractionTimes {
		output.DistractionTimes = append(output.DistractionTimes, WindowIO{Start: window.Start, End: window.End})
	}
	output.OpenAddresses = append(output.OpenAddresses, config.OpenAddresses...)
	openings, err := repository.ActiveOpenings(ctx, now)
	if err != nil {
		return BlockingScheduleOutput{}, err
	}
	for _, opening := range openings {
		output.Openings = append(output.Openings, openingOutput(opening, location))
	}
	today := now.In(location)
	openingRule := config.OpeningRuleAt(now)
	output.OpeningRule.Rule = OpeningRuleIO(openingRule)
	if config.OpeningRuleNext != nil && config.OpeningRuleNextFrom > today.Format("2006-01-02") {
		next := OpeningRuleIO(*config.OpeningRuleNext)
		output.OpeningRule.Next, output.OpeningRule.NextFrom = &next, config.OpeningRuleNextFrom
	}
	if output.OpeningRule.UsedToday, err = repository.OpeningsSince(ctx, time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, location)); err != nil {
		return BlockingScheduleOutput{}, err
	}
	output.Morning.Rule = morningIO(config.MorningAt(now))
	if config.MorningNext != nil && config.MorningNextFrom > today.Format("2006-01-02") {
		next := morningIO(*config.MorningNext)
		output.Morning.Next, output.Morning.NextFrom = &next, config.MorningNextFrom
	}
	scanned, err := repository.LatestMorningScan(ctx, now)
	if err != nil {
		return BlockingScheduleOutput{}, err
	}
	if scanned != nil && scanned.In(location).Format("2006-01-02") == today.Format("2006-01-02") {
		output.Morning.ScannedAt = scanned.In(location).Format("15:04")
	}
	rule := config.OutsideAt(now)
	output.Outside = OutsideOutput{Rule: outsideIO(rule), Notes: outsideNotes}
	if config.OutsideNext != nil && config.OutsideNextFrom > now.In(location).Format("2006-01-02") {
		next := outsideIO(*config.OutsideNext)
		output.Outside.Next, output.Outside.NextFrom = &next, config.OutsideNextFrom
	}
	if rule.Enabled {
		progress, err := store.OutsideToday(ctx, repository, rule, location, now)
		if err != nil {
			return BlockingScheduleOutput{}, err
		}
		output.Outside.Today = &progress
	}
	return output, nil
}

func addScheduleTools(server *mcp.Server, repository store.Repository, now func() time.Time) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "blocking_schedule",
		Description: "When distractions are closed: the closed-by-default switch, the daily distraction times, pages kept open, openings in force or waiting, the opening rule, the morning code rule, and the outside rule with today's time outside. " + scheduleNotes + " " + outsideNotes + " " + openingNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ BlockingScheduleInput) (*mcp.CallToolResult, BlockingScheduleOutput, error) {
		config := policy.Config{}
		stored, err := repository.LatestPolicy(ctx)
		if err != nil {
			return nil, BlockingScheduleOutput{}, err
		}
		if stored != nil {
			config = stored.Config
		}
		output, err := scheduleOutput(ctx, repository, config, now().UTC())
		return nil, output, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "set_blocking_schedule",
		Description: "Turn closed-by-default on or off, set the daily distraction times, change which pages stay open, or set the outside, opening or morning rule. " + scheduleNotes + " " + outsideNotes + " " + openingNotes + " " + approvalRule,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input SetBlockingScheduleInput) (*mcp.CallToolResult, SetBlockingScheduleOutput, error) {
		change := store.ScheduleChange{ClosedByDefault: input.ClosedByDefault, AddOpenAddresses: input.AddOpenAddresses, RemoveOpenAddresses: input.RemoveOpenAddresses}
		if input.DistractionTimes != nil {
			windows := []policy.Window{}
			for _, window := range *input.DistractionTimes {
				windows = append(windows, policy.Window{Start: window.Start, End: window.End})
			}
			change.DistractionTimes = &windows
		}
		change.LockUntilPlanned = input.LockUntilPlanned
		if rule := input.OpeningRule; rule != nil {
			change.OpeningRule = &policy.OpeningRule{WaitMinutes: rule.WaitMinutes, MaxMinutes: rule.MaxMinutes, PerDay: rule.PerDay}
		}
		if rule := input.Morning; rule != nil {
			change.Morning = &policy.MorningRule{Enabled: rule.Enabled, FromWake: rule.FromWake, From: rule.From, Until: rule.Until, LockAll: rule.LockAll}
		}
		if rule := input.Outside; rule != nil {
			change.Outside = &policy.OutsideRule{Enabled: rule.Enabled, Minutes: rule.Minutes, Meters: rule.Meters, Holds: rule.Holds, FromWake: rule.FromWake,
				From: rule.From, Until: rule.Until, WatchOnly: rule.WatchOnly, OnFoot: rule.OnFoot, AllowOpenings: rule.AllowOpenings}
		}
		if change.ClosedByDefault == nil && change.DistractionTimes == nil && change.LockUntilPlanned == nil && change.Outside == nil && change.OpeningRule == nil && change.Morning == nil && len(change.AddOpenAddresses)+len(change.RemoveOpenAddresses) == 0 {
			return nil, SetBlockingScheduleOutput{}, errors.New("nothing to change")
		}
		if change.LockUntilPlanned != nil && *change.LockUntilPlanned {
			labels, err := repository.ListActivityLabels(ctx)
			if err != nil {
				return nil, SetBlockingScheduleOutput{}, err
			}
			reachable := false
			for _, label := range labels {
				reachable = reachable || label.Label == policy.LabelEssential
			}
			if !reachable {
				return nil, SetBlockingScheduleOutput{}, errors.New("nothing is labelled essential, so the owner could not reach you to plan the day; label their messaging app essential first (change_activity_labels)")
			}
		}
		logged, config, err := repository.ApplyBlockingSchedule(ctx, change, input.Reason)
		if err != nil {
			return nil, SetBlockingScheduleOutput{}, err
		}
		output := SetBlockingScheduleOutput{}
		if output.Change, err = changeOutput(logged); err != nil {
			return nil, SetBlockingScheduleOutput{}, err
		}
		output.Schedule, err = scheduleOutput(ctx, repository, config, now().UTC())
		return nil, output, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "open_now",
		Description: "Open named sites or apps that are closed, for a stated purpose and a limited time, such as YouTube for a tutorial the work needs. " +
			"It only lifts blocks. The owner asking for it in this conversation is the approval; it is logged, and revert_change ends it early or cancels it during its wait. " + scheduleNotes + " " + openingNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input OpenNowInput) (*mcp.CallToolResult, OpenNowOutput, error) {
		if input.Minutes < 1 || input.Minutes > 240 {
			return nil, OpenNowOutput{}, errors.New("minutes must be between 1 and 240")
		}
		location, err := ownerLocation(ctx, repository)
		if err != nil {
			return nil, OpenNowOutput{}, err
		}
		until := now().UTC().Add(time.Duration(input.Minutes) * time.Minute)
		logged, opening, err := repository.OpenNow(ctx, store.Opening{Purpose: input.Purpose, Sites: input.Sites, Apps: input.Apps, Everything: input.Everything, Until: until}, input.Purpose)
		if err != nil {
			return nil, OpenNowOutput{}, err
		}
		notes := "Devices fetch decisions about once a minute, so it opens within about a minute."
		if opening.StartsAt.After(now().UTC()) {
			notes = "It opens at " + opening.StartsAt.In(location).Format("15:04") + ", after the owner's wait, and closes at " + opening.Until.In(location).Format("15:04") + "."
		}
		output := OpenNowOutput{Opening: openingOutput(opening, location), Notes: notes}
		if output.Change, err = changeOutput(logged); err != nil {
			return nil, OpenNowOutput{}, err
		}
		return nil, output, nil
	})
}
