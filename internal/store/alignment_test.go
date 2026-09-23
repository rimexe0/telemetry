package store

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEvaluateAlignmentConservativeRules(t *testing.T) {
	now := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	block := alignmentTestBlock()
	activity := &Activity{ID: 1, Title: "Write the outline", NextAction: "Open the outline file"}
	fresh := []SourceSummary{{
		SourceInstanceKey: "activitywatch:mac:window",
		SourceKind:        "activitywatch",
		DeviceID:          "mac",
		LastCollectedAt:   timePtr(now.Add(-2 * time.Minute)),
	}}
	matched := []ObservationView{alignmentObservation(`{"data":{"app":"Code","title":"Write the outline"}}`)}
	unrelated := []ObservationView{alignmentObservation(`{"data":{"app":"YouTube","title":"Music"}}`)}
	ambiguous := []ObservationView{alignmentObservation(`{"data":{"app":"Code"}}`)}

	tests := []struct {
		name       string
		context    AlignmentContext
		wantState  string
		wantReason string
	}{
		{
			name:       "linked active execution",
			context:    AlignmentContext{Now: now, At: now, PlanDate: "2026-09-23", PlanVersionID: 4, Block: &block, Activity: activity, Session: alignmentSession(7, &block.ID, executionStateActive, executionEventStarted), Sources: fresh, Observations: unrelated},
			wantState:  AlignmentAligned,
			wantReason: AlignmentReasonExecutionMatchesPlan,
		},
		{
			name:       "waiting external remains aligned",
			context:    AlignmentContext{Now: now, At: now, PlanDate: "2026-09-23", PlanVersionID: 4, Block: &block, Activity: activity, Session: alignmentSession(7, &block.ID, executionStateWaitingExternal, executionEventWaitingExternal), Sources: fresh, Observations: unrelated},
			wantState:  AlignmentAligned,
			wantReason: AlignmentReasonWaitingExternal,
		},
		{
			name:       "recovery is not drift",
			context:    AlignmentContext{Now: now, At: now, PlanDate: "2026-09-23", PlanVersionID: 4, Block: &block, Activity: activity, Session: alignmentSession(7, &block.ID, executionStateInterrupted, executionEventOfflineRecoveryStarted), Sources: []SourceSummary{}, Observations: nil},
			wantState:  AlignmentAligned,
			wantReason: AlignmentReasonRecovery,
		},
		{
			name:       "fresh unrelated evidence",
			context:    AlignmentContext{Now: now, At: now, PlanDate: "2026-09-23", PlanVersionID: 4, Block: &block, Activity: activity, Activities: []Activity{{ID: 2, Title: "Music", Status: "active"}}, Sources: fresh, Observations: unrelated},
			wantState:  AlignmentPossibleDrift,
			wantReason: AlignmentReasonActivityMismatch,
		},
		{
			name:       "stale evidence",
			context:    AlignmentContext{Now: now, At: now, PlanDate: "2026-09-23", PlanVersionID: 4, Block: &block, Activity: activity, Sources: []SourceSummary{{SourceInstanceKey: "activitywatch:mac:window", SourceKind: "activitywatch", LastCollectedAt: timePtr(now.Add(-30 * time.Minute))}}, Observations: unrelated},
			wantState:  AlignmentUnknown,
			wantReason: AlignmentReasonStaleEvidence,
		},
		{
			name:       "missing evidence",
			context:    AlignmentContext{Now: now, At: now, PlanDate: "2026-09-23", PlanVersionID: 4, Block: &block, Activity: activity, Sources: []SourceSummary{}, Observations: nil},
			wantState:  AlignmentUnknown,
			wantReason: AlignmentReasonMissingEvidence,
		},
		{
			name:       "ambiguous app evidence",
			context:    AlignmentContext{Now: now, At: now, PlanDate: "2026-09-23", PlanVersionID: 4, Block: &block, Activity: activity, Sources: fresh, Observations: ambiguous},
			wantState:  AlignmentUnknown,
			wantReason: AlignmentReasonAmbiguousEvidence,
		},
		{
			name:       "matching device evidence",
			context:    AlignmentContext{Now: now, At: now, PlanDate: "2026-09-23", PlanVersionID: 4, Block: &block, Activity: activity, Sources: fresh, Observations: matched},
			wantState:  AlignmentAligned,
			wantReason: AlignmentReasonExecutionMatchesPlan,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := EvaluateAlignment(test.context)
			if result.AlignmentState != test.wantState || result.ReasonCode != test.wantReason {
				t.Fatalf("alignment = (%q, %q), want (%q, %q): %+v", result.AlignmentState, result.ReasonCode, test.wantState, test.wantReason, result)
			}
		})
	}
}

