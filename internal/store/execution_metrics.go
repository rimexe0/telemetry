package store

import "time"

type ExecutionMetricInput struct {
	Session                ExecutionSession
	PlannedStartAt         *time.Time
	PlannedDurationSeconds int64
}

type ExecutionSummary struct {
	From                                   time.Time                `json:"from"`
	To                                     time.Time                `json:"to"`
	Coverage                               string                   `json:"coverage"`
	Sessions                               int                      `json:"sessions"`
	Completed                              int                      `json:"completed"`
	Abandoned                              int                      `json:"abandoned"`
	TerminalSessionCount                   int                      `json:"terminal_session_count"`
	AbandonmentRate                        *float64                 `json:"abandonment_rate"`
	InitiationLatenciesSeconds             []int64                  `json:"initiation_latencies_seconds"`
	InterruptionCount                      int                      `json:"interruption_count"`
	IntentionalSwitchCount                 int                      `json:"intentional_switch_count"`
	WaitingExternalCount                   int                      `json:"waiting_external_count"`
	ReturnOpportunityCount                 int                      `json:"return_opportunity_count"`
	ReturnCount                            int                      `json:"return_count"`
	ReturnRate                             *float64                 `json:"return_rate"`
	ReturnLatenciesSeconds                 []int64                  `json:"return_latencies_seconds"`
	StuckUnknownNextActionCount            int                      `json:"stuck_unknown_next_action_count"`
	StuckEmotionalOverwhelmCount           int                      `json:"stuck_emotional_overwhelm_count"`
	OverwhelmedCount                       int                      `json:"overwhelmed_count"`
	RecoveryCount                          int                      `json:"recovery_count"`
	RecoveryDurationsSeconds               []int64                  `json:"recovery_durations_seconds"`
	RecoverySameTaskReturnCount            int                      `json:"recovery_same_task_return_count"`
	RecoverySameTaskReturnRate             *float64                 `json:"recovery_same_task_return_rate"`
	PlannedSeconds                         int64                    `json:"planned_seconds"`
	SessionElapsedSeconds                  int64                    `json:"session_elapsed_seconds"`
	SessionElapsedCount                    int                      `json:"session_elapsed_count"`
	SessionElapsedWithNonactiveEventsCount int                      `json:"session_elapsed_with_nonactive_events_count"`
	DurationEstimationErrors               []ExecutionDurationError `json:"duration_estimation_errors"`
	PossibleUnplannedSwitchCount           int                      `json:"possible_unplanned_switch_count"`
	PossibleUnplannedSwitches              []AlignmentView          `json:"possible_unplanned_switches"`
	MetricSources                          []ExecutionMetricSource  `json:"metric_sources"`
	MetricSourcesTruncated                 bool                     `json:"metric_sources_truncated"`
}

type ExecutionMetricSource struct {
	Metric      string  `json:"metric"`
	SessionID   int64   `json:"session_id"`
	PlanBlockID *int64  `json:"plan_block_id,omitempty"`
	EventIDs    []int64 `json:"event_ids"`
	Value       int64   `json:"value"`
	Unit        string  `json:"unit"`
}

type ExecutionDurationError struct {
	SessionID          int64  `json:"session_id"`
	PlanBlockID        *int64 `json:"plan_block_id,omitempty"`
	PlannedSeconds     int64  `json:"planned_seconds"`
	SessionElapsed     int64  `json:"session_elapsed_seconds"`
	ErrorSeconds       int64  `json:"error_seconds"`
	HasNonactiveEvents bool   `json:"has_nonactive_events"`
}

const maxExecutionMetricSources = 5000

