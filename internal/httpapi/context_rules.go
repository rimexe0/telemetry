package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/rimexe0/telemetry/internal/store"
)

func (s *Server) contextRules(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	switch request.Method {
	case http.MethodGet:
		rules, err := s.repository.ListActivityContextRules(request.Context(), request.URL.Query().Get("include_disabled") == "true")
		if err != nil {
			writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"rules": rules})
	case http.MethodPost:
		request.Body = http.MaxBytesReader(writer, request.Body, s.config.MaxRequestSize)
		var input store.ActivityContextRule
		if err := decodeExecutionJSON(request, &input); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one context rule")
			return
		}
		// Only the mapping itself is accepted from the body.
		rule, err := s.repository.CreateActivityContextRule(request.Context(), store.ActivityContextRule{ActivityID: input.ActivityID, Signal: input.Signal, Pattern: input.Pattern, Note: input.Note})
		writeContextRuleResult(writer, http.StatusCreated, rule, err)
	default:
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or POST")
	}
}

func (s *Server) disableContextRule(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(writer, http.StatusBadRequest, "invalid_path", "rule id must be a positive integer")
		return
	}
	rule, err := s.repository.DisableActivityContextRule(request.Context(), id)
	writeContextRuleResult(writer, http.StatusOK, rule, err)
}

func writeContextRuleResult(writer http.ResponseWriter, status int, rule store.ActivityContextRule, err error) {
	switch {
	case err == nil:
		writeJSON(writer, status, rule)
	case errors.Is(err, store.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "activity or rule does not exist")
	case errors.Is(err, store.ErrInvalidContextRule):
		writeError(writer, http.StatusBadRequest, "invalid_context_rule", err.Error())
	default:
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
	}
}
