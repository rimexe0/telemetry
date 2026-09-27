-- Owner policy configuration for the deterministic policy engine (issue #19).
-- Versions are append-only; every decision names the version it used.

CREATE TABLE IF NOT EXISTS policy_versions (
    id BIGSERIAL PRIMARY KEY,
    config JSONB NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT policy_versions_note_check CHECK (length(note) <= 500)
);