func DeriveExecutionSummary(from, to time.Time, inputs []ExecutionMetricInput) ExecutionSummary {
	summary := ExecutionSummary{
		From:                       from.UTC(),
		To:                         to.UTC(),
		Coverage:                   "complete",
		InitiationLatenciesSeconds: []int64{},
		ReturnLatenciesSeconds:     []int64{},
		RecoveryDurationsSeconds:   []int64{},
		DurationEstimationErrors:   []ExecutionDurationError{},
		PossibleUnplannedSwitches:  []AlignmentView{},
		MetricSources:              []ExecutionMetricSource{},
	}
	for _, input := range inputs {
		session := input.Session
		summary.Sessions++
		addExecutionMetricSource(&summary, "sessions", session, 1, "count")
		switch session.State {
		case executionStateCompleted:
			summary.Completed++
			summary.TerminalSessionCount++
			events := matchingExecutionEvents(session.Events, executionEventCompleted)
			addExecutionMetricSource(&summary, "completed", session, 1, "count", events...)
			addExecutionMetricSource(&summary, "terminal_sessions", session, 1, "count", events...)
		case executionStateAbandoned:
			summary.Abandoned++
			summary.TerminalSessionCount++
			events := matchingExecutionEvents(session.Events, executionEventAbandoned)
			addExecutionMetricSource(&summary, "abandoned", session, 1, "count", events...)
			addExecutionMetricSource(&summary, "terminal_sessions", session, 1, "count", events...)
		}
		if input.PlannedStartAt != nil {
			latency := int64(session.StartedAt.Sub(input.PlannedStartAt.UTC()).Seconds())
			summary.InitiationLatenciesSeconds = append(summary.InitiationLatenciesSeconds, latency)
			addExecutionMetricSource(&summary, "initiation_latency_seconds", session, latency, "seconds", matchingExecutionEvents(session.Events, executionEventStarted)...)
		}
		if session.EndedAt != nil && !session.EndedAt.Before(session.StartedAt) {
			sessionElapsed := int64(session.EndedAt.Sub(session.StartedAt).Seconds())
			summary.SessionElapsedSeconds += sessionElapsed
			summary.SessionElapsedCount++
			addExecutionMetricSource(&summary, "session_elapsed_seconds", session, sessionElapsed, "seconds")
			addExecutionMetricSource(&summary, "session_elapsed_count", session, 1, "count")
			if input.PlannedDurationSeconds > 0 {
				summary.PlannedSeconds += input.PlannedDurationSeconds
				addExecutionMetricSource(&summary, "planned_seconds", session, input.PlannedDurationSeconds, "seconds")
			}
		}

		var pendingReturnAt *time.Time
		var pendingReturnEvent *ExecutionEvent
		type recoveryStart struct {
			at    time.Time
			event ExecutionEvent
		}
		recoveryStartedAt := make([]recoveryStart, 0)
		recoveryPendingReturn := false
		recoveryPendingEvents := make([]ExecutionEvent, 0)
		hasNonactiveEvents := false
		for _, event := range session.Events {
			switch event.EventType {
			case executionEventPaused, executionEventInterrupted:
				summary.InterruptionCount++
				addExecutionMetricSource(&summary, "interruption_count", session, 1, "count", event)
				hasNonactiveEvents = true
				if pendingReturnAt == nil {
					summary.ReturnOpportunityCount++
					occurredAt := event.OccurredAt
					pendingReturnAt = &occurredAt
					pendingReturnEvent = &event
					addExecutionMetricSource(&summary, "return_opportunity_count", session, 1, "count", event)
				}
			case executionEventWaitingExternal:
				summary.WaitingExternalCount++
				addExecutionMetricSource(&summary, "waiting_external_count", session, 1, "count", event)
				hasNonactiveEvents = true
				if pendingReturnAt == nil {
					summary.ReturnOpportunityCount++
					occurredAt := event.OccurredAt
					pendingReturnAt = &occurredAt
					pendingReturnEvent = &event
					addExecutionMetricSource(&summary, "return_opportunity_count", session, 1, "count", event)
				}
			case executionEventSwitchedOut:
				summary.IntentionalSwitchCount++
				addExecutionMetricSource(&summary, "intentional_switch_count", session, 1, "count", event)
				hasNonactiveEvents = true
				if pendingReturnAt == nil {
					summary.ReturnOpportunityCount++
					occurredAt := event.OccurredAt
					pendingReturnAt = &occurredAt
					pendingReturnEvent = &event
					addExecutionMetricSource(&summary, "return_opportunity_count", session, 1, "count", event)
				}
			case executionEventOfflineRecoveryStarted:
				summary.RecoveryCount++
				addExecutionMetricSource(&summary, "recovery_count", session, 1, "count", event)
				hasNonactiveEvents = true
				if pendingReturnAt == nil {
					summary.ReturnOpportunityCount++
					occurredAt := event.OccurredAt
					pendingReturnAt = &occurredAt
					pendingReturnEvent = &event
					addExecutionMetricSource(&summary, "return_opportunity_count", session, 1, "count", event)
				}
				recoveryStartedAt = append(recoveryStartedAt, recoveryStart{at: event.OccurredAt, event: event})
				recoveryPendingReturn = true
				recoveryPendingEvents = append(recoveryPendingEvents, event)
			case executionEventOfflineRecoveryEnded:
				if len(recoveryStartedAt) > 0 {
					started := recoveryStartedAt[0]
					recoveryStartedAt = recoveryStartedAt[1:]
					if !event.OccurredAt.Before(started.at) {
						duration := int64(event.OccurredAt.Sub(started.at).Seconds())
						summary.RecoveryDurationsSeconds = append(summary.RecoveryDurationsSeconds, duration)
						addExecutionMetricSource(&summary, "recovery_duration_seconds", session, duration, "seconds", started.event, event)
					}
				}
			case executionEventStuckUnknownNextAction:
				summary.StuckUnknownNextActionCount++
				addExecutionMetricSource(&summary, "stuck_unknown_next_action_count", session, 1, "count", event)
			case executionEventStuckEmotionalOverwhelm:
				summary.StuckEmotionalOverwhelmCount++
				addExecutionMetricSource(&summary, "stuck_emotional_overwhelm_count", session, 1, "count", event)
			case executionEventOverwhelmed:
				summary.OverwhelmedCount++
				addExecutionMetricSource(&summary, "overwhelmed_count", session, 1, "count", event)
			case executionEventResumed:
				if pendingReturnAt != nil && !event.OccurredAt.Before(*pendingReturnAt) {
					summary.ReturnCount++
					latency := int64(event.OccurredAt.Sub(*pendingReturnAt).Seconds())
					summary.ReturnLatenciesSeconds = append(summary.ReturnLatenciesSeconds, latency)
					if pendingReturnEvent != nil {
						addExecutionMetricSource(&summary, "return_count", session, 1, "count", *pendingReturnEvent, event)
						addExecutionMetricSource(&summary, "return_latency_seconds", session, latency, "seconds", *pendingReturnEvent, event)
					}
					if recoveryPendingReturn {
						summary.RecoverySameTaskReturnCount++
						events := append(append([]ExecutionEvent(nil), recoveryPendingEvents...), event)
						addExecutionMetricSource(&summary, "recovery_same_task_return_count", session, 1, "count", events...)
					}
				}
				pendingReturnAt = nil
				pendingReturnEvent = nil
				recoveryPendingReturn = false
				recoveryPendingEvents = nil
			}
		}
		if session.EndedAt != nil && !session.EndedAt.Before(session.StartedAt) {
			if hasNonactiveEvents {
				summary.SessionElapsedWithNonactiveEventsCount++
				addExecutionMetricSource(&summary, "session_elapsed_with_nonactive_events_count", session, 1, "count", nonactiveExecutionEvents(session.Events)...)
			}
			if session.State == executionStateCompleted && input.PlannedDurationSeconds > 0 {
				errorSeconds := int64(session.EndedAt.Sub(session.StartedAt).Seconds()) - input.PlannedDurationSeconds
				summary.DurationEstimationErrors = append(summary.DurationEstimationErrors, ExecutionDurationError{
					SessionID:          session.ID,
					PlanBlockID:        session.PlanBlockID,
					PlannedSeconds:     input.PlannedDurationSeconds,
					SessionElapsed:     int64(session.EndedAt.Sub(session.StartedAt).Seconds()),
					ErrorSeconds:       errorSeconds,
					HasNonactiveEvents: hasNonactiveEvents,
				})
				addExecutionMetricSource(&summary, "duration_estimation_error_seconds", session, errorSeconds, "seconds", matchingExecutionEvents(session.Events, executionEventCompleted)...)
			}
		}
	}
	summary.ReturnRate = executionMetricRate(summary.ReturnCount, summary.ReturnOpportunityCount)
	summary.AbandonmentRate = executionMetricRate(summary.Abandoned, summary.TerminalSessionCount)
	summary.RecoverySameTaskReturnRate = executionMetricRate(summary.RecoverySameTaskReturnCount, summary.RecoveryCount)
	if summary.MetricSourcesTruncated {
		summary.Coverage = "bounded"
		summary.ReturnRate = nil
		summary.AbandonmentRate = nil
		summary.RecoverySameTaskReturnRate = nil
	}
	return summary
}

