// Package koreader imports KOReader's local reading history without executing
// its Lua files or modifying the reader's files.
package koreader

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rimexe0/telemetry/internal/model"
	"github.com/rimexe0/telemetry/internal/queue"
	_ "modernc.org/sqlite"
)

const (
	defaultMaxRows      = 100000
	defaultMaxFileBytes = 256 << 20
	maxParserElements   = 1000000
)

// ImportOptions defines an explicit source snapshot and a local queue. The
// source files are opened read-only; ArchiveDir is required so the exact bytes
// consulted by the importer remain available independently of the source path.
type ImportOptions struct {
	DeviceID         string
	SourceGeneration string
	DatabasePath     string
	HistoryPath      string
	ArchiveDir       string
	MaxRows          int
	MaxFileBytes     int64
	Now              time.Time
}

// SourceFile is one exact source file copied to the archive before reading.
type SourceFile struct {
	Role        string `json:"role"`
	Path        string `json:"path"`
	ArchivePath string `json:"archive_path"`
	SHA256      string `json:"sha256"`
	SizeBytes   int64  `json:"size_bytes"`
}

// SnapshotManifest ties a queued import's collection time to the exact source
// bytes read. It is deliberately separate from row payloads so an unrelated
// source-file change does not create revisions for every unchanged row.
type SnapshotManifest struct {
	Version          int          `json:"version"`
	SnapshotID       string       `json:"snapshot_id"`
	DeviceID         string       `json:"device_id"`
	SourceGeneration string       `json:"source_generation"`
	DatabasePath     string       `json:"database_path"`
	HistoryPath      string       `json:"history_path,omitempty"`
	SnapshotMode     string       `json:"snapshot_mode"`
	DeletionHandling string       `json:"deletion_handling"`
	Files            []SourceFile `json:"files"`
}

// ImportResult reports the bounded import without delivering the queue to the
// central service. A later explicit drain can use the existing ingest package.
type ImportResult struct {
	DeviceID               string `json:"device_id"`
	SourceGeneration       string `json:"source_generation"`
	DatabasePath           string `json:"database_path"`
	HistoryPath            string `json:"history_path,omitempty"`
	DatabaseSchemaVersion  int64  `json:"database_schema_version"`
	StatisticsTablePresent bool   `json:"statistics_table_present"`
	HistoryFound           bool   `json:"history_found"`
	SnapshotMode           string `json:"snapshot_mode"`
	DeletionHandling       string `json:"deletion_handling"`
	BooksSeen              int    `json:"books_seen"`
	BooksQueued            int    `json:"books_queued"`
	BooksWithoutLastOpen   int    `json:"books_without_last_open"`
	PageStatsSeen          int    `json:"page_stats_seen"`
	PageStatsQueued        int    `json:"page_stats_queued"`
	HistorySeen            int    `json:"history_seen"`
	HistoryQueued          int    `json:"history_queued"`
	RevisionsEnqueued      int    `json:"revisions_enqueued"`
	MembershipsQueued      int    `json:"snapshot_memberships_queued"`
	CursorAdvanced         bool   `json:"cursor_advanced"`
	ManifestPath           string `json:"manifest_path"`
	SnapshotID             string `json:"snapshot_id"`
}

type sourceSnapshot struct {
	Manifest       SnapshotManifest
	ManifestPath   string
	DatabasePath   string
	HistoryPath    string
	HistoryPresent bool
	FilesByRole    map[string]SourceFile
}

func (snapshot sourceSnapshot) archiveFile(role string) (SourceFile, bool) {
	file, ok := snapshot.FilesByRole[role]
	return file, ok
}

type bookRow struct {
	ID             int64
	Title          sql.NullString
	Authors        sql.NullString
	Notes          sql.NullInt64
	LastOpen       sql.NullInt64
	Highlights     sql.NullInt64
	Pages          sql.NullInt64
	Series         sql.NullString
	Language       sql.NullString
	MD5            sql.NullString
	TotalReadTime  sql.NullInt64
	TotalReadPages sql.NullInt64
}

type pageStatRow struct {
	BookID     int64
	Page       int64
	StartTime  int64
	Duration   int64
	TotalPages int64
}

