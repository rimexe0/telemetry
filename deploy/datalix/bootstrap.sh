#!/usr/bin/env bash
# Run only for the reviewed Datalix release. Preserves existing credentials/data.
set -euo pipefail
script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
root_dir="$(CDPATH= cd -- "$script_dir/../.." && pwd)"
env_file="$root_dir/.env"
unit_dir="$HOME/.config/systemd/user"
for cmd in docker openssl systemctl tailscale; do command -v "$cmd" >/dev/null; done
test -x "$root_dir/bin/telemetryd"
test -r "$root_dir/web/build/index.html"
if [[ ! -f "$env_file" ]]; then
 : "${TELEMETRY_CLERK_PUBLISHABLE_KEY:?provide Clerk publishable key}"
 : "${TELEMETRY_CLERK_ISSUER:?provide Clerk HTTPS issuer}"
 : "${TELEMETRY_CLERK_OWNER_ID:?provide owner user ID}"
 : "${TELEMETRY_PUBLIC_URL:?provide private HTTPS origin}"
 umask 077
 database_password="$(openssl rand -hex 32)"
 ingest_token="$(openssl rand -hex 32)"
 read_token="$(openssl rand -hex 32)"
 cat > "$env_file" <<ENV
POSTGRES_PASSWORD=$database_password
TELEMETRY_DATABASE_URL=postgres://telemetry:$database_password@127.0.0.1:55432/telemetry?sslmode=disable
TELEMETRY_INGEST_TOKEN=$ingest_token
TELEMETRY_READ_TOKEN=$read_token
TELEMETRY_LISTEN=127.0.0.1:18080
TELEMETRY_PANEL_DIR=$root_dir/web/build
TELEMETRY_CLERK_PUBLISHABLE_KEY=$TELEMETRY_CLERK_PUBLISHABLE_KEY
TELEMETRY_CLERK_ISSUER=$TELEMETRY_CLERK_ISSUER
TELEMETRY_CLERK_OWNER_ID=$TELEMETRY_CLERK_OWNER_ID
TELEMETRY_PUBLIC_URL=$TELEMETRY_PUBLIC_URL
ENV
 chmod 600 "$env_file"
fi
install -d -m 0755 "$unit_dir"
for unit in telemetry.service telemetry-backup.service telemetry-backup.timer telemetry-backup-offsite.service telemetry-backup-offsite.timer; do
  install -m 0644 "$script_dir/$unit" "$unit_dir/$unit"
done
systemctl --user daemon-reload
systemctl --user enable telemetry.service telemetry-backup.timer
systemctl --user restart telemetry.service
systemctl --user start telemetry-backup.timer
systemctl --user is-active --quiet telemetry.service
# Only this previously reviewed listener; never reset existing Serve configuration.
tailscale serve --bg --https=8443 http://127.0.0.1:18080
