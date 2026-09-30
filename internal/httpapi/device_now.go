package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/rimexe0/telemetry/internal/policy"
	"github.com/rimexe0/telemetry/internal/store"
)

type deviceTask struct {
	Title        string `json:"title"`
	NextAction   string `json:"next_action,omitempty"`
	ResumeMarker string `json:"resume_marker,omitempty"`
	State        string `json:"state"`
	Scheduled    string `json:"scheduled,omitempty"`
}

// pairedDeviceAuthorized accepts only enrolled device credentials. The shared
// legacy ingest token can upload but cannot read the owner's plan state.
func (s *Server) pairedDeviceAuthorized(request *http.Request) bool {
	deviceID := strings.TrimSpace(request.Header.Get("X-Telemetry-Device-ID"))
	token, bearer := strings.CutPrefix(request.Header.Get("Authorization"), "Bearer ")
	if deviceID == "" || !bearer || s.config.DeviceAuthorized == nil {
		return false
	}
	registered, authorized, err := s.config.DeviceAuthorized(request.Context(), deviceID, token)
	return err == nil && registered && authorized
}

// deviceNow is the read-only state a paired owner device needs to show the
// current task and follow the policy timeline offline.
func (s *Server) deviceNow(writer http.ResponseWriter, request *http.Request) {
	if !s.pairedDeviceAuthorized(request) && !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "a paired device credential is required")
		return
	}
	ctx := request.Context()
	now := time.Now().UTC()
	config, version, facts, err := s.policyInputs(ctx, now, now.Add(12*time.Hour))
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	activities, err := s.repository.ListActivities(ctx, store.ActivityFilter{Limit: 200})
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	primary, err := s.repository.CurrentPrimaryExecutionSession(ctx)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	var task *deviceTask
	if primary != nil && primary.EndedAt == nil {
		task = &deviceTask{Title: "Activity", NextAction: primary.NextActionSnapshot, State: primary.State}
		if primary.ResumeMarker != nil {
			task.ResumeMarker = *primary.ResumeMarker
		}
		for _, activity := range activities {
			if activity.ID == primary.ActivityID {
				task.Title = activity.Title
				if activity.NextAction != "" {
					task.NextAction = activity.NextAction
				}
			}
		}
		if n := len(primary.Events); n > 0 && primary.Events[n-1].EventType == "offline_recovery_started" {
			task.State = "offline_recovery"
		}
	} else {
		// No session: show the open work block covering now, if exactly known.
		for _, block := range facts.Blocks {
			if block.Kind == policy.BlockKindWork && !now.Before(block.Start) && now.Before(block.End) && (block.State == "planned" || block.State == "active") {
				task = &deviceTask{Title: block.Title, State: "not_started", Scheduled: block.Start.Format(time.RFC3339) + "/" + block.End.Format(time.RFC3339)}
				break
			}
		}
	}
	open := []store.CollectionIncident{}
	incidents, err := s.repository.ListCollectionIncidents(request.Context(), now, 50)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	for _, incident := range incidents {
		if incident.EndedAt == nil {
			open = append(open, incident)
		}
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"generated_at": now,
		// Open collection problems, so this device can tell the owner about
		// another device that went quiet (#28).
		"collection_incidents": open,
		"plan_exists":          facts.PlanExists,
		"task":                 task,
		"decision":             policy.Evaluate(config, version, facts, now),
		// Start in the past so a device whose clock runs slightly behind still
		// finds a segment covering its own "now".
		"timeline":   policy.Timeline(config, version, facts, now.Add(-10*time.Minute), now.Add(12*time.Hour)),
		"panel_path": "/panel/",
	})
}
