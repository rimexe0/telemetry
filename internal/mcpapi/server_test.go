package mcpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/model"
	"github.com/rimexe0/telemetry/internal/store"
)

func TestMCPRequiresReadCredentials(t *testing.T) {
	handler, err := New(&fakeRepository{}, "read-token")
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", "wrong-token"} {
		request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("token %q status = %d", token, response.Code)
		}
	}
}

func TestParseQueryRejectsInvalidRange(t *testing.T) {
	_, err := parseQuery(QueryObservationsInput{From: "2026-09-07T12:00:00Z", To: "2026-09-07T11:00:00Z"})
	if err == nil {
		t.Fatal("expected invalid range")
	}
}

func TestObservationOutputUsesJSONValues(t *testing.T) {
	output, err := observationOutput(store.ObservationView{
		OriginalPayload: json.RawMessage(`{"duration":60}`),
		Projection:      json.RawMessage(`{"duration":60}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := output.OriginalPayload.(map[string]any); !ok {
		t.Fatalf("original payload type = %T", output.OriginalPayload)
	}
}

func TestParseCollectionHealthBounds(t *testing.T) {
	query, err := parseCollectionHealth(CollectionHealthInput{})
	if err != nil {
		t.Fatal(err)
	}
	if query.SourceLimit != 100 || query.ResourceSamples != 24 {
		t.Fatalf("defaults = %+v", query)
	}
	for _, input := range []CollectionHealthInput{{Limit: 0, Samples: 73}, {Limit: 1001, Samples: 1}, {Limit: 1, Samples: -1}} {
		if _, err := parseCollectionHealth(input); err == nil {
			t.Fatalf("input %+v was accepted", input)
		}
	}
}

func TestCollectionHealthToolReturnsBoundedObjectPayloads(t *testing.T) {
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	repository := fakeRepository{health: store.CollectionHealth{
		CheckedAt:        now,
		Sources:          []store.SourceSummary{{SourceInstanceKey: "android:phone:collector"}},
		SourcesTruncated: true,
		Devices: []store.CollectionHealthDevice{{
			DeviceID:       "phone",
			LastReceivedAt: &now,
			ResourceHistory: []store.ObservationView{{
				DeviceID:        "phone",
				OriginalPayload: json.RawMessage(`{"delivery_state":"delivered"}`),
				Projection:      json.RawMessage(`{"delivery_state":"delivered"}`),
			}},
		}},
	}}
	_, output, err := collectionHealthTool(&repository)(context.Background(), nil, CollectionHealthInput{Limit: 4, Samples: 8})
	if err != nil {
		t.Fatal(err)
	}
	if output.SourcesTruncated != true || len(output.Devices) != 1 || output.Devices[0].DeviceID != "phone" {
		t.Fatalf("output = %+v", output)
	}
	if _, ok := output.Devices[0].ResourceHistory[0].OriginalPayload.(map[string]any); !ok {
		t.Fatalf("resource payload type = %T", output.Devices[0].ResourceHistory[0].OriginalPayload)
	}
	if repository.healthQuery.SourceLimit != 4 || repository.healthQuery.ResourceSamples != 8 {
		t.Fatalf("query = %+v", repository.healthQuery)
	}
}

func TestCollectionHealthToolReturnsStorageError(t *testing.T) {
	repository := fakeRepository{healthErr: errors.New("database unavailable")}
	_, _, err := collectionHealthTool(&repository)(context.Background(), nil, CollectionHealthInput{})
	if err == nil || err.Error() != "database unavailable" {
		t.Fatalf("error = %v", err)
	}
}

func TestCollectionHealthToolRejectsMalformedResourceJSON(t *testing.T) {
	repository := fakeRepository{health: store.CollectionHealth{
		Devices: []store.CollectionHealthDevice{{
			DeviceID: "phone",
			ResourceHistory: []store.ObservationView{{
				OriginalPayload: json.RawMessage(`{"not-json"`),
				Projection:      json.RawMessage(`{}`),
			}},
		}},
	}}
	_, _, err := collectionHealthTool(&repository)(context.Background(), nil, CollectionHealthInput{})
	if err == nil || !strings.Contains(err.Error(), "decode original payload") {
		t.Fatalf("error = %v", err)
	}
}

type fakeRepository struct {
	health      store.CollectionHealth
	healthQuery store.CollectionHealthQuery
	healthErr   error
}

func (fakeRepository) Ingest(context.Context, model.Batch) (store.Receipt, error) {
	return store.Receipt{}, nil
}
func (fakeRepository) QueryObservations(context.Context, store.Query) ([]store.ObservationView, error) {
	return nil, nil
}
func (fakeRepository) ListSources(context.Context, int) ([]store.SourceSummary, error) {
	return nil, nil
}
func (f *fakeRepository) CollectionHealth(_ context.Context, query store.CollectionHealthQuery) (store.CollectionHealth, error) {
	f.healthQuery = query
	return f.health, f.healthErr
}
func (fakeRepository) CreateActivity(context.Context, store.Activity) (store.Activity, error) {
	return store.Activity{}, store.ErrNotFound
}
func (fakeRepository) ListActivities(context.Context, store.ActivityFilter) ([]store.Activity, error) {
	return []store.Activity{}, nil
}
func (fakeRepository) UpdateActivity(context.Context, store.Activity) (store.Activity, error) {
	return store.Activity{}, store.ErrNotFound
}
func (fakeRepository) LatestPlan(context.Context, time.Time) (*store.Plan, error) {
	return nil, nil
}
func (fakeRepository) PlanBlocksByIDs(context.Context, []int64) ([]store.PlanBlock, error) {
	return nil, nil
}
func (fakeRepository) SavePlan(context.Context, time.Time, store.PlanInput) (store.Plan, error) {
	return store.Plan{}, store.ErrNotFound
}
func (fakeRepository) SetPlanBlockState(context.Context, time.Time, int64, string, string, string, string) (store.PlanBlock, error) {
	return store.PlanBlock{}, store.ErrNotFound
}
func (fakeRepository) ListPlanVersions(context.Context, time.Time) ([]store.PlanVersionSummary, error) {
	return nil, nil
}
func (fakeRepository) ListPlanBlockEvents(context.Context, time.Time, int64) ([]store.PlanBlockEvent, error) {
	return nil, nil
}
func (fakeRepository) GetPlanningSettings(context.Context) (store.PlanningSettings, error) {
	return store.PlanningSettings{}, nil
}
func (fakeRepository) UpdatePlanningSettings(context.Context, bool) (store.PlanningSettings, error) {
	return store.PlanningSettings{}, nil
}
func (fakeRepository) StartExecutionSession(context.Context, store.ExecutionSessionInput) (store.ExecutionSession, error) {
	return store.ExecutionSession{}, store.ErrNotFound
}
func (fakeRepository) TransitionExecutionSession(context.Context, int64, store.ExecutionEventInput) (store.ExecutionSession, error) {
	return store.ExecutionSession{}, store.ErrNotFound
}
func (fakeRepository) ReturnToPrimary(context.Context, int64, store.ReturnToPrimaryInput) (store.ExecutionReturn, error) {
	return store.ExecutionReturn{}, store.ErrNotFound
}
func (fakeRepository) CurrentExecutionSession(context.Context) (*store.ExecutionSession, error) {
	return nil, nil
}
func (fakeRepository) CurrentPrimaryExecutionSession(context.Context) (*store.ExecutionSession, error) {
	return nil, nil
}
func (fakeRepository) CurrentAttendedExecutionSession(context.Context) (*store.ExecutionSession, error) {
	return nil, nil
}
func (fakeRepository) GetExecutionSession(context.Context, int64) (*store.ExecutionSession, error) {
	return nil, store.ErrNotFound
}
func (fakeRepository) ListExecutionSessions(context.Context, int) ([]store.ExecutionSession, error) {
	return []store.ExecutionSession{}, nil
}
func (fakeRepository) ListExecutionInterleaves(context.Context, int64, int) ([]store.ExecutionSession, error) {
	return []store.ExecutionSession{}, nil
}
func (fakeRepository) Ping(context.Context) error { return nil }

func (fakeRepository) CreatePlanChangeProposal(context.Context, time.Time, store.PlanChangeRequest) (store.PlanChangeProposal, error) {
	return store.PlanChangeProposal{}, nil
}

func (fakeRepository) GetPlanChangeProposal(context.Context, time.Time, int64) (store.PlanChangeProposal, error) {
	return store.PlanChangeProposal{}, store.ErrNotFound
}

func (fakeRepository) ListPlanChangeProposals(context.Context, time.Time, int) ([]store.PlanChangeProposal, error) {
	return nil, nil
}

func (fakeRepository) AcceptPlanChangeProposal(context.Context, time.Time, int64, store.ProposalDecision, json.RawMessage) (store.PlanChangeProposal, error) {
	return store.PlanChangeProposal{}, nil
}

func (fakeRepository) RejectPlanChangeProposal(context.Context, time.Time, int64, store.ProposalDecision, json.RawMessage) (store.PlanChangeProposal, error) {
	return store.PlanChangeProposal{}, nil
}
