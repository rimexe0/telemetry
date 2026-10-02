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
	// Work that is no task is named by the server; a few minutes of it is not mentioned.
	if got := tracker.decide(trackingBody("other", 0, "", "T3 Code (Nightly) · Telemetry · Clarify next steps", morning), morning.Add(5*time.Minute)); got != nil {
		t.Fatalf("a few minutes off plan must not be mentioned: %+v", got)
	}
	// The next day greets again.
	if got := tracker.decide(trackingBody("other", 0, "", "", time.Time{}), morning.Add(24*time.Hour)); got == nil || got.kind != "greet" {
		t.Fatalf("next day = %+v", got)
	}
}

func TestTrackerInMultitaskWork(t *testing.T) {
	start := time.Date(2026, 10, 2, 14, 0, 0, 0, time.Local)
	tracker := &Tracker{greeted: start.Format("2006-01-02")}
	stopped := start.Add(-4 * time.Minute)
	body := trackingBody("other", 0, "", "T3 Code (Nightly) · Telemetry · Clarify next steps", start)
	body.Tracking.LearnContext = "T3 Code (Nightly) · Telemetry"
	body.Tracking.YourTurn = []turnRef{{TaskID: 3, Task: "Dad's album", Since: stopped}}
	// The album's agent stopped: said once, as a fact.
	turn := tracker.decide(body, start)
	if turn == nil || turn.kind != "turn" || turn.title != "Your turn: Dad's album" || turn.detail != "Its agent stopped 4 min ago." {
		t.Fatalf("your turn = %+v", turn)
	}
	if got := tracker.decide(body, start.Add(time.Minute)); got != nil {
		t.Fatalf("the same stop announced twice: %+v", got)
	}
	// Ten minutes on something outside the plan, nothing running: told, with what is waiting.
	if got := tracker.decide(body, start.Add(9*time.Minute)); got != nil {
		t.Fatalf("nine minutes is not mentioned: %+v", got)
	}
	off := tracker.decide(body, start.Add(10*time.Minute))
	if off == nil || off.kind != "off" || off.title != "10 min on T3 Code (Nightly) · Telemetry · Clarify next steps" || off.detail != "Waiting for you: Dad's album." ||
		off.learn != "T3 Code (Nightly) · Telemetry" {
		t.Fatalf("off plan = %+v", off)
	}
	if got := tracker.decide(body, start.Add(15*time.Minute)); got != nil {
		t.Fatalf("repeated too soon: %+v", got)
	}
	if got := tracker.decide(body, start.Add(20*time.Minute)); got == nil || got.kind != "off" {
		t.Fatalf("still off plan ten minutes later: %+v", got)
	}
	// While an agent of a planned task runs, being elsewhere is filling a wait.
	waiting := &Tracker{greeted: start.Format("2006-01-02")}
	body.Tracking.YourTurn, body.Tracking.AgentRunning = nil, true
	if got := waiting.decide(body, start.Add(30*time.Minute)); got != nil {
		t.Fatalf("filling a wait must not be mentioned: %+v", got)
	}
	// A one-off the owner excused is left alone.
	body.Tracking.AgentRunning, body.Tracking.Excused = false, true
	if got := waiting.decide(body, start.Add(30*time.Minute)); got != nil {
		t.Fatalf("an excused one-off must not be mentioned: %+v", got)
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

	// "Wrong" on a told task: that task forgets the thing and the picked one remembers it.
	told := notice{kind: "tell", title: "Tracking: Dad's album", options: options, context: "Keynote · deck", learn: "Keynote", task: 3}
	answers = []int{0, 0}
	tracker.show(told, "")
	if len(calls) != 2 || calls[0].path != "/v1/device/evidence" || calls[0].body["task_id"] != float64(3) || calls[0].body["verdict"] != "unrelated" ||
		calls[1].body["task_id"] != float64(4) || calls[1].body["verdict"] != "related" || calls[1].body["context"] != "Keynote" || tracker.told != 4 {
		t.Fatalf("correction = %+v told = %d", calls, tracker.told)
	}
	// "Something else" needs no explanation: the told task forgets it and nothing else is said.
	answers = []int{0, 1}
	tracker.show(told, "")
	if len(calls) != 3 || last().body["verdict"] != "unrelated" || shown[len(shown)-1] != "What is this part of?" || tracker.told != 0 {
		t.Fatalf("something else = %+v shown = %v", calls, shown)
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
	// Off plan: "It's part of…" is remembered for that task at project level; "One-off" excuses it for the sitting.
	answers = []int{1, 0}
	tracker.show(notice{kind: "off", options: options, context: "ChatGPT · WhatsApp Chat - Group · Create July session PDF", learn: "ChatGPT · WhatsApp Chat - Group"}, "")
	if last().path != "/v1/device/evidence" || last().body["task_id"] != float64(3) || last().body["context"] != "ChatGPT · WhatsApp Chat - Group" || last().body["verdict"] != "related" {
		t.Fatalf("it's part of = %+v", last())
	}
	answers = []int{2}
	tracker.show(notice{kind: "off", options: options, context: "Obsidian", learn: "Obsidian"}, "")
	if last().path != "/v1/device/claims" || last().body["task_id"] != nil || last().body["context"] != "Obsidian" {
		t.Fatalf("one-off = %+v", last())
	}
	before := len(calls)
	answers = []int{0}
	tracker.show(notice{kind: "turn", title: "Your turn: Dad's album"}, "")
	answers = []int{0}
	tracker.show(notice{kind: "back", focus: focus}, "")
	answers = []int{-1}
	tracker.show(notice{kind: "ask", focus: focus}, "")
	if len(calls) != before {
		t.Fatalf("OK and an unanswered question must record nothing: %+v", calls[before:])
	}
}
