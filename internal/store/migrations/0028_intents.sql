-- What the owner is trying to do, worked out by the system from everything
-- the collectors saw (owner decision 2026-11-14). The owner did not use
-- tasks, plans or focus sessions, so nothing here depends on them.
--
-- intent_readings keeps every reading unchanged: what the assistant was
-- shown, what it printed, and what was read from that. intents is the running list those readings
-- maintain; each row names the reading that last changed it, so every
-- earlier state can be read back from the readings. An intent is an
-- inference, never something the owner said.

CREATE TABLE IF NOT EXISTS intent_readings (
    id BIGSERIAL PRIMARY KEY,
    read_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    window_from TIMESTAMPTZ NOT NULL,
    window_to TIMESTAMPTZ NOT NULL,
    digest TEXT NOT NULL,
    output TEXT NOT NULL,
    answer JSONB NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    now_intent_id BIGINT
);

CREATE INDEX IF NOT EXISTS intent_readings_read_index ON intent_readings (read_at DESC);

CREATE TABLE IF NOT EXISTS intents (
    id BIGSERIAL PRIMARY KEY,
    title TEXT NOT NULL,
    kind TEXT NOT NULL,
    status TEXT NOT NULL,
    next_step TEXT NOT NULL DEFAULT '',
    evidence JSONB NOT NULL DEFAULT '[]',
    confidence TEXT NOT NULL DEFAULT '',
    first_seen TIMESTAMPTZ NOT NULL,
    last_seen TIMESTAMPTZ NOT NULL,
    reading_id BIGINT NOT NULL REFERENCES intent_readings (id),
    CONSTRAINT intents_kind_check CHECK (kind IN ('work', 'life', 'social', 'leisure', 'rest', 'unclear')),
    CONSTRAINT intents_status_check CHECK (status IN ('active', 'waiting', 'paused', 'done', 'dropped'))
);

CREATE INDEX IF NOT EXISTS intents_last_seen_index ON intents (last_seen DESC);
