-- The assistant asks once when a planned block has not started (2026-10-06).
ALTER TABLE outreach_log DROP CONSTRAINT IF EXISTS outreach_log_kind_check;
ALTER TABLE outreach_log ADD CONSTRAINT outreach_log_kind_check CHECK (kind IN ('morning', 'off_plan', 'collection', 'missed_start'));

-- Named places from OpenStreetMap (shops, cafés, hospitals, stops…), so the
-- owner's stays can be matched to what is there without sending their
-- location anywhere. Replaced as a whole on each import; this is reference
-- data, not the owner's history.
CREATE TABLE IF NOT EXISTS map_places (
    id BIGSERIAL PRIMARY KEY,
    osm_ref TEXT NOT NULL,
    name TEXT NOT NULL,
    kind TEXT NOT NULL,
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    lat DOUBLE PRECISION NOT NULL,
    lon DOUBLE PRECISION NOT NULL
);
CREATE INDEX IF NOT EXISTS map_places_position ON map_places (lat, lon);

CREATE TABLE IF NOT EXISTS map_imports (
    id BIGSERIAL PRIMARY KEY,
    source TEXT NOT NULL,
    places INTEGER NOT NULL,
    imported_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
