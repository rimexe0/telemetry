package collector

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/rimexe0/telemetry/internal/awserver"
)

// ActivityWatchAPI serves the ActivityWatch REST subset used by the
// ActivityWatch browser extension and aw-watcher-codex, on loopback only.
type ActivityWatchAPI struct {
	Listen  string
	Version string
	// Site answers the browser extension's site checks when the blocker runs.
	Site func(host string) awserver.SiteDecision
}

func (*ActivityWatchAPI) Name() string { return "activitywatch-api" }

func (a *ActivityWatchAPI) Run(ctx context.Context, host Host) error {
	address, _, err := net.SplitHostPort(a.Listen)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(address); ip == nil || !ip.IsLoopback() {
		return errors.New("the ActivityWatch API must listen on a loopback address")
	}
	listener, err := net.Listen("tcp", a.Listen)
	if err != nil {
		return fmt.Errorf("listen %s (is aw-server still running?): %w", a.Listen, err)
	}
	server := &http.Server{
		Handler:           awserver.Handler{Store: host.Store, Hostname: host.Hostname, Version: a.Version, Site: a.Site},
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()
	if err := server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (a *ActivityWatchAPI) Status() map[string]any { return map[string]any{"listen": a.Listen} }
