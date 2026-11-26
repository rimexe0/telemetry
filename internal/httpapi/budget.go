package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rimexe0/telemetry/internal/budget"
	"github.com/rimexe0/telemetry/internal/policy"
	"github.com/rimexe0/telemetry/internal/store"
)

// The distraction budget, the door and habits (owner decision 2026-11-26,
// budget-and-habits.md).

// budgetCacheFor is how long the budget's records are reused. Devices ask
// /v1/device/now often, and reading two days of activity each time is what
// costs; the arithmetic itself is redone on every request, so sliding ends
// and door states follow the clock.
const budgetCacheFor = 30 * time.Second

type budgetCache struct {
	mu       sync.Mutex
	loadedAt time.Time
	version  int64
	first    string
	inputs   budget.Inputs
}

func (s *Server) budgetStore() (store.BudgetStore, bool) {
	budgets, ok := s.repository.(store.BudgetStore)
	return budgets, ok
}

// forgetBudget drops the cached records after a write that changes them.
func (s *Server) forgetBudget() {
	s.budget.mu.Lock()
	s.budget.loadedAt = time.Time{}
	s.budget.mu.Unlock()
}

// budgetNow is today's budget for config (labels merged) at now.
func (s *Server) budgetNow(ctx context.Context, config policy.Config, version int64, now time.Time) (budget.Result, error) {
	first := budget.AddDays(budget.DayOf(config, now), -1)
	budgets, ok := s.budgetStore()
	if !ok {
		return budget.Compute(budget.Inputs{Config: config}, first, now), nil
	}
	s.budget.mu.Lock()
	defer s.budget.mu.Unlock()
	cache := &s.budget
	if cache.loadedAt.IsZero() || now.Sub(cache.loadedAt) > budgetCacheFor || now.Before(cache.loadedAt) || cache.version != version || cache.first != first {
		inputs, err := store.LoadBudgetInputs(ctx, s.repository, budgets, config, first, now)
		if err != nil {
			return budget.Result{}, err
		}
		cache.inputs, cache.loadedAt, cache.version, cache.first = inputs, now, version, first
	}
	inputs := cache.inputs
	inputs.Config = config
	return budget.Compute(inputs, first, now), nil
}

// budgetInForce is the policy in force and today's budget.
func (s *Server) budgetInForce(ctx context.Context, now time.Time) (policy.Config, budget.Result, error) {
	config, version, err := store.PolicyInForce(ctx, s.repository)
	if err != nil {
		return config, budget.Result{}, err
	}
	result, err := s.budgetNow(ctx, config, version, now)
	return config, result, err
}

// writeRefusal is the contract's {error, message} for a refused door or
// tick; message is said to the owner as it is.
func writeRefusal(writer http.ResponseWriter, code, message string) {
	writeJSON(writer, http.StatusConflict, map[string]string{"error": code, "message": message})
}

// requestSource names who made a record: the device, or the owner.
func requestSource(request *http.Request) string {
	if device := strings.TrimSpace(request.Header.Get("X-Telemetry-Device-ID")); device != "" {
		return "device:" + device
	}
	return "owner"
}

func (s *Server) deviceOrOwner(writer http.ResponseWriter, request *http.Request) bool {
	if !s.pairedDeviceAuthorized(request) && !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "a paired device credential is required")
		return false
	}
	return true
}

func (s *Server) owner(writer http.ResponseWriter, request *http.Request) bool {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return false
	}
	return true
}

func (s *Server) needBudgetStore(writer http.ResponseWriter) (store.BudgetStore, bool) {
	budgets, ok := s.budgetStore()
	if !ok {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", "the budget is not stored here")
	}
	return budgets, ok
}

// doorView finds a door's view in a fresh budget.
func doorView(result budget.Result, id int64) *budget.DoorView {
	if result.State.Door != nil && result.State.Door.ID == id {
		return result.State.Door
	}
	return nil
}

