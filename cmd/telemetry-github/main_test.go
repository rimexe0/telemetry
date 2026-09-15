package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/githubsource"
	"github.com/rimexe0/telemetry/internal/queue"
)

type cancelAfterStatusAPI struct {
	cancel context.CancelFunc
	calls  int
}

func (a *cancelAfterStatusAPI) Get(ctx context.Context, endpoint string) ([]byte, error) {
	a.calls++
	if a.calls == 4 {
		return nil, ctx.Err()
	}
	head := strings.Repeat("a", 40)
	switch {
	case endpoint == "repos/rimexe0/telemetry":
		return []byte(`{"full_name":"rimexe0/telemetry","default_branch":"main"}`), nil
	case endpoint == "repos/rimexe0/telemetry/branches/main":
		return []byte(`{"name":"main","commit":{"sha":"` + head + `"}}`), nil
	case endpoint == "repos/rimexe0/telemetry/commits/"+head+"/status?per_page=100":
		a.cancel()
		return []byte(`{"state":"success","total_count":0,"statuses":[]}`), nil
	default:
		return nil, errors.New("unexpected endpoint")
	}
}

func TestCaptureToQueuePersistsSuccessfulPrefixAfterCaptureTimeout(t *testing.T) {
	captureContext, cancelCapture := context.WithCancel(context.Background())
	defer cancelCapture()
	api := &cancelAfterStatusAPI{cancel: cancelCapture}
	queueContext, cancelQueue := context.WithTimeout(context.Background(), time.Second)
	defer cancelQueue()
	queuePath := filepath.Join(t.TempDir(), "github.sqlite")
	output, err := captureToQueue(captureContext, queueContext, api, githubsource.Options{
		Owner: "rimexe0", Repository: "telemetry", MaxStatuses: 100, MaxCheckRuns: 100,
		Now: time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC),
	}, queuePath)
	if err == nil || output["error"] == nil {
		t.Fatalf("timeout result = %#v, %v", output, err)
	}
	localQueue, err := queue.Open(queuePath)
	if err != nil {
		t.Fatal(err)
	}
	defer localQueue.Close()
	stats, err := localQueue.Stats(context.Background())
	if err != nil || stats.Pending != 3 {
		t.Fatalf("successful capture prefix was not persisted: %+v, %v", stats, err)
	}
}
