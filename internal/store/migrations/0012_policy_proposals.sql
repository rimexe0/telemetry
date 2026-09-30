-- Proposed blocklist edits (agents propose, the owner accepts the exact
-- content). Accepting appends a policy version; nothing is edited in place.

CREATE TABLE IF NOT EXISTS policy_proposals (
    id BIGSERIAL PRIMARY KEY,
    changes JSONB NOT NULL,
    reason TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at TIMESTAMPTZ,
    decision_note TEXT NOT NULL DEFAULT '',
    decision_provenance JSONB,
    result_policy_version_id BIGINT REFERENCES policy_versions(id),
    CONSTRAINT policy_proposals_status_check CHECK (status IN ('pending', 'accepted', 'rejected')),
    CONSTRAINT policy_proposals_decided_check CHECK ((status = 'pending') = (decided_at IS NULL)),
    CONSTRAINT policy_proposals_result_check CHECK ((status = 'accepted') = (result_policy_version_id IS NOT NULL)),
    CONSTRAINT policy_proposals_reason_check CHECK (length(reason) <= 1000)
);
