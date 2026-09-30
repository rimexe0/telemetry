// telemetry-desktop is the single desktop collector. It records the focused
// window and AFK state itself, accepts browser and Codex activity through the
// ActivityWatch API subset those clients use, and delivers everything to
// Telemetry through a durable local queue.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/rimexe0/telemetry/internal/awserver"
	"github.com/rimexe0/telemetry/internal/desktop"
	"github.com/rimexe0/telemetry/internal/deviceauth"
	"github.com/rimexe0/telemetry/internal/ingest"
	"github.com/rimexe0/telemetry/internal/queue"
)

const collectorVersion = "telemetry-desktop/0.1.0"

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "run":
		if err := run(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
	case "permissions":
		permissions()
	case "diagnose":
		fmt.Println(desktop.Diagnose())
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: telemetry-desktop <run|permissions|diagnose> [flags]")
	os.Exit(2)
}

// permissions asks macOS for the Accessibility access that window titles need.
func permissions() {
	platform, err := desktop.NewPlatform()
	if err != nil {
		log.Fatal(err)
	}
	if platform.CanReadTitles(true) {
		fmt.Println("Window titles are readable.")
		return
	}
	fmt.Println("Allow telemetry-desktop in System Settings > Privacy & Security > Accessibility.")
}

func run(arguments []string) error {
	flags := flag.NewFlagSet("run", flag.ExitOnError)
	listen := flags.String("listen", "127.0.0.1:5600", "loopback address for the ActivityWatch-compatible API")
	queuePath := flags.String("queue", "", "SQLite queue path (required)")
	deviceID := flags.String("device", "", "Telemetry device ID")
	configPath := flags.String("config", "", "paired device configuration")
	serverURL := flags.String("server", "", "Telemetry core URL; omit to queue only")
	tokenFile := flags.String("ingest-token-file", "", "file containing the Telemetry ingest token")
	hostname := flags.String("hostname", "", "hostname used in bucket names (default: this computer's)")
	watch := flags.Bool("watch", true, "record the focused window and AFK state")
	deliverEvery := flags.Duration("deliver-every", 30*time.Second, "how often queued records are sent")
	flags.Parse(arguments)

	client, err := delivery(*configPath, deviceID, *serverURL, *tokenFile)
	if err != nil {
		return err
	}
	if *queuePath == "" || *deviceID == "" {
		return errors.New("-queue and -device (or -config) are required")
	}
	if *hostname == "" {
		name, err := os.Hostname()
		if err != nil {
			return err
		}
		*hostname = name
	}
	host, _, err := net.SplitHostPort(*listen)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return errors.New("-listen must be a loopback address")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	localQueue, err := queue.Open(*queuePath)
	if err != nil {
		return err
	}
	defer localQueue.Close()
	store, err := awserver.Open(ctx, localQueue, *deviceID, time.Now)
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return fmt.Errorf("listen %s (is aw-server still running?): %w", *listen, err)
	}
	server := &http.Server{
		Handler:           awserver.Handler{Store: store, Hostname: *hostname, Version: collectorVersion},
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
	}
	health := &desktop.Health{}
	status := statusReporter{queue: localQueue, client: client, deviceID: *deviceID, health: health, started: time.Now().UTC()}
	errs := make(chan error, 2)
	go func() {
		if err := server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
	}()
	if *watch {
		platform, err := desktop.NewPlatform()
		if err != nil {
			return err
		}
		go func() {
			if err := desktop.Run(ctx, store, platform, desktop.Options{Hostname: *hostname, Health: health}); err != nil {
				errs <- err
			}
		}()
	}
	log.Printf("%s collecting as %s (%s) on %s", collectorVersion, *deviceID, *hostname, *listen)
	if err := status.report(ctx, stateStarted, true); err != nil {
		log.Printf("status: %v", err)
	}
	// Announce sleep before it happens so the silence that follows is not
	// mistaken for a stopped collector.
	if err := desktop.WatchPower(func(sleeping bool) {
		state := stateAwake
		if sleeping {
			state = stateSleeping
			store.Flush(context.Background())
		}
		if err := status.report(context.Background(), state, true); err != nil {
			log.Printf("status %s: %v", state, err)
		}
	}); err != nil {
		log.Printf("sleep announcements unavailable: %v", err)
	}
	statusTicker := time.NewTicker(time.Minute)
	defer statusTicker.Stop()

	ticker := time.NewTicker(*deliverEvery)
	defer ticker.Stop()
	var lastProblem string
	for {
		select {
		case err := <-errs:
			return err
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			server.Shutdown(shutdown)
			// Keep the latest state of open events before exiting.
			err := store.Flush(shutdown)
			if statusErr := status.report(shutdown, stateStopping, true); statusErr != nil {
				log.Printf("status stopping: %v", statusErr)
			}
			return err
		case <-statusTicker.C:
			if err := status.report(ctx, stateRunning, false); err != nil {
				log.Printf("status: %v", err)
			}
		case <-ticker.C:
			problem := ""
			if err := store.Flush(ctx); err != nil {
				problem = "flush: " + err.Error()
			} else if client != nil {
				problem = deliver(ctx, localQueue, *client, *deviceID)
			}
			if problem != lastProblem {
				if problem == "" {
					log.Printf("delivery recovered")
				} else {
					log.Printf("%s", problem)
				}
				lastProblem = problem
			}
		}
	}
}

func deliver(ctx context.Context, localQueue *queue.Queue, client ingest.Client, deviceID string) string {
	passCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	_, err := ingest.DrainAll(passCtx, localQueue, client, ingest.DrainAllOptions{
		DrainOptions: ingest.DrainOptions{
			Limit: 200, LeaseDuration: time.Minute, RetryAfter: 30 * time.Second,
			Now: time.Now().UTC(), DeviceID: deviceID, Version: collectorVersion,
		},
		MaxBatches: 20,
	})
	if err != nil {
		return "delivery: " + err.Error()
	}
	return ""
}

// delivery returns nil when records should only be queued.
func delivery(configPath string, deviceID *string, serverURL, tokenFile string) (*ingest.Client, error) {
	httpClient := &http.Client{Timeout: 15 * time.Second}
	if configPath != "" {
		if serverURL != "" || tokenFile != "" {
			return nil, errors.New("-config cannot be combined with -server or -ingest-token-file")
		}
		config, err := deviceauth.Load(configPath)
		if err != nil {
			return nil, err
		}
		if *deviceID != "" && *deviceID != config.DeviceID {
			return nil, errors.New("paired configuration is for a different device")
		}
		*deviceID = config.DeviceID
		if !config.Approved {
			return nil, nil
		}
		return &ingest.Client{BaseURL: config.ServerURL, Token: config.Secret, DeviceID: config.DeviceID, HTTPClient: httpClient}, nil
	}
	if serverURL == "" && tokenFile == "" {
		return nil, nil
	}
	if serverURL == "" || tokenFile == "" {
		return nil, errors.New("-server and -ingest-token-file must be provided together")
	}
	token, err := os.ReadFile(tokenFile)
	if err != nil {
		return nil, fmt.Errorf("read ingest token: %w", err)
	}
	return &ingest.Client{BaseURL: serverURL, Token: strings.TrimSpace(string(token)), HTTPClient: httpClient}, nil
}
