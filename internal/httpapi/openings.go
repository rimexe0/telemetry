package httpapi

import (
	"bytes"
	"encoding/base64"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rimexe0/telemetry/internal/policy"
	"github.com/rimexe0/telemetry/internal/store"
	"rsc.io/qr"
)

// The panel's openings: the owner asks for something closed, confirms, and
// it opens after their wait for at most their longest length (owner request
// 2026-10-26). The same rule applies to openings the assistant makes.

type openingsResponse struct {
	Rule      policy.OpeningRule  `json:"rule"`
	Next      *policy.OpeningRule `json:"next,omitempty"`
	NextFrom  string              `json:"next_from,omitempty"`
	UsedToday int                 `json:"used_today"`
	Openings  []store.Opening     `json:"openings"`
	// Distractions are what can be opened, by group.
	Distractions []policy.TargetGroup `json:"distractions"`
}

type openingInput struct {
	Purpose    string   `json:"purpose"`
	GroupIDs   []string `json:"group_ids,omitempty"`
	Everything bool     `json:"everything,omitempty"`
	Minutes    int      `json:"minutes"`
}

func (s *Server) openingsState(request *http.Request) (openingsResponse, error) {
	ctx := request.Context()
	config, _, _, err := s.policyInputs(ctx, time.Now().UTC(), time.Now().UTC())
	if err != nil {
		return openingsResponse{}, err
	}
	now := time.Now().UTC()
	location, err := time.LoadLocation(config.Timezone)
	if err != nil {
		return openingsResponse{}, err
	}
	response := openingsResponse{Rule: config.OpeningRuleAt(now), Openings: []store.Opening{}, Distractions: config.Distractions}
	if config.OpeningRuleNext != nil && config.OpeningRuleNextFrom > now.In(location).Format("2006-01-02") {
		response.Next, response.NextFrom = config.OpeningRuleNext, config.OpeningRuleNextFrom
	}
	local := now.In(location)
	if response.UsedToday, err = s.repository.OpeningsSince(ctx, time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)); err != nil {
		return openingsResponse{}, err
	}
	if response.Openings, err = s.repository.ActiveOpenings(ctx, now); err != nil {
		return openingsResponse{}, err
	}
	if response.Distractions == nil {
		response.Distractions = []policy.TargetGroup{}
	}
	return response, nil
}

func (s *Server) openings(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	if request.Method == http.MethodPost {
		request.Body = http.MaxBytesReader(writer, request.Body, s.config.MaxRequestSize)
		var input openingInput
		if err := decodeExecutionJSON(request, &input); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one opening")
			return
		}
		if input.Minutes < 1 || input.Minutes > 240 {
			writeError(writer, http.StatusBadRequest, "invalid_opening", "minutes must be between 1 and 240")
			return
		}
		state, err := s.openingsState(request)
		if err != nil {
			writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
			return
		}
		opening := store.Opening{Purpose: input.Purpose, Everything: input.Everything, Until: time.Now().UTC().Add(time.Duration(input.Minutes) * time.Minute)}
		for _, id := range input.GroupIDs {
			for _, group := range state.Distractions {
				if group.ID == id {
					opening.Sites = append(opening.Sites, group.Domains...)
					opening.Apps = append(append(opening.Apps, group.AndroidPackages...), group.MacApps...)
				}
			}
		}
		_, saved, err := s.repository.OpenNow(request.Context(), opening, input.Purpose)
		switch {
		case errors.Is(err, store.ErrOpeningRefused), errors.Is(err, store.ErrInvalidAgentChange):
			writeError(writer, http.StatusConflict, "opening_refused", err.Error())
			return
		case err != nil:
			writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
			return
		}
		writeJSON(writer, http.StatusCreated, map[string]any{"opening": saved})
		return
	}
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or POST")
		return
	}
	state, err := s.openingsState(request)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, state)
}

// endOpening ends an opening now, or cancels it during its wait.
func (s *Server) endOpening(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(writer, http.StatusBadRequest, "invalid_id", "opening id must be a positive integer")
		return
	}
	ended, err := s.repository.EndOpening(request.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "no such opening")
		return
	}
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"opening": ended})
}

// confirmOpening records one of the owner's confirmations of an opening;
// the server refuses one given before its time.
func (s *Server) confirmOpening(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(writer, http.StatusBadRequest, "invalid_id", "opening id must be a positive integer")
		return
	}
	confirmed, err := s.repository.ConfirmOpening(request.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "no such opening")
	case errors.Is(err, store.ErrOpeningRefused):
		writeError(writer, http.StatusConflict, "confirmation_refused", err.Error())
	case err != nil:
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
	default:
		writeJSON(writer, http.StatusOK, map[string]any{"opening": confirmed})
	}
}

// scheduleRule sets the opening or morning rule through the blocking
// schedule, so a looser one waits for the next day and the change is logged
// and can be reverted.
func (s *Server) scheduleRule(writer http.ResponseWriter, request *http.Request, change store.ScheduleChange, note string) {
	_, config, err := s.repository.ApplyBlockingSchedule(request.Context(), change, note)
	if errors.Is(err, store.ErrInvalidAgentChange) {
		writeError(writer, http.StatusBadRequest, "invalid_rule", err.Error())
		return
	}
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	now := time.Now().UTC()
	writeJSON(writer, http.StatusOK, map[string]any{"opening_rule": config.OpeningRuleAt(now), "morning": config.MorningAt(now),
		"opening_rule_next_from": config.OpeningRuleNextFrom, "morning_next_from": config.MorningNextFrom})
}