// Import snapshots and imports the selected KOReader files. It never calls a
// network client, opens a writable SQLite handle, evaluates Lua, or changes
// source files.
func Import(ctx context.Context, localQueue *queue.Queue, options ImportOptions) (ImportResult, error) {
	if localQueue == nil {
		return ImportResult{}, errors.New("queue is required")
	}
	if strings.TrimSpace(options.DeviceID) == "" {
		return ImportResult{}, errors.New("device ID is required")
	}
	if strings.TrimSpace(options.SourceGeneration) == "" {
		return ImportResult{}, errors.New("source generation is required")
	}
	if len(options.SourceGeneration) > 128 || strings.ContainsRune(options.SourceGeneration, '\x00') {
		return ImportResult{}, errors.New("source generation must be at most 128 bytes and contain no NUL")
	}
	if strings.TrimSpace(options.DatabasePath) == "" {
		return ImportResult{}, errors.New("KOReader statistics database path is required")
	}
	if strings.TrimSpace(options.ArchiveDir) == "" {
		return ImportResult{}, errors.New("archive directory is required")
	}
	if options.MaxRows == 0 {
		options.MaxRows = defaultMaxRows
	}
	if options.MaxRows < 0 {
		return ImportResult{}, errors.New("maximum rows must not be negative")
	}
	if options.MaxFileBytes == 0 {
		options.MaxFileBytes = defaultMaxFileBytes
	}
	if options.MaxFileBytes < 1 {
		return ImportResult{}, errors.New("maximum file bytes must be positive")
	}
	if options.Now.IsZero() {
		options.Now = time.Now().UTC()
	}
	options.Now = options.Now.UTC()

	databasePath, err := filepath.Abs(options.DatabasePath)
	if err != nil {
		return ImportResult{}, fmt.Errorf("resolve database path: %w", err)
	}
	historyPath, historyExplicit, err := resolveHistoryPath(databasePath, options.HistoryPath)
	if err != nil {
		return ImportResult{}, err
	}
	archiveDir, err := filepath.Abs(options.ArchiveDir)
	if err != nil {
		return ImportResult{}, fmt.Errorf("resolve archive directory: %w", err)
	}

	snapshot, err := captureSources(databasePath, historyPath, historyExplicit, archiveDir, options)
	if err != nil {
		return ImportResult{}, err
	}

	database, err := openReadOnly(snapshot.DatabasePath)
	if err != nil {
		return ImportResult{}, err
	}
	defer database.Close()
	if _, err := database.ExecContext(ctx, "PRAGMA query_only = 1"); err != nil {
		return ImportResult{}, fmt.Errorf("enable SQLite query-only mode: %w", err)
	}
	if err := verifySQLiteQuickCheck(ctx, database); err != nil {
		return ImportResult{}, err
	}

	version, err := schemaVersion(ctx, database)
	if err != nil {
		return ImportResult{}, err
	}
	if err := requireStatisticsSchema(ctx, database); err != nil {
		return ImportResult{}, err
	}
	if err := enforceRowLimit(ctx, database, "book", options.MaxRows); err != nil {
		return ImportResult{}, err
	}
	if err := enforceRowLimit(ctx, database, "page_stat_data", options.MaxRows); err != nil {
		return ImportResult{}, err
	}

	result := ImportResult{
		DeviceID:               options.DeviceID,
		SourceGeneration:       options.SourceGeneration,
		DatabasePath:           databasePath,
		HistoryPath:            historyPath,
		DatabaseSchemaVersion:  version,
		StatisticsTablePresent: true,
		HistoryFound:           snapshot.HistoryPresent,
		SnapshotMode:           snapshot.Manifest.SnapshotMode,
		DeletionHandling:       snapshot.Manifest.DeletionHandling,
		ManifestPath:           snapshot.ManifestPath,
		SnapshotID:             snapshot.Manifest.SnapshotID,
	}

	if err := importBooks(ctx, database, localQueue, options, version, snapshot, &result); err != nil {
		return result, err
	}
	if err := importPageStats(ctx, database, localQueue, options, version, snapshot, &result); err != nil {
		return result, err
	}
	if result.HistoryFound {
		rawHistory, err := readBoundedFile(snapshot.HistoryPath, options.MaxFileBytes)
		if err != nil {
			return result, fmt.Errorf("read KOReader history: %w", err)
		}
		entries, err := parseHistory(rawHistory, options.MaxRows, parserElementLimit(options.MaxRows))
		if err != nil {
			return result, err
		}
		result.HistorySeen = len(entries)
		if err := importHistory(ctx, localQueue, options, snapshot, entries, &result); err != nil {
			return result, err
		}
	}

	digest, err := manifestDigest(snapshot.ManifestPath, options.MaxFileBytes)
	if err != nil {
		return result, fmt.Errorf("read KOReader snapshot manifest digest: %w", err)
	}
	state := fmt.Sprintf(`{"manifest_path":%q,"manifest_sha256":%q,"snapshot_id":%q,"source_generation":%q,"snapshot_mode":%q,"deletion_handling":%q,"collected_at":%q}`, snapshot.ManifestPath, digest, snapshot.Manifest.SnapshotID, options.SourceGeneration, snapshot.Manifest.SnapshotMode, snapshot.Manifest.DeletionHandling, options.Now.Format(time.RFC3339Nano))
	if err := localQueue.SaveCursor(ctx, cursorKey(options.DeviceID, options.SourceGeneration, databasePath), state, options.Now); err != nil {
		return result, fmt.Errorf("save KOReader snapshot cursor: %w", err)
	}
	result.CursorAdvanced = true
	return result, nil
}

