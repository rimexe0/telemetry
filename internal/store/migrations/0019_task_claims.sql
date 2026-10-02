-- The owner's own answer to "what are you working on", given on a device
-- when the rules cannot tell (owner decision 2026-10-02). A NULL task means
-- "not work". Claims are evidence and are never edited.

CREATE TABLE IF NOT EXISTS task_claims (
    id BIGSERIAL PRIMARY KEY,
    task_id BIGINT REFERENCES activities(id),
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    device_id TEXT NOT NULL,
    context TEXT NOT NULL DEFAULT '',
    CONSTRAINT task_claims_context_check CHECK (char_length(context) <= 500)
);

CREATE INDEX IF NOT EXISTS task_claims_started_index ON task_claims (started_at);
