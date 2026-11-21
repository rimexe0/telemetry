-- Setup codes: the owner makes one in the panel and the new device redeems it
-- once, so it joins without a second approval step. Only a hash of the code
-- is kept.
CREATE TABLE IF NOT EXISTS telemetry_pairing_invites (
 invite_id TEXT PRIMARY KEY,
 code_hash BYTEA NOT NULL UNIQUE CHECK(octet_length(code_hash)=32),
 created_at TIMESTAMPTZ NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL,
 used_at TIMESTAMPTZ,
 used_by_device TEXT,
 used_by_name TEXT,
 cancelled_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS telemetry_pairing_invites_open ON telemetry_pairing_invites(expires_at) WHERE used_at IS NULL AND cancelled_at IS NULL;

-- A device that pairs again (wiped, reinstalled, or removed and set up again)
-- keeps its device ID and history; each credential it replaced is kept here.
CREATE TABLE IF NOT EXISTS telemetry_device_credential_history (
 device_id TEXT NOT NULL,
 previous_pairing_id TEXT NOT NULL,
 previous_status TEXT NOT NULL,
 previous_name TEXT NOT NULL,
 replaced_by_pairing_id TEXT NOT NULL,
 replaced_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS telemetry_device_credential_history_device ON telemetry_device_credential_history(device_id, replaced_at DESC);

-- The pairing details a setup code carries besides the server address.
CREATE TABLE IF NOT EXISTS telemetry_pairing_settings (
 id INT PRIMARY KEY CHECK(id=1),
 relay_url TEXT NOT NULL DEFAULT '',
 updated_at TIMESTAMPTZ NOT NULL
);
