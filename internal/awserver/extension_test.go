package awserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rimexe0/telemetry/internal/xpi"
)

func TestPackedExtensionIsServedForBrowserPolicy(t *testing.T) {
	packed := filepath.Join(t.TempDir(), "extension.crx")
	server := httptest.NewServer(Handler{ExtensionVersion: func() string { return "0.8.0" }, ExtensionPackage: func() string { return packed }})
	defer server.Close()
	get := func(path string) (int, string) {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(body)
	}
	if status, _ := get("/api/telemetry/extension.xml"); status != http.StatusNotFound {
		t.Fatalf("without a package: %d", status)
	}
	os.WriteFile(packed, []byte("Cr24"), 0o644)
	status, body := get("/api/telemetry/extension.xml")
	if status != http.StatusOK || !strings.Contains(body, `appid="`+ExtensionID+`"`) || !strings.Contains(body, `version="0.8.0"`) ||
		!strings.Contains(body, "http://127.0.0.1:5600/api/telemetry/extension.crx") {
		t.Fatalf("update manifest %d: %s", status, body)
	}
	if status, body := get("/api/telemetry/extension.crx"); status != http.StatusOK || body != "Cr24" {
		t.Fatalf("package %d: %q", status, body)
	}
}

func TestFirefoxPackageIsServedWithItsUpdateManifest(t *testing.T) {
	packed := filepath.Join(t.TempDir(), "extension.xpi")
	handler := Handler{FirefoxPackage: func() string { return packed }}
	// Served in memory: the sandbox some tests run in stops a real
	// connection's file transfer after its first bytes.
	get := func(path string) (int, []byte) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		return recorder.Code, recorder.Body.Bytes()
	}
	if status, _ := get("/api/telemetry/updates.json"); status != http.StatusNotFound {
		t.Fatalf("without a package: %d", status)
	}
	data, err := xpi.Build("../../extensions/telemetry-blocker")
	if err != nil {
		t.Fatal(err)
	}
	info, _ := xpi.Read(data)
	os.WriteFile(packed, data, 0o644)
	status, body := get("/api/telemetry/updates.json")
	var manifest struct {
		Addons map[string]struct {
			Updates []struct {
				Version    string `json:"version"`
				UpdateLink string `json:"update_link"`
				UpdateHash string `json:"update_hash"`
			} `json:"updates"`
		} `json:"addons"`
	}
	if status != http.StatusOK || json.Unmarshal(body, &manifest) != nil {
		t.Fatalf("update manifest %d: %s", status, body)
	}
	updates := manifest.Addons[xpi.ID].Updates
	sum := sha256.Sum256(data)
	// Firefox takes an update from an http address only with its hash.
	if len(updates) != 1 || updates[0].Version != info.Version || updates[0].UpdateHash != "sha256:"+hex.EncodeToString(sum[:]) ||
		updates[0].UpdateLink != "http://127.0.0.1:5600/api/telemetry/extension.xpi?v="+info.Version {
		t.Fatalf("updates = %+v", updates)
	}
	if status, got := get("/api/telemetry/extension.xpi?v=" + info.Version); status != http.StatusOK || !bytes.Equal(got, data) {
		t.Fatalf("package %d: %d bytes, want %d", status, len(got), len(data))
	}
}
