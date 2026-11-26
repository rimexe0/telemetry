package xpi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFirefoxManifestRunsTheSameExtension(t *testing.T) {
	chromium, err := os.ReadFile("../../extensions/telemetry-blocker/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	data, err := Manifest(chromium)
	if err != nil {
		t.Fatal(err)
	}
	var firefox, original map[string]any
	json.Unmarshal(data, &firefox)
	json.Unmarshal(chromium, &original)
	background := firefox["background"].(map[string]any)
	if scripts, _ := background["scripts"].([]any); len(scripts) != 1 || scripts[0] != "background.js" || background["service_worker"] != nil {
		t.Fatalf("background = %v", background)
	}
	if _, has := firefox["update_url"]; has {
		t.Fatal("the Chromium update address was kept")
	}
	hosts := firefox["host_permissions"].([]any)
	joined := ""
	for _, host := range hosts {
		joined += host.(string) + " "
	}
	if !strings.Contains(joined, "http://127.0.0.1:5600/*") || !strings.Contains(joined, "http://*/*") || !strings.Contains(joined, "https://*/*") {
		t.Fatalf("host_permissions = %v", hosts)
	}
	gecko := firefox["browser_specific_settings"].(map[string]any)["gecko"].(map[string]any)
	if gecko["id"] != ID || gecko["strict_min_version"] != MinVersion || gecko["update_url"] != nil || gecko["data_collection_permissions"] == nil {
		t.Fatalf("gecko = %v", gecko)
	}
	// Everything else is the Chromium manifest's.
	for _, key := range []string{"version", "permissions", "content_scripts", "action", "icons", "name", "manifest_version"} {
		a, _ := json.Marshal(firefox[key])
		b, _ := json.Marshal(original[key])
		if !bytes.Equal(a, b) {
			t.Errorf("%s changed: %s, was %s", key, a, b)
		}
	}
}

func TestPackageHoldsTheExtensionAndSaysWhetherItIsSigned(t *testing.T) {
	xpi, err := Build("../../extensions/telemetry-blocker")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := Build("../../extensions/telemetry-blocker")
	if !bytes.Equal(xpi, again) {
		t.Fatal("the same files packed differently")
	}
	info, err := Read(xpi)
	if err != nil || info.ID != ID || info.Version == "" || info.Signed {
		t.Fatalf("info = %+v, %v", info, err)
	}
	signed := withSignature(t, xpi)
	if info, _ := Read(signed); !info.Signed {
		t.Fatal("a signed copy reads as unsigned")
	}
	if same, err := SameExtension(xpi, signed); err != nil || !same {
		t.Fatalf("a signature made it another extension: %v %v", same, err)
	}
	other := rezip(t, xpi, func(name string, data []byte) []byte {
		if name == "cover.js" {
			return append(data, '\n')
		}
		return data
	})
	if same, _ := SameExtension(other, signed); same {
		t.Fatal("a changed file went unnoticed")
	}
}

// withSignature adds the files Mozilla's signing adds.
func withSignature(t *testing.T, xpi []byte) []byte {
	return rezip(t, xpi, nil, "META-INF/mozilla.rsa", "META-INF/cose.sig", "META-INF/manifest.mf")
}

func rezip(t *testing.T, xpi []byte, change func(string, []byte) []byte, extra ...string) []byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(xpi), int64(len(xpi)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for _, file := range reader.File {
		opened, _ := file.Open()
		data, _ := io.ReadAll(opened)
		opened.Close()
		if change != nil {
			data = change(file.Name, data)
		}
		w, _ := writer.Create(file.Name)
		w.Write(data)
	}
	for _, name := range extra {
		w, _ := writer.Create(name)
		w.Write([]byte("signature"))
	}
	writer.Close()
	return out.Bytes()
}

func TestPolicyKeepsTheBrowsersOwnSettings(t *testing.T) {
	// LibreWolf ships these in its distribution folder.
	base := []byte(`{"policies": {"DisableTelemetry": true, "ExtensionSettings": {
		"*": {"installation_mode": "allowed", "allowed_types": ["extension", "theme"]},
		"uBlock0@raymondhill.net": {"installation_mode": "normal_installed"}},
		"Preferences": {"browser.x": {"Value": 1}}}}`)
	merged, err := Merge(base, Policies(true), true)
	if err != nil {
		t.Fatal(err)
	}
	settings := merged["ExtensionSettings"].(map[string]any)
	ours := settings[ID].(map[string]any)
	if ours["installation_mode"] != "force_installed" || ours["install_url"] != InstallURL || ours["update_url"] != UpdateURL || ours["private_browsing"] != true {
		t.Fatalf("Telemetry entry = %v", ours)
	}
	if settings["*"] == nil || settings["uBlock0@raymondhill.net"] == nil || settings[ActivityWatchID] == nil {
		t.Fatalf("settings = %v", settings)
	}
	preferences := merged["Preferences"].(map[string]any)
	if preferences["browser.x"] == nil || preferences["extensions.update.interval"] == nil || preferences["xpinstall.signatures.required"] != nil {
		t.Fatalf("preferences = %v", preferences)
	}
	if merged["DisableTelemetry"] != true {
		t.Fatal("a Linux system policy file dropped the browser's other policies")
	}
	// On macOS the browser still reads its own file for policies ours does
	// not name.
	mac, _ := Merge(base, Policies(false), false)
	if _, has := mac["DisableTelemetry"]; has || mac["ExtensionSettings"].(map[string]any)["*"] == nil {
		t.Fatalf("mac = %v", mac)
	}
	if mac["Preferences"].(map[string]any)["xpinstall.signatures.required"] == nil {
		t.Fatal("an unsigned package is not allowed")
	}
	// Without a file of the browser's own, ours alone.
	if alone, err := Merge(nil, Policies(true), true); err != nil || len(alone) != 2 {
		t.Fatalf("alone = %v, %v", alone, err)
	}
}

// The MDM profile generator writes the same policy in Python.
func TestEnrollmentProfileUsesTheSamePolicy(t *testing.T) {
	script, err := os.ReadFile("../../deploy/mdm/enrollment.py")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{ID, InstallURL, UpdateURL, ActivityWatchID, ActivityWatchURL, "net.librewolf.librewolf", "org.mozilla.firefox",
		"EnterprisePoliciesEnabled", "extensions.update.interval", "xpinstall.signatures.required"} {
		if !strings.Contains(string(script), want) {
			t.Errorf("enrollment.py does not mention %q", want)
		}
	}
}

// A fake of Mozilla's signing API: one add-on, signed a moment after
// submission.
type fakeAMO struct {
	mu        sync.Mutex
	versions  map[string]bool // version → signed
	uploads   int
	submitted int
	auth      []string
	server    *httptest.Server
	signed    []byte
}

func (f *fakeAMO) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/v5/addons/upload/":
		if r.FormValue("channel") != "unlisted" {
			http.Error(w, "channel", 400)
			return
		}
		f.uploads++
		w.Write([]byte(`{"uuid":"u1","processed":false}`))
	case r.Method == http.MethodGet && r.URL.Path == "/api/v5/addons/upload/u1/":
		w.Write([]byte(`{"uuid":"u1","processed":true,"valid":true}`))
	case r.Method == http.MethodPut && r.URL.Path == "/api/v5/addons/addon/"+ID+"/":
		f.submitted++
		f.versions["0.11.0"] = false
		w.Write([]byte(`{}`))
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v5/addons/addon/"+ID+"/versions/"):
		number := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v5/addons/addon/"+ID+"/versions/"), "/")
		signed, ok := f.versions[number]
		if !ok {
			http.NotFound(w, r)
			return
		}
		status := "unreviewed"
		if signed {
			status = "public"
		}
		// The next look finds it signed.
		f.versions[number] = true
		json.NewEncoder(w).Encode(map[string]any{"version": number, "file": map[string]any{"status": status, "url": f.server.URL + "/files/" + number + ".xpi"}})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/files/"):
		w.Write(f.signed)
	default:
		http.Error(w, r.Method+" "+r.URL.Path, 404)
	}
}