func (s *Server) openingRule(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, s.config.MaxRequestSize)
	var rule policy.OpeningRule
	if err := decodeExecutionJSON(request, &rule); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one opening rule")
		return
	}
	s.scheduleRule(writer, request, store.ScheduleChange{OpeningRule: &rule}, "Opening rule set in the panel")
}

type morningResponse struct {
	Rule     policy.MorningRule  `json:"rule"`
	Next     *policy.MorningRule `json:"next,omitempty"`
	NextFrom string              `json:"next_from,omitempty"`
	CodeMade *time.Time          `json:"code_made,omitempty"`
	// Holding is true while the morning hold keeps the day shut now.
	Holding     bool       `json:"holding"`
	ScannedAt   *time.Time `json:"scanned_at,omitempty"`
	ScanAddress string     `json:"scan_address"`
}

func (s *Server) morning(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	if request.Method == http.MethodPut {
		request.Body = http.MaxBytesReader(writer, request.Body, s.config.MaxRequestSize)
		var rule policy.MorningRule
		if err := decodeExecutionJSON(request, &rule); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one morning rule")
			return
		}
		s.scheduleRule(writer, request, store.ScheduleChange{Morning: &rule}, "Morning rule set in the panel")
		return
	}
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or PUT")
		return
	}
	ctx := request.Context()
	now := time.Now().UTC()
	config, version, facts, err := s.policyInputs(ctx, now, now)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	location, err := time.LoadLocation(config.Timezone)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	response := morningResponse{Rule: config.MorningAt(now), ScannedAt: facts.MorningScannedAt, ScanAddress: s.scanAddress(request, "")}
	if config.MorningNext != nil && config.MorningNextFrom > now.In(location).Format("2006-01-02") {
		response.Next, response.NextFrom = config.MorningNext, config.MorningNextFrom
	}
	response.Holding = policy.Evaluate(config, version, facts, now).Mode == policy.ModeMorning
	if response.CodeMade, err = s.repository.MorningCodeMade(ctx); err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

// newMorningCode makes a new code to print and retires the old one. It is
// refused while the morning hold keeps the day shut, so a new code on the
// screen cannot stand in for the printed one.
func (s *Server) newMorningCode(writer http.ResponseWriter, request *http.Request) {
	if !s.ownerAuthorized(request) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "owner credentials are required")
		return
	}
	ctx := request.Context()
	now := time.Now().UTC()
	config, version, facts, err := s.policyInputs(ctx, now, now)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	if policy.Evaluate(config, version, facts, now).Mode == policy.ModeMorning {
		writeError(writer, http.StatusConflict, "morning_holding", "a new code can be made once the morning is open")
		return
	}
	code, err := s.repository.CreateMorningCode(ctx)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store_unavailable", err.Error())
		return
	}
	address := s.scanAddress(request, code)
	symbol, err := qr.Encode(address, qr.M)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "qr_failed", err.Error())
		return
	}
	symbol.Scale = 8
	writeJSON(writer, http.StatusCreated, map[string]string{"address": address, "image": "data:image/png;base64," + base64.StdEncoding.EncodeToString(symbol.PNG())})
}

// scanAddress is the address the printed code opens: this server's own
// origin, as the owner's browser reached it.
func (s *Server) scanAddress(request *http.Request, code string) string {
	scheme := "https"
	if request.TLS == nil && request.Header.Get("X-Forwarded-Proto") != "https" && !strings.HasSuffix(request.Host, ".ts.net") {
		scheme = "http"
	}
	origin := scheme + "://" + request.Host
	if s.config.PublicURL != "" {
		origin = strings.TrimRight(s.config.PublicURL, "/")
	}
	return origin + "/morning?code=" + url.QueryEscape(code)
}

var morningPage = template.Must(template.New("morning").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Morning</title>
<style>body{font:16px/1.5 ui-monospace,Menlo,monospace;margin:0;min-height:100vh;display:grid;place-items:center;background:#f4f1ea;color:#111}main{border:1px solid #111;padding:24px;max-width:320px}</style>
</head><body><main><p>{{.}}</p></main></body></html>`))

// scanMorning records a scan of the printed code. The code is the credential,
// so it needs no sign-in: the phone's camera opens it as is.
func (s *Server) scanMorning(writer http.ResponseWriter, request *http.Request) {
	ok, err := s.repository.ScanMorningCode(request.Context(), request.URL.Query().Get("code"))
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	message := "Morning open. Devices follow within a minute."
	switch {
	case err != nil:
		writer.WriteHeader(http.StatusServiceUnavailable)
		message = "The server could not record the scan. Try again in a moment."
	case !ok:
		writer.WriteHeader(http.StatusNotFound)
		message = "This is not the current morning code."
	default:
		if s.config.Changes != nil {
			s.config.Changes.Changed()
		}
	}
	var page bytes.Buffer
	_ = morningPage.Execute(&page, message)
	_, _ = writer.Write(page.Bytes())
}
