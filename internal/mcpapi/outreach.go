package mcpapi

import (
	"context"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rimexe0/telemetry/internal/store"
)

const outreachNotes = "The system writes to the owner in your name without being asked, for two things only: a morning message when the records show they woke, " +
	"and a check-in after about 45 minutes at their devices on none of the day's tasks with no agent to wait for, at most one message an hour. " +
	"You did not see these being sent. When the owner's message reads like an answer to something you did not say, it is probably an answer to one of these. " +
	"An entry with no message means the system looked and stayed quiet. Times are the owner's local time."

type OutreachLogInput struct {
	Hours int `json:"hours,omitempty" jsonschema:"how far back, 1 through 168; default 24"`
}

type OutreachEntry struct {
	ID        int64  `json:"id"`
	At        string `json:"at" jsonschema:"local YYYY-MM-DD HH:MM"`
	Kind      string `json:"kind" jsonschema:"morning or off_plan"`
	Message   string `json:"message" jsonschema:"what was sent; empty when the system stayed quiet"`
	Delivered bool   `json:"delivered"`
	Error     string `json:"error,omitempty"`
	Facts     string `json:"facts,omitempty" jsonschema:"what the message was written from"`
}

type OutreachLogOutput struct {
	Messages []OutreachEntry `json:"messages" jsonschema:"newest first"`
	Timezone string          `json:"timezone"`
	Notes    string          `json:"notes"`
}

func addOutreachTools(server *mcp.Server, repository store.Repository, now func() time.Time) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "outreach_log",
		Description: "Messages the system sent the owner in your name. " + outreachNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input OutreachLogInput) (*mcp.CallToolResult, OutreachLogOutput, error) {
		hours := input.Hours
		if hours == 0 {
			hours = 24
		}
		if hours < 1 || hours > 168 {
			return nil, OutreachLogOutput{}, errors.New("hours must be between 1 and 168")
		}
		location, err := ownerLocation(ctx, repository)
		if err != nil {
			return nil, OutreachLogOutput{}, err
		}
		entries, err := repository.ListOutreach(ctx, now().UTC().Add(-time.Duration(hours)*time.Hour))
		if err != nil {
			return nil, OutreachLogOutput{}, err
		}
		output := OutreachLogOutput{Messages: []OutreachEntry{}, Timezone: location.String(), Notes: outreachNotes}
		for _, entry := range entries {
			output.Messages = append(output.Messages, OutreachEntry{ID: entry.ID, At: entry.CreatedAt.In(location).Format("2006-01-02 15:04"), Kind: entry.Kind,
				Message: entry.Message, Delivered: entry.Delivered, Error: entry.Error, Facts: entry.Facts})
		}
		return nil, output, nil
	})
}
