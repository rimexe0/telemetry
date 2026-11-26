package collector

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rimexe0/telemetry/internal/model"
)

// Firefox-family browsers (LibreWolf, Firefox) keep their history in each
// profile's places.sqlite: moz_places holds the pages, moz_historyvisits each
// visit, with its time in microseconds since 1970. Firefox records no time
// spent on a page; the extension's and the web watcher's tab records give
// that.
const firefoxHistorySchemaVersion = "firefox-places-1"

// firefoxRoots are the Firefox-family browsers' data folders, each with a
// profiles.ini naming its profiles.
func firefoxRoots() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	if runtime.GOOS == "darwin" {
		base := filepath.Join(home, "Library", "Application Support")
		return []string{filepath.Join(base, "librewolf"), filepath.Join(base, "Firefox")}
	}
	return []string{
		filepath.Join(home, ".librewolf"), filepath.Join(home, ".var", "app", "io.gitlab.librewolf-community", ".librewolf"),
		filepath.Join(home, ".mozilla", "firefox"), filepath.Join(home, ".config", "mozilla", "firefox"),
		filepath.Join(home, ".var", "app", "org.mozilla.firefox", ".mozilla", "firefox"),
	}
}

// placesFile is one profile's history.
type placesFile struct {
	path, browser, profile string
}

// placesFiles finds each profile's places.sqlite: those profiles.ini names,
// and any other profile folder in the usual places (macOS keeps them in
// Profiles/, Linux beside profiles.ini).
func placesFiles(roots []string) []placesFile {
	files := []placesFile{}
	seen := map[string]bool{}
	for _, root := range roots {
		browser := strings.ToLower(strings.TrimPrefix(filepath.Base(root), "."))
		if browser == "firefox" || strings.Contains(root, "mozilla") {
			browser = "firefox"
		}
		dirs := profileDirs(root)
		for _, pattern := range []string{filepath.Join(root, "Profiles", "*"), filepath.Join(root, "*")} {
			matches, _ := filepath.Glob(pattern)
			dirs = append(dirs, matches...)
		}
		sort.Strings(dirs)
		for _, dir := range dirs {
			path := filepath.Join(dir, "places.sqlite")
			if seen[path] {
				continue
			}
			if info, err := os.Stat(path); err != nil || info.IsDir() {
				continue
			}
			seen[path] = true
			files = append(files, placesFile{path: path, browser: browser, profile: filepath.Base(dir)})
		}
	}
	return files
}

// profileDirs reads the profile folders profiles.ini names.
func profileDirs(root string) []string {
	file, err := os.Open(filepath.Join(root, "profiles.ini"))
	if err != nil {
		return nil
	}
	defer file.Close()
	dirs := []string{}
	var path string
	relative := true
	flush := func() {
		if path != "" {
			if relative {
				path = filepath.Join(root, filepath.FromSlash(path))
			}
			dirs = append(dirs, filepath.Clean(path))
		}
		path, relative = "", true
	}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "["):
			flush()
		case strings.HasPrefix(line, "Path="):
			path = strings.TrimPrefix(line, "Path=")
		case strings.HasPrefix(line, "IsRelative="):
			relative = strings.TrimPrefix(line, "IsRelative=") != "0"
		}
	}
	flush()
	return dirs
}

// collectPlaces reads the visits after this file's position from a copy.
func (b *BrowserHistory) collectPlaces(ctx context.Context, host Host, file placesFile, now time.Time) error {
	cursorKey := "browser-history:" + file.path
	value, _, err := host.Queue.LoadCursor(ctx, cursorKey)
	if err != nil {
		return err
	}
	last, _ := strconv.ParseInt(value, 10, 64)
	db, done, err := openCopy(file.path)
	if err != nil {
		return err
	}
	defer done()

	instance := "browser-history:" + host.DeviceID + ":" + file.browser + ":" + file.profile
	for {
		rows, err := db.QueryContext(ctx, `SELECT v.id, v.visit_date, v.visit_type, v.from_visit, p.url, COALESCE(p.title, '')
			FROM moz_historyvisits v JOIN moz_places p ON p.id = v.place_id WHERE v.id > ? ORDER BY v.id LIMIT ?`, last, browserHistoryBatch)
		if err != nil {
			return err
		}
		count := 0
		for rows.Next() {
			var id, visitDate, visitType, fromVisit int64
			var address, title string
			if err := rows.Scan(&id, &visitDate, &visitType, &fromVisit, &address, &title); err != nil {
				rows.Close()
				return err
			}
			count++
			payload, _ := json.Marshal(map[string]any{"visit_id": id, "url": address, "title": title, "visit_type": visitType,
				"from_visit": fromVisit, "browser": file.browser, "profile": file.profile})
			observation := model.Observation{SourceInstanceKey: instance, SourceKind: "browser_history", DeviceID: host.DeviceID,
				SourceRecordKey: "visit:" + strconv.FormatInt(id, 10), Kind: "browser.visit", OccurredAt: time.UnixMicro(visitDate).UTC(),
				CollectedAt: now, SourceSchemaVersion: firefoxHistorySchemaVersion, OriginalPayload: payload}
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
