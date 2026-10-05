package outreach

import (
	"context"
	"testing"
	"time"
)

func TestMorningIsSentOnceSoonAfterWaking(t *testing.T) {
	istanbul, _ := time.LoadLocation("Europe/Istanbul")
	at := func(day, hour, minute int) time.Time { return time.Date(2026, 10, day, hour, minute, 0, 0, istanbul) }
	wake := at(3, 8, 26)
	if !MorningDue(at(3, 8, 28), &wake, nil, istanbul) {
		t.Fatal("just woke: due")
	}
	if MorningDue(at(3, 8, 28), nil, nil, istanbul) {
		t.Fatal("no wake known: not due")
	}
	if MorningDue(at(3, 11, 0), &wake, nil, istanbul) {
		t.Fatal("hours later it is no longer the morning message")
	}
	if MorningDue(at(3, 8, 40), &wake, []Sent{{Kind: KindMorning, At: at(3, 8, 28)}}, istanbul) {
		t.Fatal("already sent today")
	}
	// Yesterday's message, and a nap later today, do not count or repeat.
	if !MorningDue(at(3, 8, 28), &wake, []Sent{{Kind: KindMorning, At: at(2, 10, 30)}}, istanbul) {
		t.Fatal("yesterday's message must not stop today's")
	}
	nap := at(3, 17, 30)
	if MorningDue(at(3, 17, 31), &nap, []Sent{{Kind: KindMorning, At: at(3, 8, 28)}}, istanbul) {
		t.Fatal("waking from a nap is not a second morning")
	}
	yesterday := at(2, 8, 26)
	if MorningDue(at(3, 0, 30), &yesterday, nil, istanbul) {
		t.Fatal("up past midnight is not waking")
	}
}

func TestOffPlanNeedsALongStretchAndRespectsTheCap(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	off := Stretch{Active: 40 * time.Minute, OpenTasks: 2}
	if !OffPlanDue(now, off, nil) {
		t.Fatal("40 minutes on nothing planned: due")
	}
	for name, stretch := range map[string]Stretch{
		"some task time":   {Active: 40 * time.Minute, OnTasks: 5 * time.Minute, OpenTasks: 2},
		"mostly away":      {Active: 10 * time.Minute, OpenTasks: 2},
		"away now":         {Active: 40 * time.Minute, OpenTasks: 2, Away: true},
		"excused":          {Active: 40 * time.Minute, OpenTasks: 2, Excused: true},
		"waiting on agent": {Active: 40 * time.Minute, OpenTasks: 2, AgentRunning: true},
		"no tasks today":   {Active: 40 * time.Minute},
		"time set aside":   {Active: 40 * time.Minute, OpenTasks: 2, Leisure: true},
	} {
		if OffPlanDue(now, stretch, nil) {
			t.Fatalf("%s: not due", name)
		}
	}
	if OffPlanDue(now, off, []Sent{{Kind: KindMorning, At: now.Add(-30 * time.Minute)}}) {
		t.Fatal("one unasked message an hour, whatever its kind")
	}
	if !OffPlanDue(now, off, []Sent{{Kind: KindCollection, At: now.Add(-time.Minute)}}) {
		t.Fatal("a notice about collection does not count against the cap")
	}
	if !OffPlanDue(now, off, []Sent{{Kind: KindOffPlan, At: now.Add(-61 * time.Minute)}}) {
		t.Fatal("an hour later: due again")
	}
}

func TestMessageAndCommands(t *testing.T) {
	if Message(" NOTHING.\n") != "" || Message("\"nothing\"") != "" {
		t.Fatal("NOTHING means stay quiet")
	}
	if got := Message("\nGood morning.\n"); got != "Good morning." {
		t.Fatalf("message = %q", got)
	}
	answer, err := Compose(context.Background(), []string{"printf", "%s"}, "hello")
	if err != nil || answer != "hello" {
		t.Fatalf("compose = %q, %v", answer, err)
	}
	if err := Send(context.Background(), []string{"sh", "-c", "test \"$(cat)\" = '-dash first'"}, "-dash first"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := Send(context.Background(), []string{"false"}, "x"); err == nil {
		t.Fatal("a failing send must be reported")
	}
}

// The Mac sleeping without saying so closes the incident and waking opens a
// new one; that is the same problem and is told once.
func TestCollectionProblemIsToldOnce(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	kind := "module_failing:t3-open"
	if CollectionDue(1, "mac", kind, now.Add(-20*time.Minute), now, nil) {
		t.Fatal("a problem that may still fix itself is not told yet")
	}
	if !CollectionDue(1, "mac", kind, now.Add(-40*time.Minute), now, nil) {
		t.Fatal("a lasting problem is told")
	}
	told := []Told{{Facts: CollectionMarker(1, "mac", kind), At: now.Add(-3 * time.Hour)}}
	if CollectionDue(1, "mac", kind, now.Add(-4*time.Hour), now, told) {
		t.Fatal("the same incident is told once")
	}
	if CollectionDue(2, "mac", kind, now.Add(-time.Hour), now, told) {
		t.Fatal("the same problem reopened after sleep is not told again")
	}
	if !CollectionDue(2, "fedora", kind, now.Add(-time.Hour), now, told) {
		t.Fatal("another machine is another problem")
	}
	if !CollectionDue(2, "mac", kind, now.Add(-time.Hour), now.Add(25*time.Hour), told) {
		t.Fatal("a problem still there a day later is told again")
	}
	if CollectionDue(7, "mac", kind, now.Add(-time.Hour), now, []Told{{Facts: "incident 7", At: now}}) {
		t.Fatal("messages logged before the device and kind were recorded still count")
	}
}
