package policy

import (
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScenarios(t *testing.T) {
	files, err := filepath.Glob("testdata/scenarios/*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("no scenarios: %v", err)
	}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		scenario, err := ParseScenario(raw)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		t.Run(scenario.Name, func(t *testing.T) {
			result := RunScenario(scenario)
			if result.Error != "" {
				t.Fatal(result.Error)
			}
			for _, item := range result.Results {
				if !item.Pass {
					t.Errorf("at %s: %v", item.At, item.Failures)
				}
			}
		})
	}
}

func testConfig() Config {
	config := Config{
		Distractions: []TargetGroup{{ID: "social", AndroidPackages: []string{"com.example.feed", "com.example.shared"}, Domains: []string{"Feed.Example.com"}}},
		Essentials:   []TargetGroup{{ID: "calls", AndroidPackages: []string{"com.example.dialer", "com.example.shared"}}},
	}
	if err := config.Normalize(); err != nil {
		panic(err)
	}
	return config
}

func TestEssentialIdentifiersAreNeverBlocked(t *testing.T) {
	start := time.Date(2026, 9, 28, 6, 0, 0, 0, time.UTC)
	decision := Evaluate(testConfig(), 3, Facts{PlanExists: true, Blocks: []Block{{ID: 1, Title: "Work", Start: start, End: start.Add(time.Hour), Kind: BlockKindWork, State: "planned"}}}, start.Add(time.Minute))
	if decision.Mode != ModeWork || len(decision.Blocked.AndroidPackages) != 1 || decision.Blocked.AndroidPackages[0] != "com.example.feed" {
		t.Fatalf("decision = %+v", decision)
	}
	if decision.Blocked.Domains[0] != "feed.example.com" || decision.PolicyVersion != 3 {
		t.Fatalf("domains/version = %+v", decision)
	}
}

func TestGatesNeedAnEnd(t *testing.T) {
	now := time.Now()
	if err := (Facts{Gates: []Gate{{ID: "g", ActiveFrom: now}}}).Validate(); err == nil {
		t.Fatal("gate without an end was accepted")
	}
}

func TestConfigValidation(t *testing.T) {
	for _, config := range []Config{
		{Timezone: "Mars/Olympus"},
		{EmergencyMinutes: 500},
		{PlanGate: PlanGateConfig{EndsAt: "25:00"}},
		{Distractions: []TargetGroup{{ID: "a"}, {ID: "a"}}},
		{Distractions: []TargetGroup{{ID: "a", Domains: []string{"https://x.com"}}}},
	} {
		if err := config.Normalize(); err == nil {
			t.Fatalf("accepted %+v", config)
		}
	}
}

func TestTimelineMergesAndCoversTheWindow(t *testing.T) {
	start := time.Date(2026, 9, 28, 6, 0, 0, 0, time.UTC)
	facts := Facts{PlanExists: true, Blocks: []Block{
		{ID: 1, Title: "A", Start: start.Add(time.Hour), End: start.Add(2 * time.Hour), Kind: BlockKindWork, State: "planned"},
		{ID: 2, Title: "Lunch", Start: start.Add(2 * time.Hour), End: start.Add(3 * time.Hour), Kind: BlockKindFree, State: "planned"},
	}}
	segments := Timeline(testConfig(), 1, facts, start, start.Add(6*time.Hour))
	modes := []string{}
	for i, segment := range segments {
		modes = append(modes, segment.Decision.Mode)
		if i > 0 && !segment.From.Equal(segments[i-1].To) {
			t.Fatalf("gap between segments %d and %d", i-1, i)
		}
	}
	if len(segments) != 3 || modes[0] != ModeLeisure || modes[1] != ModeWork || modes[2] != ModeLeisure {
		t.Fatalf("modes = %v", modes)
	}
	if !segments[0].From.Equal(start) || !segments[2].To.Equal(start.Add(6*time.Hour)) {
		t.Fatal("timeline does not cover the window")
	}
}

