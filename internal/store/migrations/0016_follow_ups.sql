-- Follow-ups: things the owner must come back to at a time, held by the
-- system so nothing depends on the owner's memory (owner decision
-- 2026-10-01). A follow-up stays open until it is marked done or dropped.

CREATE TABLE IF NOT EXISTS follow_ups (
    id BIGSERIAL PRIMARY KEY,
    title TEXT NOT NULL,
    due_at TIMESTAMPTZ NOT NULL,
    nag_minutes INT,
    state TEXT NOT NULL DEFAULT 'open',
    closed_at TIMESTAMPTZ,
    closed_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT follow_ups_title_check CHECK (char_length(title) BETWEEN 1 AND 200),
    CONSTRAINT follow_ups_nag_check CHECK (nag_minutes IS NULL OR nag_minutes BETWEEN 1 AND 1440),
    CONSTRAINT follow_ups_state_check CHECK (state IN ('open', 'done', 'dropped'))
);

CREATE INDEX IF NOT EXISTS follow_ups_open_index ON follow_ups (due_at) WHERE state = 'open';

ALTER TABLE agent_changes DROP CONSTRAINT IF EXISTS agent_changes_kind_check;
ALTER TABLE agent_changes ADD CONSTRAINT agent_changes_kind_check
    CHECK (kind IN ('blocklist', 'labels', 'tasks', 'plan', 'follow_ups'));