// createDoor makes a door after "Still want it": it opens after the wait.
func (s *Server) createDoor(writer http.ResponseWriter, request *http.Request) {
	if !s.deviceOrOwner(writer, request) {
		return
	}
	budgets, ok := s.needBudgetStore(writer)
	if !ok {
		return
	}
	ctx, now := request.Context(), time.Now().UTC()
	s.forgetBudget()
	config, result, err := s.budgetInForce(ctx, now)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	door, err := budget.NewDoor(config, result.State, now)
	if errors.Is(err, budget.ErrDoorUnavailable) {
		writeRefusal(writer, "door_unavailable", err.Error())
		return
	}
	door.Source = requestSource(request)
	saved, err := budgets.CreateDoor(ctx, door)
	if errors.Is(err, store.ErrDoorConflict) {
		writeRefusal(writer, "door_unavailable", "A door is already waiting or open.")
		return
	}
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	s.forgetBudget()
	_, fresh, err := s.budgetInForce(ctx, now)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	if view := doorView(fresh, saved.ID); view != nil {
		writeJSON(writer, http.StatusOK, view)
		return
	}
	writeJSON(writer, http.StatusOK, budget.DoorView{ID: saved.ID, State: budget.DoorWaiting, RequestedAt: saved.RequestedAt, OpensAt: saved.OpensAt,
		WindowEnd: saved.WindowEnd, EndsAt: saved.WindowEnd, Borrowing: saved.Borrowing})
}

// cancelDoor cancels a door during its wait, at no cost.
func (s *Server) cancelDoor(writer http.ResponseWriter, request *http.Request) {
	if !s.deviceOrOwner(writer, request) {
		return
	}
	budgets, ok := s.needBudgetStore(writer)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(writer, http.StatusBadRequest, "invalid_path", "door id must be a positive integer")
		return
	}
	ctx, now := request.Context(), time.Now().UTC()
	door, err := budgets.CancelDoor(ctx, id, now)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "no such door")
		return
	case errors.Is(err, store.ErrDoorNotWaiting):
		writeRefusal(writer, "door_not_waiting", "The door is already open; it can only be cancelled while waiting.")
		return
	case err != nil:
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	s.forgetBudget()
	_, fresh, err := s.budgetInForce(ctx, now)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	if view := doorView(fresh, door.ID); view != nil {
		writeJSON(writer, http.StatusOK, view)
		return
	}
	writeJSON(writer, http.StatusOK, budget.DoorView{ID: door.ID, State: budget.DoorCancelled, RequestedAt: door.RequestedAt, OpensAt: door.OpensAt,
		WindowEnd: door.WindowEnd, EndsAt: now, Borrowing: door.Borrowing})
}

// doorBack records a door screen left with Back: the owner returned.
func (s *Server) doorBack(writer http.ResponseWriter, request *http.Request) {
	if !s.deviceOrOwner(writer, request) {
		return
	}
	budgets, ok := s.needBudgetStore(writer)
	if !ok {
		return
	}
	back, err := budgets.RecordDoorBack(request.Context(), budget.DoorBack{At: time.Now().UTC(), Source: requestSource(request)})
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"back": back})
}

// deviceGrayscale records grayscale kept off while wanted, or turned back on.
func (s *Server) deviceGrayscale(writer http.ResponseWriter, request *http.Request) {
	if !s.deviceOrOwner(writer, request) {
		return
	}
	budgets, ok := s.needBudgetStore(writer)
	if !ok {
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, s.config.MaxRequestSize)
	var input struct {
		Action   string `json:"action"`
		Platform string `json:"platform"`
	}
	if err := decodeExecutionJSON(request, &input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one grayscale action")
		return
	}
	event := budget.GrayscaleEvent{At: time.Now().UTC(), Action: input.Action, Platform: input.Platform, Source: requestSource(request)}
	if err := store.ValidGrayscale(event); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_grayscale", err.Error())
		return
	}
	saved, err := budgets.RecordGrayscale(request.Context(), event)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	s.forgetBudget()
	_, fresh, err := s.budgetInForce(request.Context(), event.At)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"event": saved, "budget": fresh.State})
}

