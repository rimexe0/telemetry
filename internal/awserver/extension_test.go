package awserver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
