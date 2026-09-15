// Package githubsource captures bounded, read-only GitHub repository status.
// It deliberately does not inspect issues, pull requests, messages, or files.
package githubsource

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/rimexe0/telemetry/internal/model"
)

const (
	apiVersion       = "2026-03-10"
	maxResponseBytes = 8 << 20
	requestTimeout   = 15 * time.Second
)

// API is the narrow read-only GitHub endpoint used by Capture. Implementations
// must return the bytes of the selected metadata response, not an unfiltered
// upstream response.
type API interface {
	Get(context.Context, string) ([]byte, error)
}

// GHAPI invokes the directly authenticated GitHub CLI. The token is resolved
// by gh from its own credential store and is never passed to this process.
type GHAPI struct {
	// Binary permits tests to provide a small fake executable. Empty means gh.
	Binary string
}

func (a GHAPI) Get(ctx context.Context, endpoint string) ([]byte, error) {
	selection, ok := selectionFor(endpoint)
	if !ok {
		return nil, errors.New("github endpoint is outside the read-only repository scope")
	}
	binary := a.Binary
	if binary == "" {
		binary = "gh"
	}
	args := []string{
		"api", endpoint,
		"--method", "GET",
		"--header", "Accept: application/vnd.github+json",
		"--header", "X-GitHub-Api-Version: " + apiVersion,
		"--jq", selection,
	}
	if ctx == nil {
		ctx = context.Background()
	}
	requestContext, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	command := exec.CommandContext(requestContext, binary, args...)
	var output cappedBuffer
	output.limit = maxResponseBytes
	command.Stdout = &output
	// Do not expose gh's stderr, which can contain environment-specific details.
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("github request %s failed: %w", endpoint, err)
	}
	if output.exceeded {
		return nil, fmt.Errorf("github response %s exceeds %d bytes", endpoint, maxResponseBytes)
	}
	return append([]byte(nil), output.Bytes()...), nil
}

type cappedBuffer struct {
	data     []byte
	limit    int
	exceeded bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		b.exceeded = true
		return len(p), nil
	}
	remaining := b.limit - len(b.data)
	if remaining <= 0 {
		b.exceeded = true
		return len(p), nil
	}
	if len(p) > remaining {
		b.data = append(b.data, p[:remaining]...)
		b.exceeded = true
		return len(p), nil
	}
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *cappedBuffer) Bytes() []byte { return b.data }

// Options bounds one capture. Only the selected repository's default branch
// head is inspected; statuses and check runs are metadata for that head.
type Options struct {
	Owner        string
	Repository   string
	DeviceID     string
	MaxStatuses  int
	MaxCheckRuns int
	Now          time.Time
}

// Result reports the observations and coverage produced before any error.
// Callers should enqueue Observations even when Err is non-nil so a successful
// prefix remains durable and the failed source state stays visible to the CLI.
type Result struct {
	Observations       []model.Observation
	Repository         string
	DefaultBranch      string
	HeadSHA            string
	StatusesSeen       int
	CheckRunsSeen      int
	StatusesTruncated  bool
	CheckRunsTruncated bool
	Err                error
}

