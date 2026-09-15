package githubsource

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/queue"
)

type fakeAPI struct {
	responses map[string][]byte
	errors    map[string]error
}

func (f fakeAPI) Get(_ context.Context, endpoint string) ([]byte, error) {
	if err := f.errors[endpoint]; err != nil {
		return nil, err
	}
	response, ok := f.responses[endpoint]
	if !ok {
		return nil, errors.New("unexpected endpoint: " + endpoint)
	}
	return append([]byte(nil), response...), nil
}

func TestCaptureRetainsExactResponsesAndQueueDeduplicatesRepeat(t *testing.T) {
	api := fixtureAPI()
	now := time.Date(2026, 9, 12, 8, 0, 0, 123000000, time.FixedZone("Europe/Istanbul", 3*60*60))
	options := Options{Owner: "rimexe0", Repository: "telemetry", MaxStatuses: 100, MaxCheckRuns: 100, Now: now}
	result := Capture(context.Background(), api, options)
	if result.Err != nil {
		t.Fatalf("capture error = %v", result.Err)
	}
	if len(result.Observations) != 4 || result.DefaultBranch != "main" || result.HeadSHA != strings.Repeat("a", 40) {
		t.Fatalf("capture result = %+v", result)
	}
	for index, observation := range result.Observations {
		if !observation.OccurredAt.Equal(now.UTC()) || !observation.CollectedAt.Equal(now.UTC()) {
			t.Fatalf("observation %d timestamps = %s / %s", index, observation.OccurredAt, observation.CollectedAt)
		}
		var envelope struct {
			Telemetry struct {
				CaptureTimeKind string `json:"capture_time_kind"`
				ResponseSHA     string `json:"response_sha256"`
				ResponseBase64  string `json:"response_bytes_base64"`
			} `json:"_telemetry"`
		}
		if err := json.Unmarshal(observation.OriginalPayload, &envelope); err != nil {
			t.Fatalf("observation %d payload: %v", index, err)
		}
		if envelope.Telemetry.CaptureTimeKind != "capture_time" || strings.Contains(string(observation.OriginalPayload), `"capture_time":`) {
			t.Fatalf("observation %d provenance = %+v", index, envelope.Telemetry)
		}
		decoded, err := base64.StdEncoding.DecodeString(envelope.Telemetry.ResponseBase64)
		if err != nil {
			t.Fatalf("observation %d response bytes: %v", index, err)
		}
		digest := sha256.Sum256(decoded)
		if hex.EncodeToString(digest[:]) != envelope.Telemetry.ResponseSHA {
			t.Fatalf("observation %d response digest does not match retained bytes", index)
		}
	}

	path := filepath.Join(t.TempDir(), "github.sqlite")
	localQueue, err := queue.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer localQueue.Close()
	for _, observation := range result.Observations {
		inserted, err := localQueue.Enqueue(context.Background(), observation, now)
		if err != nil || !inserted {
			t.Fatalf("first enqueue = %v, %v", inserted, err)
		}
	}
	for _, observation := range result.Observations {
		inserted, err := localQueue.Enqueue(context.Background(), observation, now)
		if err != nil || inserted {
			t.Fatalf("repeat enqueue = %v, %v", inserted, err)
		}
	}
	second := Capture(context.Background(), api, Options{
		Owner: "rimexe0", Repository: "telemetry", MaxStatuses: 100, MaxCheckRuns: 100,
		Now: now.Add(time.Hour),
	})
	if second.Err != nil {
		t.Fatal(second.Err)
	}
	for _, observation := range second.Observations {
		inserted, err := localQueue.Enqueue(context.Background(), observation, now.Add(time.Hour))
		if err != nil || inserted {
			t.Fatalf("unchanged response at a later capture time = %v, %v", inserted, err)
		}
	}
	changedAPI := fixtureAPI()
	changedAPI.responses["repos/rimexe0/telemetry"] = []byte("{\"full_name\":\"rimexe0/telemetry\",\"default_branch\":\"main\",\"private\":true,\"visibility\":\"private\",\"updated_at\":\"2026-09-12T07:59:00Z\",\"pushed_at\":\"2026-09-12T07:58:00Z\",\"description\":\"outside selected projection\"}")
	changed := Capture(context.Background(), changedAPI, options)
	if changed.Err != nil || changed.Observations[0].RevisionDigest == result.Observations[0].RevisionDigest {
		t.Fatalf("response field outside projection did not create a revision: %v", changed.Err)
	}
	inserted, err := localQueue.Enqueue(context.Background(), changed.Observations[0], now)
	if err != nil || !inserted {
		t.Fatalf("changed response enqueue = %v, %v", inserted, err)
	}
	stats, err := localQueue.Stats(context.Background())
	if err != nil || stats.Pending != 5 {
		t.Fatalf("queue stats = %+v, %v", stats, err)
	}
}

