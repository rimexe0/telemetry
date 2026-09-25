package httpapi

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/rimexe0/telemetry/internal/model"
	"github.com/rimexe0/telemetry/internal/store"
)

const (
	agentContextLookback  = 14 * 24 * time.Hour
	maxAgentSources       = 8
	agentReconcileTimeout = 5 * time.Second
)

func batchHasAgentLifecycle(batch model.Batch) bool {
	for _, observation := range batch.Observations {
		if observation.SourceKind == "t3code" && observation.Kind == "t3code.turn" {
			return true
		}
	}
	return false
}

// reconcileAgentLifecycle runs after a stored batch that contains agent
// lifecycle evidence. It only ever records readiness for the current waiting
// primary, and only through explicit project rules; failures leave ingest
// successful and execution unchanged.
func (s *Server) reconcileAgentLifecycle(parent context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), agentReconcileTimeout)
	defer cancel()
	if err := s.recordAgentReadiness(ctx); err != nil && !errors.Is(err, store.ErrInvalidExecutionTransition) && !errors.Is(err, store.ErrInvalidExecutionInput) {
		log.Printf("agent lifecycle reconcile: %v", err)
	}
}

func (s *Server) recordAgentReadiness(ctx context.Context) error {
	primary, err := s.repository.CurrentPrimaryExecutionSession(ctx)
	if err != nil || primary == nil || primary.State != "waiting_external" {
		return err
	}
	var waitingAt time.Time
	for _, event := range primary.Events {
		if event.EventType == "waiting_external" && event.OccurredAt.After(waitingAt) {
			waitingAt = event.OccurredAt
		}
	}
	if waitingAt.IsZero() {
		return nil
	}
	rules, err := s.repository.ListActivityContextRules(ctx, false)
	if err != nil || len(rules) == 0 {
		return err
	}
	sources, err := s.repository.ListSources(ctx, 1000)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Add(time.Minute)
	var observations []store.ObservationView
	checked := 0
	for _, source := range sources {
		if source.SourceKind != "t3code" {
			continue
		}
		if checked++; checked > maxAgentSources {
			break
		}
		for _, window := range [][2]time.Time{{waitingAt.Add(-agentContextLookback), waitingAt}, {waitingAt, now}} {
			from, to := window[0], window[1]
			rows, err := s.repository.QueryObservations(ctx, store.Query{SourceInstanceKey: source.SourceInstanceKey, From: &from, To: &to, Limit: 1000})
			if err != nil {
				return err
			}
			observations = append(observations, rows...)
		}
	}
	lifecycles := make([]store.AgentLifecycle, 0)
	for _, observation := range observations {
		if lifecycle, ok := store.AgentLifecycleFromObservation(observation); ok {
			lifecycles = append(lifecycles, lifecycle)
		}
	}
	candidate, ok := store.FindAgentReady(primary, lifecycles, store.AgentSessionContexts(observations), rules)
	if !ok {
		return nil
	}
	_, err = s.repository.RecordAgentReady(ctx, primary.ID, candidate)
	return err
}
