package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/rimexe0/telemetry/internal/store"
)

func (s *Server) executionSessions(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	switch request.Method {
	case http.MethodGet:
		s.listExecutionSessions(writer, request)
	case http.MethodPost:
		s.startExecutionSession(writer, request)
	default:
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or POST")
	}
}

func (s *Server) startExecutionSession(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, s.config.MaxRequestSize)
	var input store.ExecutionSessionInput
	if err := decodeExecutionJSON(request, &input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one execution session")
		return
	}
	session, err := s.repository.StartExecutionSession(request.Context(), input)
	if errors.Is(err, store.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "activity or plan block does not exist")
		return
	}
	if executionClientError(err) {
		writeError(writer, http.StatusBadRequest, "invalid_execution", err.Error())
		return
	}
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusCreated, session)
}

func (s *Server) listExecutionSessions(writer http.ResponseWriter, request *http.Request) {
	value, err := limit(request, 200)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	if value > 200 {
		writeError(writer, http.StatusBadRequest, "invalid_query", "limit must be an integer from 1 through 200")
		return
	}
	sessions, err := s.repository.ListExecutionSessions(request.Context(), value)
	if executionClientError(err) {
		writeError(writer, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"sessions": sessions})
}

func (s *Server) currentExecutionSession(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	session, err := s.repository.CurrentExecutionSession(request.Context())
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"session": session})
}

func (s *Server) executionSession(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	sessionID, err := executionSessionID(request)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_path", err.Error())
		return
	}
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}
	session, err := s.repository.GetExecutionSession(request.Context(), sessionID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "execution session does not exist")
		return
	}
	if executionClientError(err) {
		writeError(writer, http.StatusBadRequest, "invalid_path", err.Error())
		return
	}
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, session)
}

func (s *Server) executionSessionEvents(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	sessionID, err := executionSessionID(request)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_path", err.Error())
		return
	}
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, s.config.MaxRequestSize)
	var input store.ExecutionEventInput
	if err := decodeExecutionJSON(request, &input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one execution event")
		return
	}
	session, err := s.repository.TransitionExecutionSession(request.Context(), sessionID, input)
	if errors.Is(err, store.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "execution session does not exist")
		return
	}
	if executionClientError(err) {
		writeError(writer, http.StatusBadRequest, "invalid_transition", err.Error())
		return
	}
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, session)
}

func executionSessionID(request *http.Request) (int64, error) {
	sessionID, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || sessionID <= 0 {
		return 0, errors.New("session id must be a positive integer")
	}
	return sessionID, nil
}

func decodeExecutionJSON(request *http.Request, target any) error {
	decoder := json.NewDecoder(request.Body)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return errors.New("request body contains more than one JSON value")
		}
		return err
	}
	return nil
}

func executionClientError(err error) bool {
	return errors.Is(err, store.ErrInvalidExecutionInput) || errors.Is(err, store.ErrInvalidExecutionTransition)
}
