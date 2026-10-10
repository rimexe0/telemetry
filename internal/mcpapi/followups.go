package mcpapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rimexe0/telemetry/internal/store"
)

const followUpNotes = "A follow-up is something the owner must come back to at a time, such as hanging the laundry when the machine finishes. " +
	"The owner's memory is not reliable, so create one whenever they mention something that will need them later; do not expect them to remember it. " +
	"Once due, the phone shows it and keeps showing it until it is marked done (on the phone or here) or dropped. " +
	"nag_minutes makes a due follow-up alert again at that interval; set it only when the owner asks for it. Times are the owner's local time."

type FollowUpOutput struct {
	ID           int64      `json:"id"`
	Title        string     `json:"title"`
	Due          string     `json:"due" jsonschema:"local YYYY-MM-DD HH:MM"`
	DueAt        time.Time  `json:"due_at"`
	DueInMinutes int        `json:"due_in_minutes" jsonschema:"negative when overdue"`
	NagMinutes   *int       `json:"nag_minutes,omitempty"`
	State        string     `json:"state"`
	ClosedAt     *time.Time `json:"closed_at,omitempty"`
	ClosedBy     string     `json:"closed_by,omitempty"`
	// Place is set for a follow-up that comes due at a place; it is
	// waiting until the phone reaches it.
	Place           *FollowUpPlaceOutput `json:"place,omitempty"`
	WaitingForPlace bool                 `json:"waiting_for_place,omitempty"`
	TriggeredAt     *time.Time           `json:"triggered_at,omitempty"`
}

type FollowUpsInput struct {
	ClosedHours int `json:"closed_hours,omitempty" jsonschema:"also list follow-ups closed within this many hours, 0 through 720; default 24"`
}

type FollowUpsOutput struct {
	Now       time.Time        `json:"now"`
	Timezone  string           `json:"timezone"`
	FollowUps []FollowUpOutput `json:"follow_ups"`
	Notes     string           `json:"notes"`
}

type FollowUpChangeInput struct {
	Action     string  `json:"action" jsonschema:"add or update"`
	ID         *int64  `json:"id,omitempty" jsonschema:"follow-up id, required for update"`
	Title      *string `json:"title,omitempty" jsonschema:"what to do, in a few words"`
	InMinutes  *int    `json:"in_minutes,omitempty" jsonschema:"due this many minutes from now, 0 through 43200"`
	At         *string `json:"at,omitempty" jsonschema:"due at this local time: HH:MM (today, or tomorrow if that time has passed) or YYYY-MM-DD HH:MM"`
	NagMinutes *int    `json:"nag_minutes,omitempty" jsonschema:"alert again this often once due, 1 through 1440"`
	ClearNag   bool    `json:"clear_nag,omitempty"`
	State      *string `json:"state,omitempty" jsonschema:"open, done or dropped"`
	// Place makes it come due on arriving at or leaving a place.
	Place      *FollowUpPlaceInput `json:"place,omitempty" jsonschema:"come due on arriving at or leaving a place instead of at a time"`
	ClearPlace bool                `json:"clear_place,omitempty" jsonschema:"make it a plain timed follow-up again"`
}

type ChangeFollowUpsInput struct {
	Changes []FollowUpChangeInput `json:"changes"`
	Reason  string                `json:"reason" jsonschema:"the owner's words where possible"`
}

type ChangeFollowUpsOutput struct {
	Change    ChangeOutput     `json:"change"`
	FollowUps []FollowUpOutput `json:"follow_ups"`
}

func followUpOutput(item store.FollowUp, now time.Time, location *time.Location) FollowUpOutput {
	return FollowUpOutput{ID: item.ID, Title: item.Title, Due: item.DueAt.In(location).Format("2006-01-02 15:04"), DueAt: item.DueAt,
		DueInMinutes: int(item.DueAt.Sub(now).Minutes()), NagMinutes: item.NagMinutes, State: item.State, ClosedAt: item.ClosedAt, ClosedBy: item.ClosedBy,
		Place: placeOutput(item.Place), WaitingForPlace: item.Place != nil && item.TriggeredAt == nil && item.State == store.FollowUpOpen, TriggeredAt: item.TriggeredAt}
}