func resolveHistoryPath(databasePath, explicit string) (string, bool, error) {
	if explicit != "" {
		path, err := filepath.Abs(explicit)
		if err != nil {
			return "", true, fmt.Errorf("resolve history path: %w", err)
		}
		if !fileExists(path) {
			return "", true, fmt.Errorf("KOReader history path does not exist: %s", path)
		}
		return path, true, nil
	}
	// statistics.sqlite3 lives under <data-dir>/settings/.
	return filepath.Join(filepath.Dir(filepath.Dir(databasePath)), "history.lua"), false, nil
}

func captureSources(databasePath, historyPath string, historyExplicit bool, archiveDir string, options ImportOptions) (sourceSnapshot, error) {
	if err := os.MkdirAll(archiveDir, 0o700); err != nil {
		return sourceSnapshot{}, fmt.Errorf("create archive directory: %w", err)
	}
	if sidecar, err := activeSQLiteSidecar(databasePath); err != nil {
		return sourceSnapshot{}, err
	} else if sidecar != "" {
		return sourceSnapshot{}, fmt.Errorf("KOReader statistics database has active SQLite sidecar %s; quiesce the reader and retry", sidecar)
	}
	temporaryDir, err := os.MkdirTemp(archiveDir, ".telemetry-koreader-snapshot-*")
	if err != nil {
		return sourceSnapshot{}, fmt.Errorf("create KOReader snapshot directory: %w", err)
	}
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.RemoveAll(temporaryDir)
		}
	}()

	files := make([]SourceFile, 0, 2)
	statistics, err := captureFile("statistics", databasePath, temporaryDir, options.MaxFileBytes)
	if err != nil {
		return sourceSnapshot{}, err
	}
	files = append(files, statistics)
	historyPresent := historyExplicit || fileExists(historyPath)
	if historyPresent {
		history, err := captureFile("history", historyPath, temporaryDir, options.MaxFileBytes)
		if err != nil {
			return sourceSnapshot{}, err
		}
		files = append(files, history)
	}
	if sidecar, err := activeSQLiteSidecar(databasePath); err != nil {
		return sourceSnapshot{}, err
	} else if sidecar != "" {
		return sourceSnapshot{}, fmt.Errorf("KOReader statistics database acquired active SQLite sidecar %s during snapshot; retry with the reader quiescent", sidecar)
	}

	identity := snapshotIdentity{
		Version:          1,
		DeviceID:         options.DeviceID,
		SourceGeneration: options.SourceGeneration,
		DatabasePath:     databasePath,
		HistoryPath:      historyPath,
		SnapshotMode:     "full_snapshot",
		DeletionHandling: "no_tombstones",
		Files:            make([]snapshotFileIdentity, 0, len(files)),
	}
	for _, file := range files {
		identity.Files = append(identity.Files, snapshotFileIdentity{Role: file.Role, Path: file.Path, SHA256: file.SHA256, SizeBytes: file.SizeBytes})
	}
	encodedIdentity, err := json.Marshal(identity)
	if err != nil {
		return sourceSnapshot{}, fmt.Errorf("encode KOReader snapshot identity: %w", err)
	}
	digest := sha256.Sum256(encodedIdentity)
	snapshotID := hex.EncodeToString(digest[:])
	finalDir := filepath.Join(archiveDir, "snapshot-"+snapshotID)
	for index := range files {
		files[index].ArchivePath = filepath.Join(finalDir, filepath.Base(files[index].ArchivePath))
	}
	if existing, err := os.Stat(finalDir); err == nil {
		if !existing.IsDir() {
			return sourceSnapshot{}, fmt.Errorf("KOReader snapshot path is not a directory: %s", finalDir)
		}
		if err := validateArchivedFiles(files, options.MaxFileBytes); err != nil {
			return sourceSnapshot{}, err
		}
		_ = os.RemoveAll(temporaryDir)
		removeTemporary = false
	} else if os.IsNotExist(err) {
		if err := os.Rename(temporaryDir, finalDir); err != nil {
			if !os.IsExist(err) {
				return sourceSnapshot{}, fmt.Errorf("install KOReader snapshot directory: %w", err)
			}
			if err := validateArchivedFiles(files, options.MaxFileBytes); err != nil {
				return sourceSnapshot{}, err
			}
		} else {
			removeTemporary = false
		}
		if err := validateArchivedFiles(files, options.MaxFileBytes); err != nil {
			return sourceSnapshot{}, err
		}
	} else {
		return sourceSnapshot{}, fmt.Errorf("inspect KOReader snapshot path: %w", err)
	}
	manifest := SnapshotManifest{
		Version:          1,
		SnapshotID:       snapshotID,
		DeviceID:         options.DeviceID,
		SourceGeneration: options.SourceGeneration,
		DatabasePath:     databasePath,
		HistoryPath:      historyPath,
		SnapshotMode:     "full_snapshot",
		DeletionHandling: "no_tombstones",
		Files:            files,
	}
	manifestPath, err := writeManifest(archiveDir, manifest, options.MaxFileBytes)
	if err != nil {
		return sourceSnapshot{}, err
	}
	byRole := make(map[string]SourceFile, len(files))
	for _, file := range files {
		byRole[file.Role] = file
	}
	return sourceSnapshot{
		Manifest:       manifest,
		ManifestPath:   manifestPath,
		DatabasePath:   byRole["statistics"].ArchivePath,
		HistoryPath:    byRole["history"].ArchivePath,
		HistoryPresent: historyPresent,
		FilesByRole:    byRole,
	}, nil
}

