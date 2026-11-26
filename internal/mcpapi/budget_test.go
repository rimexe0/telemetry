package mcpapi

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rimexe0/telemetry/internal/activity"
	"github.com/rimexe0/telemetry/internal/budget"
	"github.com/rimexe0/telemetry/internal/policy"
	"github.com/rimexe0/telemetry/internal/store"
)

// budgetRepository answers the budget tools from memory.
type budgetRepository struct {
	planningRepository
	config  *policy.Config
	habits  []budget.Habit
	events  []budget.HabitEvent
	changes []store.HabitChange
}

func (r *budgetRepository) LatestPolicy(context.Context) (*store.PolicyVersion, error) {
	if r.config == nil {
		return nil, nil
	}
	return &store.PolicyVersion{ID: 1, Config: *r.config}, nil
}

func (r *budgetRepository) ListHabits(context.Context) ([]budget.Habit, error) { return r.habits, nil }
func (r *budgetRepository) ApplyHabitChanges(_ context.Context, changes []store.HabitChange, reason string) (store.AgentChange, []budget.Habit, error) {
	r.changes = append(r.changes, changes...)
	written := []budget.Habit{}
	for _, change := range changes {
		if change.Action == "add" {
			habit := budget.Habit{ID: int64(len(r.habits) + 1), Name: *change.Name, Weekdays: budget.Weekdays{}, CreatedAt: time.Now().UTC().Add(-48 * time.Hour)}
			if change.Weekdays != nil {
				habit.Weekdays = *change.Weekdays
			}
			r.habits = append(r.habits, habit)
			written = append(written, habit)
		}
	}
	encoded, _ := json.Marshal(changes)
	return store.AgentChange{ID: 21, Kind: store.AgentChangeHabits, Changes: encoded, Previous: json.RawMessage(`[]`), Reason: reason}, written, nil
}
func (r *budgetRepository) HabitEvents(context.Context, string) ([]budget.HabitEvent, error) {
	return r.events, nil
}
func (r *budgetRepository) RecordHabitEvent(_ context.Context, event budget.HabitEvent) (budget.HabitEvent, error) {
	event.ID = int64(len(r.events) + 1)
	r.events = append(r.events, event)
	return event, nil
}
func (r *budgetRepository) BudgetDoors(context.Context, time.Time) ([]budget.Door, error) {
	return []budget.Door{}, nil
}
func (r *budgetRepository) CreateDoor(_ context.Context, door budget.Door) (budget.Door, error) {
	return door, nil
}
func (r *budgetRepository) CancelDoor(context.Context, int64, time.Time) (budget.Door, error) {
	return budget.Door{}, store.ErrNotFound
}
func (r *budgetRepository) RecordDoorBack(_ context.Context, back budget.DoorBack) (budget.DoorBack, error) {
	return back, nil
}
func (r *budgetRepository) DoorBacks(context.Context, time.Time) ([]budget.DoorBack, error) {
	return []budget.DoorBack{{ID: 1}}, nil
}
func (r *budgetRepository) RecordGrayscale(_ context.Context, event budget.GrayscaleEvent) (budget.GrayscaleEvent, error) {
	return event, nil
}
func (r *budgetRepository) GrayscaleEvents(context.Context, time.Time) ([]budget.GrayscaleEvent, error) {
	return nil, nil
}
func (r *budgetRepository) Reasons(context.Context) ([]string, error) {
	return []string{"to make music"}, nil
}
func (r *budgetRepository) SaveReasons(_ context.Context, reasons []string) ([]string, error) {
	return reasons, nil
}
func (r *budgetRepository) BudgetDays(context.Context, int) ([]budget.Snapshot, error) {
	return []budget.Snapshot{{Day: "2026-11-25", Budget: 150}}, nil
}
func (r *budgetRepository) BudgetDay(context.Context, string) (*budget.Snapshot, error) {
	return nil, nil
}
func (r *budgetRepository) SaveBudgetDay(context.Context, budget.Snapshot) (bool, error) {
	return true, nil
}

