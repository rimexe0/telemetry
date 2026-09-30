package policy

import (
	"testing"
	"time"
)

func TestDistractionLabelsBlockDuringWork(t *testing.T) {
	config := WithLabels(Config{}, []LabeledTarget{
		{Kind: "app", Match: "com.twitter.android", Label: "distraction"},
		{Kind: "site", Match: "x.com", Label: "Distraction"},
		{Kind: "app", Match: "osu!", Label: "distraction"},
		{Kind: "app", Match: "tw.nekomimi.nekogram", Label: "essential"},
		{Kind: "app", Match: "T3 Code", Label: "work"},
	})
	if err := config.Normalize(); err != nil {
		t.Fatal(err)
	}
	if len(config.Distractions) != 3 || len(config.Essentials) != 1 {
		t.Fatalf("distractions %+v essentials %+v", config.Distractions, config.Essentials)
	}
	start := time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)
	decision := Evaluate(config, 1, Facts{PlanExists: true, PlanDate: "2026-09-30", Blocks: []Block{{ID: 1, Title: "Write", Start: start, End: start.Add(time.Hour), Kind: BlockKindWork, State: "planned"}}}, start.Add(time.Minute))
	blocked := map[string]bool{}
	for _, pkg := range decision.Blocked.AndroidPackages {
		blocked[pkg] = true
	}
	if !blocked["com.twitter.android"] || blocked["tw.nekomimi.nekogram"] {
		t.Fatalf("decision = %+v", decision)
	}
}
