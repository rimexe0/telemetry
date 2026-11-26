package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/activity"
)

type usageRepository struct {
	*fakeRepository
	records []activity.Record
}

func (r *usageRepository) ActivityRecords(context.Context, time.Time, time.Time) ([]activity.Record, map[string]time.Time, error) {
	return r.records, nil, nil
}

func TestActivitySummaryNamesPhoneAppsAndKeepsThePackage(t *testing.T) {
	at := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	repository := &usageRepository{fakeRepository: &fakeRepository{appNames: activity.AppNames{"com.whatsapp": "WhatsApp"}}, records: []activity.Record{
		{Source: "android:p:usage", Kind: "android.usage.activity_resumed", Start: at, Payload: json.RawMessage(`{"package_name":"com.whatsapp"}`)},
		{Source: "android:p:usage", Kind: "android.usage.activity_paused", Start: at.Add(20 * time.Minute), Payload: json.RawMessage(`{"package_name":"com.whatsapp"}`)},
	}}
	server, err := New(repository, Config{IngestToken: "ingest", ReadToken: "read", OwnerAuthorized: func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer owner" }})
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer owner")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		return response
	}

	response := get("/v1/activity/summary?date=2026-01-15")
	if response.Code != http.StatusOK {
		t.Fatalf("summary status = %d: %s", response.Code, response.Body.String())
	}
	var summary struct {
		Devices []struct {
			Device string           `json:"device"`
			Apps   []activity.Usage `json:"apps"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, device := range summary.Devices {
		if device.Device == activity.DevicePhone {
			found = len(device.Apps) == 1 && device.Apps[0].Name == "com.whatsapp" && device.Apps[0].AppName == "WhatsApp"
		}
	}
	if !found {
		t.Fatalf("summary = %s", response.Body.String())
	}

	response = get("/v1/app-names")
	var names struct {
		Names map[string]string `json:"names"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &names) != nil || names.Names["com.whatsapp"] != "WhatsApp" {
		t.Fatalf("app names = %d %s", response.Code, response.Body.String())
	}

	// The agents' read token does not read the owner's endpoints.
	request := httptest.NewRequest(http.MethodGet, "/v1/app-names", nil)
	request.Header.Set("Authorization", "Bearer read")
	unauthorized := httptest.NewRecorder()
	server.ServeHTTP(unauthorized, request)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("read token status = %d", unauthorized.Code)
	}
}
