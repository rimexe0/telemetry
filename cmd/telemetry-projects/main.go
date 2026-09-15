// telemetry-projects captures selected T3 Code and Git project metadata into
// a local Telemetry queue. It never uploads the queue or mutates the source.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/rimexe0/telemetry/internal/projectsource"
	"github.com/rimexe0/telemetry/internal/queue"
)

const collectorVersion = "0.1.0"

type stringList []string

func (values *stringList) String() string { return fmt.Sprint([]string(*values)) }

func (values *stringList) Set(value string) error {
	if value == "" || strings.TrimSpace(value) == "" {
		return fmt.Errorf("project path cannot be empty or whitespace")
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("project path cannot have leading or trailing whitespace")
	}
	*values = append(*values, value)
	return nil
}

func main() {
	if len(os.Args) < 2 || os.Args[1] != "sync" {
		printJSON(map[string]any{"collector_version": collectorVersion, "error": "usage: telemetry-projects sync [flags]"})
		os.Exit(1)
	}
	output, err := run(os.Args[2:])
	printJSON(output)
	if err != nil {
		os.Exit(1)
	}
}

func run(arguments []string) (map[string]any, error) {
	failure := func(err error) (map[string]any, error) {
		return map[string]any{"collector_version": collectorVersion, "error": err.Error()}, err
	}
	flags := flag.NewFlagSet("sync", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	queuePath := flags.String("queue", "", "Telemetry collector SQLite queue path (required)")
	dbPath := flags.String("db", "", "T3 Code userdata/state.sqlite path (required; opened read-only)")
	usagePath := flags.String("usage-cache", "", "T3 Code usage-scan-cache.json path (optional; opened read-only)")
	deviceID := flags.String("device", "", "Telemetry device ID (required)")
	fromValue := flags.String("from", "", "initial RFC3339 time range start (required until a cursor exists)")
	untilValue := flags.String("until", "", "exclusive RFC3339 time range end (defaults to now)")
	overlap := flags.Duration("overlap", projectsource.DefaultOverlap, "reconciliation overlap for mutable T3 rows")
	limit := flags.Int("limit", 1000, "maximum records per source table/cache pass")
	passTimeout := flags.Duration("timeout", 20*time.Second, "maximum duration of one collection pass")
	collectGit := flags.Bool("git", false, "collect read-only Git status for each selected project")
	gitTimeout := flags.Duration("git-timeout", 5*time.Second, "maximum duration per Git status read")
	var projects stringList
	flags.Var(&projects, "project", "explicit project workspace path; repeat for multiple projects (required)")
	if err := flags.Parse(arguments); err != nil {
		return failure(err)
	}
	if *queuePath == "" || *dbPath == "" || *deviceID == "" || len(projects) == 0 {
		return failure(fmt.Errorf("-queue, -db, -device, and at least one -project are required"))
	}
	from, err := parseTimeFlag("from", *fromValue)
	if err != nil {
		return failure(err)
	}
	until := time.Now().UTC()
	if *untilValue != "" {
		until, err = parseTimeFlag("until", *untilValue)
		if err != nil {
			return failure(err)
		}
	}
	if !from.IsZero() && !until.After(from) {
		return failure(fmt.Errorf("-until must be after -from"))
	}
	if *passTimeout <= 0 {
		return failure(fmt.Errorf("-timeout must be positive"))
	}
	if err := projectsource.ValidateQueuePath(*dbPath, *queuePath); err != nil {
		return failure(err)
	}
	if *usagePath != "" {
		if err := projectsource.ValidateQueuePaths(*queuePath, *usagePath); err != nil {
			return failure(err)
		}
	}
	localQueue, err := queue.Open(*queuePath)
	if err != nil {
		return failure(err)
	}
	defer localQueue.Close()
	passContext, cancel := context.WithTimeout(context.Background(), *passTimeout)
	defer cancel()
	result, err := projectsource.Sync(passContext, localQueue, projectsource.Options{
		DeviceID:       *deviceID,
		DBPath:         *dbPath,
		UsageCachePath: *usagePath,
		ProjectPaths:   projects,
		From:           from,
		Until:          until,
		Overlap:        *overlap,
		Limit:          *limit,
		Now:            time.Now().UTC(),
		CollectGit:     *collectGit,
		GitTimeout:     *gitTimeout,
	})
	resultMap := map[string]any{"collector_version": collectorVersion, "capture": result}
	if err != nil {
		resultMap["error"] = err.Error()
		return resultMap, err
	}
	if isPartial(result) {
		resultMap["error"] = "capture completed with partial source status"
		return resultMap, errors.New("capture completed with partial source status")
	}
	return resultMap, nil
}

func isPartial(result projectsource.SyncResult) bool {
	return result.State.Status == "partial" || result.Usage.Status == "partial" || result.Git.Status == "partial"
}

func printJSON(value any) {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	fmt.Println(string(encoded))
}

func parseTimeFlag(name, value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("-%s must be RFC3339: %w", name, err)
	}
	return parsed.UTC(), nil
}
