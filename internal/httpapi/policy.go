package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/rimexe0/telemetry/internal/policy"
	"github.com/rimexe0/telemetry/internal/store"
)

const maxPolicyTimelineHours = 48

type policyConfigInput struct {
	Config policy.Config `json:"config"`
	Note   string        `json:"note,omitempty"`
}

func (s *Server) policyConfig(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	switch request.Method {
	case http.MethodGet:
		version, err := s.repository.LatestPolicy(request.Context())
		if err != nil {
			writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"policy": version})
	case http.MethodPut:
		request.Body = http.MaxBytesReader(writer, request.Body, s.config.MaxRequestSize)
		var input policyConfigInput
		if err := decodeExecutionJSON(request, &input); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one policy config")
			return
		}
		version, err := s.repository.SavePolicy(request.Context(), input.Config, input.Note)
		if errors.Is(err, store.ErrInvalidPolicy) {
			writeError(writer, http.StatusBadRequest, "invalid_policy", err.Error())
			return
		}
		if errors.Is(err, store.ErrSettingsLocked) {
			writeError(writer, http.StatusLocked, "settings_locked", err.Error())
			return
		}
		if err != nil {
			writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"policy": version})
	default:
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or PUT")
	}
}

func (s *Server) policyCurrent(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	at := time.Now().UTC()
	if value := request.URL.Query().Get("at"); value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_query", "at must be an RFC 3339 time")
			return
		}
		at = parsed.UTC()
	}
	config, version, facts, err := s.policyInputs(request.Context(), at, at)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"decision": policy.Evaluate(config, version, facts, at), "configured": version > 0})
}

func (s *Server) policyTimeline(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	hours := 12
	if value := request.URL.Query().Get("hours"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > maxPolicyTimelineHours {
			writeError(writer, http.StatusBadRequest, "invalid_query", "hours must be an integer from 1 through 48")
			return
		}
		hours = parsed
	}
	from := time.Now().UTC().Truncate(time.Minute)
	to := from.Add(time.Duration(hours) * time.Hour)
	config, version, facts, err := s.policyInputs(request.Context(), from, to)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"generated_at": time.Now().UTC(), "policy_version": version, "configured": version > 0,
		"segments": policy.Timeline(config, version, facts, from, to),
		"note":     "Assumes current execution state persists; fetch again when state changes.",
	})
}

