-- Changes an assistant applied after the owner approved them in conversation
-- (owner decision 2026-09-30). Each row keeps the state it replaced so it can
-- be reverted; a revert is itself a change. Rows are never deleted.

CREATE TABLE IF NOT EXISTS agent_changes (
    id BIGSERIAL PRIMARY KEY,
    kind TEXT NOT NULL,
    changes JSONB NOT NULL,
    previous JSONB NOT NULL,
    reason TEXT NOT NULL,
    policy_version_id BIGINT REFERENCES policy_versions(id),
    revert_of BIGINT REFERENCES agent_changes(id),
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    reverted_at TIMESTAMPTZ,
    reverted_by BIGINT REFERENCES agent_changes(id),
    CONSTRAINT agent_changes_kind_check CHECK (kind IN ('blocklist', 'labels')),
    CONSTRAINT agent_changes_reason_check CHECK (length(reason) <= 1000)
);

CREATE INDEX IF NOT EXISTS agent_changes_applied_index ON agent_changes (id DESC);
