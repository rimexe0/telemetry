package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/rimexe0/telemetry/internal/store"
)

const (
	maxAlignmentSources      = 16
	maxAlignmentObservations = 1000
	maxAlignmentHistoryDays  = 31
	maxAlignmentResults      = 200
	maxAlignmentInterleaves  = 100
)

var istanbulPlanLocation = time.FixedZone("Europe/Istanbul", 3*60*60)

func (s *Server) alignmentCurrent(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	at, err := alignmentAt(request)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	view, err := s.loadAlignmentAt(request.Context(), at)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"alignment": view})
}

func (s *Server) alignmentHistory(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	window, err := alignmentWindow(request)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	limit, err := limit(request, 50)
	if err != nil || limit > maxAlignmentResults {
		if err == nil {
			err = errors.New("limit must be an integer from 1 through 200")
		}
		writeError(writer, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	views, err := s.loadAlignmentHistory(request.Context(), window.from, window.to, limit)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"alignments": views})
}

func (s *Server) executionSummary(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	window, err := alignmentWindow(request)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	sessions, sessionsLimited, err := s.executionSessionsInWindow(request.Context(), window.from, window.to)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	inputs, err := s.executionMetricInputs(request.Context(), sessions)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	summary := store.DeriveExecutionSummary(window.from, window.to, inputs)
	alignments, err := s.loadAlignmentHistory(request.Context(), window.from, window.to, maxAlignmentResults)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	for _, alignment := range alignments {
		if alignment.AlignmentState == store.AlignmentPossibleDrift {
			summary.PossibleUnplannedSwitchCount++
			summary.PossibleUnplannedSwitches = append(summary.PossibleUnplannedSwitches, alignment)
		}
	}
	if sessionsLimited || len(alignments) == maxAlignmentResults {
		summary.Coverage = "bounded"
		summary.ReturnRate = nil
		summary.AbandonmentRate = nil
		summary.RecoverySameTaskReturnRate = nil
	}
	writeJSON(writer, http.StatusOK, map[string]any{"summary": summary})
}

func (s *Server) loadAlignmentAt(ctx context.Context, at time.Time) (store.AlignmentView, error) {
	planDate := alignmentPlanDate(at)
	plan, err := s.repository.LatestPlan(ctx, planDate)
	if err != nil {
		return store.AlignmentView{}, err
	}
	if plan == nil {
		return store.EvaluateAlignment(store.AlignmentContext{At: at, Now: time.Now().UTC(), PlanDate: planDate.Format("2006-01-02")}), nil
	}
	block := currentPlanBlock(plan, at)
	if block == nil {
		return store.EvaluateAlignment(store.AlignmentContext{At: at, Now: time.Now().UTC(), PlanDate: planDate.Format("2006-01-02"), PlanVersionID: plan.VersionID}), nil
	}
	activities, err := s.repository.ListActivities(ctx, store.ActivityFilter{Limit: 200})
	if err != nil {
		return store.AlignmentView{}, err
	}
	activitiesLimited := len(activities) == 200
	contextRules, err := s.repository.ListActivityContextRules(ctx, false)
	if err != nil {
		return store.AlignmentView{}, err
	}
	sources, err := s.repository.ListSources(ctx, 1000)
	if err != nil {
		return store.AlignmentView{}, err
	}
	observations, coverageLimited, err := s.alignmentObservations(ctx, block.StartTime, blockEnd(*block), sources)
	if err != nil {
		return store.AlignmentView{}, err
	}
	primarySession, attendedSession, interleaves, err := s.executionAlignmentAt(ctx, at)
	if err != nil {
		return store.AlignmentView{}, err
	}
	view := store.EvaluateAlignment(store.AlignmentContext{
		At:              at,
		Now:             time.Now().UTC(),
		PlanDate:        planDate.Format("2006-01-02"),
		PlanVersionID:   plan.VersionID,
		Block:           block,
		Activity:        activityForBlock(activities, block),
		Activities:      activities,
		Session:         primarySession,
		PrimarySession:  primarySession,
		AttendedSession: attendedSession,
		Interleaves:     interleaves,
		Sources:         sources,
		Observations:    observations,
		ContextRules:    contextRules,
	})
	return alignmentCoverageResult(view, coverageLimited || activitiesLimited), nil
}

