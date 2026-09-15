package macos_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectsRunnerQuotesSelectedPathsAndNeverReadsCredential(t *testing.T) {
	tempDir := t.TempDir()
	paths := prepareProjectRunnerFixture(t, tempDir)
	command := exec.Command("zsh", "deploy/macos/run-projects.sh")
	command.Dir = filepath.Join("..", "..")
	command.Env = projectRunnerEnv(tempDir, paths)
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
	collectorLog, err := os.ReadFile(paths.collectorLog)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"sync", "-queue", paths.queue, "-db", paths.database, "-usage-cache", paths.usageCache, "-device", "mac-test", "-project", paths.project, "-git", "-timeout", "20s"} {
		if !strings.Contains(string(collectorLog), want) {
			t.Fatalf("collector log omitted exact argument %q: %s", want, collectorLog)
		}
	}
	drainerLog, err := os.ReadFile(paths.drainerLog)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"drain", "-queue", paths.queue, "-device", "mac-test", "-limit", "100", "-all", "-max-batches", "10", "-server", "https://example.test", paths.token} {
		if !strings.Contains(string(drainerLog), want) {
			t.Fatalf("drainer log omitted exact argument %q: %s", want, drainerLog)
		}
	}
	if strings.Contains(string(drainerLog), "fixture-secret") {
		t.Fatal("runner exposed credential contents")
	}
}

func TestProjectsRunnerDrainsAfterCaptureFailureAndPropagatesStatus(t *testing.T) {
	tempDir := t.TempDir()
	paths := prepareProjectRunnerFixture(t, tempDir)
	environment := projectRunnerEnv(tempDir, paths)
	environment = append(environment, "FAKE_COLLECTOR_FAIL=1")
	command := exec.Command("zsh", "deploy/macos/run-projects.sh")
	command.Dir = filepath.Join("..", "..")
	command.Env = environment
	if err := command.Run(); err == nil {
		t.Fatal("capture failure was hidden")
	}
	if _, err := os.Stat(paths.drainerLog); err != nil {
		t.Fatalf("drainer was not attempted after capture failure: %v", err)
	}
}

func TestProjectsRunnerAllowsConcurrentDuplicatePasses(t *testing.T) {
	tempDir := t.TempDir()
	paths := prepareProjectRunnerFixture(t, tempDir)
	baseEnvironment := projectRunnerEnv(tempDir, paths)
	commands := make([]*exec.Cmd, 2)
	for index := range commands {
		commands[index] = exec.Command("zsh", "deploy/macos/run-projects.sh")
		commands[index].Dir = filepath.Join("..", "..")
		commands[index].Env = baseEnvironment
		if err := commands[index].Start(); err != nil {
			t.Fatal(err)
		}
	}
	for _, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	collectorLog, err := os.ReadFile(paths.collectorLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(collectorLog), "collector:sync") != 2 {
		t.Fatalf("duplicate captures = %d, want 2: %s", strings.Count(string(collectorLog), "collector:sync"), collectorLog)
	}
	drainerLog, err := os.ReadFile(paths.drainerLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(drainerLog), "drainer:drain") != 2 {
		t.Fatalf("duplicate drains = %d, want 2: %s", strings.Count(string(drainerLog), "drainer:drain"), drainerLog)
	}
}

