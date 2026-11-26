package xpi

import (
	"encoding/json"
	"fmt"
)

// Addresses the collector serves the package and its update manifest on.
const (
	InstallURL = "http://127.0.0.1:5600/api/telemetry/extension.xpi"
	UpdateURL  = "http://127.0.0.1:5600/api/telemetry/updates.json"
)

// UpdateIntervalSeconds is how often Firefox asks for add-on updates under
// this policy. Firefox cannot be told to check now, so a new extension
// arrives within this time, or at the next start. Its own default is a day.
const UpdateIntervalSeconds = 3600

// Policies are the enterprise policies that keep the extension installed in
// a Firefox-family browser (owner request 2026-11-24: managed outside the
// browser).
//
//   - Telemetry is force-installed from the collector, cannot be removed or
//     switched off, runs in private windows, and updates from the collector.
//   - Nothing else is installed: the Telemetry extension itself records the
//     tab in front for the collector (owner 2026-11-26: no ActivityWatch
//     add-on in LibreWolf).
//   - An unsigned package installs only where signatures are not required,
//     which LibreWolf allows policy to say. That lets any unsigned add-on
//     install, so it is set only while the kept package is unsigned.
func Policies(signed bool) map[string]any {
	preferences := map[string]any{
		"extensions.update.interval": map[string]any{"Value": UpdateIntervalSeconds, "Status": "default"},
	}
	if !signed {
		preferences["xpinstall.signatures.required"] = map[string]any{"Value": false, "Status": "locked"}
	}
	return map[string]any{
		"ExtensionSettings": map[string]any{
			ID: map[string]any{"installation_mode": "force_installed", "install_url": InstallURL, "update_url": UpdateURL,
				"private_browsing": true},
		},
		"Preferences": preferences,
	}
}

// mergedPolicies are the policies whose entries are merged one by one, so
// the browser's own entries stay beside ours.
var mergedPolicies = map[string]bool{"ExtensionSettings": true, "Preferences": true}

// Merge adds ours to base, the policies.json the browser ships in its
// distribution folder, which may be empty. ExtensionSettings and Preferences
// are merged entry by entry, ours winning; other policies of ours replace
// the browser's.
//
// With all, the result holds every policy in base too: on Linux a system
// policies.json replaces the browser's own file instead of adding to it.
// Without it, the result holds only the policies ours names: on macOS,
// policies from managed preferences replace the file's policy of the same
// name, and the file's other policies still apply.
func Merge(base []byte, ours map[string]any, all bool) (map[string]any, error) {
	var shipped struct {
		Policies map[string]any `json:"policies"`
	}
	if len(base) > 0 {
		if err := json.Unmarshal(base, &shipped); err != nil {
			return nil, fmt.Errorf("browser policies: %w", err)
		}
	}
	merged := map[string]any{}
	if all {
		for name, value := range shipped.Policies {
			merged[name] = value
		}
	}
	for name, value := range ours {
		entries, isMap := value.(map[string]any)
		existing, wasMap := shipped.Policies[name].(map[string]any)
		if !mergedPolicies[name] || !isMap || !wasMap {
			merged[name] = value
			continue
		}
		combined := map[string]any{}
		for key, entry := range existing {
			combined[key] = entry
		}
		for key, entry := range entries {
			combined[key] = entry
		}
		merged[name] = combined
	}
	return merged, nil
}
