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

func TestCollectionHealthRequiresReadCredentials(t *testing.T) {
	repository := &fakeRepository{}
	server := testServer(t, repository)
	request := httptest.NewRequest(http.MethodGet, "/v1/collection/health", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("collection health unauthorized status = %d: %s", response.Code, response.Body.String())
	}
	if repository.healthCalled {
		t.Fatal("unauthorized request reached repository")
	}
}

func TestCollectionHealthUsesBoundedQuery(t *testing.T) {
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	repository := &fakeRepository{health: store.CollectionHealth{
		CheckedAt: now,
		Sources:   []store.SourceSummary{{SourceInstanceKey: "android:phone:collector"}},
		Devices:   []store.CollectionHealthDevice{{DeviceID: "phone", ResourceHistory: []store.ObservationView{}}},
	}}
	server := testServer(t, repository)
	request := httptest.NewRequest(http.MethodGet, "/v1/collection/health?limit=7&samples=12", nil)
	request.Header.Set("Authorization", "Bearer read-secret")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("collection health status = %d: %s", response.Code, response.Body.String())
	}
	if repository.healthQuery.SourceLimit != 7 || repository.healthQuery.ResourceSamples != 12 {
		t.Fatalf("collection health query = %+v", repository.healthQuery)
	}
	var output store.CollectionHealth
	if err := json.Unmarshal(response.Body.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if !output.CheckedAt.Equal(now) || len(output.Devices) != 1 {
		t.Fatalf("collection health output = %+v", output)
	}
}

func TestCollectionHealthRejectsInvalidSamples(t *testing.T) {
	repository := &fakeRepository{}
	server := testServer(t, repository)
	request := httptest.NewRequest(http.MethodGet, "/v1/collection/health?samples=73", nil)
	request.Header.Set("Authorization", "Bearer read-secret")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid samples status = %d: %s", response.Code, response.Body.String())
	}
	if repository.healthCalled {
		t.Fatal("invalid request reached repository")
	}
}