func (s *Server) loadAlignmentHistory(ctx context.Context, from, to time.Time, limit int) ([]store.AlignmentView, error) {
	activities, err := s.repository.ListActivities(ctx, store.ActivityFilter{Limit: 200})
	if err != nil {
		return nil, err
	}
	activitiesLimited := len(activities) == 200
	contextRules, err := s.repository.ListActivityContextRules(ctx, false)
	if err != nil {
		return nil, err
	}
	sources, err := s.repository.ListSources(ctx, 1000)
	if err != nil {
		return nil, err
	}
	observations, coverageLimited, err := s.alignmentObservations(ctx, from, to, sources)
	if err != nil {
		return nil, err
	}
	sessions, sessionsLimited, err := s.executionSessionsInWindow(ctx, from, to)
	if err != nil {
		return nil, err
	}
	firstDay := alignmentPlanDate(from)
	lastDay := alignmentPlanDate(to.Add(-time.Nanosecond))
	views := make([]store.AlignmentView, 0)
	for day := firstDay; !day.After(lastDay); day = day.AddDate(0, 0, 1) {
		plan, err := s.repository.LatestPlan(ctx, day)
		if err != nil {
			return nil, err
		}
		if plan == nil {
			continue
		}
		for index := range plan.Blocks {
			block := plan.Blocks[index]
			end := blockEnd(block)
			if !block.StartTime.Before(to) || !end.After(from) {
				continue
			}
			evaluatedAt := maxTime(block.StartTime, from)
			primarySession, _ := executionSessionSnapshotAt(sessionForBlock(sessions, block), evaluatedAt)
			interleaves := executionInterleavesForBlock(sessions, primarySession, evaluatedAt)
			attendedSession := primarySession
			for index := range interleaves {
				if interleaves[index].State == "active" {
					attendedSession = &interleaves[index]
					break
				}
			}
			if attendedSession == primarySession && primarySession != nil && primarySession.State != "active" {
				attendedSession = nil
			}
			view := store.EvaluateAlignment(store.AlignmentContext{
				At:              evaluatedAt,
				Now:             time.Now().UTC(),
				PlanDate:        day.Format("2006-01-02"),
				PlanVersionID:   plan.VersionID,
				Block:           &block,
				Activity:        activityForBlock(activities, &block),
				Activities:      activities,
				Session:         primarySession,
				PrimarySession:  primarySession,
				AttendedSession: attendedSession,
				Interleaves:     interleaves,
				Sources:         sources,
				Observations:    observations,
				ContextRules:    contextRules,
			})
			views = append(views, alignmentCoverageResult(view, coverageLimited || sessionsLimited || activitiesLimited))
			if len(views) == limit {
				return views, nil
			}
		}
	}
	sort.SliceStable(views, func(left, right int) bool { return views[left].EvaluatedAt.Before(views[right].EvaluatedAt) })
	return views, nil
}

func (s *Server) alignmentObservations(ctx context.Context, from, to time.Time, sources []store.SourceSummary) ([]store.ObservationView, bool, error) {
	relevant := make([]store.SourceSummary, 0)
	for _, source := range sources {
		if source.SourceKind == "activitywatch" || source.SourceKind == "android_usage" {
			relevant = append(relevant, source)
		}
	}
	if len(relevant) > maxAlignmentSources {
		return nil, true, nil
	}
	observations := make([]store.ObservationView, 0)
	coverageLimited := false
	for _, source := range relevant {
		rows, err := s.repository.QueryObservations(ctx, store.Query{SourceInstanceKey: source.SourceInstanceKey, From: &from, To: &to, Limit: maxAlignmentObservations})
		if err != nil {
			return nil, false, err
		}
		if len(rows) == maxAlignmentObservations {
			coverageLimited = true
		}
		observations = append(observations, rows...)
	}
	return observations, coverageLimited, nil
}

func (s *Server) executionSessionAt(ctx context.Context, at time.Time) (*store.ExecutionSession, error) {
	primary, err := s.repository.CurrentPrimaryExecutionSession(ctx)
	if err != nil {
		return nil, err
	}
	if sessionContainsTime(primary, at) {
		return primary, nil
	}
	current, err := s.repository.CurrentExecutionSession(ctx)
	if err != nil {
		return nil, err
	}
	if sessionContainsTime(current, at) {
		return current, nil
	}
	sessions, err := s.repository.ListExecutionSessions(ctx, maxAlignmentResults)
	if err != nil {
		return nil, err
	}
	for _, candidate := range sessions {
		if !sessionContainsTime(&candidate, at) {
			continue
		}
		return s.repository.GetExecutionSession(ctx, candidate.ID)
	}
	return nil, nil
}

