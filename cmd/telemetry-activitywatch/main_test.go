package main

import (
	"github.com/rimexe0/telemetry/internal/deviceauth"
	"os"
	"path/filepath"
	"testing"
)

func TestReadToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("  from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := readToken("", path)
	if err != nil || value != "from-file" {
		t.Fatalf("file token = %q, %v", value, err)
	}
	value, err = readToken("inline", "")
	if err != nil || value != "inline" {
		t.Fatalf("inline token = %q, %v", value, err)
	}
	if _, err := readToken("inline", path); err == nil {
		t.Fatal("both token forms unexpectedly succeeded")
	}
}

func TestPairingConfigPreservesDeviceAndQueuesWhilePending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "device.json")
	c := deviceauth.Config{ServerURL: "https://private.example", DeviceID: "existing-mac", DeviceName: "Mac", Secret: "secret"}
	if err := deviceauth.Save(path, c); err != nil {
		t.Fatal(err)
	}
	device, server, token, file := "existing-mac", "", "", ""
	paired, err := applyPairing(path, &device, &server, &token, &file)
	if err != nil || paired || device != "existing-mac" || server != "" || token != "" {
		t.Fatalf("pending should queue only: %v %v", paired, err)
	}
	c.Approved = true
	if err := deviceauth.Save(path, c); err != nil {
		t.Fatal(err)
	}
	paired, err = applyPairing(path, &device, &server, &token, &file)
	if err != nil || !paired || server != c.ServerURL || token != c.Secret {
		t.Fatal("approved config not loaded")
	}
	device, server, token, file = "another-mac", "", "", ""
	if _, err = applyPairing(path, &device, &server, &token, &file); err == nil {
		t.Fatal("changed existing device ID")
	}
	device, server = "existing-mac", "https://another-server.example"
	if _, err = applyPairing(path, &device, &server, &token, &file); err == nil {
		t.Fatal("credential sent to another server")
	}
}
