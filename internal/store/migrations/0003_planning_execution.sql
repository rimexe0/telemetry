-- Phase B/C planning state. Activity phase is separate from task status;
-- block events retain owner corrections instead of overwriting the last note.

ALTER TABLE activities
    ADD COLUMN IF NOT EXISTS activity_state TEXT NOT NULL DEFAULT 'growth';

ALTER TABLE activities
    ADD COLUMN IF NOT EXISTS resume_marker TEXT NOT NULL DEFAULT '';

ALTER TABLE activities DROP CONSTRAINT IF EXISTS activities_status_check;
ALTER TABLE activities
    ADD CONSTRAINT activities_status_check
    CHECK (status IN ('active', 'waiting', 'blocked', 'done', 'completed', 'abandoned', 'unknown'));

ALTER TABLE activities DROP CONSTRAINT IF EXISTS activities_activity_state_check;
ALTER TABLE activities
    ADD CONSTRAINT activities_activity_state_check
    CHECK (activity_state IN ('growth', 'maintenance', 'parked'));

ALTER TABLE plan_blocks DROP CONSTRAINT IF EXISTS plan_blocks_state_check;
ALTER TABLE plan_blocks
    ADD CONSTRAINT plan_blocks_state_check
    CHECK (state IN ('planned', 'active', 'done', 'skipped', 'switched', 'blocked', 'unknown'));

CREATE TABLE IF NOT EXISTS plan_block_events (
    id BIGSERIAL PRIMARY KEY,
    plan_block_id BIGINT NOT NULL REFERENCES plan_blocks(id),
    state TEXT NOT NULL,
    action TEXT NOT NULL DEFAULT 'state',
    explanation_code TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    provenance JSONB NOT NULL DEFAULT '{"recorded_by":"owner","source":"panel"}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT plan_block_events_state_check CHECK (state IN ('planned', 'active', 'done', 'skipped', 'switched', 'blocked', 'unknown')),
    CONSTRAINT plan_block_events_action_check CHECK (action IN ('planned', 'state', 'explanation', 'return')),
    CONSTRAINT plan_block_events_explanation_check CHECK (explanation_code IN ('', 'distracted', 'blocked', 'urgent', 'intentional_switch', 'unrealistic_plan', 'finished_early', 'other'))
);

CREATE INDEX IF NOT EXISTS plan_block_events_block_index
    ON plan_block_events (plan_block_id, id DESC);

CREATE TABLE IF NOT EXISTS planning_settings (
    id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    daily_plan_gate_enabled BOOLEAN NOT NULL DEFAULT false,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO planning_settings (id, daily_plan_gate_enabled)
VALUES (1, false)
ON CONFLICT (id) DO NOTHING;
