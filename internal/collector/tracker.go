package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rimexe0/telemetry/internal/desktop"
	"github.com/rimexe0/telemetry/internal/model"
)

const (
	// The owner agreed to be told two minutes after leaving a focus task.
	focusLeave = 2 * time.Minute
	// While still away from it, the reminder comes back this often.
	focusRepeat = 5 * time.Minute
	// A task whose agent stopped is said once at the stop, and once more
	// when it has waited this long and the owner is elsewhere.
	waitingAgain = 30 * time.Minute
	maxChoices   = 6
)

// Tracker tells the owner, at this computer, which task the system thinks
// they are on, and lets them say when that is wrong. Outside a focus session
// it does not ask what they are doing: they do several things at once, and
// what is on screen is named by the server from its own records. It says
// when a task's agent has stopped and it is their turn, and once more if it
// is still waiting a while later. Time outside the day's tasks is not
// counted at them here: the owner found a growing "N min outside today's
// tasks" to be a scolding, and the assistant's check-in judges real drift.
// In a focus session it says when they have left the task, and asks whether
// something it does not know is part of it. Every notice states a fact and
// the way forward. Nothing here depends on the owner remembering anything.
type Tracker struct {
	Credentials *Credentials
	HTTP        *http.Client
	// Notices is "focus" (only in a focus session the owner started; the
	// default), "all", or "off". The owner is easily pulled away, and found
	// notices outside focus to be one more distraction (2026-10-06).
	Notices string
	// Prompt shows a notice and returns the option chosen, or -1.
	Prompt func(title, detail string, options []string, timeout time.Duration) int

	mu      sync.Mutex
	busy    bool
	told    int64
	greeted string
	// Per focus session: what was asked about, and the last reminder.
	session  time.Time
	asked    map[string]bool
	reminded time.Time
	// Outside focus: the agent stops already announced.
	turns map[string]bool
	// host is set by Run; notices are recorded through it.
	host   *Host
	health Health
}

// The tracker's notice settings.
const (
	NoticesFocus = "focus"
	NoticesAll   = "all"
	NoticesOff   = "off"
)

func (*Tracker) Name() string { return "tracker" }

func (t *Tracker) Status() map[string]any {
	status := t.health.Fields()
	status["notices"] = desktop.PromptSupported()
	return status
}

const trackerGreeted = "tracker:greeted"

// Run only waits: the blocker hands over each answer it fetches. It restores
// the day already greeted, so a restart does not repeat the day's greeting.
func (t *Tracker) Run(ctx context.Context, host Host) error {
	t.mu.Lock()
	t.host = &host
	if day, ok, err := host.Queue.LoadCursor(ctx, trackerGreeted); err == nil && ok {
		t.greeted = day
	}
	t.mu.Unlock()
	<-ctx.Done()
	return nil
}

// record keeps every notice and the owner's answer as an observation: what
// was said, about what, and what they chose. Later this shows which notices
// bring the owner back and which they ignore.
func (t *Tracker) record(next notice, options []string, choice int) {
	t.mu.Lock()
	host := t.host
	t.mu.Unlock()
	if host == nil {
		return
	}
	now := time.Now().UTC()
	answer := ""
	if choice >= 0 && choice < len(options) {
		answer = options[choice]
	}
	payload, _ := json.Marshal(map[string]any{"notice": next.kind, "title": next.title, "detail": next.detail, "context": next.context,
		"task_id": next.task, "options": options, "answer": answer, "answered": choice >= 0})
	observation := model.Observation{
		SourceInstanceKey: "telemetry-desktop:" + host.DeviceID + ":notices", SourceKind: "desktop_notice", DeviceID: host.DeviceID,
		SourceRecordKey: "notice:" + now.Format(time.RFC3339Nano), Kind: "desktop.notice", OccurredAt: now, CollectedAt: now,
		SourceSchemaVersion: "desktop-notice-1", OriginalPayload: payload,
	}
	if observation.Normalize() == nil {
		host.Queue.Enqueue(context.Background(), observation, now)
	}
	if next.kind == "greet" {
		host.Queue.SaveCursor(context.Background(), trackerGreeted, now.Local().Format("2006-01-02"), now)
	}
}

type taskRef struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

type focusNow struct {
	TaskID       int64      `json:"task_id"`
	Task         string     `json:"task"`
	NextAction   string     `json:"next_action"`
	Since        time.Time  `json:"since"`
	On           string     `json:"on"`
	Context      string     `json:"context"`
	LearnContext string     `json:"learn_context"`
	OffSince     *time.Time `json:"off_since"`
}

type turnRef struct {
	TaskID int64     `json:"task_id"`
	Task   string    `json:"task"`
	Since  time.Time `json:"since"`
}