func (s *Server) executionAlignmentAt(ctx context.Context, at time.Time) (*store.ExecutionSession, *store.ExecutionSession, []store.ExecutionSession, error) {
	current, err := s.executionSessionAt(ctx, at)
	if err != nil || current == nil {
		return nil, nil, nil, err
	}
	primary := current
	if current.Role == store.ExecutionRoleWaitingInterleave {
		if current.PrimarySessionID == nil {
			return nil, nil, nil, store.ErrInvalidExecutionInput
		}
		primary, err = s.repository.GetExecutionSession(ctx, *current.PrimarySessionID)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	primary, _ = executionSessionSnapshotAt(primary, at)
	if primary == nil {
		return nil, nil, nil, nil
	}
	if primary.Role != "" && primary.Role != store.ExecutionRolePrimary {
		return nil, nil, nil, store.ErrInvalidExecutionInput
	}
	interleaves, err := s.repository.ListExecutionInterleaves(ctx, primary.ID, maxAlignmentInterleaves)
	if err != nil {
		return nil, nil, nil, err
	}
	seen := make(map[int64]struct{}, len(interleaves))
	related := make([]store.ExecutionSession, 0, len(interleaves))
	for _, interleave := range interleaves {
		if !sessionContainsTime(&interleave, at) {
			continue
		}
		snapshot, ok := executionSessionSnapshotAt(&interleave, at)
		if !ok {
			continue
		}
		seen[snapshot.ID] = struct{}{}
		related = append(related, *snapshot)
	}
	if current.Role == store.ExecutionRoleWaitingInterleave && sessionContainsTime(current, at) {
		if _, ok := seen[current.ID]; !ok {
			if snapshot, exists := executionSessionSnapshotAt(current, at); exists {
				related = append(related, *snapshot)
			}
		}
	}
	var attended *store.ExecutionSession
	for index := range related {
		if related[index].State == "active" && related[index].EndedAt == nil {
			attended = &related[index]
			break
		}
	}
	if attended == nil && primary.State == "active" && sessionContainsTime(primary, at) {
		attended = primary
	}
	return primary, attended, related, nil
}

func (s *Server) executionSessionsInWindow(ctx context.Context, from, to time.Time) ([]store.ExecutionSession, bool, error) {
	sessions, err := s.repository.ListExecutionSessions(ctx, maxAlignmentResults)
	if err != nil {
		return nil, false, err
	}
	sessionsLimited := len(sessions) >= maxAlignmentResults && len(sessions) > 0 && sessionOverlapsWindow(sessions[len(sessions)-1], from, to)
	result := make([]store.ExecutionSession, 0, len(sessions))
	for _, candidate := range sessions {
		if !sessionOverlapsWindow(candidate, from, to) {
			continue
		}
		full, err := s.repository.GetExecutionSession(ctx, candidate.ID)
		if err != nil {
			return nil, false, err
		}
		if full != nil {
			result = append(result, *full)
		}
	}
	return result, sessionsLimited, nil
}

func (s *Server) executionMetricInputs(ctx context.Context, sessions []store.ExecutionSession) ([]store.ExecutionMetricInput, error) {
	blockIDs := make([]int64, 0, len(sessions))
	seenBlockIDs := make(map[int64]struct{}, len(sessions))
	for _, session := range sessions {
		if session.PlanBlockID != nil {
			if _, ok := seenBlockIDs[*session.PlanBlockID]; !ok {
				seenBlockIDs[*session.PlanBlockID] = struct{}{}
				blockIDs = append(blockIDs, *session.PlanBlockID)
			}
		}
	}
	blocks, err := s.repository.PlanBlocksByIDs(ctx, blockIDs)
	if err != nil {
		return nil, err
	}
	blocksByID := make(map[int64]store.PlanBlock, len(blocks))
	for _, block := range blocks {
		blocksByID[block.ID] = block
	}
	inputs := make([]store.ExecutionMetricInput, 0, len(sessions))
	for _, session := range sessions {
		input := store.ExecutionMetricInput{Session: session}
		if session.PlanBlockID != nil {
			if block, ok := blocksByID[*session.PlanBlockID]; ok {
				start := block.StartTime
				input.PlannedStartAt = &start
				input.PlannedDurationSeconds = int64(block.DurationMinutes) * 60
			}
		}
		inputs = append(inputs, input)
	}
	return inputs, nil
}

func alignmentCoverageResult(view store.AlignmentView, limited bool) store.AlignmentView {
	if limited && view.AlignmentState != store.AlignmentAligned {
		view.AlignmentState = store.AlignmentUnknown
		view.ReasonCode = store.AlignmentReasonAmbiguousEvidence
		view.Reason = "The bounded evidence query reached its limit; alignment is unknown."
		view.Coverage = "bounded"
		view.FreshnessNote += "; evidence query bounded"
	}
	return view
}

func alignmentAt(request *http.Request) (time.Time, error) {
	value := request.URL.Query().Get("at")
	if value == "" {
		return time.Now().UTC(), nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, errors.New("at must be RFC3339")
	}
	return parsed.UTC(), nil
}

type alignmentTimeWindow struct {
	from time.Time
	to   time.Time
}

func alignmentWindow(request *http.Request) (alignmentTimeWindow, error) {
	fromValue := request.URL.Query().Get("from")
	toValue := request.URL.Query().Get("to")
	if fromValue == "" || toValue == "" {
		return alignmentTimeWindow{}, errors.New("from and to are required RFC3339 values")
	}
	from, err := time.Parse(time.RFC3339, fromValue)
	if err != nil {
		return alignmentTimeWindow{}, errors.New("from must be RFC3339")
	}
	to, err := time.Parse(time.RFC3339, toValue)
	if err != nil {
		return alignmentTimeWindow{}, errors.New("to must be RFC3339")
	}
	from, to = from.UTC(), to.UTC()
	if !from.Before(to) {
		return alignmentTimeWindow{}, errors.New("from must be before to")
	}
	if to.Sub(from) > maxAlignmentHistoryDays*24*time.Hour {
		return alignmentTimeWindow{}, fmt.Errorf("alignment window cannot exceed %d days", maxAlignmentHistoryDays)
	}
	return alignmentTimeWindow{from: from, to: to}, nil
}

func alignmentPlanDate(at time.Time) time.Time {
	local := at.In(istanbulPlanLocation)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
}

func currentPlanBlock(plan *store.Plan, at time.Time) *store.PlanBlock {
	if plan == nil {
		return nil
	}
	for index := range plan.Blocks {
		block := &plan.Blocks[index]
		if !at.Before(block.StartTime) && at.Before(blockEnd(*block)) {
			return block
		}
	}
	return nil
}

func activityForBlock(activities []store.Activity, block *store.PlanBlock) *store.Activity {
	if block == nil || block.ActivityID == nil {
		return nil
	}
	for index := range activities {
		if activities[index].ID == *block.ActivityID {
			return &activities[index]
		}
	}
	return nil
}

func sessionForBlock(sessions []store.ExecutionSession, block store.PlanBlock) *store.ExecutionSession {
	var selected *store.ExecutionSession
	selectedPriority := 0
	for index := range sessions {
		session := &sessions[index]
		if session.Role == store.ExecutionRoleWaitingInterleave {
			continue
		}
		if !sessionOverlapsBlock(*session, block) {
			continue
		}
		priority := 1
		if session.PlanBlockID != nil && *session.PlanBlockID == block.ID {
			priority = 2
		}
		if selected == nil || priority > selectedPriority || (priority == selectedPriority && session.StartedAt.After(selected.StartedAt)) {
			selected = session
			selectedPriority = priority
		}
	}
	return selected
}

func executionInterleavesForBlock(sessions []store.ExecutionSession, primary *store.ExecutionSession, at time.Time) []store.ExecutionSession {
	if primary == nil {
		return nil
	}
	interleaves := make([]store.ExecutionSession, 0)
	for _, session := range sessions {
		if session.Role != store.ExecutionRoleWaitingInterleave || session.PrimarySessionID == nil ||
			*session.PrimarySessionID != primary.ID || !sessionContainsTime(&session, at) {
			continue
		}
		if snapshot, ok := executionSessionSnapshotAt(&session, at); ok {
			interleaves = append(interleaves, *snapshot)
		}
	}
	return interleaves
}

func executionSessionSnapshotAt(session *store.ExecutionSession, at time.Time) (*store.ExecutionSession, bool) {
	if session == nil {
		return nil, false
	}
	state, ok := store.ExecutionSessionStateAt(*session, at)
	if !ok {
		return nil, false
	}
	snapshot := *session
	snapshot.State = state
	if state != "completed" && state != "abandoned" {
		snapshot.EndedAt = nil
	}
	return &snapshot, true
}

func sessionContainsTime(session *store.ExecutionSession, at time.Time) bool {
	if session == nil || session.StartedAt.After(at) {
		return false
	}
	return session.EndedAt == nil || !at.After(*session.EndedAt)
}

func sessionOverlapsWindow(session store.ExecutionSession, from, to time.Time) bool {
	return session.StartedAt.Before(to) && (session.EndedAt == nil || session.EndedAt.After(from))
}

func sessionOverlapsBlock(session store.ExecutionSession, block store.PlanBlock) bool {
	return sessionOverlapsWindow(session, block.StartTime, blockEnd(block))
}

func blockEnd(block store.PlanBlock) time.Time {
	return block.StartTime.Add(time.Duration(block.DurationMinutes) * time.Minute)
}

func maxTime(left, right time.Time) time.Time {
	if left.After(right) {
		return left
	}
	return right
}
