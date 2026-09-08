CREATE TABLE IF NOT EXISTS telemetry_pairing_sessions (
 pairing_id TEXT PRIMARY KEY,
 device_id TEXT NOT NULL,
 device_name TEXT NOT NULL,
 credential_hash BYTEA NOT NULL CHECK(octet_length(credential_hash)=32),
 status TEXT NOT NULL CHECK(status IN ('pending','approved')),
 created_at TIMESTAMPTZ NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL,
 approved_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS telemetry_pairing_recent ON telemetry_pairing_sessions(created_at);
CREATE INDEX IF NOT EXISTS telemetry_pairing_pending ON telemetry_pairing_sessions(expires_at) WHERE status='pending';
CREATE INDEX IF NOT EXISTS telemetry_pairing_device ON telemetry_pairing_sessions(device_id,created_at DESC);
CREATE TABLE IF NOT EXISTS telemetry_devices (
 device_id TEXT PRIMARY KEY,
 device_name TEXT NOT NULL,
 pairing_id TEXT NOT NULL UNIQUE REFERENCES telemetry_pairing_sessions(pairing_id),
 credential_hash BYTEA NOT NULL CHECK(octet_length(credential_hash)=32),
 status TEXT NOT NULL CHECK(status IN ('active','revoked')),
 owner_id TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL,
 approved_at TIMESTAMPTZ NOT NULL,
 revoked_at TIMESTAMPTZ,
 last_seen_at TIMESTAMPTZ
);
