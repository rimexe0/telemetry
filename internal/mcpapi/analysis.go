package mcpapi

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rimexe0/telemetry/internal/activity"
	"github.com/rimexe0/telemetry/internal/store"
)

// Tools for looking at weeks and months at once: the counting is done here,
// so an assistant compares days instead of adding up records.

const analysisNotes = "Each row is computed from the original records by the same day model as the panel. " +
	"Missing data is never rest, focus or inactivity: check no_data_minutes before comparing days, and leave out or flag days with large gaps. " +
	"Report patterns as facts about this owner, never as failures."

type DateRangeInput struct {
	FromDate string `json:"from_date,omitempty" jsonschema:"first local day YYYY-MM-DD; default six days before to_date"`
	ToDate   string `json:"to_date,omitempty" jsonschema:"last local day YYYY-MM-DD, included; default today"`
}

type ActivityDailyOutput struct {
	Timezone string     `json:"timezone"`
	Days     []DailyRow `json:"days"`
	Notes    string     `json:"notes"`
}

type DailyRow struct {
	Date    string        `json:"date"`
	Devices []DailyDevice `json:"devices"`
	// FirstActive and LastActive are the day's first and last recorded use
	// on any device.
	FirstActive *time.Time `json:"first_active,omitempty"`
	LastActive  *time.Time `json:"last_active,omitempty"`
	// LongestFocusMinutes is the longest stretch of use not mostly on
	// distractions.
	LongestFocusMinutes float64     `json:"longest_focus_minutes"`
	DistractionMinutes  float64     `json:"distraction_minutes"`
	DriftEpisodes       int         `json:"drift_episodes"`
	HadPlan             bool        `json:"had_plan"`
	PlannedMinutes      float64     `json:"planned_minutes"`
	InPlanMinutes       float64     `json:"in_plan_minutes"`
	RestMinutes         float64     `json:"rest_minutes"`
	ScrollingMinutes    float64     `json:"scrolling_minutes"`
	OutsideMinutes      float64     `json:"outside_minutes"`
	Tasks               []DailyTask `json:"tasks,omitempty"`
}

type DailyDevice struct {
	Device             string           `json:"device"`
	ActiveMinutes      float64          `json:"active_minutes"`
	DistractionMinutes float64          `json:"distraction_minutes"`
	NoDataMinutes      float64          `json:"no_data_minutes"`
	TopApps            []activity.Usage `json:"top_apps"`
}

type DailyTask struct {
	Title   string  `json:"title"`
	Minutes float64 `json:"minutes"`
	Returns int     `json:"returns"`
}

type HealthDailyOutput struct {
	Timezone string               `json:"timezone"`
	Days     []activity.HealthDay `json:"days"`
	Notes    string               `json:"notes"`
}

type DriftEpisodesInput struct {
	DateRangeInput
	MinMinutes float64 `json:"min_minutes,omitempty" jsonschema:"shortest episode in active minutes; default 10"`
}

type DriftEpisodesOutput struct {
	Timezone string                  `json:"timezone"`
	Episodes []activity.DriftEpisode `json:"episodes"`
	Notes    string                  `json:"notes"`
}

func addAnalysisTools(server *mcp.Server, repository store.Repository, now func() time.Time) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "activity_daily",
		Description: "One row per local day over up to 62 days: active, distraction and no-data minutes and top apps per device, first and last use, " +
			"the longest stretch not on distractions, drift episodes, planned and in-plan minutes, rest, scrolling, time outside and time on tasks. " + analysisNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input DateRangeInput) (*mcp.CallToolResult, ActivityDailyOutput, error) {
		days, location, err := dayRange(ctx, repository, input, now(), 62)
		if err != nil {
			return nil, ActivityDailyOutput{}, err
		}
		output := ActivityDailyOutput{Timezone: location.String(), Days: []DailyRow{}, Notes: analysisNotes}
		for _, day := range days {
			row, err := dailyRow(ctx, repository, day, now())
			if err != nil {
				return nil, ActivityDailyOutput{}, fmt.Errorf("%s: %w", day.Format("2006-01-02"), err)
			}
			output.Days = append(output.Days, row)
		}
		return nil, output, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "health_daily",
		Description: "One row per local day over up to 186 days: the night that ended that day (in bed and asleep minutes), steps and heart rate. " +
			"Sources overlap (the phone and Samsung Health through Health Connect, and Samsung Health directly), so they are never added up: " +
			"steps is the highest single source, with every source's count listed, and overlapping nights count once. Heart rate is from the source with the most samples that day. " +
			"A day without a row value has no record, which is not zero. Report patterns as facts, never as failures.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input DateRangeInput) (*mcp.CallToolResult, HealthDailyOutput, error) {
		days, location, err := dayRange(ctx, repository, input, now(), 186)
		if err != nil {
			return nil, HealthDailyOutput{}, err
		}
		from, to := days[0], days[len(days)-1].AddDate(0, 0, 1)
		// A night that ends on the first day began the evening before.
		records, err := repository.HealthRecords(ctx, from.AddDate(0, 0, -1), to)
		if err != nil {
			return nil, HealthDailyOutput{}, err
		}
		return nil, HealthDailyOutput{Timezone: location.String(), Days: activity.HealthDays(records, from, to, location),
			Notes: "Steps and nights are deduplicated across sources, never summed; missing values are unknown, not zero."}, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "drift_episodes",
		Description: "Stretches of use spent mostly on distractions over up to 31 days, on any device: when, what they went to, what came before, " +
			"and whether the owner came back to work within an hour (came_back) and to a task (back_to_task). Coming back is what matters. " + analysisNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input DriftEpisodesInput) (*mcp.CallToolResult, DriftEpisodesOutput, error) {
		minimum := input.MinMinutes
		if minimum == 0 {
			minimum = 10
		}
		if minimum < 1 || minimum > 240 {
			return nil, DriftEpisodesOutput{}, errors.New("min_minutes must be between 1 and 240")
		}
		days, location, err := dayRange(ctx, repository, input.DateRangeInput, now(), 31)
		if err != nil {
			return nil, DriftEpisodesOutput{}, err
		}
		output := DriftEpisodesOutput{Timezone: location.String(), Episodes: []activity.DriftEpisode{}, Notes: analysisNotes}
		for _, day := range days {
			built, err := buildDay(ctx, repository, day, now())
			if err != nil {
				return nil, DriftEpisodesOutput{}, fmt.Errorf("%s: %w", day.Format("2006-01-02"), err)
			}
			output.Episodes = append(output.Episodes, activity.DriftEpisodes(built, minimum)...)
		}
		return nil, output, nil
	})
}

