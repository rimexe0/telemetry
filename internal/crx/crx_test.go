package crx

import (
	"crypto/rand"
	"crypto/rsa"
	"os"
	"path/filepath"
	"testing"
)

func TestPackedExtensionVerifies(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"manifest_version":3,"name":"x","version":"1"}`), 0o600)
	archive, err := Zip(dir, []string{"manifest.json"})
	if err != nil {
		t.Fatal(err)
	}
	packed, err := Pack(archive, key)
	if err != nil {
		t.Fatal(err)
	}
	id, err := Verify(packed)
	want, _ := ID(&key.PublicKey)
	if err != nil || id != want || len(id) != 32 {
		t.Fatalf("id %q, want %q, err %v", id, want, err)
	}
	packed[len(packed)-30] ^= 1
	if _, err := Verify(packed); err == nil {
		t.Fatal("a changed archive verified")
	}
}
