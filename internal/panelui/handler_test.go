package panelui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNewServesBuiltAssetsWithPanelHeaders(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "index.html"), []byte("<main>Telemetry</main>"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler, err := New(directory)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusOK || response.Body.String() != "<main>Telemetry</main>" {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Security-Policy") != "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self' 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing panel security headers: %#v", response.Header())
	}
}

func TestNewRequiresStaticIndex(t *testing.T) {
	if _, err := New(t.TempDir()); err == nil {
		t.Fatal("New accepted a directory without index.html")
	}
}