func mcpBudgetConfig() *policy.Config {
	since := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	config := policy.Config{Timezone: "Europe/Istanbul", ClosedByDefault: true, DistractionTimes: []policy.Window{{Start: "19:00", End: "00:00"}},
		Distractions: []policy.TargetGroup{{ID: "video", AndroidPackages: []string{"com.google.android.youtube"}}},
		Budget: policy.Budget{Enabled: true, BaseMinutes: 120, MaxMinutes: 300, FloorMinutes: 30, HabitsForMax: 6, RateDistraction: 1, RateDay: 3,
			RateBedtime: 10, RateGrayscaleOff: 3, BorrowMultiplier: 2, WindowMinutes: 60, WindowUseMinutes: 10, GraceMinutes: 1, WaitMinutes: 10},
		BudgetSince: &since}
	return &config
}

func connectBudget(t *testing.T, repository *budgetRepository, now time.Time) *mcp.ClientSession {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "telemetry-test", Version: "0"}, nil)
	addBudgetTools(server, repository, repository, func() time.Time { return now })
	addScheduleTools(server, repository, func() time.Time { return now })
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "0"}, nil).Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func TestBudgetToolsAreRegisteredWithTheBudgetStore(t *testing.T) {
	handler, err := New(&budgetRepository{}, "token")
	if err != nil || handler == nil {
		t.Fatal(err)
	}
	session := connectBudget(t, &budgetRepository{}, time.Now())
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"budget", "habits", "change_habits", "tick_habit", "set_blocking_schedule", "blocking_schedule"} {
		if !names[want] {
			t.Fatalf("missing tool %s", want)
		}
	}
}

func TestBudgetToolShowsTodayAndTheSnapshots(t *testing.T) {
	// 12:00 in Istanbul, outside the distraction time.
	now := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	repository := &budgetRepository{config: mcpBudgetConfig()}
	session := connectBudget(t, repository, now)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "budget", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("budget = %+v, %v", result, err)
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	var output BudgetOutput
	json.Unmarshal(encoded, &output)
	if !output.Today.Enabled || output.Today.Day != "2026-11-26" || output.Today.Left != 120 || output.Today.Rate.Kind != budget.RateKindDay || !output.Today.DoorAvailable ||
		len(output.Days) != 1 || output.DoorBacks != 1 || output.Today.Reasons[0] != "to make music" {
		t.Fatalf("budget = %s", encoded)
	}
}

func TestChangeAndTickHabits(t *testing.T) {
	now := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	repository := &budgetRepository{config: mcpBudgetConfig()}
	session := connectBudget(t, repository, now)
	bad, _ := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "change_habits", Arguments: map[string]any{
		"changes": []map[string]any{{"action": "add", "name": "walk", "weekdays": []int{9}}}, "reason": "x"}})
	if !bad.IsError {
		t.Fatal("a weekday out of range must be refused")
	}
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "change_habits", Arguments: map[string]any{
		"changes": []map[string]any{{"action": "add", "name": "walk", "weekdays": []int{}}}, "reason": "the owner wants to walk daily"}})
	if err != nil || result.IsError {
		t.Fatalf("change_habits = %+v, %v", result, err)
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	var changed ChangeHabitsOutput
	json.Unmarshal(encoded, &changed)
	if changed.Change.ID != 21 || len(changed.Habits) != 1 || len(repository.changes) != 1 || *repository.changes[0].Name != "walk" {
		t.Fatalf("change = %s", encoded)
	}
	if unsaid, _ := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "tick_habit", Arguments: map[string]any{"habit_id": 1}}); !unsaid.IsError {
		t.Fatal("a tick needs the owner's words")
	}
	ticked, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "tick_habit", Arguments: map[string]any{"habit_id": 1, "said": "I walked"}})
	if err != nil || ticked.IsError {
		t.Fatalf("tick_habit = %+v, %v", ticked, err)
	}
	encoded, _ = json.Marshal(ticked.StructuredContent)
	var tick TickHabitOutput
	json.Unmarshal(encoded, &tick)
	if len(repository.events) != 1 || repository.events[0].Source != "assistant" || repository.events[0].Day != "2026-11-26" || !tick.Budget.Habits[0].Done || tick.Budget.Earned != 30 {
		t.Fatalf("tick = %s", encoded)
	}
	if missing, _ := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "tick_habit", Arguments: map[string]any{"habit_id": 7, "said": "done"}}); !missing.IsError {
		t.Fatal("a habit that does not exist cannot be ticked")
	}
	listed, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "habits", Arguments: map[string]any{"days": 7}})
	if err != nil || listed.IsError {
		t.Fatalf("habits = %+v, %v", listed, err)
	}
	encoded, _ = json.Marshal(listed.StructuredContent)
	var habits HabitsOutput
	json.Unmarshal(encoded, &habits)
	if len(habits.Habits) != 1 || len(habits.Ticks) != 1 || len(habits.Heatmap.Habits) != 1 || habits.Heatmap.Habits[0].Days["2026-11-26"] != budget.CellDone {
		t.Fatalf("habits = %s", encoded)
	}
}