func TestEvaluateAlignmentKeepsIntentionalSwitchDistinct(t *testing.T) {
	now := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	block := alignmentTestBlock()
	otherBlock := int64(99)
	result := EvaluateAlignment(AlignmentContext{
		Now:           now,
		At:            now,
		PlanDate:      "2026-09-23",
		PlanVersionID: 4,
		Block:         &block,
		Session:       alignmentSession(7, &otherBlock, executionStateInterrupted, executionEventSwitchedOut),
		Sources:       []SourceSummary{},
	})
	if result.AlignmentState != AlignmentAligned || result.ReasonCode != AlignmentReasonIntentionalSwitch {
		t.Fatalf("intentional switch = %+v", result)
	}
}

func TestEvaluateAlignmentDoesNotCallUnconfiguredWindowEvidenceDrift(t *testing.T) {
	now := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	block := alignmentTestBlock()
	result := EvaluateAlignment(AlignmentContext{
		Now:          now,
		At:           now,
		Block:        &block,
		Activity:     &Activity{ID: 1, Title: "Write the outline"},
		Sources:      []SourceSummary{{SourceKind: "activitywatch", LastCollectedAt: timePtr(now.Add(-time.Minute))}},
		Observations: []ObservationView{alignmentObservation(`{"data":{"app":"YouTube","title":"Music"}}`)},
	})
	if result.AlignmentState != AlignmentUnknown || result.ReasonCode != AlignmentReasonAmbiguousEvidence {
		t.Fatalf("unconfigured window evidence = (%q, %q), want unknown ambiguous evidence", result.AlignmentState, result.ReasonCode)
	}
}

func TestEvaluateAlignmentOnlyCallsConfiguredOtherActivityPossibleDrift(t *testing.T) {
	now := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	block := alignmentTestBlock()
	result := EvaluateAlignment(AlignmentContext{
		Now:          now,
		At:           now,
		Block:        &block,
		Activity:     &Activity{ID: 1, Title: "Write the outline"},
		Activities:   []Activity{{ID: 1, Title: "Write the outline", Status: "active"}, {ID: 2, Title: "Payroll export", Status: "active"}},
		Sources:      []SourceSummary{{SourceKind: "activitywatch", LastCollectedAt: timePtr(now.Add(-time.Minute))}},
		Observations: []ObservationView{alignmentObservation(`{"data":{"app":"Excel","title":"Payroll export"}}`)},
	})
	if result.AlignmentState != AlignmentPossibleDrift || result.ReasonCode != AlignmentReasonActivityMismatch {
		t.Fatalf("configured other activity = (%q, %q), want possible drift", result.AlignmentState, result.ReasonCode)
	}
	if len(result.Evidence) != 1 || result.Evidence[0].RelatedActivityID == nil || *result.Evidence[0].RelatedActivityID != 2 {
		t.Fatalf("configured activity evidence = %+v, want reference to activity 2", result.Evidence)
	}
}

