-- A task's rules may name apps (owner decision 2026-10-01: the plan knows
-- which apps and sites a task uses), and the assistant applies rule changes
-- as logged, revertible changes.

ALTER TABLE activity_context_rules DROP CONSTRAINT IF EXISTS activity_context_rules_signal_check;
ALTER TABLE activity_context_rules ADD CONSTRAINT activity_context_rules_signal_check
    CHECK (signal IN ('path_prefix', 'domain', 'url_prefix', 'workspace_id', 'app'));

ALTER TABLE agent_changes DROP CONSTRAINT IF EXISTS agent_changes_kind_check;
ALTER TABLE agent_changes ADD CONSTRAINT agent_changes_kind_check
    CHECK (kind IN ('blocklist', 'labels', 'tasks', 'plan', 'follow_ups', 'rules'));
