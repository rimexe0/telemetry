package policy

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Scenario is a scripted day for the simulator. Times are "HH:MM" on Day in
// the config timezone, or RFC 3339.
type Scenario struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Day         string          `json:"day"`
	Config      Config          `json:"config"`
	Start       ScenarioState   `json:"start"`
	Events      []ScenarioEvent `json:"events,omitempty"`
	Expect      []Expectation   `json:"expect"`
}

type ScenarioBlock struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Start string `json:"start"`
	End   string `json:"end"`
	Kind  string `json:"kind"`
	State string `json:"state,omitempty"`
}

type ScenarioGate struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	From  string `json:"from"`
	Until string `json:"until"`
}

type ScenarioState struct {
	PlanExists bool            `json:"plan_exists"`
	Blocks     []ScenarioBlock `json:"blocks,omitempty"`
	Gates      []ScenarioGate  `json:"gates,omitempty"`
}

type ScenarioEvent struct {
	At             string          `json:"at"`
	Note           string          `json:"note,omitempty"`
	Execution      *Execution      `json:"execution,omitempty"`
	ClearExecution bool            `json:"clear_execution,omitempty"`
	SatisfyGate    string          `json:"satisfy_gate,omitempty"`
	AddGate        *ScenarioGate   `json:"add_gate,omitempty"`
	Urgent         *ScenarioUrgent `json:"urgent,omitempty"`
	ClearUrgent    bool            `json:"clear_urgent,omitempty"`
	Emergency      bool            `json:"emergency,omitempty"`
	BreakStart     bool            `json:"break_start,omitempty"`
	BreakEnd       bool            `json:"break_end,omitempty"`
	BlockState     *BlockState     `json:"block_state,omitempty"`
	PlanSaved      []ScenarioBlock `json:"plan_saved,omitempty"`
}

type ScenarioUrgent struct {
	Purpose         string   `json:"purpose"`
	AllowedGroupIDs []string `json:"allowed_group_ids,omitempty"`
	Until           string   `json:"until"`
}

type BlockState struct {
	ID    int64  `json:"id"`
	State string `json:"state"`
}

type Expectation struct {
	At             string   `json:"at"`
	Mode           string   `json:"mode"`
	BlockAll       *bool    `json:"block_all,omitempty"`
	Blocked        []string `json:"blocked,omitempty"`
	NotBlocked     []string `json:"not_blocked,omitempty"`
	NothingBlocked bool     `json:"nothing_blocked,omitempty"`
	ReasonContains string   `json:"reason_contains,omitempty"`
}

type ExpectationResult struct {
	At       string   `json:"at"`
	Pass     bool     `json:"pass"`
	Failures []string `json:"failures,omitempty"`
	Decision Decision `json:"decision"`
}

type ScenarioResult struct {
	Name    string              `json:"name"`
	Pass    bool                `json:"pass"`
	Error   string              `json:"error,omitempty"`
	Results []ExpectationResult `json:"results"`
}

func ParseScenario(raw []byte) (Scenario, error) {
	var scenario Scenario
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&scenario); err != nil {
		return Scenario{}, err
	}
	return scenario, nil
}

type scenarioClock struct {
	day      string
	location *time.Location
}

func (c scenarioClock) parse(value string) (time.Time, error) {
	if len(value) == 5 && value[2] == ':' {
		return time.ParseInLocation("2006-01-02 15:04", c.day+" "+value, c.location)
	}
	return time.Parse(time.RFC3339, value)
}

// RunScenario applies events in time order and checks each expectation
// against the facts as they stand at that moment.
func RunScenario(scenario Scenario) ScenarioResult {
	result := ScenarioResult{Name: scenario.Name, Results: []ExpectationResult{}}
	fail := func(err error) ScenarioResult {
		result.Error = err.Error()
		return result
	}
	config := scenario.Config
	if err := config.Normalize(); err != nil {
		return fail(err)
	}
	location, _ := time.LoadLocation(config.Timezone)
	clock := scenarioClock{day: scenario.Day, location: location}
	base, err := scenarioFacts(clock, scenario.Start)
	if err != nil {
		return fail(err)
	}
	type timedEvent struct {
		at    time.Time
		event ScenarioEvent
	}
	events := make([]timedEvent, 0, len(scenario.Events))
	for _, event := range scenario.Events {
		at, err := clock.parse(event.At)
		if err != nil {
			return fail(fmt.Errorf("event at %q: %w", event.At, err))
		}
		events = append(events, timedEvent{at, event})
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].at.Before(events[j].at) })

	result.Pass = true
	for _, expectation := range scenario.Expect {
		at, err := clock.parse(expectation.At)
		if err != nil {
			return fail(fmt.Errorf("expectation at %q: %w", expectation.At, err))
		}
		facts := cloneFacts(base)
		for _, item := range events {
			if item.at.After(at) {
				break
			}
			if err := applyEvent(clock, config, &facts, item.at, item.event); err != nil {
				return fail(fmt.Errorf("event at %s: %w", item.event.At, err))
			}
		}
		if err := facts.Validate(); err != nil {
			return fail(err)
		}
		decision := Evaluate(config, 1, facts, at)
		checked := ExpectationResult{At: expectation.At, Pass: true, Decision: decision}
		for _, failure := range checkExpectation(expectation, decision) {
			checked.Pass = false
			checked.Failures = append(checked.Failures, failure)
		}
		if !checked.Pass {
			result.Pass = false
		}
		result.Results = append(result.Results, checked)
	}
	return result
}

