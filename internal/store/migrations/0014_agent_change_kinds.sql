-- The assistant also applies owner-approved task pool and day plan changes
-- (owner decision 2026-10-01); they are logged and revertible like labels.

ALTER TABLE agent_changes DROP CONSTRAINT IF EXISTS agent_changes_kind_check;
ALTER TABLE agent_changes ADD CONSTRAINT agent_changes_kind_check
    CHECK (kind IN ('blocklist', 'labels', 'tasks', 'plan'));
