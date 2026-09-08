package deviceauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoginPersistsSecretAndUsesHashThenBearer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "device.json")
	var hash, secret string
	var server *httptest.Server
	starts := 0
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			starts++
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["secret"] != "" || body["pairing_secret"] != "" || len(body["credential_hash"]) != 64 {
				t.Error("start leaked plaintext or omitted hash")
			}
			hash = body["credential_hash"]
			saved, err := Load(path)
			if err != nil {
				t.Error("secret not retained before start")
			}
			secret = saved.Secret
			json.NewEncoder(w).Encode(map[string]string{"pairing_id": "test-id", "panel_url": server.URL + "/panel/?pair=test-id"})
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+secret {
			t.Error("poll bearer mismatch")
		}
		sum := sha256.Sum256([]byte(secret))
		if hex.EncodeToString(sum[:]) != hash {
			t.Error("hash mismatch")
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "approved", "device_id": "existing-mac"})
	}))
	defer server.Close()
	c := Client{HTTPClient: server.Client(), PollInterval: time.Millisecond}
	saved, err := c.Login(context.Background(), path, server.URL, "existing-mac", "Mac", func(u string) {
		if !strings.HasPrefix(u, server.URL+"/panel/") {
			t.Error(u)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !saved.Approved || saved.DeviceID != "existing-mac" {
		t.Fatal("not approved")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("credential permissions")
	}
	if _, err = c.Login(context.Background(), path, server.URL, "existing-mac", "Mac", nil); err != nil {
		t.Fatal(err)
	}
	if starts != 1 {
		t.Fatal("restart created another enrollment")
	}
	if _, err = c.Login(context.Background(), path, "https://other.example", "existing-mac", "Mac", nil); err == nil {
		t.Fatal("credential rebound to another server")
	}
}
func TestLoginRejectsForeignBrowserURL(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"pairing_id": "id", "panel_url": "https://attacker.example/panel/?pair=id"})
	}))
	defer server.Close()
	c := Client{HTTPClient: server.Client()}
	if _, err := c.Login(context.Background(), filepath.Join(t.TempDir(), "device.json"), server.URL, "mac", "Mac", nil); err == nil {
		t.Fatal("accepted foreign pairing page")
	}
}
