// telemetry-koreader reads an explicit KOReader source snapshot into a local
// durable queue. It never delivers the queue or changes the reader files.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/rimexe0/telemetry/internal/koreader"
	"github.com/rimexe0/telemetry/internal/queue"
)

const collectorVersion = "0.1.0"

func main() {
	flags := flag.NewFlagSet("telemetry-koreader", flag.ExitOnError)
	databasePath := flags.String("database", "", "path to KOReader settings/statistics.sqlite3 (required)")
	historyPath := flags.String("history", "", "path to KOReader history.lua (derived from database path when omitted)")
	archiveDir := flags.String("archive-dir", "", "directory for exact read-only source snapshots (required)")
	queuePath := flags.String("queue", "", "collector SQLite queue path (required)")
	deviceID := flags.String("device", "", "stable Telemetry device ID (required)")
	sourceGeneration := flags.String("source-generation", "", "explicit stable KOReader source generation; bump after reset or replacement (required)")
	maxRows := flags.Int("max-rows", 100000, "maximum rows per KOReader source before refusing the import")
	maxFileBytes := flags.Int64("max-file-bytes", 256<<20, "maximum bytes per source file before refusing the snapshot")
	nowValue := flags.String("now", "", "collection time as RFC3339Nano; defaults to current UTC time")
	flags.Parse(os.Args[1:])
	if *databasePath == "" || *archiveDir == "" || *queuePath == "" || *deviceID == "" || *sourceGeneration == "" {
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
	result, err := koreader.Import(context.Background(), localQueue, koreader.ImportOptions{
		DeviceID:         *deviceID,
		SourceGeneration: *sourceGeneration,
		DatabasePath:     *databasePath,
		HistoryPath:      *historyPath,
		ArchiveDir:       *archiveDir,
		MaxRows:          *maxRows,
		MaxFileBytes:     *maxFileBytes,
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