type trackingNow struct {
	Tracking struct {
		State        string     `json:"state"`
		TaskID       *int64     `json:"task_id"`
		Task         string     `json:"task"`
		Since        *time.Time `json:"since"`
		Context      string     `json:"context"`
		LearnContext string     `json:"learn_context"`
		Excused      bool       `json:"excused"`
		AgentRunning bool       `json:"agent_running"`
		YourTurn     []turnRef  `json:"your_turn"`
		Focus        *focusNow  `json:"focus"`
	} `json:"tracking"`
	Tasks []taskRef `json:"tasks"`
}

// notice is what to show the owner now, if anything.
type notice struct {
	// kind is "greet" (the day's tasks), "tell" (a recognised task), "turn"
	// (a task's agent stopped, or is still waiting), "back" (left the focus
	// task) or "ask" (is this part of the focus task?).
	kind    string
	title   string
	detail  string
	options []taskRef
	focus   *focusNow
	// context and learn are what is on screen and what an answer about it
	// would be remembered as; task is the task a "tell" named.
	context string
	learn   string
	task    int64
}

// decide picks at most one notice and remembers that it was given, so each
// comes once: the day's tasks at the first sit-down, a task when it is first
// recognised, a question per unknown thing in a focus session, and a
// reminder when the owner has left the focus task.
func (t *Tracker) decide(now trackingNow, at time.Time) *notice {
	tracking := now.Tracking
	if tracking.State == "" || tracking.State == "away" || t.Notices == NoticesOff || (t.Notices != NoticesAll && tracking.Focus == nil) {
		return nil
	}
	if day := at.Format("2006-01-02"); t.greeted != day && len(now.Tasks) > 0 && tracking.Focus == nil {
		t.greeted = day
		titles := []string{}
		for _, task := range now.Tasks {
			titles = append(titles, task.Title)
		}
		return &notice{kind: "greet", title: "Today", detail: strings.Join(titles, " · "), options: now.Tasks}
	}
	if focus := tracking.Focus; focus != nil {
		if !focus.Since.Equal(t.session) {
			t.session, t.asked, t.reminded = focus.Since, map[string]bool{}, time.Time{}
		}
		if focus.On == "on" || focus.OffSince == nil || at.Sub(*focus.OffSince) < focusLeave {
			return nil
		}
		if focus.On == "unknown" && !t.asked[focus.LearnContext] {
			t.asked[focus.LearnContext] = true
			t.reminded = at
			return &notice{kind: "ask", title: "Is this part of " + focus.Task + "?", detail: focus.Context, focus: focus}
		}
		if at.Sub(t.reminded) >= focusRepeat {
			t.reminded = at
			detail := fmt.Sprintf("%d min on %s.", int(at.Sub(*focus.OffSince).Minutes()), focus.Context)
			if focus.NextAction != "" {
				detail += " Next: " + focus.NextAction
			}
			return &notice{kind: "back", title: "Back to: " + focus.Task, detail: detail, focus: focus}
		}
		return nil
	}
	// An agent stopped: it is the owner's turn on that task. Said once per stop,
	// and not when they are already on it.
	if t.turns == nil {
		t.turns = map[string]bool{}
	}
	for _, turn := range tracking.YourTurn {
		key := fmt.Sprintf("%d %s", turn.TaskID, turn.Since.UTC().Format(time.RFC3339))
		if t.turns[key] || (tracking.TaskID != nil && *tracking.TaskID == turn.TaskID) {
			continue
		}
		t.turns[key] = true
		return &notice{kind: "turn", title: "Your turn: " + turn.Task, detail: "Its agent stopped " + ago(at.Sub(turn.Since)) + "."}
	}
	if tracking.State == "task" && tracking.TaskID != nil && *tracking.TaskID != t.told {
		t.told = *tracking.TaskID
		return &notice{kind: "tell", title: "Tracking: " + tracking.Task, detail: tracking.Context, options: now.Tasks,
			context: tracking.Context, learn: tracking.LearnContext, task: *tracking.TaskID}
	}
	// Still waiting a while later, and the owner is elsewhere: said once
	// more, as what is waiting rather than how long they have been away.
	for _, turn := range tracking.YourTurn {
		key := fmt.Sprintf("again %d %s", turn.TaskID, turn.Since.UTC().Format(time.RFC3339))
		if t.turns[key] || at.Sub(turn.Since) < waitingAgain || (tracking.TaskID != nil && *tracking.TaskID == turn.TaskID) {
			continue
		}
		t.turns[key] = true
		return &notice{kind: "turn", title: turn.Task + " is waiting for you", detail: "Its agent stopped at " + turn.Since.Local().Format("15:04") + "."}
	}
	return nil
}

// ago words a duration for a notice: "just now", "4 min ago", "2 h ago".
func ago(elapsed time.Duration) string {
	switch minutes := int(elapsed.Minutes()); {
	case minutes < 1:
		return "just now"
	case minutes < 60:
		return fmt.Sprintf("%d min ago", minutes)
	default:
		return fmt.Sprintf("%d h ago", minutes/60)
	}
}

