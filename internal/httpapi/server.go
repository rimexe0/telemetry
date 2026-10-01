// Package httpapi exposes the authenticated collector and read API.
package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rimexe0/telemetry/internal/model"
	"github.com/rimexe0/telemetry/internal/store"
)

type Config struct {
	IngestToken        string
	IngestDeviceTokens map[string]string
	ReadToken          string
	PanelToken         string
	PanelSessionSecret string
	PanelSessionTTL    time.Duration
	PanelCookieSecure  bool
	MaxRequestSize     int64
	// OwnerAuthorized verifies an external owner session. It must reject all other accounts.
	OwnerAuthorized     func(*http.Request) bool
	DeviceAuthorized    func(context.Context, string, string) (bool, bool, error)
	ClerkPublishableKey string
	PairingEnabled      bool
	// DeviceSessions, when set, lets paired devices open owner panel views.
	DeviceSessions *DeviceSessions
}

type Server struct {
	repository store.Repository
	config     Config
	mux        *http.ServeMux
}

func New(repository store.Repository, config Config) (*Server, error) {
	if repository == nil {
		return nil, errors.New("repository is required")
	}
	if config.IngestToken == "" || config.ReadToken == "" {
		return nil, errors.New("ingest and read tokens are required")
	}
	if config.MaxRequestSize <= 0 {
		config.MaxRequestSize = 16 << 20
	}
	server := &Server{repository: repository, config: config, mux: http.NewServeMux()}
	server.mux.HandleFunc("GET /healthz", server.health)
	server.mux.HandleFunc("GET /v1/auth/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{"publishable_key": config.ClerkPublishableKey, "pairing_enabled": config.PairingEnabled})
	})
	server.mux.HandleFunc("POST /v1/ingest/batches", server.ingest)
	server.mux.HandleFunc("GET /v1/observations", server.observations)
	server.mux.HandleFunc("GET /v1/sources", server.sources)
	server.mux.HandleFunc("GET /v1/collection/health", server.collectionHealth)
	server.mux.HandleFunc("GET /v1/panel/session", server.panelSessionStatus)
	server.mux.HandleFunc("POST /v1/panel/session", server.startPanelSession)
	server.mux.HandleFunc("DELETE /v1/panel/session", server.endPanelSession)
	server.mux.HandleFunc("/v1/execution/sessions", server.executionSessions)
	server.mux.HandleFunc("GET /v1/execution/current", server.currentExecutionSession)
	server.mux.HandleFunc("GET /v1/execution/primary", server.currentPrimaryExecutionSession)
	server.mux.HandleFunc("GET /v1/execution/attended", server.currentAttendedExecutionSession)
	server.mux.HandleFunc("/v1/execution/sessions/{id}", server.executionSession)
	server.mux.HandleFunc("GET /v1/execution/sessions/{id}/interleaves", server.executionInterleaves)
	server.mux.HandleFunc("POST /v1/execution/sessions/{id}/events", server.executionSessionEvents)
	server.mux.HandleFunc("POST /v1/execution/sessions/{id}/return-to-primary", server.returnToPrimaryExecution)
	server.mux.HandleFunc("GET /v1/alignment/current", server.alignmentCurrent)
	server.mux.HandleFunc("GET /v1/alignment/history", server.alignmentHistory)
	server.mux.HandleFunc("GET /v1/execution/summary", server.executionSummary)
	server.mux.HandleFunc("/v1/activities", server.activities)
	server.mux.HandleFunc("PATCH /v1/activities/{id}", server.activity)
	server.mux.HandleFunc("/v1/plans/{date}", server.plan)
	server.mux.HandleFunc("GET /v1/plans/{date}/versions", server.planVersions)
	server.mux.HandleFunc("PATCH /v1/plans/{date}/blocks/{id}", server.planBlock)
	server.mux.HandleFunc("GET /v1/plans/{date}/blocks/{id}/events", server.planBlockEvents)
	server.mux.HandleFunc("/v1/planning/settings", server.planningSettings)
	server.mux.HandleFunc("/v1/plans/{date}/replan-proposals", server.replanProposals)
	server.mux.HandleFunc("/v1/context-rules", server.contextRules)
	server.mux.HandleFunc("/v1/policy/config", server.policyConfig)
	server.mux.HandleFunc("GET /v1/policy/current", server.policyCurrent)
	server.mux.HandleFunc("GET /v1/policy/timeline", server.policyTimeline)
	server.mux.HandleFunc("GET /v1/policy/suggestions", server.policySuggestions)
	server.mux.HandleFunc("GET /v1/device/now", server.deviceNow)
	server.mux.HandleFunc("GET /v1/device/insights", server.deviceInsights)
	server.mux.HandleFunc("POST /v1/device/panel-session", server.devicePanelSession)
	server.mux.HandleFunc("POST /v1/context-rules/{id}/disable", server.disableContextRule)
	server.mux.HandleFunc("GET /v1/plans/{date}/replan-proposals/{id}", server.replanProposal)
	server.mux.HandleFunc("POST /v1/plans/{date}/replan-proposals/{id}/accept", server.acceptReplanProposal)
	server.mux.HandleFunc("POST /v1/plans/{date}/replan-proposals/{id}/reject", server.rejectReplanProposal)
	server.mux.HandleFunc("GET /v1/activity/summary", server.activitySummary)
	server.mux.HandleFunc("GET /v1/policy/proposals", server.policyProposals)
	server.mux.HandleFunc("POST /v1/policy/proposals/{id}/accept", server.acceptPolicyProposal)
	server.mux.HandleFunc("POST /v1/policy/proposals/{id}/reject", server.rejectPolicyProposal)
	server.mux.HandleFunc("GET /v1/activity-labels", server.activityLabels)
	server.mux.HandleFunc("DELETE /v1/activity-labels/{id}", server.removeActivityLabel)
	server.mux.HandleFunc("POST /v1/activity-label-proposals/{id}/accept", server.acceptLabelProposal)
	server.mux.HandleFunc("POST /v1/activity-label-proposals/{id}/reject", server.rejectLabelProposal)
	return server, nil
}