// Capture reads repository metadata, the default branch head, and bounded
// status/check-run metadata for that head. It performs no GitHub writes.
func Capture(ctx context.Context, api API, options Options) Result {
	result := Result{}
	if api == nil {
		result.Err = errors.New("github API is required")
		return result
	}
	options.Owner = strings.TrimSpace(options.Owner)
	options.Repository = strings.TrimSpace(options.Repository)
	if options.Owner == "" || options.Repository == "" {
		result.Err = errors.New("github owner and repository are required")
		return result
	}
	if strings.ContainsAny(options.Owner, "/\\") || strings.ContainsAny(options.Repository, "/\\") {
		result.Err = errors.New("github owner and repository must be single path components")
		return result
	}
	if err := validateLimit(options.MaxStatuses, "status"); err != nil {
		result.Err = err
		return result
	}
	if err := validateLimit(options.MaxCheckRuns, "check-run"); err != nil {
		result.Err = err
		return result
	}
	capturedAt := options.Now.UTC()
	if capturedAt.IsZero() {
		capturedAt = time.Now().UTC()
	}
	result.Repository = options.Owner + "/" + options.Repository
	deviceID := options.DeviceID
	if strings.TrimSpace(deviceID) == "" {
		deviceID = "github:" + result.Repository
	}
	// Include the caller's explicit device identity. The same repository may be
	// observed by more than one collector and must not share a source instance.
	sourceInstance := "github:" + result.Repository + ":device:" + url.PathEscape(deviceID)
	base := "repos/" + url.PathEscape(options.Owner) + "/" + url.PathEscape(options.Repository)

	repositoryRaw, err := api.Get(ctx, base)
	if err != nil {
		result.Err = err
		return result
	}
	var repository repositoryResponse
	if err := decodeObject(repositoryRaw, &repository, "repository", "full_name", "default_branch"); err != nil {
		result.Err = err
		return result
	}
	if !strings.EqualFold(repository.FullName, result.Repository) {
		result.Err = fmt.Errorf("github repository response identity %q does not match selected %q", repository.FullName, result.Repository)
		return result
	}
	if repository.DefaultBranch == "" {
		result.Err = errors.New("github repository response has no default branch")
		return result
	}
	result.DefaultBranch = repository.DefaultBranch
	repositoryObservation, err := makeObservation(
		deviceID, sourceInstance, "repository:"+result.Repository,
		"github.repository.snapshot", base, capturedAt, repositoryProjection(repository), repositoryRaw,
	)
	if err != nil {
		result.Err = err
		return result
	}
	result.Observations = append(result.Observations, repositoryObservation)

	branchPath := base + "/branches/" + url.PathEscape(repository.DefaultBranch)
	branchRaw, err := api.Get(ctx, branchPath)
	if err != nil {
		result.Err = err
		return result
	}
	var branch branchResponse
	if err := decodeObject(branchRaw, &branch, "default branch", "name", "commit"); err != nil {
		result.Err = err
		return result
	}
	if !strings.EqualFold(branch.Name, repository.DefaultBranch) {
		result.Err = fmt.Errorf("github branch response identity %q does not match default branch %q", branch.Name, repository.DefaultBranch)
		return result
	}
	if branch.Commit.SHA == "" {
		result.Err = errors.New("github default branch response has no head SHA")
		return result
	}
	if !validSHA(branch.Commit.SHA) {
		result.Err = fmt.Errorf("github default branch response has invalid head SHA %q", branch.Commit.SHA)
		return result
	}
	result.HeadSHA = branch.Commit.SHA
	branchObservation, err := makeObservation(
		deviceID, sourceInstance, "default-branch-head:"+result.Repository+":"+repository.DefaultBranch,
		"github.default_branch.head", branchPath, capturedAt, branchProjection(repository.DefaultBranch, branch), branchRaw,
	)
	if err != nil {
		result.Err = err
		return result
	}
	result.Observations = append(result.Observations, branchObservation)

	statusPath := base + "/commits/" + url.PathEscape(branch.Commit.SHA) + "/status?per_page=" + strconv.Itoa(options.MaxStatuses)
	statusRaw, err := api.Get(ctx, statusPath)
	if err != nil {
		result.Err = err
		return result
	}
	var status statusResponse
	if err := decodeObject(statusRaw, &status, "commit status", "state", "total_count", "statuses"); err != nil {
		result.Err = err
		return result
	}
	if err := requireNonNullFields(statusRaw, "commit status", "state", "total_count", "statuses"); err != nil {
		result.Err = err
		return result
	}
	result.StatusesSeen = len(status.Statuses)
	result.StatusesTruncated = status.TotalCount > len(status.Statuses) || len(status.Statuses) >= options.MaxStatuses
	statusObservation, err := makeObservation(
		deviceID, sourceInstance, "commit-status:"+result.Repository+":"+branch.Commit.SHA,
		"github.commit.status", statusPath, capturedAt,
		statusProjection(branch.Commit.SHA, status, options.MaxStatuses), statusRaw,
	)
	if err != nil {
		result.Err = err
		return result
	}
	result.Observations = append(result.Observations, statusObservation)

	checkPath := base + "/commits/" + url.PathEscape(branch.Commit.SHA) + "/check-runs?filter=latest&per_page=" + strconv.Itoa(options.MaxCheckRuns)
	checkRaw, err := api.Get(ctx, checkPath)
	if err != nil {
		result.Err = err
		return result
	}
	var checks checkRunsResponse
	if err := decodeObject(checkRaw, &checks, "check runs", "total_count", "check_runs"); err != nil {
		result.Err = err
		return result
	}
	if err := requireNonNullFields(checkRaw, "check runs", "total_count", "check_runs"); err != nil {
		result.Err = err
		return result
	}
	result.CheckRunsSeen = len(checks.CheckRuns)
	result.CheckRunsTruncated = checks.TotalCount > len(checks.CheckRuns) || len(checks.CheckRuns) >= options.MaxCheckRuns
	checkObservation, err := makeObservation(
		deviceID, sourceInstance, "commit-check-runs:"+result.Repository+":"+branch.Commit.SHA,
		"github.commit.check_runs", checkPath, capturedAt,
		checkRunsProjection(branch.Commit.SHA, checks, options.MaxCheckRuns), checkRaw,
	)
	if err != nil {
		result.Err = err
		return result
	}
	result.Observations = append(result.Observations, checkObservation)
	return result
}

