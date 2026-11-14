package collector

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rimexe0/telemetry/internal/model"
)

// BrowserHistory copies each visit from the history the browsers keep
// themselves. That history lasts about 90 days and is written whether or not
// this collector runs, so web use is never lost to the collector being
// stopped, broken or uninstalled: the next run reads everything it missed.
// Each history file has its own position in the queue's state, so a visit
// is read once, and a restart neither skips nor repeats any.
//
// It complements the extension's tab records: those give exact time in
// front; these give every page opened, also in windows the extension could
// not see.
type BrowserHistory struct {
	// Roots are the folders holding browser profiles; empty means this
	// machine's usual ones.
	Roots    []string
	Interval time.Duration

	health Health
	mu     sync.Mutex
	files  int
	sent   int
}

const browserHistorySchemaVersion = "chromium-history-1"

// browserHistoryBatch is how many visits are read at a time.
const browserHistoryBatch = 2000

// chromeEpochOffset is the microseconds from 1601, where Chromium's
// timestamps start, to 1970.
const chromeEpochOffset = 11644473600 * 1000000

func (*BrowserHistory) Name() string { return "browser-history" }

func (b *BrowserHistory) Status() map[string]any {
	status := b.health.Fields()
	b.mu.Lock()
	defer b.mu.Unlock()
	status["history_files"] = b.files
	status["visits_sent"] = b.sent
	return status
}

// browserRoots are the Chromium-family browsers' data folders.
func browserRoots() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	if runtime.GOOS == "darwin" {
		base := filepath.Join(home, "Library", "Application Support")
		return []string{
			filepath.Join(base, "net.imput.helium"), filepath.Join(base, "Google", "Chrome"), filepath.Join(base, "Chromium"),
			filepath.Join(base, "BraveSoftware", "Brave-Browser"), filepath.Join(base, "Vivaldi"), filepath.Join(base, "Arc", "User Data"),
			filepath.Join(base, "Microsoft Edge"),
		}
	}
	base := filepath.Join(home, ".config")
	return []string{
		filepath.Join(base, "net.imput.helium"), filepath.Join(base, "google-chrome"), filepath.Join(base, "chromium"),
		filepath.Join(base, "BraveSoftware", "Brave-Browser"), filepath.Join(base, "vivaldi"), filepath.Join(base, "microsoft-edge"),
	}
}

func (b *BrowserHistory) Run(ctx context.Context, host Host) error {
	if b.Interval == 0 {
		b.Interval = 10 * time.Minute
	}
	if len(b.Roots) == 0 {
		b.Roots = browserRoots()
	}
	ticker := time.NewTicker(b.Interval)
	defer ticker.Stop()
	for {
		if err := b.collect(ctx, host, time.Now().UTC()); err != nil {
			b.health.Failed(time.Now(), err)
		} else {
			b.health.Succeeded(time.Now())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// historyFiles finds every profile's History file under the roots.
func historyFiles(roots []string) []string {
	files := []string{}
	for _, root := range roots {
		matches, _ := filepath.Glob(filepath.Join(root, "*", "History"))
		files = append(files, matches...)
	}
	return files
}

func (b *BrowserHistory) collect(ctx context.Context, host Host, now time.Time) error {
	files := historyFiles(b.Roots)
	b.mu.Lock()
	b.files = len(files)
	b.mu.Unlock()
	failures := []string{}
	for _, file := range files {
		if err := b.collectFile(ctx, host, file, now); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", file, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("browser history: %s", strings.Join(failures, "; "))
	}
	return nil
}

// browserAndProfile names a History file: ".../net.imput.helium/Profile 1/History"
// is browser "net.imput.helium", profile "Profile 1".
func browserAndProfile(roots []string, file string) (string, string) {
	profileDir := filepath.Dir(file)
	profile := filepath.Base(profileDir)
	for _, root := range roots {
		if filepath.Dir(profileDir) == root {
			if home, err := os.UserHomeDir(); err == nil {
				for _, base := range []string{filepath.Join(home, "Library", "Application Support"), filepath.Join(home, ".config")} {
					if relative, err := filepath.Rel(base, root); err == nil && !strings.HasPrefix(relative, "..") {
						return filepath.ToSlash(relative), profile
					}
				}
			}
			return filepath.Base(root), profile
		}
	}
	return filepath.Base(filepath.Dir(profileDir)), profile
}

// collectFile reads the visits after this file's position. The browser
// keeps History open, so a copy is read.
func (b *BrowserHistory) collectFile(ctx context.Context, host Host, file string, now time.Time) error {
	cursorKey := "browser-history:" + file
	value, _, err := host.Queue.LoadCursor(ctx, cursorKey)
	if err != nil {
		return err
	}
	last, _ := strconv.ParseInt(value, 10, 64)

	copyDir, err := os.MkdirTemp("", "telemetry-history-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(copyDir)
	copied := filepath.Join(copyDir, "History")
	if err := copyFile(file, copied); err != nil {
		return err
	}
	// A journal beside it holds writes not yet in the file.
	for _, suffix := range []string{"-wal", "-journal"} {
		if _, err := os.Stat(file + suffix); err == nil {
			_ = copyFile(file+suffix, copied+suffix)
		}
	}
	db, err := sql.Open("sqlite", "file:"+copied+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()

	browser, profile := browserAndProfile(b.Roots, file)
	instance := "browser-history:" + host.DeviceID + ":" + browser + ":" + profile
	for {
		rows, err := db.QueryContext(ctx, `SELECT v.id, v.visit_time, v.visit_duration, v.transition, v.from_visit, u.url, u.title
			FROM visits v JOIN urls u ON u.id = v.url WHERE v.id > ? ORDER BY v.id LIMIT ?`, last, browserHistoryBatch)
		if err != nil {
			return err
		}
		count := 0
		for rows.Next() {
			var id, visitTime, duration, transition, fromVisit int64
			var address, title string
			if err := rows.Scan(&id, &visitTime, &duration, &transition, &fromVisit, &address, &title); err != nil {
				rows.Close()
				return err
			}
			count++
			at := time.UnixMicro(visitTime - chromeEpochOffset).UTC()
			payload, _ := json.Marshal(map[string]any{"visit_id": id, "url": address, "title": title, "visit_duration_us": duration,
				"transition": transition, "from_visit": fromVisit, "browser": browser, "profile": profile})
			observation := model.Observation{SourceInstanceKey: instance, SourceKind: "browser_history", DeviceID: host.DeviceID,
				SourceRecordKey: "visit:" + strconv.FormatInt(id, 10), Kind: "browser.visit", OccurredAt: at, CollectedAt: now,
				SourceSchemaVersion: browserHistorySchemaVersion, OriginalPayload: payload}
			if duration > 0 {
				end := at.Add(time.Duration(duration) * time.Microsecond)
				observation.EndedAt = &end
			}
			if err := observation.Normalize(); err != nil {
				rows.Close()
				return err
			}
			if _, err := host.Queue.Enqueue(ctx, observation, now); err != nil {
				rows.Close()
				return err
			}
			last = id
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if count > 0 {
			if err := host.Queue.SaveCursor(ctx, cursorKey, strconv.FormatInt(last, 10), now); err != nil {
				return err
			}
			b.mu.Lock()
			b.sent += count
			b.mu.Unlock()
		}
		if count < browserHistoryBatch {
			return nil
		}
	}
}

func copyFile(from, to string) error {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.Create(to)
	if err != nil {
		return err
	}
	if _, err := io.Copy(target, source); err != nil {
		target.Close()
		return err
	}
	return target.Close()
}