func TestCollectionHealthReturnsStorageError(t *testing.T) {
	repository := &fakeRepository{healthErr: context.DeadlineExceeded}
	server := testServer(t, repository)
	request := httptest.NewRequest(http.MethodGet, "/v1/collection/health", nil)
	request.Header.Set("Authorization", "Bearer read-secret")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("storage error status = %d: %s", response.Code, response.Body.String())
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
	health       store.CollectionHealth
	healthQuery  store.CollectionHealthQuery
	healthErr    error
	healthCalled bool

	activities     []store.Activity
	activityInput  store.Activity
	activityErr    error
	plan           *store.Plan
	planBlocks     []store.PlanBlock
	planInput      store.PlanInput
	planDate       time.Time
	planErr        error
	blockState     store.PlanBlock
	blockID        int64
	blockPlanDate  time.Time
	blockStateName string
	blockErr       error
	versions       []store.PlanVersionSummary
	settings       store.PlanningSettings
	settingsInput  bool

	executionSession         *store.ExecutionSession
	executionCurrent         *store.ExecutionSession
	executionPrimary         *store.ExecutionSession
	executionAttended        *store.ExecutionSession
	executionSessions        []store.ExecutionSession
	executionInterleaves     []store.ExecutionSession
	executionReturn          store.ExecutionReturn
	executionStartInput      store.ExecutionSessionInput
	executionEventInput      store.ExecutionEventInput
	executionReturnInput     store.ReturnToPrimaryInput
	executionEventSession    int64
	executionLimit           int
	executionInterleaveLimit int
	executionErr             error

	proposals          map[int64]*store.PlanChangeProposal
	proposalRequest    store.PlanChangeRequest
	proposalProvenance json.RawMessage
	proposalSaves      int

	contextRules []store.ActivityContextRule
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
func (f *fakeRepository) CollectionHealth(_ context.Context, query store.CollectionHealthQuery) (store.CollectionHealth, error) {
	f.healthCalled = true
	f.healthQuery = query
	return f.health, f.healthErr
}
func (f *fakeRepository) CreateActivity(_ context.Context, activity store.Activity) (store.Activity, error) {
	if f.activityErr != nil {
		return store.Activity{}, f.activityErr
	}
	activity.ID = 1
	f.activityInput = activity
	return activity, nil
}
func (f *fakeRepository) ListActivities(_ context.Context, filter store.ActivityFilter) ([]store.Activity, error) {
	return f.activities, nil
}
func (f *fakeRepository) UpdateActivity(_ context.Context, activity store.Activity) (store.Activity, error) {
	if f.activityErr != nil {
		return store.Activity{}, f.activityErr
	}
	f.activityInput = activity
	return activity, nil
}
func (f *fakeRepository) LatestPlan(_ context.Context, planDate time.Time) (*store.Plan, error) {
	f.planDate = planDate
	return f.plan, nil
}
func (f *fakeRepository) PlanBlocksByIDs(_ context.Context, ids []int64) ([]store.PlanBlock, error) {
	wanted := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		wanted[id] = struct{}{}
	}
	blocks := make([]store.PlanBlock, 0, len(f.planBlocks))
	for _, block := range f.planBlocks {
		if _, ok := wanted[block.ID]; ok {
			blocks = append(blocks, block)
		}
	}
	return blocks, nil
}
func (f *fakeRepository) ListPlanVersions(_ context.Context, planDate time.Time) ([]store.PlanVersionSummary, error) {
	f.planDate = planDate
	return f.versions, nil
}
func (f *fakeRepository) SavePlan(_ context.Context, planDate time.Time, input store.PlanInput) (store.Plan, error) {
	if f.planErr != nil {
		return store.Plan{}, f.planErr
	}
	f.planDate = planDate
	f.planInput = input
	blocks := make([]store.PlanBlock, 0, len(input.Blocks))
	for i, spec := range input.Blocks {
		blocks = append(blocks, store.PlanBlock{ID: int64(i + 1), ActivityID: spec.ActivityID, Title: spec.Title, StartTime: spec.StartTime, DurationMinutes: spec.DurationMinutes, Fixed: spec.Fixed, State: "planned", Position: i})
	}
	return store.Plan{PlanDate: planDate.Format("2006-01-02"), VersionID: 1, Blocks: blocks}, nil
}
func (f *fakeRepository) SetPlanBlockState(_ context.Context, planDate time.Time, blockID int64, state, _, _, _ string) (store.PlanBlock, error) {
	if f.blockErr != nil {
		return store.PlanBlock{}, f.blockErr
	}
	f.blockID = blockID
	f.blockPlanDate = planDate
	f.blockStateName = state
	return f.blockState, nil
}
func (f *fakeRepository) ListPlanBlockEvents(context.Context, time.Time, int64) ([]store.PlanBlockEvent, error) {
	return []store.PlanBlockEvent{}, nil
}
func (f *fakeRepository) GetPlanningSettings(context.Context) (store.PlanningSettings, error) {
	return f.settings, nil
}
func (f *fakeRepository) UpdatePlanningSettings(_ context.Context, enabled bool) (store.PlanningSettings, error) {
	f.settingsInput = enabled
	f.settings.DailyPlanGateEnabled = enabled
	return f.settings, nil
}
func (f *fakeRepository) StartExecutionSession(_ context.Context, input store.ExecutionSessionInput) (store.ExecutionSession, error) {
	if f.executionErr != nil {
		return store.ExecutionSession{}, f.executionErr
	}
	f.executionStartInput = input
	if f.executionSession != nil {
		return *f.executionSession, nil
	}
	return store.ExecutionSession{ID: 1, ActivityID: input.ActivityID, PlanBlockID: input.PlanBlockID, Role: input.Role, PrimarySessionID: input.PrimarySessionID, State: "active", Events: []store.ExecutionEvent{{ID: 1, SessionID: 1, EventType: "started"}}}, nil
}
func (f *fakeRepository) TransitionExecutionSession(_ context.Context, sessionID int64, input store.ExecutionEventInput) (store.ExecutionSession, error) {
	if f.executionErr != nil {
		return store.ExecutionSession{}, f.executionErr
	}
	f.executionEventSession = sessionID
	f.executionEventInput = input
	if f.executionSession != nil {
		return *f.executionSession, nil
	}
	return store.ExecutionSession{ID: sessionID, State: "active", Events: []store.ExecutionEvent{{ID: 1, SessionID: sessionID, EventType: input.EventType, Note: input.Note}}}, nil
}
func (f *fakeRepository) CurrentExecutionSession(context.Context) (*store.ExecutionSession, error) {
	if f.executionErr != nil {
		return nil, f.executionErr
	}
	return f.executionCurrent, nil
}
func (f *fakeRepository) CurrentPrimaryExecutionSession(context.Context) (*store.ExecutionSession, error) {
	if f.executionErr != nil {
		return nil, f.executionErr
	}
	return f.executionPrimary, nil
}
func (f *fakeRepository) CurrentAttendedExecutionSession(context.Context) (*store.ExecutionSession, error) {
	if f.executionErr != nil {
		return nil, f.executionErr
	}
	return f.executionAttended, nil
}
func (f *fakeRepository) GetExecutionSession(_ context.Context, sessionID int64) (*store.ExecutionSession, error) {
	if f.executionErr != nil {
		return nil, f.executionErr
	}
	if f.executionSession == nil || f.executionSession.ID != sessionID {
		return nil, store.ErrNotFound
	}
	return f.executionSession, nil
}
func (f *fakeRepository) ListExecutionSessions(_ context.Context, limit int) ([]store.ExecutionSession, error) {
	if f.executionErr != nil {
		return nil, f.executionErr
	}
	f.executionLimit = limit
	return f.executionSessions, nil
}
func (f *fakeRepository) ListExecutionInterleaves(_ context.Context, _ int64, limit int) ([]store.ExecutionSession, error) {
	if f.executionErr != nil {
		return nil, f.executionErr
	}
	f.executionInterleaveLimit = limit
	return f.executionInterleaves, nil
}
func (f *fakeRepository) ReturnToPrimary(_ context.Context, _ int64, input store.ReturnToPrimaryInput) (store.ExecutionReturn, error) {
	if f.executionErr != nil {
		return store.ExecutionReturn{}, f.executionErr
	}
	f.executionReturnInput = input
	return f.executionReturn, nil
}
func (f *fakeRepository) CreatePlanChangeProposal(_ context.Context, planDate time.Time, request store.PlanChangeRequest) (store.PlanChangeProposal, error) {
	f.proposalRequest = request
	if f.plan == nil {
		return store.PlanChangeProposal{}, store.ErrNotFound
	}
	origin, plan, changes, err := store.PreparePlanChange(*f.plan, request)
	if err != nil {
		return store.PlanChangeProposal{}, err
	}
	hash, _ := store.ProposalContentHash(f.plan.PlanDate, f.plan.VersionID, plan)
	if f.proposals == nil {
		f.proposals = map[int64]*store.PlanChangeProposal{}
	}
	proposal := &store.PlanChangeProposal{ID: int64(len(f.proposals) + 1), PlanDate: planDate.Format("2006-01-02"), BasePlanVersionID: f.plan.VersionID,
		Origin: origin, Reason: request.Reason, ProposedPlan: plan, Changes: changes, Warnings: []store.ProposalWarning{}, ContentHash: hash, Status: store.ProposalPending}
	f.proposals[proposal.ID] = proposal
	return *proposal, nil
}

