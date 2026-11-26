package httpapi

import (
	"context"
	"encoding/json"

	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/activity"
	"github.com/rimexe0/telemetry/internal/budget"
	"github.com/rimexe0/telemetry/internal/policy"
	"github.com/rimexe0/telemetry/internal/store"
)

// memoryBudget keeps the budget's records in memory with the same rules as
// the Postgres store: one door at a time, cancel only while waiting, no
// ticks for an archived habit, and nothing deleted.
type memoryBudget struct {
	mu        sync.Mutex
	habits    []budget.Habit
	events    []budget.HabitEvent
	doors     []budget.Door
	backs     []budget.DoorBack
	grayscale []budget.GrayscaleEvent
	reasons   []string
	days      []budget.Snapshot
	changes   [][]store.HabitChange
	// why is the reason each change was logged with.
	why []string
	// phone is when a phone last delivered; nil for no phone.
	phone *time.Time
}

func (m *memoryBudget) ListHabits(context.Context) ([]budget.Habit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]budget.Habit{}, m.habits...), nil
}

func (m *memoryBudget) ApplyHabitChanges(_ context.Context, changes []store.HabitChange, reason string) (store.AgentChange, []budget.Habit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if strings.TrimSpace(reason) == "" {
		return store.AgentChange{}, nil, fmt.Errorf("%w: reason", store.ErrInvalidAgentChange)
	}
	written := []budget.Habit{}
	for _, change := range changes {
		switch change.Action {
		case "add":
			if change.Name == nil {
				return store.AgentChange{}, nil, fmt.Errorf("%w: name", store.ErrInvalidAgentChange)
			}
			name, err := budget.ValidateName(*change.Name)
			if err != nil {
				return store.AgentChange{}, nil, fmt.Errorf("%w: %v", store.ErrInvalidAgentChange, err)
			}
			habit := budget.Habit{ID: int64(len(m.habits) + 1), Name: name, Position: len(m.habits), Weekdays: budget.Weekdays{}, CreatedAt: time.Now().UTC().Add(-48 * time.Hour)}
			if change.Weekdays != nil {
				habit.Weekdays = *change.Weekdays
			}
			m.habits = append(m.habits, habit)
			written = append(written, habit)
		case "update":
			found := false
			for i := range m.habits {
				if change.ID == nil || m.habits[i].ID != *change.ID {
					continue
				}
				found = true
				if change.Name != nil {
					name, err := budget.ValidateName(*change.Name)
					if err != nil {
						return store.AgentChange{}, nil, fmt.Errorf("%w: %v", store.ErrInvalidAgentChange, err)
					}
					m.habits[i].Name = name
				}
				if change.Weekdays != nil {
					m.habits[i].Weekdays = *change.Weekdays
				}
				if change.Archived != nil && *change.Archived {
					now := time.Now().UTC()
					m.habits[i].ArchivedAt = &now
				} else if change.Archived != nil {
					m.habits[i].ArchivedAt = nil
				}
				written = append(written, m.habits[i])
			}
			if !found {
				return store.AgentChange{}, nil, fmt.Errorf("%w: no such habit", store.ErrInvalidAgentChange)
			}
		default:
			return store.AgentChange{}, nil, fmt.Errorf("%w: action", store.ErrInvalidAgentChange)
		}
	}
	m.changes = append(m.changes, changes)
	m.why = append(m.why, reason)
	encoded, _ := json.Marshal(changes)
	return store.AgentChange{ID: int64(len(m.changes)), Kind: store.AgentChangeHabits, Changes: encoded, Previous: json.RawMessage(`[]`), Reason: reason}, written, nil
}

func (m *memoryBudget) HabitEvents(_ context.Context, fromDay string) ([]budget.HabitEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := []budget.HabitEvent{}
	for _, event := range m.events {
		if event.Day >= fromDay {
			result = append(result, event)
		}
	}
	return result, nil
}