func TestSigningSubmitsEachVersionOnce(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "icons"), 0o755)
	for _, name := range Files {
		os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte("x"), 0o644)
	}
	os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"manifest_version":3,"name":"Telemetry","version":"0.11.0","background":{"service_worker":"background.js"}}`), 0o644)
	xpi, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeAMO{versions: map[string]bool{}, signed: withSignature(t, xpi)}
	fake.server = httptest.NewServer(fake)
	defer fake.server.Close()
	amo := AMO{Base: fake.server.URL, Issuer: "user:1:2", Secret: "secret", Poll: time.Millisecond, Timeout: 10 * time.Second}
	signed, fresh, err := amo.Sign(context.Background(), xpi)
	if err != nil || !fresh || !bytes.Equal(signed, fake.signed) || fake.uploads != 1 || fake.submitted != 1 {
		t.Fatalf("first: fresh %v, err %v, uploads %d, submitted %d", fresh, err, fake.uploads, fake.submitted)
	}
	for _, header := range fake.auth {
		if !strings.HasPrefix(header, "JWT ") || strings.Count(header, ".") != 2 {
			t.Fatalf("authorization %q", header)
		}
	}
	// The next release with the same version takes the signed copy.
	signed, fresh, err = amo.Sign(context.Background(), xpi)
	if err != nil || fresh || !bytes.Equal(signed, fake.signed) || fake.uploads != 1 || fake.submitted != 1 {
		t.Fatalf("again: fresh %v, err %v, uploads %d, submitted %d", fresh, err, fake.uploads, fake.submitted)
	}
	if _, _, err := (AMO{Base: fake.server.URL}).Sign(context.Background(), xpi); err == nil {
		t.Fatal("signed without credentials")
	}
}
