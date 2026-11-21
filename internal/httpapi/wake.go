package httpapi

import (
	"net/http"
	"time"

	"github.com/rimexe0/telemetry/internal/store"
)

// wakeTime answers when the owner last woke before ?at= (RFC 3339): the end
// of the last long quiet stretch on the devices that recorded.
// The panel's day timeline starts there instead of at midnight. wake_at is
// null when no such stretch is found.
func (s *Server) wakeTime(writer http.ResponseWriter, request *http.Request) {
	if !s.readAuthorized(request) && !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	at := time.Now().UTC()
	if raw := request.URL.Query().Get("at"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_request", "at must be an RFC 3339 time")
			return
		}
		if parsed.Before(at) {
			at = parsed.UTC()
		}
	}
	wake, err := store.LoadShownWakeTime(request.Context(), s.repository, at)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, map[string]any{"at": at, "wake_at": wake})
}
