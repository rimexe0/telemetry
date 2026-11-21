package enrollment

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"rsc.io/qr"
)

// DefaultJoinPage opens a setup code on a phone. It is a static page: the
// code travels after the #, which browsers never send to any server, and the
// page only hands it to the Telemetry app (or offers the app first).
const DefaultJoinPage = "https://te.rime.cyou/join"

// HandlerConfig connects enrollment to the outer authentication boundary.
// VerifyOwner must validate the Clerk bearer token and return true only for the
// configured owner. The enrollment package deliberately does not parse Clerk
// tokens or know provider-specific claims.
type HandlerConfig struct {
	VerifyOwner  func(*http.Request) bool
	PublicURL    string
	MaxBodyBytes int64
	// RelayKey is the server's public key for sealed requests through a
	// relay, empty when this server has no relay. Setup codes carry it.
	RelayKey string
	// JoinPage is the page a setup code's QR opens; DefaultJoinPage if empty.
	JoinPage string
}

// Handler exposes collector pairing and owner device management endpoints.
type Handler struct {
	service       *Service
	verifyOwner   func(*http.Request) bool
	publicURL     string
	trustedOrigin string
	maxBodyBytes  int64
	relayKey      string
	joinPage      string
	mux           *http.ServeMux
}

// NewHandler builds the pairing router. PublicURL is also the sole trusted
// browser origin for approving and revoking devices; this keeps the approval
// endpoints CSRF-protected when the API is reachable through Tailscale.
func NewHandler(service *Service, config HandlerConfig) (*Handler, error) {
	if service == nil || service.pool == nil {
		return nil, errors.New("enrollment service is required")
	}
	if config.VerifyOwner == nil {
		return nil, errors.New("owner verifier is required")
	}
	publicURL := strings.TrimRight(strings.TrimSpace(config.PublicURL), "/")
	if publicURL == "" {
		return nil, errors.New("public URL is required")
	}
	origin, err := canonicalOrigin(publicURL)
	if err != nil {
		return nil, fmt.Errorf("public URL: %w", err)
	}
	if config.MaxBodyBytes <= 0 {
		config.MaxBodyBytes = 16 << 10
	}
	handler := &Handler{
		service:       service,
		verifyOwner:   config.VerifyOwner,
		publicURL:     publicURL,
		trustedOrigin: origin,
		maxBodyBytes:  config.MaxBodyBytes,
		relayKey:      strings.TrimSpace(config.RelayKey),
		joinPage:      strings.TrimSpace(config.JoinPage),
		mux:           http.NewServeMux(),
	}
	if handler.joinPage == "" {
		handler.joinPage = DefaultJoinPage
	}
	handler.mux.HandleFunc("POST /v1/pairing/sessions", handler.start)
	handler.mux.HandleFunc("GET /v1/pairing/sessions", handler.listPending)
	handler.mux.HandleFunc("GET /v1/pairing/sessions/{pairing_id}", handler.poll)
	handler.mux.HandleFunc("GET /v1/pairing/sessions/{pairing_id}/details", handler.details)
	handler.mux.HandleFunc("POST /v1/pairing/sessions/{pairing_id}/approve", handler.approve)
	handler.mux.HandleFunc("POST /v1/pairing/invites", handler.createInvite)
	handler.mux.HandleFunc("GET /v1/pairing/invites", handler.listInvites)
	handler.mux.HandleFunc("POST /v1/pairing/invites/{invite_id}/cancel", handler.cancelInvite)
	handler.mux.HandleFunc("GET /v1/pairing/settings", handler.settings)
	handler.mux.HandleFunc("PUT /v1/pairing/settings", handler.saveSettings)
	handler.mux.HandleFunc("GET /v1/devices", handler.listDevices)
	handler.mux.HandleFunc("POST /v1/devices/{device_id}/revoke", handler.revoke)
	return handler, nil
}

func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	h.mux.ServeHTTP(writer, request)
}

type startRequest struct {
	DeviceID       string `json:"device_id"`
	DeviceName     string `json:"device_name"`
	CredentialHash string `json:"credential_hash"`
	// Code is a setup code made in the panel; with it the device is
	// connected at once.
	Code string `json:"code,omitempty"`
}

func (h *Handler) start(writer http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Origin") != "" && !h.trusted(request) {
		writeError(writer, http.StatusForbidden, "untrusted_origin", "pairing must originate from the configured server or a native collector")
		return
	}
	var input startRequest
	if err := decodeJSON(writer, request, h.maxBodyBytes, &input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	hash, err := ParseCredentialHash(input.CredentialHash)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_credential_hash", err.Error())
		return
	}
	session, err := h.service.StartWithCode(request.Context(), input.DeviceID, input.DeviceName, hash, input.Code)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{
		"pairing_id": session.PairingID,
		"panel_url":  h.publicURL + "/panel/?pair=" + url.QueryEscape(session.PairingID),
		"expires_at": session.ExpiresAt,
		"status":     session.Status,
	})
}

