-- Keep one primary objective and allow one actionable waiting interleave at a time.

ALTER TABLE execution_sessions
    ADD COLUMN execution_role TEXT NOT NULL DEFAULT 'primary',
    ADD COLUMN primary_session_id BIGINT REFERENCES execution_sessions(id),
    ADD CONSTRAINT execution_sessions_role_check
        CHECK (execution_role IN ('primary', 'waiting_interleave')),
    ADD CONSTRAINT execution_sessions_primary_link_check
        CHECK (
            (execution_role = 'primary' AND primary_session_id IS NULL)
            OR (execution_role = 'waiting_interleave' AND primary_session_id IS NOT NULL)
        ),
    ADD CONSTRAINT execution_sessions_not_own_primary_check
        CHECK (primary_session_id IS DISTINCT FROM id);

ALTER TABLE execution_events
    DROP CONSTRAINT IF EXISTS execution_events_type_check;

ALTER TABLE execution_events
    ADD CONSTRAINT execution_events_type_check
    CHECK (event_type IN (
        'started',
        'paused',
        'waiting_external',
        'external_ready',
        'interrupted',
        'resumed',
        'switched_out',
        'completed',
        'abandoned',
        'stuck_unknown_next_action',
        'stuck_emotional_overwhelm',
        'overwhelmed',
        'offline_recovery_started',
        'offline_recovery_ended',
        'owner_explanation'
    ));

DO $$
BEGIN
    IF (SELECT COUNT(*) FROM execution_sessions WHERE execution_role = 'primary' AND ended_at IS NULL) > 1 THEN
        RAISE EXCEPTION 'multiple non-terminal execution sessions exist; an owner must select the primary before migration';
    END IF;
END
$$;

CREATE UNIQUE INDEX execution_sessions_one_open_primary_index
    ON execution_sessions (execution_role)
    WHERE execution_role = 'primary' AND ended_at IS NULL;

CREATE UNIQUE INDEX execution_sessions_one_actionable_interleave_index
    ON execution_sessions (primary_session_id)
    WHERE execution_role = 'waiting_interleave'
      AND ended_at IS NULL
      AND state IN ('active', 'waiting_external');

CREATE INDEX execution_sessions_interleave_history_index
    ON execution_sessions (primary_session_id, started_at DESC, id DESC)
    WHERE execution_role = 'waiting_interleave';
