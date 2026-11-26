package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/rimexe0/telemetry/internal/xpi"
)

func TestBrowserPolicyKeepsTheBrowsersOwnAndAllowsAnUnsignedPackage(t *testing.T) {
	dir := t.TempDir()
	packed := filepath.Join(dir, "extension.xpi")
	data, err := xpi.Build("../../extensions/telemetry-blocker")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(packed, data, 0o644)
	base := filepath.Join(dir, "policies.json")
	os.WriteFile(base, []byte(`{"policies": {"DisableTelemetry": true, "ExtensionSettings": {"uBlock0@raymondhill.net": {"installation_mode": "normal_installed"}}}}`), 0o644)

	var out bytes.Buffer
	if err := browserPolicy([]string{"-base", base, "-xpi", packed}, &out); err != nil {
		t.Fatal(err)
	}
	var linux struct {
		Policies map[string]any `json:"policies"`
	}
	if err := json.Unmarshal(out.Bytes(), &linux); err != nil {
		t.Fatal(err)
	}
	settings := linux.Policies["ExtensionSettings"].(map[string]any)
	preferences := linux.Policies["Preferences"].(map[string]any)
	if linux.Policies["DisableTelemetry"] != true || settings[xpi.ID] == nil || settings["uBlock0@raymondhill.net"] == nil || preferences["xpinstall.signatures.required"] == nil {
		t.Fatalf("linux = %v", linux.Policies)
	}

	out.Reset()
	if err := browserPolicy([]string{"-mac", "-base", filepath.Join(dir, "missing.json"), "-xpi", packed}, &out); err != nil {
		t.Fatal(err)
	}
	var mac map[string]any
	json.Unmarshal(out.Bytes(), &mac)
	if mac["EnterprisePoliciesEnabled"] != true || mac["ExtensionSettings"] == nil || mac["policies"] != nil {
		t.Fatalf("mac = %v", mac)
	}
}
