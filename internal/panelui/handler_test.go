package panelui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestClerkCSPRejectsInjectedOrigins(t *testing.T) {
	for _, issuer := range []string{"http://clerk.example", "https://clerk.example; script-src *", "https://user@clerk.example", "https://clerk.example/path"} {
		if _, err := NewWithClerk(t.TempDir(), issuer); err == nil {
			t.Fatalf("accepted %q", issuer)
		}
	}
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "index.html"), []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	h, err := NewWithClerk(directory, "https://clerk.example")
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "https://clerk.example") || strings.Contains(csp, "unsafe-eval") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("CSP %s", csp)
	}
}

// The policy follows an issuer set while the server runs, and an injected
// value falls back to the policy without Clerk instead of widening it.
func TestClerkCSPFollowsTheCurrentIssuer(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "index.html"), []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	issuer := ""
	h, err := NewWithClerkFunc(directory, func() string { return issuer })
	if err != nil {
		t.Fatal(err)
	}
	csp := func() string {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
		return w.Header().Get("Content-Security-Policy")
	}
	if got := csp(); got != baseCSP {
		t.Fatalf("no issuer: %s", got)
	}
	issuer = "https://first.clerk.accounts.dev"
	if got := csp(); !strings.Contains(got, "script-src 'self' 'unsafe-inline' https://first.clerk.accounts.dev ") {
		t.Fatalf("first issuer: %s", got)
	}
	issuer = "https://second.clerk.accounts.dev"
	if got := csp(); strings.Contains(got, "first.clerk") || !strings.Contains(got, "https://second.clerk.accounts.dev") {
		t.Fatalf("swapped issuer: %s", got)
	}
	for _, injected := range []string{"http://clerk.example", "https://clerk.example; script-src *", "https://user@clerk.example", "https://clerk.example/path", "https://clerk.example 'unsafe-eval'"} {
		issuer = injected
		if got := csp(); got != baseCSP {
			t.Fatalf("%q widened the policy: %s", injected, got)
		}
	}
	issuer = ""
	if got := csp(); got != baseCSP {
		t.Fatalf("cleared issuer: %s", got)
	}
}
