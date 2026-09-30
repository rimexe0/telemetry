-- Owner labels for apps and sites, such as "distraction" or "work". Agents may
-- only propose label changes; the owner accepts the exact reviewed content.
-- Labels are never deleted: removal sets removed_at.

CREATE TABLE IF NOT EXISTS activity_label_proposals (
    id BIGSERIAL PRIMARY KEY,
    changes JSONB NOT NULL,
    reason TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at TIMESTAMPTZ,
    decision_note TEXT NOT NULL DEFAULT '',
    decision_provenance JSONB,
    CONSTRAINT activity_label_proposals_status_check CHECK (status IN ('pending', 'accepted', 'rejected')),
    CONSTRAINT activity_label_proposals_decided_check CHECK ((status = 'pending') = (decided_at IS NULL)),
    CONSTRAINT activity_label_proposals_reason_check CHECK (length(reason) <= 1000)
);

CREATE TABLE IF NOT EXISTS activity_labels (
    id BIGSERIAL PRIMARY KEY,
    kind TEXT NOT NULL,
    match TEXT NOT NULL,
    label TEXT NOT NULL,
    proposal_id BIGINT REFERENCES activity_label_proposals(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    removed_at TIMESTAMPTZ,
    CONSTRAINT activity_labels_kind_check CHECK (kind IN ('app', 'site'))
);

CREATE UNIQUE INDEX IF NOT EXISTS activity_labels_current_index
    ON activity_labels (kind, lower(match)) WHERE removed_at IS NULL;
