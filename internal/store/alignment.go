package store

import (
	"encoding/json"
	"strings"
	"time"
	"unicode"
)

const (
	AlignmentAligned       = "aligned"
	AlignmentPossibleDrift = "possible_drift"
	AlignmentUnknown       = "unknown"

	AlignmentReasonExecutionMatchesPlan = "execution_matches_plan"
	AlignmentReasonWaitingExternal      = "waiting_external"
	AlignmentReasonUsefulInterleave     = "useful_interleave"
	AlignmentReasonReturnPending        = "return_pending"
	AlignmentReasonIntentionalSwitch    = "intentional_switch"
	AlignmentReasonRecovery             = "recovery"
	AlignmentReasonActivityMismatch     = "activity_mismatch"
	AlignmentReasonNoCurrentPlanBlock   = "no_current_plan_block"
	AlignmentReasonStaleEvidence        = "stale_evidence"
	AlignmentReasonMissingEvidence      = "missing_evidence"
	AlignmentReasonAmbiguousEvidence    = "ambiguous_evidence"
)

const alignmentFreshnessWindow = 15 * time.Minute

type AlignmentEvidence struct {
	SourceInstanceKey string     `json:"source_instance_key"`
	SourceKind        string     `json:"source_kind"`
	SourceRecordKey   string     `json:"source_record_key"`
	RelatedActivityID *int64     `json:"related_activity_id,omitempty"`
	RelatedActivity   string     `json:"related_activity_title,omitempty"`
	Kind              string     `json:"kind"`
	OccurredAt        time.Time  `json:"occurred_at"`
	EndedAt           *time.Time `json:"ended_at,omitempty"`
	CollectedAt       time.Time  `json:"collected_at"`
	Summary           string     `json:"summary"`
	Relationship      string     `json:"relationship"`
	Origin            string     `json:"origin"`
}

type AlignmentView struct {
	EvaluatedAt        time.Time           `json:"evaluated_at"`
	PlanDate           string              `json:"plan_date"`
	PlanVersionID      *int64              `json:"plan_version_id,omitempty"`
	PlanBlockID        *int64              `json:"plan_block_id,omitempty"`
	ActivityID         *int64              `json:"activity_id,omitempty"`
	ActivityTitle      string              `json:"activity_title,omitempty"`
	NextAction         string              `json:"next_action,omitempty"`
	ResumeMarker       string              `json:"resume_marker,omitempty"`
	PlanBlockState     string              `json:"plan_block_state,omitempty"`
	ExecutionSessionID *int64              `json:"execution_session_id,omitempty"`
	ExecutionState     string              `json:"execution_state,omitempty"`
	AttendedSessionID  *int64              `json:"attended_execution_session_id,omitempty"`
	AttendedState      string              `json:"attended_execution_state,omitempty"`
	AlignmentState     string              `json:"alignment_state"`
	ReasonCode         string              `json:"reason_code"`
	Reason             string              `json:"reason"`
	SourceFreshness    string              `json:"source_freshness"`
	Coverage           string              `json:"coverage"`
	FreshnessNote      string              `json:"freshness_note"`
	Evidence           []AlignmentEvidence `json:"evidence"`
}

type AlignmentContext struct {
	EvaluatedAt     time.Time
	At              time.Time
	Now             time.Time
	PlanDate        string
	PlanVersionID   int64
	Block           *PlanBlock
	Activity        *Activity
	Activities      []Activity
	Session         *ExecutionSession
	PrimarySession  *ExecutionSession
	AttendedSession *ExecutionSession
	Interleaves     []ExecutionSession
	Sources         []SourceSummary
	Observations    []ObservationView
}

