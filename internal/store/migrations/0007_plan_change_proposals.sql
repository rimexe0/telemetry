-- Reviewable plan changes (issue #3). A proposal binds to one base plan
-- version and one exact content hash. Proposals are never edited: a different
-- change is a new proposal. Accepting appends a plan version; old versions and
-- execution history are never rewritten.

CREATE TABLE IF NOT EXISTS plan_change_proposals (
    id BIGSERIAL PRIMARY KEY,
    plan_date DATE NOT NULL,
    base_plan_version_id BIGINT NOT NULL REFERENCES plan_versions(id),
    origin TEXT NOT NULL,
    reason TEXT NOT NULL,
    request JSONB NOT NULL,
    proposed_plan JSONB NOT NULL,
    changes JSONB NOT NULL,
    warnings JSONB NOT NULL DEFAULT '[]'::jsonb,
    content_hash TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at TIMESTAMPTZ,
    decision_note TEXT NOT NULL DEFAULT '',
    decision_provenance JSONB,
    result_plan_version_id BIGINT REFERENCES plan_versions(id),
    CONSTRAINT plan_change_proposals_origin_check CHECK (origin IN ('disruption_helper', 'explicit')),
    CONSTRAINT plan_change_proposals_status_check CHECK (status IN ('pending', 'accepted', 'rejected', 'stale')),
    CONSTRAINT plan_change_proposals_result_check CHECK ((status = 'accepted') = (result_plan_version_id IS NOT NULL)),
    CONSTRAINT plan_change_proposals_decided_check CHECK ((status = 'pending') = (decided_at IS NULL))
);

CREATE INDEX IF NOT EXISTS plan_change_proposals_date_index
    ON plan_change_proposals (plan_date, id DESC);

-- A block carried into a replanned version records the block it continues, so
-- execution sessions attached to the older block can still be related to it.
ALTER TABLE plan_blocks
    ADD COLUMN IF NOT EXISTS source_block_id BIGINT REFERENCES plan_blocks(id);
