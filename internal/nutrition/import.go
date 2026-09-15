// Package nutrition imports the lossless JSON export produced by the
// OpenNutriTracker fork. It reads a user-selected export archive, preserves
// that archive as an exact local snapshot, and queues bounded observations for
// later delivery. The fork and its source files remain read-only.
package nutrition

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rimexe0/telemetry/internal/model"
	"github.com/rimexe0/telemetry/internal/queue"
)

const (
	ExportSchemaVersion  = "opennutritracker-export-v1"
	defaultMaxRecords    = 100000
	defaultMaxFileBytes  = 256 << 20
	defaultMaxEntryBytes = 128 << 20
	maxArchiveBytes      = 1 << 30
	maxRecordsLimit      = 1000000
	maxPathBytes         = 4096
)

// ImportOptions selects one explicit OpenNutriTracker export and the queue
// receiving its observations. Timezone is required because the app's
// DateTime.toIso8601String export can omit an offset.
type ImportOptions struct {
	DeviceID         string
	SourceGeneration string
	ExportPath       string
	ArchiveDir       string
	Timezone         string
	MaxRecords       int
	MaxFileBytes     int64
	MaxEntryBytes    int64
	Now              time.Time
}

// ImportResult reports one bounded source pass without delivering the queue.
type ImportResult struct {
	DeviceID          string         `json:"device_id"`
	SourceGeneration  string         `json:"source_generation"`
	ExportPath        string         `json:"export_path"`
	ArchivePath       string         `json:"archive_path"`
	ArchiveSHA256     string         `json:"archive_sha256"`
	ArchiveSizeBytes  int64          `json:"archive_size_bytes"`
	ManifestPath      string         `json:"manifest_path"`
	RecordsSeen       int            `json:"records_seen"`
	RevisionsEnqueued int            `json:"revisions_enqueued"`
	MembershipsQueued int            `json:"snapshot_memberships_queued"`
	ByKind            map[string]int `json:"by_kind"`
	FilesPresent      []string       `json:"files_present"`
	CursorAdvanced    bool           `json:"cursor_advanced"`
}

type archiveManifest struct {
	Version          int            `json:"version"`
	SnapshotID       string         `json:"snapshot_id"`
	DeviceID         string         `json:"device_id"`
	SourceGeneration string         `json:"source_generation"`
	SourcePath       string         `json:"source_path"`
	ArchivePath      string         `json:"archive_path"`
	ArchiveSHA256    string         `json:"archive_sha256"`
	ArchiveSizeBytes int64          `json:"archive_size_bytes"`
	SchemaVersion    string         `json:"schema_version"`
	Entries          []archiveEntry `json:"entries"`
}

type archiveEntry struct {
	Name              string `json:"name"`
	CompressedBytes   uint64 `json:"compressed_bytes"`
	UncompressedBytes uint64 `json:"uncompressed_bytes"`
}

type recordSpec struct {
	Name       string
	Kind       string
	KeyField   string
	TimeFields []string
	Optional   bool
}

var exportRecordSpecs = []recordSpec{
	{Name: "user_intake.json", Kind: "opennutritracker.intake", KeyField: "id", TimeFields: []string{"dateTime"}},
	{Name: "user_activity.json", Kind: "opennutritracker.activity", KeyField: "id", TimeFields: []string{"date"}},
	{Name: "user_tracked_day.json", Kind: "opennutritracker.tracked_day", KeyField: "day", TimeFields: []string{"day"}},
	{Name: "user_recipes.json", Kind: "opennutritracker.recipe", KeyField: "id", TimeFields: []string{"updatedAt", "createdAt"}, Optional: true},
	{Name: "weight_log.json", Kind: "opennutritracker.weight_log", KeyField: "date", TimeFields: []string{"date"}, Optional: true},
}