func EvaluateAlignment(input AlignmentContext) AlignmentView {
	now := input.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	evaluatedAt := input.EvaluatedAt
	if evaluatedAt.IsZero() {
		evaluatedAt = input.At
	}
	evaluatedAt = evaluatedAt.UTC()
	if evaluatedAt.IsZero() {
		evaluatedAt = now
	}
	freshness := alignmentSourceFreshness(input.Sources, now)
	result := AlignmentView{
		EvaluatedAt:     evaluatedAt,
		PlanDate:        input.PlanDate,
		PlanVersionID:   int64Pointer(input.PlanVersionID),
		AlignmentState:  AlignmentUnknown,
		ReasonCode:      AlignmentReasonNoCurrentPlanBlock,
		Reason:          "No planned block covers the evaluated time.",
		SourceFreshness: freshness.state,
		Coverage:        alignmentCoverageForFreshness(freshness.state),
		FreshnessNote:   freshness.note,
		Evidence:        []AlignmentEvidence{},
	}
	if input.Block == nil {
		return result
	}
	result.PlanBlockID = int64Pointer(input.Block.ID)
	result.PlanBlockState = input.Block.State
	if input.Activity != nil {
		result.ActivityID = int64Pointer(input.Activity.ID)
		result.ActivityTitle = input.Activity.Title
		result.NextAction = input.Activity.NextAction
		result.ResumeMarker = input.Activity.ResumeMarker
	}
	primarySession := input.PrimarySession
	if primarySession == nil && input.Session != nil && input.Session.Role != ExecutionRoleWaitingInterleave {
		primarySession = input.Session
	}
	attendedSession := input.AttendedSession
	if attendedSession == nil && primarySession != nil && primarySession.State == executionStateActive {
		attendedSession = primarySession
	}
	if primarySession != nil {
		result.ExecutionSessionID = int64Pointer(primarySession.ID)
		result.ExecutionState = primarySession.State
	}
	if attendedSession != nil {
		result.AttendedSessionID = int64Pointer(attendedSession.ID)
		result.AttendedState = attendedSession.State
	}

	result.SourceFreshness = freshness.state
	result.Coverage = alignmentCoverageForFreshness(freshness.state)
	result.FreshnessNote = freshness.note
	result.Evidence = alignmentEvidence(*input.Block, input.Activity, input.Activities, input.Observations)

	if primarySession != nil {
		if primarySession.State == "unknown" {
			result.ReasonCode = AlignmentReasonAmbiguousEvidence
			result.Reason = "Execution event times do not support a reliable state for this interval."
			return result
		}
		for _, interleave := range input.Interleaves {
			if interleave.Role == ExecutionRoleWaitingInterleave && interleave.PrimarySessionID != nil &&
				*interleave.PrimarySessionID == primarySession.ID && interleave.EndedAt == nil &&
				interleave.State == executionStateActive && primarySession.State == executionStateWaitingExternal &&
				primaryMatchesBlock(*primarySession, *input.Block) {
				if executionReturnPending(primarySession.Events, evaluatedAt) {
					result.AlignmentState = AlignmentAligned
					result.ReasonCode = AlignmentReasonReturnPending
					result.Reason = "The primary objective is ready to resume while the owner is still attending its linked interleave."
					return result
				}
				result.AlignmentState = AlignmentAligned
				result.ReasonCode = AlignmentReasonUsefulInterleave
				result.Reason = "The primary objective is waiting for an external event while the owner attends its linked interleave."
				return result
			}
		}
		lastEvent := lastMeaningfulExecutionEventAt(primarySession.Events, evaluatedAt)
		if lastEvent == executionEventSwitchedOut {
			result.AlignmentState = AlignmentAligned
			result.ReasonCode = AlignmentReasonIntentionalSwitch
			result.Reason = "The owner recorded an intentional switch; this is not classified as accidental drift."
			return result
		}
		if lastEvent == executionEventOverwhelmed || lastEvent == executionEventOfflineRecoveryStarted || lastEvent == executionEventOfflineRecoveryEnded {
			result.AlignmentState = AlignmentAligned
			result.ReasonCode = AlignmentReasonRecovery
			result.Reason = "The owner recorded overwhelm or an offline recovery period. Recovery is not task abandonment."
			return result
		}
		if primaryMatchesBlock(*primarySession, *input.Block) {
			switch primarySession.State {
			case executionStateActive:
				result.AlignmentState = AlignmentAligned
				result.ReasonCode = AlignmentReasonExecutionMatchesPlan
				result.Reason = "The active execution session is linked to this planned block."
				return result
			case executionStateWaitingExternal:
				if freshness.state == alignmentFreshnessFresh && hasEvidenceRelationship(result.Evidence, "unmatched") {
					break
				}
				result.AlignmentState = AlignmentAligned
				result.ReasonCode = AlignmentReasonWaitingExternal
				result.Reason = "The owner is waiting for an external process while this task remains current."
				return result
			case executionStateCompleted:
				result.AlignmentState = AlignmentAligned
				result.ReasonCode = AlignmentReasonExecutionMatchesPlan
				result.Reason = "The owner recorded a completed execution session linked to this planned block."
				return result
			}
		}
	}

	if freshness.state != alignmentFreshnessFresh {
		result.ReasonCode = freshness.reason
		result.Reason = freshness.reasonText
		return result
	}
	if hasEvidenceRelationship(result.Evidence, "matched") {
		result.AlignmentState = AlignmentAligned
		result.ReasonCode = AlignmentReasonExecutionMatchesPlan
		result.Reason = "Fresh device evidence contains terms related to this planned block. This does not prove completion."
		return result
	}
	if hasEvidenceRelationship(result.Evidence, "unmatched") {
		result.AlignmentState = AlignmentPossibleDrift
		result.ReasonCode = AlignmentReasonActivityMismatch
		result.Reason = "Fresh device evidence matches another configured activity. This may be useful switching or possible drift."
		return result
	}
	if hasEvidenceRelationship(result.Evidence, "ambiguous") || len(result.Evidence) > 0 {
		result.ReasonCode = AlignmentReasonAmbiguousEvidence
		result.Reason = "The available evidence is ambiguous or only describes agent activity; no drift claim is made."
		return result
	}
	result.ReasonCode = AlignmentReasonMissingEvidence
	result.Reason = "No activity evidence overlaps this planned block."
	return result
}

