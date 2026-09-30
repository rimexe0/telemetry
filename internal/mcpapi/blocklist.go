package mcpapi

import (
	"context"

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

const blocklistNotes = "Blocking works on the Android phone only, per app (package name), and only while a plan work block or execution session is open (or the daily-plan gate applies). " +
	"Websites and Mac apps cannot be blocked yet; for those, label the site so its time is tracked."

type BlocklistInput struct{}

type BlocklistOutput struct {
	PolicyVersion int64    `json:"policy_version"`
	Blocked       []string `json:"blocked_android_packages"`
	AlwaysAllowed []string `json:"always_allowed_android_packages"`
	Notes         string   `json:"notes"`
}

type ChangeBlocklistInput struct {
	Changes []policy.BlocklistChange `json:"changes" jsonschema:"one entry per Android app: action block, always_allow or unlist; android_package such as com.twitter.android (activity_summary lists the phone's package names)"`
	Reason  string                   `json:"reason" jsonschema:"the owner's request, in their words where possible"`
}

type ChangeLabelsInput struct {
	Changes []activity.LabelChange `json:"changes" jsonschema:"label edits: action set or remove; kind app (Mac app name or Android package, exact) or site (domain, includes subdomains); match; label for set, such as distraction or work"`
	Reason  string                 `json:"reason" jsonschema:"the owner's request, in their words where possible"`
}

type AppliedChangeOutput struct {
	Change store.AgentChange `json:"change"`
	Notes  string            `json:"notes"`
}

type RecentChangesInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"number of changes, 1 through 100; default 10"`
}

type RecentChangesOutput struct {
	Changes []store.AgentChange `json:"changes"`
}

type RevertChangeInput struct {
	ID int64 `json:"id" jsonschema:"the change to undo, from recent_changes"`
}

func addChangeTools(server *mcp.Server, repository store.Repository) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "blocklist",
		Description: "The phone apps currently blocked or always allowed. " + blocklistNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ BlocklistInput) (*mcp.CallToolResult, BlocklistOutput, error) {
		output := BlocklistOutput{Blocked: []string{}, AlwaysAllowed: []string{}, Notes: blocklistNotes}
		current, err := repository.LatestPolicy(ctx)
		if err != nil || current == nil {
			return nil, output, err
		}
		output.PolicyVersion = current.ID
		for _, group := range current.Config.Distractions {
			output.Blocked = append(output.Blocked, group.AndroidPackages...)
		}
		for _, group := range current.Config.Essentials {
			output.AlwaysAllowed = append(output.AlwaysAllowed, group.AndroidPackages...)
		}
		return nil, output, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "change_blocklist",
		Description: "Block, always allow or unlist phone apps. " + blocklistNotes + " " + approvalRule,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input ChangeBlocklistInput) (*mcp.CallToolResult, AppliedChangeOutput, error) {
		change, err := repository.ApplyBlocklistChange(ctx, input.Changes, input.Reason)
		if err != nil {
			return nil, AppliedChangeOutput{}, err
		}
		return nil, AppliedChangeOutput{Change: change, Notes: "Applied. The phone picks it up within a minute."}, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "change_activity_labels",
		Description: "Set or remove the owner's labels for apps and sites (for example distraction or work); activity_summary totals time per label, and a site label wins over the browser's app label. " + approvalRule,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input ChangeLabelsInput) (*mcp.CallToolResult, AppliedChangeOutput, error) {
		change, err := repository.ApplyLabelChanges(ctx, input.Changes, input.Reason)
		if err != nil {
			return nil, AppliedChangeOutput{}, err
		}
		return nil, AppliedChangeOutput{Change: change, Notes: "Applied."}, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "recent_changes",
		Description: "Blocklist and label changes applied through this assistant, newest first, each with what it replaced and whether it was reverted.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input RecentChangesInput) (*mcp.CallToolResult, RecentChangesOutput, error) {
		limit := input.Limit
		if limit == 0 {
			limit = 10
		}
		changes, err := repository.ListAgentChanges(ctx, limit)
		return nil, RecentChangesOutput{Changes: changes}, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "revert_change",
		Description: "Undo one applied change: restores exactly the apps or labels it touched to what they were before. The revert is itself a change that can be reverted. Confirm with the owner first.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input RevertChangeInput) (*mcp.CallToolResult, AppliedChangeOutput, error) {
		change, err := repository.RevertAgentChange(ctx, input.ID)
		if err != nil {
			return nil, AppliedChangeOutput{}, err
		}
		return nil, AppliedChangeOutput{Change: change, Notes: "Reverted."}, nil
	})
}