type repositoryResponse struct {
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
	Visibility    string `json:"visibility"`
	Archived      bool   `json:"archived"`
	Disabled      bool   `json:"disabled"`
	UpdatedAt     string `json:"updated_at"`
	PushedAt      string `json:"pushed_at"`
}

type branchResponse struct {
	Name      string `json:"name"`
	Protected bool   `json:"protected"`
	Commit    struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

type statusResponse struct {
	State      string `json:"state"`
	TotalCount int    `json:"total_count"`
	Statuses   []struct {
		Context   string `json:"context"`
		State     string `json:"state"`
		CreatedAt string `json:"created_at"`
		UpdatedAt string `json:"updated_at"`
	} `json:"statuses"`
}

type checkRunsResponse struct {
	TotalCount int `json:"total_count"`
	CheckRuns  []struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		Status      string `json:"status"`
		Conclusion  string `json:"conclusion"`
		StartedAt   string `json:"started_at"`
		CompletedAt string `json:"completed_at"`
	} `json:"check_runs"`
}

func decodeObject(raw []byte, target any, name string, required ...string) error {
	return decodeObjectWithFields(raw, target, name, required...)
}

func decodeObjectWithFields(raw []byte, target any, name string, required ...string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return fmt.Errorf("decode github %s: %w", name, err)
	}
	if object == nil {
		return fmt.Errorf("decode github %s: expected a JSON object", name)
	}
	for _, field := range required {
		if _, ok := object[field]; !ok {
			return fmt.Errorf("decode github %s: missing %q", name, field)
		}
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("decode github %s: %w", name, err)
	}
	return nil
}

func requireNonNullFields(raw []byte, name string, fields ...string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return fmt.Errorf("decode github %s: expected a JSON object", name)
	}
	for _, field := range fields {
		if string(object[field]) == "null" {
			return fmt.Errorf("decode github %s: %q cannot be null", name, field)
		}
	}
	return nil
}

func validateLimit(value int, name string) error {
	if value < 1 || value > 100 {
		return fmt.Errorf("github %s limit must be between 1 and 100", name)
	}
	return nil
}

func validSHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func repositoryProjection(value repositoryResponse) map[string]any {
	return map[string]any{
		"full_name":      value.FullName,
		"default_branch": value.DefaultBranch,
		"private":        value.Private,
		"visibility":     value.Visibility,
		"archived":       value.Archived,
		"disabled":       value.Disabled,
		"updated_at":     value.UpdatedAt,
		"pushed_at":      value.PushedAt,
		"coverage":       map[string]any{"scope": "repository_metadata", "complete": true},
	}
}

func branchProjection(defaultBranch string, value branchResponse) map[string]any {
	return map[string]any{
		"branch":    defaultBranch,
		"head_sha":  value.Commit.SHA,
		"protected": value.Protected,
		"coverage":  map[string]any{"scope": "default_branch_head", "complete": true},
	}
}

func statusProjection(headSHA string, value statusResponse, limit int) map[string]any {
	statuses := make([]map[string]any, 0, len(value.Statuses))
	for _, status := range value.Statuses {
		statuses = append(statuses, map[string]any{
			"context":    status.Context,
			"state":      status.State,
			"created_at": status.CreatedAt,
			"updated_at": status.UpdatedAt,
		})
	}
	return map[string]any{
		"head_sha":    headSHA,
		"state":       value.State,
		"total_count": value.TotalCount,
		"statuses":    statuses,
		"coverage": map[string]any{
			"scope":     "default_branch_head_commit_status",
			"bounded":   true,
			"limit":     limit,
			"returned":  len(statuses),
			"truncated": value.TotalCount > len(statuses) || len(statuses) >= limit,
			"excluded":  []string{"issues", "pull_requests", "message_content"},
		},
	}
}

