package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/model"
	"github.com/rimexe0/telemetry/internal/store"
)

func TestIngestRequiresCredentialsAndNormalizesBatch(t *testing.T) {
	repository := &fakeRepository{}
	server := testServer(t, repository)
	body := `{"id":"batch-1","device_id":"mac","collector_version":"test","observations":[{"source_instance_key":"activitywatch:mac:window","source_kind":"activitywatch","device_id":"mac","source_record_key":"1","kind":"activitywatch.event","occurred_at":"2026-09-07T10:00:00Z","collected_at":"2026-09-07T10:01:00Z","original_payload":{"duration":1}}]}`
	unauthorized := httptest.NewRequest(http.MethodPost, "/v1/ingest/batches", strings.NewReader(body))
	unauthorizedResponse := httptest.NewRecorder()
	server.ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorizedResponse.Code)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/ingest/batches", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer ingest-secret")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("ingest status = %d: %s", response.Code, response.Body.String())
	}
	if len(repository.ingested.Observations) != 1 || repository.ingested.Observations[0].RevisionDigest == "" {
		t.Fatalf("repository received unnormalized batch: %+v", repository.ingested)
	}
}

func TestIngestRejectsTrailingJSON(t *testing.T) {
	repository := &fakeRepository{}
	server := testServer(t, repository)
	body := `{"id":"batch-1","device_id":"mac","collector_version":"test","observations":[{"source_instance_key":"activitywatch:mac:window","source_kind":"activitywatch","device_id":"mac","source_record_key":"1","kind":"activitywatch.event","occurred_at":"2026-09-07T10:00:00Z","collected_at":"2026-09-07T10:01:00Z","original_payload":{"duration":1}}]}`
	request := httptest.NewRequest(http.MethodPost, "/v1/ingest/batches", strings.NewReader(body+`{}`))
	request.Header.Set("Authorization", "Bearer ingest-secret")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("trailing JSON status = %d: %s", response.Code, response.Body.String())
	}
}