// policyInputs assembles engine facts from retained records. Gates, urgent
// purposes and breaks are supported by the engine but have no stored source
// yet (#22, #23, #24, #25); an emergency unlock comes from the phone.
func (s *Server) policyInputs(ctx context.Context, from, to time.Time) (policy.Config, int64, policy.Facts, error) {
	config := policy.Config{}
	version := int64(0)
	stored, err := s.repository.LatestPolicy(ctx)
	if err != nil {
		return config, 0, policy.Facts{}, err
	}
	if stored != nil {
		config, version = stored.Config, stored.ID
	}
	// The owner's "distraction" and "essential" labels enforce like the
	// policy's own lists.
	labels, err := s.repository.ListActivityLabels(ctx)
	if err != nil {
		return config, 0, policy.Facts{}, err
	}
	config = policy.WithLabels(config, store.LabeledTargets(labels))
	if err := config.Normalize(); err != nil {
		return config, 0, policy.Facts{}, err
	}
	settings, err := s.repository.GetPlanningSettings(ctx)
	if err != nil {
		return config, 0, policy.Facts{}, err
	}
	// The existing panel setting is the single switch for the plan gate.
	// Locking until planned is set with the blocking schedule.
	config.PlanGate.Enabled = settings.DailyPlanGateEnabled || config.PlanGate.FromWake

	facts := policy.Facts{PlanDate: alignmentPlanDate(from).Format("2006-01-02")}
	// The day before is read too: a block that starts late can run past midnight.
	for day := alignmentPlanDate(from).AddDate(0, 0, -1); !day.After(alignmentPlanDate(to)); day = day.AddDate(0, 0, 1) {
		plan, err := s.repository.LatestPlan(ctx, day)
		if err != nil {
			return config, 0, policy.Facts{}, err
		}
		if plan == nil {
			continue
		}
		if day.Equal(alignmentPlanDate(from)) {
			facts.PlanExists, facts.PlanEmpty = true, len(plan.Blocks) == 0
		}
		for _, block := range plan.Blocks {
			kind := policy.BlockKindFree
			if block.ActivityID != nil || block.Focus {
				kind = policy.BlockKindWork
			}
			if day.Before(alignmentPlanDate(from)) && !blockEnd(block).After(from) {
				continue
			}
			facts.Blocks = append(facts.Blocks, policy.Block{ID: block.ID, Title: block.Title, Start: block.StartTime, End: blockEnd(block), Kind: kind, State: block.State,
				HasTask: block.ActivityID != nil})
		}
	}
	if config.PlanGate.FromWake && !facts.PlanExists {
		// The gate starts when the owner wakes, which the records show.
		if facts.WakeAt, err = store.LoadWakeTime(ctx, s.repository, from); err != nil {
			return config, 0, policy.Facts{}, err
		}
	}
	// Time outside today, for the outside rule. Missing positions count as
	// none, so the rule holds until the walk is proven.
	if rule := config.OutsideAt(time.Now()); rule.Enabled {
		location, err := time.LoadLocation(config.Timezone)
		if err != nil {
			return config, 0, policy.Facts{}, err
		}
		progress, err := store.OutsideToday(ctx, s.repository, rule, location, time.Now())
		if err != nil {
			return config, 0, policy.Facts{}, err
		}
		facts.OutsideMinutes = progress.Minutes
		if rule.FromWake && facts.WakeAt == nil {
			if facts.WakeAt, err = store.LoadWakeTime(ctx, s.repository, from); err != nil {
				return config, 0, policy.Facts{}, err
			}
		}
	}
	// The morning hold starts from waking and ends at a scan of the code.
	if rule := config.MorningAt(from); rule.Enabled {
		if rule.FromWake && rule.From == "" && facts.WakeAt == nil {
			if facts.WakeAt, err = store.LoadWakeTime(ctx, s.repository, from); err != nil {
				return config, 0, policy.Facts{}, err
			}
		}
		if facts.MorningScannedAt, err = s.repository.LatestMorningScan(ctx, from); err != nil {
			return config, 0, policy.Facts{}, err
		}
	}
	// An emergency unlock started on the phone opens every device.
	if facts.Emergency, err = s.repository.LatestEmergency(ctx, from); err != nil {
		return config, 0, policy.Facts{}, err
	}
	openings, err := s.repository.ActiveOpenings(ctx, from)
	if err != nil {
		return config, 0, policy.Facts{}, err
	}
	facts.Openings = store.PolicyOpenings(config, openings)
	primary, err := s.repository.CurrentPrimaryExecutionSession(ctx)
	if err != nil {
		return config, 0, policy.Facts{}, err
	}
	if primary != nil && primary.EndedAt == nil {
		execution := &policy.Execution{State: primary.State}
		if n := len(primary.Events); n > 0 && primary.Events[n-1].EventType == "offline_recovery_started" {
			execution.Recovery = true
		}
		for _, block := range facts.Blocks {
			if primary.PlanBlockID != nil && block.ID == *primary.PlanBlockID {
				execution.Title = block.Title
			}
		}
		// A focus session started without a block is named by its task.
		if execution.Title == "" {
			activities, err := s.repository.ListActivities(ctx, store.ActivityFilter{Limit: 500})
			if err != nil {
				return config, 0, policy.Facts{}, err
			}
			for _, activity := range activities {
				if activity.ID == primary.ActivityID {
					execution.Title = activity.Title
				}
			}
		}
		facts.Execution = execution
	}
	return config, version, facts, nil
}

// policySuggestions lists the phone's most used apps so the owner can pick
// what to block. Nothing is added to the policy by this call.
func (s *Server) policySuggestions(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	apps, err := s.repository.AndroidAppUsage(request.Context(), time.Now().UTC().Add(-30*24*time.Hour), 40)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"android_apps": apps})
}