type snapshotIdentity struct {
	Version          int                    `json:"version"`
	DeviceID         string                 `json:"device_id"`
	SourceGeneration string                 `json:"source_generation"`
	DatabasePath     string                 `json:"database_path"`
	HistoryPath      string                 `json:"history_path,omitempty"`
	SnapshotMode     string                 `json:"snapshot_mode"`
	DeletionHandling string                 `json:"deletion_handling"`
	Files            []snapshotFileIdentity `json:"files"`
}

type snapshotFileIdentity struct {
	Role      string `json:"role"`
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

func activeSQLiteSidecar(databasePath string) (string, error) {
	for _, suffix := range []string{"-wal", "-journal", "-shm"} {
		path := databasePath + suffix
		if _, err := os.Lstat(path); err == nil {
			return path, nil
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect KOReader SQLite sidecar %s: %w", path, err)
		}
	}
	return "", nil
}

func captureFile(role, sourcePath, outputDir string, maxBytes int64) (SourceFile, error) {
	input, err := os.Open(sourcePath)
	if err != nil {
		return SourceFile{}, fmt.Errorf("open KOReader %s source: %w", role, err)
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return SourceFile{}, fmt.Errorf("stat KOReader %s source: %w", role, err)
	}
	if !info.Mode().IsRegular() {
		return SourceFile{}, fmt.Errorf("KOReader %s source is not a regular file: %s", role, sourcePath)
	}
	if info.Size() > maxBytes {
		return SourceFile{}, fmt.Errorf("KOReader %s source is %d bytes, over limit %d", role, info.Size(), maxBytes)
	}
	temporary, err := os.CreateTemp(outputDir, ".source-*")
	if err != nil {
		return SourceFile{}, fmt.Errorf("create KOReader archive file: %w", err)
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	hash := sha256.New()
	limit := maxBytes
	if limit < math.MaxInt64 {
		limit++
	}
	written, err := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(input, limit))
	if err != nil {
		_ = temporary.Close()
		return SourceFile{}, fmt.Errorf("archive KOReader %s source: %w", role, err)
	}
	if written > maxBytes {
		_ = temporary.Close()
		return SourceFile{}, fmt.Errorf("KOReader %s source exceeds limit %d bytes", role, maxBytes)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return SourceFile{}, fmt.Errorf("sync KOReader archive: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return SourceFile{}, fmt.Errorf("close KOReader archive: %w", err)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	sourceDigest, sourceSize, err := boundedFileDigest(sourcePath, maxBytes)
	if err != nil {
		return SourceFile{}, fmt.Errorf("recheck KOReader %s source: %w", role, err)
	}
	if sourceDigest != digest || sourceSize != written {
		return SourceFile{}, fmt.Errorf("KOReader %s source changed during snapshot", role)
	}
	archivePath := filepath.Join(outputDir, filepath.Base(sourcePath))
	if err := os.Rename(temporaryPath, archivePath); err != nil {
		return SourceFile{}, fmt.Errorf("install KOReader archive file: %w", err)
	}
	removeTemporary = false
	if err := os.Chmod(archivePath, 0o600); err != nil {
		return SourceFile{}, fmt.Errorf("protect KOReader archive: %w", err)
	}
	return SourceFile{Role: role, Path: sourcePath, ArchivePath: archivePath, SHA256: digest, SizeBytes: written}, nil
}

func validateArchivedFiles(files []SourceFile, maxBytes int64) error {
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		if _, exists := seen[file.Role]; exists {
			return fmt.Errorf("duplicate KOReader archive role %s", file.Role)
		}
		seen[file.Role] = struct{}{}
		if len(file.SHA256) != sha256.Size*2 || file.SizeBytes < 0 {
			return fmt.Errorf("invalid KOReader archive metadata for %s", file.Role)
		}
		info, err := os.Stat(file.ArchivePath)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("KOReader archive file is missing: %s", file.ArchivePath)
		}
		if info.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("KOReader archive file is not private: %s", file.ArchivePath)
		}
		digest, size, err := boundedFileDigest(file.ArchivePath, maxBytes)
		if err != nil {
			return fmt.Errorf("verify KOReader archive %s: %w", file.Role, err)
		}
		if digest != file.SHA256 || size != file.SizeBytes {
			return fmt.Errorf("KOReader archive digest mismatch for %s", file.Role)
		}
	}
	return nil
}

