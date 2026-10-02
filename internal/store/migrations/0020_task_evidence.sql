-- What each task has learned about the owner: which things on screen were
-- part of it and which were not (owner decision 2026-10-02: per-task models
-- that learn from the owner's own answers). An entry is evidence, not a
-- rule: a newer answer replaces it, and the old one is kept.

CREATE TABLE IF NOT EXISTS task_evidence (
    id BIGSERIAL PRIMARY KEY,
    task_id BIGINT NOT NULL REFERENCES activities(id),
    context TEXT NOT NULL,
    verdict TEXT NOT NULL,
    source TEXT NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    device_id TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    superseded_at TIMESTAMPTZ,
    CONSTRAINT task_evidence_context_check CHECK (char_length(context) BETWEEN 1 AND 500),
    CONSTRAINT task_evidence_verdict_check CHECK (verdict IN ('related', 'unrelated', 'unsure')),
    CONSTRAINT task_evidence_source_check CHECK (source IN ('owner', 'judge'))
);

CREATE UNIQUE INDEX IF NOT EXISTS task_evidence_current_index
    ON task_evidence (task_id, lower(context)) WHERE superseded_at IS NULL;
