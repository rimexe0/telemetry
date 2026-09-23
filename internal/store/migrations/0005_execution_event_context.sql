-- Issue #2 keeps owner/context explanations in the #1 execution event stream.

ALTER TABLE execution_events
    ADD COLUMN IF NOT EXISTS metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS provenance JSONB NOT NULL DEFAULT '{"recorded_by":"owner","source":"execution_api"}'::jsonb;

ALTER TABLE execution_events
    DROP CONSTRAINT IF EXISTS execution_events_metadata_object_check;

ALTER TABLE execution_events
    ADD CONSTRAINT execution_events_metadata_object_check
    CHECK (jsonb_typeof(metadata) = 'object');

ALTER TABLE execution_events
    DROP CONSTRAINT IF EXISTS execution_events_provenance_object_check;

ALTER TABLE execution_events
    ADD CONSTRAINT execution_events_provenance_object_check
    CHECK (jsonb_typeof(provenance) = 'object');

ALTER TABLE execution_events
    DROP CONSTRAINT IF EXISTS execution_events_type_check;

ALTER TABLE execution_events
    ADD CONSTRAINT execution_events_type_check
    CHECK (event_type IN (
        'started',
        'paused',
        'waiting_external',
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
