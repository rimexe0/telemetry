package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/rimexe0/telemetry/internal/policy"
)

// The decision modes of the distraction budget (budget-and-habits.md): a
// door the owner opened, and a distraction time whose budget is used up.
const (
	modeDoor       = "door"
	modeBudgetUsed = "budget_used"
)

// budgetFresh is how old the last answer may be for the door to be offered:
// without the server there is no door.
const budgetFresh = 3 * time.Minute

// budgetState is the part of /v1/device/now's budget field the collector
// itself acts on. The extension gets the field as the server sent it.
type budgetState struct {
	Enabled           bool       `json:"enabled"`
	BedtimeAt         *time.Time `json:"bedtime_at"`
	WindDown          bool       `json:"wind_down"`
	GrayscaleDeclined bool       `json:"grayscale_declined"`
	Rate              struct {
		Total        float64 `json:"total"`
		GrayscaleOff float64 `json:"grayscale_off"`
	} `json:"rate"`
	Settings struct {
		// RateGrayscaleOff is not in the contract's settings; it is read
		// when the server sends it.
		RateGrayscaleOff *float64 `json:"rate_grayscale_off"`
	} `json:"settings"`
}

// grayscaleSurcharge is what keeping grayscale off adds to the rate, when
// the answer says: the setting when sent, otherwise the surcharge in force.
func (s budgetState) grayscaleSurcharge() (float64, bool) {
	if s.Settings.RateGrayscaleOff != nil && *s.Settings.RateGrayscaleOff > 0 {
		return *s.Settings.RateGrayscaleOff, true
	}
	if s.Rate.GrayscaleOff > 0 {
		return s.Rate.GrayscaleOff, true
	}
	return 0, false
}

// BudgetStatus is what the extension's door screen and counter read.
type BudgetStatus struct {
	// Connected means the answer is current enough to offer the door.
	Connected bool       `json:"connected"`
	FetchedAt *time.Time `json:"fetched_at,omitempty"`
	// Budget is /v1/device/now's budget field as the server sent it, null
	// without one.
	Budget json.RawMessage `json:"budget"`
	// BedtimeUntil is the end of the bedtime in force, from the timeline.
	BedtimeUntil *time.Time `json:"bedtime_until,omitempty"`
	Now          time.Time  `json:"now"`
}

// loadBudget takes the budget field of an answer; the caller holds b.mu.
func (b *Blocker) loadBudget(raw json.RawMessage) {
	if len(raw) == 0 || string(raw) == "null" {
		b.budgetRaw, b.budget = nil, budgetState{}
		return
	}
	var state budgetState
	if json.Unmarshal(raw, &state) != nil {
		return
	}
	b.budgetRaw, b.budget = append(json.RawMessage(nil), raw...), state
}

// distractionDomains are the sites the timeline closes outside distraction
// times. Every blocking mode closes the owner's distractions, so together
// they name them, including while a door or distraction time has them open.
// A timeline that closes nothing keeps the last known set. Called with b.mu
// held.
func (b *Blocker) learnDistractions(timeline []policy.Segment) {
	seen := map[string]bool{}
	domains := []string{}
	for _, segment := range timeline {
		for _, domain := range segment.Decision.Blocked.Domains {
			domain = strings.TrimPrefix(strings.ToLower(domain), "www.")
			if domain != "" && !seen[domain] {
				seen[domain] = true
				domains = append(domains, domain)
			}
		}
	}
	if len(domains) > 0 {
		b.distractions = domains
	}
}

// Budget describes the distraction budget for the extension.
func (b *Blocker) Budget() BudgetStatus {
	now := time.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	status := BudgetStatus{Budget: json.RawMessage("null"), Now: now.UTC()}
	if b.budgetRaw != nil {
		status.Budget = b.budgetRaw
	}
	if !b.fetchedAt.IsZero() {
		fetched := b.fetchedAt.UTC()
		status.FetchedAt = &fetched
		status.Connected = now.Sub(b.fetchedAt) < budgetFresh
	}
	if until, ok := bedtimeUntil(b.timeline, now); ok {
		until = until.UTC()
		status.BedtimeUntil = &until
	}
	return status
}

// bedtimeUntil finds the end of the bedtime in force at now: the end of the
// run of bedtime segments that holds now.
func bedtimeUntil(timeline []policy.Segment, now time.Time) (time.Time, bool) {
	var end time.Time
	in := false
	for _, segment := range timeline {
		bedtime := segment.Decision.Mode == policy.ModeBedtime
		if in {
			if !bedtime || segment.From.After(end) {
				return end, true
			}
			end = segment.To
			continue
		}
		if bedtime && !now.Before(segment.From) && now.Before(segment.To) {
			in, end = true, segment.To
		}
	}
	return end, in
}

// devicePaths are the server calls the extension may make through the
// collector: the door, Back, cancelling a waiting door and habit ticks.
var devicePaths = regexp.MustCompile(`^(door|door/back|door/[0-9]+/cancel|habits/[0-9]+/(tick|untick))$`)

// DeviceCall makes one of the extension's budget calls to the server with
// this computer's credentials, so the extension never holds them. It
// returns the server's status and body; a call that changed something
// fetches the new answer first, so the page reads the new state at once.
func (b *Blocker) DeviceCall(ctx context.Context, path string, body []byte) (int, []byte) {
	if !devicePaths.MatchString(path) {
		return http.StatusNotFound, errorBody("not_found", "Not a call the extension can make.")
	}
	if b.Credentials == nil {
		return http.StatusServiceUnavailable, errorBody("not_paired", "This computer is not connected to Telemetry, so there is no door here.")
	}
	if len(bytes.TrimSpace(body)) == 0 {
		body = []byte("{}")
	}
	if !json.Valid(body) {
		return http.StatusBadRequest, errorBody("invalid_body", "The request body must be JSON.")
	}
	status, reply, err := b.deviceRequest(ctx, http.MethodPost, "/v1/device/"+path, body)
	if err != nil {
		return http.StatusBadGateway, errorBody("unreachable", "The Telemetry server can't be reached right now, so this can't be done.")
	}
	if status >= 200 && status < 300 {
		// A tick answers with the new budget.
		if strings.HasPrefix(path, "habits/") {
			b.mu.Lock()
			b.loadBudget(reply)
			b.mu.Unlock()
		}
		b.mu.Lock()
		host := b.host
		b.mu.Unlock()
		if host != nil {
			b.refresh(ctx, *host)
		}
	}
	return status, reply
}

// deviceRequest makes one request to the server as this device.
func (b *Blocker) deviceRequest(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	if b.Credentials == nil {
		return 0, nil, errors.New("this computer is not paired")
	}
	client := b.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(b.Credentials.BaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Authorization", "Bearer "+b.Credentials.Secret)
	request.Header.Set("X-Telemetry-Device-ID", b.Credentials.DeviceID)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	reply, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return 0, nil, err
	}
	return response.StatusCode, reply, nil
}

func errorBody(code, message string) []byte {
	body, _ := json.Marshal(map[string]string{"error": code, "message": message})
	return body
}

// closedTitle is the block notice's first line for an app the decision
// closes.
func closedTitle(app string, decision policy.Decision) string {
	switch decision.Mode {
	case modeBudgetUsed:
		return app + " is closed: today's distraction time is used up"
	case modeDoor:
		return app + " is closed"
	}
	return fmt.Sprintf("%s is closed during %s", app, strings.ToLower(ModeLabel(decision.Mode)))
}
