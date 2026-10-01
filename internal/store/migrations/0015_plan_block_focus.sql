-- A focus block closes distractions without being tied to a task (owner
-- decision 2026-10-01: "block now" works without a task).

ALTER TABLE plan_blocks ADD COLUMN IF NOT EXISTS focus BOOLEAN NOT NULL DEFAULT false;
