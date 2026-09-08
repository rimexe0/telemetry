package mcpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rimexe0/telemetry/internal/model"
	"github.com/rimexe0/telemetry/internal/store"
)

func TestMCPRequiresReadCredentials(t *testing.T) {
	handler, err := New(fakeRepository{}, "read-token")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.Code)
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

type fakeRepository struct{}

func (fakeRepository) Ingest(context.Context, model.Batch) (store.Receipt, error) {
	return store.Receipt{}, nil
}
func (fakeRepository) QueryObservations(context.Context, store.Query) ([]store.ObservationView, error) {
	return nil, nil
}
func (fakeRepository) ListSources(context.Context, int) ([]store.SourceSummary, error) {
	return nil, nil
}
func (fakeRepository) Ping(context.Context) error { return nil }