// Import snapshots and reads the selected export. It never writes to the
// OpenNutriTracker path, calls its backend, or delivers anything centrally.
func Import(ctx context.Context, localQueue *queue.Queue, options ImportOptions) (ImportResult, error) {
	result := ImportResult{DeviceID: options.DeviceID, SourceGeneration: options.SourceGeneration, ByKind: map[string]int{}}
	if localQueue == nil {
		return result, errors.New("queue is required")
	}
	if err := validateTextOption("device ID", options.DeviceID, 128); err != nil {
		return result, err
	}
	if err := validateTextOption("source generation", options.SourceGeneration, 128); err != nil {
		return result, err
	}
	if options.MaxRecords == 0 {
		options.MaxRecords = defaultMaxRecords
	}
	if options.MaxRecords < 1 || options.MaxRecords > maxRecordsLimit {
		return result, fmt.Errorf("maximum records must be between 1 and %d", maxRecordsLimit)
	}
	if options.MaxFileBytes == 0 {
		options.MaxFileBytes = defaultMaxFileBytes
	}
	if options.MaxFileBytes < 1 || options.MaxFileBytes > maxArchiveBytes {
		if options.MaxFileBytes > maxArchiveBytes {
			return result, fmt.Errorf("maximum archive bytes cannot exceed %d", maxArchiveBytes)
		}
		return result, errors.New("maximum archive bytes must be positive")
	}
	if options.MaxEntryBytes == 0 {
		options.MaxEntryBytes = defaultMaxEntryBytes
	}
	if options.MaxEntryBytes < 1 || options.MaxEntryBytes > options.MaxFileBytes {
		return result, errors.New("maximum ZIP entry bytes is invalid")
	}
	if strings.TrimSpace(options.Timezone) == "" {
		return result, errors.New("timezone is required")
	}
	location, err := time.LoadLocation(options.Timezone)
	if err != nil {
		return result, fmt.Errorf("load timezone %q: %w", options.Timezone, err)
	}
	if options.Now.IsZero() {
		options.Now = time.Now().UTC()
	} else {
		options.Now = options.Now.UTC()
	}
	exportPath, err := explicitRegularFile(options.ExportPath, "export")
	if err != nil {
		return result, err
	}
	archiveDir, err := explicitDirectory(options.ArchiveDir)
	if err != nil {
		return result, err
	}
	result.ExportPath = exportPath

	manifest, manifestPath, err := snapshotExport(exportPath, archiveDir, options)
	if err != nil {
		return result, err
	}
	result.ArchivePath = manifest.ArchivePath
	result.ArchiveSHA256 = manifest.ArchiveSHA256
	result.ArchiveSizeBytes = manifest.ArchiveSizeBytes
	result.ManifestPath = manifestPath

	reader, err := zip.OpenReader(manifest.ArchivePath)
	if err != nil {
		return result, fmt.Errorf("open OpenNutriTracker export archive: %w", err)
	}
	defer reader.Close()
	entries := make(map[string]*zip.File, len(reader.File))
	for _, entry := range reader.File {
		entries[entry.Name] = entry
	}
	sourceInstance := "opennutritracker:" + options.DeviceID + ":" + options.SourceGeneration
	for _, spec := range exportRecordSpecs {
		entry, present := entries[spec.Name]
		if !present {
			if spec.Optional {
				continue
			}
			return result, fmt.Errorf("required export entry %s is missing", spec.Name)
		}
		if entry.FileInfo().IsDir() {
			return result, fmt.Errorf("export entry %s is a directory", spec.Name)
		}
		data, err := readEntryBounded(entry, options.MaxEntryBytes)
		if err != nil {
			return result, fmt.Errorf("read export entry %s: %w", spec.Name, err)
		}
		records, err := decodeRecordArray(data, options.MaxRecords)
		if err != nil {
			return result, fmt.Errorf("decode export entry %s: %w", spec.Name, err)
		}
		for index, raw := range records {
			key, occurredAt, occurredAtKind, err := recordIdentity(raw, spec, location, options.Now)
			if err != nil {
				return result, fmt.Errorf("%s record %d: %w", spec.Name, index+1, err)
			}
			observation := model.Observation{
				SourceInstanceKey:   sourceInstance,
				SourceKind:          "opennutritracker",
				DeviceID:            options.DeviceID,
				SourceRecordKey:     spec.Kind + ":" + key,
				Kind:                spec.Kind,
				OccurredAt:          occurredAt,
				CollectedAt:         options.Now,
				Timezone:            options.Timezone,
				SourceSchemaVersion: ExportSchemaVersion,
				OriginalPayload:     append(json.RawMessage(nil), raw...),
			}
			if err := observation.Normalize(); err != nil {
				return result, fmt.Errorf("normalize %s record %d: %w", spec.Name, index+1, err)
			}
			inserted, err := localQueue.Enqueue(ctx, observation, options.Now)
			if err != nil {
				return result, fmt.Errorf("enqueue %s record %d: %w", spec.Name, index+1, err)
			}
			result.RecordsSeen++
			result.ByKind[spec.Kind]++
			if inserted {
				result.RevisionsEnqueued++
			}
			membership, err := makeMembershipObservation(options, manifest, manifestPath, spec.Name, index+1, observation, occurredAtKind)
			if err != nil {
				return result, fmt.Errorf("enqueue %s snapshot membership: %w", spec.Name, err)
			}
			membershipInserted, err := localQueue.Enqueue(ctx, membership, options.Now)
			if err != nil {
				return result, fmt.Errorf("enqueue %s snapshot membership: %w", spec.Name, err)
			}
			if membershipInserted {
				result.MembershipsQueued++
			}
		}
		result.FilesPresent = append(result.FilesPresent, spec.Name)
	}
	sort.Strings(result.FilesPresent)
	cursorKey := "opennutritracker.snapshot." + options.DeviceID + "." + options.SourceGeneration
	cursor := map[string]any{
		"manifest_path":  manifestPath,
		"snapshot_id":    manifest.SnapshotID,
		"archive_sha256": manifest.ArchiveSHA256,
		"captured_at":    options.Now.Format(time.RFC3339Nano),
	}
	encodedCursor, err := json.Marshal(cursor)
	if err != nil {
		return result, fmt.Errorf("encode OpenNutriTracker cursor: %w", err)
	}
	if err := localQueue.SaveCursor(ctx, cursorKey, string(encodedCursor), options.Now); err != nil {
		return result, fmt.Errorf("save OpenNutriTracker cursor: %w", err)
	}
	result.CursorAdvanced = true
	return result, nil
}

