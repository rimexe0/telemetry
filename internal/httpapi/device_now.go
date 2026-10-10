package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rimexe0/telemetry/internal/activity"
	"github.com/rimexe0/telemetry/internal/policy"
	"github.com/rimexe0/telemetry/internal/store"
)

// deviceTimeline is how far ahead a device can follow the approved schedule
// without reaching the server. Past it, a device stops blocking.
const deviceTimeline = 48 * time.Hour

// trackingWindow is how far back the current-task reading looks. It covers a
// working day, so an agent that stopped hours ago still counts as waiting.
const trackingWindow = 12 * time.Hour

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
// deviceWaitMax is the longest a device's request waits for a change; it
// stays well under the relay's idle limit.
const deviceWaitMax = 50 * time.Second

// deviceNow answers with what a device should do now. With ?wait= the
// fingerprint the device already has, it waits until that changes, for at
// most ?timeout= seconds (up to 50), and answers 304 when nothing did: a
// change made by the assistant or the owner reaches the device at once.
func (s *Server) deviceNow(writer http.ResponseWriter, request *http.Request) {
	if !s.pairedDeviceAuthorized(request) && !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "a paired device credential is required")
		return
	}
	known := request.URL.Query().Get("wait")
	// A device that checks places itself says so; older ones only get place
	// follow-ups once the server has seen the place reached.
	places := request.URL.Query().Get("places") == "1"
	wait := time.Duration(0)
	if seconds, err := strconv.Atoi(request.URL.Query().Get("timeout")); err == nil && seconds > 0 && known != "" && s.config.Changes != nil {
		wait = min(time.Duration(seconds)*time.Second, deviceWaitMax)
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		// Taken before computing, so a change during it is not missed.
		var next <-chan struct{}
		if s.config.Changes != nil {
			next = s.config.Changes.Next()
		}
		body, err := s.deviceNowBody(request.Context(), places)
		if err != nil {
			writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
			return
		}
		if wait == 0 || body["fingerprint"] != known {
			writeJSON(writer, http.StatusOK, body)
			return
		}
		select {
		case <-next:
		case <-deadline.C:
			writer.WriteHeader(http.StatusNotModified)
			return
		case <-request.Context().Done():
			return
		}
	}
}

func (s *Server) deviceNowBody(ctx context.Context, places bool) (map[string]any, error) {
	now := time.Now().UTC()
	config, version, facts, err := s.policyInputs(ctx, now, now.Add(deviceTimeline))
	if err != nil {
		return nil, err
	}
	activities, err := s.repository.ListActivities(ctx, store.ActivityFilter{Limit: 200})
	if err != nil {
		return nil, err
	}
	primary, err := s.repository.CurrentPrimaryExecutionSession(ctx)
	if err != nil {
		return nil, err
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
		// A focus block with no task is not a task to start; its reason says
		// what it does.
		for _, block := range facts.Blocks {
			if block.Kind == policy.BlockKindWork && block.HasTask && !now.Before(block.Start) && now.Before(block.End) && (block.State == "planned" || block.State == "active") {
				task = &deviceTask{Title: block.Title, State: "not_started", Scheduled: block.Start.Format(time.RFC3339) + "/" + block.End.Format(time.RFC3339)}
				break
			}
		}
	}
	open := []store.CollectionIncident{}
	incidents, err := s.repository.ListCollectionIncidents(ctx, now, 50)
	if err != nil {
		return nil, err
	}
	for _, incident := range incidents {
		if incident.EndedAt == nil {
			open = append(open, incident)
		}
	}
	// Open follow-ups, so the device can show the due ones until they are done.
	followUps, err := s.repository.ListFollowUps(ctx, now)
	if err != nil {
		return nil, err
	}
	openFollowUps := []store.FollowUp{}
	for _, item := range followUps {
		if item.State == store.FollowUpOpen && (places || item.Place == nil || item.TriggeredAt != nil) {
			if !places && item.TriggeredAt != nil && item.TriggeredAt.After(item.DueAt) {
				item.DueAt = *item.TriggeredAt
			}
			openFollowUps = append(openFollowUps, item)
		}
	}
	// What the owner is on at the Mac right now, and the open tasks to choose from.
	recent, _, err := store.LoadTrackingInput(ctx, s.repository, alignmentPlanDate(now), now.Add(-trackingWindow), now)
	if err != nil {
		return nil, err
	}
	tracking := activity.Track(recent)
	tasks := []activity.TaskRef{}
	for _, item := range recent.Tasks {
		tasks = append(tasks, activity.TaskRef{ID: item.ID, Title: item.Title})
	}
	// With no session and no block, the recognised task is the current task.
	if task == nil && tracking.State == activity.KindTask {
		task = &deviceTask{Title: tracking.Task, State: "active"}
	}
	decision := policy.Evaluate(config, version, facts, now)
	// Start in the past so a device whose clock runs slightly behind still
	// finds a segment covering its own "now".
	timeline := policy.Timeline(config, version, facts, now.Add(-10*time.Minute), now.Add(deviceTimeline))
	return map[string]any{
		"fingerprint":  nowFingerprint(decision, timeline, task, facts.PlanExists, openFollowUps),
		"generated_at": now,
		"tracking":     tracking,
		"tasks":        tasks,
		"follow_ups":   openFollowUps,
		// Open collection problems, so this device can tell the owner about
		// another device that went quiet (#28).
		"collection_incidents": open,
		"plan_exists":          facts.PlanExists,
		"task":                 task,
		"decision":             decision,
		"timeline":             timeline,
		"panel_path":           "/panel/",
	}, nil
}