func (m *memoryBudget) RecordHabitEvent(_ context.Context, event budget.HabitEvent) (budget.HabitEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	received := time.Now().UTC()
	if event.At.IsZero() {
		event.At = received
	}
	for _, habit := range m.habits {
		if habit.ID == event.HabitID {
			if habit.ArchivedAt != nil && !habit.ArchivedAt.After(event.At) {
				return budget.HabitEvent{}, fmt.Errorf("%w: archived", store.ErrInvalidHabit)
			}
			for _, existing := range m.events {
				if existing.HabitID == event.HabitID && existing.Day == event.Day && existing.Action == event.Action && existing.At.Equal(event.At) {
					return existing, nil
				}
			}
			event.ID = int64(len(m.events) + 1)
			event.ReceivedAt = received
			m.events = append(m.events, event)
			return event, nil
		}
	}
	return budget.HabitEvent{}, store.ErrNotFound
}

func (m *memoryBudget) BudgetDoors(_ context.Context, since time.Time) ([]budget.Door, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := []budget.Door{}
	for _, door := range m.doors {
		if door.WindowEnd.After(since) || !door.RequestedAt.Before(since) {
			result = append(result, door)
		}
	}
	return result, nil
}

func (m *memoryBudget) CreateDoor(_ context.Context, door budget.Door) (budget.Door, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.doors {
		if existing.CancelledAt == nil && existing.WindowEnd.After(door.RequestedAt) {
			return budget.Door{}, store.ErrDoorConflict
		}
	}
	door.ID = int64(len(m.doors) + 1)
	m.doors = append(m.doors, door)
	return door, nil
}

func (m *memoryBudget) CancelDoor(_ context.Context, id int64, at time.Time) (budget.Door, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.doors {
		if m.doors[i].ID != id {
			continue
		}
		switch {
		case m.doors[i].CancelledAt != nil:
		case !m.doors[i].OpensAt.After(at):
			return budget.Door{}, store.ErrDoorNotWaiting
		default:
			m.doors[i].CancelledAt = &at
		}
		return m.doors[i], nil
	}
	return budget.Door{}, store.ErrNotFound
}

func (m *memoryBudget) RecordDoorBack(_ context.Context, back budget.DoorBack) (budget.DoorBack, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	back.ID = int64(len(m.backs) + 1)
	m.backs = append(m.backs, back)
	return back, nil
}

func (m *memoryBudget) DoorBacks(context.Context, time.Time) ([]budget.DoorBack, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]budget.DoorBack{}, m.backs...), nil
}

func (m *memoryBudget) RecordGrayscale(_ context.Context, event budget.GrayscaleEvent) (budget.GrayscaleEvent, error) {
	if err := store.ValidGrayscale(event); err != nil {
		return budget.GrayscaleEvent{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	event.ID = int64(len(m.grayscale) + 1)
	m.grayscale = append(m.grayscale, event)
	return event, nil
}

func (m *memoryBudget) GrayscaleEvents(context.Context, time.Time) ([]budget.GrayscaleEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]budget.GrayscaleEvent{}, m.grayscale...), nil
}

func (m *memoryBudget) Reasons(context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string{}, m.reasons...), nil
}

func (m *memoryBudget) SaveReasons(_ context.Context, reasons []string) ([]string, error) {
	cleaned, err := store.CleanReasons(reasons)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reasons = cleaned
	return cleaned, nil
}

// BudgetDays are each day's newest revision, newest day first.
func (m *memoryBudget) BudgetDays(context.Context, int) ([]budget.Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	newest := map[string]budget.Snapshot{}
	for _, snapshot := range m.days {
		if current, ok := newest[snapshot.Day]; !ok || snapshot.Revision > current.Revision {
			newest[snapshot.Day] = snapshot
		}
	}
	result := []budget.Snapshot{}
	for _, snapshot := range newest {
		result = append(result, snapshot)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Day > result[j].Day })
	return result, nil
}

func (m *memoryBudget) BudgetDay(_ context.Context, day string) (*budget.Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.newestDay(day), nil
}

