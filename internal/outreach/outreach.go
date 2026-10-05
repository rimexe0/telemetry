// Package outreach decides when the assistant writes to the owner without
// being asked, and words what it is given to write from. It reaches out for
// two things only: the morning, when the records show the owner woke, and a
// long stretch outside the day's tasks with nothing to wait for. Everything
// else stays a notice on the device.
package outreach

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	KindMorning = "morning"
	KindOffPlan = "off_plan"
	// KindCollection tells the owner that something Telemetry reads changed
	// its format and needs fixing. It is not counted against the hourly cap.
	KindCollection = "collection"

	// MorningWithin is how long after waking the morning message is still
	// sent; later than that it is no longer the start of the day.
	MorningWithin = 2 * time.Hour
	// OffPlanWindow is the stretch looked at, and OffPlanActive how much of
	// it the owner must have been at a device for it to count.
	OffPlanWindow = 45 * time.Minute
	OffPlanActive = 30 * time.Minute
	// Between is the least time between two messages nobody asked for.
	Between = time.Hour
	// A check-in comes at most OffPlanPerDay times in a day, OffPlanRepeat
	// apart: the owner found one an hour, each saying the same, to be nagging.
	OffPlanRepeat = 3 * time.Hour
	OffPlanPerDay = 3
	// Drift is OffPlanDistracted on distractions, or no one thing taking
	// OffPlanFocused of the time: jumping between unrelated things. A long
	// stretch on one thing, such as a project that is not a task, is
	// deliberate and is left alone.
	OffPlanDistracted = 15 * time.Minute
	OffPlanFocused    = 0.6
)

// Sent is an earlier message, or an earlier decision to stay quiet.
type Sent struct {
	Kind      string
	At        time.Time
	Delivered bool
}

// MorningDue reports whether the morning message should go out now: the
// owner woke today, not long ago, and has not had one today.
func MorningDue(now time.Time, wake *time.Time, sent []Sent, location *time.Location) bool {
	if wake == nil || now.Before(*wake) || now.Sub(*wake) > MorningWithin {
		return false
	}
	today := now.In(location).Format("2006-01-02")
	if wake.In(location).Format("2006-01-02") != today {
		return false
	}
	for _, earlier := range sent {
		if earlier.Kind == KindMorning && earlier.At.In(location).Format("2006-01-02") == today {
			return false
		}
	}
	return true
}

// Stretch is the last OffPlanWindow of the owner's day.
type Stretch struct {
	// Active is time at a device; OnTasks is how much of it went to tasks.
	Active, OnTasks time.Duration
	// Away means nothing recent is on screen.
	Away bool
	// Excused means the owner said what is on screen is a one-off.
	Excused bool
	// AgentRunning means a task's agent is at work, so being elsewhere
	// fills a wait.
	AgentRunning bool
	// Leisure means the owner set this time aside: a distraction time, or a
	// planned block that is not for a task.
	Leisure bool
	// OpenTasks is how many tasks the day has.
	OpenTasks int
	// Distracted is time on things labelled "distraction"; Largest is the
	// time on the one thing, not a distraction, that took the most.
	Distracted, Largest time.Duration
}

// Drifting reports whether the stretch looks like drift rather than
// deliberate work on something that is not one of the day's tasks.
func (s Stretch) Drifting() bool {
	return s.Distracted >= OffPlanDistracted || s.Largest.Seconds() < OffPlanFocused*s.Active.Seconds()
}

// OffPlanDue reports whether the assistant should look at a long stretch
// outside the day's tasks.
func OffPlanDue(now time.Time, stretch Stretch, sent []Sent) bool {
	if stretch.OpenTasks == 0 || stretch.Away || stretch.Excused || stretch.AgentRunning || stretch.Leisure || stretch.OnTasks > 0 || stretch.Active < OffPlanActive ||
		!stretch.Drifting() {
		return false
	}
	today := 0
	for _, earlier := range sent {
		if earlier.Kind != KindCollection && now.Sub(earlier.At) < Between {
			return false
		}
		if earlier.Kind == KindOffPlan && earlier.Delivered {
			if now.Sub(earlier.At) < OffPlanRepeat {
				return false
			}
			if now.Sub(earlier.At) < 24*time.Hour {
				today++
			}
		}
	}
	return today < OffPlanPerDay
}

const rules = "You are the owner's assistant, writing to them on Telegram without being asked. " +
	"Their executive function does not work reliably, so you carry the remembering, timing and sequencing. " +
	"Use only the facts below; do not add any. Do not ask them to recall anything. State facts and the way forward; never scold, praise or moralise. " +
	"Short, plain sentences, no headings, no lists longer than three lines. Change nothing in the system now; only write. " +
	"Answer with the message text alone."

// MorningPrompt asks for the message that starts the owner's day.
func MorningPrompt(facts string) string {
	return rules + "\n\nThey just woke up. Tell them what the records say about yesterday in two or three lines, what is waiting, " +
		"and ask what today is for, offering the most likely one to three things from the facts. " +
		"If the day is locked, say in one line that everything opens once the day is planned with you.\n\nFacts:\n" + facts
}

// OffPlanPrompt asks whether to write after a stretch of drift outside the
// day's tasks, and what.
func OffPlanPrompt(facts string) string {
	return rules + "\n\nThey have drifted for a while: on distractions, or jumping between unrelated things, and on none of today's tasks. Decide whether a message helps. " +
		"If the plan says they are resting or the facts show nothing they need to get back to, answer exactly NOTHING. " +
		"Look forward, not back: do not say how long it has been or count minutes. " +
		"If an agent is waiting for them, name that task and the next step there. Otherwise, if today has no plan, offer to agree on one to three things for today with them. " +
		"Otherwise offer the next smallest concrete step of one task, or to make today's plan smaller. " +
		"Never repeat the suggestion of their last check-in; if there is nothing new to offer, answer exactly NOTHING. " +
		"They may be avoiding a task they dread: do not say so; make the step small enough to start. Two sentences at most.\n\nFacts:\n" + facts
}

