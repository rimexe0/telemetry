package awserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxBody = 1 << 20

// Handler serves the part of the ActivityWatch REST API that its browser
// extension and aw-watcher-codex use. It must only listen on loopback.
type Handler struct {
	Store    *Store
	Hostname string
	Version  string
	// Site, when set, answers whether a website is blocked right now, for
	// the Telemetry browser extension. address is the page's full address
	// when the extension sends it.
	Site func(host, address string) SiteDecision
	// Status, when set, describes the decision in force for the extension.
	Status func() BrowserStatus
	// CheckIn, when set, hears each request from the Telemetry extension:
	// the connection's remote address, and whether the extension may run in
	// private windows when it said so.
	CheckIn func(remote string, incognito *bool)
}

// BrowserStatus is what the extension shows in its popup.
type BrowserStatus struct {
	Connected     bool       `json:"connected"`
	Mode          string     `json:"mode,omitempty"`
	Reason        string     `json:"reason,omitempty"`
	BlockingSites []string   `json:"blocking_sites"`
	BlockAll      bool       `json:"block_all"`
	NextBlockAt   *time.Time `json:"next_block_at,omitempty"`
	NextBlockMode string     `json:"next_block_mode,omitempty"`
	FetchedAt     *time.Time `json:"decisions_fetched_at,omitempty"`
}