// tickHabit ticks or unticks a habit for today's budget day; the response
// is the new budget.
func (s *Server) tickHabit(writer http.ResponseWriter, request *http.Request, action string) {
	budgets, ok := s.needBudgetStore(writer)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(writer, http.StatusBadRequest, "invalid_path", "habit id must be a positive integer")
		return
	}
	var input struct {
		Day string `json:"day"`
	}
	// The body is optional: {} or nothing ticks today.
	body, err := io.ReadAll(io.LimitReader(request.Body, 4096))
	if err == nil && len(bytes.TrimSpace(body)) > 0 {
		err = json.Unmarshal(body, &input)
	}
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", `request body is empty or {"day": "YYYY-MM-DD"}`)
		return
	}
	ctx, now := request.Context(), time.Now().UTC()
	err = store.TickHabit(ctx, s.repository, budgets, id, action, input.Day, requestSource(request), now)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "no such habit")
		return
	case errors.Is(err, store.ErrHabitWrongDay):
		writeError(writer, http.StatusBadRequest, "invalid_day", err.Error())
		return
	case errors.Is(err, store.ErrHabitDayClosed):
		writeRefusal(writer, "day_closed", err.Error())
		return
	case errors.Is(err, store.ErrHabitNotToday):
		writeRefusal(writer, "not_scheduled", err.Error())
		return
	case errors.Is(err, store.ErrInvalidHabit):
		writeRefusal(writer, "invalid_habit", err.Error())
		return
	case err != nil:
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	s.forgetBudget()
	_, fresh, err := s.budgetInForce(ctx, now)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, fresh.State)
}

func (s *Server) deviceTick(writer http.ResponseWriter, request *http.Request) {
	if s.deviceOrOwner(writer, request) {
		s.tickHabit(writer, request, budget.ActionTick)
	}
}

func (s *Server) deviceUntick(writer http.ResponseWriter, request *http.Request) {
	if s.deviceOrOwner(writer, request) {
		s.tickHabit(writer, request, budget.ActionUntick)
	}
}

func (s *Server) ownerTick(writer http.ResponseWriter, request *http.Request) {
	if s.owner(writer, request) {
		s.tickHabit(writer, request, budget.ActionTick)
	}
}

func (s *Server) ownerUntick(writer http.ResponseWriter, request *http.Request) {
	if s.owner(writer, request) {
		s.tickHabit(writer, request, budget.ActionUntick)
	}
}

func (s *Server) ownerCreateDoor(writer http.ResponseWriter, request *http.Request) {
	if s.owner(writer, request) {
		s.createDoor(writer, request)
	}
}

func (s *Server) ownerCancelDoor(writer http.ResponseWriter, request *http.Request) {
	if s.owner(writer, request) {
		s.cancelDoor(writer, request)
	}
}

// budgetOverview is today's state, the day snapshots and the recent doors.
func (s *Server) budgetOverview(writer http.ResponseWriter, request *http.Request) {
	if !s.owner(writer, request) {
		return
	}
	budgets, ok := s.needBudgetStore(writer)
	if !ok {
		return
	}
	days := 30
	if value := request.URL.Query().Get("days"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 400 {
			writeError(writer, http.StatusBadRequest, "invalid_query", "days must be an integer from 1 through 400")
			return
		}
		days = parsed
	}
	ctx, now := request.Context(), time.Now().UTC()
	config, result, err := s.budgetInForce(ctx, now)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	snapshots, err := budgets.BudgetDays(ctx, days)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	since := budget.DayStart(config, budget.AddDays(budget.DayOf(config, now), -days))
	doors, err := budgets.BudgetDoors(ctx, since)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	backs, err := budgets.DoorBacks(ctx, since)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"today": result.State, "days": snapshots, "doors": doors, "door_backs": backs})
}

type habitInput struct {
	Name     *string          `json:"name"`
	Position *int             `json:"position"`
	Weekdays *budget.Weekdays `json:"weekdays"`
	Archived *bool            `json:"archived"`
}

// habits lists the owner's habits, archived ones included, or adds one.
func (s *Server) habits(writer http.ResponseWriter, request *http.Request) {
	if !s.owner(writer, request) {
		return
	}
	budgets, ok := s.needBudgetStore(writer)
	if !ok {
		return
	}
	if request.Method == http.MethodGet {
		habits, err := budgets.ListHabits(request.Context())
		if err != nil {
			writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"habits": habits})
		return
	}
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or POST")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, s.config.MaxRequestSize)
	var input habitInput
	if err := decodeExecutionJSON(request, &input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one habit")
		return
	}
	s.applyHabit(writer, request, budgets, store.HabitChange{Action: "add", Name: input.Name, Position: input.Position, Weekdays: input.Weekdays}, "Habit added in the panel", http.StatusCreated)
}

