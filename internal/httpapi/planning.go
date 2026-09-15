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
	blockID, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || blockID <= 0 {
		writeError(writer, http.StatusBadRequest, "invalid_path", "block id must be a positive integer")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, s.config.MaxRequestSize)
	var input struct {
		State     string `json:"state"`
		StateNote string `json:"state_note"`
	}
	if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one block state")
		return
	}
	block, err := s.repository.SetPlanBlockState(request.Context(), blockID, input.State, strings.TrimSpace(input.StateNote))
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

func parsePlanDate(value string) (time.Time, error) {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, errors.New("plan date must use the YYYY-MM-DD form")
	}
	return time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC), nil
}
