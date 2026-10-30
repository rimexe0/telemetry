package policy

import (
	"strings"
	"testing"
	"time"
)

func closedConfig() Config {
	config := testConfig()
	config.ClosedByDefault = true
	config.DistractionTimes = []Window{{Start: "20:00", End: "22:00"}, {Start: "23:30", End: "00:30"}}
	config.OpenAddresses = []string{"https://www.youtube.com/watch?v=rFZHOHl-L8A"}
	return config
}

func TestClosedByDefaultOpensOnlyInDistractionTime(t *testing.T) {
	config := closedConfig()
	if err := config.Normalize(); err != nil {
		t.Fatal(err)
	}
	if config.OpenAddresses[0] != "youtube.com/watch?v=rFZHOHl-L8A" {
		t.Fatalf("open address = %q", config.OpenAddresses[0])
	}
	istanbul, _ := time.LoadLocation("Europe/Istanbul")
	at := func(hour, minute int) time.Time { return time.Date(2026, 10, 2, hour, minute, 0, 0, istanbul) }
	// No plan and no block: closed during the day.
	closed := Evaluate(config, 1, Facts{}, at(11, 0))
	if closed.Mode != ModeClosed || len(closed.BlockedGroups) == 0 || closed.Reason != "Distractions are closed. Distraction time starts at 20:00." ||
		len(closed.OpenAddresses) != 1 {
		t.Fatalf("daytime = %+v", closed)
	}
	open := Evaluate(config, 1, Facts{}, at(20, 30))
	if open.Mode != ModeLeisure || len(open.BlockedGroups) != 0 || open.Reason != "Distraction time until 22:00." {
		t.Fatalf("distraction time = %+v", open)
	}
	// A window across midnight holds on both sides of it.
	late := Evaluate(config, 1, Facts{}, at(23, 45))
	early := Evaluate(config, 1, Facts{}, at(0, 15))
	if late.Mode != ModeLeisure || late.Reason != "Distraction time until tomorrow 00:30." || early.Mode != ModeLeisure || early.Reason != "Distraction time until 00:30." {
		t.Fatalf("across midnight = %q, %q", late.Reason, early.Reason)
	}
	if after := Evaluate(config, 1, Facts{}, at(22, 0)); after.Mode != ModeClosed || after.Reason != "Distractions are closed. Distraction time starts at 23:30." {
		t.Fatalf("after the window = %+v", after)
	}
	// A work block wins over distraction time.
	work := Evaluate(config, 1, Facts{Blocks: []Block{{ID: 1, Title: "Album", Start: at(20, 0), End: at(21, 0), Kind: BlockKindWork, State: "planned"}}}, at(20, 30))
	if work.Mode != ModeWork || len(work.BlockedGroups) == 0 {
		t.Fatalf("work block in distraction time = %+v", work)
	}
	// Without the switch, a day with nothing planned still keeps
	// distractions closed; a planned day with no work block is free.
	config.ClosedByDefault = false
	if closed := Evaluate(config, 1, Facts{}, at(11, 0)); closed.Mode != ModeClosed || len(closed.BlockedGroups) == 0 {
		t.Fatalf("switch off, no plan = %+v", closed)
	}
	if closed := Evaluate(config, 1, Facts{PlanExists: true, PlanEmpty: true}, at(11, 0)); closed.Mode != ModeClosed || len(closed.BlockedGroups) == 0 {
		t.Fatalf("switch off, empty plan = %+v", closed)
	}
	planned := Facts{PlanExists: true, Blocks: []Block{{ID: 1, Title: "Album", Start: at(9, 0), End: at(10, 0), Kind: BlockKindWork, State: "done"}}}
	if free := Evaluate(config, 1, planned, at(11, 0)); free.Mode != ModeLeisure || len(free.BlockedGroups) != 0 {
		t.Fatalf("switch off, planned = %+v", free)
	}
}