func validateTextOption(name, value string, maxBytes int) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	if len(value) > maxBytes || strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("%s must be at most %d bytes and contain no NUL", name, maxBytes)
	}
	return nil
}

func explicitRegularFile(path, label string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("%s path is required", label)
	}
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("resolve %s path: %w", label, err)
	}
	if len(absolute) > maxPathBytes {
		return "", fmt.Errorf("%s path is too long", label)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", fmt.Errorf("inspect %s path: %w", label, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s path must be a regular non-symlink file", label)
	}
	return absolute, nil
}

func explicitDirectory(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("archive directory is required")
	}
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("resolve archive directory: %w", err)
	}
	if len(absolute) > maxPathBytes {
		return "", errors.New("archive directory path is too long")
	}
	info, err := os.Lstat(absolute)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("archive directory is not a real directory")
		}
		if info.Mode().Perm()&0o077 != 0 {
			return "", fmt.Errorf("existing archive directory must be private (mode 700): %s", absolute)
		}
		return absolute, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect archive directory: %w", err)
	}
	parent := filepath.Dir(absolute)
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return "", fmt.Errorf("inspect archive directory parent: %w", err)
	}
	if parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() {
		return "", errors.New("archive directory parent is not a real directory")
	}
	if err := os.Mkdir(absolute, 0o700); err != nil {
		return "", fmt.Errorf("create archive directory: %w", err)
	}
	return absolute, nil
}

