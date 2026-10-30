package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/rimexe0/telemetry/internal/policy"
	"github.com/rimexe0/telemetry/internal/store"
)

// The settings lock and bedtime (owner request 2026-10-30): blocking cannot
// be loosened at the owner's lock times except after the PIN someone else
// holds, and bed hours lock the screens.

type lockResponse struct {
	Lock          policy.SettingsLock     `json:"lock"`
	LockNext      *policy.SettingsLock    `json:"lock_next,omitempty"`
	LockNextFrom  string                  `json:"lock_next_from,omitempty"`
	Bedtime       policy.Bedtime          `json:"bedtime"`
	BedtimeNext   *policy.Bedtime         `json:"bedtime_next,omitempty"`
	BedtimeFrom   string                  `json:"bedtime_next_from,omitempty"`
	Extension     policy.ExtensionRule    `json:"extension"`
	ExtensionNext *policy.ExtensionRule   `json:"extension_next,omitempty"`
	ExtensionFrom string                  `json:"extension_next_from,omitempty"`
	State         store.SettingsLockState `json:"state"`
}

type lockInput struct {
	Lock      *policy.SettingsLock  `json:"lock,omitempty"`
	Bedtime   *policy.Bedtime       `json:"bedtime,omitempty"`
	Extension *policy.ExtensionRule `json:"extension,omitempty"`
}

func (s *Server) settingsLock(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	if request.Method == http.MethodPut {
		request.Body = http.MaxBytesReader(writer, request.Body, s.config.MaxRequestSize)
		var input lockInput
		if err := decodeExecutionJSON(request, &input); err != nil || (input.Lock == nil && input.Bedtime == nil && input.Extension == nil) {
			writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain lock, bedtime or extension")
			return
		}
		_, _, err := s.repository.ApplyBlockingSchedule(request.Context(), store.ScheduleChange{SettingsLock: input.Lock, Bedtime: input.Bedtime, Extension: input.Extension}, "Settings lock, bedtime or browser extension rule set in the panel")
		switch {
		case errors.Is(err, store.ErrSettingsLocked):
			writeError(writer, http.StatusLocked, "settings_locked", err.Error())
			return
		case errors.Is(err, store.ErrInvalidAgentChange):
			writeError(writer, http.StatusBadRequest, "invalid_rule", err.Error())
			return
		case err != nil:
			writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
			return
		}
	} else if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or PUT")
		return
	}
	ctx := request.Context()
	config := policy.Config{}
	stored, err := s.repository.LatestPolicy(ctx)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	if stored != nil {
		config = stored.Config
	}
	if err := config.Normalize(); err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	now := time.Now().UTC()
	location, _ := time.LoadLocation(config.Timezone)
	today := now.In(location).Format("2006-01-02")
	response := lockResponse{Lock: config.SettingsLockAt(now), Bedtime: config.BedtimeAt(now), Extension: config.ExtensionRuleAt(now)}
	if config.ExtensionNext != nil && config.ExtensionNextFrom > today {
		response.ExtensionNext, response.ExtensionFrom = config.ExtensionNext, config.ExtensionNextFrom
	}
	if config.SettingsLockNext != nil && config.SettingsLockNextFrom > today {
		response.LockNext, response.LockNextFrom = config.SettingsLockNext, config.SettingsLockNextFrom
	}
	if config.BedtimeNext != nil && config.BedtimeNextFrom > today {
		response.BedtimeNext, response.BedtimeFrom = config.BedtimeNext, config.BedtimeNextFrom
	}
	if response.State, err = s.repository.SettingsLock(ctx); err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func (s *Server) settingsPin(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, s.config.MaxRequestSize)
	var input struct {
		Current string `json:"current"`
		Pin     string `json:"pin"`
	}
	if err := decodeExecutionJSON(request, &input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain the PIN")
		return
	}
	err := s.repository.SetSettingsPin(request.Context(), input.Current, input.Pin)
	switch {
	case errors.Is(err, store.ErrSettingsLocked):
		writeError(writer, http.StatusLocked, "settings_locked", err.Error())
	case errors.Is(err, store.ErrInvalidAgentChange):
		writeError(writer, http.StatusBadRequest, "invalid_pin", err.Error())
	case err != nil:
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
	default:
		writeJSON(writer, http.StatusOK, map[string]bool{"pin_set": true})
	}
}

func (s *Server) unlockSettings(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, s.config.MaxRequestSize)
	var input struct {
		Pin string `json:"pin"`
	}
	if err := decodeExecutionJSON(request, &input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain the PIN")
		return
	}
	until, err := s.repository.UnlockSettings(request.Context(), input.Pin)
	switch {
	case errors.Is(err, store.ErrSettingsLocked):
		writeError(writer, http.StatusLocked, "settings_locked", err.Error())
	case err != nil:
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
	default:
		writeJSON(writer, http.StatusOK, map[string]time.Time{"unlocked_until": until})
	}
}
