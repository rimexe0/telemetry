package main

import (
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/silence"
)

func TestWithoutRetiredLeavesOutOnlyNamedMachines(t *testing.T) {
	desktops := []silence.DesktopStatus{{DeviceID: "fedora"}, {DeviceID: "mac-1"}, {DeviceID: "catchy"}}
	kept := withoutRetired(desktops, "fedora, old-laptop")
	if len(kept) != 2 || kept[0].DeviceID != "mac-1" || kept[1].DeviceID != "catchy" {
		t.Fatalf("kept = %+v", kept)
	}
	if len(desktops) != 3 || desktops[0].DeviceID != "fedora" {
		t.Fatalf("the input changed: %+v", desktops)
	}
	if got := withoutRetired(desktops, ""); len(got) != 3 {
		t.Fatalf("nothing retired, kept = %+v", got)
	}
}

// A sleeping Mac answers in the minute it wakes for now and then; that is
// not a Mac that is on with its collector stopped (2026-11-15).
func TestABriefAnswerFromASleepingMacIsNotOn(t *testing.T) {
	answers := steadyAnswers{}
	start := time.Date(2026, 11, 15, 5, 19, 0, 0, time.UTC)
	if answers.on("mac", true, start) {
		t.Fatal("one answer counted as on")
	}
	if answers.on("mac", false, start.Add(time.Minute)) {
		t.Fatal("a missed answer counted as on")
	}
	if answers.on("mac", true, start.Add(16*time.Minute)) || answers.on("mac", true, start.Add(25*time.Minute)) {
		t.Fatal("the count did not start again after a miss")
	}
	if !answers.on("mac", true, start.Add(26*time.Minute)) {
		t.Fatal("ten minutes of answers did not count as on")
	}
}
