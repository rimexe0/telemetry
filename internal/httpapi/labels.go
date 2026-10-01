package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/rimexe0/telemetry/internal/activity"
	"github.com/rimexe0/telemetry/internal/policy"
	"github.com/rimexe0/telemetry/internal/store"
)

// activityLabels lists labels in force, pending proposals and the latest
// decided ones for the owner.
func (s *Server) activityLabels(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	labels, err := s.repository.ListActivityLabels(request.Context())
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	proposals, err := s.repository.ListLabelProposals(request.Context(), "", 30)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"labels": labels, "proposals": proposals})
}

func (s *Server) acceptLabelProposal(writer http.ResponseWriter, request *http.Request) {
	s.decideLabelProposal(writer, request, true)
}

func (s *Server) rejectLabelProposal(writer http.ResponseWriter, request *http.Request) {
	s.decideLabelProposal(writer, request, false)
}

// decideLabelProposal is the only way a label proposal is applied.
func (s *Server) decideLabelProposal(writer http.ResponseWriter, request *http.Request, accept bool) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
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
	proposal, err := s.repository.DecideLabelProposal(request.Context(), id, accept, decision, ownerDecisionProvenance)
	if err != nil {
		writeProposalError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, proposal)
}

func (s *Server) removeActivityLabel(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(writer, http.StatusBadRequest, "invalid_path", "label id must be a positive integer")
		return
	}
	if err := s.repository.RemoveActivityLabel(request.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "label does not exist")
	} else if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
	} else {
		writeJSON(writer, http.StatusOK, map[string]any{"removed": id})
	}
}

// activitySummary is the panel's view of where one local day went.
func (s *Server) activitySummary(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	config := policy.Config{}
	stored, err := s.repository.LatestPolicy(request.Context())
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	if stored != nil {
		config = stored.Config
	}
	if err := config.Normalize(); err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	location, err := time.LoadLocation(config.Timezone)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	now := time.Now().UTC()
	day := now.In(location)
	if value := request.URL.Query().Get("date"); value != "" {
		if day, err = time.ParseInLocation("2006-01-02", value, location); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_query", "date must be YYYY-MM-DD")
			return
		}
	}
	from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, location).UTC()
	to := from.AddDate(0, 0, 1)
	if to.After(now) {
		to = now
	}
	if !to.After(from) {
		writeError(writer, http.StatusBadRequest, "invalid_query", "date is in the future")
		return
	}
	records, lastSeen, err := s.repository.ActivityRecords(request.Context(), from, to)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	labels, err := s.repository.ListActivityLabels(request.Context())
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	result := activity.Build(records, from, to)
	for device, last := range lastSeen {
		result.LastRecord[device] = last
	}
	plan, err := s.repository.LatestPlan(request.Context(), time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC))
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	coding, err := s.repository.CodingRecords(request.Context(), from)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	summaryLabels := store.LabelsForSummary(labels)
	writeJSON(writer, http.StatusOK, map[string]any{
		"from": from, "to": to, "timezone": location.String(),
		"devices":     activity.Summarize(result, 10, summaryLabels),
		"no_data":     result.Gaps,
		"plan_exists": plan != nil,
		"plan_review": activity.ReviewPlan(store.PlannedBlocks(plan), result, coding, summaryLabels, from, to, 5),
		"insights":    activity.Insights(result, activity.CodingThreads(coding, from, to), summaryLabels),
	})
}

// deviceDay gives a paired device one local day: the plan with the time
// recorded inside each block, time on distractions, and the day's activity
// compressed into chunks. With segments=1 each chunk also carries the
// records behind it, for the details page.
func (s *Server) deviceDay(writer http.ResponseWriter, request *http.Request) {
	if !s.pairedDeviceAuthorized(request) && !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "a paired device credential is required")
		return
	}
	ctx := request.Context()
	config := policy.Config{}
	stored, err := s.repository.LatestPolicy(ctx)
	if err == nil && stored != nil {
		config = stored.Config
	}
	if err == nil {
		err = config.Normalize()
	}
	var location *time.Location
	if err == nil {
		location, err = time.LoadLocation(config.Timezone)
	}
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	now := time.Now().UTC()
	day := now.In(location)
	if value := request.URL.Query().Get("date"); value != "" {
		if day, err = time.ParseInLocation("2006-01-02", value, location); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_query", "date must be YYYY-MM-DD")
			return
		}
	}
	from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, location).UTC()
	to := from.AddDate(0, 0, 1)
	if to.After(now) {
		to = now
	}
	if !to.After(from) {
		writeError(writer, http.StatusBadRequest, "invalid_query", "date is in the future")
		return
	}
	records, _, err := s.repository.ActivityRecords(ctx, from, to)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	labels, err := s.repository.ListActivityLabels(ctx)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	plan, err := s.repository.LatestPlan(ctx, time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC))
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	coding, err := s.repository.CodingRecords(ctx, from)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	rules, err := s.repository.ListActivityContextRules(ctx, false)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"date": day.Format("2006-01-02"), "timezone": location.String(), "from": from, "to": to, "generated_at": now, "plan_exists": plan != nil,
		"day": activity.BuildDay(store.PlannedBlocks(plan), activity.Build(records, from, to), store.LabelsForSummary(labels), activity.ThreadMessages(coding), store.TaskRules(rules), from, to,
			request.URL.Query().Get("segments") == "1"),
	})
}

// policyProposals lists blocklist proposals for the owner.
func (s *Server) policyProposals(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	proposals, err := s.repository.ListPolicyProposals(request.Context(), "", 30)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"proposals": proposals})
}

func (s *Server) acceptPolicyProposal(writer http.ResponseWriter, request *http.Request) {
	s.decidePolicyProposal(writer, request, true)
}

func (s *Server) rejectPolicyProposal(writer http.ResponseWriter, request *http.Request) {
	s.decidePolicyProposal(writer, request, false)
}

// decidePolicyProposal is the only way a blocklist proposal is applied.
func (s *Server) decidePolicyProposal(writer http.ResponseWriter, request *http.Request, accept bool) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
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
	proposal, err := s.repository.DecidePolicyProposal(request.Context(), id, accept, decision, ownerDecisionProvenance)
	if errors.Is(err, store.ErrInvalidPolicy) {
		writeError(writer, http.StatusBadRequest, "invalid_policy", err.Error())
		return
	}
	if err != nil {
		writeProposalError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, proposal)
}
