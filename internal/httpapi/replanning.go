package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/rimexe0/telemetry/internal/store"
)

// Decisions are only reachable through owner-authorized requests. Nothing in a
// request body can mark a proposal approved; acceptance requires this route
// plus the exact reviewed content hash.
var ownerDecisionProvenance = json.RawMessage(`{"decided_by":"owner","source":"owner_api"}`)

func (s *Server) replanProposals(writer http.ResponseWriter, request *http.Request) {
	planDate, ok := s.ownerPlanDate(writer, request)
	if !ok {
		return
	}
	switch request.Method {
	case http.MethodGet:
		value, err := limit(request, 20)
		if err != nil || value > 50 {
			writeError(writer, http.StatusBadRequest, "invalid_query", "limit must be an integer from 1 through 50")
			return
		}
		proposals, err := s.repository.ListPlanChangeProposals(request.Context(), planDate, value)
		if err != nil {
			writeProposalError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"proposals": proposals})
	case http.MethodPost:
		request.Body = http.MaxBytesReader(writer, request.Body, s.config.MaxRequestSize)
		var input store.PlanChangeRequest
		if err := decodeExecutionJSON(request, &input); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one replan request")
			return
		}
		proposal, err := s.repository.CreatePlanChangeProposal(request.Context(), planDate, input)
		if err != nil {
			writeProposalError(writer, err)
			return
		}
		writeJSON(writer, http.StatusCreated, proposal)
	default:
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or POST")
	}
}

func (s *Server) replanProposal(writer http.ResponseWriter, request *http.Request) {
	planDate, ok := s.ownerPlanDate(writer, request)
	if !ok {
		return
	}
	id, ok := proposalID(writer, request)
	if !ok {
		return
	}
	proposal, err := s.repository.GetPlanChangeProposal(request.Context(), planDate, id)
	if err != nil {
		writeProposalError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, proposal)
}

func (s *Server) acceptReplanProposal(writer http.ResponseWriter, request *http.Request) {
	s.decideReplanProposal(writer, request, s.repository.AcceptPlanChangeProposal)
}

func (s *Server) rejectReplanProposal(writer http.ResponseWriter, request *http.Request) {
	s.decideReplanProposal(writer, request, s.repository.RejectPlanChangeProposal)
}

type proposalDecider func(context.Context, time.Time, int64, store.ProposalDecision, json.RawMessage) (store.PlanChangeProposal, error)

func (s *Server) decideReplanProposal(writer http.ResponseWriter, request *http.Request, decide proposalDecider) {
	planDate, ok := s.ownerPlanDate(writer, request)
	if !ok {
		return
	}
	id, ok := proposalID(writer, request)
	if !ok {
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, s.config.MaxRequestSize)
	var decision store.ProposalDecision
	if err := decodeExecutionJSON(request, &decision); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one decision")
		return
	}
	proposal, err := decide(request.Context(), planDate, id, decision, ownerDecisionProvenance)
	if err != nil {
		writeProposalError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, proposal)
}

func (s *Server) ownerPlanDate(writer http.ResponseWriter, request *http.Request) (time.Time, bool) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return time.Time{}, false
	}
	planDate, err := parsePlanDate(request.PathValue("date"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_path", err.Error())
		return time.Time{}, false
	}
	return planDate, true
}

func proposalID(writer http.ResponseWriter, request *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(writer, http.StatusBadRequest, "invalid_path", "proposal id must be a positive integer")
		return 0, false
	}
	return id, true
}

func writeProposalError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "plan or proposal does not exist")
	case errors.Is(err, store.ErrProposalStale):
		writeError(writer, http.StatusConflict, "proposal_stale", err.Error())
	case errors.Is(err, store.ErrProposalConflict):
		writeError(writer, http.StatusConflict, "proposal_conflict", err.Error())
	case errors.Is(err, store.ErrInvalidProposal):
		writeError(writer, http.StatusBadRequest, "invalid_proposal", err.Error())
	default:
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
	}
}