func checkRunsProjection(headSHA string, value checkRunsResponse, limit int) map[string]any {
	runs := make([]map[string]any, 0, len(value.CheckRuns))
	for _, run := range value.CheckRuns {
		runs = append(runs, map[string]any{
			"id":           run.ID,
			"name":         run.Name,
			"status":       run.Status,
			"conclusion":   run.Conclusion,
			"started_at":   run.StartedAt,
			"completed_at": run.CompletedAt,
		})
	}
	return map[string]any{
		"head_sha":    headSHA,
		"total_count": value.TotalCount,
		"check_runs":  runs,
		"coverage": map[string]any{
			"scope":     "default_branch_head_check_runs",
			"bounded":   true,
			"limit":     limit,
			"returned":  len(runs),
			"truncated": value.TotalCount > len(runs) || len(runs) >= limit,
			"filter":    "latest",
			"excluded":  []string{"issues", "pull_requests", "message_content"},
		},
	}
}

func makeObservation(deviceID, sourceInstance, recordKey, kind, endpoint string, capturedAt time.Time, projection map[string]any, response []byte) (model.Observation, error) {
	digest := sha256.Sum256(response)
	// Keep the response digest in the projected source payload as well as the
	// provenance envelope. This makes every received byte part of revision
	// identity even when a model implementation treats provenance metadata as
	// non-semantic.
	projection["source_response_sha256"] = hex.EncodeToString(digest[:])
	payload := map[string]any{
		"_telemetry": map[string]any{
			"capture_time_kind":     "capture_time",
			"endpoint":              endpoint,
			"response_sha256":       hex.EncodeToString(digest[:]),
			"response_bytes_base64": base64.StdEncoding.EncodeToString(response),
			"response_bytes_length": len(response),
		},
		"github": projection,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return model.Observation{}, fmt.Errorf("encode github %s observation: %w", kind, err)
	}
	observation := model.Observation{
		SourceInstanceKey:   sourceInstance,
		SourceKind:          "github_repository",
		DeviceID:            deviceID,
		SourceRecordKey:     recordKey,
		Kind:                kind,
		OccurredAt:          capturedAt.UTC(),
		CollectedAt:         capturedAt.UTC(),
		SourceSchemaVersion: "github-rest-api-" + apiVersion,
		OriginalPayload:     raw,
	}
	if err := observation.Normalize(); err != nil {
		return model.Observation{}, fmt.Errorf("normalize github %s observation: %w", kind, err)
	}
	return observation, nil
}

func validEndpoint(endpoint string) bool {
	_, ok := selectionFor(endpoint)
	return ok
}

func selectionFor(endpoint string) (string, bool) {
	if strings.HasPrefix(endpoint, "/") || !strings.HasPrefix(endpoint, "repos/") || strings.Contains(endpoint, "..") {
		return "", false
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Path == "" {
		return "", false
	}
	switch {
	case isRepositoryPath(parsed.Path):
		return `{full_name: .full_name, default_branch: .default_branch, private: .private, visibility: .visibility, archived: .archived, disabled: .disabled, updated_at: .updated_at, pushed_at: .pushed_at}`, true
	case strings.Contains(parsed.Path, "/branches/") && parsed.RawQuery == "":
		return `{name: .name, protected: .protected, commit: {sha: .commit.sha}}`, true
	case strings.HasSuffix(parsed.Path, "/status") && parsed.RawQuery != "":
		return `{state: .state, total_count: .total_count, statuses: [.statuses[] | {context: .context, state: .state, created_at: .created_at, updated_at: .updated_at}]}`, true
	case strings.HasSuffix(parsed.Path, "/check-runs") && parsed.RawQuery != "":
		return `{total_count: .total_count, check_runs: [.check_runs[] | {id: .id, name: .name, status: .status, conclusion: .conclusion, started_at: .started_at, completed_at: .completed_at}]}`, true
	default:
		return "", false
	}
}

func isRepositoryPath(path string) bool {
	parts := strings.Split(path, "/")
	return len(parts) == 3 && parts[0] == "repos" && parts[1] != "" && parts[2] != ""
}