func (s *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	s.mux.ServeHTTP(writer, request)
}

func (s *Server) health(writer http.ResponseWriter, request *http.Request) {
	if err := s.repository.Ping(request.Context()); err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) ingest(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, s.config.MaxRequestSize)
	decoder := json.NewDecoder(request.Body)
	var batch model.Batch
	if err := decoder.Decode(&batch); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one batch")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one batch")
		return
	}
	if err := batch.Normalize(); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_batch", err.Error())
		return
	}
	if !s.ingestAuthorized(request, batch.DeviceID) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "valid ingest credentials are required for this device")
		return
	}
	receipt, err := s.repository.Ingest(request.Context(), batch)
	if errors.Is(err, store.ErrBatchConflict) {
		writeError(writer, http.StatusConflict, "batch_conflict", err.Error())
		return
	}
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	if batchHasAgentLifecycle(batch) {
		s.reconcileAgentLifecycle(request.Context())
	}
	writeJSON(writer, http.StatusAccepted, receipt)
}

func (s *Server) observations(writer http.ResponseWriter, request *http.Request) {
	if !s.readAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "valid read credentials are required")
		return
	}
	query, err := observationQuery(request)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	values, err := s.repository.QueryObservations(request.Context(), query)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"observations": values})
}

func (s *Server) sources(writer http.ResponseWriter, request *http.Request) {
	if !s.readAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "valid read credentials are required")
		return
	}
	limit, err := limit(request, 100)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	values, err := s.repository.ListSources(request.Context(), limit)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"sources": values})
}

func (s *Server) collectionHealth(writer http.ResponseWriter, request *http.Request) {
	if !s.readAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "valid read credentials are required")
		return
	}
	query, err := collectionHealthQuery(request)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	value, err := s.repository.CollectionHealth(request.Context(), query)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, value)
}

func (s *Server) authorized(request *http.Request, token string) bool {
	value := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
	if value == request.Header.Get("Authorization") {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(value), []byte(token)) == 1
}

// ingestAuthorized keeps the existing global token for legacy collectors while
// allowing a device to use a token that is valid only for its declared ID. A
// scoped request must carry both the device header and a matching batch ID.
func (s *Server) ingestAuthorized(request *http.Request, deviceID string) bool {
	claimedDevice := strings.TrimSpace(request.Header.Get("X-Telemetry-Device-ID"))
	if claimedDevice != "" && claimedDevice != deviceID {
		return false
	}
	token, bearer := strings.CutPrefix(request.Header.Get("Authorization"), "Bearer ")
	if s.config.DeviceAuthorized != nil {
		registered, authorized, err := s.config.DeviceAuthorized(request.Context(), deviceID, token)
		if err != nil {
			return false
		}
		if registered {
			return bearer && claimedDevice == deviceID && authorized
		}
	}
	if claimedDevice != "" {
		token, exists := s.config.IngestDeviceTokens[claimedDevice]
		return exists && s.authorized(request, token)
	}
	return s.authorized(request, s.config.IngestToken)
}

func (s *Server) readAuthorized(request *http.Request) bool {
	if s.config.OwnerAuthorized != nil {
		return s.authorized(request, s.config.ReadToken) || s.ownerAuthorized(request)
	}
	return s.authorized(request, s.config.ReadToken) || s.panelSessionAuthorized(request)
}

func (s *Server) panelSessionsEnabled() bool {
	return s.config.OwnerAuthorized == nil && s.config.PanelToken != "" && s.config.PanelSessionSecret != ""
}

func (s *Server) panelSessionStatus(writer http.ResponseWriter, request *http.Request) {
	if s.config.OwnerAuthorized != nil {
		writer.Header().Set("Cache-Control", "no-store")
		writeJSON(writer, http.StatusOK, map[string]bool{"authenticated": s.ownerAuthorized(request)})
		return
	}
	if !s.panelSessionsEnabled() {
		writeError(writer, http.StatusNotFound, "panel_unavailable", "panel sessions are not configured")
		return
	}
	if !s.panelSessionAuthorized(request) {
		writeJSON(writer, http.StatusOK, map[string]bool{"authenticated": false})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"authenticated": true})
}