func addExecutionMetricSource(summary *ExecutionSummary, metric string, session ExecutionSession, value int64, unit string, events ...ExecutionEvent) {
	if len(summary.MetricSources) >= maxExecutionMetricSources {
		summary.MetricSourcesTruncated = true
		return
	}
	eventIDs := make([]int64, 0, len(events))
	for _, event := range events {
		if event.ID > 0 {
			eventIDs = append(eventIDs, event.ID)
		}
	}
	summary.MetricSources = append(summary.MetricSources, ExecutionMetricSource{
		Metric:      metric,
		SessionID:   session.ID,
		PlanBlockID: session.PlanBlockID,
		EventIDs:    eventIDs,
		Value:       value,
		Unit:        unit,
	})
}

func matchingExecutionEvents(events []ExecutionEvent, eventType string) []ExecutionEvent {
	matches := make([]ExecutionEvent, 0)
	for _, event := range events {
		if event.EventType == eventType {
			matches = append(matches, event)
		}
	}
	return matches
}

func nonactiveExecutionEvents(events []ExecutionEvent) []ExecutionEvent {
	result := make([]ExecutionEvent, 0)
	for _, event := range events {
		switch event.EventType {
		case executionEventPaused, executionEventInterrupted, executionEventWaitingExternal, executionEventSwitchedOut, executionEventOfflineRecoveryStarted:
			result = append(result, event)
		}
	}
	return result
}

func executionMetricRate(numerator, denominator int) *float64 {
	if denominator == 0 {
		return nil
	}
	rate := float64(numerator) / float64(denominator)
	return &rate
}