// habit changes a habit's name, position, weekdays or archiving.
func (s *Server) habit(writer http.ResponseWriter, request *http.Request) {
	if !s.owner(writer, request) {
		return
	}
	budgets, ok := s.needBudgetStore(writer)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(writer, http.StatusBadRequest, "invalid_path", "habit id must be a positive integer")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, s.config.MaxRequestSize)
	var input habitInput
	if err := decodeExecutionJSON(request, &input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain the fields to change")
		return
	}
	s.applyHabit(writer, request, budgets, store.HabitChange{Action: "update", ID: &id, Name: input.Name, Position: input.Position, Weekdays: input.Weekdays, Archived: input.Archived},
		"Habit changed in the panel", http.StatusOK)
}

// applyHabit logs a panel change like an assistant's, so it can be reverted.
func (s *Server) applyHabit(writer http.ResponseWriter, request *http.Request, budgets store.BudgetStore, change store.HabitChange, reason string, status int) {
	_, written, err := budgets.ApplyHabitChanges(request.Context(), []store.HabitChange{change}, reason)
	if errors.Is(err, store.ErrInvalidAgentChange) {
		writeError(writer, http.StatusBadRequest, "invalid_habit", err.Error())
		return
	}
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	s.forgetBudget()
	writeJSON(writer, status, map[string]any{"habit": written[0]})
}

// habitHeatmap is the GitHub-style grid of days per habit, with runs.
func (s *Server) habitHeatmap(writer http.ResponseWriter, request *http.Request) {
	if !s.owner(writer, request) {
		return
	}
	budgets, ok := s.needBudgetStore(writer)
	if !ok {
		return
	}
	days := 365
	if value := request.URL.Query().Get("days"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 800 {
			writeError(writer, http.StatusBadRequest, "invalid_query", "days must be an integer from 1 through 800")
			return
		}
		days = parsed
	}
	heatmap, err := LoadHeatmap(request.Context(), s.repository, budgets, days, time.Now().UTC())
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, heatmap)
}

// LoadHeatmap reads the habits and their ticks for the last days.
func LoadHeatmap(ctx context.Context, repository store.Repository, budgets store.BudgetStore, days int, now time.Time) (budget.Heatmap, error) {
	config, _, err := store.PolicyInForce(ctx, repository)
	if err != nil {
		return budget.Heatmap{}, err
	}
	first := budget.AddDays(budget.DayOf(config, now), -(days - 1))
	habits, err := budgets.ListHabits(ctx)
	if err != nil {
		return budget.Heatmap{}, err
	}
	events, err := budgets.HabitEvents(ctx, first)
	if err != nil {
		return budget.Heatmap{}, err
	}
	return budget.BuildHeatmap(config, habits, events, first, now), nil
}

// reasons are the owner's own lines shown on the door screen.
func (s *Server) reasons(writer http.ResponseWriter, request *http.Request) {
	if !s.owner(writer, request) {
		return
	}
	budgets, ok := s.needBudgetStore(writer)
	if !ok {
		return
	}
	switch request.Method {
	case http.MethodGet:
		reasons, err := budgets.Reasons(request.Context())
		if err != nil {
			writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"reasons": reasons})
	case http.MethodPut:
		request.Body = http.MaxBytesReader(writer, request.Body, s.config.MaxRequestSize)
		var input struct {
			Reasons []string `json:"reasons"`
		}
		if err := decodeExecutionJSON(request, &input); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_json", `request body must be {"reasons": [...]}`)
			return
		}
		saved, err := budgets.SaveReasons(request.Context(), input.Reasons)
		if errors.Is(err, store.ErrInvalidHabit) {
			writeError(writer, http.StatusBadRequest, "invalid_reasons", err.Error())
			return
		}
		if err != nil {
			writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
			return
		}
		s.forgetBudget()
		writeJSON(writer, http.StatusOK, map[string]any{"reasons": saved})
	default:
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or PUT")
	}
}

// openingRefusedByBudget says why an opening cannot start: with the budget
// on, nothing opens once neither budget nor borrowing is left.
func (s *Server) openingRefusedByBudget(ctx context.Context) (string, error) {
	_, result, err := s.budgetInForce(ctx, time.Now().UTC())
	if err != nil {
		return "", err
	}
	if result.State.Exhausted() {
		return "Nothing is left to spend or borrow today, so an opening can't start.", nil
	}
	return "", nil
}
