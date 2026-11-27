// Package panelui serves the built static management panel from the Go core.
package panelui

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
)

// mapTiles is where Activity's Places map loads its OpenStreetMap tiles
// from, as images. Nothing else of the panel leaves its own origin.
const mapTiles = "https://tile.openstreetmap.org"

const baseCSP = "default-src 'self'; connect-src 'self'; img-src 'self' data: " + mapTiles + "; style-src 'self'; script-src 'self' 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'"

// New returns a handler for a SvelteKit static build directory. The panel has
// no external runtime dependencies apart from the map's tiles, so a headless
// deployment keeps one service.
func New(directory string) (http.Handler, error) { return NewWithClerk(directory, "") }

// NewWithClerk allows only the configured Clerk frontend and its documented dependencies.
func NewWithClerk(directory, issuer string) (http.Handler, error) {
	csp, err := clerkCSP(issuer)
	if err != nil {
		return nil, err
	}
	return serve(directory, func() string { return csp })
}

// NewWithClerkFunc follows a Clerk frontend that can change while the server
// runs, as set from the panel's sign-in settings. issuer is asked on every
// request; an issuer that is not a plain HTTPS origin gets the policy without
// Clerk, so a bad stored value can never widen the policy.
func NewWithClerkFunc(directory string, issuer func() string) (http.Handler, error) {
	if issuer == nil {
		return nil, errors.New("issuer function is required")
	}
	var (
		mu         sync.Mutex
		lastIssuer string
		lastCSP    = baseCSP
	)
	return serve(directory, func() string {
		current := issuer()
		mu.Lock()
		defer mu.Unlock()
		if current != lastIssuer {
			csp, err := clerkCSP(current)
			if err != nil {
				log.Printf("panel: ignoring Clerk issuer for the content policy: %v", err)
				csp = baseCSP
			}
			lastIssuer, lastCSP = current, csp
		}
		return lastCSP
	})
}

func clerkCSP(issuer string) (string, error) {
	if issuer == "" {
		return baseCSP, nil
	}
	parsed, err := url.Parse(issuer)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") || strings.ContainsAny(issuer, " ;,'\"\r\n\t") {
		return "", errors.New("Clerk issuer must be an HTTPS origin")
	}
	origin := "https://" + parsed.Host
	return "default-src 'self'; connect-src 'self' " + origin + " https://*.protect.clerk.com:*; img-src 'self' data: https://img.clerk.com " + mapTiles + "; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline' " + origin + " https://challenges.cloudflare.com https://*.protect.clerk.com; worker-src 'self' blob:; frame-src 'self' https://challenges.cloudflare.com https://*.protect.clerk.com; base-uri 'none'; frame-ancestors 'none'; form-action 'self'", nil
}

func serve(directory string, csp func() string) (http.Handler, error) {
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
		writer.Header().Set("Content-Security-Policy", csp())
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		files.ServeHTTP(writer, request)
	}), nil
}
