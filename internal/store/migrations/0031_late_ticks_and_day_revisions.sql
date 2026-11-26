-- Ticks made on the phone while the server could not be reached count for the
-- day they were made, and correct a day that already closed. at stays when
-- the tick was made; received_at is when the server got it. Rows from before
-- were received as they were made.
ALTER TABLE habit_events ADD COLUMN IF NOT EXISTS received_at TIMESTAMPTZ;
UPDATE habit_events SET received_at = at WHERE received_at IS NULL;
ALTER TABLE habit_events ALTER COLUMN received_at SET DEFAULT now();
ALTER TABLE habit_events ALTER COLUMN received_at SET NOT NULL;

-- A closed day is corrected by a new revision of its snapshot; the earlier
-- ones stay. The newest revision of a day is the one in force. cause says
-- what made a revision; the first snapshot has none.
ALTER TABLE budget_days ADD COLUMN IF NOT EXISTS revision INTEGER NOT NULL DEFAULT 1;
ALTER TABLE budget_days ADD COLUMN IF NOT EXISTS cause TEXT NOT NULL DEFAULT '';
ALTER TABLE budget_days DROP CONSTRAINT IF EXISTS budget_days_day_key;
CREATE UNIQUE INDEX IF NOT EXISTS budget_days_day_revision_index ON budget_days (day, revision);
