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
	"net/url"
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
	"github.com/rimexe0/telemetry/internal/sealed"
)

// collectorVersion is replaced with the release name in released builds.
var collectorVersion = "telemetry-desktop/0.2.0"

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
	case "guard":
		if err := guard(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: telemetry-desktop <run|login|permissions|diagnose|guard> [flags]")
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
		return "window,activitywatch-api,t3,t3-open,blocker,tracker"
	}
	return "activitywatch-api,t3"
}

func buildModules(names, listen, t3Dir, t3Token, notices string, distractionsOnly bool, credentials *collector.Credentials) ([]collector.Module, error) {
	modules := []collector.Module{}
	seen := map[string]bool{}
	var blocker *collector.Blocker
	var tracker *collector.Tracker
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
		case "t3-open":
			modules = append(modules, &collector.T3Open{Dir: t3Dir, TokenFile: t3Token})
		case "blocker":
			blocker = &collector.Blocker{Credentials: credentials, DistractionsOnly: distractionsOnly}
			modules = append(modules, blocker)
		case "tracker":
			tracker = &collector.Tracker{Credentials: credentials, Notices: notices}
			modules = append(modules, tracker)
		default:
			return nil, fmt.Errorf("unknown module %q (known: window, activitywatch-api, t3, t3-open, blocker, tracker)", name)
		}
	}
	// The browser extension asks the local API; the blocker answers.
	if api != nil && blocker != nil {
		api.Site, api.Browser, api.CheckIn = blocker.SiteDecision, blocker.BrowserStatus, blocker.ExtensionCheckIn
	}
	// The tracker reads the answers the blocker already fetches.
	if tracker != nil && blocker != nil {
		blocker.OnNow = tracker.Handle
	}
	if len(modules) == 0 {
		return nil, errors.New("-modules must name at least one module")
	}
	return modules, nil
}

func run(arguments []string) error {
	flags := flag.NewFlagSet("run", flag.ExitOnError)
	moduleNames := flags.String("modules", defaultModules(), "comma-separated modules to run: window, activitywatch-api, t3, t3-open, blocker, tracker")
	listen := flags.String("listen", "127.0.0.1:5600", "loopback address for the activitywatch-api module")
	home, _ := os.UserHomeDir()
	t3Dir := flags.String("t3-dir", filepath.Join(home, ".t3", "userdata"), "T3 Code userdata directory for the t3 module")
	t3Token := flags.String("t3-token-file", "", "T3 session token for the t3-open module, from `t3 auth session issue` (default: t3-session-token beside -config or -queue)")
	queuePath := flags.String("queue", "", "SQLite queue path (required)")
	deviceID := flags.String("device", "", "Telemetry device ID")
	configPath := flags.String("config", "", "paired device configuration")
	serverURL := flags.String("server", "", "Telemetry core URL; omit to queue only")
	tokenFile := flags.String("ingest-token-file", "", "file containing the Telemetry ingest token")
	hostname := flags.String("hostname", "", "hostname used in bucket names (default: this computer's)")
	deliverEvery := flags.Duration("deliver-every", 30*time.Second, "how often queued records are sent")
	notices := flags.String("notices", collector.NoticesFocus, "tracker notices: focus (only in a focus session), all, or off")
	distractionsOnly := flags.Bool("distractions-only", false, "block only distractions, never everything but the essentials; for a machine agents also use")
	relay := flags.String("relay", "", "relay that carries sealed device requests to the server, such as https://relay.example.com; the server address is used when it cannot be reached")
	relayKey := flags.String("relay-key", "", "the server's public key for -relay")
	flags.Parse(arguments)

	client, err := delivery(*configPath, deviceID, *serverURL, *tokenFile)
	if err != nil {
		return err
	}
	if *relay != "" && client != nil {
		// Every module's requests to the server go sealed through the relay,
		// so they do not depend on Tailscale; anything else goes directly.
		server, err := url.Parse(client.BaseURL)
		if err != nil {
			return fmt.Errorf("server address: %w", err)
		}
		public, err := sealed.ParsePublic(*relayKey)
		if err != nil {
			return fmt.Errorf("-relay-key: %w", err)
		}
		http.DefaultTransport = &sealed.Transport{Server: server, Relay: *relay, Sealer: sealed.NewSealer(public), Direct: http.DefaultTransport}
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
	if *t3Token == "" {
		beside := *configPath
		if beside == "" {
			beside = *queuePath
		}
		*t3Token = filepath.Join(filepath.Dir(beside), "t3-session-token")
	}
	modules, err := buildModules(*moduleNames, *listen, *t3Dir, *t3Token, *notices, *distractionsOnly, credentials)
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
	asleep := &sleepState{}
	if platform, err := desktop.NewPlatform(); err == nil {
		asleep.idle = platform.IdleTime
	}
	if err := desktop.WatchPower(func(sleeping bool) {
		asleep.set(sleeping, time.Now())
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
				if err := status.report(ctx, asleep.state(time.Now()), false); err != nil {
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
