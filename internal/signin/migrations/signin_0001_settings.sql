-- How the owner signs in to the panel, set from the panel (2026-11-21). One
-- row per settings key; every change is kept with the value it replaced so it
-- can be read back and reverted by hand.
CREATE TABLE IF NOT EXISTS telemetry_settings (
    key TEXT PRIMARY KEY,
    value JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS telemetry_settings_changes (
    id BIGSERIAL PRIMARY KEY,
    key TEXT NOT NULL,
    changed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    change TEXT NOT NULL,
    previous JSONB NOT NULL,
    value JSONB NOT NULL
);

CREATE INDEX IF NOT EXISTS telemetry_settings_changes_key_index ON telemetry_settings_changes (key, id DESC);
