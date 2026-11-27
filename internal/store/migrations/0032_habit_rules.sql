-- A habit may have a rule that ticks it by itself once the records prove it
-- (owner request 2026-11-26): for now a place to reach within a morning
-- window. NULL is no rule. It changes with the habit, so it is logged with
-- what it replaced and can be reverted like the habit's other fields. A tick
-- a rule made says so in habit_events.source ("rule:server" or
-- "rule:device:<id>").
ALTER TABLE habits ADD COLUMN IF NOT EXISTS rule JSONB;
