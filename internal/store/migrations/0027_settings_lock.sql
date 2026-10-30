-- A PIN someone else holds is the only way past the settings lock (owner
-- request 2026-10-30). Only a salted hash is kept; every try is recorded so
-- wrong ones can be limited, and each unlock is kept with its end.
CREATE TABLE IF NOT EXISTS settings_pin (
    id BIGSERIAL PRIMARY KEY,
    salt TEXT NOT NULL,
    pin_hash TEXT NOT NULL,
    set_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    replaced_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS settings_pin_tries (
    id BIGSERIAL PRIMARY KEY,
    tried_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ok BOOLEAN NOT NULL
);

CREATE INDEX IF NOT EXISTS settings_pin_tries_tried_at_index ON settings_pin_tries (tried_at);

CREATE TABLE IF NOT EXISTS settings_unlocks (
    id BIGSERIAL PRIMARY KEY,
    unlocked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    until TIMESTAMPTZ NOT NULL
);
