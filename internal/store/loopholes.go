package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Loophole cases: the owner asked that a coding agent look into every
// loosening, report on it, and fix the loophole if there is one, without a
// message for each change (owner request 2026-11-22).
const (
	// LoopholeSettle is how long after the last loosening a case waits
	// before an agent is asked to look at it. Several changes in a row become
	// one case.
	LoopholeSettle = 10 * time.Minute
	// LoopholeMergeAfter is how long the owner has to answer a report with a
	// fix before the fix merges. LoopholeReminders messages are sent in that
	// time, one an hour.
	LoopholeMergeAfter = 5 * time.Hour
	LoopholeReminders  = 5
	LoopholeRemindEach = time.Hour
	// loopholeDispatchTries is how often a case is offered to the webhook.
	loopholeDispatchTries = 30
)

// Outcomes a case closes with.
const (
	LoopholeMerged       = "merged"
	LoopholeDropped      = "dropped"
	LoopholeNothingFound = "nothing_found"
	LoopholeChanged      = "changed"
)

// LoopholeEvent is one accepted loosening.
type LoopholeEvent struct {
	At     time.Time `json:"at"`
	Source string    `json:"source" jsonschema:"schedule, blocklist, labels, label_removed, policy or pin_unlock"`
	What   string    `json:"what"`
	Reason string    `json:"reason,omitempty"`
	Detail any       `json:"detail,omitempty"`
}

// LoopholeCase is the loosenings close together and what became of them.
type LoopholeCase struct {
	ID               int64           `json:"id"`
	OpenedAt         time.Time       `json:"opened_at"`
	LastEventAt      time.Time       `json:"last_event_at"`
	Events           []LoopholeEvent `json:"events"`
	DispatchedAt     *time.Time      `json:"dispatched_at,omitempty"`
	DispatchAttempts int             `json:"dispatch_attempts"`
	DispatchError    string          `json:"dispatch_error,omitempty"`
	ReportedAt       *time.Time      `json:"reported_at,omitempty"`
	Report           string          `json:"report,omitempty"`
	Message          string          `json:"message,omitempty"`
	PRURL            string          `json:"pr_url,omitempty"`
	ThreadURL        string          `json:"thread_url,omitempty"`
	MergeAfter       *time.Time      `json:"merge_after,omitempty"`
	Reminders        int             `json:"reminders"`
	RemindedAt       *time.Time      `json:"reminded_at,omitempty"`
	AnsweredAt       *time.Time      `json:"answered_at,omitempty"`
	Answer           string          `json:"answer,omitempty"`
	ClosedAt         *time.Time      `json:"closed_at,omitempty"`
	Outcome          string          `json:"outcome,omitempty"`
	OutcomeNote      string          `json:"outcome_note,omitempty"`
}

// Status is where the case stands: waiting (to be handed to an agent),
// investigating, reported (waiting for the owner), answered, or closed.
func (c LoopholeCase) Status() string {
	switch {
	case c.ClosedAt != nil:
		return "closed"
	case c.AnsweredAt != nil:
		return "answered"
	case c.ReportedAt != nil:
		return "reported"
	case c.DispatchedAt != nil:
		return "investigating"
	}
	return "waiting"
}

// ReminderDue reports whether the owner should be told about a reported
// case now: once when it is reported, then each hour until the fix merges.
// A report without a fix is told once.
func (c LoopholeCase) ReminderDue(now time.Time) bool {
	if c.Status() != "reported" || strings.TrimSpace(c.Message) == "" {
		return false
	}
	if c.MergeAfter == nil {
		return c.Reminders == 0
	}
	if c.Reminders >= LoopholeReminders || !now.Before(*c.MergeAfter) {
		return false
	}
	return c.RemindedAt == nil || now.Sub(*c.RemindedAt) >= LoopholeRemindEach-time.Minute
}

// LoopholeStore keeps loophole cases. Postgres implements it.
type LoopholeStore interface {
	LoopholeCases(ctx context.Context, openOnly bool, limit int) ([]LoopholeCase, error)
	LoopholeCase(ctx context.Context, id int64) (LoopholeCase, error)
	LoopholesToDispatch(ctx context.Context, now time.Time) ([]LoopholeCase, error)
	MarkLoopholeDispatched(ctx context.Context, id int64, failure error) error
	ReportLoophole(ctx context.Context, id int64, report, message, prURL, threadURL string) (LoopholeCase, error)
	MarkLoopholeReminded(ctx context.Context, id int64) error
	AnswerLoophole(ctx context.Context, id int64, answer string) (LoopholeCase, error)
	CloseLoophole(ctx context.Context, id int64, outcome, note string) (LoopholeCase, error)
	LoopholeWebhook(ctx context.Context) (string, error)
	SetLoopholeWebhook(ctx context.Context, webhook string) error
}

var _ LoopholeStore = (*Postgres)(nil)

