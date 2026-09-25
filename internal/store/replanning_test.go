package store

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func at(clock string) time.Time {
	parsed, err := time.Parse(time.RFC3339, "2026-09-25T"+clock+":00+03:00")
	if err != nil {
		panic(err)
	}
	return parsed
}

func ptr[T any](value T) *T { return &value }

func basePlan(blocks ...PlanBlock) Plan {
	for i := range blocks {
		blocks[i].Position = i
		if blocks[i].State == "" {
			blocks[i].State = "planned"
		}
	}
	return Plan{PlanDate: "2026-09-25", VersionID: 10, Blocks: blocks}
}

func startsByID(plan ProposedPlan) map[int64]string {
	starts := map[int64]string{}
	for _, block := range plan.Blocks {
		if block.SourceBlockID != nil {
			starts[*block.SourceBlockID] = block.StartTime.In(at("00:00").Location()).Format("15:04")
		}
	}
	return starts
}

func kinds(changes []PlanChange) map[int64]string {
	result := map[int64]string{}
	for _, change := range changes {
		if change.BaseBlockID != nil && change.Kind != ChangeFixedConflict {
			result[*change.BaseBlockID] = change.Kind
		}
	}
	return result
}

func TestDisruptionKeepsFixedBlocksAndShiftsMovableWorkInOrder(t *testing.T) {
	base := basePlan(
		PlanBlock{ID: 1, Title: "Deep work", StartTime: at("09:00"), DurationMinutes: 60},
		PlanBlock{ID: 2, Title: "Standup", StartTime: at("10:30"), DurationMinutes: 30, Fixed: true},
		PlanBlock{ID: 3, Title: "Review", StartTime: at("11:00"), DurationMinutes: 45},
		PlanBlock{ID: 4, Title: "Email", StartTime: at("13:00"), DurationMinutes: 30},
	)
	// A 09:15-10:00 appointment interrupts deep work.
	plan, changes, err := DisruptionReplan(base, DisruptionInput{Start: at("09:15"), End: ptr(at("10:00")), AvailableUntil: ptr(at("18:00"))})
	if err != nil {
		t.Fatal(err)
	}
	// Deep work cannot fit 10:00-11:00 across the fixed standup, so it moves
	// after it; review keeps its order after deep work; email is untouched.
	want := map[int64]string{1: "11:00", 2: "10:30", 3: "12:00", 4: "13:00"}
	if got := startsByID(plan); !reflect.DeepEqual(got, want) {
		t.Fatalf("starts = %v, want %v", got, want)
	}
	if got := kinds(changes); !reflect.DeepEqual(got, map[int64]string{1: ChangeMoved, 2: ChangeUnchanged, 3: ChangeMoved, 4: ChangeUnchanged}) {
		t.Fatalf("kinds = %v", got)
	}
	for _, block := range plan.Blocks {
		if *block.SourceBlockID == 1 && block.DurationMinutes != 60 {
			t.Fatalf("duration changed: %d", block.DurationMinutes)
		}
	}
}

func TestDisruptionParksWorkThatCannotFitInsteadOfShorteningIt(t *testing.T) {
	base := basePlan(
		PlanBlock{ID: 1, Title: "Write", StartTime: at("14:00"), DurationMinutes: 90},
		PlanBlock{ID: 2, Title: "Call", StartTime: at("16:00"), DurationMinutes: 30, Fixed: true},
		PlanBlock{ID: 3, Title: "Read", StartTime: at("16:30"), DurationMinutes: 30},
	)
	plan, changes, err := DisruptionReplan(base, DisruptionInput{Start: at("14:00"), End: ptr(at("15:00")), AvailableUntil: ptr(at("17:00"))})
	if err != nil {
		t.Fatal(err)
	}
	// Writing needs 90 minutes: 15:00 crosses the call and 16:30 crosses
	// available_until, so it is parked. Reading can still start at 16:30.
	if got := startsByID(plan); !reflect.DeepEqual(got, map[int64]string{2: "16:00", 3: "16:30"}) {
		t.Fatalf("starts = %v", got)
	}
	var parked *PlanChange
	for i := range changes {
		if changes[i].Kind == ChangeParked {
			parked = &changes[i]
		}
	}
	if parked == nil || *parked.BaseBlockID != 1 || *parked.OldDuration != 90 || parked.Detail == "" {
		t.Fatalf("parked change = %+v", parked)
	}
}

func TestDisruptionRequiresAvailableUntil(t *testing.T) {
	_, _, err := DisruptionReplan(basePlan(PlanBlock{ID: 1, Title: "Work", StartTime: at("09:00"), DurationMinutes: 30}), DisruptionInput{Start: at("09:00")})
	if !errors.Is(err, ErrInvalidProposal) {
		t.Fatalf("err = %v", err)
	}
}

func TestDisruptionKeepsFinishedBlocksAndTheirState(t *testing.T) {
	base := basePlan(
		PlanBlock{ID: 1, Title: "Done early", StartTime: at("10:00"), DurationMinutes: 30, State: "done"},
		PlanBlock{ID: 2, Title: "In progress", StartTime: at("10:00"), DurationMinutes: 60, State: "active"},
		PlanBlock{ID: 3, Title: "Earlier leftover", StartTime: at("08:00"), DurationMinutes: 30},
	)
	plan, _, err := DisruptionReplan(base, DisruptionInput{Start: at("10:20"), End: ptr(at("10:40")), AvailableUntil: ptr(at("20:00"))})
	if err != nil {
		t.Fatal(err)
	}
	states := map[int64]string{}
	for _, block := range plan.Blocks {
		states[*block.SourceBlockID] = block.State
	}
	if !reflect.DeepEqual(states, map[int64]string{1: "done", 2: "planned", 3: "planned"}) {
		t.Fatalf("states = %v", states)
	}
	if got := startsByID(plan); got[1] != "10:00" || got[2] != "10:40" || got[3] != "08:00" {
		t.Fatalf("starts = %v", got)
	}
}

