package mcpapi

import (
	"context"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rimexe0/telemetry/internal/store"
)

const intentNotes = "These are what the system thinks the owner is trying to do, worked out every 10 minutes from everything the collectors saw " +
	"(Mac and phone apps, sites and window titles, and coding agent threads), without asking them. They are inferences, not things the owner said: " +
	"say \"it looks like\", and let the owner correct you. The owner does not use tasks or plans; this list is how the system knows their day. " +
	"Leisure and rest are listed plainly and are not failures. Times are the owner's local time."

type IntentsInput struct {
	Hours int `json:"hours,omitempty" jsonschema:"how far back, 1 through 168; default 24"`
}

type IntentEntry struct {
	ID         int64    `json:"id"`
	Title      string   `json:"title"`
	Kind       string   `json:"kind" jsonschema:"work, life, social, leisure, rest or unclear"`
	Status     string   `json:"status" jsonschema:"active, waiting (an agent works on it), paused, done or dropped"`
	NextStep   string   `json:"next_step,omitempty"`
	Evidence   []string `json:"evidence"`
	Confidence string   `json:"confidence,omitempty"`
	FirstSeen  string   `json:"first_seen" jsonschema:"local YYYY-MM-DD HH:MM"`
	LastSeen   string   `json:"last_seen" jsonschema:"local YYYY-MM-DD HH:MM"`
}

type IntentsOutput struct {
	Now      *IntentEntry  `json:"now,omitempty" jsonschema:"what the latest reading thinks they are on right now"`
	ReadAt   string        `json:"read_at,omitempty" jsonschema:"when the latest reading was made; empty before the first"`
	Note     string        `json:"note,omitempty" jsonschema:"the latest reading's one-line summary"`
	Intents  []IntentEntry `json:"intents" jsonschema:"most recently seen first"`
	Timezone string        `json:"timezone"`
	Notes    string        `json:"notes"`
}

func addIntentTools(server *mcp.Server, repository store.Repository, now func() time.Time) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "what_owner_is_doing",
		Description: "What the owner seems to be trying to do now and lately. " + intentNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input IntentsInput) (*mcp.CallToolResult, IntentsOutput, error) {
		hours := input.Hours
		if hours == 0 {
			hours = 24
		}
		if hours < 1 || hours > 168 {
			return nil, IntentsOutput{}, errors.New("hours must be between 1 and 168")
		}
		location, err := ownerLocation(ctx, repository)
		if err != nil {
			return nil, IntentsOutput{}, err
		}
		intents, err := repository.ListIntents(ctx, now().UTC().Add(-time.Duration(hours)*time.Hour), now().UTC())
		if err != nil {
			return nil, IntentsOutput{}, err
		}
		latest, err := repository.LatestIntentReading(ctx)
		if err != nil {
			return nil, IntentsOutput{}, err
		}
		local := func(at time.Time) string { return at.In(location).Format("2006-01-02 15:04") }
		output := IntentsOutput{Intents: []IntentEntry{}, Timezone: location.String(), Notes: intentNotes}
		for _, item := range intents {
			entry := IntentEntry{ID: item.ID, Title: item.Title, Kind: item.Kind, Status: item.Status, NextStep: item.NextStep, Evidence: item.Evidence,
				Confidence: item.Confidence, FirstSeen: local(item.FirstSeen), LastSeen: local(item.LastSeen)}
			output.Intents = append(output.Intents, entry)
			if latest != nil && latest.NowIntentID != nil && *latest.NowIntentID == item.ID {
				current := entry
				output.Now = &current
			}
		}
		if latest != nil {
			output.ReadAt, output.Note = local(latest.ReadAt), latest.Note
		}
		return nil, output, nil
	})
}
