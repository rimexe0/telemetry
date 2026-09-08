CREATE TABLE IF NOT EXISTS ingestion_batches (
    batch_id TEXT PRIMARY KEY,
    device_id TEXT NOT NULL,
    collector_version TEXT NOT NULL,
    request_digest TEXT NOT NULL,
    received_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS source_instances (
    source_instance_key TEXT PRIMARY KEY,
    source_kind TEXT NOT NULL,
    device_id TEXT NOT NULL,
    first_collected_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS observations (
    id BIGSERIAL PRIMARY KEY,
    source_instance_key TEXT NOT NULL REFERENCES source_instances(source_instance_key),
    source_record_key TEXT NOT NULL,
    kind TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    ended_at TIMESTAMPTZ,
    collected_at TIMESTAMPTZ NOT NULL,
    effective_revision_id BIGINT,
    UNIQUE (source_instance_key, source_record_key)
);

CREATE TABLE IF NOT EXISTS observation_revisions (
    id BIGSERIAL PRIMARY KEY,
    observation_id BIGINT NOT NULL REFERENCES observations(id),
    revision_digest TEXT NOT NULL,
    original_payload BYTEA NOT NULL,
    projection JSONB NOT NULL,
    collected_at TIMESTAMPTZ NOT NULL,
    source_schema_version TEXT NOT NULL DEFAULT '',
    timezone TEXT NOT NULL DEFAULT '',
    UNIQUE (observation_id, revision_digest)
);

DO $$
BEGIN
    ALTER TABLE observations
        ADD CONSTRAINT observations_effective_revision_fk
        FOREIGN KEY (effective_revision_id) REFERENCES observation_revisions(id)
        DEFERRABLE INITIALLY DEFERRED;
EXCEPTION
    WHEN duplicate_object THEN NULL;
END $$;

CREATE TABLE IF NOT EXISTS ingestion_batch_observations (
    batch_id TEXT NOT NULL REFERENCES ingestion_batches(batch_id),
    observation_revision_id BIGINT NOT NULL REFERENCES observation_revisions(id),
    PRIMARY KEY (batch_id, observation_revision_id)
);

CREATE INDEX IF NOT EXISTS observations_time_index
    ON observations (occurred_at, id);

CREATE INDEX IF NOT EXISTS observations_source_time_index
    ON observations (source_instance_key, occurred_at, id);
