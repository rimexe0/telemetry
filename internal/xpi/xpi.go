// Package xpi packs the Telemetry browser extension for Firefox and its
// forks, such as LibreWolf, from the same files as the Chromium build. Only
// the manifest differs: Firefox runs the background script as an event page,
// needs the extension's own ID, and grants host access to the hosts the
// manifest lists. Browser policy installs the package from the collector and
// takes its updates from there too.
package xpi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ID is the extension's ID in Firefox. Browser policy installs it by this ID,
// and Mozilla's signing service knows it by it.
const ID = "telemetry@rime.cyou"

// MinVersion is the oldest Firefox the package is for: the first that reads
// data_collection_permissions, which Mozilla requires to sign a new add-on.
const MinVersion = "140.0"

// Files are the extension's files, as in the Chromium build.
var Files = []string{"manifest.json", "background.js", "door.js", "cover.js", "popup.html", "popup.js",
	"icons/icon-16.png", "icons/icon-32.png", "icons/icon-48.png", "icons/icon-128.png"}

// Manifest turns the Chromium manifest into Firefox's.
//
//   - Firefox has no background service worker; the same script runs as an
//     event page.
//   - Firefox grants a Manifest V3 extension the hosts in host_permissions, and
//     a copy installed by policy gets them at install with no way to take them
//     back. Content scripts and scripting need every page, so every page is
//     listed; Chromium grants content script hosts by themselves.
//   - The update address comes from browser policy, which overrides the
//     manifest's. Mozilla's signing accepts only an https address here, and
//     the collector answers on http://127.0.0.1.
//   - The extension reports the address and title of the pages in front to
//     the collector on this computer, which sends them to the owner's server,
//     so it says it collects browsing activity and website content.
func Manifest(chromium []byte) ([]byte, error) {
	var manifest map[string]any
	if err := json.Unmarshal(chromium, &manifest); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	background, _ := manifest["background"].(map[string]any)
	worker, _ := background["service_worker"].(string)
	if worker == "" {
		return nil, errors.New("manifest has no background service worker")
	}
	manifest["background"] = map[string]any{"scripts": []string{worker}}
	delete(manifest, "update_url")
	hosts, _ := manifest["host_permissions"].([]any)
	for _, pattern := range []string{"http://*/*", "https://*/*"} {
		found := false
		for _, host := range hosts {
			found = found || host == pattern
		}
		if !found {
			hosts = append(hosts, pattern)
		}
	}
	manifest["host_permissions"] = hosts
	manifest["browser_specific_settings"] = map[string]any{
		"gecko": map[string]any{
			"id":                 ID,
			"strict_min_version": MinVersion,
			"data_collection_permissions": map[string]any{
				"required": []string{"browsingActivity", "websiteContent"},
			},
		},
	}
	return json.MarshalIndent(manifest, "", "  ")
}

// Build packs the extension in dir for Firefox: its files with Firefox's
// manifest. The archive is the same for the same files.
func Build(dir string) ([]byte, error) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for _, name := range Files {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			return nil, err
		}
		if name == "manifest.json" {
			if data, err = Manifest(data); err != nil {
				return nil, err
			}
		}
		file, err := writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		if err != nil {
			return nil, err
		}
		if _, err := file.Write(data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return archive.Bytes(), nil
}

// Info is what a package says about itself.
type Info struct {
	ID      string
	Version string
	// Signed means the package carries Mozilla's signature, which Firefox
	// requires and LibreWolf can do without.
	Signed bool
}

// Read reads a package's ID, version and whether Mozilla signed it.
func Read(xpi []byte) (Info, error) {
	files, err := contents(xpi)
	if err != nil {
		return Info{}, err
	}
	var manifest struct {
		Version  string `json:"version"`
		Settings struct {
			Gecko struct {
				ID string `json:"id"`
			} `json:"gecko"`
		} `json:"browser_specific_settings"`
	}
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil {
		return Info{}, fmt.Errorf("package manifest: %w", err)
	}
	_, signed := files["META-INF/mozilla.rsa"]
	_, cose := files["META-INF/cose.sig"]
	return Info{ID: manifest.Settings.Gecko.ID, Version: manifest.Version, Signed: signed || cose}, nil
}

// contents reads every file in a package.
func contents(xpi []byte) (map[string][]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(xpi), int64(len(xpi)))
	if err != nil {
		return nil, fmt.Errorf("package: %w", err)
	}
	files := map[string][]byte{}
	for _, file := range reader.File {
		opened, err := file.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(opened, 16<<20))
		opened.Close()
		if err != nil {
			return nil, err
		}
		files[file.Name] = data
	}
	return files, nil
}

// SameExtension reports whether two packages hold the same extension files,
// whatever signature either carries. A signed copy made earlier for the same
// version differs when the extension changed without a new version.
func SameExtension(a, b []byte) (bool, error) {
	first, err := contents(a)
	if err != nil {
		return false, err
	}
	second, err := contents(b)
	if err != nil {
		return false, err
	}
	names := func(files map[string][]byte) []string {
		list := []string{}
		for name := range files {
			if !strings.HasPrefix(name, "META-INF/") {
				list = append(list, name)
			}
		}
		sort.Strings(list)
		return list
	}
	left, right := names(first), names(second)
	if strings.Join(left, "\n") != strings.Join(right, "\n") {
		return false, nil
	}
	for _, name := range left {
		if !bytes.Equal(first[name], second[name]) {
			return false, nil
		}
	}
	return true, nil
}