const (
	alignmentFreshnessFresh   = "fresh"
	alignmentFreshnessStale   = "stale"
	alignmentFreshnessMissing = "missing"
)

type alignmentFreshness struct {
	state      string
	note       string
	reason     string
	reasonText string
}

func alignmentSourceFreshness(sources []SourceSummary, now time.Time) alignmentFreshness {
	relevant := make([]SourceSummary, 0)
	for _, source := range sources {
		if source.SourceKind == "activitywatch" || source.SourceKind == "android_usage" {
			relevant = append(relevant, source)
		}
	}
	if len(relevant) == 0 {
		return alignmentFreshness{state: alignmentFreshnessMissing, note: "No ActivityWatch or Android activity source is available.", reason: AlignmentReasonMissingEvidence, reasonText: "Activity evidence is unavailable."}
	}
	details := make([]string, 0, len(relevant))
	for _, source := range relevant {
		if source.LastCollectedAt == nil {
			details = append(details, source.SourceKind+" has no recent receipt")
			continue
		}
		age := now.Sub(source.LastCollectedAt.UTC())
		if age < 0 {
			age = 0
		}
		details = append(details, source.SourceKind+" "+age.Round(time.Minute).String()+" ago")
	}
	for _, source := range relevant {
		if source.LastCollectedAt != nil && now.Sub(source.LastCollectedAt.UTC()) <= alignmentFreshnessWindow {
			return alignmentFreshness{state: alignmentFreshnessFresh, note: strings.Join(details, ", ")}
		}
	}
	return alignmentFreshness{state: alignmentFreshnessStale, note: strings.Join(details, ", "), reason: AlignmentReasonStaleEvidence, reasonText: "Available activity evidence is stale; alignment is unknown."}
}

