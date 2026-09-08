// Package panelui serves the built static management panel from the Go core.
package panelui

import (
	"errors"
	"net/http"
	"os"
)

// New returns a handler for a SvelteKit static build directory. The panel has
// no external runtime dependencies, so a headless deployment keeps one service.
func New(directory string) (http.Handler, error) {
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
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self' 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		files.ServeHTTP(writer, request)
	}), nil
}
