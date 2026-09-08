#!/usr/bin/env bash
# Installs or updates the M1 core on Fedora. It never rotates an existing .env
# or removes the PostgreSQL volume.
set -euo pipefail

script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
root_dir="$(CDPATH= cd -- "$script_dir/../.." && pwd)"
env_file="$root_dir/.env"
unit_dir="$HOME/.config/systemd/user"

require_command() {
  command -v "$1" >/dev/null || {
    echo "required command is unavailable: $1" >&2
    exit 1
  }
}

for command in docker openssl systemctl tailscale; do
  require_command "$command"
done

test -x "$root_dir/bin/telemetryd" || {
  echo "missing executable: $root_dir/bin/telemetryd" >&2
  exit 1
}

if [[ ! -f "$env_file" ]]; then
  tailscale_ip="$(tailscale ip -4 | head -n 1)"
  [[ -n "$tailscale_ip" ]] || {
    echo "no IPv4 Tailscale address is available" >&2
    exit 1
  }
  umask 077
  database_password="$(openssl rand -hex 32)"
  ingest_token="$(openssl rand -hex 32)"
  read_token="$(openssl rand -hex 32)"
  cat >"$env_file" <<EOF
POSTGRES_PASSWORD=$database_password
TELEMETRY_DATABASE_URL=postgres://telemetry:$database_password@127.0.0.1:55432/telemetry?sslmode=disable
TELEMETRY_INGEST_TOKEN=$ingest_token
TELEMETRY_READ_TOKEN=$read_token
TELEMETRY_LISTEN=$tailscale_ip:18080
EOF
  chmod 600 "$env_file"
fi

for variable in POSTGRES_PASSWORD TELEMETRY_DATABASE_URL TELEMETRY_INGEST_TOKEN TELEMETRY_READ_TOKEN TELEMETRY_LISTEN; do
  grep -q "^$variable=" "$env_file" || {
    echo "missing $variable in $env_file" >&2
    exit 1
  }
done

install -d -m 0755 "$unit_dir"
install -m 0644 "$root_dir/deploy/fedora/telemetry.service" "$unit_dir/telemetry.service"
install -m 0644 "$root_dir/deploy/fedora/telemetry-backup.service" "$unit_dir/telemetry-backup.service"
install -m 0644 "$root_dir/deploy/fedora/telemetry-backup.timer" "$unit_dir/telemetry-backup.timer"
systemctl --user daemon-reload
systemctl --user enable --now telemetry.service
systemctl --user enable --now telemetry-backup.timer
systemctl --user is-active --quiet telemetry.service
systemctl --user is-active --quiet telemetry-backup.timer
echo "Telemetry is active on $(grep '^TELEMETRY_LISTEN=' "$env_file" | cut -d= -f2)"