func TestClosedTimelineChangesAtWindowEdgesAndMidnight(t *testing.T) {
	config := closedConfig()
	if err := config.Normalize(); err != nil {
		t.Fatal(err)
	}
	istanbul, _ := time.LoadLocation("Europe/Istanbul")
	from := time.Date(2026, 10, 2, 19, 0, 0, 0, istanbul)
	segments := Timeline(config, 1, Facts{}, from, from.Add(8*time.Hour))
	got := []string{}
	for _, segment := range segments {
		got = append(got, segment.From.In(istanbul).Format("15:04")+" "+segment.Decision.Mode)
	}
	want := "19:00 closed|20:00 leisure|22:00 closed|23:30 leisure|00:00 leisure|00:30 closed"
	if strings.Join(got, "|") != want {
		t.Fatalf("timeline = %v", got)
	}
}

func TestABlockAcrossMidnightSaysTomorrow(t *testing.T) {
	config := testConfig()
	istanbul, _ := time.LoadLocation("Europe/Istanbul")
	start := time.Date(2026, 10, 2, 10, 35, 0, 0, istanbul)
	facts := Facts{Blocks: []Block{{ID: 3, Title: "Rest", Start: start, End: start.Add(24 * time.Hour), Kind: BlockKindWork, State: "planned"}}}
	if today := Evaluate(config, 1, facts, start.Add(time.Hour)); today.Reason != "Working on Rest. Until tomorrow 10:35." {
		t.Fatalf("today = %q", today.Reason)
	}
	if tomorrow := Evaluate(config, 1, facts, start.Add(20*time.Hour)); tomorrow.Mode != ModeWork || tomorrow.Reason != "Working on Rest. Until 10:35." {
		t.Fatalf("after midnight = %+v", tomorrow)
	}
}

func TestOpenAddressesAreValidatedAndMatched(t *testing.T) {
	config := testConfig()
	config.OpenAddresses = []string{"youtube.com"}
	if err := config.Normalize(); err == nil {
		t.Fatal("a bare site must be refused as an open address")
	}
	config.DistractionTimes, config.OpenAddresses = []Window{{Start: "20:00", End: "20:00"}}, nil
	if err := config.Normalize(); err == nil {
		t.Fatal("a window with no length must be refused")
	}
	open := []string{"youtube.com/watch?v=rFZHOHl-L8A"}
	if !AddressOpen(open, "https://www.youtube.com/watch?v=rFZHOHl-L8A&t=12") || AddressOpen(open, "https://www.youtube.com/watch?v=other") || AddressOpen(open, "https://www.youtube.com/") {
		t.Fatal("only the named page stays open")
	}
}

func TestOpeningsLiftOnlyWhatTheyName(t *testing.T) {
	config := closedConfig()
	config.Distractions = append(config.Distractions, TargetGroup{ID: "video", Domains: []string{"video.example.com"}})
	if err := config.Normalize(); err != nil {
		t.Fatal(err)
	}
	istanbul, _ := time.LoadLocation("Europe/Istanbul")
	at := time.Date(2026, 10, 2, 11, 0, 0, 0, istanbul)
	facts := Facts{Openings: []Opening{{ID: 4, Purpose: "a tutorial for work", GroupIDs: []string{"video"}, Until: at.Add(20 * time.Minute)}}}
	during := Evaluate(config, 1, facts, at)
	if during.Mode != ModeClosed || len(during.BlockedGroups) != 1 || during.BlockedGroups[0] != "social" || len(during.Blocked.Domains) != 1 ||
		!strings.Contains(during.Reason, "Opened until 11:20 for: a tutorial for work.") {
		t.Fatalf("during the opening = %+v", during)
	}
	if after := Evaluate(config, 1, facts, at.Add(20*time.Minute)); len(after.BlockedGroups) != 2 || strings.Contains(after.Reason, "Opened") {
		t.Fatalf("after the opening = %+v", after)
	}
	// In distraction time nothing is blocked, so an opening changes nothing.
	free := Evaluate(config, 1, Facts{Openings: []Opening{{ID: 5, Purpose: "x", GroupIDs: []string{"video"}, Until: at.Add(12 * time.Hour)}}}, at.Add(9*time.Hour+30*time.Minute))
	if free.Mode != ModeLeisure || strings.Contains(free.Reason, "Opened") {
		t.Fatalf("in distraction time = %+v", free)
	}
	all := Evaluate(config, 1, Facts{Openings: []Opening{{ID: 6, Purpose: "everything", All: true, Until: at.Add(time.Minute)}}}, at)
	if len(all.BlockedGroups) != 0 || len(all.Blocked.Domains) != 0 || len(all.Blocked.AndroidPackages) != 0 {
		t.Fatalf("open everything = %+v", all)
	}
}

