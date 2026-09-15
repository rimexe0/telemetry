-- Planning records are owner-authored intent, kept separately from collected
-- observations. Activity records follow R-017 (constraints and cost), R-052
-- (next action and parked ideas) and R-056 (task state). Plan versions are
-- append-only so an edited plan never overwrites what was planned before.

CREATE TABLE IF NOT EXISTS activities (
    id BIGSERIAL PRIMARY KEY,
    title TEXT NOT NULL,
    notes TEXT NOT NULL DEFAULT '',
    next_action TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active',
    priority INT NOT NULL DEFAULT 0,
    deadline TIMESTAMPTZ,
    effort_estimate_minutes INT,
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT activities_status_check CHECK (status IN ('active', 'waiting', 'blocked', 'done', 'abandoned'))
);

CREATE INDEX IF NOT EXISTS activities_status_index ON activities (status, priority DESC, id);

CREATE TABLE IF NOT EXISTS plan_versions (
    id BIGSERIAL PRIMARY KEY,
    plan_date DATE NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    origin TEXT NOT NULL DEFAULT 'owner',
    note TEXT NOT NULL DEFAULT '',
    CONSTRAINT plan_versions_origin_check CHECK (origin IN ('owner'))
);

CREATE INDEX IF NOT EXISTS plan_versions_date_index ON plan_versions (plan_date, created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS plan_blocks (
    id BIGSERIAL PRIMARY KEY,
    plan_version_id BIGINT NOT NULL REFERENCES plan_versions(id),
    activity_id BIGINT REFERENCES activities(id),
    title TEXT NOT NULL,
    start_time TIMESTAMPTZ NOT NULL,
    duration_minutes INT NOT NULL,
    fixed BOOLEAN NOT NULL DEFAULT false,
    state TEXT NOT NULL DEFAULT 'planned',
    state_note TEXT NOT NULL DEFAULT '',
    position INT NOT NULL DEFAULT 0,
    CONSTRAINT plan_blocks_state_check CHECK (state IN ('planned', 'active', 'done', 'skipped', 'switched')),
    CONSTRAINT plan_blocks_duration_check CHECK (duration_minutes BETWEEN 1 AND 1440)
);

CREATE INDEX IF NOT EXISTS plan_blocks_version_index ON plan_blocks (plan_version_id, position, id);