// ErrInvalidLoophole is a report, answer or setting that cannot be stored.
var ErrInvalidLoophole = errors.New("invalid loophole case")

type execQuerier interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// noteLoosening records an accepted loosening, as part of the change's own
// transaction, so a change that is rolled back (a preview) leaves nothing.
// It joins the case still waiting to be handed over, if there is one.
func noteLoosening(ctx context.Context, q execQuerier, event LoopholeEvent) error {
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	encoded, err := json.Marshal([]LoopholeEvent{event})
	if err != nil {
		return err
	}
	var id int64
	err = q.QueryRow(ctx, `SELECT id FROM loophole_cases WHERE dispatched_at IS NULL AND closed_at IS NULL ORDER BY id DESC LIMIT 1 FOR UPDATE`).Scan(&id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		_, err = q.Exec(ctx, `INSERT INTO loophole_cases (opened_at, last_event_at, events) VALUES ($1, $1, $2::jsonb)`, event.At, string(encoded))
		return err
	case err != nil:
		return err
	}
	_, err = q.Exec(ctx, `UPDATE loophole_cases SET events = events || $2::jsonb, last_event_at = $3 WHERE id = $1`, id, string(encoded), event.At)
	return err
}

const loopholeColumns = `id, opened_at, last_event_at, events, dispatched_at, dispatch_attempts, dispatch_error, reported_at, report, message, pr_url, thread_url,
	merge_after, reminders, reminded_at, answered_at, answer, closed_at, outcome, outcome_note`

func scanLoophole(row pgx.Row) (LoopholeCase, error) {
	var c LoopholeCase
	var events []byte
	err := row.Scan(&c.ID, &c.OpenedAt, &c.LastEventAt, &events, &c.DispatchedAt, &c.DispatchAttempts, &c.DispatchError, &c.ReportedAt, &c.Report, &c.Message,
		&c.PRURL, &c.ThreadURL, &c.MergeAfter, &c.Reminders, &c.RemindedAt, &c.AnsweredAt, &c.Answer, &c.ClosedAt, &c.Outcome, &c.OutcomeNote)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(events, &c.Events); err != nil {
		return c, err
	}
	return c, nil
}

func (p *Postgres) loopholes(ctx context.Context, query string, args ...any) ([]LoopholeCase, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+loopholeColumns+` FROM loophole_cases `+query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cases := []LoopholeCase{}
	for rows.Next() {
		c, err := scanLoophole(rows)
		if err != nil {
			return nil, err
		}
		cases = append(cases, c)
	}
	return cases, rows.Err()
}

// LoopholeCases lists cases, newest first.
func (p *Postgres) LoopholeCases(ctx context.Context, openOnly bool, limit int) ([]LoopholeCase, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	where := ""
	if openOnly {
		where = "WHERE closed_at IS NULL "
	}
	return p.loopholes(ctx, where+"ORDER BY id DESC LIMIT $1", limit)
}