func TestPlanGateFromWakeLocksEverythingUntilPlanned(t *testing.T) {
	config := testConfig()
	config.PlanGate = PlanGateConfig{Enabled: true, FromWake: true, LockAll: true}
	istanbul, _ := time.LoadLocation("Europe/Istanbul")
	at := func(hour, minute int) time.Time { return time.Date(2026, 10, 3, hour, minute, 0, 0, istanbul) }
	wake := at(8, 26)
	// Up past midnight, not yet slept: yesterday's wake does not lock today.
	yesterday := at(8, 26).AddDate(0, 0, -1)
	if got := Evaluate(config, 1, Facts{WakeAt: &yesterday}, at(0, 30)); got.Mode != ModeClosed || got.BlockAll {
		t.Fatalf("after midnight, before sleeping = %+v", got)
	}
	// No known wake: nothing is locked on missing evidence.
	if got := Evaluate(config, 1, Facts{}, at(9, 0)); got.Mode != ModeClosed || got.BlockAll {
		t.Fatalf("no wake known = %+v", got)
	}
	locked := Evaluate(config, 1, Facts{WakeAt: &wake}, at(8, 30))
	if locked.Mode != ModePlanGate || !locked.BlockAll || len(locked.AlwaysAllowed.AndroidPackages) == 0 {
		t.Fatalf("after waking, no plan = %+v", locked)
	}
	if got := Evaluate(config, 1, Facts{WakeAt: &wake, PlanExists: true}, at(8, 40)); got.Mode != ModeLeisure || got.BlockAll {
		t.Fatalf("planned = %+v", got)
	}
	// The timeline changes at the wake time.
	segments := Timeline(config, 1, Facts{WakeAt: &wake}, at(8, 0), at(9, 0))
	if len(segments) != 2 || segments[0].Decision.Mode != ModeClosed || segments[1].Decision.Mode != ModePlanGate || !segments[1].From.Equal(wake) {
		t.Fatalf("timeline = %+v", segments)
	}
	// Switched on at 10:30 on a day already begun without a plan: nothing
	// locks until the next waking.
	since := at(10, 30)
	config.PlanGate.Since = &since
	if got := Evaluate(config, 1, Facts{WakeAt: &wake}, at(10, 31)); got.Mode != ModeClosed || got.BlockAll {
		t.Fatalf("switched on after waking = %+v", got)
	}
	tomorrow := wake.AddDate(0, 0, 1)
	if got := Evaluate(config, 1, Facts{WakeAt: &tomorrow}, tomorrow.Add(time.Minute)); got.Mode != ModePlanGate || !got.BlockAll {
		t.Fatalf("the next morning = %+v", got)
	}
	// An emergency unlock from the phone opens even the morning lock, for its minutes only.
	emergency := &Emergency{StartedAt: tomorrow.Add(5 * time.Minute), Until: tomorrow.Add(15 * time.Minute)}
	if got := Evaluate(config, 1, Facts{WakeAt: &tomorrow, Emergency: emergency}, tomorrow.Add(6*time.Minute)); got.Mode != ModeEmergency || got.BlockAll {
		t.Fatalf("emergency = %+v", got)
	}
	if got := Evaluate(config, 1, Facts{WakeAt: &tomorrow, Emergency: emergency}, tomorrow.Add(16*time.Minute)); got.Mode != ModePlanGate {
		t.Fatalf("after the emergency = %+v", got)
	}
}
