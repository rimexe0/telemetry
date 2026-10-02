package collector

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func trackingBody(state string, task int64, title, context string, since time.Time, candidates ...taskRef) trackingNow {
	var now trackingNow
	now.Tasks = []taskRef{{3, "Dad's album"}, {4, "Science fair presentation"}}
	now.Tracking.State, now.Tracking.Task, now.Tracking.Context, now.Tracking.Candidates = state, title, context, candidates
	if task != 0 {
		now.Tracking.TaskID = &task
	}
	if !since.IsZero() {
		now.Tracking.Since = &since
	}
	return now
}

func TestTrackerShowsEachNoticeOnce(t *testing.T) {
	tracker := &Tracker{}
	morning := time.Date(2026, 10, 2, 14, 0, 0, 0, time.Local)
	if got := tracker.decide(trackingBody("away", 0, "", "", time.Time{}), morning); got != nil {
		t.Fatalf("nobody at the Mac: %+v", got)
	}
	// First sit-down of the day lists the tasks, once.
	greet := tracker.decide(trackingBody("other", 0, "", "", time.Time{}), morning)
	if greet == nil || greet.kind != "greet" || greet.detail != "Dad's album · Science fair presentation" {
		t.Fatalf("greeting = %+v", greet)
	}
	if got := tracker.decide(trackingBody("other", 0, "", "", time.Time{}), morning.Add(time.Minute)); got != nil {
		t.Fatalf("second greeting: %+v", got)
	}
	// A recognised task is told once, and again only when it changes.
	tell := tracker.decide(trackingBody("task", 3, "Dad's album", "", morning), morning.Add(2*time.Minute))
	if tell == nil || tell.kind != "tell" || tell.title != "Tracking: Dad's album" {
		t.Fatalf("tell = %+v", tell)
	}
	if got := tracker.decide(trackingBody("task", 3, "Dad's album", "", morning), morning.Add(3*time.Minute)); got != nil {
		t.Fatalf("told twice: %+v", got)
	}
	if got := tracker.decide(trackingBody("task", 4, "Science fair presentation", "", morning), morning.Add(4*time.Minute)); got == nil || got.kind != "tell" {
		t.Fatalf("a different task must be told: %+v", got)
	}
	// Unrecognised work is asked about once per stretch.
	since := morning.Add(10 * time.Minute)
	ask := tracker.decide(trackingBody("unclear", 0, "", "Keynote", since, taskRef{3, "Dad's album"}, taskRef{4, "Science fair presentation"}), since.Add(2*time.Minute))
	if ask == nil || ask.kind != "ask" || ask.detail != "Keynote" || len(ask.options) != 2 {
		t.Fatalf("ask = %+v", ask)
	}
	if got := tracker.decide(trackingBody("unclear", 0, "", "Keynote", since, taskRef{3, "Dad's album"}), since.Add(3*time.Minute)); got != nil {
		t.Fatalf("asked twice about one stretch: %+v", got)
	}
	// The next day greets again.
	if got := tracker.decide(trackingBody("other", 0, "", "", time.Time{}), morning.Add(24*time.Hour)); got == nil || got.kind != "greet" {
		t.Fatalf("next day = %+v", got)
	}
}

func TestTrackerRecordsTheAnswer(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/device/claims" || request.Header.Get("X-Telemetry-Device-ID") != "mac-1" || request.Header.Get("Authorization") != "Bearer secret" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(request.Body)
		json.Unmarshal(body, &received)
		writer.Write([]byte(`{}`))
	}))
	defer server.Close()
	shown := []string{}
	answers := []int{1}
	tracker := &Tracker{Credentials: &Credentials{BaseURL: server.URL, DeviceID: "mac-1", Secret: "secret"},
		Prompt: func(title, detail string, options []string, _ time.Duration) int {
			shown = append(shown, title)
			answer := answers[0]
			answers = answers[1:]
			return answer
		}}
	options := []taskRef{{3, "Dad's album"}, {4, "Science fair presentation"}}
	tracker.show(notice{kind: "ask", title: "What are you working on?", detail: "Keynote", options: options}, "Keynote")
	if received["task_id"] != float64(4) || received["context"] != "Keynote" || tracker.told != 4 {
		t.Fatalf("claim = %+v told = %d", received, tracker.told)
	}
	// "Wrong" on a told task opens the question; "Not work" is the last option.
	answers = []int{0, 2}
	tracker.show(notice{kind: "tell", title: "Tracking: Dad's album", options: options}, "Finder")
	if len(shown) != 3 || shown[2] != "What are you working on?" || received["task_id"] != nil || received["context"] != "Finder" {
		t.Fatalf("shown = %v claim = %+v", shown, received)
	}
}