func snapshotExport(exportPath, archiveDir string, options ImportOptions) (archiveManifest, string, error) {
	input, err := os.Open(exportPath)
	if err != nil {
		return archiveManifest{}, "", fmt.Errorf("open OpenNutriTracker export: %w", err)
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return archiveManifest{}, "", fmt.Errorf("stat OpenNutriTracker export: %w", err)
	}
	if info.Size() > options.MaxFileBytes {
		return archiveManifest{}, "", fmt.Errorf("export is %d bytes, over limit %d", info.Size(), options.MaxFileBytes)
	}
	temporary, err := os.CreateTemp(archiveDir, ".opennutritracker-export-*")
	if err != nil {
		return archiveManifest{}, "", fmt.Errorf("create export snapshot: %w", err)
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return archiveManifest{}, "", fmt.Errorf("protect export snapshot: %w", err)
	}
	hash := sha256.New()
	limit := options.MaxFileBytes
	if limit < math.MaxInt64 {
		limit++
	}
	written, err := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(input, limit))
	if err != nil {
		_ = temporary.Close()
		return archiveManifest{}, "", fmt.Errorf("copy export snapshot: %w", err)
	}
	if written > options.MaxFileBytes {
		_ = temporary.Close()
		return archiveManifest{}, "", fmt.Errorf("export exceeds limit %d", options.MaxFileBytes)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return archiveManifest{}, "", fmt.Errorf("sync export snapshot: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return archiveManifest{}, "", fmt.Errorf("close export snapshot: %w", err)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	sourceDigest, sourceSize, err := fileDigest(exportPath, options.MaxFileBytes)
	if err != nil {
		return archiveManifest{}, "", fmt.Errorf("recheck OpenNutriTracker export: %w", err)
	}
	if sourceDigest != digest || sourceSize != written {
		return archiveManifest{}, "", errors.New("OpenNutriTracker export changed during snapshot")
	}
	archivePath := filepath.Join(archiveDir, "export-"+digest+".zip")
	if existing, err := os.Lstat(archivePath); err == nil {
		if existing.Mode()&os.ModeSymlink != 0 {
			return archiveManifest{}, "", fmt.Errorf("existing export archive is a symlink: %s", archivePath)
		}
		if !existing.Mode().IsRegular() || existing.Mode().Perm()&0o077 != 0 {
			return archiveManifest{}, "", fmt.Errorf("existing export archive is not private: %s", archivePath)
		}
		actual, size, err := fileDigest(archivePath, options.MaxFileBytes)
		if err != nil || actual != digest || size != written {
			return archiveManifest{}, "", fmt.Errorf("existing export archive conflicts with source snapshot")
		}
		_ = os.Remove(temporaryPath)
		removeTemporary = false
	} else if errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(temporaryPath, archivePath); err != nil {
			return archiveManifest{}, "", fmt.Errorf("install export snapshot: %w", err)
		}
		removeTemporary = false
		if err := os.Chmod(archivePath, 0o600); err != nil {
			return archiveManifest{}, "", fmt.Errorf("protect installed export snapshot: %w", err)
		}
	} else {
		return archiveManifest{}, "", fmt.Errorf("inspect export snapshot path: %w", err)
	}
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return archiveManifest{}, "", fmt.Errorf("validate export ZIP: %w", err)
	}
	defer reader.Close()
	manifest := archiveManifest{
		Version: 1, SnapshotID: digest, DeviceID: options.DeviceID,
		SourceGeneration: options.SourceGeneration, SourcePath: exportPath,
		ArchivePath: archivePath, ArchiveSHA256: digest, ArchiveSizeBytes: written,
		SchemaVersion: ExportSchemaVersion,
		Entries:       make([]archiveEntry, 0, len(reader.File)),
	}
	seen := make(map[string]struct{}, len(reader.File))
	var totalUncompressed uint64
	for _, entry := range reader.File {
		if err := validateZipName(entry.Name); err != nil {
			return archiveManifest{}, "", err
		}
		if _, exists := seen[entry.Name]; exists {
			return archiveManifest{}, "", fmt.Errorf("duplicate ZIP entry %s", entry.Name)
		}
		seen[entry.Name] = struct{}{}
		if entry.FileInfo().Mode()&os.ModeSymlink != 0 {
			return archiveManifest{}, "", fmt.Errorf("ZIP entry %s is a symlink", entry.Name)
		}
		if entry.UncompressedSize64 > uint64(options.MaxEntryBytes) {
			return archiveManifest{}, "", fmt.Errorf("ZIP entry %s exceeds limit %d", entry.Name, options.MaxEntryBytes)
		}
		if entry.UncompressedSize64 > uint64(options.MaxFileBytes) || totalUncompressed > uint64(options.MaxFileBytes)-entry.UncompressedSize64 {
			return archiveManifest{}, "", fmt.Errorf("ZIP uncompressed content exceeds limit %d", options.MaxFileBytes)
		}
		totalUncompressed += entry.UncompressedSize64
		manifest.Entries = append(manifest.Entries, archiveEntry{Name: entry.Name, CompressedBytes: entry.CompressedSize64, UncompressedBytes: entry.UncompressedSize64})
	}
	sort.Slice(manifest.Entries, func(i, j int) bool { return manifest.Entries[i].Name < manifest.Entries[j].Name })
	manifestPath := filepath.Join(archiveDir, "export-"+digest+".json")
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return archiveManifest{}, "", fmt.Errorf("encode export manifest: %w", err)
	}
	if info, err := os.Lstat(manifestPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
			return archiveManifest{}, "", fmt.Errorf("existing export manifest is not private: %s", manifestPath)
		}
		existing, err := os.ReadFile(manifestPath)
		if err != nil {
			return archiveManifest{}, "", fmt.Errorf("read export manifest: %w", err)
		}
		if !bytes.Equal(existing, encoded) {
			return archiveManifest{}, "", fmt.Errorf("export manifest conflicts with existing bytes")
		}
		return manifest, manifestPath, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return archiveManifest{}, "", fmt.Errorf("inspect export manifest: %w", err)
	}
	if err := writePrivateFile(manifestPath, encoded); err != nil {
		return archiveManifest{}, "", fmt.Errorf("write export manifest: %w", err)
	}
	return manifest, manifestPath, nil
}

