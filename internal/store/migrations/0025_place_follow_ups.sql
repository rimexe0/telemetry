-- Follow-ups that come due on arriving at or leaving a place instead of at a
-- time (2026-10-10). place holds where and when; the phone checks its own
-- positions against it and reports when it fired. due_at stays the earliest
-- time such a follow-up may come due.
ALTER TABLE follow_ups ADD COLUMN IF NOT EXISTS place JSONB;
ALTER TABLE follow_ups ADD COLUMN IF NOT EXISTS triggered_at TIMESTAMPTZ;
