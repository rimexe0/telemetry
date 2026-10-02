// Package judge asks an assistant whether something on screen is part of a
// task, when the task's own memory and rules cannot tell. The assistant is
// any command that takes a prompt as its last argument and prints an answer:
// Hermes in one-shot mode, or another agent CLI.
package judge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

const (
	Related   = "related"
	Unrelated = "unrelated"
	Unsure    = "unsure"
)

// Question is what the judge is shown. It carries titles and names only,
// never page or message contents.
type Question struct {
	Task       string
	Notes      string
	NextAction string
	// Related and Unrelated are what the task has already learned.
	Related   []string
	Unrelated []string
	// Context is the app or site, with its project and thread for a coding
	// agent; Title is the window or page title.
	Context string
	Title   string
}

type Verdict struct {
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
}

// Prompt words the question. It asks for "unsure" whenever the judge cannot
// tell: this owner does tasks in their own and often unexpected way, so
// something unfamiliar is not thereby unrelated.
func Prompt(question Question) string {
	lines := []string{
		"You judge whether what is on a person's computer screen is part of the one task they chose to focus on right now.",
		"This person does tasks in their own, often unexpected way, and uses general tools (chat assistants, search, odd sites) for almost anything.",
		"So: something unfamiliar is NOT thereby unrelated. Say \"unrelated\" only when it clearly has nothing to do with the task. Say \"unsure\" whenever you cannot tell; they will then be asked.",
		"",
		"Task: " + question.Task,
	}
	if question.Notes != "" {
		lines = append(lines, "Task notes: "+question.Notes)
	}
	if question.NextAction != "" {
		lines = append(lines, "Next step of the task: "+question.NextAction)
	}
	if len(question.Related) > 0 {
		lines = append(lines, "Already known to be part of this task: "+strings.Join(question.Related, "; "))
	}
	if len(question.Unrelated) > 0 {
		lines = append(lines, "Already known NOT to be part of this task: "+strings.Join(question.Unrelated, "; "))
	}
	lines = append(lines, "", "On screen now: "+question.Context)
	if question.Title != "" {
		lines = append(lines, "Window or page title: "+question.Title)
	}
	lines = append(lines, "", "Do not use any tools. Answer with exactly one line of JSON and nothing else:",
		`{"verdict":"related|unrelated|unsure","reason":"a few words"}`)
	return strings.Join(lines, "\n")
}

// Parse reads the verdict from the judge's output: the last line that holds
// a JSON object with a known verdict.
func Parse(output string) (Verdict, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		start, end := strings.Index(line, "{"), strings.LastIndex(line, "}")
		if start < 0 || end <= start {
			continue
		}
		var verdict Verdict
		if json.Unmarshal([]byte(line[start:end+1]), &verdict) != nil {
			continue
		}
		verdict.Verdict = strings.ToLower(strings.TrimSpace(verdict.Verdict))
		if verdict.Verdict == Related || verdict.Verdict == Unrelated || verdict.Verdict == Unsure {
			return verdict, nil
		}
	}
	return Verdict{}, errors.New("the judge gave no verdict")
}

// Ask runs the command with the prompt as its last argument.
func Ask(ctx context.Context, command []string, question Question) (Verdict, error) {
	if len(command) == 0 {
		return Verdict{}, errors.New("no judge command is configured")
	}
	arguments := append(append([]string{}, command[1:]...), Prompt(question))
	output, err := exec.CommandContext(ctx, command[0], arguments...).Output()
	if err != nil {
		return Verdict{}, fmt.Errorf("judge command: %w", err)
	}
	return Parse(string(output))
}

// TaskOption is one of the owner's open tasks, as shown to the judge.
type TaskOption struct {
	ID      int64
	Title   string
	Notes   string
	Related []string
}

// Link is the judge's answer to "which task is this part of". TaskID is nil
// when it is none of them or the judge cannot tell.
type Link struct {
	TaskID     *int64 `json:"task_id"`
	Confidence string `json:"confidence"`
	Reason     string `json:"reason"`
}

// LinkPrompt asks which of the owner's tasks, if any, something on screen
// belongs to. It asks for null unless the match is clear, and says that a
// shared tool is not a match: a wrong link counts unrelated time as work,
// which is worse than no link. context is "app · project · thread" for a
// coding agent, or an app or site.
func LinkPrompt(tasks []TaskOption, context, title string) string {
	lines := []string{
		"A person has a few tasks for today. Decide which task, if any, the thing on their screen belongs to.",
		"The same apps and tools (T3 Code, ChatGPT, Codex, a browser, an editor) are used for every task and for things that are no task at all.",
		"A tool's name appearing in a task's title is NOT a match. Match only when the project, document or subject on screen is clearly the task's own.",
		"Most things belong to no task: then answer null. If you are not sure, answer null with confidence \"low\". Never guess.",
		"",
		"Tasks:",
	}
	for _, task := range tasks {
		line := fmt.Sprintf("- id %d: %s", task.ID, task.Title)
		if task.Notes != "" {
			line += " (" + task.Notes + ")"
		}
		if len(task.Related) > 0 {
			line += " [already known to include: " + strings.Join(task.Related, "; ") + "]"
		}
		lines = append(lines, line)
	}
	lines = append(lines, "")
	// Name the tool apart from what it is being used on.
	if parts := strings.SplitN(context, " · ", 3); len(parts) >= 2 {
		lines = append(lines, "Tool in use (not evidence by itself): "+parts[0], "Project open in it: "+parts[1])
		if len(parts) == 3 {
			lines = append(lines, "Thread title: "+parts[2])
		}
	} else {
		lines = append(lines, "On screen: "+context)
	}
	if title != "" {
		lines = append(lines, "Window or page title: "+title)
	}
	lines = append(lines, "", "Do not use any tools. Answer with exactly one line of JSON and nothing else:",
		`{"task_id":<id or null>,"confidence":"high|low","reason":"a few words"}`)
	return strings.Join(lines, "\n")
}

// ParseLink reads the judge's link from the last JSON line of its output.
func ParseLink(output string) (Link, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		start, end := strings.Index(line, "{"), strings.LastIndex(line, "}")
		if start < 0 || end <= start || !strings.Contains(line, "task_id") {
			continue
		}
		var link Link
		if json.Unmarshal([]byte(line[start:end+1]), &link) != nil {
			continue
		}
		link.Confidence = strings.ToLower(strings.TrimSpace(link.Confidence))
		return link, nil
	}
	return Link{}, errors.New("the judge gave no answer")
}

// AskLink runs the command with the link prompt as its last argument.
func AskLink(ctx context.Context, command []string, tasks []TaskOption, onScreen, title string) (Link, error) {
	if len(command) == 0 {
		return Link{}, errors.New("no judge command is configured")
	}
	arguments := append(append([]string{}, command[1:]...), LinkPrompt(tasks, onScreen, title))
	output, err := exec.CommandContext(ctx, command[0], arguments...).Output()
	if err != nil {
		return Link{}, fmt.Errorf("judge command: %w", err)
	}
	return ParseLink(string(output))
}
