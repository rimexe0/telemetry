package mcpapi

import (
	"context"
	"encoding/json"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rimexe0/telemetry/internal/activity"
	"github.com/rimexe0/telemetry/internal/policy"
	"github.com/rimexe0/telemetry/internal/store"
)

// Owner decision 2026-09-30: the assistant shows the exact change, the owner
// approves it in the conversation, and the assistant applies it. Every
// applied change is logged with what it replaced and can be reverted.
const approvalRule = "Before calling this, show the owner exactly what will change and apply it only after they approve that change in this conversation. " +
	"Every applied change is logged with what it replaced; recent_changes lists them and revert_change undoes one."

const blocklistNotes = "Blocking follows the owner's labels: label \"distraction\" is blocked while a plan work block or execution session is open (or the daily-plan gate applies); \"essential\" is never blocked; other labels only name time. " +
	"Enforcement: Android apps on the phone (block screen, or suspended when root is enabled there); Mac apps are hidden by the Telemetry Desktop collector with a short notice; websites are covered by the Telemetry browser extension in Helium. " +
	"When closed_by_default is on (see blocking_schedule), distractions are closed at all times except the daily distraction times and openings. " +
	"When it is off, blocking happens only during a work or focus block or an execution session, or under the daily-plan gate."

type BlocklistInput struct{}

type BlocklistOutput struct {
	PolicyVersion int64    `json:"policy_version"`
	Blocked       []string `json:"blocked"`
	AlwaysAllowed []string `json:"always_allowed"`
	Notes         string   `json:"notes"`
}

type ChangeLabelsInput struct {
	Changes []activity.LabelChange `json:"changes" jsonschema:"label edits: action set or remove; kind app (Mac app name or Android package, exact) or site (domain, includes subdomains); match; label for set, such as distraction or work"`
	Reason  string                 `json:"reason" jsonschema:"the owner's request, in their words where possible"`
}

// ChangeOutput is store.AgentChange with its JSON fields as JSON values:
// json.RawMessage would be advertised as a byte array and fail validation.
type ChangeOutput struct {
	ID              int64      `json:"id"`
	Kind            string     `json:"kind"`
	Changes         any        `json:"changes"`
	Previous        any        `json:"previous"`
	Reason          string     `json:"reason"`
	PolicyVersionID *int64     `json:"policy_version_id,omitempty"`
	RevertOf        *int64     `json:"revert_of,omitempty"`
	AppliedAt       time.Time  `json:"applied_at"`
	RevertedAt      *time.Time `json:"reverted_at,omitempty"`
	RevertedBy      *int64     `json:"reverted_by,omitempty"`
}

func changeOutput(change store.AgentChange) (ChangeOutput, error) {
	output := ChangeOutput{ID: change.ID, Kind: change.Kind, Reason: change.Reason, PolicyVersionID: change.PolicyVersionID,
		RevertOf: change.RevertOf, AppliedAt: change.AppliedAt, RevertedAt: change.RevertedAt, RevertedBy: change.RevertedBy}
	for _, field := range []struct {
		raw    []byte
		target *any
	}{{change.Changes, &output.Changes}, {change.Previous, &output.Previous}} {
		if len(field.raw) == 0 {
			*field.target = []any{}
			continue
		}
		if err := json.Unmarshal(field.raw, field.target); err != nil {
			return output, err
		}
	}
	return output, nil
}

type AppliedChangeOutput struct {
	Change ChangeOutput `json:"change"`
	Notes  string       `json:"notes"`
}

type RecentChangesInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"number of changes, 1 through 100; default 10"`
}

type RecentChangesOutput struct {
	Changes []ChangeOutput `json:"changes"`
}

type RevertChangeInput struct {
	ID int64 `json:"id" jsonschema:"the change to undo, from recent_changes"`
}

func addChangeTools(server *mcp.Server, repository store.Repository) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "blocklist",
		Description: "What is blocked during work and what is never blocked, from the policy and the owner's labels. " + blocklistNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ BlocklistInput) (*mcp.CallToolResult, BlocklistOutput, error) {
		output := BlocklistOutput{Blocked: []string{}, AlwaysAllowed: []string{}, Notes: blocklistNotes}
		config := policy.Config{}
		current, err := repository.LatestPolicy(ctx)
		if err != nil {
			return nil, output, err
		}
		if current != nil {
			config, output.PolicyVersion = current.Config, current.ID
		}
		labels, err := repository.ListActivityLabels(ctx)
		if err != nil {
			return nil, output, err
		}
		config = policy.WithLabels(config, store.LabeledTargets(labels))
		describe := func(group policy.TargetGroup) []string {
			result := []string{}
			for _, value := range group.AndroidPackages {
				result = append(result, "phone app "+value)
			}
			for _, value := range group.MacApps {
				result = append(result, "Mac app "+value)
			}
			for _, value := range group.Domains {
				result = append(result, "site "+value)
			}
			return result
		}
		for _, group := range config.Distractions {
			output.Blocked = append(output.Blocked, describe(group)...)
		}
		for _, group := range config.Essentials {
			output.AlwaysAllowed = append(output.AlwaysAllowed, describe(group)...)
		}
		return nil, output, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "change_activity_labels",
		Description: "Set or remove the owner's labels for apps and sites. " + blocklistNotes + " " +
			"When the owner names a service (x, reddit, youtube), label its site and each of its apps (Android package, Mac app) together in one change; activity_summary lists the exact app names. " +
			"activity_summary totals time per label, and a site label wins over the browser's app label. " + approvalRule,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input ChangeLabelsInput) (*mcp.CallToolResult, AppliedChangeOutput, error) {
		change, err := repository.ApplyLabelChanges(ctx, input.Changes, input.Reason)
		if err != nil {
			return nil, AppliedChangeOutput{}, err
		}
		output, err := changeOutput(change)
		return nil, AppliedChangeOutput{Change: output, Notes: "Applied."}, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "recent_changes",
		Description: "Label, task and day plan changes applied through this assistant, newest first, each with what it replaced and whether it was reverted.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input RecentChangesInput) (*mcp.CallToolResult, RecentChangesOutput, error) {
		limit := input.Limit
		if limit == 0 {
			limit = 10
		}
		changes, err := repository.ListAgentChanges(ctx, limit)
		if err != nil {
			return nil, RecentChangesOutput{}, err
		}
		output := RecentChangesOutput{Changes: []ChangeOutput{}}
		for _, change := range changes {
			converted, err := changeOutput(change)
			if err != nil {
				return nil, RecentChangesOutput{}, err
			}
			output.Changes = append(output.Changes, converted)
		}
		return nil, output, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "revert_change",
		Description: "Undo one applied change: restores exactly the labels, tasks or day plan it touched to what they were before (a task it added is marked abandoned; tasks and plan versions are never deleted). The revert is itself a change that can be reverted. Confirm with the owner first.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input RevertChangeInput) (*mcp.CallToolResult, AppliedChangeOutput, error) {
		change, err := repository.RevertAgentChange(ctx, input.ID)
		if err != nil {
			return nil, AppliedChangeOutput{}, err
		}
		output, err := changeOutput(change)
		return nil, AppliedChangeOutput{Change: output, Notes: "Reverted."}, err
	})
}
