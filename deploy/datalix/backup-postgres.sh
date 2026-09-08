#!/usr/bin/env bash
# Creates an immutable logical PostgreSQL snapshot for R-011. It intentionally
# has no retention deletion: authoritative history must not be pruned by a job.
set -euo pipefail

script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
root_dir="$(CDPATH= cd -- "$script_dir/../.." && pwd)"
env_file="$root_dir/.env"
compose_file="$root_dir/deploy/datalix/compose.yaml"
backup_root="${TELEMETRY_BACKUP_ROOT:-$HOME/telemetry-backups}"
postgres_backup_dir="$backup_root/postgres"
timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
basename="telemetry-postgres-$timestamp"
partial_dump="$postgres_backup_dir/$basename.dump.partial"
final_dump="$postgres_backup_dir/$basename.dump"
partial_manifest="$postgres_backup_dir/$basename.manifest.json.partial"
final_manifest="$postgres_backup_dir/$basename.manifest.json"

test -r "$env_file" || {
  echo "missing protected Telemetry environment file: $env_file" >&2
  exit 1
}
command -v docker >/dev/null || {
  echo "docker is required for the PostgreSQL snapshot" >&2
  exit 1
}
command -v flock >/dev/null || {
  echo "flock is required to prevent overlapping snapshots" >&2
  exit 1
}

umask 077
install -d -m 0700 "$postgres_backup_dir"
exec 9>"$postgres_backup_dir/.backup.lock"
flock -n 9 || {
  echo "another Telemetry PostgreSQL snapshot is running" >&2
  exit 1
}

# The password is read only inside the existing PostgreSQL container. It is
# neither emitted nor placed in a command-line argument on the host.
docker compose --project-name telemetry --env-file "$env_file" --file "$compose_file" \
  exec -T postgres sh -c 'PGPASSWORD="$POSTGRES_PASSWORD" pg_dump --format=custom --no-owner --no-privileges --username="$POSTGRES_USER" "$POSTGRES_DB"' \
  >"$partial_dump"

bytes="$(stat --format=%s "$partial_dump")"
checksum="$(sha256sum "$partial_dump" | awk '{print $1}')"
printf '{"created_at":"%s","database":"telemetry","format":"pg_dump_custom","bytes":%s,"sha256":"%s"}\n' \
  "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$bytes" "$checksum" >"$partial_manifest"

mv "$partial_dump" "$final_dump"
mv "$partial_manifest" "$final_manifest"
printf 'created %s (%s bytes, sha256 %s)\n' "$final_dump" "$bytes" "$checksum"