func validateZipName(name string) error {
	if name == "" || strings.ContainsRune(name, '\x00') || strings.ContainsRune(name, '\\') || filepath.IsAbs(name) || strings.HasPrefix(name, "/") {
		return fmt.Errorf("unsafe ZIP entry %q", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return fmt.Errorf("unsafe ZIP entry %q", name)
		}
	}
	return nil
}

func readEntryBounded(entry *zip.File, maxBytes int64) ([]byte, error) {
	if entry.UncompressedSize64 > uint64(maxBytes) {
		return nil, fmt.Errorf("declares %d bytes, over limit %d", entry.UncompressedSize64, maxBytes)
	}
	reader, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("content exceeds limit %d", maxBytes)
	}
	return data, nil
}

func decodeRecordArray(data []byte, maxRecords int) ([]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != '[' {
		return nil, errors.New("expected a JSON array")
	}
	records := make([]json.RawMessage, 0)
	for decoder.More() {
		if len(records) >= maxRecords {
			return nil, fmt.Errorf("contains more than %d records", maxRecords)
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, err
		}
		records = append(records, append(json.RawMessage(nil), raw...))
	}
	token, err = decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, ok = token.(json.Delim)
	if !ok || delimiter != ']' {
		return nil, errors.New("expected end of JSON array")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("contains trailing JSON")
		}
		return nil, err
	}
	return records, nil
}

