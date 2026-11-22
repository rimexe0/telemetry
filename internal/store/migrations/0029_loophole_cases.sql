-- Loophole cases (owner request 2026-11-22). Every accepted change that
-- loosens blocking, from the panel or an agent, is recorded. After a quiet
-- spell the server asks a coding agent (a T3 Code webhook) to investigate.
-- The agent reports back here, and its fix PR merges after five hours
-- without an answer, with the owner told hourly in between.
CREATE TABLE IF NOT EXISTS loophole_cases (
    id BIGSERIAL PRIMARY KEY,
    opened_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_event_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    events JSONB NOT NULL DEFAULT '[]'::jsonb,
    dispatched_at TIMESTAMPTZ,
    dispatch_attempts INT NOT NULL DEFAULT 0,
    dispatch_error TEXT NOT NULL DEFAULT '',
    reported_at TIMESTAMPTZ,
    report TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL DEFAULT '',
    pr_url TEXT NOT NULL DEFAULT '',
    thread_url TEXT NOT NULL DEFAULT '',
    merge_after TIMESTAMPTZ,
    reminders INT NOT NULL DEFAULT 0,
    reminded_at TIMESTAMPTZ,
    answered_at TIMESTAMPTZ,
    answer TEXT NOT NULL DEFAULT '',
    closed_at TIMESTAMPTZ,
    outcome TEXT NOT NULL DEFAULT '',
    outcome_note TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS loophole_cases_open_index ON loophole_cases (opened_at) WHERE closed_at IS NULL;

CREATE TABLE IF NOT EXISTS loophole_settings (
    id INT PRIMARY KEY CHECK (id = 1),
    webhook_url TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