func (m *memoryBudget) newestDay(day string) *budget.Snapshot {
	var newest *budget.Snapshot
	for _, snapshot := range m.days {
		if snapshot.Day == day && (newest == nil || snapshot.Revision > newest.Revision) {
			copied := snapshot
			newest = &copied
		}
	}
	return newest
}

func (m *memoryBudget) SaveBudgetDay(_ context.Context, snapshot budget.Snapshot) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.newestDay(snapshot.Day) != nil {
		return false, nil
	}
	snapshot.Revision, snapshot.Cause = 1, ""
	m.days = append(m.days, snapshot)
	return true, nil
}

func (m *memoryBudget) ReviseBudgetDay(_ context.Context, snapshot budget.Snapshot, cause string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	newest := m.newestDay(snapshot.Day)
	if newest == nil || newest.SameDay(snapshot) {
		return false, nil
	}
	snapshot.Revision, snapshot.Cause = newest.Revision+1, cause
	m.days = append(m.days, snapshot)
	return true, nil
}

func (m *memoryBudget) PhoneLastDelivered(context.Context) (*time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.phone, nil
}

// budgetRepository is the fake repository with the budget's records, and
// activity records the test sets.
type budgetRepository struct {
	*fakeRepository
	*memoryBudget
	records []activity.Record
}

func (r *budgetRepository) ActivityRecords(context.Context, time.Time, time.Time) ([]activity.Record, map[string]time.Time, error) {
	return r.records, nil, nil
}

// budgetPolicy has the budget on with the contract's numbers (for tests
// only) and a distraction time that does not cover now, with bedtime off.
func budgetPolicy(t *testing.T) policy.Config {
	t.Helper()
	location, _ := time.LoadLocation("Europe/Istanbul")
	later := time.Now().In(location).Add(3 * time.Hour)
	config := policy.Config{
		Timezone:         "Europe/Istanbul",
		Distractions:     []policy.TargetGroup{{ID: "video", AndroidPackages: []string{"com.google.android.youtube"}}},
		ClosedByDefault:  true,
		DistractionTimes: []policy.Window{{Start: later.Format("15:04"), End: later.Add(time.Hour).Format("15:04")}},
		Budget: policy.Budget{Enabled: true, BaseMinutes: 120, MaxMinutes: 300, FloorMinutes: 30, HabitsForMax: 6,
			RateDistraction: 1, RateDay: 3, RateBedtime: 10, RateGrayscaleOff: 3, BorrowMultiplier: 2,
			WindowMinutes: 60, WindowUseMinutes: 10, GraceMinutes: 1, WaitMinutes: 10},
	}
	since := time.Now().UTC().Add(-72 * time.Hour)
	config.BudgetSince = &since
	if err := config.Normalize(); err != nil {
		t.Fatal(err)
	}
	return config
}

