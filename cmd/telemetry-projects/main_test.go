package main

import (
	"testing"

	"github.com/rimexe0/telemetry/internal/projectsource"
)

func TestPartialCaptureIsAnErrorWithStructuredResult(t *testing.T) {
	result := projectsource.SyncResult{Usage: projectsource.SourceResult{Status: "partial"}}
	if !isPartial(result) {
		t.Fatal("partial usage was treated as successful")
	}
	result = projectsource.SyncResult{State: projectsource.SourceResult{Status: "ok"}, Usage: projectsource.SourceResult{Status: "ok"}, Git: projectsource.SourceResult{Status: "ok"}}
	if isPartial(result) {
		t.Fatal("healthy source result was treated as partial")
	}
}
