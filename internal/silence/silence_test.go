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
		if got := Evaluate(Evidence{Desktop: &DesktopStatus{At: *ago(300), State: state}}, now); len(got) != 0 {
			t.Fatalf("%s: %v", state, got)
		}
	}
	got := kinds(Evaluate(Evidence{Desktop: &DesktopStatus{At: *ago(11), State: "running", TitlesReadable: true, LastWindowAt: ago(11)}}, now))
	if !got["mac/silent"] || len(got) != 1 {
		t.Fatalf("unannounced silence must be reported alone: %v", got)
	}
}

func TestRunningCollectorThatCannotSeeIsReported(t *testing.T) {
	got := kinds(Evaluate(Evidence{Desktop: &DesktopStatus{At: *ago(1), State: "running", TitlesReadable: false, LastWindowAt: ago(9)}}, now))
	if !got["mac/titles_unreadable"] || !got["mac/window_sampling_stopped"] {
		t.Fatalf("got %v", got)
	}
	healthy := Evaluate(Evidence{Desktop: &DesktopStatus{At: *ago(1), State: "running", TitlesReadable: true, LastWindowAt: ago(1)}}, now)
	if len(healthy) != 0 {
		t.Fatalf("healthy collector: %v", healthy)
	}
}
