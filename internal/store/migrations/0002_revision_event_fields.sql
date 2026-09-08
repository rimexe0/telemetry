ALTER TABLE observation_revisions
    ADD COLUMN IF NOT EXISTS kind TEXT,
    ADD COLUMN IF NOT EXISTS occurred_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS ended_at TIMESTAMPTZ;

UPDATE observation_revisions AS revision
SET
    kind = observation.kind,
    occurred_at = observation.occurred_at,
    ended_at = observation.ended_at
FROM observations AS observation
WHERE revision.observation_id = observation.id
  AND revision.occurred_at IS NULL;

ALTER TABLE observation_revisions
    ALTER COLUMN kind SET NOT NULL,
    ALTER COLUMN occurred_at SET NOT NULL;

UPDATE observations AS observation
SET
    kind = revision.kind,
    occurred_at = revision.occurred_at,
    ended_at = revision.ended_at
FROM observation_revisions AS revision
WHERE observation.effective_revision_id = revision.id;
