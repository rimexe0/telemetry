package koreader

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/queue"
	_ "modernc.org/sqlite"
)

func TestImportQueuesBooksPageStatsAndHistoryIdempotently(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "statistics.sqlite3")
	createFixtureDatabase(t, databasePath)
	historyPath := filepath.Join(root, "history.lua")
	copyFixture(t, filepath.Join("testdata", "history.lua"), historyPath)
	queueStore, err := queue.Open(filepath.Join(root, "queue", "collector.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer queueStore.Close()
	archiveDir := filepath.Join(root, "archive")
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	beforeDigest := sha256File(t, databasePath)

	first, err := Import(ctx, queueStore, ImportOptions{
		DeviceID:         "kindle-pw2",
		SourceGeneration: "main",
		DatabasePath:     databasePath,
		HistoryPath:      historyPath,
		ArchiveDir:       archiveDir,
		Now:              now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.BooksSeen != 2 || first.BooksQueued != 1 || first.BooksWithoutLastOpen != 1 ||
		first.PageStatsSeen != 2 || first.PageStatsQueued != 2 || first.HistorySeen != 2 ||
		first.HistoryQueued != 2 || first.RevisionsEnqueued != 5 || first.MembershipsQueued != 5 || !first.CursorAdvanced {
		t.Fatalf("first import = %+v", first)
	}
	if !first.HistoryFound || first.ManifestPath == "" {
		t.Fatalf("snapshot result = %+v", first)
	}
	if first.SnapshotMode != "full_snapshot" || first.DeletionHandling != "no_tombstones" || first.SnapshotID == "" {
		t.Fatalf("snapshot deletion metadata = %+v", first)
	}
	if got := sha256File(t, databasePath); got != beforeDigest {
		t.Fatalf("source database changed: before=%s after=%s", beforeDigest, got)
	}
	stats, err := queueStore.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Pending != 10 {
		t.Fatalf("queue after first import = %+v", stats)
	}

	second, err := Import(ctx, queueStore, ImportOptions{
		DeviceID:         "kindle-pw2",
		SourceGeneration: "main",
		DatabasePath:     databasePath,
		HistoryPath:      historyPath,
		ArchiveDir:       archiveDir,
		Now:              now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.RevisionsEnqueued != 0 || second.MembershipsQueued != 0 || second.BooksQueued != 0 || second.PageStatsQueued != 0 || second.HistoryQueued != 0 {
		t.Fatalf("repeated import created rows = %+v", second)
	}
	if second.ManifestPath != first.ManifestPath || second.SnapshotID != first.SnapshotID {
		t.Fatalf("same source produced a different snapshot identity: first=%+v second=%+v", first, second)
	}
	stats, err = queueStore.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Pending != 10 {
		t.Fatalf("queue after repeated import = %+v", stats)
	}

	updateFixtureDuration(t, databasePath, 90)
	third, err := Import(ctx, queueStore, ImportOptions{
		DeviceID:         "kindle-pw2",
		SourceGeneration: "main",
		DatabasePath:     databasePath,
		HistoryPath:      historyPath,
		ArchiveDir:       archiveDir,
		Now:              now.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if third.RevisionsEnqueued != 1 || third.MembershipsQueued != 5 || third.PageStatsQueued != 1 {
		t.Fatalf("changed import = %+v", third)
	}
	stats, err = queueStore.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Pending != 16 {
		t.Fatalf("queue after changed import = %+v", stats)
	}

	items, err := queueStore.Claim(ctx, 20, time.Minute, now.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 16 {
		t.Fatalf("claimed items = %d, want 16", len(items))
	}
	var pageDurations []int64
	bookKeyFound := false
	membershipCount := 0
	for _, item := range items {
		var payload map[string]any
		if err := json.Unmarshal(item.Observation.OriginalPayload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["table"] == "page_stat_data" {
			pageDurations = append(pageDurations, int64(payload["duration"].(float64)))
		}
		if payload["table"] == "book" && item.Observation.SourceRecordKey == "book:id:1" {
			bookKeyFound = true
		}
	}
	if len(pageDurations) != 3 {
		t.Fatalf("page durations = %v", pageDurations)
	}
	sort.Slice(pageDurations, func(i, j int) bool { return pageDurations[i] < pageDurations[j] })
	if pageDurations[0] != 0 || pageDurations[1] != 60 || pageDurations[2] != 90 {
		t.Fatalf("page durations = %v", pageDurations)
	}
	if !bookKeyFound {
		t.Fatal("book identity did not use the stable partial MD5")
	}
	manifestBytes, err := os.ReadFile(first.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest SnapshotManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Files) != 2 {
		t.Fatalf("manifest files = %+v", manifest.Files)
	}
	for _, file := range manifest.Files {
		if sha256File(t, file.ArchivePath) != file.SHA256 {
			t.Fatalf("archive digest mismatch for %+v", file)
		}
	}
	for _, item := range items {
		var payload map[string]any
		if err := json.Unmarshal(item.Observation.OriginalPayload, &payload); err != nil {
			t.Fatal(err)
		}
		if item.Observation.Kind == "koreader.snapshot_membership" {
			membershipCount++
			if payload["deletion_handling"] != "no_tombstones" {
				t.Fatalf("missing durable snapshot provenance: %v", payload)
			}
			if payload["manifest_path"] != first.ManifestPath && payload["manifest_path"] != third.ManifestPath {
				t.Fatalf("missing archive manifest provenance: %v", payload)
			}
		}
	}
	if membershipCount != 10 {
		t.Fatalf("snapshot membership count = %d, want 10", membershipCount)
	}
}

func TestImportUsesImmutableArchivedDatabase(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "statistics.sqlite3")
	createFixtureDatabase(t, databasePath)
	historyPath := filepath.Join(root, "history.lua")
	copyFixture(t, filepath.Join("testdata", "history.lua"), historyPath)
	queueStore, err := queue.Open(filepath.Join(root, "queue.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer queueStore.Close()
	snapshot, err := captureSources(databasePath, historyPath, true, filepath.Join(root, "archive"), ImportOptions{
		DeviceID: "kindle-pw2", SourceGeneration: "main", MaxFileBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	updateFixtureDuration(t, databasePath, 90)
	database, err := openReadOnly(snapshot.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var duration int64
	if err := database.QueryRow(`SELECT duration FROM page_stat_data WHERE id_book = 1 AND page = 3`).Scan(&duration); err != nil {
		t.Fatal(err)
	}
	if duration != 60 {
		t.Fatalf("archived duration = %d, want 60", duration)
	}
}

func TestImportRejectsActiveSQLiteSidecars(t *testing.T) {
	for _, suffix := range []string{"-wal", "-journal", "-shm"} {
		t.Run(strings.TrimPrefix(suffix, "-"), func(t *testing.T) {
			root := t.TempDir()
			databasePath := filepath.Join(root, "statistics.sqlite3")
			createFixtureDatabase(t, databasePath)
			if err := os.WriteFile(databasePath+suffix, []byte("active"), 0o600); err != nil {
				t.Fatal(err)
			}
			queueStore, err := queue.Open(filepath.Join(root, "queue.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer queueStore.Close()
			_, err = Import(context.Background(), queueStore, ImportOptions{
				DeviceID: "kindle-pw2", SourceGeneration: "main", DatabasePath: databasePath,
				ArchiveDir: filepath.Join(root, "archive"), MaxFileBytes: 1 << 20,
			})
			if err == nil || !strings.Contains(err.Error(), "active SQLite sidecar") {
				t.Fatalf("active %s error = %v", suffix, err)
			}
		})
	}
}

func TestImportRejectsOversizedInputsAndManifestCorruption(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "statistics.sqlite3")
	createFixtureDatabase(t, databasePath)
	historyPath := filepath.Join(root, "history.lua")
	copyFixture(t, filepath.Join("testdata", "history.lua"), historyPath)
	queueStore, err := queue.Open(filepath.Join(root, "queue.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer queueStore.Close()
	_, err = Import(context.Background(), queueStore, ImportOptions{
		DeviceID: "kindle-pw2", SourceGeneration: "main", DatabasePath: databasePath,
		HistoryPath: historyPath, ArchiveDir: filepath.Join(root, "archive"), MaxFileBytes: 1,
	})
	if err == nil || !strings.Contains(err.Error(), "over limit") {
		t.Fatalf("oversized source error = %v", err)
	}

	result, err := Import(context.Background(), queueStore, ImportOptions{
		DeviceID: "kindle-pw2", SourceGeneration: "main", DatabasePath: databasePath,
		HistoryPath: historyPath, ArchiveDir: filepath.Join(root, "archive"), MaxFileBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(result.ManifestPath, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Import(context.Background(), queueStore, ImportOptions{
		DeviceID: "kindle-pw2", SourceGeneration: "main", DatabasePath: databasePath,
		HistoryPath: historyPath, ArchiveDir: filepath.Join(root, "archive"), MaxFileBytes: 1 << 20,
	})
	if err == nil || !strings.Contains(err.Error(), "different or invalid bytes") {
		t.Fatalf("corrupt manifest error = %v", err)
	}
}

func TestHistoryParserHonorsElementLimit(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("testdata", "history.lua"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseHistory(contents, 10, 1); err == nil || !strings.Contains(err.Error(), "elements") {
		t.Fatalf("element limit error = %v", err)
	}
}

func TestSourceGenerationSeparatesReplacements(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "statistics.sqlite3")
	createFixtureDatabase(t, databasePath)
	historyPath := filepath.Join(root, "history.lua")
	copyFixture(t, filepath.Join("testdata", "history.lua"), historyPath)
	queueStore, err := queue.Open(filepath.Join(root, "queue.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer queueStore.Close()
	for _, generation := range []string{"main", "replacement"} {
		result, err := Import(context.Background(), queueStore, ImportOptions{
			DeviceID: "kindle-pw2", SourceGeneration: generation, DatabasePath: databasePath,
			HistoryPath: historyPath, ArchiveDir: filepath.Join(root, "archive"), Now: time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC),
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.RevisionsEnqueued != 5 || result.MembershipsQueued != 5 {
			t.Fatalf("generation %s import = %+v", generation, result)
		}
	}
	stats, err := queueStore.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Pending != 20 {
		t.Fatalf("queue after replacement generation = %+v", stats)
	}
}

func TestBookIdentityDoesNotMergeRowsWithMatchingMetadata(t *testing.T) {
	first, firstQuality := bookIdentity(bookRow{ID: 11, Title: sql.NullString{String: "Same title", Valid: true}, Authors: sql.NullString{String: "Same author", Valid: true}, MD5: sql.NullString{String: "same-md5", Valid: true}})
	second, secondQuality := bookIdentity(bookRow{ID: 12, Title: sql.NullString{String: "Same title", Valid: true}, Authors: sql.NullString{String: "Same author", Valid: true}, MD5: sql.NullString{String: "same-md5", Valid: true}})
	if first == second || firstQuality != "native_row_id_generation_scoped" || secondQuality != firstQuality {
		t.Fatalf("matching metadata merged: first=%q (%s), second=%q (%s)", first, firstQuality, second, secondQuality)
	}
}

func TestHistoryPrependAndReorderKeepEventIdentity(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "statistics.sqlite3")
	createFixtureDatabase(t, databasePath)
	historyPath := filepath.Join(root, "history.lua")
	copyFixture(t, filepath.Join("testdata", "history.lua"), historyPath)
	queueStore, err := queue.Open(filepath.Join(root, "queue.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer queueStore.Close()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	first, err := Import(context.Background(), queueStore, ImportOptions{
		DeviceID: "kindle-pw2", SourceGeneration: "main", DatabasePath: databasePath,
		HistoryPath: historyPath, ArchiveDir: filepath.Join(root, "archive"), Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.HistoryQueued != 2 || first.RevisionsEnqueued != 5 || first.MembershipsQueued != 5 {
		t.Fatalf("initial history import = %+v", first)
	}
	reordered := []byte(`{
    [1] = {
        ["time"] = 1726200600,
        ["file"] = "/mnt/us/books/Reading Three.epub",
    },
    [2] = {
        ["time"] = 1726200300,
        ["file"] = "/mnt/us/books/Reading Two.pdf",
    },
    [3] = {
        ["time"] = 1726200000,
        ["file"] = "/mnt/us/books/Reading One.epub",
    },
}`)
	if err := os.WriteFile(historyPath, reordered, 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := Import(context.Background(), queueStore, ImportOptions{
		DeviceID: "kindle-pw2", SourceGeneration: "main", DatabasePath: databasePath,
		HistoryPath: historyPath, ArchiveDir: filepath.Join(root, "archive"), Now: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.HistorySeen != 3 || second.HistoryQueued != 1 || second.RevisionsEnqueued != 1 || second.MembershipsQueued != 6 {
		t.Fatalf("prepend/reorder import = %+v", second)
	}
	items, err := queueStore.Claim(context.Background(), 30, time.Minute, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	historyEvents := 0
	memberships := 0
	for _, item := range items {
		var payload map[string]any
		if err := json.Unmarshal(item.Observation.OriginalPayload, &payload); err != nil {
			t.Fatal(err)
		}
		switch item.Observation.Kind {
		case "koreader.history":
			historyEvents++
			if _, exists := payload["entry_index"]; exists {
				t.Fatalf("history position leaked into event revision: %v", payload)
			}
		case "koreader.snapshot_membership":
			memberships++
			if payload["source_observation_kind"] == "koreader.history" {
				if _, exists := payload["source_position"]; !exists {
					t.Fatalf("history membership lacks source position: %v", payload)
				}
			}
		}
	}
	if historyEvents != 3 || memberships != 11 {
		t.Fatalf("history events=%d memberships=%d, want 3 and 11", historyEvents, memberships)
	}
}

func TestHistoryParserDoesNotExecuteLua(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker")
	if _, err := parseHistory([]byte(fmt.Sprintf(`return os.execute("touch %s")`, marker)), 10, 100); err == nil {
		t.Fatal("executable Lua was accepted")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("test input executed Lua")
	}
}

func TestImportRequiresArchiveDirectory(t *testing.T) {
	queueStore, err := queue.Open(filepath.Join(t.TempDir(), "queue.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer queueStore.Close()
	_, err = Import(context.Background(), queueStore, ImportOptions{DeviceID: "kindle", SourceGeneration: "main"})
	if err == nil || err.Error() != "KOReader statistics database path is required" {
		t.Fatalf("error = %v", err)
	}
}

func createFixtureDatabase(t *testing.T, path string) {
	t.Helper()
	sqlite, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlite.Close()
	schema, err := os.ReadFile(filepath.Join("testdata", "statistics.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlite.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
}

func updateFixtureDuration(t *testing.T, path string, duration int64) {
	t.Helper()
	sqlite, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlite.Close()
	if _, err := sqlite.Exec(`UPDATE page_stat_data SET duration = ? WHERE id_book = 1 AND page = 3`, duration); err != nil {
		t.Fatal(err)
	}
}

func copyFixture(t *testing.T, source, target string) {
	t.Helper()
	contents, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}

func sha256File(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(fmt.Errorf("read %s: %w", path, err))
	}
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:])
}