func (h *Handler) poll(writer http.ResponseWriter, request *http.Request) {
	pairingID := request.PathValue("pairing_id")
	secret, ok := bearerToken(request)
	if !ok {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "collector bearer credentials are required")
		return
	}
	session, err := h.service.Poll(request.Context(), pairingID, secret)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"status": session.Status, "device_id": session.DeviceID})
}

func (h *Handler) details(writer http.ResponseWriter, request *http.Request) {
	if !h.verifyOwner(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	session, err := h.service.Details(request.Context(), request.PathValue("pairing_id"))
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, session)
}

func (h *Handler) approve(writer http.ResponseWriter, request *http.Request) {
	if !h.trusted(request) {
		writeError(writer, http.StatusForbidden, "untrusted_origin", "approval must come from the configured Telemetry origin")
		return
	}
	if !h.verifyOwner(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	// Accept an explicit empty JSON object. This prevents accidental method
	// calls with an unrelated payload while keeping the browser request simple.
	if err := decodeJSON(writer, request, h.maxBodyBytes, &struct{}{}); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	session, err := h.service.Approve(request.Context(), request.PathValue("pairing_id"))
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"status": session.Status, "device_id": session.DeviceID})
}

func (h *Handler) listPending(writer http.ResponseWriter, request *http.Request) {
	if !h.verifyOwner(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	sessions, err := h.service.ListPending(request.Context())
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"sessions": sessions})
}

func (h *Handler) listDevices(writer http.ResponseWriter, request *http.Request) {
	if !h.verifyOwner(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	devices, err := h.service.ListDevices(request.Context())
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"devices": devices})
}

func (h *Handler) revoke(writer http.ResponseWriter, request *http.Request) {
	if !h.trusted(request) {
		writeError(writer, http.StatusForbidden, "untrusted_origin", "revocation must come from the configured Telemetry origin")
		return
	}
	if !h.verifyOwner(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	device, err := h.service.Revoke(request.Context(), request.PathValue("device_id"))
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"status": device.Status, "device_id": device.DeviceID})
}

// ownerWrite checks a state-changing owner request: the configured origin,
// the owner's sign-in, and a JSON body.
func (h *Handler) ownerWrite(writer http.ResponseWriter, request *http.Request, body any) bool {
	if !h.trusted(request) {
		writeError(writer, http.StatusForbidden, "untrusted_origin", "this change must come from the configured Telemetry origin")
		return false
	}
	if !h.verifyOwner(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return false
	}
	if err := decodeJSON(writer, request, h.maxBodyBytes, body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	return true
}

// SetupCode is everything a new device needs to connect, made in the panel.
type SetupCode struct {
	Invite
	ServerURL string `json:"server_url"`
	RelayURL  string `json:"relay_url,omitempty"`
	RelayKey  string `json:"relay_key,omitempty"`
	// JoinURL is what the QR holds: the join page, with the details after #.
	JoinURL string `json:"join_url"`
	// SetupLink opens the Telemetry phone app directly.
	SetupLink string `json:"setup_link"`
	// Commands install and connect a computer in one step.
	Commands map[string]string `json:"commands"`
	// QR is a PNG of JoinURL as a data: URL.
	QR string `json:"qr"`
}

func (h *Handler) createInvite(writer http.ResponseWriter, request *http.Request) {
	if !h.ownerWrite(writer, request, &struct{}{}) {
		return
	}
	relayURL, err := h.service.RelayURL(request.Context())
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	invite, err := h.service.CreateInvite(request.Context())
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	code := SetupCode{Invite: invite, ServerURL: h.publicURL}
	join := url.Values{"s": {h.publicURL}, "c": {invite.Code}}
	link := url.Values{"server": {h.publicURL}, "code": {invite.Code}}
	if relayURL != "" && h.relayKey != "" {
		code.RelayURL, code.RelayKey = relayURL, h.relayKey
		join.Set("r", relayURL)
		join.Set("k", h.relayKey)
		link.Set("relay", relayURL)
		link.Set("key", h.relayKey)
	}
	code.JoinURL = h.joinPage + "#" + join.Encode()
	code.SetupLink = "telemetry://setup?" + link.Encode()
	code.Commands = map[string]string{
		"mac":   "curl -fsSL https://te.rime.cyou/i/mac | bash -s -- '" + code.JoinURL + "'",
		"linux": "curl -fsSL https://te.rime.cyou/i/linux | bash -s -- '" + code.JoinURL + "'",
	}
	symbol, err := qr.Encode(code.JoinURL, qr.M)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "qr_failed", err.Error())
		return
	}
	symbol.Scale = 8
	code.QR = "data:image/png;base64," + base64.StdEncoding.EncodeToString(symbol.PNG())
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusCreated, code)
}

