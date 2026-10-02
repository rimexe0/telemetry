-- Distractions closed by default with scheduled distraction time, and one-off
-- openings for a stated purpose (owner decision 2026-10-02).

CREATE TABLE IF NOT EXISTS openings (
    id BIGSERIAL PRIMARY KEY,
    purpose TEXT NOT NULL,
    sites TEXT[] NOT NULL DEFAULT '{}',
    apps TEXT[] NOT NULL DEFAULT '{}',
    everything BOOLEAN NOT NULL DEFAULT false,
    until TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at TIMESTAMPTZ,
    CONSTRAINT openings_purpose_check CHECK (char_length(purpose) BETWEEN 1 AND 300)
);

CREATE INDEX IF NOT EXISTS openings_until_index ON openings (until);

ALTER TABLE agent_changes DROP CONSTRAINT IF EXISTS agent_changes_kind_check;
ALTER TABLE agent_changes ADD CONSTRAINT agent_changes_kind_check
    CHECK (kind IN ('blocklist', 'labels', 'tasks', 'plan', 'follow_ups', 'rules', 'schedule', 'opening'));
