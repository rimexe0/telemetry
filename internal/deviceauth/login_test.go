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

func TestLoginWithCodeConnectsAtOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "device.json")
	var server *httptest.Server
	polls := 0
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			polls++
			json.NewEncoder(w).Encode(map[string]string{"status": "pending", "device_id": "mac"})
			return
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["code"] != "K7QM-3XPD" {
			t.Errorf("code not sent: %v", body)
		}
		json.NewEncoder(w).Encode(map[string]string{"pairing_id": "id", "panel_url": server.URL + "/panel/?pair=id", "status": "approved"})
	}))
	defer server.Close()
	c := Client{HTTPClient: server.Client(), PollInterval: time.Millisecond}
	saved, err := c.LoginWithCode(context.Background(), path, server.URL, "mac", "Mac", "K7QM-3XPD", func(string) { t.Error("showed an approval link for a setup code") })
	if err != nil || !saved.Approved || polls != 0 {
		t.Fatalf("login with code = %+v, %v, polls %d", saved, err, polls)
	}
}

func TestUnfinishedLoginForAnotherServerStartsOver(t *testing.T) {
	path := filepath.Join(t.TempDir(), "device.json")
	if err := Save(path, Config{ServerURL: "https://old.example", DeviceID: "mac", DeviceName: "Mac", Secret: "s", PairingID: "old"}); err != nil {
		t.Fatal(err)
	}
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			json.NewEncoder(w).Encode(map[string]string{"pairing_id": "new", "panel_url": server.URL + "/panel/?pair=new"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "approved", "device_id": "mac"})
	}))
	defer server.Close()
	c := Client{HTTPClient: server.Client(), PollInterval: time.Millisecond}
	saved, err := c.Login(context.Background(), path, server.URL, "mac", "Mac", nil)
	if err != nil || !saved.Approved || saved.ServerURL != server.URL || saved.PairingID != "new" || saved.Secret == "s" {
		t.Fatalf("restarted login = %+v, %v", saved, err)
	}
}

func TestExpiredRequestAsksAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "device.json")
	var server *httptest.Server
	starts, polls := 0, 0
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			starts++
			id := []string{"", "first", "second"}[starts]
			json.NewEncoder(w).Encode(map[string]string{"pairing_id": id, "panel_url": server.URL + "/panel/?pair=" + id})
			return
		}
		polls++
		if strings.HasSuffix(r.URL.Path, "/first") {
			w.WriteHeader(http.StatusGone)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "approved", "device_id": "mac"})
	}))
	defer server.Close()
	shown := 0
	c := Client{HTTPClient: server.Client(), PollInterval: time.Millisecond}
	saved, err := c.Login(context.Background(), path, server.URL, "mac", "Mac", func(string) { shown++ })
	if err != nil || !saved.Approved || starts != 2 || saved.PairingID != "second" {
		t.Fatalf("after expiry = %+v, %v, starts %d", saved, err, starts)
	}
}

func TestRemovedDevicePairsAgainWithNewKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "device.json")
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			json.NewEncoder(w).Encode(map[string]string{"pairing_id": "again", "panel_url": server.URL + "/panel/?pair=again"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "approved", "device_id": "mac"})
	}))
	defer server.Close()
	if err := Save(path, Config{ServerURL: server.URL, DeviceID: "mac", DeviceName: "Mac", Secret: "old-key", PairingID: "first", Approved: true}); err != nil {
		t.Fatal(err)
	}
	if err := MarkRemoved(path); err != nil {
		t.Fatal(err)
	}
	marked, _ := Load(path)
	if marked.Approved || !marked.Removed {
		t.Fatalf("marked = %+v", marked)
	}
	c := Client{HTTPClient: server.Client(), PollInterval: time.Millisecond}
	saved, err := c.Login(context.Background(), path, server.URL, "", "Mac", nil)
	if err != nil || !saved.Approved || saved.Removed || saved.Secret == "old-key" || saved.DeviceID != "mac" || saved.PairingID != "again" {
		t.Fatalf("paired again = %+v, %v", saved, err)
	}
}