func TestEvaluateAlignmentKeepsConflictingActivityMatchesUnknown(t *testing.T) {
	now := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	block := alignmentTestBlock()
	result := EvaluateAlignment(AlignmentContext{
		Now:      now,
		At:       now,
		Block:    &block,
		Activity: &Activity{ID: 1, Title: "Write the outline"},
		Activities: []Activity{
			{ID: 2, Title: "Payroll export", Status: "active"},
			{ID: 3, Title: "Payroll export", Status: "waiting"},
		},
		Sources:      []SourceSummary{{SourceKind: "activitywatch", LastCollectedAt: timePtr(now.Add(-time.Minute))}},
		Observations: []ObservationView{alignmentObservation(`{"data":{"app":"Excel","title":"Payroll export"}}`)},
	})
	if result.AlignmentState != AlignmentUnknown || result.ReasonCode != AlignmentReasonAmbiguousEvidence {
		t.Fatalf("conflicting activity matches = (%q, %q), want unknown ambiguous evidence", result.AlignmentState, result.ReasonCode)
	}
}

func TestDeriveExecutionSummaryUsesRetainedEventsWithoutProductivityScore(t *testing.T) {
	start := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	ended := start.Add(60 * time.Minute)
	secondStart := start.Add(70 * time.Minute)
	secondEnd := secondStart.Add(10 * time.Minute)
	summary := DeriveExecutionSummary(start, ended, []ExecutionMetricInput{{
		Session: ExecutionSession{
			ID:          7,
			PlanBlockID: timeInt64(10),
			State:       executionStateCompleted,
			StartedAt:   start,
			EndedAt:     &ended,
			Events: []ExecutionEvent{
				{EventType: executionEventStarted, OccurredAt: start},
				{EventType: executionEventInterrupted, OccurredAt: start.Add(10 * time.Minute)},
				{EventType: executionEventWaitingExternal, OccurredAt: start.Add(15 * time.Minute)},
				{EventType: executionEventResumed, OccurredAt: start.Add(20 * time.Minute)},
				{EventType: executionEventSwitchedOut, OccurredAt: start.Add(30 * time.Minute)},
				{EventType: executionEventResumed, OccurredAt: start.Add(40 * time.Minute)},
				{EventType: executionEventOfflineRecoveryStarted, OccurredAt: start.Add(42 * time.Minute)},
				{EventType: executionEventOfflineRecoveryEnded, OccurredAt: start.Add(45 * time.Minute)},
				{EventType: executionEventResumed, OccurredAt: start.Add(46 * time.Minute)},
				{EventType: executionEventStuckUnknownNextAction, OccurredAt: start.Add(5 * time.Minute)},
				{EventType: executionEventStuckEmotionalOverwhelm, OccurredAt: start.Add(6 * time.Minute)},
				{EventType: executionEventOverwhelmed, OccurredAt: start.Add(41 * time.Minute)},
			},
		},
		PlannedStartAt:         timePtr(start.Add(-5 * time.Minute)),
		PlannedDurationSeconds: 3000,
	}, {
		Session: ExecutionSession{
			ID:          9,
			PlanBlockID: timeInt64(11),
			State:       executionStateAbandoned,
			StartedAt:   secondStart,
			EndedAt:     &secondEnd,
			Events: []ExecutionEvent{
				{EventType: executionEventStarted, OccurredAt: secondStart},
				{EventType: executionEventOfflineRecoveryStarted, OccurredAt: secondStart.Add(time.Minute)},
				{EventType: executionEventAbandoned, OccurredAt: secondEnd},
			},
		},
		PlannedDurationSeconds: 300,
	}})

	if summary.Sessions != 2 || summary.Completed != 1 || summary.Abandoned != 1 || summary.InterruptionCount != 1 || summary.IntentionalSwitchCount != 1 {
		t.Fatalf("summary counts = %+v", summary)
	}
	if summary.ReturnCount != 3 || len(summary.ReturnLatenciesSeconds) != 3 || summary.ReturnLatenciesSeconds[0] != 600 || summary.RecoverySameTaskReturnCount != 1 {
		t.Fatalf("return metrics = %+v", summary)
	}
	if len(summary.InitiationLatenciesSeconds) != 1 || summary.InitiationLatenciesSeconds[0] != 300 || summary.RecoveryDurationsSeconds[0] != 180 {
		t.Fatalf("timing metrics = %+v", summary)
	}
	if summary.PlannedSeconds != 3300 {
		t.Fatalf("planned/actual metrics = %+v", summary)
	}
	encoded, err := json.Marshal(summary)
	if err != nil || string(encoded) == "" || containsJSONKey(encoded, "productivity_score") {
		t.Fatalf("summary encoding = %s, %v", encoded, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	var returnOpportunities, terminalSessions, elapsedCount, uncertainElapsedCount int
	var returnRate, abandonmentRate, recoveryReturnRate float64
	var elapsedSeconds int64
	var durationErrors []struct {
		SessionID          int64 `json:"session_id"`
		PlanBlockID        int64 `json:"plan_block_id"`
		PlannedSeconds     int64 `json:"planned_seconds"`
		SessionElapsed     int64 `json:"session_elapsed_seconds"`
		ErrorSeconds       int64 `json:"error_seconds"`
		HasNonactiveEvents bool  `json:"has_nonactive_events"`
	}
	for key, destination := range map[string]any{
		"return_opportunity_count":                    &returnOpportunities,
		"return_rate":                                 &returnRate,
		"terminal_session_count":                      &terminalSessions,
		"abandonment_rate":                            &abandonmentRate,
		"recovery_same_task_return_rate":              &recoveryReturnRate,
		"session_elapsed_seconds":                     &elapsedSeconds,
		"session_elapsed_count":                       &elapsedCount,
		"session_elapsed_with_nonactive_events_count": &uncertainElapsedCount,
		"duration_estimation_errors":                  &durationErrors,
	} {
		if value, ok := fields[key]; !ok || json.Unmarshal(value, destination) != nil {
			t.Fatalf("missing or invalid summary field %s: %s", key, encoded)
		}
	}
	if returnOpportunities != 4 || returnRate != 0.75 || terminalSessions != 2 || abandonmentRate != 0.5 || recoveryReturnRate != 0.5 {
		t.Fatalf("rates and denominators = %s", encoded)
	}
	if elapsedSeconds != 4200 || elapsedCount != 2 || uncertainElapsedCount != 2 || len(durationErrors) != 1 || durationErrors[0].SessionID != 7 || durationErrors[0].PlanBlockID != 10 || durationErrors[0].PlannedSeconds != 3000 || durationErrors[0].SessionElapsed != 3600 || durationErrors[0].ErrorSeconds != 600 || !durationErrors[0].HasNonactiveEvents {
		t.Fatalf("duration comparison = %s", encoded)
	}
}

func containsJSONKey(encoded []byte, key string) bool {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return true
	}
	_, exists := fields[key]
	return exists
}

func TestExecutionSummaryLeavesRatesNullWithoutEligibleEvents(t *testing.T) {
	start := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	summary := DeriveExecutionSummary(start, start.Add(time.Hour), []ExecutionMetricInput{{
		Session: ExecutionSession{ID: 12, State: executionStateActive, StartedAt: start},
	}})
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"return_rate", "abandonment_rate", "recovery_same_task_return_rate"} {
		if string(fields[key]) != "null" {
			t.Fatalf("%s = %s, want null with no denominator", key, fields[key])
		}
	}
}

