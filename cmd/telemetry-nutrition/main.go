// telemetry-nutrition reads an explicit OpenNutriTracker export into a local
// durable queue. It never delivers the queue or changes the app's files.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/rimexe0/telemetry/internal/nutrition"
	"github.com/rimexe0/telemetry/internal/queue"
)

const collectorVersion = "0.1.0"

func main() {
	flags := flag.NewFlagSet("telemetry-nutrition", flag.ExitOnError)
	exportPath := flags.String("export", "", "path to OpenNutriTracker JSON export ZIP (required)")
	archiveDir := flags.String("archive-dir", "", "directory for exact read-only export snapshots (required)")
	queuePath := flags.String("queue", "", "collector SQLite queue path (required)")
	deviceID := flags.String("device", "", "stable Telemetry device ID (required)")
	sourceGeneration := flags.String("source-generation", "", "explicit stable OpenNutriTracker source generation (required)")
	timezone := flags.String("timezone", "", "IANA timezone used for export timestamps without an offset (required)")
	maxRecords := flags.Int("max-records", 100000, "maximum records per export JSON file")
	maxFileBytes := flags.Int64("max-file-bytes", 256<<20, "maximum source ZIP and total uncompressed bytes")
	maxEntryBytes := flags.Int64("max-entry-bytes", 128<<20, "maximum uncompressed bytes per ZIP entry")
	nowValue := flags.String("now", "", "collection time as RFC3339Nano; defaults to current UTC time")
	flags.Parse(os.Args[1:])
	if *exportPath == "" || *archiveDir == "" || *queuePath == "" || *deviceID == "" || *sourceGeneration == "" || *timezone == "" {
		flags.Usage()
		os.Exit(2)
	}
	now := time.Time{}
	if *nowValue != "" {
		parsed, err := time.Parse(time.RFC3339Nano, *nowValue)
		if err != nil {
			log.Fatalf("-now must be RFC3339Nano: %v", err)
		}
		now = parsed
	}
	localQueue, err := queue.Open(*queuePath)
	if err != nil {
		log.Fatal(err)
	}
	defer localQueue.Close()
	result, err := nutrition.Import(context.Background(), localQueue, nutrition.ImportOptions{
		DeviceID:         *deviceID,
		SourceGeneration: *sourceGeneration,
		ExportPath:       *exportPath,
		ArchiveDir:       *archiveDir,
		Timezone:         *timezone,
		MaxRecords:       *maxRecords,
		MaxFileBytes:     *maxFileBytes,
		MaxEntryBytes:    *maxEntryBytes,
		Now:              now,
	})
	if err != nil {
		log.Fatal(err)
	}
	printJSON(map[string]any{
		"collector_version": collectorVersion,
		"import":            result,
	})
}

func printJSON(value any) {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		log.Fatal(fmt.Errorf("encode result: %w", err))
	}
	fmt.Println(string(encoded))
}
