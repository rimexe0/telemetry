// Package silence decides when a collection source has gone quiet in a way
// that is a problem, as opposed to a device that is asleep or was stopped on
// purpose. It only reports collection problems; silence is never evidence of
// what the owner did.
package silence

import "time"

const (
	DeviceMac   = "mac"
	DevicePhone = "phone"

	KindSilent           = "silent"
	KindTitlesUnreadable = "titles_unreadable"
	KindWindowStopped    = "window_sampling_stopped"
)

// Thresholds are deliberately above each source's normal cadence: the phone
// delivers every minute and the desktop collector reports every minute.
const (
	PhoneSilentAfter   = 15 * time.Minute
	DesktopSilentAfter = 10 * time.Minute
	WindowStaleAfter   = 5 * time.Minute
)

// DesktopStatus is the latest telemetry.collector_status from the Mac.
type DesktopStatus struct {
	At             time.Time
	State          string
	TitlesReadable bool
	LastWindowAt   *time.Time
}

type Evidence struct {
	// PhoneLastReceived is when the server last received anything from the
	// phone. Receipt time is used because a phone backfills old events.
	PhoneLastReceived *time.Time
	Desktop           *DesktopStatus
}

type Finding struct {
	Device string         `json:"device"`
	Kind   string         `json:"kind"`
	Since  time.Time      `json:"since"`
	Detail map[string]any `json:"detail"`
}

// Evaluate returns the problems that hold at now. A device that never
// reported is not expected and produces nothing.
func Evaluate(evidence Evidence, now time.Time) []Finding {
	findings := []Finding{}
	if last := evidence.PhoneLastReceived; last != nil && now.Sub(*last) >= PhoneSilentAfter {
		findings = append(findings, Finding{Device: DevicePhone, Kind: KindSilent, Since: *last, Detail: map[string]any{
			"message": "The phone has delivered nothing since this time. It reports every minute, so collection has probably stopped (app force-stopped, no network or Tailscale, or the phone is off).",
		}})
	}
	status := evidence.Desktop
	if status == nil {
		return findings
	}
	switch {
	case status.State == "sleeping" || status.State == "stopping":
		// Announced: the silence that follows is expected.
	case now.Sub(status.At) >= DesktopSilentAfter:
		findings = append(findings, Finding{Device: DeviceMac, Kind: KindSilent, Since: status.At, Detail: map[string]any{
			"last_state": status.State,
			"message":    "The Mac collector stopped reporting without announcing sleep or a stop. It may have crashed, lost its network, or been unloaded.",
		}})
	case status.State != "running":
		// "started" and "awake" are sent before the watchers have run, so
		// they say nothing about watcher health yet.
	default:
		if !status.TitlesReadable {
			findings = append(findings, Finding{Device: DeviceMac, Kind: KindTitlesUnreadable, Since: status.At, Detail: map[string]any{
				"message": "The Mac collector cannot read window titles. Accessibility access for Telemetry Desktop is missing.",
			}})
		}
		if status.LastWindowAt == nil || status.At.Sub(*status.LastWindowAt) >= WindowStaleAfter {
			since := status.At
			if status.LastWindowAt != nil {
				since = *status.LastWindowAt
			}
			findings = append(findings, Finding{Device: DeviceMac, Kind: KindWindowStopped, Since: since, Detail: map[string]any{
				"message": "The Mac collector is running but has not recorded the focused window recently.",
			}})
		}
	}
	return findings
}
