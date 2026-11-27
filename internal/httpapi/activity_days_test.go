package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/activity"
	"github.com/rimexe0/telemetry/internal/places"
	"github.com/rimexe0/telemetry/internal/store"
)

// daysRepository answers activity records by their time and has labels.
type daysRepository struct {
	placesRepository
	labels []store.ActivityLabel
}

func (r daysRepository) ActivityRecords(_ context.Context, from, to time.Time) ([]activity.Record, map[string]time.Time, error) {
	result := []activity.Record{}
	for _, record := range r.records {
		if !record.Start.Before(from) && record.Start.Before(to) {
			result = append(result, record)
		}
	}
	return result, nil, nil
}

func (r daysRepository) ListActivityLabels(context.Context) ([]store.ActivityLabel, error) {
	return r.labels, nil
}

type daysBody struct {
	Timezone string        `json:"timezone"`
	Version  string        `json:"version"`
	From     string        `json:"from"`
	To       string        `json:"to"`
	Days     []activityDay `json:"days"`
}

func daysCall(t *testing.T, server *Server, query string) (int, daysBody) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/v1/activity/days"+query, nil)
	request.Header.Set("X-Test-Owner", "yes")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	var body daysBody
	if response.Code == http.StatusOK {
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
	}
	return response.Code, body
}

func TestActivityDaysGivesOneRowPerDayAndKeepsFinishedOnes(t *testing.T) {
	_, inner := budgetServer(t, budgetPolicy(t))
	repository := &daysRepository{placesRepository: placesRepository{inner},
		labels: []store.ActivityLabel{{ID: 1, Kind: activity.LabelKindApp, Match: "com.example.feed", Label: activity.Distraction}}}
	server, err := New(repository, Config{IngestToken: "ingest-secret", ReadToken: "read-secret",
		OwnerAuthorized: func(request *http.Request) bool { return request.Header.Get("X-Test-Owner") == "yes" }})
	if err != nil {
		t.Fatal(err)
	}
	location, _ := time.LoadLocation("Europe/Istanbul")
	now := time.Now().In(location)
	day := func(back, hour, minute int) time.Time {
		return time.Date(now.Year(), now.Month(), now.Day()-back, hour, minute, 0, 0, location).UTC()
	}
	usage := func(kind string, at time.Time) activity.Record {
		return activity.Record{Source: "android:phone:usage", Kind: "android.usage.activity_" + kind, Start: at, Payload: json.RawMessage(`{"package_name":"com.example.feed"}`)}
	}
	inner.records = []activity.Record{usage("resumed", day(3, 10, 0)), usage("paused", day(3, 10, 30))}
	// At the pier for half an hour three days ago, and nowhere else.
	inner.fixes = []places.Fix{pierFix(day(3, 12, 0), 0), pierFix(day(3, 12, 10), 5), pierFix(day(3, 12, 30), 10)}

	if code, _ := daysCall(t, server, ""); code != http.StatusOK {
		t.Fatalf("default range: %d", code)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/activity/days", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("without the owner: %d", response.Code)
	}

	from := day(4, 0, 0).In(location).Format("2006-01-02")
	code, body := daysCall(t, server, "?from="+from)
	if code != http.StatusOK || len(body.Days) != 5 || body.From != from || body.Version == "" {
		t.Fatalf("code %d body %+v", code, body)
	}
	empty, used, today := body.Days[0], body.Days[1], body.Days[4]
	for _, device := range empty.Devices {
		if device.Reported {
			t.Fatalf("a day with nothing reported %+v", device)
		}
	}
	if empty.Positions != 0 || empty.DistanceKM != nil || empty.OutsideMinutes != nil {
		t.Fatalf("a day without positions has places numbers: %+v", empty)
	}
	if !used.Final || today.Final || used.DistractionMinutes != 30 || len(used.Cells) != 144 || used.Cells[60] != 'd' {
		t.Fatalf("the used day %+v", used)
	}
	phone := used.Devices[1]
	if phone.Device != activity.DevicePhone || !phone.Reported || phone.DistractionMinutes != 30 || phone.ActiveMinutes != 30 {
		t.Fatalf("the phone %+v", phone)
	}
	if len(used.Apps) != 1 || used.Apps[0].Name != "com.example.feed" || used.Apps[0].Label != activity.Distraction {
		t.Fatalf("apps %+v", used.Apps)
	}
	if used.Positions != 3 || used.DistanceKM == nil || len(used.Stays) != 1 || used.Stays[0].Name != "Test pier" {
		t.Fatalf("places %+v", used)
	}
	if used.AgentMinutes == nil || *used.AgentMinutes != 0 {
		t.Fatalf("agent minutes %v", used.AgentMinutes)
	}

	// A finished day comes from the cache; a change of labels counts it again.
	inner.records = nil
	if _, again := daysCall(t, server, "?from="+from); again.Days[1].DistractionMinutes != 30 {
		t.Fatalf("finished day was counted again: %+v", again.Days[1])
	}
	repository.labels = nil
	if _, again := daysCall(t, server, "?from="+from); again.Days[1].DistractionMinutes != 0 || again.Version == body.Version {
		t.Fatalf("new labels kept the old count: %+v", again.Days[1])
	}

	tomorrow := day(-1, 0, 0).In(location).Format("2006-01-02")
	if code, _ := daysCall(t, server, "?to="+tomorrow); code != http.StatusBadRequest {
		t.Fatalf("future: %d", code)
	}
	if code, _ := daysCall(t, server, "?from="+day(80, 0, 0).In(location).Format("2006-01-02")); code != http.StatusBadRequest {
		t.Fatalf("too long: %d", code)
	}
}