func (p *Postgres) LoopholeCase(ctx context.Context, id int64) (LoopholeCase, error) {
	c, err := scanLoophole(p.pool.QueryRow(ctx, `SELECT `+loopholeColumns+` FROM loophole_cases WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// LoopholesToDispatch are the cases quiet for LoopholeSettle and not yet
// taken by an agent.
func (p *Postgres) LoopholesToDispatch(ctx context.Context, now time.Time) ([]LoopholeCase, error) {
	return p.loopholes(ctx, `WHERE dispatched_at IS NULL AND closed_at IS NULL AND last_event_at <= $1 AND dispatch_attempts < $2 ORDER BY id`,
		now.Add(-LoopholeSettle), loopholeDispatchTries)
}

// MarkLoopholeDispatched records an attempt to hand a case to the webhook.
func (p *Postgres) MarkLoopholeDispatched(ctx context.Context, id int64, failure error) error {
	if failure != nil {
		message := failure.Error()
		if len(message) > 500 {
			message = message[:500]
		}
		_, err := p.pool.Exec(ctx, `UPDATE loophole_cases SET dispatch_attempts = dispatch_attempts + 1, dispatch_error = $2 WHERE id = $1`, id, message)
		return err
	}
	_, err := p.pool.Exec(ctx, `UPDATE loophole_cases SET dispatch_attempts = dispatch_attempts + 1, dispatch_error = '', dispatched_at = now() WHERE id = $1`, id)
	return err
}

func cleanText(value string, limit int, name string) (string, error) {
	value = strings.TrimSpace(value)
	if len([]rune(value)) > limit {
		return "", fmt.Errorf("%w: %s must be at most %d characters", ErrInvalidLoophole, name, limit)
	}
	return value, nil
}

func httpsURL(value, name string) error {
	if value == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("%w: %s must be an https URL", ErrInvalidLoophole, name)
	}
	return nil
}

// ReportLoophole stores the agent's report. With a fix (a PR), the fix
// merges LoopholeMergeAfter from now unless the owner answers first.
func (p *Postgres) ReportLoophole(ctx context.Context, id int64, report, message, prURL, threadURL string) (LoopholeCase, error) {
	var err error
	if report, err = cleanText(report, 20000, "report"); err != nil {
		return LoopholeCase{}, err
	}
	if message, err = cleanText(message, 1500, "message"); err != nil {
		return LoopholeCase{}, err
	}
	prURL, threadURL = strings.TrimSpace(prURL), strings.TrimSpace(threadURL)
	if err := httpsURL(prURL, "pr_url"); err != nil {
		return LoopholeCase{}, err
	}
	if err := httpsURL(threadURL, "thread_url"); err != nil {
		return LoopholeCase{}, err
	}
	if report == "" || message == "" {
		return LoopholeCase{}, fmt.Errorf("%w: a report and a message are required", ErrInvalidLoophole)
	}
	c, err := scanLoophole(p.pool.QueryRow(ctx, `UPDATE loophole_cases SET reported_at = now(), report = $2, message = $3, pr_url = $4, thread_url = $5,
		merge_after = CASE WHEN $4 = '' THEN NULL ELSE now() + $6::interval END, reminders = 0, reminded_at = NULL,
		dispatched_at = COALESCE(dispatched_at, now())
		WHERE id = $1 AND closed_at IS NULL AND answered_at IS NULL RETURNING `+loopholeColumns,
		id, report, message, prURL, threadURL, LoopholeMergeAfter.String()))
	if errors.Is(err, pgx.ErrNoRows) {
		return c, fmt.Errorf("%w: case %d is not open, or the owner already answered it", ErrInvalidLoophole, id)
	}
	return c, err
}

func (p *Postgres) MarkLoopholeReminded(ctx context.Context, id int64) error {
	_, err := p.pool.Exec(ctx, `UPDATE loophole_cases SET reminders = reminders + 1, reminded_at = now() WHERE id = $1`, id)
	return err
}

// AnswerLoophole records what the owner said about a report; it stops the
// reminders and the merge without an answer.
func (p *Postgres) AnswerLoophole(ctx context.Context, id int64, answer string) (LoopholeCase, error) {
	answer, err := cleanText(answer, 4000, "answer")
	if err != nil {
		return LoopholeCase{}, err
	}
	if answer == "" {
		return LoopholeCase{}, fmt.Errorf("%w: the answer is required", ErrInvalidLoophole)
	}
	c, err := scanLoophole(p.pool.QueryRow(ctx, `UPDATE loophole_cases SET answered_at = now(), answer = $2 WHERE id = $1 AND closed_at IS NULL RETURNING `+loopholeColumns, id, answer))
	if errors.Is(err, pgx.ErrNoRows) {
		return c, fmt.Errorf("%w: case %d is not open", ErrInvalidLoophole, id)
	}
	return c, err
}

// CloseLoophole records how a case ended.
func (p *Postgres) CloseLoophole(ctx context.Context, id int64, outcome, note string) (LoopholeCase, error) {
	switch outcome {
	case LoopholeMerged, LoopholeDropped, LoopholeNothingFound, LoopholeChanged:
	default:
		return LoopholeCase{}, fmt.Errorf("%w: outcome must be merged, dropped, nothing_found or changed", ErrInvalidLoophole)
	}
	note, err := cleanText(note, 4000, "note")
	if err != nil {
		return LoopholeCase{}, err
	}
	c, err := scanLoophole(p.pool.QueryRow(ctx, `UPDATE loophole_cases SET closed_at = now(), outcome = $2, outcome_note = $3,
		dispatched_at = COALESCE(dispatched_at, now()) WHERE id = $1 AND closed_at IS NULL RETURNING `+loopholeColumns, id, outcome, note))
	if errors.Is(err, pgx.ErrNoRows) {
		return c, fmt.Errorf("%w: case %d is not open", ErrInvalidLoophole, id)
	}
	return c, err
}

// LoopholeWebhook is where new cases are sent; empty when none is set.
func (p *Postgres) LoopholeWebhook(ctx context.Context) (string, error) {
	var webhook string
	err := p.pool.QueryRow(ctx, `SELECT webhook_url FROM loophole_settings WHERE id = 1`).Scan(&webhook)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return webhook, err
}

func (p *Postgres) SetLoopholeWebhook(ctx context.Context, webhook string) error {
	webhook = strings.TrimSpace(webhook)
	if err := httpsURL(webhook, "webhook_url"); err != nil {
		return err
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO loophole_settings (id, webhook_url, updated_at) VALUES (1, $1, now())
		ON CONFLICT (id) DO UPDATE SET webhook_url = EXCLUDED.webhook_url, updated_at = EXCLUDED.updated_at`, webhook)
	return err
}
