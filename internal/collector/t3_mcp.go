package collector

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const t3SchemaVersionMCP = "t3-mcp-1"

// t3MCPProtocol is the MCP protocol version this client speaks.
const t3MCPProtocol = "2025-06-18"

// t3MCP reads T3 Code through its MCP server, the interface T3 offers to
// outside agents, with a read-only MCP credential issued by
// `t3 auth session issue --subject mcp-client --scope orchestration:read`.
// It records the same projects, threads and turns as the database reader,
// and also who sent the message that started each turn: the owner, or an
// agent.
//
// Message contents are never kept: message lists are asked for with T3's
// smallest text limit (one character), which is dropped unread; only each
// message's sender, time and turn are used.
type t3MCP struct {
	origin  string
	token   string
	client  *http.Client
	session string
	id      int
	threads map[string]*t3MCPThread
}

// t3MCPThread is what was last read of one thread; it is read again only
// when T3 reports a newer update.
type t3MCPThread struct {
	updatedAt string
	// after is the position to continue the message list from.
	after   *int
	senders map[string]string // run id -> "user" or "agent"
	// ownerAt is the time of the owner's latest message.
	ownerAt string
	row     t3Row
	turns   map[string]t3Row
}

func newT3MCP(origin, token string) *t3MCP {
	return &t3MCP{origin: origin, token: token, client: &http.Client{Timeout: 30 * time.Second}, threads: map[string]*t3MCPThread{}}
}

// errT3NotRunning is returned when nothing answers at T3's address.
var errT3NotRunning = errors.New("T3 is not running")

// post sends one JSON-RPC message and returns the answer to it, if it has
// an id. T3 answers either with JSON or with a short event stream.
func (m *t3MCP) post(ctx context.Context, message map[string]any) (json.RawMessage, error) {
	body, _ := json.Marshal(message)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, m.origin+"/mcp", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+m.token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", t3MCPProtocol)
	if m.session != "" {
		request.Header.Set("Mcp-Session-Id", m.session)
	}
	response, err := m.client.Do(request)
	if err != nil {
		var refused *net.OpError
		if errors.As(err, &refused) {
			return nil, errT3NotRunning
		}
		return nil, err
	}
	defer response.Body.Close()
	if session := response.Header.Get("Mcp-Session-Id"); session != "" {
		m.session = session
	}
	switch {
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden:
		return nil, errors.New("T3 refused the MCP credential (issue a new one with `t3 auth session issue --subject mcp-client --scope orchestration:read`)")
	case response.StatusCode == http.StatusNotFound && m.session != "":
		m.session = ""
		return nil, errT3SessionGone
	case response.StatusCode == http.StatusAccepted:
		return nil, nil
	case response.StatusCode != http.StatusOK:
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 300))
		return nil, fmt.Errorf("T3's MCP server answered %d: %s", response.StatusCode, detail)
	}
	id, _ := message["id"].(int)
	if strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 64<<10), 16<<20)
		for scanner.Scan() {
			data, found := strings.CutPrefix(scanner.Text(), "data:")
			if !found {
				continue
			}
			if answer, ok := t3MCPAnswer([]byte(strings.TrimSpace(data)), id); ok {
				return answer, nil
			}
		}
		return nil, errors.New("T3's MCP answer ended without a result")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if answer, ok := t3MCPAnswer(data, id); ok {
		return answer, nil
	}
	return nil, errors.New("T3's MCP answer has no result")
}

var errT3SessionGone = errors.New("T3 MCP session expired")