// nowFingerprint changes only when what a device does changes: the decision
// and its timeline (without the times that move with every request), the
// current task, whether a plan exists, and the open follow-ups. What is on
// screen right now is left out, or every device would wake all the time.
func nowFingerprint(decision policy.Decision, timeline []policy.Segment, task *deviceTask, planExists bool, followUps []store.FollowUp) string {
	strip := func(decision policy.Decision) policy.Decision {
		decision.At = time.Time{}
		return decision
	}
	segments := []any{}
	for i, segment := range timeline {
		item := map[string]any{"decision": strip(segment.Decision)}
		if i > 0 {
			item["from"] = segment.From
		}
		if i < len(timeline)-1 {
			item["to"] = segment.To
		}
		segments = append(segments, item)
	}
	encoded, _ := json.Marshal(map[string]any{"decision": strip(decision), "timeline": segments, "task": task, "plan": planExists, "follow_ups": followUps})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:12])
}

// deviceFollowUpDone lets a paired device mark one follow-up done. It is the
// only owner record a device credential can change: the reminder stays on the
// phone until it is done, so the phone must be able to say so.
func (s *Server) deviceFollowUpDone(writer http.ResponseWriter, request *http.Request) {
	if !s.pairedDeviceAuthorized(request) && !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "a paired device credential is required")
		return
	}
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(writer, http.StatusBadRequest, "invalid_path", "follow-up id must be a positive integer")
		return
	}
	by := "owner"
	if device := strings.TrimSpace(request.Header.Get("X-Telemetry-Device-ID")); device != "" {
		by = "device:" + device
	}
	item, err := s.repository.CloseFollowUp(request.Context(), id, by)
	if errors.Is(err, store.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "follow-up does not exist")
		return
	}
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"follow_up": item})
}

// deviceFollowUpTriggered lets a paired device say a place follow-up's place
// was reached, so the server, the assistant and other devices know it is due.
// The body may give {"at": RFC3339}, the time the phone saw it; default now.
func (s *Server) deviceFollowUpTriggered(writer http.ResponseWriter, request *http.Request) {
	if !s.pairedDeviceAuthorized(request) && !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "a paired device credential is required")
		return
	}
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(writer, http.StatusBadRequest, "invalid_path", "follow-up id must be a positive integer")
		return
	}
	at := time.Now().UTC()
	var body struct {
		At *time.Time `json:"at"`
	}
	if json.NewDecoder(io.LimitReader(request.Body, 4096)).Decode(&body) == nil && body.At != nil && body.At.Before(at) && at.Sub(*body.At) < 7*24*time.Hour {
		at = body.At.UTC()
	}
	item, err := s.repository.TriggerFollowUp(request.Context(), id, at)
	if errors.Is(err, store.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "follow-up does not exist")
		return
	}
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"follow_up": item})
}

// deviceClaim records the owner's answer, given on a paired device, to "what
// are you working on": a task, or none. It applies from now while they stay
// at the Mac.
func (s *Server) deviceClaim(writer http.ResponseWriter, request *http.Request) {
	if !s.pairedDeviceAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "a paired device credential is required")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, s.config.MaxRequestSize)
	var input struct {
		TaskID  *int64 `json:"task_id"`
		Context string `json:"context"`
	}
	if err := decodeExecutionJSON(request, &input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one claim")
		return
	}
	claim, err := s.repository.AddTaskClaim(request.Context(), store.TaskClaim{TaskID: input.TaskID, Context: input.Context,
		DeviceID: strings.TrimSpace(request.Header.Get("X-Telemetry-Device-ID"))})
	if errors.Is(err, store.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "task does not exist")
		return
	}
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"claim": claim})
}

// deviceFocus lets the owner start, pause, resume or end a focus session
// from a paired device.
func (s *Server) deviceFocus(writer http.ResponseWriter, request *http.Request) {
	if !s.pairedDeviceAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "a paired device credential is required")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, s.config.MaxRequestSize)
	var input struct {
		Action string `json:"action"`
		TaskID int64  `json:"task_id"`
	}
	if err := decodeExecutionJSON(request, &input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one focus action")
		return
	}
	session, err := store.ChangeFocus(request.Context(), s.repository, input.Action, input.TaskID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "task does not exist")
	case errors.Is(err, store.ErrInvalidExecutionInput), errors.Is(err, store.ErrInvalidExecutionTransition), errors.Is(err, store.ErrExecutionConflict):
		writeError(writer, http.StatusBadRequest, "invalid_focus", err.Error())
	case err != nil:
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
	default:
		writeJSON(writer, http.StatusOK, map[string]any{"session": session})
	}
}

// deviceEvidence records the owner's answer, on a paired device, that
// something is or is not part of a task. The task remembers it.
func (s *Server) deviceEvidence(writer http.ResponseWriter, request *http.Request) {
	if !s.pairedDeviceAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "a paired device credential is required")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, s.config.MaxRequestSize)
	var input struct {
		TaskID  int64  `json:"task_id"`
		Context string `json:"context"`
		Verdict string `json:"verdict"`
	}
	if err := decodeExecutionJSON(request, &input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one answer")
		return
	}
	evidence, err := s.repository.AddTaskEvidence(request.Context(), store.TaskEvidence{TaskID: input.TaskID, Context: input.Context, Verdict: input.Verdict,
		Source: store.EvidenceOwner, DeviceID: strings.TrimSpace(request.Header.Get("X-Telemetry-Device-ID"))})
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "task does not exist")
	case errors.Is(err, store.ErrInvalidEvidence):
		writeError(writer, http.StatusBadRequest, "invalid_evidence", err.Error())
	case err != nil:
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
	default:
		writeJSON(writer, http.StatusOK, map[string]any{"evidence": evidence})
	}
}
