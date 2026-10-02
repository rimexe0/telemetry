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
	"and say back what you opened and until when. Times are the owner's local time."

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
	Until      string   `json:"until" jsonschema:"local HH:MM"`
}

type BlockingScheduleInput struct{}

type BlockingScheduleOutput struct {
	ClosedByDefault  bool            `json:"closed_by_default"`
	DistractionTimes []WindowIO      `json:"distraction_times"`
	OpenAddresses    []string        `json:"open_addresses"`
	Openings         []OpeningOutput `json:"openings" jsonschema:"openings in force now"`
	Timezone         string          `json:"timezone"`
	Notes            string          `json:"notes"`
}

type SetBlockingScheduleInput struct {
	ClosedByDefault     *bool       `json:"closed_by_default,omitempty"`
	DistractionTimes    *[]WindowIO `json:"distraction_times,omitempty" jsonschema:"the whole list of daily distraction times; replaces the current one"`
	AddOpenAddresses    []string    `json:"add_open_addresses,omitempty" jsonschema:"page addresses to keep open, such as https://www.youtube.com/watch?v=..."`
	RemoveOpenAddresses []string    `json:"remove_open_addresses,omitempty"`
	Reason              string      `json:"reason" jsonschema:"the owner's words where possible"`
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
		Until: opening.Until.In(location).Format("15:04")}
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
		Timezone: config.Timezone, Notes: scheduleNotes}
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
	return output, nil
}

func addScheduleTools(server *mcp.Server, repository store.Repository, now func() time.Time) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "blocking_schedule",
		Description: "When distractions are closed: the closed-by-default switch, the daily distraction times, pages kept open, and openings in force. " + scheduleNotes,
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
		Description: "Turn closed-by-default on or off, set the daily distraction times, or change which pages stay open. " + scheduleNotes + " " + approvalRule,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input SetBlockingScheduleInput) (*mcp.CallToolResult, SetBlockingScheduleOutput, error) {
		change := store.ScheduleChange{ClosedByDefault: input.ClosedByDefault, AddOpenAddresses: input.AddOpenAddresses, RemoveOpenAddresses: input.RemoveOpenAddresses}
		if input.DistractionTimes != nil {
			windows := []policy.Window{}
			for _, window := range *input.DistractionTimes {
				windows = append(windows, policy.Window{Start: window.Start, End: window.End})
			}
			change.DistractionTimes = &windows
		}
		if change.ClosedByDefault == nil && change.DistractionTimes == nil && len(change.AddOpenAddresses)+len(change.RemoveOpenAddresses) == 0 {
			return nil, SetBlockingScheduleOutput{}, errors.New("nothing to change")
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
			"It only lifts blocks. The owner asking for it in this conversation is the approval; it is logged, and revert_change ends it early. " + scheduleNotes,
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
		output := OpenNowOutput{Opening: openingOutput(opening, location), Notes: "Devices fetch decisions about once a minute, so it opens within about a minute."}
		if output.Change, err = changeOutput(logged); err != nil {
			return nil, OpenNowOutput{}, err
		}
		return nil, output, nil
	})
}
