package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/changes"
	"github.com/rimexe0/telemetry/internal/store"
)

func deviceServer(t *testing.T, repository *fakeRepository) *Server {
	t.Helper()
	server, err := New(repository, Config{
		IngestToken: "ingest-secret",
		ReadToken:   "read-secret",
		DeviceAuthorized: func(_ context.Context, deviceID, token string) (bool, bool, error) {
			return deviceID == "android-1", deviceID == "android-1" && token == "device-secret", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func deviceGet(server *Server, device, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/v1/device/now", nil)
	if device != "" {
		request.Header.Set("X-Telemetry-Device-ID", device)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response
}

func TestDeviceNowRequiresAPairedDevice(t *testing.T) {
	server := deviceServer(t, &fakeRepository{})
	for _, attempt := range [][2]string{{"", ""}, {"android-1", "wrong"}, {"android-1", "ingest-secret"}, {"android-2", "ingest-secret"}, {"", "read-secret"}} {
		if response := deviceGet(server, attempt[0], attempt[1]); response.Code != http.StatusUnauthorized {
			t.Fatalf("%v status = %d", attempt, response.Code)
		}
	}
}

func TestDeviceNowShowsTaskAndPolicy(t *testing.T) {
	activity := int64(5)
	now := time.Now().UTC()
	marker := "Section 2"
	repository := &fakeRepository{
		activities:       []store.Activity{{ID: 5, Title: "Client work", NextAction: "Draft summary"}},
		plan:             &store.Plan{VersionID: 1, Blocks: []store.PlanBlock{{ID: 9, ActivityID: &activity, Title: "Client work", StartTime: now.Add(-10 * time.Minute), DurationMinutes: 60, State: "active"}}},
		executionPrimary: &store.ExecutionSession{ID: 3, ActivityID: 5, State: "active", NextActionSnapshot: "old", ResumeMarker: &marker},
	}
	response := deviceGet(deviceServer(t, repository), "android-1", "device-secret")
	var payload struct {
		Task     deviceTask `json:"task"`
		Decision struct {
			Mode string `json:"mode"`
		} `json:"decision"`
		Timeline []any `json:"timeline"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body)
	}
	if payload.Task.Title != "Client work" || payload.Task.NextAction != "Draft summary" || payload.Task.ResumeMarker != "Section 2" || payload.Decision.Mode != "work" || len(payload.Timeline) == 0 {
		t.Fatalf("payload = %+v", payload)
	}
	repository.executionPrimary = nil
	response = deviceGet(deviceServer(t, repository), "android-1", "device-secret")
	_ = json.Unmarshal(response.Body.Bytes(), &payload)
	if payload.Task.State != "not_started" || payload.Task.Title != "Client work" {
		t.Fatalf("block-only task = %+v", payload.Task)
	}
}

func TestDeviceNowTimelineStartsBeforeNow(t *testing.T) {
	response := deviceGet(deviceServer(t, &fakeRepository{}), "android-1", "device-secret")
	var payload struct {
		GeneratedAt time.Time `json:"generated_at"`
		Timeline    []struct {
			From time.Time `json:"from"`
		} `json:"timeline"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || len(payload.Timeline) == 0 {
		t.Fatalf("payload = %s", response.Body)
	}
	if !payload.Timeline[0].From.Before(payload.GeneratedAt.Add(-5 * time.Minute)) {
		t.Fatalf("timeline starts at %s, generated %s", payload.Timeline[0].From, payload.GeneratedAt)
	}
}

// A device that already has the current answer waits; a change wakes it at
// once, and with no change it hears "not modified".
func TestDeviceNowWaitsForAChange(t *testing.T) {
	repository := &fakeRepository{}
	feed := changes.New()
	server, err := New(repository, Config{
		IngestToken: "ingest-secret",
		ReadToken:   "read-secret",
		DeviceAuthorized: func(_ context.Context, deviceID, token string) (bool, bool, error) {
			return deviceID == "android-1", deviceID == "android-1" && token == "device-secret", nil
		},
		Changes: feed,
	})
	if err != nil {
		t.Fatal(err)
	}
	first := deviceGet(server, "android-1", "device-secret")
	var body map[string]any
	json.Unmarshal(first.Body.Bytes(), &body)
	known, _ := body["fingerprint"].(string)
	if first.Code != http.StatusOK || known == "" {
		t.Fatalf("first = %d %v", first.Code, body["fingerprint"])
	}
	again := deviceGet(server, "android-1", "device-secret")
	json.Unmarshal(again.Body.Bytes(), &body)
	if body["fingerprint"] != known {
		t.Fatal("the fingerprint must not move with the clock")
	}
	wait := func(timeout string) (*httptest.ResponseRecorder, time.Duration) {
		request := httptest.NewRequest(http.MethodGet, "/v1/device/now?wait="+known+"&timeout="+timeout, nil)
		request.Header.Set("X-Telemetry-Device-ID", "android-1")
		request.Header.Set("Authorization", "Bearer device-secret")
		response := httptest.NewRecorder()
		started := time.Now()
		server.ServeHTTP(response, request)
		return response, time.Since(started)
	}
	if response, took := wait("1"); response.Code != http.StatusNotModified || took < 900*time.Millisecond {
		t.Fatalf("no change = %d after %s", response.Code, took)
	}
	// A plan appears while the device waits.
	go func() {
		time.Sleep(200 * time.Millisecond)
		activity := int64(5)
		repository.plan = &store.Plan{VersionID: 2, Blocks: []store.PlanBlock{{ID: 1, ActivityID: &activity, Title: "Work",
			StartTime: time.Now().Add(-time.Minute), DurationMinutes: 60, State: "planned"}}}
		feed.Changed()
	}()
	if response, took := wait("30"); response.Code != http.StatusOK || took > 5*time.Second {
		t.Fatalf("after a change = %d after %s", response.Code, took)
	}
}

func TestDeviceNowKeepsWaitingPlaceRemindersFromOlderApps(t *testing.T) {
	now := time.Now().UTC()
	reached := now.Add(-time.Minute)
	place := &store.FollowUpPlace{When: store.PlaceArrive, Name: "home", Home: true, Points: []store.PlacePoint{{Lat: 38.4, Lon: 17.1, Radius: 120}}}
	server := deviceServer(t, &fakeRepository{followUps: []store.FollowUp{
		{ID: 1, Title: "Timed", DueAt: now, State: store.FollowUpOpen},
		{ID: 2, Title: "Waiting for home", DueAt: now.Add(-time.Hour), State: store.FollowUpOpen, Place: place},
		{ID: 3, Title: "Reached home", DueAt: now.Add(-time.Hour), State: store.FollowUpOpen, Place: place, TriggeredAt: &reached},
	}})
	listed := func(path string) map[int64]store.FollowUp {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("X-Telemetry-Device-ID", "android-1")
		request.Header.Set("Authorization", "Bearer device-secret")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		var body struct {
			FollowUps []store.FollowUp `json:"follow_ups"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, response.Code, response.Body)
		}
		items := map[int64]store.FollowUp{}
		for _, item := range body.FollowUps {
			items[item.ID] = item
		}
		return items
	}
	old := listed("/v1/device/now")
	if _, ok := old[2]; ok || len(old) != 2 {
		t.Fatalf("an older app must not see a waiting place reminder: %+v", old)
	}
	if !old[3].DueAt.Equal(reached) {
		t.Fatalf("an older app sees a reached place reminder as due from the arrival, got %v", old[3].DueAt)
	}
	if current := listed("/v1/device/now?places=1"); len(current) != 3 || current[2].Place == nil {
		t.Fatalf("the current app gets every open follow-up with its place: %+v", current)
	}
}