func scenarioFacts(clock scenarioClock, state ScenarioState) (Facts, error) {
	facts := Facts{PlanExists: state.PlanExists, PlanDate: clock.day}
	blocks, err := scenarioBlocks(clock, state.Blocks)
	if err != nil {
		return Facts{}, err
	}
	facts.Blocks = blocks
	for _, gate := range state.Gates {
		parsed, err := scenarioGate(clock, gate)
		if err != nil {
			return Facts{}, err
		}
		facts.Gates = append(facts.Gates, parsed)
	}
	return facts, nil
}

func scenarioBlocks(clock scenarioClock, blocks []ScenarioBlock) ([]Block, error) {
	result := []Block{}
	for _, block := range blocks {
		start, err := clock.parse(block.Start)
		if err != nil {
			return nil, err
		}
		end, err := clock.parse(block.End)
		if err != nil {
			return nil, err
		}
		state := block.State
		if state == "" {
			state = "planned"
		}
		kind := block.Kind
		if kind == "" {
			kind = BlockKindWork
		}
		result = append(result, Block{ID: block.ID, Title: block.Title, Start: start, End: end, Kind: kind, State: state})
	}
	return result, nil
}

func scenarioGate(clock scenarioClock, gate ScenarioGate) (Gate, error) {
	from, err := clock.parse(gate.From)
	if err != nil {
		return Gate{}, err
	}
	until, err := clock.parse(gate.Until)
	if err != nil {
		return Gate{}, err
	}
	return Gate{ID: gate.ID, Label: gate.Label, ActiveFrom: from, Until: until}, nil
}

func applyEvent(clock scenarioClock, config Config, facts *Facts, at time.Time, event ScenarioEvent) error {
	if event.Execution != nil {
		execution := *event.Execution
		facts.Execution = &execution
	}
	if event.ClearExecution {
		facts.Execution = nil
	}
	if event.SatisfyGate != "" {
		found := false
		for i := range facts.Gates {
			if facts.Gates[i].ID == event.SatisfyGate {
				facts.Gates[i].Satisfied = true
				found = true
			}
		}
		if !found {
			return fmt.Errorf("unknown gate %q", event.SatisfyGate)
		}
	}
	if event.AddGate != nil {
		gate, err := scenarioGate(clock, *event.AddGate)
		if err != nil {
			return err
		}
		facts.Gates = append(facts.Gates, gate)
	}
	if event.Urgent != nil {
		until, err := clock.parse(event.Urgent.Until)
		if err != nil {
			return err
		}
		facts.Urgent = &Urgent{Purpose: event.Urgent.Purpose, AllowedGroupIDs: event.Urgent.AllowedGroupIDs, Until: until}
	}
	if event.ClearUrgent {
		facts.Urgent = nil
	}
	if event.Emergency {
		facts.Emergency = &Emergency{StartedAt: at, Until: at.Add(time.Duration(config.EmergencyMinutes) * time.Minute)}
	}
	if event.BreakStart {
		since := at
		facts.BreakSince = &since
	}
	if event.BreakEnd {
		facts.BreakSince = nil
	}
	if event.BlockState != nil {
		found := false
		for i := range facts.Blocks {
			if facts.Blocks[i].ID == event.BlockState.ID {
				facts.Blocks[i].State = event.BlockState.State
				found = true
			}
		}
		if !found {
			return fmt.Errorf("unknown block %d", event.BlockState.ID)
		}
	}
	if event.PlanSaved != nil {
		blocks, err := scenarioBlocks(clock, event.PlanSaved)
		if err != nil {
			return err
		}
		facts.PlanExists = true
		facts.Blocks = blocks
	}
	return nil
}

func cloneFacts(facts Facts) Facts {
	clone := facts
	clone.Blocks = append([]Block(nil), facts.Blocks...)
	clone.Gates = append([]Gate(nil), facts.Gates...)
	return clone
}

func checkExpectation(expectation Expectation, decision Decision) []string {
	failures := []string{}
	if expectation.Mode != "" && decision.Mode != expectation.Mode {
		failures = append(failures, fmt.Sprintf("mode = %s, want %s (%s)", decision.Mode, expectation.Mode, decision.Reason))
	}
	if expectation.BlockAll != nil && decision.BlockAll != *expectation.BlockAll {
		failures = append(failures, fmt.Sprintf("block_all = %t, want %t", decision.BlockAll, *expectation.BlockAll))
	}
	blocked := map[string]bool{}
	for _, id := range decision.BlockedGroups {
		blocked[id] = true
	}
	for _, id := range expectation.Blocked {
		if !blocked[id] {
			failures = append(failures, fmt.Sprintf("%s is not blocked", id))
		}
	}
	for _, id := range expectation.NotBlocked {
		if blocked[id] {
			failures = append(failures, fmt.Sprintf("%s is blocked", id))
		}
	}
	if expectation.NothingBlocked && (decision.BlockAll || len(decision.BlockedGroups) > 0) {
		failures = append(failures, fmt.Sprintf("something is blocked: %v block_all=%t", decision.BlockedGroups, decision.BlockAll))
	}
	if expectation.ReasonContains != "" && !strings.Contains(decision.Reason, expectation.ReasonContains) {
		failures = append(failures, fmt.Sprintf("reason %q does not contain %q", decision.Reason, expectation.ReasonContains))
	}
	if decision.Reason == "" || decision.PolicyVersion == 0 {
		failures = append(failures, "decision has no reason or policy version")
	}
	return failures
}
