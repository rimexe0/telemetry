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
)

// Sent is an earlier message, or an earlier decision to stay quiet.
type Sent struct {
	Kind string
	At   time.Time
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
}

// OffPlanDue reports whether the assistant should look at a long stretch
// outside the day's tasks.
func OffPlanDue(now time.Time, stretch Stretch, sent []Sent) bool {
	if stretch.OpenTasks == 0 || stretch.Away || stretch.Excused || stretch.AgentRunning || stretch.Leisure || stretch.OnTasks > 0 || stretch.Active < OffPlanActive {
		return false
	}
	for _, earlier := range sent {
		if earlier.Kind != KindCollection && now.Sub(earlier.At) < Between {
			return false
		}
	}
	return true
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

// OffPlanPrompt asks whether to write after a long stretch outside the
// day's tasks, and what.
func OffPlanPrompt(facts string) string {
	return rules + "\n\nThey have been at their devices for a while on none of today's tasks. Decide whether a message helps. " +
		"If the plan says they are resting or the facts show nothing they need to get back to, answer exactly NOTHING. " +
		"Otherwise say how long it has been, and offer the next smallest concrete step of one task, or to make today's plan smaller. " +
		"They may be avoiding a task they dread: do not say so; make the step small enough to start. Two sentences at most.\n\nFacts:\n" + facts
}

// Watched are the collector modules that read T3 Code's internal formats;
// the owner asked to be told when an update breaks them.
var Watched = map[string]string{
	"module_failing:t3":      "T3 Code's database",
	"module_failing:t3-open": "which project T3 Code has open",
}

// A collection problem is told only once it has lasted CollectionGrace, so
// a module that recovers by itself says nothing, and the same problem on the
// same machine is told at most once per CollectionRepeat. An incident closes
// while its machine sleeps or loses its network and opens again when it
// returns; that is still the same problem.
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
	if now.Sub(started) < CollectionGrace {
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

// CollectionMessage tells the owner that reading T3 Code broke.
func CollectionMessage(what, device, since, problem string) string {
	if len(problem) > 300 {
		problem = problem[:300]
	}
	return "Telemetry can no longer read " + what + " on " + device + " (since " + since + "). T3 Code probably changed it in an update. " +
		"Until it is fixed, T3 Code time is guessed from your messages.\nError: " + problem
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