func alignmentCoverageForFreshness(freshness string) string {
	if freshness == alignmentFreshnessMissing {
		return "missing"
	}
	return "available"
}

func primaryMatchesBlock(session ExecutionSession, block PlanBlock) bool {
	if session.PlanBlockID != nil {
		return *session.PlanBlockID == block.ID
	}
	return block.ActivityID != nil && session.ActivityID == *block.ActivityID
}

func alignmentEvidence(block PlanBlock, activity *Activity, activities []Activity, observations []ObservationView) []AlignmentEvidence {
	terms := alignmentTerms(strings.Join([]string{block.Title, activityTitle(activity), activityNextAction(activity), activityResumeMarker(activity), activityNotes(activity)}, " "))
	currentActivityID := int64(0)
	if activity != nil {
		currentActivityID = activity.ID
	} else if block.ActivityID != nil {
		currentActivityID = *block.ActivityID
	}
	result := make([]AlignmentEvidence, 0)
	for _, observation := range observations {
		if !alignmentOverlaps(block, observation) {
			continue
		}
		text, summary, specific := alignmentPayloadText(observation)
		relationship := "context_only"
		origin := "observed_agent_activity"
		var related *Activity
		if alignmentHumanActivity(observation) {
			origin = "observed_device_activity"
			if !specific {
				relationship = "ambiguous"
			} else if len(terms) > 0 && alignmentContainsTerm(text, terms) {
				relationship = "matched"
			} else {
				if candidate, unique := alignmentRelatedActivity(text, activities, currentActivityID); unique {
					related = &candidate
					relationship = "unmatched"
				} else {
					relationship = "ambiguous"
				}
			}
		}
		evidence := AlignmentEvidence{SourceInstanceKey: observation.SourceInstanceKey, SourceKind: observation.SourceKind, SourceRecordKey: observation.SourceRecordKey, Kind: observation.Kind, OccurredAt: observation.OccurredAt, EndedAt: observation.EndedAt, CollectedAt: observation.CollectedAt, Summary: summary, Relationship: relationship, Origin: origin}
		if related != nil {
			evidence.RelatedActivityID = int64Pointer(related.ID)
			evidence.RelatedActivity = related.Title
		}
		result = append(result, evidence)
		if len(result) == 24 {
			break
		}
	}
	return result
}

func alignmentRelatedActivity(text string, activities []Activity, currentID int64) (Activity, bool) {
	var match Activity
	for _, activity := range activities {
		if activity.ID == currentID || activity.Status == activityStatusDone || activity.Status == activityStatusCompleted || activity.Status == activityStatusAbandoned {
			continue
		}
		if alignmentContainsAllTerms(text, alignmentTerms(activity.Title)) {
			if match.ID != 0 {
				return Activity{}, false
			}
			match = activity
		}
	}
	return match, match.ID != 0
}

func lastMeaningfulExecutionEventAt(events []ExecutionEvent, at time.Time) string {
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].OccurredAt.After(at) {
			continue
		}
		if events[index].EventType != executionEventOwnerExplanation {
			return events[index].EventType
		}
	}
	return ""
}

func executionReturnPending(events []ExecutionEvent, at time.Time) bool {
	waiting, ready := false, false
	for _, event := range events {
		if event.OccurredAt.After(at) {
			continue
		}
		switch event.EventType {
		case executionEventWaitingExternal:
			waiting, ready = true, false
		case executionEventExternalReady:
			if waiting {
				ready = true
			}
		case executionEventResumed, executionEventPaused, executionEventInterrupted,
			executionEventSwitchedOut, executionEventCompleted, executionEventAbandoned:
			waiting, ready = false, false
		}
	}
	return waiting && ready
}

