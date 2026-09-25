package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	ContextSignalPathPrefix  = "path_prefix"
	ContextSignalDomain      = "domain"
	ContextSignalURLPrefix   = "url_prefix"
	ContextSignalWorkspaceID = "workspace_id"

	maxContextRules = 500
)

var (
	ErrInvalidContextRule = errors.New("invalid activity context rule")
	contextSignals        = map[string]struct{}{ContextSignalPathPrefix: {}, ContextSignalDomain: {}, ContextSignalURLPrefix: {}, ContextSignalWorkspaceID: {}}
)

// ActivityContextRule is an explicit owner mapping from a strong context
// signal to an activity. Application names are deliberately not a signal:
// multi-purpose tools say nothing about the project by themselves.
type ActivityContextRule struct {
	ID         int64      `json:"id"`
	ActivityID int64      `json:"activity_id"`
	Signal     string     `json:"signal"`
	Pattern    string     `json:"pattern"`
	Note       string     `json:"note,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	DisabledAt *time.Time `json:"disabled_at,omitempty"`
}

// ActivityContext is derived from one retained observation. It never replaces
// the observation; the source record key identifies the original.
type ActivityContext struct {
	App          string   `json:"app,omitempty"`
	Title        string   `json:"title,omitempty"`
	URL          string   `json:"url,omitempty"`
	Domain       string   `json:"domain,omitempty"`
	Paths        []string `json:"paths,omitempty"`
	Branch       string   `json:"branch,omitempty"`
	WorkspaceIDs []string `json:"workspace_ids,omitempty"`
	AgentSession string   `json:"agent_session,omitempty"`
}

func (c ActivityContext) empty() bool {
	return c.URL == "" && len(c.Paths) == 0 && len(c.WorkspaceIDs) == 0
}

// ContextMatch records which rule matched which observed value.
type ContextMatch struct {
	RuleID     int64  `json:"rule_id"`
	ActivityID int64  `json:"activity_id"`
	Signal     string `json:"signal"`
	Pattern    string `json:"pattern"`
	Value      string `json:"value"`
}

// ExtractActivityContext reads the context fields the existing sources
// already retain: ActivityWatch window/web events (app, title, url), the Codex
// watcher (project_path, cwd, chat_title), T3 Code projects and threads
// (workspace_root, worktree_path, project_id, branch) and git status snapshots.
func ExtractActivityContext(observation ObservationView) ActivityContext {
	var payload map[string]any
	if err := json.Unmarshal(observation.OriginalPayload, &payload); err != nil {
		return ActivityContext{}
	}
	data, ok := payload["data"].(map[string]any)
	if !ok {
		data = payload
	}
	result := ActivityContext{
		App:          alignmentString(data["app"]),
		Title:        alignmentString(data["title"]),
		URL:          alignmentString(data["url"]),
		Branch:       alignmentString(data["branch"]),
		AgentSession: alignmentString(data["chat_title"]),
	}
	if observation.SourceKind == "android_usage" {
		result.App = alignmentString(payload["package_name"])
	}
	if parsed, err := url.Parse(result.URL); err == nil && parsed.Host != "" {
		result.Domain = strings.ToLower(parsed.Hostname())
	}
	for _, key := range []string{"project_path", "cwd", "worktree_path", "workspace_root"} {
		if value := cleanContextPath(alignmentString(data[key])); value != "" && !containsString(result.Paths, value) {
			result.Paths = append(result.Paths, value)
		}
	}
	if observation.SourceKind == "t3code" {
		if id := alignmentString(data["project_id"]); id != "" {
			result.WorkspaceIDs = append(result.WorkspaceIDs, "t3:"+id)
		}
	}
	return result
}

func cleanContextPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !strings.HasPrefix(value, "/") {
		return ""
	}
	return path.Clean(value)
}

func containsString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func pathHasPrefix(value, prefix string) bool {
	return value == prefix || prefix == "/" || strings.HasPrefix(value, prefix+"/")
}

func domainMatches(domain, pattern string) bool {
	return domain == pattern || strings.HasSuffix(domain, "."+pattern)
}

// MatchActivityContext applies enabled rules deterministically. For each
// observed path only the longest matching prefix counts, so a rule for
// /work/client wins over /work. It returns every surviving match; callers
// treat more than one distinct activity as ambiguous rather than picking one.
func MatchActivityContext(context ActivityContext, rules []ActivityContextRule) []ContextMatch {
	matches := []ContextMatch{}
	if context.empty() {
		return matches
	}
	for _, value := range context.Paths {
		best := -1
		var candidates []ContextMatch
		for _, rule := range rules {
			if rule.DisabledAt != nil || rule.Signal != ContextSignalPathPrefix || !pathHasPrefix(value, rule.Pattern) {
				continue
			}
			if len(rule.Pattern) > best {
				best, candidates = len(rule.Pattern), nil
			}
			if len(rule.Pattern) == best {
				candidates = append(candidates, ContextMatch{RuleID: rule.ID, ActivityID: rule.ActivityID, Signal: rule.Signal, Pattern: rule.Pattern, Value: value})
			}
		}
		matches = append(matches, candidates...)
	}
	for _, rule := range rules {
		if rule.DisabledAt != nil {
			continue
		}
		switch rule.Signal {
		case ContextSignalDomain:
			if context.Domain != "" && domainMatches(context.Domain, rule.Pattern) {
				matches = append(matches, ContextMatch{RuleID: rule.ID, ActivityID: rule.ActivityID, Signal: rule.Signal, Pattern: rule.Pattern, Value: context.Domain})
			}
		case ContextSignalURLPrefix:
			if context.URL != "" && strings.HasPrefix(context.URL, rule.Pattern) {
				matches = append(matches, ContextMatch{RuleID: rule.ID, ActivityID: rule.ActivityID, Signal: rule.Signal, Pattern: rule.Pattern, Value: context.URL})
			}
		case ContextSignalWorkspaceID:
			if containsString(context.WorkspaceIDs, rule.Pattern) {
				matches = append(matches, ContextMatch{RuleID: rule.ID, ActivityID: rule.ActivityID, Signal: rule.Signal, Pattern: rule.Pattern, Value: rule.Pattern})
			}
		}
	}
	// A URL prefix is more specific than its domain; drop domain matches when a
	// URL prefix matched the same URL.
	if hasSignal(matches, ContextSignalURLPrefix) {
		filtered := matches[:0]
		for _, match := range matches {
			if match.Signal != ContextSignalDomain {
				filtered = append(filtered, match)
			}
		}
		matches = filtered
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].RuleID < matches[j].RuleID })
	return matches
}

func hasSignal(matches []ContextMatch, signal string) bool {
	for _, match := range matches {
		if match.Signal == signal {
			return true
		}
	}
	return false
}

// matchedActivities returns the distinct activity IDs in ascending order.
func matchedActivities(matches []ContextMatch) []int64 {
	seen := map[int64]struct{}{}
	ids := []int64{}
	for _, match := range matches {
		if _, ok := seen[match.ActivityID]; !ok {
			seen[match.ActivityID] = struct{}{}
			ids = append(ids, match.ActivityID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func normaliseContextRule(rule *ActivityContextRule) error {
	rule.Signal = strings.TrimSpace(rule.Signal)
	rule.Pattern = strings.TrimSpace(rule.Pattern)
	rule.Note = strings.TrimSpace(rule.Note)
	if rule.ActivityID <= 0 {
		return fmt.Errorf("%w: activity_id must reference an activity", ErrInvalidContextRule)
	}
	if _, ok := contextSignals[rule.Signal]; !ok {
		return fmt.Errorf("%w: signal must be path_prefix, domain, url_prefix or workspace_id", ErrInvalidContextRule)
	}
	if rule.Pattern == "" || len([]rune(rule.Pattern)) > maxPlanningTextRunes || len([]rune(rule.Note)) > maxPlanningTextRunes {
		return fmt.Errorf("%w: pattern is required and pattern and note must be at most %d characters", ErrInvalidContextRule, maxPlanningTextRunes)
	}
	switch rule.Signal {
	case ContextSignalPathPrefix:
		cleaned := cleanContextPath(rule.Pattern)
		if cleaned == "" || cleaned == "/" {
			return fmt.Errorf("%w: path_prefix must be an absolute path below /", ErrInvalidContextRule)
		}
		rule.Pattern = cleaned
	case ContextSignalDomain:
		rule.Pattern = strings.TrimPrefix(strings.ToLower(rule.Pattern), ".")
		if strings.ContainsAny(rule.Pattern, "/:@ ") || !strings.Contains(rule.Pattern, ".") {
			return fmt.Errorf("%w: domain must be a host name such as example.com", ErrInvalidContextRule)
		}
	case ContextSignalURLPrefix:
		parsed, err := url.Parse(rule.Pattern)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || strings.Trim(parsed.Path, "/") == "" {
			return fmt.Errorf("%w: url_prefix must be an http(s) URL with a path, such as https://github.com/owner/repo", ErrInvalidContextRule)
		}
	}
	return nil
}

func (p *Postgres) CreateActivityContextRule(ctx context.Context, rule ActivityContextRule) (ActivityContextRule, error) {
	if err := normaliseContextRule(&rule); err != nil {
		return ActivityContextRule{}, err
	}
	var count int
	if err := p.pool.QueryRow(ctx, `SELECT COUNT(*) FROM activity_context_rules WHERE disabled_at IS NULL`).Scan(&count); err != nil {
		return ActivityContextRule{}, err
	}
	if count >= maxContextRules {
		return ActivityContextRule{}, fmt.Errorf("%w: at most %d enabled rules are supported", ErrInvalidContextRule, maxContextRules)
	}
	created, err := scanContextRule(p.pool.QueryRow(ctx, `
		INSERT INTO activity_context_rules (activity_id, signal, pattern, note) VALUES ($1, $2, $3, $4)
		RETURNING id, activity_id, signal, pattern, note, created_at, disabled_at`,
		rule.ActivityID, rule.Signal, rule.Pattern, rule.Note))
	var postgresErr *pgconn.PgError
	if errors.As(err, &postgresErr) {
		switch postgresErr.Code {
		case "23503":
			return ActivityContextRule{}, ErrNotFound
		case "23505":
			return ActivityContextRule{}, fmt.Errorf("%w: the same enabled rule already exists", ErrInvalidContextRule)
		}
	}
	return created, err
}

func (p *Postgres) ListActivityContextRules(ctx context.Context, includeDisabled bool) ([]ActivityContextRule, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT id, activity_id, signal, pattern, note, created_at, disabled_at
		FROM activity_context_rules WHERE $1 OR disabled_at IS NULL
		ORDER BY id LIMIT $2`, includeDisabled, maxContextRules*2)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rules := []ActivityContextRule{}
	for rows.Next() {
		rule, err := scanContextRule(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

// DisableActivityContextRule keeps the row so earlier matches stay explainable.
func (p *Postgres) DisableActivityContextRule(ctx context.Context, id int64) (ActivityContextRule, error) {
	rule, err := scanContextRule(p.pool.QueryRow(ctx, `
		UPDATE activity_context_rules SET disabled_at = COALESCE(disabled_at, now()) WHERE id = $1
		RETURNING id, activity_id, signal, pattern, note, created_at, disabled_at`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ActivityContextRule{}, ErrNotFound
	}
	return rule, err
}

func scanContextRule(row pgx.Row) (ActivityContextRule, error) {
	var rule ActivityContextRule
	err := row.Scan(&rule.ID, &rule.ActivityID, &rule.Signal, &rule.Pattern, &rule.Note, &rule.CreatedAt, &rule.DisabledAt)
	return rule, err
}