// dayRange returns the local midnights of each day in the range, oldest
// first, at most limit of them.
func dayRange(ctx context.Context, repository store.Repository, input DateRangeInput, current time.Time, limit int) ([]time.Time, *time.Location, error) {
	location, err := ownerLocation(ctx, repository)
	if err != nil {
		return nil, nil, err
	}
	today := current.In(location)
	last := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, location)
	if input.ToDate != "" {
		if last, err = time.ParseInLocation("2006-01-02", input.ToDate, location); err != nil {
			return nil, nil, errors.New("to_date must be YYYY-MM-DD")
		}
	}
	first := last.AddDate(0, 0, -6)
	if input.FromDate != "" {
		if first, err = time.ParseInLocation("2006-01-02", input.FromDate, location); err != nil {
			return nil, nil, errors.New("from_date must be YYYY-MM-DD")
		}
	}
	if last.After(current) {
		return nil, nil, errors.New("to_date must not be in the future")
	}
	if last.Before(first) {
		return nil, nil, errors.New("from_date must not be after to_date")
	}
	days := []time.Time{}
	for day := first; !day.After(last); day = day.AddDate(0, 0, 1) {
		if len(days) == limit {
			return nil, nil, fmt.Errorf("the range must be at most %d days", limit)
		}
		days = append(days, day)
	}
	return days, location, nil
}

// buildDay runs the panel's day model for one local day, up to now.
func buildDay(ctx context.Context, repository store.Repository, day, current time.Time) (activity.Day, error) {
	input, err := dayInput(ctx, repository, day, current)
	if err != nil {
		return activity.Day{}, err
	}
	return activity.BuildDay(input), nil
}

func dayInput(ctx context.Context, repository store.Repository, day, current time.Time) (activity.DayInput, error) {
	end := day.AddDate(0, 0, 1)
	if end.After(current) {
		end = current
	}
	input, _, err := store.LoadDayInput(ctx, repository, day, day, end, false)
	return input, err
}

func dailyRow(ctx context.Context, repository store.Repository, day, current time.Time) (DailyRow, error) {
	input, err := dayInput(ctx, repository, day, current)
	if err != nil {
		return DailyRow{}, err
	}
	built := activity.BuildDay(input)
	row := DailyRow{Date: day.Format("2006-01-02"), Devices: []DailyDevice{}, DistractionMinutes: built.DistractionMinutes,
		DriftEpisodes: len(activity.DriftEpisodes(built, 10)), HadPlan: len(built.Blocks) > 0, PlannedMinutes: built.PlannedMinutes,
		InPlanMinutes: built.InPlanMinutes, RestMinutes: built.RestMinutes, ScrollingMinutes: built.ScrollingMinutes, OutsideMinutes: built.OutsideMinutes}
	distraction := map[string]float64{}
	for _, device := range built.Distraction {
		distraction[device.Device] = device.Minutes
	}
	for _, summary := range activity.Summarize(input.Result, 3, input.Labels) {
		row.Devices = append(row.Devices, DailyDevice{Device: summary.Device, ActiveMinutes: summary.ActiveMinutes,
			DistractionMinutes: distraction[summary.Device], NoDataMinutes: summary.NoDataMinutes, TopApps: summary.Apps})
	}
	for _, segment := range input.Result.Segments {
		start, end := segment.Start, segment.End
		if row.FirstActive == nil || start.Before(*row.FirstActive) {
			row.FirstActive = &start
		}
		if row.LastActive == nil || end.After(*row.LastActive) {
			row.LastActive = &end
		}
	}
	for _, chunk := range built.Chunks {
		if chunk.Kind != "distraction" && chunk.ActiveMinutes > row.LongestFocusMinutes {
			row.LongestFocusMinutes = chunk.ActiveMinutes
		}
	}
	for _, task := range built.Tasks {
		if task.Minutes > 0 {
			row.Tasks = append(row.Tasks, DailyTask{Title: task.Title, Minutes: task.Minutes, Returns: task.Returns})
		}
	}
	return row, nil
}