func writeManifest(archiveDir string, manifest SnapshotManifest, maxBytes int64) (string, error) {
	if err := validateManifest(manifest, maxBytes); err != nil {
		return "", err
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode KOReader snapshot manifest: %w", err)
	}
	path := filepath.Join(archiveDir, "snapshot-"+manifest.SnapshotID+".json")
	if existing, err := readBoundedFile(path, maxBytes); err == nil {
		if !bytes.Equal(existing, encoded) {
			return "", fmt.Errorf("snapshot manifest path already contains different or invalid bytes: %s", path)
		}
		return path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect snapshot manifest: %w", err)
	}
	temporary, err := os.CreateTemp(archiveDir, ".manifest-*")
	if err != nil {
		return "", fmt.Errorf("create KOReader snapshot manifest: %w", err)
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if _, err := temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("write KOReader snapshot manifest: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("sync KOReader snapshot manifest: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("close KOReader snapshot manifest: %w", err)
	}
	// A hard link installs the fully synced file without replacing a manifest
	// another importer may have created concurrently. The temporary file and
	// final path are in the same directory.
	if err := os.Link(temporaryPath, path); err != nil {
		if !os.IsExist(err) {
			return "", fmt.Errorf("install KOReader snapshot manifest: %w", err)
		}
		removeTemporary = true
		existing, readErr := readBoundedFile(path, maxBytes)
		if readErr != nil || !bytes.Equal(existing, encoded) {
			return "", fmt.Errorf("concurrent snapshot manifest differs: %s", path)
		}
		return path, nil
	}
	if err := os.Remove(temporaryPath); err != nil {
		return "", fmt.Errorf("remove KOReader manifest temporary file: %w", err)
	}
	removeTemporary = false
	directory, err := os.Open(archiveDir)
	if err != nil {
		return "", fmt.Errorf("open archive directory for sync: %w", err)
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil {
		return "", fmt.Errorf("sync archive directory: %w", syncErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close archive directory: %w", closeErr)
	}
	return path, nil
}

func validateManifest(manifest SnapshotManifest, maxBytes int64) error {
	if manifest.Version != 1 || manifest.SnapshotID == "" || manifest.DeviceID == "" || manifest.SourceGeneration == "" {
		return errors.New("invalid KOReader snapshot manifest identity")
	}
	if manifest.SnapshotMode != "full_snapshot" || manifest.DeletionHandling != "no_tombstones" {
		return errors.New("invalid KOReader snapshot deletion metadata")
	}
	if !filepath.IsAbs(manifest.DatabasePath) || len(manifest.Files) == 0 || len(manifest.Files) > 4 {
		return errors.New("invalid KOReader snapshot manifest paths")
	}
	if manifest.HistoryPath != "" && !filepath.IsAbs(manifest.HistoryPath) {
		return errors.New("invalid KOReader history path")
	}
	identity := snapshotIdentity{Version: manifest.Version, DeviceID: manifest.DeviceID, SourceGeneration: manifest.SourceGeneration, DatabasePath: manifest.DatabasePath, HistoryPath: manifest.HistoryPath, SnapshotMode: manifest.SnapshotMode, DeletionHandling: manifest.DeletionHandling, Files: make([]snapshotFileIdentity, 0, len(manifest.Files))}
	if manifest.HistoryPath == "" {
		identity.HistoryPath = ""
	}
	for _, file := range manifest.Files {
		if file.Role == "" || !filepath.IsAbs(file.Path) || !filepath.IsAbs(file.ArchivePath) || len(file.SHA256) != sha256.Size*2 || file.SizeBytes < 0 {
			return fmt.Errorf("invalid KOReader snapshot file metadata for %s", file.Role)
		}
		identity.Files = append(identity.Files, snapshotFileIdentity{Role: file.Role, Path: file.Path, SHA256: file.SHA256, SizeBytes: file.SizeBytes})
	}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(encoded)
	if manifest.SnapshotID != hex.EncodeToString(digest[:]) {
		return errors.New("KOReader snapshot manifest identity digest mismatch")
	}
	return validateArchivedFiles(manifest.Files, maxBytes)
}

func openReadOnly(path string) (*sql.DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve SQLite path: %w", err)
	}
	// SQLite URI paths keep slash separators unescaped while encoding URI
	// delimiters that can appear in a mounted filename.
	uriPath := strings.ReplaceAll(filepath.ToSlash(absolute), "%", "%25")
	uriPath = strings.ReplaceAll(uriPath, "?", "%3F")
	uriPath = strings.ReplaceAll(uriPath, "#", "%23")
	database, err := sql.Open("sqlite", "file:"+uriPath+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("open KOReader statistics database read-only: %w", err)
	}
	database.SetMaxOpenConns(1)
	return database, nil
}

func schemaVersion(ctx context.Context, database *sql.DB) (int64, error) {
	var version int64
	if err := database.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return 0, fmt.Errorf("read KOReader SQLite schema version: %w", err)
	}
	return version, nil
}

func verifySQLiteQuickCheck(ctx context.Context, database *sql.DB) error {
	var result string
	if err := database.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&result); err != nil {
		return fmt.Errorf("check KOReader SQLite snapshot integrity: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("KOReader SQLite snapshot integrity check failed: %s", result)
	}
	return nil
}

func requireStatisticsSchema(ctx context.Context, database *sql.DB) error {
	rows, err := database.QueryContext(ctx, `
		SELECT name, type
		FROM sqlite_master
		WHERE name IN ('book', 'page_stat_data')
		ORDER BY name`)
	if err != nil {
		return fmt.Errorf("inspect KOReader statistics schema: %w", err)
	}
	defer rows.Close()
	seen := map[string]string{}
	for rows.Next() {
		var name, kind string
		if err := rows.Scan(&name, &kind); err != nil {
			return fmt.Errorf("scan KOReader statistics schema: %w", err)
		}
		seen[name] = kind
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, name := range []string{"book", "page_stat_data"} {
		if seen[name] != "table" {
			return fmt.Errorf("KOReader statistics database lacks table %s", name)
		}
	}
	return nil
}

func enforceRowLimit(ctx context.Context, database *sql.DB, table string, limit int) error {
	var count int
	if err := database.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
		return fmt.Errorf("count KOReader %s rows: %w", table, err)
	}
	if count > limit {
		return fmt.Errorf("KOReader %s contains %d rows, over limit %d", table, count, limit)
	}
	return nil
}

func importBooks(ctx context.Context, database *sql.DB, localQueue *queue.Queue, options ImportOptions, databaseSchemaVersion int64, snapshot sourceSnapshot, result *ImportResult) error {
	rows, err := database.QueryContext(ctx, `
		SELECT id, title, authors, notes, last_open, highlights, pages,
		       series, language, md5, total_read_time, total_read_pages
		FROM book
		ORDER BY id`)
	if err != nil {
		return fmt.Errorf("read KOReader books: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row bookRow
		if err := rows.Scan(&row.ID, &row.Title, &row.Authors, &row.Notes, &row.LastOpen, &row.Highlights, &row.Pages, &row.Series, &row.Language, &row.MD5, &row.TotalReadTime, &row.TotalReadPages); err != nil {
			return fmt.Errorf("scan KOReader book: %w", err)
		}
		result.BooksSeen++
		if !row.LastOpen.Valid || row.LastOpen.Int64 <= 0 {
			result.BooksWithoutLastOpen++
			continue
		}
		occurredAt, err := unixSeconds(row.LastOpen.Int64)
		if err != nil {
			return fmt.Errorf("KOReader book %d last_open: %w", row.ID, err)
		}
		bookKey, identityQuality := bookIdentity(row)
		payload := map[string]any{
			"table":            "book",
			"id":               row.ID,
			"book_identity":    bookKey,
			"identity_quality": identityQuality,
			"title":            nullableString(row.Title),
			"authors":          nullableString(row.Authors),
			"notes":            nullableInt(row.Notes),
			"last_open":        nullableInt(row.LastOpen),
			"highlights":       nullableInt(row.Highlights),
			"pages":            nullableInt(row.Pages),
			"series":           nullableString(row.Series),
			"language":         nullableString(row.Language),
			"md5":              nullableString(row.MD5),
			"total_read_time":  nullableInt(row.TotalReadTime),
			"total_read_pages": nullableInt(row.TotalReadPages),
			"time_basis":       "unix_seconds",
		}
		observation, err := makeObservation(options, "statistics", bookKey, "koreader.book", fmt.Sprintf("koreader-statistics-sqlite-%d", databaseSchemaVersion), occurredAt, nil, payload)
		if err != nil {
			return err
		}
		inserted, err := enqueueWithMembership(ctx, localQueue, options, snapshot, "statistics", observation, result, nil)
		if err != nil {
			return fmt.Errorf("enqueue KOReader book %d: %w", row.ID, err)
		}
		if inserted {
			result.BooksQueued++
		}
	}
	return rows.Err()
}

func importPageStats(ctx context.Context, database *sql.DB, localQueue *queue.Queue, options ImportOptions, databaseSchemaVersion int64, snapshot sourceSnapshot, result *ImportResult) error {
	bookKeys, err := loadBookIdentities(ctx, database)
	if err != nil {
		return err
	}
	rows, err := database.QueryContext(ctx, `
		SELECT id_book, page, start_time, duration, total_pages
		FROM page_stat_data
		ORDER BY id_book, page, start_time`)
	if err != nil {
		return fmt.Errorf("read KOReader page statistics: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row pageStatRow
		if err := rows.Scan(&row.BookID, &row.Page, &row.StartTime, &row.Duration, &row.TotalPages); err != nil {
			return fmt.Errorf("scan KOReader page statistic: %w", err)
		}
		result.PageStatsSeen++
		occurredAt, err := unixSeconds(row.StartTime)
		if err != nil {
			return fmt.Errorf("KOReader page statistic start_time: %w", err)
		}
		if row.Duration < 0 || row.Duration > int64(math.MaxInt64)/int64(time.Second) {
			return fmt.Errorf("KOReader page statistic has invalid duration %d", row.Duration)
		}
		endedAt := occurredAt.Add(time.Duration(row.Duration) * time.Second)
		bookKey := bookKeys[row.BookID]
		if bookKey == "" {
			return fmt.Errorf("KOReader page statistic refers to unknown book %d", row.BookID)
		}
		payload := map[string]any{
			"table":         "page_stat_data",
			"id_book":       row.BookID,
			"book_identity": bookKey,
			"page":          row.Page,
			"start_time":    row.StartTime,
			"duration":      row.Duration,
			"total_pages":   row.TotalPages,
			"time_basis":    "unix_seconds",
		}
		key := fmt.Sprintf("page_stat_data:%s:%d:%d", bookKey, row.Page, row.StartTime)
		observation, err := makeObservation(options, "statistics", key, "koreader.page_stat_data", fmt.Sprintf("koreader-statistics-sqlite-%d", databaseSchemaVersion), occurredAt, &endedAt, payload)
		if err != nil {
			return err
		}
		inserted, err := enqueueWithMembership(ctx, localQueue, options, snapshot, "statistics", observation, result, nil)
		if err != nil {
			return fmt.Errorf("enqueue KOReader page statistic: %w", err)
		}
		if inserted {
			result.PageStatsQueued++
		}
	}
	return rows.Err()
}

func loadBookIdentities(ctx context.Context, database *sql.DB) (map[int64]string, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, title, authors, md5
		FROM book
		ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("read KOReader book identities: %w", err)
	}
	defer rows.Close()
	identities := make(map[int64]string)
	for rows.Next() {
		var row struct {
			ID      int64
			Title   sql.NullString
			Authors sql.NullString
			MD5     sql.NullString
		}
		if err := rows.Scan(&row.ID, &row.Title, &row.Authors, &row.MD5); err != nil {
			return nil, fmt.Errorf("scan KOReader book identity: %w", err)
		}
		key, _ := bookIdentity(bookRow{ID: row.ID, Title: row.Title, Authors: row.Authors, MD5: row.MD5})
		identities[row.ID] = key
	}
	return identities, rows.Err()
}

func bookIdentity(row bookRow) (string, string) {
	// KOReader declares book.id as INTEGER PRIMARY KEY AUTOINCREMENT. The
	// caller scopes this key by the explicit source generation, so a reset or
	// replacement must use a new generation before IDs can be reused.
	return fmt.Sprintf("book:id:%d", row.ID), "native_row_id_generation_scoped"
}

func nullableStringValue(value sql.NullString) string {
	if value.Valid {
		return value.String
	}
	return ""
}

func importHistory(ctx context.Context, localQueue *queue.Queue, options ImportOptions, snapshot sourceSnapshot, entries []HistoryEntry, result *ImportResult) error {
	duplicateOrdinals := map[string]int{}
	for _, entry := range entries {
		occurredAt, err := unixSeconds(entry.TimeUnix)
		if err != nil {
			return fmt.Errorf("KOReader history entry %d: %w", entry.Index, err)
		}
		identity := fmt.Sprintf("%d\x00%s", entry.TimeUnix, entry.File)
		duplicateOrdinals[identity]++
		hash := sha256.Sum256([]byte(identity))
		key := fmt.Sprintf("history:%s:%d", hex.EncodeToString(hash[:]), duplicateOrdinals[identity])
		payload := map[string]any{
			"table":      "history.lua",
			"time":       entry.TimeUnix,
			"file":       entry.File,
			"time_basis": "unix_seconds",
		}
		observation, err := makeObservation(options, "history", key, "koreader.history", "koreader-history-lua-v1", occurredAt, nil, payload)
		if err != nil {
			return err
		}
		inserted, err := enqueueWithMembership(ctx, localQueue, options, snapshot, "history", observation, result, &entry.Index)
		if err != nil {
			return fmt.Errorf("enqueue KOReader history entry %d: %w", entry.Index, err)
		}
		if inserted {
			result.HistoryQueued++
		}
	}
	return nil
}

func makeObservation(options ImportOptions, source, key, kind, sourceSchemaVersion string, occurredAt time.Time, endedAt *time.Time, payload map[string]any) (model.Observation, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return model.Observation{}, fmt.Errorf("encode KOReader %s payload: %w", kind, err)
	}
	observation := model.Observation{
		SourceInstanceKey:   fmt.Sprintf("koreader:%s:%s:%s", options.DeviceID, options.SourceGeneration, source),
		SourceKind:          "koreader",
		DeviceID:            options.DeviceID,
		SourceRecordKey:     key,
		Kind:                kind,
		OccurredAt:          occurredAt,
		EndedAt:             endedAt,
		CollectedAt:         options.Now,
		SourceSchemaVersion: sourceSchemaVersion,
		OriginalPayload:     encoded,
	}
	if err := observation.Normalize(); err != nil {
		return model.Observation{}, fmt.Errorf("normalize KOReader %s observation: %w", kind, err)
	}
	return observation, nil
}

func enqueueWithMembership(ctx context.Context, localQueue *queue.Queue, options ImportOptions, snapshot sourceSnapshot, source string, observation model.Observation, result *ImportResult, sourcePosition *int) (bool, error) {
	inserted, err := localQueue.Enqueue(ctx, observation, options.Now)
	if err != nil {
		return false, fmt.Errorf("enqueue KOReader %s: %w", observation.Kind, err)
	}
	if inserted {
		result.RevisionsEnqueued++
	}
	membership, err := makeMembershipObservation(options, snapshot, source, observation, sourcePosition)
	if err != nil {
		return inserted, err
	}
	membershipInserted, err := localQueue.Enqueue(ctx, membership, options.Now)
	if err != nil {
		return inserted, fmt.Errorf("enqueue KOReader snapshot membership: %w", err)
	}
	if membershipInserted {
		result.MembershipsQueued++
	}
	return inserted, nil
}

func makeMembershipObservation(options ImportOptions, snapshot sourceSnapshot, source string, observation model.Observation, sourcePosition *int) (model.Observation, error) {
	file, ok := snapshot.archiveFile(source)
	if !ok {
		return model.Observation{}, fmt.Errorf("KOReader snapshot has no archive file for %s", source)
	}
	identity := snapshot.Manifest.SnapshotID + "\x00" + observation.SourceInstanceKey + "\x00" + observation.SourceRecordKey + "\x00" + observation.RevisionDigest
	hash := sha256.Sum256([]byte(identity))
	payload := map[string]any{
		"snapshot_id":             snapshot.Manifest.SnapshotID,
		"manifest_path":           snapshot.ManifestPath,
		"snapshot_mode":           snapshot.Manifest.SnapshotMode,
		"deletion_handling":       snapshot.Manifest.DeletionHandling,
		"source_generation":       snapshot.Manifest.SourceGeneration,
		"source_role":             file.Role,
		"source_path":             file.Path,
		"source_sha256":           file.SHA256,
		"archive_path":            file.ArchivePath,
		"archive_size_bytes":      file.SizeBytes,
		"source_instance_key":     observation.SourceInstanceKey,
		"source_record_key":       observation.SourceRecordKey,
		"source_observation_kind": observation.Kind,
		"source_revision_digest":  observation.RevisionDigest,
	}
	if sourcePosition != nil {
		payload["source_position"] = *sourcePosition
	}
	return makeObservation(options, "snapshot", "membership:"+hex.EncodeToString(hash[:]), "koreader.snapshot_membership", "koreader-snapshot-membership-v1", options.Now.UTC(), nil, payload)
}

func unixSeconds(value int64) (time.Time, error) {
	if value <= 0 {
		return time.Time{}, fmt.Errorf("Unix timestamp must be positive, got %d", value)
	}
	if value > math.MaxInt64/int64(time.Second) {
		return time.Time{}, fmt.Errorf("Unix timestamp is outside supported range: %d", value)
	}
	return time.Unix(value, 0).UTC(), nil
}

func nullableString(value sql.NullString) any {
	if value.Valid {
		return value.String
	}
	return nil
}

func nullableInt(value sql.NullInt64) any {
	if value.Valid {
		return value.Int64
	}
	return nil
}

func readBoundedFile(path string, maxBytes int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("source is not a regular file: %s", path)
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("source is %d bytes, over limit %d", info.Size(), maxBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	limit := maxBytes
	if limit < math.MaxInt64 {
		limit++
	}
	contents, err := io.ReadAll(io.LimitReader(file, limit))
	if err != nil {
		return nil, err
	}
	if int64(len(contents)) > maxBytes {
		return nil, fmt.Errorf("source exceeds limit %d bytes", maxBytes)
	}
	return contents, nil
}

func parserElementLimit(maxRows int) int {
	if maxRows >= maxParserElements/4 {
		return maxParserElements
	}
	return maxRows * 4
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func boundedFileDigest(path string, maxBytes int64) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	limit := maxBytes
	if limit < math.MaxInt64 {
		limit++
	}
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(file, limit))
	if err != nil {
		return "", size, err
	}
	if size > maxBytes {
		return "", size, fmt.Errorf("file exceeds limit %d bytes", maxBytes)
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func manifestDigest(path string, maxBytes int64) (string, error) {
	digest, _, err := boundedFileDigest(path, maxBytes)
	return digest, err
}

func cursorKey(deviceID, sourceGeneration, databasePath string) string {
	hash := sha256.Sum256([]byte(databasePath))
	generationHash := sha256.Sum256([]byte(sourceGeneration))
	return "koreader.snapshot." + deviceID + "." + hex.EncodeToString(generationHash[:]) + "." + hex.EncodeToString(hash[:])
}