func (s *Server) startPanelSession(writer http.ResponseWriter, request *http.Request) {
	if !s.panelSessionsEnabled() {
		writeError(writer, http.StatusNotFound, "panel_unavailable", "panel sessions are not configured")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 4096)
	var input struct {
		Token string `json:"token"`
	}
	decoder := json.NewDecoder(request.Body)
	if err := decoder.Decode(&input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one panel token")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one panel token")
		return
	}
	if subtle.ConstantTimeCompare([]byte(input.Token), []byte(s.config.PanelToken)) != 1 {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "valid panel credentials are required")
		return
	}
	expiresAt := time.Now().UTC().Add(s.panelSessionTTL())
	http.SetCookie(writer, s.panelCookie(expiresAt))
	writeJSON(writer, http.StatusOK, map[string]any{"authenticated": true, "expires_at": expiresAt})
}

func (s *Server) endPanelSession(writer http.ResponseWriter, request *http.Request) {
	if !s.panelSessionsEnabled() {
		writeError(writer, http.StatusNotFound, "panel_unavailable", "panel sessions are not configured")
		return
	}
	http.SetCookie(writer, &http.Cookie{
		Name:     panelSessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.config.PanelCookieSecure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
	})
	writeJSON(writer, http.StatusOK, map[string]bool{"authenticated": false})
}

const panelSessionCookieName = "telemetry_panel_session"

func (s *Server) panelSessionTTL() time.Duration {
	if s.config.PanelSessionTTL <= 0 {
		return 12 * time.Hour
	}
	return s.config.PanelSessionTTL
}

func (s *Server) panelCookie(expiresAt time.Time) *http.Cookie {
	payload := fmt.Sprintf("%d", expiresAt.Unix())
	return &http.Cookie{
		Name:     panelSessionCookieName,
		Value:    payload + "." + s.panelSessionSignature(payload),
		Path:     "/",
		HttpOnly: true,
		Secure:   s.config.PanelCookieSecure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
		Expires:  expiresAt,
	}
}

func (s *Server) panelSessionAuthorized(request *http.Request) bool {
	if !s.panelSessionsEnabled() {
		return false
	}
	cookie, err := request.Cookie(panelSessionCookieName)
	if err != nil {
		return false
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 {
		return false
	}
	expiresAt, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().UTC().Unix() >= expiresAt {
		return false
	}
	actual, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	expected, err := base64.RawURLEncoding.DecodeString(s.panelSessionSignature(parts[0]))
	if err != nil {
		return false
	}
	return hmac.Equal(actual, expected)
}

func (s *Server) panelSessionSignature(payload string) string {
	mac := hmac.New(sha256.New, []byte(s.config.PanelSessionSecret))
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func observationQuery(request *http.Request) (store.Query, error) {
	parsed := store.Query{SourceInstanceKey: request.URL.Query().Get("source_instance_key")}
	limit, err := limit(request, 100)
	if err != nil {
		return parsed, err
	}
	parsed.Limit = limit
	if value := request.URL.Query().Get("from"); value != "" {
		parsed.From, err = parseTime(value)
		if err != nil {
			return parsed, fmt.Errorf("from: %w", err)
		}
	}
	if value := request.URL.Query().Get("to"); value != "" {
		parsed.To, err = parseTime(value)
		if err != nil {
			return parsed, fmt.Errorf("to: %w", err)
		}
	}
	if parsed.From != nil && parsed.To != nil && !parsed.From.Before(*parsed.To) {
		return parsed, errors.New("from must be before to")
	}
	return parsed, nil
}

func collectionHealthQuery(request *http.Request) (store.CollectionHealthQuery, error) {
	sourceLimit, err := limit(request, 100)
	if err != nil {
		return store.CollectionHealthQuery{}, err
	}
	samples := 24
	if value := request.URL.Query().Get("samples"); value != "" {
		samples, err = strconv.Atoi(value)
		if err != nil || samples < 1 || samples > 72 {
			return store.CollectionHealthQuery{}, errors.New("samples must be an integer from 1 through 72")
		}
	}
	return store.CollectionHealthQuery{SourceLimit: sourceLimit, ResourceSamples: samples}, nil
}

func parseTime(value string) (*time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, errors.New("must be RFC3339")
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func limit(request *http.Request, fallback int) (int, error) {
	value := request.URL.Query().Get("limit")
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 || parsed > 1000 {
		return 0, errors.New("limit must be an integer from 1 through 1000")
	}
	return parsed, nil
}

func writeError(writer http.ResponseWriter, status int, code, detail string) {
	writeJSON(writer, status, map[string]any{"error": map[string]string{"code": code, "detail": detail}})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