func TestCaptureReportsBoundedStatusAndCheckCoverage(t *testing.T) {
	api := fixtureAPI()
	headSHA := strings.Repeat("a", 40)
	api.responses["repos/rimexe0/telemetry/commits/"+headSHA+"/status?per_page=1"] = api.responses["repos/rimexe0/telemetry/commits/"+headSHA+"/status?per_page=100"]
	api.responses["repos/rimexe0/telemetry/commits/"+headSHA+"/check-runs?filter=latest&per_page=1"] = api.responses["repos/rimexe0/telemetry/commits/"+headSHA+"/check-runs?filter=latest&per_page=100"]
	result := Capture(context.Background(), api, Options{
		Owner: "rimexe0", Repository: "telemetry", MaxStatuses: 1, MaxCheckRuns: 1,
		Now: time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC),
	})
	if result.Err != nil || !result.StatusesTruncated || !result.CheckRunsTruncated {
		t.Fatalf("bounded result = %+v", result)
	}
	for _, observation := range result.Observations {
		if observation.Kind != "github.commit.status" && observation.Kind != "github.commit.check_runs" {
			continue
		}
		var envelope struct {
			Github struct {
				Coverage struct {
					Bounded   bool `json:"bounded"`
					Truncated bool `json:"truncated"`
					Limit     int  `json:"limit"`
				} `json:"coverage"`
			} `json:"github"`
		}
		if err := json.Unmarshal(observation.OriginalPayload, &envelope); err != nil {
			t.Fatal(err)
		}
		if !envelope.Github.Coverage.Bounded || !envelope.Github.Coverage.Truncated || envelope.Github.Coverage.Limit != 1 {
			t.Fatalf("%s coverage = %+v", observation.Kind, envelope.Github.Coverage)
		}
	}
}

func TestCaptureReturnsDurablePrefixWhenChecksFail(t *testing.T) {
	api := fixtureAPI()
	api.errors["repos/rimexe0/telemetry/commits/"+strings.Repeat("a", 40)+"/check-runs?filter=latest&per_page=100"] = errors.New("check API unavailable")
	result := Capture(context.Background(), api, Options{Owner: "rimexe0", Repository: "telemetry", MaxStatuses: 100, MaxCheckRuns: 100})
	if result.Err == nil || !strings.Contains(result.Err.Error(), "check API unavailable") {
		t.Fatalf("failure = %v", result.Err)
	}
	if len(result.Observations) != 3 || result.HeadSHA == "" {
		t.Fatalf("durable prefix = %+v", result)
	}
}

func TestCaptureRejectsInvalidLimitsAndMismatchedIdentities(t *testing.T) {
	for _, limits := range [][2]int{{0, 100}, {-1, 100}, {101, 100}, {100, 0}, {100, -1}, {100, 101}} {
		result := Capture(context.Background(), fixtureAPI(), Options{
			Owner: "rimexe0", Repository: "telemetry", MaxStatuses: limits[0], MaxCheckRuns: limits[1],
		})
		if result.Err == nil || !strings.Contains(result.Err.Error(), "between 1 and 100") {
			t.Errorf("limits %v = %v", limits, result.Err)
		}
	}

	wrongRepository := fixtureAPI()
	wrongRepository.responses["repos/rimexe0/telemetry"] = []byte(`{"full_name":"rimexe0/other","default_branch":"main"}`)
	if result := Capture(context.Background(), wrongRepository, validOptions()); result.Err == nil || !strings.Contains(result.Err.Error(), "does not match selected") {
		t.Fatalf("wrong repository identity = %v", result.Err)
	}
	wrongBranch := fixtureAPI()
	wrongBranch.responses["repos/rimexe0/telemetry/branches/main"] = []byte(`{"name":"develop","protected":false,"commit":{"sha":"` + strings.Repeat("a", 40) + `"}}`)
	if result := Capture(context.Background(), wrongBranch, validOptions()); result.Err == nil || !strings.Contains(result.Err.Error(), "does not match default branch") {
		t.Fatalf("wrong branch identity = %v", result.Err)
	}
	invalidHead := fixtureAPI()
	invalidHead.responses["repos/rimexe0/telemetry/branches/main"] = []byte(`{"name":"main","commit":{"sha":"not-a-commit"}}`)
	if result := Capture(context.Background(), invalidHead, validOptions()); result.Err == nil || !strings.Contains(result.Err.Error(), "invalid head SHA") {
		t.Fatalf("invalid head identity = %v", result.Err)
	}

	deviceA := Capture(context.Background(), fixtureAPI(), Options{Owner: "rimexe0", Repository: "telemetry", DeviceID: "collector-a", MaxStatuses: 100, MaxCheckRuns: 100})
	deviceB := Capture(context.Background(), fixtureAPI(), Options{Owner: "rimexe0", Repository: "telemetry", DeviceID: "collector-b", MaxStatuses: 100, MaxCheckRuns: 100})
	if deviceA.Err != nil || deviceB.Err != nil || deviceA.Observations[0].SourceInstanceKey == deviceB.Observations[0].SourceInstanceKey {
		t.Fatalf("device-scoped source instances = %q / %q", deviceA.Observations[0].SourceInstanceKey, deviceB.Observations[0].SourceInstanceKey)
	}
}