func budgetServer(t *testing.T, config policy.Config) (*Server, *budgetRepository) {
	t.Helper()
	repository := &budgetRepository{fakeRepository: &fakeRepository{policies: []store.PolicyVersion{{ID: 1, Config: config}}}, memoryBudget: &memoryBudget{}}
	server, err := New(repository, Config{
		IngestToken:     "ingest-secret",
		ReadToken:       "read-secret",
		OwnerAuthorized: func(request *http.Request) bool { return request.Header.Get("X-Test-Owner") == "yes" },
		DeviceAuthorized: func(_ context.Context, deviceID, token string) (bool, bool, error) {
			return deviceID == "android-1", deviceID == "android-1" && token == "device-secret", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return server, repository
}

func deviceCall(server *Server, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("X-Telemetry-Device-ID", "android-1")
	request.Header.Set("Authorization", "Bearer device-secret")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response
}

func ownerCall(server *Server, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("X-Test-Owner", "yes")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response
}

func decodeBody(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode %s: %v", response.Body, err)
	}
}

func TestDeviceNowCarriesTheBudget(t *testing.T) {
	server, repository := budgetServer(t, budgetPolicy(t))
	repository.reasons = []string{"to finish the album"}
	repository.habits = []budget.Habit{{ID: 1, Name: "walk", Weekdays: budget.Weekdays{}, CreatedAt: time.Now().UTC().Add(-48 * time.Hour)}}
	response := deviceGet(server, "android-1", "device-secret")
	var body struct {
		Budget      map[string]any `json:"budget"`
		Fingerprint string         `json:"fingerprint"`
	}
	decodeBody(t, response, &body)
	for _, key := range []string{"enabled", "day", "budget", "spent", "left", "earned", "penalty", "debt", "debt_tomorrow", "borrowable", "rate",
		"in_distraction_time", "next_distraction_time", "bedtime_at", "wind_down", "grayscale_declined", "door", "door_available",
		"door_unavailable_reason", "next_door_at", "habits", "reasons", "settings"} {
		if _, ok := body.Budget[key]; !ok {
			t.Errorf("budget is missing %q", key)
		}
	}
	if body.Budget["enabled"] != true || body.Budget["door_available"] != true || body.Budget["door"] != nil || len(body.Budget["habits"].([]any)) != 1 {
		t.Fatalf("budget = %v", body.Budget)
	}
	// Nothing open and nothing in use: asking again gives the same fingerprint.
	again := deviceGet(server, "android-1", "device-secret")
	var second struct {
		Fingerprint string `json:"fingerprint"`
	}
	decodeBody(t, again, &second)
	if second.Fingerprint != body.Fingerprint {
		t.Fatal("the fingerprint moved with nothing open")
	}
}

func TestDisabledBudgetKeepsTheFingerprint(t *testing.T) {
	config := budgetPolicy(t)
	config.Budget.Enabled = false
	server, _ := budgetServer(t, config)
	var body struct {
		Budget      map[string]any  `json:"budget"`
		Decision    policy.Decision `json:"decision"`
		Fingerprint string          `json:"fingerprint"`
	}
	decodeBody(t, deviceGet(server, "android-1", "device-secret"), &body)
	if body.Budget["enabled"] != false || body.Budget["door_unavailable_reason"] != "The budget is off." {
		t.Fatalf("budget = %v", body.Budget)
	}
	if body.Fingerprint == "" || budgetPrint(budget.State{}) != nil {
		t.Fatal("a disabled budget adds nothing to the fingerprint")
	}
}

func TestDoorIsMadeWaitsAndIsCancelled(t *testing.T) {
	server, repository := budgetServer(t, budgetPolicy(t))
	response := deviceCall(server, http.MethodPost, "/v1/device/door", "{}")
	if response.Code != http.StatusOK {
		t.Fatalf("door = %d %s", response.Code, response.Body)
	}
	var door budget.DoorView
	decodeBody(t, response, &door)
	if door.State != budget.DoorWaiting || door.OpensAt.Sub(door.RequestedAt) != 10*time.Minute || door.UseMinutes == nil || *door.UseMinutes != 10 {
		t.Fatalf("door = %+v", door)
	}
	if repository.doors[0].Source != "device:android-1" {
		t.Fatalf("source = %q", repository.doors[0].Source)
	}
	// While it waits, the decision does not open yet, and the device is
	// told about the door.
	var now struct {
		Budget   budget.State    `json:"budget"`
		Decision policy.Decision `json:"decision"`
		Timeline []policy.Segment
	}
	decodeBody(t, deviceGet(server, "android-1", "device-secret"), &now)
	if now.Budget.Door == nil || now.Budget.Door.State != budget.DoorWaiting || now.Budget.DoorAvailable || now.Decision.Mode == policy.ModeDoor {
		t.Fatalf("now = %+v", now)
	}
	opens := false
	for _, segment := range now.Timeline {
		opens = opens || segment.Decision.Mode == policy.ModeDoor && segment.From.Equal(door.OpensAt)
	}
	if !opens {
		t.Fatalf("the timeline does not open the door at %v: %+v", door.OpensAt, now.Timeline)
	}
	second := deviceCall(server, http.MethodPost, "/v1/device/door", "{}")
	var refusal map[string]string
	decodeBody(t, second, &refusal)
	if second.Code != http.StatusConflict || refusal["error"] != "door_unavailable" || !strings.HasPrefix(refusal["message"], "A door is already waiting.") {
		t.Fatalf("second door = %d %v", second.Code, refusal)
	}
	cancelled := deviceCall(server, http.MethodPost, fmt.Sprintf("/v1/device/door/%d/cancel", door.ID), "")
	decodeBody(t, cancelled, &door)
	if cancelled.Code != http.StatusOK || door.State != budget.DoorCancelled {
		t.Fatalf("cancel = %d %+v", cancelled.Code, door)
	}
	// A cancelled door leaves no wait for the next one.
	if third := ownerCall(server, http.MethodPost, "/v1/door", "{}"); third.Code != http.StatusOK {
		t.Fatalf("door after cancelling = %d %s", third.Code, third.Body)
	}
	if back := deviceCall(server, http.MethodPost, "/v1/device/door/back", "{}"); back.Code != http.StatusOK || len(repository.backs) != 1 {
		t.Fatalf("back = %d", back.Code)
	}
	if missing := deviceCall(server, http.MethodPost, "/v1/device/door/99/cancel", ""); missing.Code != http.StatusNotFound {
		t.Fatalf("missing door = %d", missing.Code)
	}
	anonymous := httptest.NewRecorder()
	server.ServeHTTP(anonymous, httptest.NewRequest(http.MethodPost, "/v1/device/door", strings.NewReader("{}")))
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("without a device credential = %d", anonymous.Code)
	}
}

func TestOpenDoorCannotBeCancelled(t *testing.T) {
	server, repository := budgetServer(t, budgetPolicy(t))
	now := time.Now().UTC()
	repository.doors = []budget.Door{{ID: 1, RequestedAt: now.Add(-15 * time.Minute), OpensAt: now.Add(-5 * time.Minute), WindowEnd: now.Add(55 * time.Minute), UseMinutes: 10, GraceMinutes: 1}}
	response := deviceCall(server, http.MethodPost, "/v1/device/door/1/cancel", "")
	if response.Code != http.StatusConflict {
		t.Fatalf("cancel an open door = %d", response.Code)
	}
	var body struct {
		Decision policy.Decision `json:"decision"`
		Budget   budget.State    `json:"budget"`
	}
	decodeBody(t, deviceGet(server, "android-1", "device-secret"), &body)
	if body.Decision.Mode != policy.ModeDoor || body.Budget.Door.State != budget.DoorOpen || body.Budget.NextDoorAt == nil {
		t.Fatalf("now = %+v", body)
	}
}

func TestGrayscaleAndReasons(t *testing.T) {
	server, repository := budgetServer(t, budgetPolicy(t))
	if bad := deviceCall(server, http.MethodPost, "/v1/device/grayscale", `{"action":"off","platform":"android"}`); bad.Code != http.StatusBadRequest {
		t.Fatalf("bad action = %d", bad.Code)
	}
	if bad := deviceCall(server, http.MethodPost, "/v1/device/grayscale", `{"action":"decline","platform":"windows"}`); bad.Code != http.StatusBadRequest {
		t.Fatalf("bad platform = %d", bad.Code)
	}
	if ok := deviceCall(server, http.MethodPost, "/v1/device/grayscale", `{"action":"decline","platform":"linux"}`); ok.Code != http.StatusOK || len(repository.grayscale) != 1 || repository.grayscale[0].Source != "device:android-1" {
		t.Fatalf("decline = %d %s", ok.Code, ok.Body)
	}
	saved := ownerCall(server, http.MethodPut, "/v1/reasons", `{"reasons":["  to make music  ", ""]}`)
	var reasons struct {
		Reasons []string `json:"reasons"`
	}
	decodeBody(t, saved, &reasons)
	if saved.Code != http.StatusOK || len(reasons.Reasons) != 1 || reasons.Reasons[0] != "to make music" {
		t.Fatalf("reasons = %d %v", saved.Code, reasons)
	}
	decodeBody(t, ownerCall(server, http.MethodGet, "/v1/reasons", ""), &reasons)
	if len(reasons.Reasons) != 1 {
		t.Fatalf("read reasons = %v", reasons)
	}
	if device := deviceCall(server, http.MethodPut, "/v1/reasons", `{"reasons":[]}`); device.Code != http.StatusUnauthorized {
		t.Fatalf("a device cannot change the reasons: %d", device.Code)
	}
}

func TestHabitsAreAddedTickedAndShown(t *testing.T) {
	config := budgetPolicy(t)
	server, repository := budgetServer(t, config)
	added := ownerCall(server, http.MethodPost, "/v1/habits", `{"name":"walk","weekdays":[]}`)
	var habit struct {
		Habit budget.Habit `json:"habit"`
	}
	decodeBody(t, added, &habit)
	if added.Code != http.StatusCreated || habit.Habit.Name != "walk" || len(repository.changes) != 1 {
		t.Fatalf("add = %d %s", added.Code, added.Body)
	}
	if bad := ownerCall(server, http.MethodPost, "/v1/habits", `{"name":"  "}`); bad.Code != http.StatusBadRequest {
		t.Fatalf("an empty name = %d", bad.Code)
	}
	path := fmt.Sprintf("/v1/device/habits/%d/tick", habit.Habit.ID)
	day, open := budget.TickDay(config, time.Now())
	ticked := deviceCall(server, http.MethodPost, path, "")
	if !open {
		if ticked.Code != http.StatusConflict {
			t.Fatalf("tick after bedtime = %d", ticked.Code)
		}
		return
	}
	var state budget.State
	decodeBody(t, ticked, &state)
	if ticked.Code != http.StatusOK || len(state.Habits) != 1 || !state.Habits[0].Done || state.Earned != 30 || state.Day != day {
		t.Fatalf("tick = %d %s", ticked.Code, ticked.Body)
	}
	if wrong := ownerCall(server, http.MethodPost, fmt.Sprintf("/v1/habits/%d/tick", habit.Habit.ID), `{"day":"2020-01-01"}`); wrong.Code != http.StatusBadRequest {
		t.Fatalf("another day = %d", wrong.Code)
	}
	unticked := ownerCall(server, http.MethodPost, fmt.Sprintf("/v1/habits/%d/untick", habit.Habit.ID), `{"day":"`+day+`"}`)
	decodeBody(t, unticked, &state)
	if unticked.Code != http.StatusOK || state.Habits[0].Done || len(repository.events) != 2 {
		t.Fatalf("untick = %d %s", unticked.Code, unticked.Body)
	}
	if missing := deviceCall(server, http.MethodPost, "/v1/device/habits/99/tick", ""); missing.Code != http.StatusNotFound {
		t.Fatalf("missing habit = %d", missing.Code)
	}
	archived := ownerCall(server, http.MethodPatch, fmt.Sprintf("/v1/habits/%d", habit.Habit.ID), `{"archived":true}`)
	if archived.Code != http.StatusOK || repository.habits[0].ArchivedAt == nil {
		t.Fatalf("archive = %d %s", archived.Code, archived.Body)
	}
	listed := ownerCall(server, http.MethodGet, "/v1/habits", "")
	var habits struct {
		Habits []budget.Habit `json:"habits"`
	}
	decodeBody(t, listed, &habits)
	if len(habits.Habits) != 1 || habits.Habits[0].ArchivedAt == nil {
		t.Fatalf("an archived habit keeps its place in the list: %s", listed.Body)
	}
	heatmap := ownerCall(server, http.MethodGet, "/v1/habits/heatmap?days=30", "")
	var grid budget.Heatmap
	decodeBody(t, heatmap, &grid)
	if heatmap.Code != http.StatusOK || len(grid.Habits) != 1 || grid.Combined == nil {
		t.Fatalf("heatmap = %d %s", heatmap.Code, heatmap.Body)
	}
	overview := ownerCall(server, http.MethodGet, "/v1/budget?days=30", "")
	var summary map[string]any
	decodeBody(t, overview, &summary)
	if overview.Code != http.StatusOK || summary["today"] == nil || summary["days"] == nil {
		t.Fatalf("budget = %d %s", overview.Code, overview.Body)
	}
}

func TestTickAfterBedtimeStartsIsRefused(t *testing.T) {
	config := budgetPolicy(t)
	location, _ := time.LoadLocation("Europe/Istanbul")
	now := time.Now().In(location)
	config.Bedtime = policy.Bedtime{Enabled: true, Start: now.Add(-time.Hour).Format("15:04"), End: now.Add(time.Hour).Format("15:04")}
	server, repository := budgetServer(t, config)
	repository.habits = []budget.Habit{{ID: 1, Name: "walk", Weekdays: budget.Weekdays{}, CreatedAt: now.Add(-72 * time.Hour).UTC()}}
	if _, open := budget.TickDay(config, time.Now()); open {
		t.Skip("the clock is too near a day boundary for this bedtime")
	}
	response := deviceCall(server, http.MethodPost, "/v1/device/habits/1/tick", "")
	var refusal map[string]string
	decodeBody(t, response, &refusal)
	if response.Code != http.StatusConflict || refusal["error"] != "day_closed" || refusal["message"] != "Bedtime has started, so today's habits are settled." {
		t.Fatalf("tick in bedtime = %d %v", response.Code, refusal)
	}
}

func TestNothingOpensWhenNeitherBudgetNorBorrowingIsLeft(t *testing.T) {
	config := budgetPolicy(t)
	// A single minute, with nothing to borrow.
	config.Budget.BaseMinutes, config.Budget.MaxMinutes, config.Budget.FloorMinutes = 1, 1, 1
	server, repository := budgetServer(t, config)
	now := time.Now().UTC()
	repository.records = []activity.Record{
		{Source: "android:phone:usage", Kind: "android.usage.activity_resumed", Start: now.Add(-6 * time.Minute), Payload: json.RawMessage(`{"package_name":"com.google.android.youtube"}`)},
		{Source: "android:phone:usage", Kind: "android.usage.activity_paused", Start: now.Add(-3 * time.Minute), Payload: json.RawMessage(`{"package_name":"com.google.android.youtube"}`)},
	}
	if day := budget.DayStart(config, budget.DayOf(config, now)); now.Sub(day) < 10*time.Minute {
		t.Skip("the budget day only just started")
	}
	door := deviceCall(server, http.MethodPost, "/v1/device/door", "{}")
	var refusal map[string]string
	decodeBody(t, door, &refusal)
	if door.Code != http.StatusConflict || refusal["message"] != "Nothing is left to spend or borrow today." {
		t.Fatalf("door = %d %v", door.Code, refusal)
	}
	opening := ownerCall(server, http.MethodPost, "/v1/openings", `{"purpose":"a tutorial","everything":true,"minutes":10}`)
	if opening.Code != http.StatusConflict || !strings.Contains(opening.Body.String(), "an opening can't start") || len(repository.openings) != 0 {
		t.Fatalf("opening = %d %s", opening.Code, opening.Body)
	}
}

func TestFingerprintStepsKeepTheEndAhead(t *testing.T) {
	now := time.Date(2026, 11, 26, 18, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		left time.Duration
		step time.Duration
	}{{100 * time.Minute, 15 * time.Minute}, {30 * time.Minute, 5 * time.Minute}, {6 * time.Minute, time.Minute}} {
		end := now.Add(test.left).Add(37 * time.Second)
		steady := steadyFacts(policy.Facts{Budget: &policy.BudgetFact{EndsAt: end}}, now)
		if got := steady.Budget.EndsAt; !got.Equal(end.Truncate(test.step)) {
			t.Errorf("%v left: %v, want a step of %v", test.left, got, test.step)
		}
	}
	door := steadyFacts(policy.Facts{Door: &policy.DoorFact{EndsAt: now.Add(90 * time.Second)}}, now)
	if !door.Door.EndsAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("door end = %v", door.Door.EndsAt)
	}
}