func TestIngestAcceptsMatchingDeviceScopedCredential(t *testing.T) {
	repository := &fakeRepository{}
	server, err := New(repository, Config{
		IngestToken:        "legacy-ingest-secret",
		IngestDeviceTokens: map[string]string{"android-s19": "android-secret"},
		ReadToken:          "read-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"id":"android-batch","device_id":"android-s19","collector_version":"test","observations":[{"source_instance_key":"healthconnect:android-s19","source_kind":"health_connect","device_id":"android-s19","source_record_key":"steps:1","kind":"health_connect.steps","occurred_at":"2026-09-07T10:00:00Z","collected_at":"2026-09-07T10:01:00Z","original_payload":{"count":1}}]}`
	request := httptest.NewRequest(http.MethodPost, "/v1/ingest/batches", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer android-secret")
	request.Header.Set("X-Telemetry-Device-ID", "android-s19")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("scoped ingest status = %d: %s", response.Code, response.Body.String())
	}
}

func TestIngestRejectsDeviceScopedCredentialForAnotherDevice(t *testing.T) {
	repository := &fakeRepository{}
	server, err := New(repository, Config{
		IngestToken:        "legacy-ingest-secret",
		IngestDeviceTokens: map[string]string{"android-s19": "android-secret"},
		ReadToken:          "read-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"id":"android-batch","device_id":"mac","collector_version":"test","observations":[{"source_instance_key":"healthconnect:mac","source_kind":"health_connect","device_id":"mac","source_record_key":"steps:1","kind":"health_connect.steps","occurred_at":"2026-09-07T10:00:00Z","collected_at":"2026-09-07T10:01:00Z","original_payload":{"count":1}}]}`
	request := httptest.NewRequest(http.MethodPost, "/v1/ingest/batches", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer android-secret")
	request.Header.Set("X-Telemetry-Device-ID", "android-s19")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("mismatched device status = %d: %s", response.Code, response.Body.String())
	}
}

func TestObservationsUsesBoundedQuery(t *testing.T) {
	repository := &fakeRepository{observations: []store.ObservationView{{SourceRecordKey: "one"}}}
	server := testServer(t, repository)
	request := httptest.NewRequest(http.MethodGet, "/v1/observations?source_instance_key=watch&limit=2&from=2026-09-07T10:00:00Z", nil)
	request.Header.Set("Authorization", "Bearer read-secret")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("observations status = %d: %s", response.Code, response.Body.String())
	}
	if repository.query.SourceInstanceKey != "watch" || repository.query.Limit != 2 || repository.query.From == nil {
		t.Fatalf("query = %+v", repository.query)
	}
}

func TestPanelSessionCanReadButCannotUseTheBearerToken(t *testing.T) {
	repository := &fakeRepository{
		observations: []store.ObservationView{{SourceRecordKey: "one"}},
		sources:      []store.SourceSummary{{SourceInstanceKey: "activitywatch:mac:codex", ObservationCount: 1}},
	}
	server, err := New(repository, Config{
		IngestToken:        "ingest-secret",
		ReadToken:          "read-secret",
		PanelToken:         "panel-secret",
		PanelSessionSecret: "session-secret",
		PanelSessionTTL:    time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}

	login := httptest.NewRequest(http.MethodPost, "/v1/panel/session", strings.NewReader(`{"token":"panel-secret"}`))
	loginResponse := httptest.NewRecorder()
	server.ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("login status = %d: %s", loginResponse.Code, loginResponse.Body.String())
	}
	cookies := loginResponse.Result().Cookies()
	if len(cookies) != 1 || cookies[0].HttpOnly == false || cookies[0].Value == "panel-secret" {
		t.Fatalf("unexpected session cookie: %+v", cookies)
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/sources", nil)
	request.AddCookie(cookies[0])
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("session source read status = %d: %s", response.Code, response.Body.String())
	}

	logout := httptest.NewRequest(http.MethodDelete, "/v1/panel/session", nil)
	logoutResponse := httptest.NewRecorder()
	server.ServeHTTP(logoutResponse, logout)
	if logoutResponse.Code != http.StatusOK {
		t.Fatalf("logout status = %d: %s", logoutResponse.Code, logoutResponse.Body.String())
	}
	afterLogout := httptest.NewRequest(http.MethodGet, "/v1/sources", nil)
	response = httptest.NewRecorder()
	server.ServeHTTP(response, afterLogout)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("logged out source read status = %d: %s", response.Code, response.Body.String())
	}
}

func TestPanelSessionRejectsBadCredentialsAndDisabledSessions(t *testing.T) {
	server := testServer(t, &fakeRepository{})
	disabledRequest := httptest.NewRequest(http.MethodPost, "/v1/panel/session", strings.NewReader(`{"token":"panel-secret"}`))
	disabledResponse := httptest.NewRecorder()
	server.ServeHTTP(disabledResponse, disabledRequest)
	if disabledResponse.Code != http.StatusNotFound {
		t.Fatalf("disabled session status = %d: %s", disabledResponse.Code, disabledResponse.Body.String())
	}

	enabled, err := New(&fakeRepository{}, Config{IngestToken: "ingest-secret", ReadToken: "read-secret", PanelToken: "panel-secret", PanelSessionSecret: "session-secret"})
	if err != nil {
		t.Fatal(err)
	}
	badRequest := httptest.NewRequest(http.MethodPost, "/v1/panel/session", strings.NewReader(`{"token":"wrong"}`))
	badResponse := httptest.NewRecorder()
	enabled.ServeHTTP(badResponse, badRequest)
	if badResponse.Code != http.StatusUnauthorized {
		t.Fatalf("bad credential status = %d: %s", badResponse.Code, badResponse.Body.String())
	}
}

func testServer(t *testing.T, repository *fakeRepository) *Server {
	t.Helper()
	server, err := New(repository, Config{IngestToken: "ingest-secret", ReadToken: "read-secret"})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

type fakeRepository struct {
	ingested     model.Batch
	query        store.Query
	observations []store.ObservationView
	sources      []store.SourceSummary
}

func (f *fakeRepository) Ingest(_ context.Context, batch model.Batch) (store.Receipt, error) {
	f.ingested = batch
	return store.Receipt{BatchID: batch.ID, ObservationsSeen: len(batch.Observations)}, nil
}

func (f *fakeRepository) QueryObservations(_ context.Context, query store.Query) ([]store.ObservationView, error) {
	f.query = query
	return f.observations, nil
}

func (f *fakeRepository) ListSources(context.Context, int) ([]store.SourceSummary, error) {
	return f.sources, nil
}
func (f *fakeRepository) Ping(context.Context) error { return nil }

func TestBatchJSONFixtureRemainsValid(t *testing.T) {
	var batch model.Batch
	if err := json.Unmarshal([]byte(`{"id":"b","device_id":"d","observations":[]}`), &batch); err != nil {
		t.Fatal(err)
	}
	if batch.ID != "b" || batch.DeviceID != "d" {
		t.Fatal("fixture was not decoded")
	}
	_ = time.Second
}