func alignmentOverlaps(block PlanBlock, observation ObservationView) bool {
	blockStart := block.StartTime
	blockEnd := blockStart.Add(time.Duration(block.DurationMinutes) * time.Minute)
	observedEnd := observation.OccurredAt
	if observation.EndedAt != nil {
		observedEnd = *observation.EndedAt
	}
	return observation.OccurredAt.Before(blockEnd) && !observedEnd.Before(blockStart)
}

func alignmentHumanActivity(observation ObservationView) bool {
	if observation.SourceKind == "android_usage" {
		return true
	}
	return observation.SourceKind == "activitywatch" && !strings.Contains(observation.SourceInstanceKey, "aw-watcher-codex")
}

func alignmentPayloadText(observation ObservationView) (string, string, bool) {
	var payload map[string]any
	if err := json.Unmarshal(observation.OriginalPayload, &payload); err != nil {
		return "", observation.Kind, false
	}
	if observation.SourceKind == "android_usage" {
		values := []string{alignmentString(payload["package_name"]), alignmentString(payload["class_name"]), alignmentString(payload["event_type"])}
		values = nonEmptyStrings(values)
		return alignmentNormalise(strings.Join(values, " ")), strings.Join(values, " · "), len(values) > 1
	}
	data, _ := payload["data"].(map[string]any)
	values := []string{alignmentString(data["app"]), alignmentString(data["title"]), alignmentString(data["url"]), alignmentString(data["project_path"]), alignmentString(data["cwd"]), alignmentString(data["chat_title"]), alignmentString(data["model"]), alignmentString(data["status"])}
	values = nonEmptyStrings(values)
	specific := false
	for _, key := range []string{"title", "url", "project_path", "cwd", "chat_title", "model", "status"} {
		if alignmentString(data[key]) != "" {
			specific = true
			break
		}
	}
	return alignmentNormalise(strings.Join(values, " ")), strings.Join(values[:minInt(len(values), 3)], " · "), specific
}

func alignmentTerms(value string) []string {
	stopWords := map[string]struct{}{"the": {}, "and": {}, "for": {}, "with": {}, "from": {}, "this": {}, "that": {}, "open": {}, "page": {}, "work": {}, "task": {}, "plan": {}, "session": {}}
	terms := make([]string, 0)
	seen := map[string]struct{}{}
	for _, term := range strings.Fields(alignmentNormalise(value)) {
		if len([]rune(term)) < 3 {
			continue
		}
		if _, ok := stopWords[term]; ok {
			continue
		}
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		terms = append(terms, term)
	}
	return terms
}

func alignmentNormalise(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return ' '
	}, value)
}

func alignmentContainsTerm(text string, terms []string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func alignmentContainsAllTerms(text string, terms []string) bool {
	if len(terms) == 0 {
		return false
	}
	available := make(map[string]struct{})
	for _, term := range strings.Fields(text) {
		available[term] = struct{}{}
	}
	for _, term := range terms {
		if _, ok := available[term]; !ok {
			return false
		}
	}
	return true
}

func alignmentString(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}

func nonEmptyStrings(values []string) []string {
	result := values[:0]
	for _, value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func hasEvidenceRelationship(evidence []AlignmentEvidence, relationship string) bool {
	for _, item := range evidence {
		if item.Relationship == relationship {
			return true
		}
	}
	return false
}

func int64Pointer(value int64) *int64 {
	if value == 0 {
		return nil
	}
	return &value
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func activityTitle(activity *Activity) string {
	if activity == nil {
		return ""
	}
	return activity.Title
}

func activityNextAction(activity *Activity) string {
	if activity == nil {
		return ""
	}
	return activity.NextAction
}

func activityResumeMarker(activity *Activity) string {
	if activity == nil {
		return ""
	}
	return activity.ResumeMarker
}

func activityNotes(activity *Activity) string {
	if activity == nil {
		return ""
	}
	return activity.Notes
}
