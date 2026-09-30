// Package store defines the central durable telemetry repository.
package store

import (
	"context"
	"encoding/json"
	"github.com/rimexe0/telemetry/internal/activity"
	"github.com/rimexe0/telemetry/internal/silence"
	"time"

	"github.com/rimexe0/telemetry/internal/model"
	"github.com/rimexe0/telemetry/internal/policy"
)

type Receipt struct {
	BatchID          string `json:"batch_id"`
	ObservationsSeen int    `json:"observations_seen"`
	RevisionsCreated int    `json:"revisions_created"`
}

type Query struct {
	SourceInstanceKey string
	From              *time.Time
	To                *time.Time
	Limit             int
}

type ObservationView struct {
	SourceInstanceKey string          `json:"source_instance_key"`
	SourceKind        string          `json:"source_kind"`
	DeviceID          string          `json:"device_id"`
	SourceRecordKey   string          `json:"source_record_key"`
	Kind              string          `json:"kind"`
	OccurredAt        time.Time       `json:"occurred_at"`
	EndedAt           *time.Time      `json:"ended_at,omitempty"`
	CollectedAt       time.Time       `json:"collected_at"`
	RevisionDigest    string          `json:"revision_digest"`
	OriginalPayload   json.RawMessage `json:"original_payload"`
	Projection        json.RawMessage `json:"projection"`
}

type SourceSummary struct {
	SourceInstanceKey string     `json:"source_instance_key"`
	SourceKind        string     `json:"source_kind"`
	DeviceID          string     `json:"device_id"`
	FirstCollectedAt  time.Time  `json:"first_collected_at"`
	LastCollectedAt   *time.Time `json:"last_collected_at,omitempty"`
	ObservationCount  int64      `json:"observation_count"`
}

// CollectionHealthQuery bounds the source and resource history returned by a
// collection health read. Resource samples are returned newest first for each
// device.
type CollectionHealthQuery struct {
	SourceLimit     int
	ResourceSamples int
}

type CollectionHealthDevice struct {
	DeviceID        string            `json:"device_id"`
	LastReceivedAt  *time.Time        `json:"last_received_at,omitempty"`
	ResourceHistory []ObservationView `json:"resource_history"`
}

type CollectionHealth struct {
	CheckedAt        time.Time                `json:"checked_at"`
	Sources          []SourceSummary          `json:"sources"`
	SourcesTruncated bool                     `json:"sources_truncated"`
	Devices          []CollectionHealthDevice `json:"devices"`
}

// Repository is deliberately bounded. HTTP and MCP use this interface rather
// than receiving raw database access.
type Repository interface {
	Ingest(context.Context, model.Batch) (Receipt, error)
	QueryObservations(context.Context, Query) ([]ObservationView, error)
	ListSources(context.Context, int) ([]SourceSummary, error)
	CollectionHealth(context.Context, CollectionHealthQuery) (CollectionHealth, error)
	CreateActivity(context.Context, Activity) (Activity, error)
	ListActivities(context.Context, ActivityFilter) ([]Activity, error)
	UpdateActivity(context.Context, Activity) (Activity, error)
	LatestPlan(context.Context, time.Time) (*Plan, error)
	PlanBlocksByIDs(context.Context, []int64) ([]PlanBlock, error)
	ListPlanVersions(context.Context, time.Time) ([]PlanVersionSummary, error)
	SavePlan(context.Context, time.Time, PlanInput) (Plan, error)
	SetPlanBlockState(context.Context, time.Time, int64, string, string, string, string) (PlanBlock, error)
	ListPlanBlockEvents(context.Context, time.Time, int64) ([]PlanBlockEvent, error)
	GetPlanningSettings(context.Context) (PlanningSettings, error)
	UpdatePlanningSettings(context.Context, bool) (PlanningSettings, error)
	CreatePlanChangeProposal(context.Context, time.Time, PlanChangeRequest) (PlanChangeProposal, error)
	GetPlanChangeProposal(context.Context, time.Time, int64) (PlanChangeProposal, error)
	ListPlanChangeProposals(context.Context, time.Time, int) ([]PlanChangeProposal, error)
	AcceptPlanChangeProposal(context.Context, time.Time, int64, ProposalDecision, json.RawMessage) (PlanChangeProposal, error)
	RejectPlanChangeProposal(context.Context, time.Time, int64, ProposalDecision, json.RawMessage) (PlanChangeProposal, error)
	CreateActivityContextRule(context.Context, ActivityContextRule) (ActivityContextRule, error)
	ListActivityContextRules(context.Context, bool) ([]ActivityContextRule, error)
	DisableActivityContextRule(context.Context, int64) (ActivityContextRule, error)
	RecordAgentReady(context.Context, int64, AgentReadyCandidate) (ExecutionSession, error)
	LatestPolicy(context.Context) (*PolicyVersion, error)
	SavePolicy(context.Context, policy.Config, string) (PolicyVersion, error)
	AndroidAppUsage(context.Context, time.Time, int) ([]AppUsageSuggestion, error)
	ActivityRecords(context.Context, time.Time, time.Time) ([]activity.Record, map[string]time.Time, error)
	CollectionEvidence(context.Context) (silence.Evidence, error)
	CreatePolicyProposal(context.Context, []policy.BlocklistChange, string) (PolicyProposal, error)
	ListPolicyProposals(context.Context, string, int) ([]PolicyProposal, error)
	DecidePolicyProposal(context.Context, int64, bool, ProposalDecision, json.RawMessage) (PolicyProposal, error)
	CreateLabelProposal(context.Context, []activity.LabelChange, string) (LabelProposal, error)
	ListLabelProposals(context.Context, string, int) ([]LabelProposal, error)
	DecideLabelProposal(context.Context, int64, bool, ProposalDecision, json.RawMessage) (LabelProposal, error)
	ListActivityLabels(context.Context) ([]ActivityLabel, error)
	RemoveActivityLabel(context.Context, int64) error
	SyncCollectionIncidents(context.Context, []silence.Finding, time.Time) ([]CollectionIncident, error)
	ListCollectionIncidents(context.Context, time.Time, int) ([]CollectionIncident, error)
	StartExecutionSession(context.Context, ExecutionSessionInput) (ExecutionSession, error)
	TransitionExecutionSession(context.Context, int64, ExecutionEventInput) (ExecutionSession, error)
	ReturnToPrimary(context.Context, int64, ReturnToPrimaryInput) (ExecutionReturn, error)
	CurrentExecutionSession(context.Context) (*ExecutionSession, error)
	CurrentPrimaryExecutionSession(context.Context) (*ExecutionSession, error)
	CurrentAttendedExecutionSession(context.Context) (*ExecutionSession, error)
	GetExecutionSession(context.Context, int64) (*ExecutionSession, error)
	ListExecutionSessions(context.Context, int) ([]ExecutionSession, error)
	ListExecutionInterleaves(context.Context, int64, int) ([]ExecutionSession, error)
	Ping(context.Context) error
}