func TestDiffPlanIsDeterministicAndFlagsFixedConflicts(t *testing.T) {
	base := basePlan(
		PlanBlock{ID: 1, Title: "Gym", StartTime: at("07:00"), DurationMinutes: 60, Fixed: true},
		PlanBlock{ID: 2, Title: "Write", StartTime: at("09:00"), DurationMinutes: 60},
		PlanBlock{ID: 3, Title: "Dentist", StartTime: at("12:00"), DurationMinutes: 60, Fixed: true},
	)
	proposed := ProposedPlan{Blocks: []ProposedBlock{
		{SourceBlockID: ptr[int64](2), Title: "Write", StartTime: at("09:00"), DurationMinutes: 90, State: "planned"},
		{SourceBlockID: ptr[int64](3), Title: "Dentist", StartTime: at("12:30"), DurationMinutes: 60, Fixed: true, State: "planned"},
		{Title: "Shopping", StartTime: at("15:00"), DurationMinutes: 30, State: "planned"},
	}}
	first := DiffPlan(base, proposed)
	if !reflect.DeepEqual(first, DiffPlan(base, proposed)) {
		t.Fatal("diff is not deterministic")
	}
	got := []string{}
	for _, change := range first {
		got = append(got, change.Kind+":"+change.Title)
	}
	want := []string{"parked:Gym", "resized:Write", "moved:Dentist", "added:Shopping", "fixed_conflict:Gym", "fixed_conflict:Dentist"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changes = %v, want %v", got, want)
	}
}

func TestPrepareRejectsStaleBaseAndUnknownSources(t *testing.T) {
	base := basePlan(PlanBlock{ID: 1, Title: "Work", StartTime: at("09:00"), DurationMinutes: 30})
	_, _, _, err := PreparePlanChange(base, PlanChangeRequest{BasePlanVersionID: 9, Reason: "late", Disruption: &DisruptionInput{Start: at("09:00"), AvailableUntil: ptr(at("12:00"))}})
	if !errors.Is(err, ErrProposalStale) {
		t.Fatalf("stale err = %v", err)
	}
	_, _, _, err = PreparePlanChange(base, PlanChangeRequest{BasePlanVersionID: 10, Reason: "edit", Plan: &ProposedPlan{Blocks: []ProposedBlock{{SourceBlockID: ptr[int64](99), Title: "X", StartTime: at("09:00"), DurationMinutes: 30}}}})
	if !errors.Is(err, ErrInvalidProposal) {
		t.Fatalf("source err = %v", err)
	}
	_, _, _, err = PreparePlanChange(base, PlanChangeRequest{BasePlanVersionID: 10, Reason: "both"})
	if !errors.Is(err, ErrInvalidProposal) {
		t.Fatalf("mode err = %v", err)
	}
}

func TestContentHashChangesWithContent(t *testing.T) {
	plan := ProposedPlan{Blocks: []ProposedBlock{{Title: "A", StartTime: at("09:00"), DurationMinutes: 30, State: "planned"}}}
	first, _ := ProposalContentHash("2026-09-25", 10, plan)
	same, _ := ProposalContentHash("2026-09-25", 10, plan)
	plan.Blocks[0].DurationMinutes = 31
	edited, _ := ProposalContentHash("2026-09-25", 10, plan)
	otherBase, _ := ProposalContentHash("2026-09-25", 11, ProposedPlan{Blocks: []ProposedBlock{{Title: "A", StartTime: at("09:00"), DurationMinutes: 30, State: "planned"}}})
	if first != same || first == edited || first == otherBase {
		t.Fatalf("hashes %s %s %s %s", first, same, edited, otherBase)
	}
}

func TestResumeWarningsReportMissingMarkersOnly(t *testing.T) {
	changes := []PlanChange{{Kind: ChangeMoved, BaseBlockID: ptr[int64](1), Title: "Write"}, {Kind: ChangeUnchanged, BaseBlockID: ptr[int64](2), Title: "Read"}}
	sessions := []ExecutionSession{
		{ID: 5, ActivityID: 1, PlanBlockID: ptr[int64](1)},
		{ID: 6, ActivityID: 2, PlanBlockID: ptr[int64](2)},
	}
	warnings := ResumeWarnings(changes, sessions, map[int64]Activity{})
	if len(warnings) != 1 || *warnings[0].SessionID != 5 || warnings[0].Code != "missing_resume_marker" {
		t.Fatalf("warnings = %+v", warnings)
	}
	if got := ResumeWarnings(changes, sessions, map[int64]Activity{1: {ResumeMarker: "section 2"}}); len(got) != 0 {
		t.Fatalf("activity marker should satisfy: %+v", got)
	}
}

func TestReplannedBlockStillMatchesItsExecutionSession(t *testing.T) {
	original := int64(11)
	session := ExecutionSession{ActivityID: 1, PlanBlockID: &original}
	if !primaryMatchesBlock(session, PlanBlock{ID: 21, SourceBlockID: &original}) {
		t.Fatal("continued block does not match the session")
	}
	if primaryMatchesBlock(session, PlanBlock{ID: 22}) {
		t.Fatal("unrelated block matched")
	}
}
