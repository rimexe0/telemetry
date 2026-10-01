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
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/rimexe0/telemetry/internal/awserver"
	"github.com/rimexe0/telemetry/internal/collector"
	"github.com/rimexe0/telemetry/internal/desktop"
	"github.com/rimexe0/telemetry/internal/deviceauth"
	"github.com/rimexe0/telemetry/internal/ingest"
	"github.com/rimexe0/telemetry/internal/queue"
)

const collectorVersion = "telemetry-desktop/0.2.0"

// The macOS block screen needs the app loop on the main thread.
func init() { runtime.LockOSThread() }

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "run":
		if err := run(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
	case "login":
		login(os.Args[2:])
	case "permissions":
		permissions()
	case "diagnose":
		fmt.Println(desktop.Diagnose())
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: telemetry-desktop <run|login|permissions|diagnose> [flags]")
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

// login pairs this computer with Telemetry. The owner approves it once in the
// panel; the saved credential then reads blocking decisions and uploads.
func login(arguments []string) {
	flags := flag.NewFlagSet("login", flag.ExitOnError)
	server := flags.String("server", "https://server.example.ts.net:8443", "Telemetry HTTPS origin")
	id := flags.String("device", "", "existing device ID to keep, for example mac-00000001")
	name := flags.String("name", "", "device name shown in the panel")
	path := flags.String("config", deviceauth.DefaultPath(), "owner-only device configuration")
	flags.Parse(arguments)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	config, err := (deviceauth.Client{}).Login(ctx, *path, *server, *id, *name, func(address string) {
		fmt.Printf("Open this link and approve the device:\n%s\n", address)
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Connected %s. Credentials saved in %s\n", config.DeviceID, *path)
}

// defaultModules are the modules this system can run.
func defaultModules() string {
	if runtime.GOOS == "darwin" {
		return "window,activitywatch-api,t3,blocker"
	}
	return "activitywatch-api,t3"
}

func buildModules(names, listen, t3Dir string, credentials *collector.Credentials) ([]collector.Module, error) {
	modules := []collector.Module{}
	seen := map[string]bool{}
	var blocker *collector.Blocker
	var api *collector.ActivityWatchAPI
	for _, name := range strings.Split(names, ",") {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		switch name {
		case "window":
			modules = append(modules, &collector.Window{})
		case "activitywatch-api":
			api = &collector.ActivityWatchAPI{Listen: listen, Version: collectorVersion}
			modules = append(modules, api)
		case "t3":
			modules = append(modules, &collector.T3{Dir: t3Dir})
		case "blocker":
			blocker = &collector.Blocker{Credentials: credentials}
			modules = append(modules, blocker)
		default:
			return nil, fmt.Errorf("unknown module %q (known: window, activitywatch-api, t3, blocker)", name)
		}
	}
	// The browser extension asks the local API; the blocker answers.
	if api != nil && blocker != nil {
		api.Site = blocker.SiteDecision
	}
	if len(modules) == 0 {
		return nil, errors.New("-modules must name at least one module")
	}
	return modules, nil
}

func run(arguments []string) error {
	flags := flag.NewFlagSet("run", flag.ExitOnError)
	moduleNames := flags.String("modules", defaultModules(), "comma-separated modules to run: window, activitywatch-api, t3, blocker")
	listen := flags.String("listen", "127.0.0.1:5600", "loopback address for the activitywatch-api module")
	home, _ := os.UserHomeDir()
	t3Dir := flags.String("t3-dir", filepath.Join(home, ".t3", "userdata"), "T3 Code userdata directory for the t3 module")
	queuePath := flags.String("queue", "", "SQLite queue path (required)")
	deviceID := flags.String("device", "", "Telemetry device ID")
	configPath := flags.String("config", "", "paired device configuration")
	serverURL := flags.String("server", "", "Telemetry core URL; omit to queue only")
	tokenFile := flags.String("ingest-token-file", "", "file containing the Telemetry ingest token")
	hostname := flags.String("hostname", "", "hostname used in bucket names (default: this computer's)")
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
	var credentials *collector.Credentials
	if client != nil && client.DeviceID != "" {
		credentials = &collector.Credentials{BaseURL: client.BaseURL, DeviceID: client.DeviceID, Secret: client.Token}
	}
	modules, err := buildModules(*moduleNames, *listen, *t3Dir, credentials)
	if err != nil {
		return err
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
	host := collector.Host{Store: store, Queue: localQueue, DeviceID: *deviceID, Hostname: *hostname, Logf: log.Printf}
	status := statusReporter{queue: localQueue, client: client, deviceID: *deviceID, hostname: *hostname, modules: modules, started: time.Now().UTC()}
	errs := make(chan error, len(modules))
	moduleCtx, stopModules := context.WithCancel(ctx)
	defer stopModules()
	names := []string{}
	for _, module := range modules {
		names = append(names, module.Name())
		go func(module collector.Module) {
			if err := module.Run(moduleCtx, host); err != nil {
				errs <- fmt.Errorf("module %s: %w", module.Name(), err)
			}
		}(module)
	}
	log.Printf("%s collecting as %s (%s) with %s", collectorVersion, *deviceID, *hostname, strings.Join(names, ", "))
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
	return desktop.RunMain(func() error {
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
				stopModules()
				shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
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
	})
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