func TestSetBlockingScheduleTakesTheBudget(t *testing.T) {
	now := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	repository := &budgetRepository{config: mcpBudgetConfig()}
	session := connectBudget(t, repository, now)
	rule := map[string]any{"enabled": true, "base_minutes": 100, "max_minutes": 300, "floor_minutes": 30, "habits_for_max": 6, "rate_distraction": 1, "rate_day": 3,
		"rate_bedtime": 10, "rate_grayscale_off": 3, "borrow_multiplier": 2, "window_minutes": 60, "window_use_minutes": 10, "grace_minutes": 1, "wait_minutes": 10}
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "set_blocking_schedule", Arguments: map[string]any{"budget": rule, "reason": "a smaller base"}})
	if err != nil || result.IsError {
		t.Fatalf("set_blocking_schedule = %+v, %v", result, err)
	}
	if repository.schedule.Budget == nil || repository.schedule.Budget.BaseMinutes != 100 || !repository.schedule.Budget.Enabled {
		t.Fatalf("budget passed = %+v", repository.schedule.Budget)
	}
	listed, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "blocking_schedule", Arguments: map[string]any{}})
	if err != nil || listed.IsError {
		t.Fatalf("blocking_schedule = %+v, %v", listed, err)
	}
	encoded, _ := json.Marshal(listed.StructuredContent)
	var schedule BlockingScheduleOutput
	json.Unmarshal(encoded, &schedule)
	if !schedule.Budget.Rule.Enabled || schedule.Budget.Rule.BaseMinutes != 120 {
		t.Fatalf("schedule budget = %s", encoded)
	}
}

func TestOpenNowIsRefusedWhenNothingIsLeft(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC) // 15:00 in Istanbul
	config := mcpBudgetConfig()
	config.Budget.BaseMinutes, config.Budget.MaxMinutes, config.Budget.FloorMinutes = 1, 1, 1
	repository := &budgetRepository{config: config}
	repository.records = []activity.Record{
		{Source: "android:phone:usage", Kind: "android.usage.activity_resumed", Start: now.Add(-10 * time.Minute), Payload: json.RawMessage(`{"package_name":"com.google.android.youtube"}`)},
		{Source: "android:phone:usage", Kind: "android.usage.activity_paused", Start: now.Add(-5 * time.Minute), Payload: json.RawMessage(`{"package_name":"com.google.android.youtube"}`)},
	}
	session := connectBudget(t, repository, now)
	refused, _ := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "open_now", Arguments: map[string]any{"minutes": 10, "sites": []string{"youtube.com"}, "purpose": "a tutorial"}})
	if !refused.IsError || repository.opening.Purpose != "" {
		t.Fatalf("open_now with nothing left = %+v", refused)
	}
}

// OpenSpans has distractions open throughout, as an opening would: these
// tests are about refusals and shapes, not about when use is charged.
func (r *budgetRepository) OpenSpans(_ context.Context, from, to time.Time) ([]budget.OpenSpan, error) {
	return []budget.OpenSpan{{From: from, To: to}}, nil
}
