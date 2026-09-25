-- Explicit owner mappings from strong context signals to activities (issue
-- #13). Rules are never edited; replacing one disables it and adds another,
-- so earlier alignment evidence remains explainable.

CREATE TABLE IF NOT EXISTS activity_context_rules (
    id BIGSERIAL PRIMARY KEY,
    activity_id BIGINT NOT NULL REFERENCES activities(id),
    signal TEXT NOT NULL,
    pattern TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    disabled_at TIMESTAMPTZ,
    CONSTRAINT activity_context_rules_signal_check CHECK (signal IN ('path_prefix', 'domain', 'url_prefix', 'workspace_id')),
    CONSTRAINT activity_context_rules_pattern_check CHECK (length(pattern) BETWEEN 1 AND 500)
);

CREATE UNIQUE INDEX IF NOT EXISTS activity_context_rules_active_unique
    ON activity_context_rules (activity_id, signal, pattern) WHERE disabled_at IS NULL;
