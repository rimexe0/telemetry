package mcpapi

import (
	"context"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rimexe0/telemetry/internal/store"
)

const calendarNotes = "From the owner's calendars as their phone syncs them (their Google calendars), read-only and refreshed every 15 minutes; nothing is ever written to them. " +
	"An appointment that is busy is a fixed commitment: plan around it rather than over it. Free events and all-day entries such as holidays are context, not commitments. " +
	"Times are the owner's local time."

type CalendarInput struct {
	Date string `json:"date,omitempty" jsonschema:"first day, YYYY-MM-DD in the owner's time zone; default today"`
	Days int    `json:"days,omitempty" jsonschema:"how many days from date, 1 through 14; default 1"`
}

type CalendarEventOutput struct {
	Date     string `json:"date" jsonschema:"YYYY-MM-DD"`
	Start    string `json:"start,omitempty" jsonschema:"local HH:MM; empty for all-day"`
	End      string `json:"end,omitempty" jsonschema:"local HH:MM; empty for all-day"`
	Title    string `json:"title"`
	Location string `json:"location,omitempty"`
	Calendar string `json:"calendar"`
	AllDay   bool   `json:"all_day"`
	// Fixed means busy and timed: a commitment to plan around.
	Fixed bool `json:"fixed"`
}

type CalendarOutput struct {
	Events   []CalendarEventOutput `json:"events" jsonschema:"earliest first"`
	Timezone string                `json:"timezone"`
	Notes    string                `json:"notes"`
}

// calendarDays lists the events on the local days [first, first+days).
func calendarDays(ctx context.Context, repository store.Repository, first time.Time, days int, location *time.Location) ([]CalendarEventOutput, error) {
	start := time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, location)
	end := start.AddDate(0, 0, days)
	// All-day events are stored from UTC midnight; read a day either side.
	events, err := repository.CalendarEvents(ctx, start.Add(-24*time.Hour), end.Add(24*time.Hour))
	if err != nil {
		return nil, err
	}
	output := []CalendarEventOutput{}
	for _, event := range events {
		if event.AllDay {
			// An all-day event is its calendar date, not a span of hours.
			for day := event.Start.UTC(); day.Before(event.End.UTC()); day = day.AddDate(0, 0, 1) {
				local := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, location)
				if !local.Before(start) && local.Before(end) {
					output = append(output, CalendarEventOutput{Date: local.Format("2006-01-02"), Title: event.Title, Location: event.Location,
						Calendar: event.Calendar, AllDay: true})
				}
			}
			continue
		}
		if !event.Start.Before(end) || !event.End.After(start) {
			continue
		}
		local := event.Start.In(location)
		output = append(output, CalendarEventOutput{Date: local.Format("2006-01-02"), Start: local.Format("15:04"), End: event.End.In(location).Format("15:04"),
			Title: event.Title, Location: event.Location, Calendar: event.Calendar, Fixed: event.Busy})
	}
	return output, nil
}

func addCalendarTools(server *mcp.Server, repository store.Repository, now func() time.Time) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "calendar",
		Description: "The owner's calendar events for a day or up to two weeks ahead. " + calendarNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input CalendarInput) (*mcp.CallToolResult, CalendarOutput, error) {
		days := input.Days
		if days == 0 {
			days = 1
		}
		if days < 1 || days > 14 {
			return nil, CalendarOutput{}, errors.New("days must be between 1 and 14")
		}
		location, err := ownerLocation(ctx, repository)
		if err != nil {
			return nil, CalendarOutput{}, err
		}
		first := now().In(location)
		if input.Date != "" {
			if first, err = time.ParseInLocation("2006-01-02", input.Date, location); err != nil {
				return nil, CalendarOutput{}, errors.New("date must be YYYY-MM-DD")
			}
		}
		events, err := calendarDays(ctx, repository, first, days, location)
		if err != nil {
			return nil, CalendarOutput{}, err
		}
		return nil, CalendarOutput{Events: events, Timezone: location.String(), Notes: calendarNotes}, nil
	})
}
