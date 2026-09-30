package mcpapi

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rimexe0/telemetry/internal/policy"
	"github.com/rimexe0/telemetry/internal/store"
)

const blocklistNotes = "Blocking works on the Android phone only, per app (package name), and only while a plan work block or execution session is open (or the daily-plan gate applies). " +
	"Websites and Mac apps cannot be blocked yet; for those, propose an activity label instead so the time is tracked. " +
	"Agents can only propose changes; nothing applies until the owner accepts in the Telemetry panel (Plan tab, Blocked apps)."

type BlocklistInput struct{}

type BlocklistOutput struct {
	PolicyVersion int64                  `json:"policy_version"`
	Blocked       []string               `json:"blocked_android_packages"`
	AlwaysAllowed []string               `json:"always_allowed_android_packages"`
	Pending       []store.PolicyProposal `json:"pending_proposals"`
	Notes         string                 `json:"notes"`
}

type ProposeBlocklistInput struct {
	Changes []policy.BlocklistChange `json:"changes" jsonschema:"one entry per Android app: action block, always_allow or unlist; android_package such as com.twitter.android (see activity_summary phone apps for exact names)"`
	Reason  string                   `json:"reason" jsonschema:"why, in the owner's words where possible"`
}

type ProposeBlocklistOutput struct {
	Proposal store.PolicyProposal `json:"proposal"`
	Notes    string               `json:"notes"`
}

func addBlocklistTools(server *mcp.Server, repository store.Repository) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "blocklist",
		Description: "The phone apps currently blocked or always allowed, and blocklist proposals waiting for the owner. " + blocklistNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ BlocklistInput) (*mcp.CallToolResult, BlocklistOutput, error) {
		output := BlocklistOutput{Blocked: []string{}, AlwaysAllowed: []string{}, Notes: blocklistNotes}
		current, err := repository.LatestPolicy(ctx)
		if err != nil {
			return nil, output, err
		}
		if current != nil {
			output.PolicyVersion = current.ID
			for _, group := range current.Config.Distractions {
				output.Blocked = append(output.Blocked, group.AndroidPackages...)
			}
			for _, group := range current.Config.Essentials {
				output.AlwaysAllowed = append(output.AlwaysAllowed, group.AndroidPackages...)
			}
		}
		if output.Pending, err = repository.ListPolicyProposals(ctx, "pending", 50); err != nil {
			return nil, output, err
		}
		return nil, output, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "propose_blocklist_change",
		Description: "Propose blocking or always allowing phone apps, for example when the owner names distractions. " + blocklistNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input ProposeBlocklistInput) (*mcp.CallToolResult, ProposeBlocklistOutput, error) {
		proposal, err := repository.CreatePolicyProposal(ctx, input.Changes, input.Reason)
		if err != nil {
			return nil, ProposeBlocklistOutput{}, err
		}
		return nil, ProposeBlocklistOutput{Proposal: proposal, Notes: "Pending. Ask the owner to accept it in the Telemetry panel: Plan tab, Blocked apps."}, nil
	})
}
