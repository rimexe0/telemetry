// Package panelui serves the built static management panel from the Go core.
package panelui

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// New returns a handler for a SvelteKit static build directory. The panel has
// no external runtime dependencies, so a headless deployment keeps one service.
func New(directory string) (http.Handler, error) { return NewWithClerk(directory, "") }

// NewWithClerk allows only the configured Clerk frontend and its documented dependencies.
func NewWithClerk(directory, issuer string) (http.Handler, error) {
	csp := "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self' 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'"
	if issuer != "" {
		parsed, err := url.Parse(issuer)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") || strings.ContainsAny(issuer, " ;\r\n\t") {
			return nil, errors.New("Clerk issuer must be an HTTPS origin")
		}
		origin := "https://" + parsed.Host
		csp = "default-src 'self'; connect-src 'self' " + origin + " https://*.protect.clerk.com:*; img-src 'self' data: https://img.clerk.com; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline' " + origin + " https://challenges.cloudflare.com https://*.protect.clerk.com; worker-src 'self' blob:; frame-src 'self' https://challenges.cloudflare.com https://*.protect.clerk.com; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"
	}
	info, err := os.Stat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("panel directory must be a directory")
	}
	if _, err := os.Stat(directory + "/index.html"); err != nil {
		return nil, err
	}
	files := http.FileServer(http.Dir(directory))
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		// SvelteKit's static entrypoint includes a small inline module bootstrap.
		// The panel is served from a static, same-origin directory only, so allow
		// that bootstrap while retaining same-origin network and asset boundaries.
		writer.Header().Set("Content-Security-Policy", csp)
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		files.ServeHTTP(writer, request)
	}), nil
}
