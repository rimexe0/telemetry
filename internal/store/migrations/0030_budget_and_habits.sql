-- Distraction time is a budget the owner earns with their own habits, and a
-- door opens distractions outside it at a cost (owner decision 2026-11-26,
-- budget-and-habits.md). Everything here is history: rows are added, a door
-- is cancelled or a habit archived by setting a time, and nothing is
-- deleted.

-- The owner's habits, made in the panel or with their yes in a conversation.
-- weekdays are 0 (Sunday) to 6; empty means every day.
CREATE TABLE IF NOT EXISTS habits (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    position INTEGER NOT NULL DEFAULT 0,
    weekdays SMALLINT[] NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    archived_at TIMESTAMPTZ,
    CONSTRAINT habits_name_check CHECK (char_length(name) BETWEEN 1 AND 100)
);

-- Every tick and untick, for the budget day it was for.
CREATE TABLE IF NOT EXISTS habit_events (
    id BIGSERIAL PRIMARY KEY,
    habit_id BIGINT NOT NULL REFERENCES habits (id),
    day DATE NOT NULL,
    action TEXT NOT NULL,
    at TIMESTAMPTZ NOT NULL DEFAULT now(),
    source TEXT NOT NULL DEFAULT '',
    CONSTRAINT habit_events_action_check CHECK (action IN ('tick', 'untick'))
);

CREATE INDEX IF NOT EXISTS habit_events_day_index ON habit_events (day, at);

-- Doors: "Still want it" after the door screen. The allowance is kept as it
-- was when the door was made, so a later change of settings does not move an
-- open door. A door is the agreed way out and never opens a loophole case.
CREATE TABLE IF NOT EXISTS budget_doors (
    id BIGSERIAL PRIMARY KEY,
    requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    opens_at TIMESTAMPTZ NOT NULL,
    window_end TIMESTAMPTZ NOT NULL,
    in_distraction_time BOOLEAN NOT NULL DEFAULT false,
    borrowing BOOLEAN NOT NULL DEFAULT false,
    use_minutes INTEGER NOT NULL DEFAULT 0,
    grace_minutes INTEGER NOT NULL DEFAULT 0,
    cancelled_at TIMESTAMPTZ,
    source TEXT NOT NULL DEFAULT '',
    CONSTRAINT budget_doors_window_check CHECK (window_end > opens_at AND opens_at >= requested_at)
);

CREATE INDEX IF NOT EXISTS budget_doors_window_end_index ON budget_doors (window_end);

-- A door screen left with Back: the owner returned.
CREATE TABLE IF NOT EXISTS door_backs (
    id BIGSERIAL PRIMARY KEY,
    at TIMESTAMPTZ NOT NULL DEFAULT now(),
    source TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS door_backs_at_index ON door_backs (at);

-- Grayscale kept off while it is wanted (decline), or turned back on
-- (restore), on any device.
CREATE TABLE IF NOT EXISTS grayscale_events (
    id BIGSERIAL PRIMARY KEY,
    at TIMESTAMPTZ NOT NULL DEFAULT now(),
    action TEXT NOT NULL,
    platform TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT '',
    CONSTRAINT grayscale_events_action_check CHECK (action IN ('decline', 'restore')),
    CONSTRAINT grayscale_events_platform_check CHECK (platform IN ('android', 'mac', 'linux'))
);

CREATE INDEX IF NOT EXISTS grayscale_events_at_index ON grayscale_events (at);

-- The owner's reasons, shown on the door screen. Each save is a new row; the
-- newest is in force.
CREATE TABLE IF NOT EXISTS owner_reasons (
    id BIGSERIAL PRIMARY KEY,
    reasons JSONB NOT NULL DEFAULT '[]'::jsonb,
    saved_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- A budget day as it stood when it ended, written once.
CREATE TABLE IF NOT EXISTS budget_days (
    id BIGSERIAL PRIMARY KEY,
    day DATE NOT NULL UNIQUE,
    budget DOUBLE PRECISION NOT NULL,
    spent DOUBLE PRECISION NOT NULL,
    earned DOUBLE PRECISION NOT NULL,
    penalty DOUBLE PRECISION NOT NULL,
    debt DOUBLE PRECISION NOT NULL,
    debt_passed DOUBLE PRECISION NOT NULL,
    use_minutes DOUBLE PRECISION NOT NULL,
    habits_done BIGINT[] NOT NULL DEFAULT '{}',
    habits_missed BIGINT[] NOT NULL DEFAULT '{}',
    settings JSONB NOT NULL,
    closed_at TIMESTAMPTZ NOT NULL,
    written_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Habit changes are logged with what they replaced and can be reverted, like
-- the owner's other records.
ALTER TABLE agent_changes DROP CONSTRAINT IF EXISTS agent_changes_kind_check;
ALTER TABLE agent_changes ADD CONSTRAINT agent_changes_kind_check
    CHECK (kind IN ('blocklist', 'labels', 'tasks', 'plan', 'follow_ups', 'rules', 'schedule', 'opening', 'habits'));