func TestExecutionSummaryTracesSwitchAndReturnMetricsToRetainedEvents(t *testing.T) {
	start := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	blockID := int64(10)
	summary := DeriveExecutionSummary(start, end, []ExecutionMetricInput{{
		Session: ExecutionSession{
			ID:          7,
			PlanBlockID: &blockID,
			State:       executionStateCompleted,
			StartedAt:   start,
			EndedAt:     &end,
			Events: []ExecutionEvent{
				{ID: 101, SessionID: 7, EventType: executionEventStarted, OccurredAt: start},
				{ID: 102, SessionID: 7, EventType: executionEventInterrupted, OccurredAt: start.Add(10 * time.Minute)},
				{ID: 103, SessionID: 7, EventType: executionEventSwitchedOut, OccurredAt: start.Add(15 * time.Minute)},
				{ID: 104, SessionID: 7, EventType: executionEventResumed, OccurredAt: start.Add(20 * time.Minute)},
				{ID: 105, SessionID: 7, EventType: executionEventCompleted, OccurredAt: end},
			},
		},
		PlannedStartAt: timePtr(start.Add(-5 * time.Minute)),
	}})
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		InterruptionCount      int `json:"interruption_count"`
		IntentionalSwitchCount int `json:"intentional_switch_count"`
		MetricSources          []struct {
			Metric      string  `json:"metric"`
			SessionID   int64   `json:"session_id"`
			PlanBlockID *int64  `json:"plan_block_id"`
			EventIDs    []int64 `json:"event_ids"`
			Value       int64   `json:"value"`
		} `json:"metric_sources"`
	}
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	if result.InterruptionCount != 1 || result.IntentionalSwitchCount != 1 {
		t.Fatalf("switch counts = interruption %d, intentional %d", result.InterruptionCount, result.IntentionalSwitchCount)
	}
	if !hasMetricSource(result.MetricSources, "interruption_count", 7, 10, 102, 1) || !hasMetricSource(result.MetricSources, "intentional_switch_count", 7, 10, 103, 1) || !hasMetricSource(result.MetricSources, "return_latency_seconds", 7, 10, 102, 600) {
		t.Fatalf("metric sources = %+v", result.MetricSources)
	}
}