func (f *fakeRepository) GetPlanChangeProposal(_ context.Context, _ time.Time, id int64) (store.PlanChangeProposal, error) {
	proposal, ok := f.proposals[id]
	if !ok {
		return store.PlanChangeProposal{}, store.ErrNotFound
	}
	return *proposal, nil
}

func (f *fakeRepository) ListPlanChangeProposals(context.Context, time.Time, int) ([]store.PlanChangeProposal, error) {
	proposals := []store.PlanChangeProposal{}
	for id := int64(len(f.proposals)); id > 0; id-- {
		proposals = append(proposals, *f.proposals[id])
	}
	return proposals, nil
}

// The fake mirrors the store's documented decision rules; PostgreSQL
// integration tests prove the real transaction.
func (f *fakeRepository) AcceptPlanChangeProposal(_ context.Context, _ time.Time, id int64, decision store.ProposalDecision, provenance json.RawMessage) (store.PlanChangeProposal, error) {
	proposal, ok := f.proposals[id]
	if !ok {
		return store.PlanChangeProposal{}, store.ErrNotFound
	}
	if decision.ContentHash != proposal.ContentHash {
		return *proposal, store.ErrProposalConflict
	}
	switch proposal.Status {
	case store.ProposalAccepted:
		return *proposal, nil
	case store.ProposalRejected:
		return *proposal, store.ErrProposalConflict
	case store.ProposalStale:
		return *proposal, store.ErrProposalStale
	}
	if f.plan.VersionID != proposal.BasePlanVersionID {
		proposal.Status = store.ProposalStale
		return *proposal, store.ErrProposalStale
	}
	f.proposalSaves++
	f.proposalProvenance = provenance
	version := f.plan.VersionID + 1
	f.plan = &store.Plan{PlanDate: f.plan.PlanDate, VersionID: version}
	proposal.Status, proposal.ResultPlanVersionID = store.ProposalAccepted, &version
	return *proposal, nil
}

func (f *fakeRepository) RejectPlanChangeProposal(_ context.Context, _ time.Time, id int64, _ store.ProposalDecision, provenance json.RawMessage) (store.PlanChangeProposal, error) {
	proposal, ok := f.proposals[id]
	if !ok {
		return store.PlanChangeProposal{}, store.ErrNotFound
	}
	if proposal.Status == store.ProposalAccepted || proposal.Status == store.ProposalStale {
		return *proposal, store.ErrProposalConflict
	}
	proposal.Status = store.ProposalRejected
	f.proposalProvenance = provenance
	return *proposal, nil
}

func (f *fakeRepository) CreateActivityContextRule(_ context.Context, rule store.ActivityContextRule) (store.ActivityContextRule, error) {
	if rule.ActivityID == 404 {
		return store.ActivityContextRule{}, store.ErrNotFound
	}
	if rule.Signal == "app_name" {
		return store.ActivityContextRule{}, store.ErrInvalidContextRule
	}
	rule.ID = int64(len(f.contextRules) + 1)
	f.contextRules = append(f.contextRules, rule)
	return rule, nil
}

func (f *fakeRepository) ListActivityContextRules(_ context.Context, includeDisabled bool) ([]store.ActivityContextRule, error) {
	rules := []store.ActivityContextRule{}
	for _, rule := range f.contextRules {
		if includeDisabled || rule.DisabledAt == nil {
			rules = append(rules, rule)
		}
	}
	return rules, nil
}

func (f *fakeRepository) DisableActivityContextRule(_ context.Context, id int64) (store.ActivityContextRule, error) {
	if id < 1 || id > int64(len(f.contextRules)) {
		return store.ActivityContextRule{}, store.ErrNotFound
	}
	now := time.Now()
	f.contextRules[id-1].DisabledAt = &now
	return f.contextRules[id-1], nil
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
