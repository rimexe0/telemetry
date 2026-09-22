-- Execution sessions connect owner intent to the actual lifecycle of one task.
-- Events are append-only; the session row is the current state projection.

CREATE TABLE IF NOT EXISTS execution_sessions (
    id BIGSERIAL PRIMARY KEY,
    activity_id BIGINT NOT NULL REFERENCES activities(id),
    plan_block_id BIGINT REFERENCES plan_blocks(id),
    started_at TIMESTAMPTZ NOT NULL,
    ended_at TIMESTAMPTZ,
    state TEXT NOT NULL,
    next_action_snapshot TEXT NOT NULL,
    resume_marker TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT execution_sessions_state_check
        CHECK (state IN ('active', 'waiting_external', 'interrupted', 'completed', 'abandoned')),
    CONSTRAINT execution_sessions_terminal_check
        CHECK ((state IN ('completed', 'abandoned')) = (ended_at IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS execution_sessions_activity_index
    ON execution_sessions (activity_id, started_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS execution_sessions_current_index
    ON execution_sessions (updated_at DESC, id DESC)
    WHERE ended_at IS NULL;

CREATE TABLE IF NOT EXISTS execution_events (
    id BIGSERIAL PRIMARY KEY,
    session_id BIGINT NOT NULL REFERENCES execution_sessions(id),
    event_type TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT execution_events_type_check
        CHECK (event_type IN ('started', 'paused', 'waiting_external', 'interrupted', 'resumed', 'switched_out', 'completed', 'abandoned'))
);

CREATE INDEX IF NOT EXISTS execution_events_session_index
    ON execution_events (session_id, id);
