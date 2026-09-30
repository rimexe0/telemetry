-- Collection problems found by the silence checker (issue #28). An incident
-- opens when a source goes quiet in a way that is a problem and closes when
-- it recovers; history is kept.

CREATE TABLE IF NOT EXISTS collection_incidents (
    id BIGSERIAL PRIMARY KEY,
    device TEXT NOT NULL,
    kind TEXT NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    detected_at TIMESTAMPTZ NOT NULL,
    ended_at TIMESTAMPTZ,
    detail JSONB NOT NULL DEFAULT '{}'
);

CREATE UNIQUE INDEX IF NOT EXISTS collection_incidents_open_index
    ON collection_incidents (device, kind) WHERE ended_at IS NULL;
CREATE INDEX IF NOT EXISTS collection_incidents_time_index
    ON collection_incidents (detected_at DESC);
