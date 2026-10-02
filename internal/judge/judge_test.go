package judge

import (
	"context"
	"strings"
	"testing"
)

func TestParseTakesTheLastVerdictLine(t *testing.T) {
	verdict, err := Parse("thinking...\n{\"verdict\":\"Unrelated\",\"reason\":\"Shopping for shoes\"}\n")
	if err != nil || verdict.Verdict != Unrelated || verdict.Reason != "Shopping for shoes" {
		t.Fatalf("verdict = %+v, %v", verdict, err)
	}
	if wrapped, err := Parse("Here you go: {\"verdict\":\"unsure\",\"reason\":\"no title\"} ."); err != nil || wrapped.Verdict != Unsure {
		t.Fatalf("wrapped = %+v, %v", wrapped, err)
	}
	for _, bad := range []string{"", "it is related", `{"verdict":"maybe"}`} {
		if _, err := Parse(bad); err == nil {
			t.Fatalf("%q must not parse", bad)
		}
	}
}

func TestPromptCarriesTheTaskAndWhatItKnows(t *testing.T) {
	prompt := Prompt(Question{Task: "Science fair presentation", NextAction: "Outline the three sections", Related: []string{"docs.google.com"},
		Unrelated: []string{"trendyol.com"}, Context: "ChatGPT", Title: "Slide ideas for a science centre"})
	for _, want := range []string{"Task: Science fair presentation", "Next step of the task: Outline the three sections", "part of this task: docs.google.com",
		"NOT to be part of this task: trendyol.com", "On screen now: ChatGPT", "Window or page title: Slide ideas for a science centre", "unfamiliar is NOT thereby unrelated"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, prompt)
		}
	}
}

func TestAskRunsTheCommandWithThePromptLast(t *testing.T) {
	// The stand-in judge answers "related" when the prompt mentions the task.
	script := `case "$1" in *"Task: Slides"*) echo '{"verdict":"related","reason":"mentions the task"}';; *) echo '{"verdict":"unsure","reason":"?"}';; esac`
	verdict, err := Ask(context.Background(), []string{"sh", "-c", script, "judge"}, Question{Task: "Slides", Context: "Keynote"})
	if err != nil || verdict.Verdict != Related {
		t.Fatalf("verdict = %+v, %v", verdict, err)
	}
	if _, err := Ask(context.Background(), nil, Question{}); err == nil {
		t.Fatal("no command must be an error")
	}
	if _, err := Ask(context.Background(), []string{"sh", "-c", "exit 3"}, Question{}); err == nil {
		t.Fatal("a failing command must be an error")
	}
}

func TestLinkPromptAndParse(t *testing.T) {
	prompt := LinkPrompt([]TaskOption{{ID: 3, Title: "Dad's album", Notes: "monthly collage from WhatsApp exports"}, {ID: 4, Title: "Science fair presentation"}},
		"ChatGPT · WhatsApp Chat - Group · Create July session PDF", "")
	for _, want := range []string{"- id 3: Dad's album (monthly collage from WhatsApp exports)", "- id 4: Science fair presentation", "On screen: ChatGPT · WhatsApp Chat - Group", "Never guess"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, prompt)
		}
	}
	link, err := ParseLink(`{"task_id":3,"confidence":"High","reason":"WhatsApp export is the album source"}`)
	if err != nil || link.TaskID == nil || *link.TaskID != 3 || link.Confidence != "high" {
		t.Fatalf("link = %+v, %v", link, err)
	}
	none, err := ParseLink("sure:\n{\"task_id\":null,\"confidence\":\"low\",\"reason\":\"a file manager\"}")
	if err != nil || none.TaskID != nil {
		t.Fatalf("none = %+v, %v", none, err)
	}
	if _, err := ParseLink(`{"verdict":"related"}`); err == nil {
		t.Fatal("an answer without task_id must not parse")
	}
	answered, err := AskLink(context.Background(), []string{"sh", "-c", `echo '{"task_id":4,"confidence":"high","reason":"slides"}'`, "judge"}, nil, "Keynote", "")
	if err != nil || *answered.TaskID != 4 {
		t.Fatalf("asked = %+v, %v", answered, err)
	}
}