func recordIdentity(raw json.RawMessage, spec recordSpec, location *time.Location, collectedAt time.Time) (string, time.Time, string, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return "", time.Time{}, "", fmt.Errorf("record must be an object: %w", err)
	}
	var key string
	if value, ok := object[spec.KeyField]; !ok || json.Unmarshal(value, &key) != nil || strings.TrimSpace(key) == "" {
		return "", time.Time{}, "", fmt.Errorf("%s is required", spec.KeyField)
	}
	for _, field := range spec.TimeFields {
		value, ok := object[field]
		if !ok {
			continue
		}
		var textValue string
		if json.Unmarshal(value, &textValue) != nil || strings.TrimSpace(textValue) == "" {
			continue
		}
		parsed, err := parseExportTime(textValue, location)
		if err == nil {
			return key, parsed, "source_time", nil
		}
		return "", time.Time{}, "", fmt.Errorf("%s has invalid time %q: %w", field, textValue, err)
	}
	if spec.Optional {
		return key, collectedAt, "collection_time", nil
	}
	return "", time.Time{}, "", fmt.Errorf("one of %s is required", strings.Join(spec.TimeFields, ", "))
}

func parseExportTime(value string, location *time.Location) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.UTC(), nil
	}
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999", "2006-01-02"} {
		if parsed, err := time.ParseInLocation(layout, value, location); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, errors.New("expected ISO-8601 date/time")
}

func makeMembershipObservation(options ImportOptions, manifest archiveManifest, manifestPath, entryName string, row int, observation model.Observation, occurredAtKind string) (model.Observation, error) {
	identity := manifest.SnapshotID + "\x00" + observation.SourceInstanceKey + "\x00" + observation.SourceRecordKey + "\x00" + observation.RevisionDigest
	hash := sha256.Sum256([]byte(identity))
	payload := map[string]any{
		"snapshot_id":             manifest.SnapshotID,
		"manifest_path":           manifestPath,
		"source_generation":       manifest.SourceGeneration,
		"source_entry":            entryName,
		"source_row":              row,
		"source_path":             manifest.SourcePath,
		"source_sha256":           manifest.ArchiveSHA256,
		"archive_sha256":          manifest.ArchiveSHA256,
		"archive_path":            manifest.ArchivePath,
		"archive_size_bytes":      manifest.ArchiveSizeBytes,
		"source_instance_key":     observation.SourceInstanceKey,
		"source_record_key":       observation.SourceRecordKey,
		"source_observation_kind": observation.Kind,
		"source_revision_digest":  observation.RevisionDigest,
		"occurred_at_kind":        occurredAtKind,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return model.Observation{}, fmt.Errorf("encode snapshot membership: %w", err)
	}
	membership := model.Observation{
		SourceInstanceKey:   "opennutritracker:" + options.DeviceID + ":" + options.SourceGeneration + ":snapshot",
		SourceKind:          "opennutritracker",
		DeviceID:            options.DeviceID,
		SourceRecordKey:     "membership:" + hex.EncodeToString(hash[:]),
		Kind:                "opennutritracker.snapshot_membership",
		OccurredAt:          options.Now.UTC(),
		CollectedAt:         options.Now,
		Timezone:            options.Timezone,
		SourceSchemaVersion: "opennutritracker-snapshot-membership-v1",
		OriginalPayload:     encoded,
	}
	return membership, membership.Normalize()
}

func fileDigest(path string, maxBytes int64) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	count, err := io.Copy(io.MultiWriter(hash), io.LimitReader(file, maxBytes+1))
	if err != nil {
		return "", count, err
	}
	if count > maxBytes {
		return "", count, fmt.Errorf("file exceeds limit %d", maxBytes)
	}
	return hex.EncodeToString(hash.Sum(nil)), count, nil
}

func writePrivateFile(path string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".manifest-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	removeTemporary = false
	return os.Chmod(path, 0o600)
}