func TestExecutionSummaryBoundsMetricSourceReferences(t *testing.T) {
	inputs := make([]ExecutionMetricInput, maxExecutionMetricSources+1)
	for index := range inputs {
		inputs[index].Session = ExecutionSession{ID: int64(index + 1), State: executionStateActive}
	}
	start := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	summary := DeriveExecutionSummary(start, start.Add(time.Hour), inputs)
	if len(summary.MetricSources) != maxExecutionMetricSources || !summary.MetricSourcesTruncated || summary.Coverage != "bounded" {
		t.Fatalf("metric source bounds = %d, truncated %v, coverage %q", len(summary.MetricSources), summary.MetricSourcesTruncated, summary.Coverage)
	}
	if summary.ReturnRate != nil || summary.AbandonmentRate != nil || summary.RecoverySameTaskReturnRate != nil {
		t.Fatalf("bounded rates should be unknown: %+v", summary)
	}
}

func hasMetricSource(sources []struct {
	Metric      string  `json:"metric"`
	SessionID   int64   `json:"session_id"`
	PlanBlockID *int64  `json:"plan_block_id"`
	EventIDs    []int64 `json:"event_ids"`
	Value       int64   `json:"value"`
}, metric string, sessionID, planBlockID, eventID, value int64) bool {
	for _, source := range sources {
		if source.Metric != metric || source.SessionID != sessionID || source.PlanBlockID == nil || *source.PlanBlockID != planBlockID || source.Value != value {
			continue
		}
		for _, id := range source.EventIDs {
			if id == eventID {
				return true
			}
		}
	}
	return false
}

func alignmentTestBlock() PlanBlock {
	return PlanBlock{ID: 10, ActivityID: timeInt64(1), Title: "Write the outline", StartTime: time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC), DurationMinutes: 60, State: "planned"}
}

func alignmentSession(id int64, blockID *int64, state, eventType string) *ExecutionSession {
	return &ExecutionSession{ID: id, PlanBlockID: blockID, State: state, Events: []ExecutionEvent{{EventType: executionEventStarted}, {EventType: eventType}}}
}

func alignmentObservation(payload string) ObservationView {
	return ObservationView{SourceInstanceKey: "activitywatch:mac:window", SourceKind: "activitywatch", SourceRecordKey: "event-1", Kind: "activitywatch.event", OccurredAt: time.Date(2026, 9, 23, 10, 15, 0, 0, time.UTC), EndedAt: timePtr(time.Date(2026, 9, 23, 10, 30, 0, 0, time.UTC)), CollectedAt: time.Date(2026, 9, 23, 10, 31, 0, 0, time.UTC), OriginalPayload: json.RawMessage(payload)}
}

func timePtr(value time.Time) *time.Time { return &value }

func timeInt64(value int64) *int64 { return &value }
