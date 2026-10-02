-- The owner asked to be told when T3 Code's internal format changes and
-- Telemetry can no longer read it (2026-10-02). Such a notice is logged with
-- the other unasked messages.

ALTER TABLE outreach_log DROP CONSTRAINT IF EXISTS outreach_log_kind_check;
ALTER TABLE outreach_log ADD CONSTRAINT outreach_log_kind_check CHECK (kind IN ('morning', 'off_plan', 'collection'));