func TestCaptureRejectsNullAndMissingStatusResponses(t *testing.T) {
	nullStatus := fixtureAPI()
	headSHA := strings.Repeat("a", 40)
	nullStatus.responses["repos/rimexe0/telemetry/commits/"+headSHA+"/status?per_page=100"] = []byte("null")
	result := Capture(context.Background(), nullStatus, validOptions())
	if result.Err == nil || len(result.Observations) != 2 || !strings.Contains(result.Err.Error(), "expected a JSON object") {
		t.Fatalf("null status = %+v", result)
	}

	missingStatuses := fixtureAPI()
	missingStatuses.responses["repos/rimexe0/telemetry/commits/"+headSHA+"/status?per_page=100"] = []byte(`{"state":"success","total_count":0}`)
	result = Capture(context.Background(), missingStatuses, validOptions())
	if result.Err == nil || len(result.Observations) != 2 || !strings.Contains(result.Err.Error(), `missing "statuses"`) {
		t.Fatalf("missing statuses = %+v", result)
	}

	missingChecks := fixtureAPI()
	missingChecks.responses["repos/rimexe0/telemetry/commits/"+headSHA+"/check-runs?filter=latest&per_page=100"] = []byte(`{"total_count":0}`)
	result = Capture(context.Background(), missingChecks, validOptions())
	if result.Err == nil || len(result.Observations) != 3 || !strings.Contains(result.Err.Error(), `missing "check_runs"`) {
		t.Fatalf("missing check runs = %+v", result)
	}

	nullChecks := fixtureAPI()
	nullChecks.responses["repos/rimexe0/telemetry/commits/"+headSHA+"/check-runs?filter=latest&per_page=100"] = []byte(`{"total_count":0,"check_runs":null}`)
	result = Capture(context.Background(), nullChecks, validOptions())
	if result.Err == nil || len(result.Observations) != 3 || !strings.Contains(result.Err.Error(), `"check_runs" cannot be null`) {
		t.Fatalf("null check runs = %+v", result)
	}
}

func TestGHAPIDoesNotReceiveCredentialArguments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-gh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	output, err := (GHAPI{Binary: path}).Get(context.Background(), "repos/rimexe0/telemetry")
	if err != nil {
		t.Fatal(err)
	}
	text := string(output)
	if strings.Contains(strings.ToLower(text), "authorization") || strings.Contains(strings.ToLower(text), "token") {
		t.Fatalf("credential argument reached gh: %q", text)
	}
	if !strings.Contains(text, "repos/rimexe0/telemetry") || !strings.Contains(text, "X-GitHub-Api-Version: "+apiVersion) {
		t.Fatalf("unexpected gh args: %q", text)
	}
}

func fixtureAPI() fakeAPI {
	headSHA := strings.Repeat("a", 40)
	return fakeAPI{
		responses: map[string][]byte{
			"repos/rimexe0/telemetry":                                                               []byte("{\n  \"full_name\": \"rimexe0/telemetry\", \"default_branch\": \"main\", \"private\": true, \"visibility\": \"private\", \"updated_at\": \"2026-09-12T07:59:00Z\", \"pushed_at\": \"2026-09-12T07:58:00Z\"\n}"),
			"repos/rimexe0/telemetry/branches/main":                                                 []byte("{\"name\":\"main\",\"protected\":false,\"commit\":{\"sha\":\"" + headSHA + "\"}}"),
			"repos/rimexe0/telemetry/commits/" + headSHA + "/status?per_page=100":                   []byte("{\"state\":\"success\",\"total_count\":2,\"statuses\":[{\"context\":\"ci\",\"state\":\"success\",\"created_at\":\"2026-09-12T07:00:00Z\",\"updated_at\":\"2026-09-12T07:01:00Z\"},{\"context\":\"lint\",\"state\":\"success\"}]}"),
			"repos/rimexe0/telemetry/commits/" + headSHA + "/check-runs?filter=latest&per_page=100": []byte("{\"total_count\":2,\"check_runs\":[{\"id\":1,\"name\":\"ci\",\"status\":\"completed\",\"conclusion\":\"success\",\"started_at\":\"2026-09-12T07:00:00Z\",\"completed_at\":\"2026-09-12T07:02:00Z\"},{\"id\":2,\"name\":\"lint\",\"status\":\"completed\",\"conclusion\":\"success\"}]}"),
		},
		errors: map[string]error{},
	}
}

func validOptions() Options {
	return Options{Owner: "rimexe0", Repository: "telemetry", MaxStatuses: 100, MaxCheckRuns: 100}
}
