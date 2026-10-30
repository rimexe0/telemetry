-- An opening starts after the owner's wait instead of at once, and the day
-- stays shut each morning until the printed morning code is scanned (owner
-- request 2026-10-26).
ALTER TABLE openings ADD COLUMN IF NOT EXISTS starts_at TIMESTAMPTZ;
UPDATE openings SET starts_at = created_at WHERE starts_at IS NULL;
ALTER TABLE openings ALTER COLUMN starts_at SET DEFAULT now();
ALTER TABLE openings ALTER COLUMN starts_at SET NOT NULL;
-- An opening can need the owner to confirm it several times after the wait,
-- each confirmation some seconds after the last. Until it is confirmed it
-- opens nothing; once it is, it runs for length_minutes from then.
ALTER TABLE openings ADD COLUMN IF NOT EXISTS confirmations_needed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE openings ADD COLUMN IF NOT EXISTS confirmations INTEGER NOT NULL DEFAULT 0;
ALTER TABLE openings ADD COLUMN IF NOT EXISTS confirm_seconds INTEGER NOT NULL DEFAULT 0;
ALTER TABLE openings ADD COLUMN IF NOT EXISTS last_confirmed_at TIMESTAMPTZ;
ALTER TABLE openings ADD COLUMN IF NOT EXISTS length_minutes INTEGER NOT NULL DEFAULT 0;

-- Only a hash of each code is kept; the code itself is shown once, to print.
CREATE TABLE IF NOT EXISTS morning_codes (
    id BIGSERIAL PRIMARY KEY,
    code_hash TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    retired_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS morning_scans (
    id BIGSERIAL PRIMARY KEY,
    code_id BIGINT NOT NULL REFERENCES morning_codes (id),
    scanned_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS morning_scans_scanned_at_index ON morning_scans (scanned_at);