// t3MCPAnswer returns the result of the JSON-RPC answer with this id.
func t3MCPAnswer(data []byte, id int) (json.RawMessage, bool) {
	var answer struct {
		ID     *int            `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &answer) != nil || answer.ID == nil || *answer.ID != id {
		return nil, false
	}
	if answer.Error != nil {
		message, _ := json.Marshal(map[string]any{"isError": true, "content": []map[string]string{{"type": "text", "text": answer.Error.Message}}})
		return message, true
	}
	return answer.Result, true
}

func (m *t3MCP) open(ctx context.Context) error {
	m.session = ""
	m.id++
	if _, err := m.post(ctx, map[string]any{"jsonrpc": "2.0", "id": m.id, "method": "initialize", "params": map[string]any{
		"protocolVersion": t3MCPProtocol, "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "telemetry-collector", "version": "1"},
	}}); err != nil {
		return err
	}
	_, err := m.post(ctx, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	return err
}

// call runs one tool and decodes its structured result into out.
func (m *t3MCP) call(ctx context.Context, tool string, arguments map[string]any, out any) error {
	for attempt := 0; ; attempt++ {
		if m.session == "" {
			if err := m.open(ctx); err != nil {
				return err
			}
		}
		m.id++
		raw, err := m.post(ctx, map[string]any{"jsonrpc": "2.0", "id": m.id, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": arguments}})
		if errors.Is(err, errT3SessionGone) && attempt == 0 {
			continue
		}
		if err != nil {
			return err
		}
		var result struct {
			IsError    bool            `json:"isError"`
			Structured json.RawMessage `json:"structuredContent"`
			Content    []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return fmt.Errorf("%s: unreadable result: %w", tool, err)
		}
		if result.IsError {
			detail := ""
			if len(result.Content) > 0 {
				detail = result.Content[0].Text
			}
			if len(detail) > 300 {
				detail = detail[:300]
			}
			return fmt.Errorf("%s failed: %s", tool, detail)
		}
		if len(result.Structured) == 0 {
			return fmt.Errorf("%s returned no structured result (T3 may have changed its tools in an update)", tool)
		}
		if err := json.Unmarshal(result.Structured, out); err != nil {
			return fmt.Errorf("%s result changed shape (T3 may have changed its tools in an update): %w", tool, err)
		}
		return nil
	}
}

type t3MCPProject struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Root      string  `json:"workspaceRoot"`
	CreatedAt string  `json:"createdAt"`
	UpdatedAt string  `json:"updatedAt"`
	DeletedAt *string `json:"deletedAt"`
}

type t3MCPThreadSummary struct {
	ThreadID  string `json:"threadId"`
	UpdatedAt string `json:"updatedAt"`
}

type t3MCPThreadDetail struct {
	ThreadID       string  `json:"threadId"`
	ProjectID      *string `json:"projectId"`
	Title          string  `json:"title"`
	CreatedBy      string  `json:"createdBy"`
	CreationSource string  `json:"creationSource"`
	Status         string  `json:"status"`
	ActiveRunID    *string `json:"activeRunId"`
	Provider       string  `json:"providerInstanceId"`
	Model          string  `json:"model"`
	RuntimeMode    string  `json:"runtimeMode"`
	Interaction    string  `json:"interactionMode"`
	Branch         *string `json:"branch"`
	Worktree       *string `json:"worktreePath"`
	ParentThreadID *string `json:"parentThreadId"`
	Pending        int     `json:"pendingRequestCount"`
	Archived       bool    `json:"archived"`
	Settled        bool    `json:"settled"`
	CreatedAt      string  `json:"createdAt"`
	UpdatedAt      string  `json:"updatedAt"`
}

type t3MCPRun struct {
	RunID       string  `json:"runId"`
	Status      string  `json:"status"`
	RequestedAt string  `json:"requestedAt"`
	StartedAt   *string `json:"startedAt"`
	CompletedAt *string `json:"completedAt"`
}

type t3MCPItem struct {
	RunID     *string `json:"runId"`
	Type      string  `json:"type"`
	CreatedBy *string `json:"createdBy"`
	UpdatedAt string  `json:"updatedAt"`
}

// read returns every project, thread and recent turn. Threads T3 reports
// unchanged since the last read are not read again.
func (m *t3MCP) read(ctx context.Context) ([]t3Row, error) {
	rows := []t3Row{}
	for cursor := 0; ; {
		var page struct {
			Projects   []t3MCPProject `json:"projects"`
			NextCursor *int           `json:"nextCursor"`
		}
		if err := m.call(ctx, "t3_project_list", map[string]any{"cursor": cursor, "limit": 100}, &page); err != nil {
			return nil, err
		}
		for _, project := range page.Projects {
			created, err := time.Parse(time.RFC3339Nano, project.CreatedAt)
			if err != nil {
				return nil, fmt.Errorf("project %s has no creation time", project.ID)
			}
			rows = append(rows, t3Row{kind: "t3.project", key: "project:" + project.ID, occurred: created.UTC(), payload: map[string]any{
				"project_id": project.ID, "title": project.Title, "workspace_root": project.Root,
				"created_at": project.CreatedAt, "updated_at": project.UpdatedAt, "deleted_at": optionalText(project.DeletedAt),
			}})
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	for cursor := 0; ; {
		var page struct {
			Threads    []t3MCPThreadSummary `json:"threads"`
			NextCursor *int                 `json:"nextCursor"`
		}
		if err := m.call(ctx, "t3_thread_list", map[string]any{"allProjects": true, "includeSubagents": true, "limit": 100, "cursor": cursor}, &page); err != nil {
			return nil, err
		}
		for _, summary := range page.Threads {
			known := m.threads[summary.ThreadID]
			if known != nil && known.updatedAt == summary.UpdatedAt {
				continue
			}
			if known == nil {
				known = &t3MCPThread{senders: map[string]string{}, turns: map[string]t3Row{}}
				m.threads[summary.ThreadID] = known
			}
			if err := m.readThread(ctx, summary.ThreadID, known); err != nil {
				return nil, err
			}
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	for _, thread := range m.threads {
		rows = append(rows, thread.row)
		for _, turn := range thread.turns {
			rows = append(rows, turn)
		}
	}
	return rows, nil
}

// readThread reads one thread's state, its recent runs and the senders of
// its messages since the last read.
func (m *t3MCP) readThread(ctx context.Context, id string, known *t3MCPThread) error {
	var detail t3MCPThreadDetail
	runs := []t3MCPRun{}
	for first := true; ; first = false {
		arguments := map[string]any{"threadId": id, "view": "messages", "limit": 100, "maxCharsPerItem": 1, "runLimit": 50}
		if known.after != nil {
			arguments["afterPosition"] = *known.after
		}
		var page struct {
			Thread       t3MCPThreadDetail `json:"thread"`
			RecentRuns   []t3MCPRun        `json:"recentRuns"`
			Items        []t3MCPItem       `json:"items"`
			NextPosition *int              `json:"nextPosition"`
			HasMore      bool              `json:"hasMore"`
		}
		if err := m.call(ctx, "t3_thread_read", arguments, &page); err != nil {
			return err
		}
		if first {
			detail, runs = page.Thread, page.RecentRuns
		}
		for _, item := range page.Items {
			if item.Type != "user_message" || item.RunID == nil || item.CreatedBy == nil {
				continue
			}
			known.senders[*item.RunID] = *item.CreatedBy
			if *item.CreatedBy == "user" && item.UpdatedAt > known.ownerAt {
				known.ownerAt = item.UpdatedAt
			}
		}
		if page.NextPosition != nil {
			position := *page.NextPosition
			known.after = &position
		}
		if !page.HasMore || len(page.Items) == 0 {
			break
		}
	}
	if detail.ThreadID == "" {
		return fmt.Errorf("thread %s: T3 returned no thread (its tools may have changed in an update)", id)
	}
	created, err := time.Parse(time.RFC3339Nano, detail.CreatedAt)
	if err != nil {
		return fmt.Errorf("thread %s has no creation time", id)
	}
	var archived any
	if detail.Archived {
		archived = detail.UpdatedAt
	}
	known.row = t3Row{kind: "t3.thread", key: "thread:" + id, occurred: created.UTC(), payload: map[string]any{
		"thread_id": id, "project_id": optionalText(detail.ProjectID), "title": detail.Title, "branch": optionalText(detail.Branch),
		"worktree_path": optionalText(detail.Worktree), "created_at": detail.CreatedAt, "updated_at": detail.UpdatedAt,
		"archived_at": archived, "deleted_at": nil, "latest_user_message_at": emptyAsNil(known.ownerAt),
		"pending_approval_count": 0, "pending_user_input_count": detail.Pending,
		"runtime_mode": detail.RuntimeMode, "interaction_mode": detail.Interaction, "provider_instance": detail.Provider, "model": detail.Model,
		"session_status": detail.Status, "session_provider": detail.Provider, "active_turn_id": optionalText(detail.ActiveRunID),
		"session_updated_at": detail.UpdatedAt, "created_by": detail.CreatedBy, "creation_source": detail.CreationSource,
		"parent_thread_id": optionalText(detail.ParentThreadID), "settled": detail.Settled,
	}}
	for _, run := range runs {
		state, ok := t3RunStates[run.Status]
		if !ok {
			return fmt.Errorf("run %s has unknown status %q", run.RunID, run.Status)
		}
		requested, err := time.Parse(time.RFC3339Nano, run.RequestedAt)
		if err != nil {
			return fmt.Errorf("run %s has no request time", run.RunID)
		}
		var ended *time.Time
		if run.CompletedAt != nil {
			if at, err := time.Parse(time.RFC3339Nano, *run.CompletedAt); err == nil {
				at = at.UTC()
				ended = &at
			}
		}
		known.turns[run.RunID] = t3Row{kind: "t3.turn", key: "turn:" + id + ":" + run.RunID, occurred: requested.UTC(), ended: ended, payload: map[string]any{
			"thread_id": id, "turn_id": run.RunID, "state": state, "run_status": run.Status,
			"requested_at": run.RequestedAt, "started_at": optionalText(run.StartedAt), "completed_at": optionalText(run.CompletedAt),
			"requested_by": emptyAsNil(known.senders[run.RunID]),
		}}
	}
	known.updatedAt = detail.UpdatedAt
	return nil
}

func optionalText(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func emptyAsNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// t3Origin reads where the local T3 server listens.
func t3Origin(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "server-runtime.json"))
	if err != nil {
		return "", err
	}
	var runtime struct {
		Port int `json:"port"`
	}
	if err := json.Unmarshal(data, &runtime); err != nil || runtime.Port == 0 {
		return "", fmt.Errorf("T3 server-runtime.json has no port")
	}
	return "http://127.0.0.1:" + strconv.Itoa(runtime.Port), nil
}