// Handle reads one /v1/device/now answer and shows a notice when one is due.
func (t *Tracker) Handle(body []byte) {
	var now trackingNow
	if json.Unmarshal(body, &now) != nil {
		return
	}
	t.mu.Lock()
	if t.busy {
		t.mu.Unlock()
		return
	}
	next := t.decide(now, time.Now())
	if next == nil {
		t.mu.Unlock()
		return
	}
	t.busy = true
	t.mu.Unlock()
	go func() {
		defer func() {
			t.mu.Lock()
			t.busy = false
			t.mu.Unlock()
		}()
		t.show(*next, now.Tracking.Context)
	}()
}

func (t *Tracker) prompt(title, detail string, options []string, timeout time.Duration) int {
	if t.Prompt != nil {
		return t.Prompt(title, detail, options, timeout)
	}
	return desktop.Prompt(title, detail, options, timeout)
}

// present shows a notice with its buttons and records it with the answer.
func (t *Tracker) present(next notice, options []string, timeout time.Duration) int {
	choice := t.prompt(next.title, next.detail, options, timeout)
	t.record(next, options, choice)
	return choice
}

func (t *Tracker) show(next notice, onScreen string) {
	var err error
	switch next.kind {
	case "greet":
		// Each task can be started as a focus session from here; "OK" just closes.
		options := next.options
		if len(options) > maxChoices {
			options = options[:maxChoices]
		}
		titles := []string{}
		for _, option := range options {
			titles = append(titles, "Focus: "+option.Title)
		}
		if choice := t.present(next, append(titles, "OK"), 30*time.Second); choice >= 0 && choice < len(options) {
			err = t.post("/v1/device/focus", map[string]any{"action": "start", "task_id": options[choice].ID})
		}
	case "tell":
		// "Wrong" lets the owner say what it is; no answer means it was right.
		if t.present(next, []string{"Wrong"}, 8*time.Second) == 0 {
			err = t.correct(next)
		}
	case "turn":
		t.present(next, []string{"OK"}, 20*time.Second)
	case "ask":
		next.context, next.task = next.focus.Context, next.focus.TaskID
		err = t.answer(*next.focus, t.present(next, []string{"Yes", "Just this time", "No"}, time.Minute))
	case "back":
		next.context, next.task = next.focus.Context, next.focus.TaskID
		switch t.present(next, []string{"OK", "It's part of it", "Just this time", "Pause focus"}, 30*time.Second) {
		case 1:
			err = t.answer(*next.focus, 0)
		case 2:
			err = t.answer(*next.focus, 1)
		case 3:
			err = t.post("/v1/device/focus", map[string]any{"action": "pause"})
		}
	}
	if err != nil {
		t.health.Failed(time.Now(), err)
		return
	}
	t.health.Succeeded(time.Now())
}

// answer records what the owner said about the thing on screen in a focus
// session: 0 it is part of the task (remembered), 1 it is part of it this
// time only (nothing is learned), 2 it is not (remembered).
func (t *Tracker) answer(focus focusNow, choice int) error {
	switch choice {
	case 0:
		return t.post("/v1/device/evidence", map[string]any{"task_id": focus.TaskID, "context": focus.LearnContext, "verdict": "related"})
	case 1:
		return t.post("/v1/device/claims", map[string]any{"task_id": focus.TaskID, "context": focus.Context})
	case 2:
		return t.post("/v1/device/evidence", map[string]any{"task_id": focus.TaskID, "context": focus.LearnContext, "verdict": "unrelated"})
	}
	return nil
}

// correct handles "Wrong" on a told task: that task forgets this thing, and
// the task the owner picks instead remembers it. "Something else" needs no
// explanation: it is simply none of the tasks.
func (t *Tracker) correct(told notice) error {
	options := []taskRef{}
	for _, option := range told.options {
		if option.ID != told.task && len(options) < maxChoices {
			options = append(options, option)
		}
	}
	titles := []string{}
	for _, option := range options {
		titles = append(titles, option.Title)
	}
	choice := t.prompt("What is this part of?", told.context, append(titles, "Something else"), time.Minute)
	if choice < 0 {
		return nil
	}
	if err := t.post("/v1/device/evidence", map[string]any{"task_id": told.task, "context": told.learn, "verdict": "unrelated"}); err != nil {
		return err
	}
	t.mu.Lock()
	t.told = 0
	t.mu.Unlock()
	if choice >= len(options) {
		return nil
	}
	t.mu.Lock()
	t.told = options[choice].ID
	t.mu.Unlock()
	return t.post("/v1/device/evidence", map[string]any{"task_id": options[choice].ID, "context": told.learn, "verdict": "related"})
}

// post sends one of the owner's answers to the server.
func (t *Tracker) post(path string, payload map[string]any) error {
	if t.Credentials == nil {
		return fmt.Errorf("this computer is not paired")
	}
	client := t.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	body, _ := json.Marshal(payload)
	request, err := http.NewRequest(http.MethodPost, strings.TrimRight(t.Credentials.BaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+t.Credentials.Secret)
	request.Header.Set("X-Telemetry-Device-ID", t.Credentials.DeviceID)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s was refused with %d", path, response.StatusCode)
	}
	return nil
}
