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
)

// Tracker tells the owner, at this computer, which task the system thinks
// they are on, and asks when it cannot tell. Nothing here depends on the
// owner remembering to start anything: it reads the server's view each time
// the blocker fetches it.
type Tracker struct {
	Credentials *Credentials
	HTTP        *http.Client
	// Prompt shows a notice and returns the option chosen, or -1.
	Prompt func(title, detail string, options []string, timeout time.Duration) int

	mu      sync.Mutex
	busy    bool
	told    int64
	asked   time.Time
	greeted string
	health  Health
}

func (*Tracker) Name() string { return "tracker" }

func (t *Tracker) Status() map[string]any {
	status := t.health.Fields()
	status["notices"] = desktop.PromptSupported()
	return status
}

// Run only waits: the blocker hands over each answer it fetches.
func (t *Tracker) Run(ctx context.Context, host Host) error {
	<-ctx.Done()
	return nil
}

type taskRef struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

type trackingNow struct {
	Tracking struct {
		State      string     `json:"state"`
		TaskID     *int64     `json:"task_id"`
		Task       string     `json:"task"`
		Since      *time.Time `json:"since"`
		Context    string     `json:"context"`
		Candidates []taskRef  `json:"candidates"`
	} `json:"tracking"`
	Tasks []taskRef `json:"tasks"`
}

// notice is what to show the owner now, if anything.
type notice struct {
	kind    string // "greet", "tell" or "ask"
	title   string
	detail  string
	options []taskRef
}

// decide picks at most one notice and remembers that it was given, so each
// is shown once: the day's tasks at the first sit-down, a task when it is
// first recognised, and one question per stretch of unrecognised work.
func (t *Tracker) decide(now trackingNow, at time.Time) *notice {
	tracking := now.Tracking
	if tracking.State == "" || tracking.State == "away" {
		return nil
	}
	if day := at.Format("2006-01-02"); t.greeted != day && len(now.Tasks) > 0 {
		t.greeted = day
		titles := []string{}
		for _, task := range now.Tasks {
			titles = append(titles, task.Title)
		}
		// The greeting names the tasks; what is on right now follows next time.
		return &notice{kind: "greet", title: "Today", detail: strings.Join(titles, " · ")}
	}
	switch tracking.State {
	case "task":
		if tracking.TaskID != nil && *tracking.TaskID != t.told {
			t.told = *tracking.TaskID
			return &notice{kind: "tell", title: "Tracking: " + tracking.Task, options: now.Tasks}
		}
	case "unclear":
		if tracking.Since != nil && !tracking.Since.Equal(t.asked) && len(tracking.Candidates) > 0 {
			t.asked = *tracking.Since
			return &notice{kind: "ask", title: "What are you working on?", detail: tracking.Context, options: tracking.Candidates}
		}
	}
	return nil
}

const maxChoices = 6

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

func (t *Tracker) show(next notice, onScreen string) {
	prompt := t.Prompt
	if prompt == nil {
		prompt = desktop.Prompt
	}
	switch next.kind {
	case "greet":
		prompt(next.title, next.detail, []string{"OK"}, 20*time.Second)
	case "tell":
		// "Wrong" leads to the question; no answer means it was right.
		if prompt(next.title, "", []string{"Wrong"}, 8*time.Second) == 0 {
			t.ask(notice{title: "What are you working on?", detail: onScreen, options: next.options}, onScreen)
		}
	case "ask":
		t.ask(next, onScreen)
	}
}

func (t *Tracker) ask(next notice, onScreen string) {
	prompt := t.Prompt
	if prompt == nil {
		prompt = desktop.Prompt
	}
	options := next.options
	if len(options) > maxChoices {
		options = options[:maxChoices]
	}
	titles := []string{}
	for _, option := range options {
		titles = append(titles, option.Title)
	}
	choice := prompt(next.title, next.detail, append(titles, "Not work"), time.Minute)
	if choice < 0 {
		return
	}
	var task *int64
	if choice < len(options) {
		task = &options[choice].ID
		t.mu.Lock()
		t.told = *task
		t.mu.Unlock()
	}
	if err := t.claim(task, onScreen); err != nil {
		t.health.Failed(time.Now(), err)
		return
	}
	t.health.Succeeded(time.Now())
}

// claim records the owner's answer on the server.
func (t *Tracker) claim(task *int64, onScreen string) error {
	if t.Credentials == nil {
		return fmt.Errorf("this computer is not paired")
	}
	client := t.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	body, _ := json.Marshal(map[string]any{"task_id": task, "context": onScreen})
	request, err := http.NewRequest(http.MethodPost, strings.TrimRight(t.Credentials.BaseURL, "/")+"/v1/device/claims", bytes.NewReader(body))
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
		return fmt.Errorf("claim was refused with %d", response.StatusCode)
	}
	return nil
}