// OpenSpans has distractions open throughout, as an opening would: these
// tests are about refusals and shapes, not about when use is charged.
func (m *memoryBudget) OpenSpans(_ context.Context, from, to time.Time) ([]budget.OpenSpan, error) {
	return []budget.OpenSpan{{From: from, To: to}}, nil
}

func TestHabitsAndReasonsAreEditedOnThePhone(t *testing.T) {
	server, repository := budgetServer(t, budgetPolicy(t))
	added := deviceCall(server, http.MethodPost, "/v1/device/habits", `{"name":"  read  "}`)
	var habit struct {
		Habit budget.Habit `json:"habit"`
	}
	decodeBody(t, added, &habit)
	if added.Code != http.StatusCreated || habit.Habit.Name != "read" || len(habit.Habit.Weekdays) != 0 {
		t.Fatalf("add = %d %s", added.Code, added.Body)
	}
	if bad := deviceCall(server, http.MethodPost, "/v1/device/habits", `{"name":""}`); bad.Code != http.StatusBadRequest {
		t.Fatalf("an empty name = %d %s", bad.Code, bad.Body)
	}
	path := fmt.Sprintf("/v1/device/habits/%d", habit.Habit.ID)
	renamed := deviceCall(server, http.MethodPatch, path, `{"name":"read a book","weekdays":[1,3,5]}`)
	decodeBody(t, renamed, &habit)
	if renamed.Code != http.StatusOK || habit.Habit.Name != "read a book" || len(habit.Habit.Weekdays) != 3 {
		t.Fatalf("rename = %d %s", renamed.Code, renamed.Body)
	}
	// The order is not the phone's to change.
	deviceCall(server, http.MethodPatch, path, `{"position":9}`)
	if repository.changes[len(repository.changes)-1][0].Position != nil {
		t.Fatalf("the phone moved a habit: %+v", repository.changes[len(repository.changes)-1][0])
	}
	archived := deviceCall(server, http.MethodPatch, path, `{"archived":true}`)
	if archived.Code != http.StatusOK || repository.habits[0].ArchivedAt == nil {
		t.Fatalf("archive = %d %s", archived.Code, archived.Body)
	}
	back := deviceCall(server, http.MethodPatch, path, `{"archived":false}`)
	if back.Code != http.StatusOK || repository.habits[0].ArchivedAt != nil {
		t.Fatalf("undo = %d %s", back.Code, back.Body)
	}
	if missing := deviceCall(server, http.MethodPatch, "/v1/device/habits/99", `{"name":"x"}`); missing.Code != http.StatusBadRequest {
		t.Fatalf("missing habit = %d", missing.Code)
	}
	for i, why := range repository.why {
		if !strings.HasSuffix(why, "on the phone") {
			t.Fatalf("change %d was logged as %q", i, why)
		}
	}
	saved := deviceCall(server, http.MethodPut, "/v1/device/reasons", `{"reasons":["to make music"," ","to sleep"]}`)
	var reasons struct {
		Reasons []string `json:"reasons"`
	}
	decodeBody(t, saved, &reasons)
	if saved.Code != http.StatusOK || len(reasons.Reasons) != 2 || reasons.Reasons[1] != "to sleep" {
		t.Fatalf("reasons = %d %s", saved.Code, saved.Body)
	}
	if listed := deviceCall(server, http.MethodGet, "/v1/device/habits", ""); !strings.Contains(listed.Body.String(), "to sleep") {
		t.Fatalf("listed = %s", listed.Body)
	}
	for _, call := range []struct{ method, path, body string }{
		{http.MethodPost, "/v1/device/habits", `{"name":"x"}`},
		{http.MethodPatch, path, `{"name":"x"}`},
		{http.MethodPut, "/v1/device/reasons", `{"reasons":[]}`},
	} {
		request := httptest.NewRequest(call.method, call.path, strings.NewReader(call.body))
		request.Header.Set("X-Telemetry-Device-ID", "android-1")
		request.Header.Set("Authorization", "Bearer wrong")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s without the device credential = %d", call.method, call.path, response.Code)
		}
	}
}