func TestProjectsRunnerStopsBeforeDrainingAnAliasedSource(t *testing.T) {
	tempDir := t.TempDir()
	paths := prepareProjectRunnerFixture(t, tempDir)
	if err := os.WriteFile(paths.database, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.usageCache, []byte("usage"), 0o600); err != nil {
		t.Fatal(err)
	}
	environment := projectRunnerEnv(tempDir, paths)
	environment = append(environment, "TELEMETRY_PROJECTS_QUEUE="+paths.database)
	command := exec.Command("zsh", "deploy/macos/run-projects.sh")
	command.Dir = filepath.Join("..", "..")
	command.Env = environment
	if err := command.Run(); err == nil {
		t.Fatal("aliased queue was accepted")
	}
	if _, err := os.Stat(paths.drainerLog); !os.IsNotExist(err) {
		t.Fatalf("drainer was attempted after alias rejection: %v", err)
	}
	if _, err := os.Stat(paths.collectorLog); !os.IsNotExist(err) {
		t.Fatalf("collector was attempted after alias rejection: %v", err)
	}
	usageSymlink := filepath.Join(tempDir, "queue-usage-symlink.sqlite")
	if err := os.Symlink(paths.usageCache, usageSymlink); err != nil {
		t.Fatal(err)
	}
	environment = projectRunnerEnv(tempDir, paths)
	environment = append(environment, "TELEMETRY_PROJECTS_QUEUE="+usageSymlink)
	command = exec.Command("zsh", "deploy/macos/run-projects.sh")
	command.Dir = filepath.Join("..", "..")
	command.Env = environment
	if err := command.Run(); err == nil {
		t.Fatal("usage-cache symlink alias was accepted")
	}
	if _, err := os.Stat(paths.drainerLog); !os.IsNotExist(err) {
		t.Fatalf("drainer was attempted after usage symlink rejection: %v", err)
	}
	usageHardlink := filepath.Join(tempDir, "queue-usage-hardlink.sqlite")
	if err := os.Link(paths.usageCache, usageHardlink); err != nil {
		t.Fatal(err)
	}
	environment = projectRunnerEnv(tempDir, paths)
	environment = append(environment, "TELEMETRY_PROJECTS_QUEUE="+usageHardlink)
	command = exec.Command("zsh", "deploy/macos/run-projects.sh")
	command.Dir = filepath.Join("..", "..")
	command.Env = environment
	if err := command.Run(); err == nil {
		t.Fatal("usage-cache hard-link alias was accepted")
	}
	if _, err := os.Stat(paths.drainerLog); !os.IsNotExist(err) {
		t.Fatalf("drainer was attempted after usage hard-link rejection: %v", err)
	}
}

type projectRunnerPaths struct {
	collectorLog string
	drainerLog   string
	database     string
	usageCache   string
	project      string
	queue        string
	token        string
}

func prepareProjectRunnerFixture(t *testing.T, tempDir string) projectRunnerPaths {
	t.Helper()
	root := filepath.Join(tempDir, "Library", "Application Support", "Telemetry")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	token := filepath.Join(root, "activitywatch-codex.ingest-token")
	if err := os.WriteFile(token, []byte("fixture-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	collector := filepath.Join(tempDir, "collector.zsh")
	if err := os.WriteFile(collector, []byte("#!/bin/zsh\nfor arg in \"$@\"; do print -r -- \"collector:$arg\" >> \"$PROJECT_LOG\"; done\nif [[ \"${FAKE_COLLECTOR_FAIL:-0}\" == 1 ]]; then exit 1; fi\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	drainer := filepath.Join(tempDir, "drainer.zsh")
	if err := os.WriteFile(drainer, []byte("#!/bin/zsh\nfor arg in \"$@\"; do print -r -- \"drainer:$arg\" >> \"$DRAINER_LOG\"; done\nif [[ \"${FAKE_DRAINER_FAIL:-0}\" == 1 ]]; then exit 1; fi\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return projectRunnerPaths{
		collectorLog: filepath.Join(tempDir, "collector.log"),
		drainerLog:   filepath.Join(tempDir, "drainer.log"),
		database:     filepath.Join(tempDir, "T3 state with spaces.sqlite"),
		usageCache:   filepath.Join(tempDir, "usage cache.json"),
		project:      filepath.Join(tempDir, "selected project with spaces"),
		queue:        filepath.Join(root, "queue with spaces.sqlite"),
		token:        token,
	}
}

func projectRunnerEnv(tempDir string, paths projectRunnerPaths) []string {
	collector := filepath.Join(tempDir, "collector.zsh")
	drainer := filepath.Join(tempDir, "drainer.zsh")
	return append(os.Environ(),
		"HOME="+tempDir,
		"TELEMETRY_PROJECTS_COLLECTOR="+collector,
		"TELEMETRY_PROJECTS_DRAINER="+drainer,
		"TELEMETRY_PROJECTS_DB="+paths.database,
		"TELEMETRY_PROJECTS_USAGE_CACHE="+paths.usageCache,
		"TELEMETRY_PROJECTS_PATH="+paths.project,
		"TELEMETRY_PROJECTS_DEVICE=mac-test",
		"TELEMETRY_PROJECTS_QUEUE="+paths.queue,
		"TELEMETRY_PROJECTS_FROM=2026-09-12T00:00:00Z",
		"TELEMETRY_PROJECTS_SERVER=https://example.test",
		"PROJECT_LOG="+paths.collectorLog,
		"DRAINER_LOG="+paths.drainerLog,
	)
}
