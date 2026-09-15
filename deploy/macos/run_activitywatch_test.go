package macos_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiveRunnerContinuesAfterOneBucketFails(t *testing.T) {
	tempDir := t.TempDir()
	fakeCollector := filepath.Join(tempDir, "collector.zsh")
	logPath := filepath.Join(tempDir, "buckets.log")
	fakeScript := `#!/bin/zsh
set -euo pipefail
bucket=""
while (( $# > 0 )); do
  if [[ "$1" == "-bucket" ]]; then
    bucket="$2"
    shift 2
  else
    shift
  fi
done
print -r -- "$bucket" >> "$FAKE_LOG"
if [[ "$bucket" == "aw-watcher-window_plagiarism.local" ]]; then
  exit 1
fi
`
	if err := os.WriteFile(fakeCollector, []byte(fakeScript), 0o700); err != nil {
		t.Fatal(err)
	}

	command := exec.Command("zsh", "deploy/macos/run-activitywatch-live.sh")
	command.Dir = filepath.Join("..", "..")
	command.Env = append(os.Environ(),
		"HOME="+tempDir,
		"TELEMETRY_AW_COLLECTOR="+fakeCollector,
		"FAKE_LOG="+logPath,
	)
	if err := command.Run(); err == nil {
		t.Fatal("runner succeeded after a bucket failure")
	}
	contents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Fields(string(contents))
	want := []string{
		"aw-watcher-window_plagiarism.local",
		"aw-watcher-afk_plagiarism.local",
		"aw-watcher-web-chrome_plagiarism.local",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("buckets attempted = %v, want %v", got, want)
	}
}
