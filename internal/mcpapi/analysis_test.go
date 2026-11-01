package mcpapi

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rimexe0/telemetry/internal/activity"
)

type healthRepository struct {
	toolsRepository
	health []activity.Record
}

func (r *healthRepository) HealthRecords(context.Context, time.Time, time.Time) ([]activity.Record, error) {
	return r.health, nil
}

func TestAnalysisToolsGiveDailyRowsWithinTheirLimits(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	repository := &healthRepository{health: []activity.Record{{Kind: "health_connect.steps", Start: now,
		Payload: json.RawMessage(`{"count":1200,"start_time":"2026-11-01T08:00:00Z","metadata":{"data_origin":"com.sec.android.app.shealth"}}`)}}}
	session := connect(t, repository, now)
	call := func(name string, arguments map[string]any) *mcp.CallToolResult {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: arguments})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return result
	}
	daily := call("activity_daily", map[string]any{"from_date": "2026-10-30", "to_date": "2026-11-01"})
	encoded, _ := json.Marshal(daily.StructuredContent)
	var rows ActivityDailyOutput
	json.Unmarshal(encoded, &rows)
	if daily.IsError || len(rows.Days) != 3 || rows.Days[2].Date != "2026-11-01" {
		t.Fatalf("activity_daily = %s", encoded)
	}
	health := call("health_daily", map[string]any{"to_date": "2026-11-01"})
	encoded, _ = json.Marshal(health.StructuredContent)
	var days HealthDailyOutput
	json.Unmarshal(encoded, &days)
	if health.IsError || len(days.Days) != 7 || days.Days[6].Steps == nil || *days.Days[6].Steps != 1200 {
		t.Fatalf("health_daily = %s", encoded)
	}
	if !call("activity_daily", map[string]any{"from_date": "2026-08-01", "to_date": "2026-11-01"}).IsError {
		t.Fatal("more than 62 days must be refused")
	}
	if !call("drift_episodes", map[string]any{"to_date": "2026-11-05"}).IsError {
		t.Fatal("a range in the future must be refused")
	}
	if call("drift_episodes", map[string]any{}).IsError {
		t.Fatal("drift_episodes with defaults failed")
	}
}
