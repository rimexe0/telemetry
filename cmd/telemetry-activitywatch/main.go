// telemetry-activitywatch imports explicitly selected ActivityWatch buckets.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rimexe0/telemetry/internal/activitywatch"
	"github.com/rimexe0/telemetry/internal/ingest"
	"github.com/rimexe0/telemetry/internal/queue"
)

const collectorVersion = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "login":
		login(os.Args[2:])
	case "discover":
		discover(os.Args[2:])
	case "backfill":
		backfill(os.Args[2:])
	case "sync":
		if err := sync(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
	case "drain":
		drain(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func backfill(arguments []string) {
	flags := flag.NewFlagSet("backfill", flag.ExitOnError)
	url := flags.String("url", "http://127.0.0.1:5600", "ActivityWatch server URL")
	queuePath := flags.String("queue", "", "collector SQLite queue path (required)")
	deviceID := flags.String("device", "", "Telemetry device ID (required)")
	bucketID := flags.String("bucket", "", "ActivityWatch bucket ID (required)")
	from := flags.String("from", "", "initial RFC3339 time range start (required)")
	to := flags.String("to", "", "historical RFC3339 time range end (required)")
	window := flags.Duration("window", 24*time.Hour, "maximum historical request window")
	overlap := flags.Duration("overlap", 15*time.Minute, "reconciliation overlap for mutable events")
	limit := flags.Int("limit", 1000, "maximum events to retrieve per window")
	flags.Parse(arguments)
	if *queuePath == "" || *deviceID == "" || *bucketID == "" || *from == "" || *to == "" {
		log.Fatal("-queue, -device, -bucket, -from, and -to are required")
	}
	start, err := time.Parse(time.RFC3339, *from)
	if err != nil {
		log.Fatal("-from must be RFC3339")
	}
	end, err := time.Parse(time.RFC3339, *to)
	if err != nil {
		log.Fatal("-to must be RFC3339")
	}
	localQueue, err := queue.Open(*queuePath)
	if err != nil {
		log.Fatal(err)
	}
	defer localQueue.Close()
	result, err := activitywatch.BackfillBucket(context.Background(), activitywatch.Client{BaseURL: *url, HTTPClient: &http.Client{Timeout: 30 * time.Second}}, localQueue, activitywatch.BackfillOptions{
		DeviceID: *deviceID,
		Bucket:   activitywatch.Bucket{ID: *bucketID},
		From:     start,
		To:       end,
		Window:   *window,
		Overlap:  *overlap,
		Limit:    *limit,
		Now:      time.Now().UTC(),
	})
	if err != nil {
		log.Fatal(err)
	}
	printJSON(map[string]any{"backfill": result})
}

func discover(arguments []string) {
	flags := flag.NewFlagSet("discover", flag.ExitOnError)
	url := flags.String("url", "http://127.0.0.1:5600", "ActivityWatch server URL")
	flags.Parse(arguments)
	client := activitywatch.Client{BaseURL: *url, HTTPClient: &http.Client{Timeout: 10 * time.Second}}
	context := context.Background()
	info, err := client.Info(context)
	if err != nil {
		log.Fatal(err)
	}
	buckets, err := client.Buckets(context)
	if err != nil {
		log.Fatal(err)
	}
	printJSON(map[string]any{"info": json.RawMessage(info), "buckets": buckets})
}

func sync(arguments []string) error {
	flags := flag.NewFlagSet("sync", flag.ExitOnError)
	configPath := flags.String("config", "", "paired device configuration")
	url := flags.String("url", "http://127.0.0.1:5600", "ActivityWatch server URL")
	queuePath := flags.String("queue", "", "collector SQLite queue path (required)")
	deviceID := flags.String("device", "", "Telemetry device ID (required)")
	bucketID := flags.String("bucket", "", "ActivityWatch bucket ID (required)")
	from := flags.String("from", "", "initial RFC3339 time range start (required until a watermark exists)")
	overlap := flags.Duration("overlap", 15*time.Minute, "reconciliation overlap for mutable events")
	limit := flags.Int("limit", 1000, "maximum events to retrieve")
	passTimeout := flags.Duration("timeout", 20*time.Second, "maximum duration of one collection and delivery pass")
	httpTimeout := flags.Duration("http-timeout", 5*time.Second, "maximum duration of one ActivityWatch or core HTTP request")
	serverURL := flags.String("server", "", "Telemetry core URL; omit to queue only")
	ingestToken := flags.String("ingest-token", "", "Telemetry core ingest token")
	ingestTokenFile := flags.String("ingest-token-file", "", "path to a file containing the Telemetry core ingest token")
	drainAll := flags.Bool("drain-all", false, "continue bounded delivery until no claimable queue items remain")
	maxBatches := flags.Int("max-batches", 10000, "maximum batches when -drain-all is set")
	flags.Parse(arguments)
	started := time.Now()
	beforeUsage, _ := readProcessUsage()
	bodyBytes := &bodyByteCounter{}
	output := map[string]any{}
	var localQueue *queue.Queue
	finish := func() {
		stats := queue.Stats{}
		if localQueue != nil {
			if value, err := localQueue.Stats(context.Background()); err == nil {
				stats = value
			}
		}
		afterUsage, _ := readProcessUsage()
		output["metrics"] = makePassMetrics(started, time.Now(), beforeUsage, afterUsage, bodyBytes, stats)
		printJSON(output)
	}
	fail := func(err error) error {
		output["error"] = err.Error()
		finish()
		return err
	}
	paired, err := applyPairing(*configPath, deviceID, serverURL, ingestToken, ingestTokenFile)
	if err != nil {
		return fail(err)
	}
	if *queuePath == "" || *deviceID == "" || *bucketID == "" {
		return fail(fmt.Errorf("-queue, -device, and -bucket are required"))
	}
	if *passTimeout <= 0 || *httpTimeout <= 0 {
		return fail(fmt.Errorf("-timeout and -http-timeout must be positive"))
	}
	var start *time.Time
	if *from != "" {
		value, err := time.Parse(time.RFC3339, *from)
		if err != nil {
			return fail(fmt.Errorf("-from must be RFC3339"))
		}
		start = &value
	}
	localQueue, err = queue.Open(*queuePath)
	if err != nil {
		return fail(err)
	}
	defer localQueue.Close()
	now := time.Now().UTC()
	passContext, cancel := context.WithTimeout(context.Background(), *passTimeout)
	defer cancel()
	httpClient := &http.Client{Timeout: *httpTimeout, Transport: countingTransport{base: http.DefaultTransport, bytes: bodyBytes}}
	result, err := activitywatch.SyncBucket(passContext, activitywatch.Client{BaseURL: *url, HTTPClient: httpClient}, localQueue, activitywatch.SyncOptions{
		DeviceID: *deviceID,
		Bucket:   activitywatch.Bucket{ID: *bucketID},
		From:     start,
		Overlap:  *overlap,
		Limit:    *limit,
		Now:      now,
	})
	if err != nil {
		return fail(err)
	}
	output["sync"] = result
	token, err := readToken(*ingestToken, *ingestTokenFile)
	if err != nil {
		return fail(err)
	}
	if *serverURL != "" || token != "" {
		if *serverURL == "" || token == "" {
			return fail(fmt.Errorf("-server and -ingest-token must be provided together"))
		}
		drainOptions := ingest.DrainOptions{
			Limit:         100,
			LeaseDuration: time.Minute,
			RetryAfter:    time.Minute,
			Now:           now,
			DeviceID:      *deviceID,
			Version:       collectorVersion,
		}
		client := ingest.Client{BaseURL: *serverURL, Token: token, HTTPClient: httpClient}
		if paired {
			client.DeviceID = *deviceID
		}
		if *drainAll {
			drainResult, err := ingest.DrainAll(passContext, localQueue, client, ingest.DrainAllOptions{DrainOptions: drainOptions, MaxBatches: *maxBatches})
			if err != nil {
				return fail(err)
			}
			output["delivery"] = drainResult
		} else {
			drainResult, err := ingest.Drain(passContext, localQueue, client, drainOptions)
			if err != nil {
				return fail(err)
			}
			output["delivery"] = drainResult
		}
	}
	finish()
	return nil
}

func drain(arguments []string) {
	flags := flag.NewFlagSet("drain", flag.ExitOnError)
	configPath := flags.String("config", "", "paired device configuration")
	queuePath := flags.String("queue", "", "collector SQLite queue path (required)")
	deviceID := flags.String("device", "", "Telemetry device ID (required)")
	serverURL := flags.String("server", "", "Telemetry core URL (required)")
	ingestToken := flags.String("ingest-token", "", "Telemetry core ingest token (required)")
	ingestTokenFile := flags.String("ingest-token-file", "", "path to a file containing the Telemetry core ingest token")
	limit := flags.Int("limit", 100, "maximum queued observations to deliver")
	drainAll := flags.Bool("all", false, "continue bounded delivery until no claimable queue items remain")
	maxBatches := flags.Int("max-batches", 10000, "maximum batches when -all is set")
	retryAfter := flags.Duration("retry-after", time.Minute, "delay before retry after a failed delivery")
	flags.Parse(arguments)
	paired, err := applyPairing(*configPath, deviceID, serverURL, ingestToken, ingestTokenFile)
	if err != nil {
		log.Fatal(err)
	}
	token, err := readToken(*ingestToken, *ingestTokenFile)
	if err != nil {
		log.Fatal(err)
	}
	if *queuePath == "" || *deviceID == "" || *serverURL == "" || token == "" {
		log.Fatal("-queue, -device, -server, and -ingest-token are required")
	}
	localQueue, err := queue.Open(*queuePath)
	if err != nil {
		log.Fatal(err)
	}
	defer localQueue.Close()
	drainOptions := ingest.DrainOptions{
		Limit:         *limit,
		LeaseDuration: time.Minute,
		RetryAfter:    *retryAfter,
		Now:           time.Now().UTC(),
		DeviceID:      *deviceID,
		Version:       collectorVersion,
	}
	client := ingest.Client{BaseURL: *serverURL, Token: token, HTTPClient: &http.Client{Timeout: 30 * time.Second}}
	if paired {
		client.DeviceID = *deviceID
	}
	if *drainAll {
		result, err := ingest.DrainAll(context.Background(), localQueue, client, ingest.DrainAllOptions{DrainOptions: drainOptions, MaxBatches: *maxBatches})
		if err != nil {
			log.Fatal(err)
		}
		printJSON(result)
		return
	}
	result, err := ingest.Drain(context.Background(), localQueue, client, drainOptions)
	if err != nil {
		log.Fatal(err)
	}
	printJSON(result)
}

func readToken(value, file string) (string, error) {
	if value != "" && file != "" {
		return "", fmt.Errorf("provide either -ingest-token or -ingest-token-file, not both")
	}
	if file == "" {
		return value, nil
	}
	contents, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("read ingest token file: %w", err)
	}
	return strings.TrimSpace(string(contents)), nil
}

func printJSON(value any) {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(encoded))
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: telemetry-activitywatch <login|discover|backfill|sync|drain> [flags]")
}
