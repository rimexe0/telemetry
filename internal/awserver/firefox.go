package awserver

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"os"

	"github.com/rimexe0/telemetry/internal/xpi"
)

// firefoxUpdate answers Firefox's update check for the packed extension, in
// the update manifest format Firefox reads (updates.json). Firefox takes an
// update from an http address only with the package's hash.
//
// The update's address names its version. Browser policy installs from the
// plain address and does so again at each browser start while the
// installed copy came from another address, so an update an hourly check
// missed arrives at the next start too; the same version is not
// installed twice.
func (h Handler) firefoxUpdate(writer http.ResponseWriter) {
	data, err := os.ReadFile(h.FirefoxPackage())
	if err != nil {
		writeError(writer, http.StatusNotFound, "no Firefox package is kept here")
		return
	}
	info, err := xpi.Read(data)
	if err != nil || info.Version == "" {
		writeError(writer, http.StatusNotFound, "the kept Firefox package cannot be read")
		return
	}
	id := info.ID
	if id == "" {
		id = xpi.ID
	}
	sum := sha256.Sum256(data)
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, map[string]any{
		"addons": map[string]any{
			id: map[string]any{
				"updates": []map[string]any{{
					"version":     info.Version,
					"update_link": xpi.InstallURL + "?v=" + url.QueryEscape(info.Version),
					"update_hash": "sha256:" + hex.EncodeToString(sum[:]),
				}},
			},
		},
	})
}
