package main

import (
	"testing"

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