// Problem is a collection problem the owner is told about: one that stops
// tracking or blocking and that they, or the assistant on their word, can
// fix. A machine going quiet is not one: it may be asleep. Grace is how long
// it must last first.
type Problem struct {
	Grace   time.Duration
	Message func(device, since string, detail map[string]any) string
}

func detailText(detail map[string]any, key string) string {
	value, _ := detail[key].(string)
	if len(value) > 300 {
		value = value[:300]
	}
	return value
}

func t3Broke(what string) func(string, string, map[string]any) string {
	return func(device, since string, detail map[string]any) string {
		return "Telemetry can no longer read " + what + " on " + device + " (since " + since + "). T3 Code probably changed it in an update. " +
			"Until it is fixed, T3 Code time is guessed from your messages.\nError: " + detailText(detail, "error")
	}
}

// Problems by incident kind. The phone going quiet is told only while the
// owner is using the Mac (see PhoneSilentWhileActive): then it is not off.
var Problems = map[string]Problem{
	"module_failing:t3":      {CollectionGrace, t3Broke("T3 Code's database")},
	"module_failing:t3-open": {CollectionGrace, t3Broke("which project T3 Code has open")},
	"module_failing:blocker": {time.Hour, func(device, since string, detail map[string]any) string {
		return "The Mac has not been able to get blocking decisions from the server since " + since + ", so blocking there may not follow today's rules.\nError: " +
			detailText(detail, "error")
	}},
	"titles_unreadable": {CollectionGrace, func(device, since string, _ map[string]any) string {
		return "Telemetry on the Mac cannot read window titles since " + since + ": its Accessibility permission is off. " +
			"Turn on Telemetry Desktop in System Settings → Privacy & Security → Accessibility."
	}},
	"window_sampling_stopped": {CollectionGrace, func(device, since string, _ map[string]any) string {
		return "Telemetry on the Mac is running but has not recorded which window is in front since " + since + ". Restarting the Mac app should fix it."
	}},
	"silent": {CollectionGrace, func(device, since string, _ map[string]any) string {
		return "Your phone has sent nothing to Telemetry since " + since + " while you were using the Mac, so tracking and blocking on the phone are probably not running. " +
			"Open the Telemetry app on the phone once to restart it."
	}},
	"disk_low": {CollectionGrace, func(_, _ string, detail map[string]any) string {
		free, _ := detail["free_bytes"].(float64)
		return fmt.Sprintf("The server's disk has %.1f GB free. When it fills, the database stops and nothing more is recorded. Ask me to look at what can be removed.", free/(1<<30))
	}},
}

// PhoneSilentWhileActive is how much Mac use during the phone's silence
// shows it is not simply switched off for the night.
const PhoneSilentWhileActive = 20 * time.Minute

// A collection problem is told only once it has lasted its grace (by default
// CollectionGrace), so a module that recovers by itself says nothing, and
// the same problem on the same machine is told at most once per
// CollectionRepeat. An incident closes while its machine sleeps or loses its
// network and opens again when it returns; that is still the same problem.
const (
	CollectionGrace  = 30 * time.Minute
	CollectionRepeat = 24 * time.Hour
)

// Told is a collection message already delivered.
type Told struct {
	Facts string
	At    time.Time
}

// CollectionMarker is logged as the facts of a collection message.
func CollectionMarker(id int64, device, kind string) string {
	return fmt.Sprintf("incident %d %s %s", id, device, kind)
}

// CollectionDue reports whether an open incident should be told now.
func CollectionDue(id int64, device, kind string, started, now time.Time, told []Told) bool {
	problem, watched := Problems[kind]
	if !watched || now.Sub(started) < problem.Grace {
		return false
	}
	for _, entry := range told {
		fields := strings.Fields(entry.Facts)
		if len(fields) < 2 || fields[0] != "incident" {
			continue
		}
		if fields[1] == strconv.FormatInt(id, 10) {
			return false
		}
		if len(fields) == 4 && fields[2] == device && fields[3] == kind && now.Sub(entry.At) < CollectionRepeat {
			return false
		}
	}
	return true
}

// Message cleans a composed answer. It is empty when the assistant chose to
// stay quiet.
func Message(answer string) string {
	answer = strings.TrimSpace(answer)
	if strings.EqualFold(strings.Trim(answer, ".\"' "), "NOTHING") {
		return ""
	}
	return answer
}

// Compose runs the command with the prompt as its last argument and returns
// what it printed.
func Compose(ctx context.Context, command []string, prompt string) (string, error) {
	if len(command) == 0 {
		return "", errors.New("no compose command is configured")
	}
	output, err := exec.CommandContext(ctx, command[0], append(append([]string{}, command[1:]...), prompt)...).Output()
	if err != nil {
		return "", fmt.Errorf("compose command: %w", err)
	}
	return string(output), nil
}

// Send runs the command with the message on its standard input.
func Send(ctx context.Context, command []string, message string) error {
	if len(command) == 0 {
		return errors.New("no send command is configured")
	}
	send := exec.CommandContext(ctx, command[0], command[1:]...)
	send.Stdin = strings.NewReader(message)
	if output, err := send.CombinedOutput(); err != nil {
		return fmt.Errorf("send command: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
