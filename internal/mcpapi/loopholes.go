package mcpapi

import (
	"context"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rimexe0/telemetry/internal/store"
)

const loopholeNotes = "Loophole cases (owner request 2026-11-22): every accepted change that loosens blocking, from the panel or an agent, is recorded; changes close together form one case. " +
	"Ten minutes after the last one, the server hands the case to a coding agent in T3 Code, which investigates what happened with the records, writes a report, " +
	"and, when the system let something through that the owner's rules should have stopped, opens a pull request that fixes it. " +
	"The owner is messaged on Telegram with the report once, then each hour; a fix merges five hours after the report unless the owner answers first. " +
	"The owner can't control themselves at night, so loosening late in the evening deserves the most care. Report facts, never shame them. " +
	"A fix is code only: changes to the owner's labels, schedule or other rules are proposed in the report and wait for the owner's yes, as always. Times are UTC."

type LoopholeCasesInput struct {
	CaseID   int64 `json:"case_id,omitempty" jsonschema:"one case; omitted lists cases"`
	OpenOnly bool  `json:"open_only,omitempty" jsonschema:"only cases not closed"`
	Limit    int   `json:"limit,omitempty" jsonschema:"1 through 100; default 20"`
}

type LoopholeCaseOutput struct {
	store.LoopholeCase
	Status string `json:"status" jsonschema:"waiting, investigating, reported, answered or closed"`
}

type LoopholeCasesOutput struct {
	Cases []LoopholeCaseOutput `json:"cases" jsonschema:"newest first"`
	Notes string               `json:"notes"`
}

type ReportLoopholeInput struct {
	CaseID    int64  `json:"case_id"`
	Report    string `json:"report" jsonschema:"the full report: what happened, with times and evidence; whether it was a loophole, a mistake or a real need; what the fix does; any rule changes proposed for the owner to approve"`
	Message   string `json:"message" jsonschema:"what the owner reads on Telegram: at most three short plain sentences, no headings, no scolding; the server adds the links and the merge time"`
	PRURL     string `json:"pr_url,omitempty" jsonschema:"the fix's pull request; omit when there is nothing to fix in code"`
	ThreadURL string `json:"thread_url,omitempty" jsonschema:"the T3 Code thread with the investigation, when it has an https link"`
}

type AnswerLoopholeInput struct {
	CaseID int64  `json:"case_id"`
	Answer string `json:"answer" jsonschema:"what the owner said, in their words"`
}

type CloseLoopholeInput struct {
	CaseID  int64  `json:"case_id"`
	Outcome string `json:"outcome" jsonschema:"merged, dropped, nothing_found or changed"`
	Note    string `json:"note,omitempty"`
}

type LoopholeWebhookInput struct {
	URL string `json:"url" jsonschema:"https URL of the T3 Code webhook task; empty stops handing cases over"`
}

func caseOutput(c store.LoopholeCase) LoopholeCaseOutput {
	return LoopholeCaseOutput{LoopholeCase: c, Status: c.Status()}
}

func addLoopholeTools(server *mcp.Server, loopholes store.LoopholeStore) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "loophole_cases",
		Description: "Loophole cases and where each stands. " + loopholeNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input LoopholeCasesInput) (*mcp.CallToolResult, LoopholeCasesOutput, error) {
		output := LoopholeCasesOutput{Cases: []LoopholeCaseOutput{}, Notes: loopholeNotes}
		if input.CaseID != 0 {
			c, err := loopholes.LoopholeCase(ctx, input.CaseID)
			if errors.Is(err, store.ErrNotFound) {
				return nil, output, errors.New("no such case")
			}
			if err != nil {
				return nil, output, err
			}
			output.Cases = append(output.Cases, caseOutput(c))
			return nil, output, nil
		}
		cases, err := loopholes.LoopholeCases(ctx, input.OpenOnly, input.Limit)
		if err != nil {
			return nil, output, err
		}
		for _, c := range cases {
			output.Cases = append(output.Cases, caseOutput(c))
		}
		return nil, output, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "report_loophole",
		Description: "For the coding agent investigating a loophole case: store its report. With pr_url, the fix merges five hours from now unless the owner answers; " +
			"the owner is told on Telegram now and each hour until then. Report once, when the investigation and any pull request are done. " + loopholeNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input ReportLoopholeInput) (*mcp.CallToolResult, LoopholeCaseOutput, error) {
		c, err := loopholes.ReportLoophole(ctx, input.CaseID, input.Report, input.Message, input.PRURL, input.ThreadURL)
		if err != nil {
			return nil, LoopholeCaseOutput{}, err
		}
		return nil, caseOutput(c), nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "answer_loophole",
		Description: "Record the owner's answer to a loophole report they were messaged about (outreach kind loophole). Call it whenever the owner replies to one, in their words: " +
			"it stops the hourly messages and the merge without an answer, and the coding agent then does what they said. Never answer for them. " + loopholeNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input AnswerLoopholeInput) (*mcp.CallToolResult, LoopholeCaseOutput, error) {
		c, err := loopholes.AnswerLoophole(ctx, input.CaseID, input.Answer)
		if err != nil {
			return nil, LoopholeCaseOutput{}, err
		}
		return nil, caseOutput(c), nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "close_loophole",
		Description: "For the coding agent: record how a loophole case ended: merged (the fix is on main), dropped (the owner said no, or the pull request was closed), " +
			"nothing_found (no loophole: a mistake fixed, or a real need), or changed (the fix was changed as the owner asked and merged). " + loopholeNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input CloseLoopholeInput) (*mcp.CallToolResult, LoopholeCaseOutput, error) {
		c, err := loopholes.CloseLoophole(ctx, input.CaseID, input.Outcome, input.Note)
		if err != nil {
			return nil, LoopholeCaseOutput{}, err
		}
		return nil, caseOutput(c), nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "set_loophole_webhook",
		Description: "Set the T3 Code webhook that new loophole cases are handed to. Only when the owner asks to set it up or move it. " + loopholeNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input LoopholeWebhookInput) (*mcp.CallToolResult, map[string]any, error) {
		if err := loopholes.SetLoopholeWebhook(ctx, input.URL); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"saved": true, "at": time.Now().UTC()}, nil
	})
}
