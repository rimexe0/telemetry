// telemetry-github captures bounded, read-only GitHub repository metadata into
// a local Telemetry queue. It never delivers the queue to a server.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rimexe0/telemetry/internal/githubsource"
	"github.com/rimexe0/telemetry/internal/queue"
)

const collectorVersion = "github-0.1.0"

func main() {
	if len(os.Args) < 2 || os.Args[1] != "capture" {
		fmt.Fprintln(os.Stderr, "usage: telemetry-github capture -repo OWNER/REPOSITORY -queue PATH [options]")
		os.Exit(2)
	}
	if err := capture(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func capture(arguments []string) error {
	flags := flag.NewFlagSet("capture", flag.ContinueOnError)
	repository := flags.String("repo", "", "selected GitHub repository OWNER/REPOSITORY (required)")
	queuePath := flags.String("queue", "", "local SQLite queue path (required)")
	deviceID := flags.String("device", "", "Telemetry device ID (default github:OWNER/REPOSITORY)")
	maxStatuses := flags.Int("max-statuses", 100, "maximum status entries for the default-branch head")
	maxCheckRuns := flags.Int("max-check-runs", 100, "maximum check runs for the default-branch head")
	timeout := flags.Duration("timeout", 30*time.Second, "maximum capture duration")
	queueTimeout := flags.Duration("queue-timeout", 5*time.Second, "maximum local queue persistence duration")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	owner, repo, err := splitRepository(*repository)
	if err != nil {
		return err
	}
	if strings.TrimSpace(*queuePath) == "" {
		return fmt.Errorf("-queue is required")
	}
	if *timeout <= 0 {
		return fmt.Errorf("-timeout must be positive")
	}
	if *maxStatuses < 1 || *maxStatuses > 100 || *maxCheckRuns < 1 || *maxCheckRuns > 100 {
		return fmt.Errorf("status and check-run limits must be between 1 and 100")
	}
	if *queueTimeout <= 0 {
		return fmt.Errorf("-queue-timeout must be positive")
	}
	if filepath.Clean(*queuePath) == "." {
		return fmt.Errorf("-queue must be a file path")
	}

	captureContext, cancelCapture := context.WithTimeout(context.Background(), *timeout)
	defer cancelCapture()
	queueContext, cancelQueue := context.WithTimeout(context.Background(), *queueTimeout)
	defer cancelQueue()
	now := time.Now().UTC()
	output, runErr := captureToQueue(captureContext, queueContext, githubsource.GHAPI{}, githubsource.Options{
		Owner: owner, Repository: repo, DeviceID: *deviceID,
		MaxStatuses: *maxStatuses, MaxCheckRuns: *maxCheckRuns, Now: now,
	}, *queuePath)
	encoded, err := json.Marshal(output)
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return runErr
}

func captureToQueue(captureContext, queueContext context.Context, api githubsource.API, options githubsource.Options, queuePath string) (map[string]any, error) {
	now := options.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
		options.Now = now
	}
	result := githubsource.Capture(captureContext, api, options)
	localQueue, err := queue.Open(queuePath)
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("open queue: %v", err)}, err
	}
	defer localQueue.Close()
	inserted := 0
	duplicates := 0
	for _, observation := range result.Observations {
		added, enqueueErr := localQueue.Enqueue(queueContext, observation, now)
		if enqueueErr != nil {
			queueErr := fmt.Errorf("queue %s observation: %w", observation.Kind, enqueueErr)
			return map[string]any{"error": queueErr.Error()}, queueErr
		}
		if added {
			inserted++
		} else {
			duplicates++
		}
	}
	stats, err := localQueue.Stats(queueContext)
	if err != nil {
		statsErr := fmt.Errorf("read queue stats: %w", err)
		return map[string]any{"error": statsErr.Error()}, statsErr
	}
	output := map[string]any{
		"collector_version":            collectorVersion,
		"repository":                   result.Repository,
		"default_branch":               result.DefaultBranch,
		"head_sha":                     result.HeadSHA,
		"observations_seen":            len(result.Observations),
		"observations_inserted":        inserted,
		"observations_already_present": duplicates,
		"statuses_seen":                result.StatusesSeen,
		"check_runs_seen":              result.CheckRunsSeen,
		"statuses_truncated":           result.StatusesTruncated,
		"check_runs_truncated":         result.CheckRunsTruncated,
		"queue":                        stats,
		"capture_time":                 now.Format(time.RFC3339Nano),
	}
	if result.Err != nil {
		output["error"] = result.Err.Error()
	}
	return output, result.Err
}

func splitRepository(value string) (string, string, error) {
	parts := strings.Split(value, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", fmt.Errorf("-repo must be OWNER/REPOSITORY")
	}
	return parts[0], parts[1], nil
}
