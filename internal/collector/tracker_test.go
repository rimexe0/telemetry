package collector

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func trackingBody(state string, task int64, title, context string, since time.Time) trackingNow {
	var now trackingNow
	now.Tasks = []taskRef{{3, "Dad's album"}, {4, "Science fair presentation"}}
	now.Tracking.State, now.Tracking.Task, now.Tracking.Context = state, title, context
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
	// Work that is no task is named by the server and never asked about.
	if got := tracker.decide(trackingBody("other", 0, "", "T3 Code (Nightly) · Telemetry · Clarify next steps", morning), morning.Add(10*time.Minute)); got != nil {
		t.Fatalf("off-plan work must not raise a question: %+v", got)
	}
	// The next day greets again.
	if got := tracker.decide(trackingBody("other", 0, "", "", time.Time{}), morning.Add(24*time.Hour)); got == nil || got.kind != "greet" {
		t.Fatalf("next day = %+v", got)
	}
}

func TestTrackerInAFocusSession(t *testing.T) {
	tracker := &Tracker{greeted: time.Date(2026, 10, 2, 15, 0, 0, 0, time.Local).Format("2006-01-02")}
	start := time.Date(2026, 10, 2, 15, 0, 0, 0, time.Local)
	body := func(on, context string, off time.Duration) trackingNow {
		now := trackingBody("other", 0, "", context, time.Time{})
		focus := &focusNow{TaskID: 4, Task: "Science fair presentation", NextAction: "Outline the three sections", Since: start, On: on, Context: context, LearnContext: context}
		if on != "on" {
			since := start.Add(off)
			focus.OffSince = &since
		}
		now.Tracking.Focus = focus
		return now
	}
	if got := tracker.decide(body("on", "docs.google.com", 0), start.Add(5*time.Minute)); got != nil {
		t.Fatalf("on the task: %+v", got)
	}
	// A minute away is not mentioned; two minutes is.
	if got := tracker.decide(body("off", "trendyol.com", 10*time.Minute), start.Add(11*time.Minute)); got != nil {
		t.Fatalf("one minute away: %+v", got)
	}
	back := tracker.decide(body("off", "trendyol.com", 10*time.Minute), start.Add(12*time.Minute))
	if back == nil || back.kind != "back" || back.title != "Back to: Science fair presentation" || back.detail != "2 min on trendyol.com. Next: Outline the three sections" {
		t.Fatalf("back = %+v", back)
	}
	if got := tracker.decide(body("off", "trendyol.com", 10*time.Minute), start.Add(14*time.Minute)); got != nil {
		t.Fatalf("reminded again too soon: %+v", got)
	}
	if got := tracker.decide(body("off", "trendyol.com", 10*time.Minute), start.Add(17*time.Minute)); got == nil || got.kind != "back" {
		t.Fatalf("still away after five more minutes: %+v", got)
	}
	// Something unknown is asked about once, not treated as drift.
	ask := tracker.decide(body("unknown", "en.wikipedia.org", 20*time.Minute), start.Add(23*time.Minute))
	if ask == nil || ask.kind != "ask" || ask.title != "Is this part of Science fair presentation?" || ask.detail != "en.wikipedia.org" {
		t.Fatalf("ask = %+v", ask)
	}
	if got := tracker.decide(body("unknown", "en.wikipedia.org", 20*time.Minute), start.Add(24*time.Minute)); got != nil {
		t.Fatalf("asked twice about one thing: %+v", got)
	}
}

func TestTrackerRecordsAnswers(t *testing.T) {
	type call struct {
		path string
		body map[string]any
	}
	var calls []call
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Telemetry-Device-ID") != "mac-1" || request.Header.Get("Authorization") != "Bearer secret" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		raw, _ := io.ReadAll(request.Body)
		var body map[string]any
		json.Unmarshal(raw, &body)
		calls = append(calls, call{request.URL.Path, body})
		writer.Write([]byte(`{}`))
	}))
	defer server.Close()
	shown := []string{}
	var answers []int
	tracker := &Tracker{Credentials: &Credentials{BaseURL: server.URL, DeviceID: "mac-1", Secret: "secret"},
		Prompt: func(title, detail string, options []string, _ time.Duration) int {
			shown = append(shown, title)
			answer := answers[0]
			answers = answers[1:]
			return answer
		}}
	options := []taskRef{{3, "Dad's album"}, {4, "Science fair presentation"}}
	last := func() call { return calls[len(calls)-1] }

	// "Wrong" on a told task lets the owner pick the right one; "Something else" needs no explanation.
	answers = []int{0, 1}
	tracker.show(notice{kind: "tell", title: "Tracking: Dad's album", options: options}, "Keynote")
	if last().path != "/v1/device/claims" || last().body["task_id"] != float64(4) || last().body["context"] != "Keynote" || tracker.told != 4 {
		t.Fatalf("pick = %+v told = %d", last(), tracker.told)
	}
	answers = []int{0, 2}
	tracker.show(notice{kind: "tell", title: "Tracking: Dad's album", options: options}, "Finder")
	if last().body["task_id"] != nil || shown[len(shown)-1] != "What is this part of?" {
		t.Fatalf("something else = %+v shown = %v", last(), shown)
	}
	// Starting a focus session from the day's greeting.
	answers = []int{1}
	tracker.show(notice{kind: "greet", title: "Today", options: options}, "")
	if last().path != "/v1/device/focus" || last().body["action"] != "start" || last().body["task_id"] != float64(4) {
		t.Fatalf("start focus = %+v", last())
	}
	focus := &focusNow{TaskID: 4, Task: "Science fair presentation", Context: "T3 Code (Nightly) · Telemetry · Clarify next steps", LearnContext: "T3 Code (Nightly) · Telemetry"}
	// Yes is remembered at project level; "just this time" is a claim on exactly what is on screen; no is remembered.
	answers = []int{0}
	tracker.show(notice{kind: "ask", title: "Is this part of Science fair presentation?", focus: focus}, "")
	if last().path != "/v1/device/evidence" || last().body["verdict"] != "related" || last().body["context"] != "T3 Code (Nightly) · Telemetry" {
		t.Fatalf("yes = %+v", last())
	}
	answers = []int{1}
	tracker.show(notice{kind: "ask", focus: focus}, "")
	if last().path != "/v1/device/claims" || last().body["context"] != "T3 Code (Nightly) · Telemetry · Clarify next steps" || last().body["task_id"] != float64(4) {
		t.Fatalf("just this time = %+v", last())
	}
	answers = []int{2}
	tracker.show(notice{kind: "ask", focus: focus}, "")
	if last().path != "/v1/device/evidence" || last().body["verdict"] != "unrelated" {
		t.Fatalf("no = %+v", last())
	}
	// From the reminder: pause the session, or say it is part of the task.
	answers = []int{3}
	tracker.show(notice{kind: "back", focus: focus}, "")
	if last().path != "/v1/device/focus" || last().body["action"] != "pause" {
		t.Fatalf("pause = %+v", last())
	}
	before := len(calls)
	answers = []int{0}
	tracker.show(notice{kind: "back", focus: focus}, "")
	answers = []int{-1}
	tracker.show(notice{kind: "ask", focus: focus}, "")
	if len(calls) != before {
		t.Fatalf("OK and an unanswered question must record nothing: %+v", calls[before:])
	}
}
