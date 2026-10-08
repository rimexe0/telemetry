package silence

import (
	"testing"
	"time"
)

var now = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

func ago(minutes int) *time.Time {
	at := now.Add(-time.Duration(minutes) * time.Minute)
	return &at
}

func kinds(findings []Finding) map[string]bool {
	result := map[string]bool{}
	for _, finding := range findings {
		result[finding.Device+"/"+finding.Kind] = true
	}
	return result
}

func mac(state string, at int, window *WindowStatus) DesktopStatus {
	return DesktopStatus{DeviceID: "mac-1", Name: "plagiarism", At: *ago(at), State: state, Window: window}
}

func TestPhoneSilenceUsesReceiptTime(t *testing.T) {
	if got := kinds(Evaluate(Evidence{PhoneLastReceived: ago(5)}, now)); len(got) != 0 {
		t.Fatalf("5 minutes is normal: %v", got)
	}
	if got := kinds(Evaluate(Evidence{PhoneLastReceived: ago(20)}, now)); !got["phone/silent"] {
		t.Fatalf("20 minutes must be reported: %v", got)
	}
	if got := Evaluate(Evidence{}, now); len(got) != 0 {
		t.Fatalf("a phone that never reported is not expected: %v", got)
	}
}

func TestAnnouncedSleepIsNotAProblem(t *testing.T) {
	for _, state := range []string{"sleeping", "stopping"} {
		if got := Evaluate(Evidence{Desktops: []DesktopStatus{mac(state, 300, nil)}}, now); len(got) != 0 {
			t.Fatalf("%s: %v", state, got)
		}
	}
	got := Evaluate(Evidence{Desktops: []DesktopStatus{mac("running", 11, &WindowStatus{TitlesReadable: true, LastWindowAt: ago(11)})}}, now)
	if len(got) != 1 || got[0].Device != "mac-1" || got[0].Kind != KindSilent || got[0].Detail["device_name"] != "plagiarism" {
		t.Fatalf("unannounced silence must be reported alone: %+v", got)
	}
}

func TestEachDeviceIsJudgedOnItsOwn(t *testing.T) {
	server := DesktopStatus{DeviceID: "datalix", Name: "vps1", At: *ago(1), State: "running"}
	got := kinds(Evaluate(Evidence{Desktops: []DesktopStatus{server, mac("running", 12, &WindowStatus{TitlesReadable: true, LastWindowAt: ago(12)})}}, now))
	if !got["mac-1/silent"] || len(got) != 1 {
		t.Fatalf("a headless server without the window module must not be judged on windows: %v", got)
	}
}

func TestRunningCollectorThatCannotSeeIsReported(t *testing.T) {
	got := kinds(Evaluate(Evidence{Desktops: []DesktopStatus{mac("running", 1, &WindowStatus{TitlesReadable: false, LastWindowAt: ago(9)})}}, now))
	if !got["mac-1/titles_unreadable"] || !got["mac-1/window_sampling_stopped"] {
		t.Fatalf("got %v", got)
	}
	healthy := Evaluate(Evidence{Desktops: []DesktopStatus{mac("running", 1, &WindowStatus{TitlesReadable: true, LastWindowAt: ago(1)})}}, now)
	if len(healthy) != 0 {
		t.Fatalf("healthy collector: %v", healthy)
	}
}

func TestFailingModuleIsReportedAfterAWhile(t *testing.T) {
	status := DesktopStatus{DeviceID: "datalix", Name: "vps1", At: *ago(1), State: "running",
		Modules: map[string]ModuleStatus{"t3": {Error: "schema changed", ErrorSince: ago(3)}}}
	if got := Evaluate(Evidence{Desktops: []DesktopStatus{status}}, now); len(got) != 0 {
		t.Fatalf("a brief failure is not an incident: %v", got)
	}
	status.Modules["t3"] = ModuleStatus{Error: "schema changed", ErrorSince: ago(12)}
	if got := kinds(Evaluate(Evidence{Desktops: []DesktopStatus{status}}, now)); !got["datalix/module_failing:t3"] {
		t.Fatalf("got %v", got)
	}
}

func TestStartupStatusDoesNotJudgeModules(t *testing.T) {
	if got := Evaluate(Evidence{Desktops: []DesktopStatus{mac("started", 0, &WindowStatus{})}}, now); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestServerDiskNearlyFull(t *testing.T) {
	now := time.Date(2026, 10, 6, 2, 0, 0, 0, time.UTC)
	plenty, low := uint64(9<<30), uint64(1<<30)
	if findings := Evaluate(Evidence{ServerFreeBytes: &plenty}, now); len(findings) != 0 {
		t.Fatalf("9 GB free is fine: %+v", findings)
	}
	findings := Evaluate(Evidence{ServerFreeBytes: &low}, now)
	if len(findings) != 1 || findings[0].Device != DeviceServer || findings[0].Kind != KindDiskLow {
		t.Fatalf("1 GB free = %+v", findings)
	}
	full := uint64(100 << 20)
	if findings := Evaluate(Evidence{ServerFreeBytes: &full}, now); len(findings) != 2 || findings[0].Kind != KindDiskFull {
		t.Fatalf("100 MB free = %+v", findings)
	}
}
