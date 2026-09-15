package nutrition

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/queue"
)

func TestImportExportQueuesRecordsAndRetainsArchive(t *testing.T) {
	fixture := map[string]string{
		"user_intake.json": `[
  {"id":"intake-1","unit":"g","amount":120,"type":"breakfast","dateTime":"2026-09-12T08:15:00.000","meal":{"name":"Synthetic oats","source":"custom","nutriments":{"energyKcal100":100}}}
]`,
		"user_activity.json":    `[{"id":"activity-1","duration":45,"burnedKcal":320,"date":"2026-09-12T18:00:00.000","physicalActivityDBO":{"specificActivity":"Synthetic walk"}}]`,
		"user_tracked_day.json": `[{"day":"2026-09-12T00:00:00.000","calorieGoal":2200,"caloriesTracked":1850}]`,
		"user_recipes.json":     `[{"id":"recipe-1","name":"Synthetic bowl","createdAt":"2026-09-10T10:00:00.000","updatedAt":"2026-09-11T10:00:00.000"}]`,
	}
	exportPath := writeExportFixture(t, fixture)
	sourceBytes, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatal(err)
	}
	archiveDir := filepath.Join(t.TempDir(), "archive")
	queuePath := filepath.Join(t.TempDir(), "collector.sqlite")
	localQueue, err := queue.Open(queuePath)
	if err != nil {
		t.Fatal(err)
	}
	defer localQueue.Close()
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	result, err := Import(context.Background(), localQueue, ImportOptions{
		DeviceID:         "fedora",
		SourceGeneration: "fork-2026-09",
		ExportPath:       exportPath,
		ArchiveDir:       archiveDir,
		Timezone:         "Europe/Istanbul",
		Now:              now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RecordsSeen != 4 || result.RevisionsEnqueued != 4 || result.MembershipsQueued != 4 || result.CursorAdvanced != true {
		t.Fatalf("result = %+v", result)
	}
	if result.ByKind["opennutritracker.intake"] != 1 || result.ByKind["opennutritracker.activity"] != 1 || result.ByKind["opennutritracker.tracked_day"] != 1 || result.ByKind["opennutritracker.recipe"] != 1 {
		t.Fatalf("kind counts = %#v", result.ByKind)
	}
	stats, err := localQueue.Stats(context.Background())
	if err != nil || stats.Pending != 8 {
		t.Fatalf("queue stats = %+v, %v", stats, err)
	}
	items, err := localQueue.Claim(context.Background(), 10, time.Minute, now)
	if err != nil || len(items) != 8 {
		t.Fatalf("claim = %d, %v", len(items), err)
	}
	for _, item := range items {
		var payload map[string]any
		if err := json.Unmarshal(item.Observation.OriginalPayload, &payload); err != nil {
			t.Fatal(err)
		}
		if item.Observation.Kind == "opennutritracker.snapshot_membership" {
			for _, key := range []string{"archive_sha256", "archive_path", "manifest_path", "source_revision_digest"} {
				if value, ok := payload[key]; !ok || value == "" {
					t.Fatalf("missing %s in snapshot membership %s", key, item.Observation.OriginalPayload)
				}
			}
			if payload["source_observation_kind"] == "" {
				t.Fatalf("missing archive provenance in %s", item.Observation.OriginalPayload)
			}
			continue
		}
		if _, exists := payload["_telemetry"]; exists {
			t.Fatalf("source payload was modified with collector metadata: %s", item.Observation.OriginalPayload)
		}
		if item.Observation.Projection == nil || item.Observation.OccurredAt.IsZero() {
			t.Fatalf("incomplete observation = %+v", item.Observation)
		}
		switch item.Observation.SourceRecordKey {
		case "opennutritracker.intake:intake-1":
			want := time.Date(2026, 9, 12, 5, 15, 0, 0, time.UTC)
			if !item.Observation.OccurredAt.Equal(want) {
				t.Fatalf("intake time = %s, want %s", item.Observation.OccurredAt, want)
			}
		case "opennutritracker.tracked_day:2026-09-12T00:00:00.000":
			want := time.Date(2026, 9, 11, 21, 0, 0, 0, time.UTC)
			if !item.Observation.OccurredAt.Equal(want) {
				t.Fatalf("tracked day time = %s, want %s", item.Observation.OccurredAt, want)
			}
		}
	}
	archiveBytes, err := os.ReadFile(result.ArchivePath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archiveBytes)
	if got := hex.EncodeToString(digest[:]); got != result.ArchiveSHA256 {
		t.Fatalf("archive digest = %s, want %s", got, result.ArchiveSHA256)
	}
	if result.ArchiveSizeBytes != int64(len(archiveBytes)) {
		t.Fatalf("archive size = %d, want %d", result.ArchiveSizeBytes, len(archiveBytes))
	}
	if string(archiveBytes) != string(sourceBytes) {
		t.Fatal("archive bytes differ from the selected source export")
	}
	if _, err := os.Stat(result.ManifestPath); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	for _, item := range items {
		if err := localQueue.Acknowledge(context.Background(), item, now); err != nil {
			t.Fatal(err)
		}
	}
	second, err := Import(context.Background(), localQueue, ImportOptions{
		DeviceID:         "fedora",
		SourceGeneration: "fork-2026-09",
		ExportPath:       exportPath,
		ArchiveDir:       archiveDir,
		Timezone:         "Europe/Istanbul",
		Now:              now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.RevisionsEnqueued != 0 || second.RecordsSeen != 4 {
		t.Fatalf("repeat result = %+v", second)
	}
	stats, err = localQueue.Stats(context.Background())
	if err != nil || stats.Pending != 0 || stats.Acknowledged != 8 {
		t.Fatalf("repeat queue stats = %+v, %v", stats, err)
	}
}

func TestImportRejectsUnsafeArchiveEntry(t *testing.T) {
	exportPath := writeExportFixture(t, map[string]string{"../user_intake.json": `[]`})
	localQueue, err := queue.Open(filepath.Join(t.TempDir(), "collector.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer localQueue.Close()
	_, err = Import(context.Background(), localQueue, ImportOptions{
		DeviceID: "fedora", SourceGeneration: "fork", ExportPath: exportPath,
		ArchiveDir: filepath.Join(t.TempDir(), "archive"), Timezone: "UTC",
	})
	if err == nil || !strings.Contains(err.Error(), "unsafe ZIP entry") {
		t.Fatalf("error = %v", err)
	}
}

func TestImportRequiresExplicitTimezone(t *testing.T) {
	exportPath := writeExportFixture(t, map[string]string{"user_intake.json": `[]`})
	localQueue, err := queue.Open(filepath.Join(t.TempDir(), "collector.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer localQueue.Close()
	_, err = Import(context.Background(), localQueue, ImportOptions{
		DeviceID: "fedora", SourceGeneration: "fork", ExportPath: exportPath,
		ArchiveDir: filepath.Join(t.TempDir(), "archive"),
	})
	if err == nil || !strings.Contains(err.Error(), "timezone is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestImportRejectsExtremeArchiveLimit(t *testing.T) {
	exportPath := writeExportFixture(t, map[string]string{
		"user_intake.json":      `[]`,
		"user_activity.json":    `[]`,
		"user_tracked_day.json": `[]`,
	})
	localQueue, err := queue.Open(filepath.Join(t.TempDir(), "collector.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer localQueue.Close()
	_, err = Import(context.Background(), localQueue, ImportOptions{
		DeviceID: "fedora", SourceGeneration: "fork", ExportPath: exportPath,
		ArchiveDir: filepath.Join(t.TempDir(), "archive"), Timezone: "UTC",
		MaxFileBytes: math.MaxInt64,
	})
	if err == nil || !strings.Contains(err.Error(), "cannot exceed") {
		t.Fatalf("error = %v", err)
	}
}

func TestImportDoesNotModifyExistingArchiveDirectory(t *testing.T) {
	exportPath := writeExportFixture(t, map[string]string{
		"user_intake.json":      `[]`,
		"user_activity.json":    `[]`,
		"user_tracked_day.json": `[]`,
	})
	archiveDir := filepath.Join(t.TempDir(), "archive")
	if err := os.Mkdir(archiveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(archiveDir)
	if err != nil {
		t.Fatal(err)
	}
	localQueue, err := queue.Open(filepath.Join(t.TempDir(), "collector.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer localQueue.Close()
	_, err = Import(context.Background(), localQueue, ImportOptions{
		DeviceID: "fedora", SourceGeneration: "fork", ExportPath: exportPath,
		ArchiveDir: archiveDir, Timezone: "UTC",
	})
	if err == nil || !strings.Contains(err.Error(), "must be private") {
		t.Fatalf("error = %v", err)
	}
	after, err := os.Stat(archiveDir)
	if err != nil {
		t.Fatal(err)
	}
	if before.Mode().Perm() != after.Mode().Perm() {
		t.Fatalf("archive directory mode changed from %o to %o", before.Mode().Perm(), after.Mode().Perm())
	}
}

func TestImportChangedRecordQueuesOnlyNewRevision(t *testing.T) {
	base := map[string]string{
		"user_intake.json":      `[{"id":"intake-1","unit":"g","amount":120,"type":"breakfast","dateTime":"2026-09-12T08:15:00.000","meal":{"name":"Synthetic oats"}}]`,
		"user_activity.json":    `[{"id":"activity-1","duration":45,"date":"2026-09-12T18:00:00.000"}]`,
		"user_tracked_day.json": `[{"day":"2026-09-12T00:00:00.000","calorieGoal":2200,"caloriesTracked":1850}]`,
	}
	firstExport := writeExportFixture(t, base)
	changed := make(map[string]string, len(base))
	for name, value := range base {
		changed[name] = value
	}
	changed["user_intake.json"] = strings.Replace(changed["user_intake.json"], `"amount":120`, `"amount":140`, 1)
	secondExport := writeExportFixture(t, changed)
	localQueue, err := queue.Open(filepath.Join(t.TempDir(), "collector.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer localQueue.Close()
	archiveDir := filepath.Join(t.TempDir(), "archive")
	options := ImportOptions{DeviceID: "fedora", SourceGeneration: "fork", ArchiveDir: archiveDir, Timezone: "Europe/Istanbul", Now: time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)}
	options.ExportPath = firstExport
	first, err := Import(context.Background(), localQueue, options)
	if err != nil || first.RevisionsEnqueued != 3 || first.MembershipsQueued != 3 {
		t.Fatalf("first import = %+v, %v", first, err)
	}
	options.ExportPath = secondExport
	second, err := Import(context.Background(), localQueue, options)
	if err != nil || second.RecordsSeen != 3 || second.RevisionsEnqueued != 1 || second.MembershipsQueued != 3 {
		t.Fatalf("changed import = %+v, %v", second, err)
	}
}

func writeExportFixture(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opennutritracker-export.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	for name, contents := range entries {
		writer, err := archive.Create(name)
		if err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if _, err := io.WriteString(writer, contents); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