// SiteDecision is what the browser extension needs to block a site.
type SiteDecision struct {
	Host    string `json:"host"`
	Blocked bool   `json:"blocked"`
	Mode    string `json:"mode,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

func (h Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	origin := request.Header.Get("Origin")
	if origin != "" {
		// Web pages must not be able to write activity; browser extensions may.
		if !extensionOrigin(origin) {
			writeError(writer, http.StatusForbidden, "origin not allowed")
			return
		}
		writer.Header().Set("Access-Control-Allow-Origin", origin)
		writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE")
		writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if request.Method == http.MethodOptions {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
	}
	// Chromium can leave out the Origin of an extension's request to an
	// address it has permission for, so every request to these two counts;
	// the blocker works out from the connection which app made it.
	if request.URL.Path == "/api/telemetry/status" && request.Method == http.MethodGet {
		if h.CheckIn != nil {
			var incognito *bool
			if value := request.URL.Query().Get("incognito"); value != "" {
				allowed := value == "1"
				incognito = &allowed
			}
			h.CheckIn(request.RemoteAddr, incognito)
		}
		status := BrowserStatus{BlockingSites: []string{}}
		if h.Status != nil {
			status = h.Status()
		}
		writeJSON(writer, http.StatusOK, status)
		return
	}
	if request.URL.Path == "/api/telemetry/site" && request.Method == http.MethodGet {
		host := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("host")))
		decision := SiteDecision{Host: host}
		if h.CheckIn != nil {
			h.CheckIn(request.RemoteAddr, nil)
		}
		if h.Site != nil && host != "" {
			decision = h.Site(host, request.URL.Query().Get("url"))
		}
		writeJSON(writer, http.StatusOK, decision)
		return
	}
	path, ok := strings.CutPrefix(request.URL.EscapedPath(), "/api/0/")
	if !ok {
		writeError(writer, http.StatusNotFound, "not found")
		return
	}
	parts := strings.Split(strings.TrimSuffix(path, "/"), "/")
	switch {
	case len(parts) == 1 && parts[0] == "info" && request.Method == http.MethodGet:
		writeJSON(writer, http.StatusOK, map[string]any{"hostname": h.Hostname, "version": h.Version, "testing": false, "device_id": h.Store.deviceID})
	case len(parts) == 1 && parts[0] == "buckets" && request.Method == http.MethodGet:
		writeJSON(writer, http.StatusOK, h.Store.Buckets())
	case len(parts) >= 2 && parts[0] == "buckets":
		id, err := url.PathUnescape(parts[1])
		if err != nil || id == "" {
			writeError(writer, http.StatusBadRequest, "invalid bucket id")
			return
		}
		h.bucket(writer, request, id, parts[2:])
	default:
		writeError(writer, http.StatusNotFound, "not supported by the Telemetry desktop collector")
	}
}

func (h Handler) bucket(writer http.ResponseWriter, request *http.Request, id string, rest []string) {
	ctx := request.Context()
	switch {
	case len(rest) == 0 && request.Method == http.MethodGet:
		bucket, ok := h.Store.Bucket(id)
		if !ok {
			writeError(writer, http.StatusNotFound, ErrNoBucket.Error())
			return
		}
		writeJSON(writer, http.StatusOK, bucket)
	case len(rest) == 0 && request.Method == http.MethodPost:
		var bucket Bucket
		if !readJSON(writer, request, &bucket) {
			return
		}
		bucket.ID = id
		created, err := h.Store.EnsureBucket(ctx, bucket)
		if err != nil {
			writeError(writer, http.StatusInternalServerError, err.Error())
			return
		}
		if !created {
			// ActivityWatch clients treat 304 as "already exists".
			writer.WriteHeader(http.StatusNotModified)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{})
	case len(rest) == 0 && request.Method == http.MethodDelete:
		if err := h.Store.DeleteBucket(ctx, id); errors.Is(err, ErrNoBucket) {
			writeError(writer, http.StatusNotFound, err.Error())
		} else if err != nil {
			writeError(writer, http.StatusInternalServerError, err.Error())
		} else {
			writeJSON(writer, http.StatusOK, map[string]any{})
		}
	case len(rest) == 1 && rest[0] == "heartbeat" && request.Method == http.MethodPost:
		pulsetime, err := strconv.ParseFloat(request.URL.Query().Get("pulsetime"), 64)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "pulsetime is required")
			return
		}
		var heartbeat Event
		if !readJSON(writer, request, &heartbeat) {
			return
		}
		event, err := h.Store.Heartbeat(ctx, id, heartbeat, pulsetime)
		if !writeStoreError(writer, err) {
			writeJSON(writer, http.StatusOK, event)
		}
	case len(rest) == 1 && rest[0] == "events" && request.Method == http.MethodPost:
		var raw json.RawMessage
		if !readJSON(writer, request, &raw) {
			return
		}
		var events []Event
		if strings.HasPrefix(strings.TrimSpace(string(raw)), "[") {
			if err := json.Unmarshal(raw, &events); err != nil {
				writeError(writer, http.StatusBadRequest, "invalid events")
				return
			}
		} else {
			var event Event
			if err := json.Unmarshal(raw, &event); err != nil {
				writeError(writer, http.StatusBadRequest, "invalid event")
				return
			}
			events = []Event{event}
		}
		inserted, err := h.Store.Insert(ctx, id, events)
		if !writeStoreError(writer, err) {
			writeJSON(writer, http.StatusOK, inserted)
		}
	default:
		writeError(writer, http.StatusNotFound, "not supported by the Telemetry desktop collector")
	}
}

func extensionOrigin(origin string) bool {
	for _, prefix := range []string{"chrome-extension://", "moz-extension://", "safari-web-extension://"} {
		if strings.HasPrefix(origin, prefix) {
			return true
		}
	}
	return false
}

func readJSON(writer http.ResponseWriter, request *http.Request, target any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, maxBody))
	if err != nil || json.Unmarshal(body, target) != nil {
		writeError(writer, http.StatusBadRequest, "request body must be JSON")
		return false
	}
	return true
}

func writeStoreError(writer http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, ErrNoBucket):
		writeError(writer, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrInvalidEvent):
		writeError(writer, http.StatusBadRequest, err.Error())
	default:
		writeError(writer, http.StatusInternalServerError, err.Error())
	}
	return true
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeError(writer http.ResponseWriter, status int, message string) {
	writeJSON(writer, status, map[string]string{"message": message})
}