// Randomised days: whatever the plan looks like, these must always hold.
func TestPropertiesOverRandomDays(t *testing.T) {
	config := testConfig()
	random := rand.New(rand.NewSource(20260927))
	day := time.Date(2026, 9, 28, 5, 0, 0, 0, time.UTC)
	for run := 0; run < 2000; run++ {
		facts := Facts{PlanExists: random.Intn(5) > 0}
		cursor := day.Add(time.Duration(random.Intn(180)) * time.Minute)
		for i := 0; i < random.Intn(6); i++ {
			length := time.Duration(15+random.Intn(120)) * time.Minute
			kind := BlockKindWork
			if random.Intn(3) == 0 {
				kind = BlockKindFree
			}
			state := []string{"planned", "active", "done", "skipped"}[random.Intn(4)]
			facts.Blocks = append(facts.Blocks, Block{ID: int64(i + 1), Title: "B", Start: cursor, End: cursor.Add(length), Kind: kind, State: state})
			cursor = cursor.Add(length + time.Duration(random.Intn(60))*time.Minute)
		}
		if random.Intn(3) == 0 {
			from := day.Add(time.Duration(random.Intn(600)) * time.Minute)
			facts.Gates = append(facts.Gates, Gate{ID: "g", Label: "Gate", ActiveFrom: from, Until: from.Add(time.Duration(1+random.Intn(180)) * time.Minute), Satisfied: random.Intn(2) == 0})
		}
		if random.Intn(4) == 0 {
			started := day.Add(time.Duration(random.Intn(600)) * time.Minute)
			facts.Emergency = &Emergency{StartedAt: started, Until: started.Add(10 * time.Minute)}
		}
		lastEnd := day
		for _, block := range facts.Blocks {
			if block.End.After(lastEnd) {
				lastEnd = block.End
			}
		}
		for minute := 0; minute < 24*60; minute += 7 {
			at := day.Add(time.Duration(minute) * time.Minute)
			decision := Evaluate(config, 1, facts, at)
			if decision.Reason == "" {
				t.Fatalf("run %d: empty reason at %s", run, at)
			}
			for _, pkg := range decision.Blocked.AndroidPackages {
				if pkg == "com.example.dialer" || pkg == "com.example.shared" {
					t.Fatalf("run %d: essential %s blocked at %s", run, pkg, at)
				}
			}
			if facts.Emergency != nil && !at.Before(facts.Emergency.StartedAt) && at.Before(facts.Emergency.Until) && (decision.BlockAll || len(decision.BlockedGroups) > 0) {
				t.Fatalf("run %d: blocked during emergency at %s", run, at)
			}
			for _, gate := range facts.Gates {
				if decision.Mode == ModeGate && !at.Before(gate.Until) {
					t.Fatalf("run %d: gate still locked after its end at %s", run, at)
				}
			}
			// Leisure must be free: after the last block, with a plan and no
			// gate, urgent or emergency, nothing is blocked.
			gated := false
			for _, gate := range facts.Gates {
				gated = gated || (!gate.Satisfied && !at.Before(gate.ActiveFrom) && at.Before(gate.Until))
			}
			if facts.PlanExists && !gated && !at.Before(lastEnd) && (decision.BlockAll || len(decision.BlockedGroups) > 0) {
				t.Fatalf("run %d: blocked after the day's work ended at %s: %s", run, at, decision.Reason)
			}
		}
		// The timeline agrees with point evaluation everywhere.
		segments := Timeline(config, 1, facts, day, day.Add(24*time.Hour))
		for _, segment := range segments {
			probe := segment.From.Add(segment.To.Sub(segment.From) / 2)
			if Evaluate(config, 1, facts, probe).Mode != segment.Decision.Mode {
				t.Fatalf("run %d: timeline disagrees with evaluation at %s", run, probe)
			}
		}
	}
}

func TestRunnerReportsWrongExpectations(t *testing.T) {
	scenario := Scenario{Name: "wrong", Day: "2026-09-28",
		Start:  ScenarioState{PlanExists: true, Blocks: []ScenarioBlock{{ID: 1, Title: "Work", Start: "09:00", End: "10:00"}}},
		Config: Config{Distractions: []TargetGroup{{ID: "social"}}},
		Expect: []Expectation{{At: "09:30", Mode: ModeLeisure, NothingBlocked: true}}}
	result := RunScenario(scenario)
	if result.Pass || len(result.Results[0].Failures) != 2 {
		t.Fatalf("wrong expectation passed: %+v", result)
	}
}
