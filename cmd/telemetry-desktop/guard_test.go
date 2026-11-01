package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGuardAction(t *testing.T) {
	cases := []struct {
		loaded  bool
		printed string
		want    string
	}{
		{false, "", "bootstrap"},
		{true, "\tstate = running\n\tpid = 81855\n", ""},
		// The October and November failures: loaded, but launchd refuses the
		// replaced program.
		{true, "\tstate = spawn scheduled\n\tlast exit code = 78: EX_CONFIG\n\tjob state = spawn failed\n", "reload"},
		// Exited a moment ago; KeepAlive starts it again by itself.
		{true, "\tstate = not running\n\tlast exit code = 1\n", ""},
	}
	for _, c := range cases {
		if got := guardAction(c.loaded, c.printed); got != c.want {
			t.Errorf("guardAction(%v, %q) = %q, want %q", c.loaded, c.printed, got, c.want)
		}
	}
}

func TestRestoreJobPutsBackOnlyAMissingFile(t *testing.T) {
	dir := t.TempDir()
	kept, job := filepath.Join(dir, "kept.plist"), filepath.Join(dir, "job.plist")
	os.WriteFile(kept, []byte("kept"), 0o644)
	if err := restoreJob(kept, job, os.Getuid()); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(job); string(data) != "kept" {
		t.Fatalf("job = %q", data)
	}
	// The owner's own file is never overwritten.
	os.WriteFile(job, []byte("owner's"), 0o644)
	restoreJob(kept, job, os.Getuid())
	if data, _ := os.ReadFile(job); string(data) != "owner's" {
		t.Fatalf("job overwritten: %q", data)
	}
}
