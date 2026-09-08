package main

import (
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