func (h *Handler) listInvites(writer http.ResponseWriter, request *http.Request) {
	if !h.verifyOwner(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	invites, err := h.service.ListInvites(request.Context())
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, map[string]any{"invites": invites})
}

func (h *Handler) cancelInvite(writer http.ResponseWriter, request *http.Request) {
	if !h.ownerWrite(writer, request, &struct{}{}) {
		return
	}
	if err := h.service.CancelInvite(request.Context(), request.PathValue("invite_id")); err != nil {
		writeServiceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "cancelled"})
}

func (h *Handler) settings(writer http.ResponseWriter, request *http.Request) {
	if !h.verifyOwner(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	relayURL, err := h.service.RelayURL(request.Context())
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"server_url": h.publicURL, "relay_url": relayURL, "relay_key": h.relayKey, "relay_available": h.relayKey != ""})
}

func (h *Handler) saveSettings(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		RelayURL string `json:"relay_url"`
	}
	if !h.ownerWrite(writer, request, &input) {
		return
	}
	if err := h.service.SetRelayURL(request.Context(), input.RelayURL); err != nil {
		writeServiceError(writer, err)
		return
	}
	h.settings(writer, request)
}

func (h *Handler) trusted(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return false
	}
	canonical, err := canonicalOrigin(origin)
	return err == nil && canonical == h.trustedOrigin
}

func bearerToken(request *http.Request) (string, bool) {
	value := request.Header.Get("Authorization")
	if !strings.HasPrefix(value, "Bearer ") {
		return "", false
	}
	token := strings.TrimPrefix(value, "Bearer ")
	return token, token != "" && !strings.ContainsAny(token, "\r\n")
}

func canonicalOrigin(value string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("must be an absolute origin without a path")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errors.New("origin scheme must be http or https")
	}
	return scheme + "://" + strings.ToLower(parsed.Host), nil
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, limit int64, value any) error {
	request.Body = http.MaxBytesReader(writer, request.Body, limit)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return errors.New("request body must contain one JSON object")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func writeServiceError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidCode):
		writeError(writer, http.StatusForbidden, "invalid_code", err.Error())
	case errors.Is(err, ErrInviteLimit):
		writeError(writer, http.StatusTooManyRequests, "invite_limit", err.Error())
	case errors.Is(err, ErrInviteNotFound):
		writeError(writer, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, ErrInvalidRelayURL):
		writeError(writer, http.StatusBadRequest, "invalid_request", err.Error())
	case errors.Is(err, ErrUnauthorized):
		writeError(writer, http.StatusUnauthorized, "unauthorized", "valid collector credentials are required")
	case errors.Is(err, ErrAlreadyRegistered):
		writeError(writer, http.StatusConflict, "device_already_registered", err.Error())
	case errors.Is(err, ErrPendingLimit), errors.Is(err, ErrRateLimited):
		writeError(writer, http.StatusTooManyRequests, "pairing_rate_limited", err.Error())
	case errors.Is(err, ErrPairingExpired), errors.Is(err, ErrDeviceRevoked):
		writeError(writer, http.StatusGone, "pairing_unavailable", err.Error())
	case errors.Is(err, ErrPairingNotFound), errors.Is(err, ErrDeviceNotFound):
		writeError(writer, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, ErrPairingNotPending):
		writeError(writer, http.StatusConflict, "pairing_not_pending", err.Error())
	case errors.Is(err, ErrInvalidDeviceID), errors.Is(err, ErrInvalidDeviceName), errors.Is(err, ErrInvalidCredentialHash), errors.Is(err, ErrInvalidPairingID), errors.Is(err, ErrInvalidSecret):
		writeError(writer, http.StatusBadRequest, "invalid_request", err.Error())
	default:
		writeError(writer, http.StatusServiceUnavailable, "enrollment_unavailable", "enrollment storage is unavailable")
	}
}

func writeError(writer http.ResponseWriter, status int, code, detail string) {
	writeJSON(writer, status, map[string]any{"error": map[string]string{"code": code, "detail": detail}})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

// Keep time imported in this file's public JSON contract if a future handler
// adds explicit cache controls; currently encoding/json handles time.Time.
var _ = time.RFC3339
