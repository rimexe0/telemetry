package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/rimexe0/telemetry/internal/store"
)

// wakeTime answers when the owner last woke before ?at= (RFC 3339): the end
// of the last long quiet stretch on the devices that recorded.
// The panel's day timeline starts there instead of at midnight. wake_at is
// null when no such stretch is found. planned_wake is the wake time the
// owner set (the end of bedtime) on at's local date, once it has passed; the
// timeline starts at the earlier of the two.
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
	writeJSON(writer, http.StatusOK, map[string]any{"at": at, "wake_at": wake, "planned_wake": s.plannedWake(request.Context(), "", at)})
}

// plannedWake is the end of bedtime on a local date (empty: now's), when it
// is not after now; nil without a bedtime or a stored policy.
func (s *Server) plannedWake(ctx context.Context, date string, now time.Time) *time.Time {
	stored, err := s.repository.LatestPolicy(ctx)
	if err != nil || stored == nil {
		return nil
	}
	config := stored.Config
	if config.Normalize() != nil {
		return nil
	}
	if date == "" {
		location, err := time.LoadLocation(config.Timezone)
		if err != nil {
			location = time.UTC
		}
		date = now.In(location).Format("2006-01-02")
	}
	wake := config.PlannedWake(date)
	if wake == nil || wake.After(now) {
		return nil
	}
	return wake
}
