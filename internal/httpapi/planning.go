package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rimexe0/telemetry/internal/store"
)

// Planning endpoints are owner-only: activities and plans are personal intent,
// so neither device tokens nor the read token may touch them. Agents gain a
// separate scoped path later through the reviewed change loop.
func (s *Server) ownerAuthorized(request *http.Request) bool {
	return s.config.OwnerAuthorized != nil && s.config.OwnerAuthorized(request)
}

func (s *Server) activities(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	switch request.Method {
	case http.MethodGet:
		s.listActivities(writer, request)
	case http.MethodPost:
		s.createActivity(writer, request)
	default:
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or POST")
	}
}

func (s *Server) activity(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	if request.Method != http.MethodPatch {
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use PATCH")
		return
	}
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(writer, http.StatusBadRequest, "invalid_path", "activity id must be a positive integer")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, s.config.MaxRequestSize)
	var activity store.Activity
	if err := json.NewDecoder(request.Body).Decode(&activity); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one activity")
		return
	}
	activity.ID = id
	updated, err := s.repository.UpdateActivity(request.Context(), activity)
	if errors.Is(err, store.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "activity does not exist")
		return
	}
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_activity", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, updated)
}

func (s *Server) listActivities(writer http.ResponseWriter, request *http.Request) {
	filter := store.ActivityFilter{Status: request.URL.Query().Get("status")}
	limit, err := limit(request, 200)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	filter.Limit = limit
	activities, err := s.repository.ListActivities(request.Context(), filter)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"activities": activities})
}

func (s *Server) createActivity(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, s.config.MaxRequestSize)
	decoder := json.NewDecoder(request.Body)
	var activity store.Activity
	if err := decoder.Decode(&activity); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one activity")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one activity")
		return
	}
	created, err := s.repository.CreateActivity(request.Context(), activity)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_activity", err.Error())
		return
	}
	writeJSON(writer, http.StatusCreated, created)
}

func (s *Server) plan(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	planDate, err := parsePlanDate(request.PathValue("date"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_path", err.Error())
		return
	}
	switch request.Method {
	case http.MethodGet:
		plan, err := s.repository.LatestPlan(request.Context(), planDate)
		if err != nil {
			writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"plan": plan})
	case http.MethodPut:
		s.savePlan(writer, request, planDate)
	default:
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or PUT")
	}
}

func (s *Server) planVersions(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	planDate, err := parsePlanDate(request.PathValue("date"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_path", err.Error())
		return
	}
	versions, err := s.repository.ListPlanVersions(request.Context(), planDate)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"versions": versions})
}

func (s *Server) savePlan(writer http.ResponseWriter, request *http.Request, planDate time.Time) {
	request.Body = http.MaxBytesReader(writer, request.Body, s.config.MaxRequestSize)
	decoder := json.NewDecoder(request.Body)
	var input store.PlanInput
	if err := decoder.Decode(&input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one plan")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one plan")
		return
	}
	plan, err := s.repository.SavePlan(request.Context(), planDate, input)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_plan", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, plan)
}

func (s *Server) planBlock(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	if request.Method != http.MethodPatch {
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use PATCH")
		return
	}
	planDate, err := parsePlanDate(request.PathValue("date"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_path", err.Error())
		return
	}
	blockID, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || blockID <= 0 {
		writeError(writer, http.StatusBadRequest, "invalid_path", "block id must be a positive integer")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, s.config.MaxRequestSize)
	var input struct {
		State           string `json:"state"`
		StateNote       string `json:"state_note"`
		ExplanationCode string `json:"explanation_code"`
		Action          string `json:"action"`
	}
	decoder := json.NewDecoder(request.Body)
	if err := decoder.Decode(&input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one block state")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one block state")
		return
	}
	block, err := s.repository.SetPlanBlockState(request.Context(), planDate, blockID, input.State, strings.TrimSpace(input.StateNote), strings.TrimSpace(input.ExplanationCode), strings.TrimSpace(input.Action))
	if errors.Is(err, store.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "block does not exist in the current plan version")
		return
	}
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_state", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, block)
}

func (s *Server) planBlockEvents(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	planDate, err := parsePlanDate(request.PathValue("date"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_path", err.Error())
		return
	}
	blockID, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || blockID <= 0 {
		writeError(writer, http.StatusBadRequest, "invalid_path", "block id must be a positive integer")
		return
	}
	events, err := s.repository.ListPlanBlockEvents(request.Context(), planDate, blockID)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) planningSettings(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	switch request.Method {
	case http.MethodGet:
		settings, err := s.repository.GetPlanningSettings(request.Context())
		if err != nil {
			writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
			return
		}
		writePlanningSettings(writer, settings)
	case http.MethodPatch:
		request.Body = http.MaxBytesReader(writer, request.Body, s.config.MaxRequestSize)
		var input struct {
			DailyPlanGateEnabled *bool `json:"daily_plan_gate_enabled"`
		}
		decoder := json.NewDecoder(request.Body)
		if err := decoder.Decode(&input); err != nil || input.DailyPlanGateEnabled == nil {
			writeError(writer, http.StatusBadRequest, "invalid_json", "daily_plan_gate_enabled is required")
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one settings update")
			return
		}
		settings, err := s.repository.UpdatePlanningSettings(request.Context(), *input.DailyPlanGateEnabled)
		if err != nil {
			writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
			return
		}
		writePlanningSettings(writer, settings)
	default:
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or PATCH")
	}
}

func writePlanningSettings(writer http.ResponseWriter, settings store.PlanningSettings) {
	writeJSON(writer, http.StatusOK, map[string]any{
		"daily_plan_gate_enabled": settings.DailyPlanGateEnabled,
		"updated_at":              settings.UpdatedAt,
		"enforcement_active":      false,
	})
}

func parsePlanDate(value string) (time.Time, error) {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, errors.New("plan date must use the YYYY-MM-DD form")
	}
	return time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC), nil
}
