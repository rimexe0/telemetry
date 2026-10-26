package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/policy"
	"github.com/rimexe0/telemetry/internal/store"
)

func TestOpeningFromThePanelOpensTheChosenGroups(t *testing.T) {
	repository := &fakeRepository{policies: []store.PolicyVersion{{ID: 1, Config: policy.Config{Timezone: "Europe/Istanbul",
		Distractions: []policy.TargetGroup{{ID: "x", Label: "X", AndroidPackages: []string{"com.twitter.android"}, Domains: []string{"x.com"}}}}}}}
	server := ownerTestServer(t, repository)

	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/openings", strings.NewReader(`{"purpose":"reply to a message","group_ids":["x"],"minutes":20}`)))
	if response.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	if len(repository.openings) != 1 || repository.openings[0].Sites[0] != "x.com" || repository.openings[0].Apps[0] != "com.twitter.android" {
		t.Fatalf("opening = %+v", repository.openings)
	}

	response = httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/openings", nil))
	var state openingsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil || len(state.Openings) != 1 || state.UsedToday != 1 || len(state.Distractions) != 1 {
		t.Fatalf("state %d %v: %s", response.Code, err, response.Body.String())
	}

	response = httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/openings/1/end", nil))
	if response.Code != http.StatusOK || repository.openings[0].EndedAt == nil {
		t.Fatalf("end %d: %s", response.Code, response.Body.String())
	}
}

func TestMorningCodeIsMadeOnlyWhileOpenAndItsScanOpensTheMorning(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Istanbul")
	if time.Now().In(location).Format("15:04") >= "23:58" {
		t.Skip("the test's morning hold ends at 23:59")
	}
	morning := policy.MorningRule{Enabled: true, From: "00:00", Until: "23:59", LockAll: true}
	config := policy.Config{Timezone: "Europe/Istanbul", Morning: morning}
	repository := &fakeRepository{policies: []store.PolicyVersion{{ID: 1, Config: config}}}
	server := ownerTestServer(t, repository)
	call := func(method, target string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(method, target, nil))
		return response
	}

	if response := call(http.MethodPost, "/v1/morning/code"); response.Code != http.StatusConflict {
		t.Fatalf("a code was made while the morning held: %d %s", response.Code, response.Body.String())
	}
	config.Morning.Enabled = false
	repository.policies = append(repository.policies, store.PolicyVersion{ID: 2, Config: config})
	response := call(http.MethodPost, "/v1/morning/code")
	var made struct{ Address, Image string }
	if err := json.Unmarshal(response.Body.Bytes(), &made); err != nil || response.Code != http.StatusCreated ||
		!strings.HasSuffix(made.Address, "/morning?code=code-1") || !strings.HasPrefix(made.Image, "data:image/png;base64,") {
		t.Fatalf("code %d: %s", response.Code, response.Body.String())
	}

	config.Morning.Enabled = true
	repository.policies = append(repository.policies, store.PolicyVersion{ID: 3, Config: config})
	var status morningResponse
	if err := json.Unmarshal(call(http.MethodGet, "/v1/morning").Body.Bytes(), &status); err != nil || !status.Holding || status.CodeMade == nil {
		t.Fatalf("status before the scan: %+v %v", status, err)
	}
	if response := call(http.MethodGet, "/morning?code=wrong"); response.Code != http.StatusNotFound {
		t.Fatalf("a wrong code: %d", response.Code)
	}
	if response := call(http.MethodGet, "/morning?code=code-1"); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Morning open") {
		t.Fatalf("scan: %d %s", response.Code, response.Body.String())
	}
	status = morningResponse{}
	if err := json.Unmarshal(call(http.MethodGet, "/v1/morning").Body.Bytes(), &status); err != nil || status.Holding || status.ScannedAt == nil {
		t.Fatalf("status after the scan: %+v %v", status, err)
	}
}
