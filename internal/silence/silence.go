// Package silence decides when a collection source has gone quiet in a way
// that is a problem, as opposed to a device that is asleep or was stopped on
// purpose. It only reports collection problems; silence is never evidence of
// what the owner did.
package silence

import "time"

const (
	DeviceMac    = "mac"
	DevicePhone  = "phone"
	DeviceServer = "server"

	KindSilent           = "silent"
	KindTitlesUnreadable = "titles_unreadable"
	KindWindowStopped    = "window_sampling_stopped"
	// KindDiskLow: the server's disk is nearly full; when it fills, the
	// database stops and nothing more is recorded.
	KindDiskLow = "disk_low"
	// KindDiskFull: the disk is full and the database has stopped storing
	// uploads. Told separately, so it reaches the owner after the earlier
	// warning.
	KindDiskFull = "disk_full"
	// KindModulePrefix plus a module name: that module kept failing.
	KindModulePrefix = "module_failing:"
)

// Thresholds are deliberately above each source's normal cadence: the phone
// delivers every minute and the desktop collector reports every minute.
const (
	PhoneSilentAfter   = 15 * time.Minute
	DesktopSilentAfter = 10 * time.Minute
	WindowStaleAfter   = 5 * time.Minute
	ModuleFailingAfter = 10 * time.Minute
	// DiskLowBelow is the free space on the server's disk below which
	// collection is at risk.
	DiskLowBelow  = 3 << 30
	DiskFullBelow = 512 << 20
)

// DesktopStatus is the latest telemetry.collector_status from one desktop
// collector (the Mac, or a Linux machine running it headless).
type DesktopStatus struct {
	DeviceID string
	Name     string
	At       time.Time
	State    string
	// Window is nil when the machine does not run the window module.
	Window  *WindowStatus
	Modules map[string]ModuleStatus
}

type WindowStatus struct {
	TitlesReadable bool
	LastWindowAt   *time.Time
}

type ModuleStatus struct {
	Error      string
	ErrorSince *time.Time
}

type Evidence struct {
	// PhoneLastReceived is when the server last received anything from the
	// phone. Receipt time is used because a phone backfills old events.
	PhoneLastReceived *time.Time
	Desktops          []DesktopStatus
	// ServerFreeBytes is the free space on the server's disk, when known.
	ServerFreeBytes *uint64
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
			"device_name": "Phone",
			"message":     "The phone has delivered nothing since this time. It reports every minute, so collection has probably stopped (app force-stopped, no network or Tailscale, or the phone is off).",
		}})
	}
	for _, status := range evidence.Desktops {
		findings = append(findings, evaluateDesktop(status, now)...)
	}
	if free := evidence.ServerFreeBytes; free != nil && *free < DiskFullBelow {
		findings = append(findings, Finding{Device: DeviceServer, Kind: KindDiskFull, Since: now, Detail: map[string]any{
			"device_name": "Server", "free_bytes": *free,
			"message": "The server's disk is full; the database cannot store uploads until there is room.",
		}})
	}
	if free := evidence.ServerFreeBytes; free != nil && *free < DiskLowBelow {
		findings = append(findings, Finding{Device: DeviceServer, Kind: KindDiskLow, Since: now, Detail: map[string]any{
			"device_name": "Server", "free_bytes": *free,
			"message": "The server's disk is nearly full. When it fills, the database stops and nothing more is recorded.",
		}})
	}
	return findings
}

func evaluateDesktop(status DesktopStatus, now time.Time) []Finding {
	findings := []Finding{}
	add := func(kind string, since time.Time, detail map[string]any) {
		detail["device_name"] = status.Name
		findings = append(findings, Finding{Device: status.DeviceID, Kind: kind, Since: since, Detail: detail})
	}
	switch {
	case status.State == "sleeping" || status.State == "stopping":
		// Announced: the silence that follows is expected.
	case now.Sub(status.At) >= DesktopSilentAfter:
		add(KindSilent, status.At, map[string]any{
			"last_state": status.State,
			"message":    "The collector on " + status.Name + " stopped reporting without announcing sleep or a stop. It may have crashed, lost its network, or been unloaded.",
		})
	case status.State != "running":
		// "started" and "awake" are sent before the modules have run, so
		// they say nothing about module health yet.
	default:
		if window := status.Window; window != nil {
			if !window.TitlesReadable {
				add(KindTitlesUnreadable, status.At, map[string]any{
					"message": "The collector on " + status.Name + " cannot read window titles. Accessibility access for Telemetry Desktop is missing.",
				})
			}
			if window.LastWindowAt == nil || status.At.Sub(*window.LastWindowAt) >= WindowStaleAfter {
				since := status.At
				if window.LastWindowAt != nil {
					since = *window.LastWindowAt
				}
				add(KindWindowStopped, since, map[string]any{
					"message": "The collector on " + status.Name + " is running but has not recorded the focused window recently.",
				})
			}
		}
		for name, module := range status.Modules {
			if module.ErrorSince != nil && now.Sub(*module.ErrorSince) >= ModuleFailingAfter {
				add(KindModulePrefix+name, *module.ErrorSince, map[string]any{
					"module": name, "error": module.Error,
					"message": "The " + name + " module on " + status.Name + " keeps failing: " + module.Error,
				})
			}
		}
	}
	return findings
}