// followUpDue resolves "in N minutes" or a local clock time to an instant.
func followUpDue(input FollowUpChangeInput, now time.Time, location *time.Location) (*time.Time, error) {
	switch {
	case input.InMinutes != nil && input.At != nil:
		return nil, errors.New("give in_minutes or at, not both")
	case input.InMinutes != nil:
		if *input.InMinutes < 0 || *input.InMinutes > 43200 {
			return nil, errors.New("in_minutes must be between 0 and 43200")
		}
		due := now.Add(time.Duration(*input.InMinutes) * time.Minute).UTC()
		return &due, nil
	case input.At != nil:
		value := strings.TrimSpace(*input.At)
		if due, err := time.ParseInLocation("2006-01-02 15:04", value, location); err == nil {
			due = due.UTC()
			return &due, nil
		}
		clock, err := time.Parse("15:04", value)
		if err != nil {
			return nil, errors.New("at must be HH:MM or YYYY-MM-DD HH:MM")
		}
		local := now.In(location)
		due := time.Date(local.Year(), local.Month(), local.Day(), clock.Hour(), clock.Minute(), 0, 0, location)
		if due.Before(now) {
			due = due.AddDate(0, 0, 1)
		}
		due = due.UTC()
		return &due, nil
	}
	return nil, nil
}

func addFollowUpTools(server *mcp.Server, repository store.Repository, now func() time.Time) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "follow_ups",
		Description: "The owner's open follow-ups, soonest first, and the ones closed recently. " + followUpNotes + " " + placeFollowUpNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input FollowUpsInput) (*mcp.CallToolResult, FollowUpsOutput, error) {
		hours := input.ClosedHours
		if hours == 0 {
			hours = 24
		}
		if hours < 0 || hours > 720 {
			return nil, FollowUpsOutput{}, errors.New("closed_hours must be between 0 and 720")
		}
		location, err := ownerLocation(ctx, repository)
		if err != nil {
			return nil, FollowUpsOutput{}, err
		}
		current := now().UTC()
		items, err := repository.ListFollowUps(ctx, current.Add(-time.Duration(hours)*time.Hour))
		if err != nil {
			return nil, FollowUpsOutput{}, err
		}
		output := FollowUpsOutput{Now: current, Timezone: location.String(), FollowUps: []FollowUpOutput{}, Notes: followUpNotes}
		for _, item := range items {
			output.FollowUps = append(output.FollowUps, followUpOutput(item, current, location))
		}
		return nil, output, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "change_follow_ups",
		Description: "Add follow-ups or update them: retitle, move the time, set a place, mark done or dropped. " + followUpNotes + " " + placeFollowUpNotes +
			" The owner mentioning the thing in this conversation is the approval; tell them what you set and for when. Every change is logged; recent_changes lists it and revert_change undoes it.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input ChangeFollowUpsInput) (*mcp.CallToolResult, ChangeFollowUpsOutput, error) {
		location, err := ownerLocation(ctx, repository)
		if err != nil {
			return nil, ChangeFollowUpsOutput{}, err
		}
		current := now().UTC()
		changes := make([]store.FollowUpChange, 0, len(input.Changes))
		nearest := map[int]string{}
		for i, item := range input.Changes {
			due, err := followUpDue(item, current, location)
			if err != nil {
				return nil, ChangeFollowUpsOutput{}, fmt.Errorf("change %d: %w", i+1, err)
			}
			var place *store.FollowUpPlace
			if item.Place != nil {
				if item.ClearPlace {
					return nil, ChangeFollowUpsOutput{}, fmt.Errorf("change %d: give place or clear_place, not both", i+1)
				}
				if place, nearest[i], err = resolvePlace(ctx, repository, *item.Place, current, location); err != nil {
					return nil, ChangeFollowUpsOutput{}, fmt.Errorf("change %d: %w", i+1, err)
				}
				// A place alone may come due from now.
				if item.Action == "add" && due == nil {
					due = &current
				}
			}
			if item.Action == "add" && due == nil {
				return nil, ChangeFollowUpsOutput{}, fmt.Errorf("change %d: a new follow-up needs in_minutes, at or a place", i+1)
			}
			changes = append(changes, store.FollowUpChange{Action: strings.TrimSpace(item.Action), ID: item.ID, Title: item.Title, DueAt: due,
				NagMinutes: item.NagMinutes, ClearNag: item.ClearNag, State: item.State, Place: place, ClearPlace: item.ClearPlace})
		}
		change, written, err := repository.ApplyFollowUpChanges(ctx, changes, input.Reason)
		if err != nil {
			return nil, ChangeFollowUpsOutput{}, err
		}
		output := ChangeFollowUpsOutput{FollowUps: []FollowUpOutput{}}
		if output.Change, err = changeOutput(change); err != nil {
			return nil, ChangeFollowUpsOutput{}, err
		}
		for i, item := range written {
			entry := followUpOutput(item, current, location)
			if entry.Place != nil && i < len(input.Changes) {
				entry.Place.Nearest = nearest[i]
			}
			output.FollowUps = append(output.FollowUps, entry)
		}
		return nil, output, nil
	})
}
