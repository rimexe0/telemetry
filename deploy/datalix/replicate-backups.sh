#!/usr/bin/env bash
# Copy complete PostgreSQL snapshots to a reviewed SSH destination.
#
# The destination is deliberately required. This helper never deletes a local
# snapshot or a remote object, and it refuses to replace a mismatched object.
# A manifest/dump basename is the stable operation key for retries.
set -euo pipefail

backup_root="${TELEMETRY_BACKUP_ROOT:-$HOME/telemetry-backups}"
local_dir="$backup_root/postgres"
offsite_target="${TELEMETRY_OFFSITE_SSH_TARGET:-}"
offsite_root="${TELEMETRY_OFFSITE_BACKUP_ROOT:-}"
offsite_identity_file="${TELEMETRY_OFFSITE_SSH_IDENTITY_FILE:-}"
max_snapshots_per_run="${TELEMETRY_OFFSITE_MAX_SNAPSHOTS_PER_RUN:-4}"
operation_id="$(date -u +%Y%m%dT%H%M%SZ)-$$-${RANDOM:-0}"
state_file=""
state_header=""

fail() {
  echo "offsite backup replication: $*" >&2
  exit 1
}

record_failure() {
  invalid_count=$((invalid_count + 1))
  echo "offsite backup replication: $*" >&2
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "required command is unavailable: $1"
}

[[ -n "$offsite_target" ]] || fail "set TELEMETRY_OFFSITE_SSH_TARGET to the reviewed SSH destination"
[[ -n "$offsite_root" ]] || fail "set TELEMETRY_OFFSITE_BACKUP_ROOT to the reviewed absolute destination path"
[[ "$offsite_target" != -* ]] || fail "SSH destination must not begin with '-'"
case "$offsite_root" in
  /*) ;;
  *) fail "TELEMETRY_OFFSITE_BACKUP_ROOT must be an absolute path" ;;
esac
case "$offsite_root" in
  /|*/|*//*|*/./*|*/.|*/../*|*/..|../*) fail "TELEMETRY_OFFSITE_BACKUP_ROOT contains an equivalent or unsafe path" ;;
esac
case "$offsite_root" in
  *[!A-Za-z0-9._/-]*) fail "TELEMETRY_OFFSITE_BACKUP_ROOT contains unsupported characters" ;;
esac
case "$max_snapshots_per_run" in
  ""|*[!0-9]*) fail "TELEMETRY_OFFSITE_MAX_SNAPSHOTS_PER_RUN must be a positive integer" ;;
esac
[ "$max_snapshots_per_run" -gt 0 ] || fail "TELEMETRY_OFFSITE_MAX_SNAPSHOTS_PER_RUN must be a positive integer"
for command in date flock python3 realpath scp sha256sum ssh stat; do
  require_command "$command"
done
if [ -n "$offsite_identity_file" ]; then
  case "$offsite_identity_file" in
    /*) ;;
    *) fail "TELEMETRY_OFFSITE_SSH_IDENTITY_FILE must be an absolute path" ;;
  esac
  case "$offsite_identity_file" in
    *[!A-Za-z0-9._/-]*|*/../*|*/..|../*) fail "TELEMETRY_OFFSITE_SSH_IDENTITY_FILE contains an unsafe path" ;;
  esac
  [ -f "$offsite_identity_file" ] || fail "SSH identity file does not exist: $offsite_identity_file"
  [ ! -L "$offsite_identity_file" ] || fail "SSH identity file must not be a symlink"
  [ "$(stat -c %a "$offsite_identity_file")" = 600 ] || fail "SSH identity file must have mode 600"
fi
[[ -d "$local_dir" ]] || fail "local backup directory does not exist: $local_dir"

ssh_options=(-o BatchMode=yes -o ConnectTimeout=30)
scp_options=(-p -o BatchMode=yes -o ConnectTimeout=30)
if [ -n "$offsite_identity_file" ]; then
  ssh_options+=( -i "$offsite_identity_file" )
  scp_options+=( -i "$offsite_identity_file" )
fi

# Resolve the effective non-secret SSH routing into the progress namespace.
# This keeps an old journal from authorizing a different alias, host, account,
# or identity. The command is configuration-only and never contacts the host.
effective_ssh_config=$(ssh "${ssh_options[@]}" -G "$offsite_target" 2>/dev/null | awk '$1 ~ /^(user|hostname|port|identityfile|stricthostkeychecking|userknownhostsfile|globalknownhostsfile)$/ {printf "%s=%s;", $1, $2}' | sed 's/;$//') || fail "could not resolve effective SSH configuration"
[ -n "$effective_ssh_config" ] || fail "effective SSH configuration is empty"
state_binding="$offsite_target|$offsite_root|$offsite_identity_file|$effective_ssh_config"
state_namespace=$(printf '%s' "$state_binding" | sha256sum | awk '{print $1}')
state_file="$backup_root/.offsite-replication-state.$state_namespace.tsv"
state_header="# telemetry-offsite-state-v1|$state_binding"

umask 077
exec 9>"$backup_root/.offsite-replicate.lock"
flock -n 9 || fail "another offsite replication is running"
if [ -L "$state_file" ] || { [ -e "$state_file" ] && [ ! -f "$state_file" ]; }; then
  fail "offsite replication state path is not a regular file: $state_file"
fi
if [ ! -e "$state_file" ]; then
  printf '%s\n' "$state_header" >"$state_file"
fi
[ "$(stat -c %a "$state_file")" = 600 ] || fail "offsite replication state file must have mode 600"

state_matches() {
  local base="$1"
  local dump_bytes="$2"
  local dump_sha="$3"
  local manifest_bytes="$4"
  local manifest_sha="$5"
  IFS= read -r first_line <"$state_file" || return 1
  [ "$first_line" = "$state_header" ] || return 1
  awk -F '\t' -v expected_base="$base" -v expected_dump_bytes="$dump_bytes" \
    -v expected_dump_sha="$dump_sha" -v expected_manifest_bytes="$manifest_bytes" \
    -v expected_manifest_sha="$manifest_sha" \
    'NF == 6 && $1 == expected_base && $2 == expected_dump_bytes && $3 == expected_dump_sha && $4 == expected_manifest_bytes && $5 == expected_manifest_sha && $6 != "" { found = 1 } END { exit(found ? 0 : 1) }' \
    "$state_file"
}

record_state() {
  local base="$1"
  local dump_bytes="$2"
  local dump_sha="$3"
  local manifest_bytes="$4"
  local manifest_sha="$5"
  state_matches "$base" "$dump_bytes" "$dump_sha" "$manifest_bytes" "$manifest_sha" && return 0
  printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$base" "$dump_bytes" "$dump_sha" "$manifest_bytes" "$manifest_sha" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >>"$state_file"
}

# The manifest is produced by backup-postgres.sh only after the dump has been
# completed and renamed. Parse its exact schema before any network operation.
manifest_fields() {
  python3 - "$1" <<'PY'
import datetime
import json
import re
import sys

def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate manifest key: {}".format(key))
        result[key] = value
    return result

try:
    with open(sys.argv[1], "r", encoding="utf-8") as stream:
        value = json.load(stream, object_pairs_hook=unique_object)
except (OSError, ValueError, json.JSONDecodeError) as error:
    raise SystemExit("invalid JSON: {}".format(error))

expected_keys = {"created_at", "database", "format", "bytes", "sha256"}
if not isinstance(value, dict) or set(value) != expected_keys:
    raise SystemExit("manifest keys must be exactly created_at,database,format,bytes,sha256")
if value["database"] != "telemetry":
    raise SystemExit("manifest database must be telemetry")
if value["format"] != "pg_dump_custom":
    raise SystemExit("manifest format is not pg_dump_custom")
created_at = value["created_at"]
if not isinstance(created_at, str) or re.fullmatch(r"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z", created_at) is None:
    raise SystemExit("manifest created_at must be an RFC3339 UTC second")
try:
    datetime.datetime.strptime(created_at, "%Y-%m-%dT%H:%M:%SZ")
except ValueError:
    raise SystemExit("manifest created_at is not a valid UTC timestamp")
size = value["bytes"]
digest = value["sha256"]
if isinstance(size, bool) or not isinstance(size, int) or size < 0:
    raise SystemExit("manifest bytes must be a non-negative integer")
if not isinstance(digest, str) or re.fullmatch(r"[0-9a-f]{64}", digest) is None:
    raise SystemExit("manifest sha256 must be a lowercase SHA-256 digest")
print("{} {}".format(size, digest))
PY
}

verify_local_dump() {
  local dump="$1"
  local expected_bytes="$2"
  local expected_sha="$3"
  local actual_bytes
  local actual_sha
  local actual_mode
  actual_bytes=$(stat -c %s "$dump")
  actual_sha=$(sha256sum "$dump" | awk '{print $1}')
  actual_mode=$(stat -c %a "$dump")
  [ "$actual_bytes" = "$expected_bytes" ] || return 1
  [ "$actual_sha" = "$expected_sha" ] || return 1
  [ "$actual_mode" = 600 ] || return 1
}

# Reject existing symlink ancestors and create only the final destination
# directory. The remote root is a constrained path, so positional shell args
# remain unambiguous without evaluating operator supplied shell syntax.
remote_init() {
  ssh "${ssh_options[@]}" "$offsite_target" "sh -s -- $offsite_root" <<'REMOTE'
set -eu
root=$1
root_without_slash=${root#/}
current=/
old_ifs=$IFS
IFS=/
for segment in $root_without_slash; do
  [ -n "$segment" ] || continue
  current="$current$segment"
  if [ -L "$current" ]; then
    echo "remote backup root contains a symlink ancestor" >&2
    exit 2
  fi
  if [ -e "$current" ] && [ ! -d "$current" ]; then
    echo "remote backup root contains a non-directory ancestor" >&2
    exit 2
  fi
  current="$current/"
done
IFS=$old_ifs

directory=$root/postgres
if [ -L "$directory" ]; then
  echo "remote PostgreSQL backup path must not be a symlink" >&2
  exit 2
fi
if [ -e "$directory" ] && [ ! -d "$directory" ]; then
  echo "remote PostgreSQL backup path is not a directory" >&2
  exit 2
fi
if [ ! -e "$directory" ]; then
  install -d -m 700 "$directory"
fi
[ "$(realpath -- "$directory")" = "$directory" ] || {
  echo "remote PostgreSQL backup path changed or contains a symlink" >&2
  exit 3
}
[ "$(stat -c %a "$directory")" = 700 ] || {
  echo "remote PostgreSQL backup path must have mode 700" >&2
  exit 3
}
REMOTE
}

remote_probe() {
  local base="$1"
  local expected_dump_bytes="$2"
  local expected_dump_sha="$3"
  local expected_manifest_sha="$4"
  local expected_manifest_bytes="$5"
  ssh "${ssh_options[@]}" "$offsite_target" "sh -s -- $offsite_root $base $expected_dump_bytes $expected_dump_sha $expected_manifest_sha $expected_manifest_bytes" <<'REMOTE'
set -eu
root=$1
base=$2
expected_dump_bytes=$3
expected_dump_sha=$4
expected_manifest_sha=$5
expected_manifest_bytes=$6
directory=$root/postgres
[ "$(realpath -- "$directory")" = "$directory" ] || exit 2
[ "$(stat -c %a "$directory")" = 700 ] || exit 2

file_state() {
  path=$1
  expected_size=$2
  expected_sha=$3
  if [ -L "$path" ]; then
    printf 'conflict'
  elif [ -e "$path" ] && [ ! -f "$path" ]; then
    printf 'conflict'
  elif [ -f "$path" ]; then
    canonical=$(realpath -- "$path" 2>/dev/null || true)
    [ "$canonical" = "$path" ] || {
      printf 'conflict'
      return
    }
    actual_size=$(stat -c %s "$path")
    actual_sha=$(sha256sum "$path" | awk '{print $1}')
    actual_mode=$(stat -c %a "$path")
    if [ "$actual_size" = "$expected_size" ] && [ "$actual_sha" = "$expected_sha" ] && [ "$actual_mode" = 600 ]; then
      printf 'yes'
    else
      printf 'mismatch'
    fi
  else
    printf 'no'
  fi
}

file_state "$directory/$base.dump" "$expected_dump_bytes" "$expected_dump_sha"
printf ' '
file_state "$directory/$base.manifest.json" "$expected_manifest_bytes" "$expected_manifest_sha"
printf '\n'
REMOTE
}

# Return a matching leftover temp file, if a prior SCP completed but the
# process timed out before promotion. Mismatched leftovers are ignored and
# retained; the next attempt gets a new operation-specific temp name.
remote_find_matching_temp() {
  local base="$1"
  local kind="$2"
  local expected_bytes="$3"
  local expected_sha="$4"
  local prefix
  if [ "$kind" = dump ]; then
    prefix=".$base.dump.partial.$expected_sha."
  else
    prefix=".$base.manifest.json.partial.$expected_sha."
  fi
  ssh "${ssh_options[@]}" "$offsite_target" "sh -s -- $offsite_root $prefix $expected_bytes $expected_sha" <<'REMOTE'
set -eu
root=$1
prefix=$2
expected_bytes=$3
expected_sha=$4
directory=$root/postgres
[ "$(realpath -- "$directory")" = "$directory" ] || exit 2
[ "$(stat -c %a "$directory")" = 700 ] || exit 2
for path in "$directory"/"$prefix"*; do
  [ -L "$path" ] && continue
  [ -f "$path" ] || continue
  name=${path##*/}
  case "$name" in
    "$prefix"*) ;;
    *) continue ;;
  esac
  case "$name" in
    *[!A-Za-z0-9._-]*) continue ;;
  esac
  suffix=${name#"$prefix"}
  [ -n "$suffix" ] || continue
  canonical=$(realpath -- "$path" 2>/dev/null || true)
  [ "$canonical" = "$path" ] || continue
  actual_size=$(stat -c %s "$path")
  actual_sha=$(sha256sum "$path" | awk '{print $1}')
  actual_mode=$(stat -c %a "$path")
  if [ "$actual_size" = "$expected_bytes" ] && [ "$actual_sha" = "$expected_sha" ] && [ "$actual_mode" = 600 ]; then
    printf '%s\n' "$name"
    exit 0
  fi
done
printf 'none\n'
REMOTE
}

remote_stage_probe() {
  local temp_name="$1"
  local expected_bytes="$2"
  local expected_sha="$3"
  ssh "${ssh_options[@]}" "$offsite_target" "sh -s -- $offsite_root $temp_name $expected_bytes $expected_sha" <<'REMOTE'
set -eu
root=$1
temp_name=$2
expected_bytes=$3
expected_sha=$4
directory=$root/postgres
[ "$(realpath -- "$directory")" = "$directory" ] || exit 2
[ "$(stat -c %a "$directory")" = 700 ] || exit 2
case "$temp_name" in *[!A-Za-z0-9._-]*) printf 'conflict\n'; exit 0 ;; esac
path=$directory/$temp_name
if [ -L "$path" ]; then
  printf 'conflict\n'
elif [ -e "$path" ] && [ ! -f "$path" ]; then
  printf 'conflict\n'
elif [ -f "$path" ]; then
  canonical=$(realpath -- "$path" 2>/dev/null || true)
  if [ "$canonical" != "$path" ]; then
    printf 'conflict\n'
    exit 0
  fi
  actual_size=$(stat -c %s "$path")
  actual_sha=$(sha256sum "$path" | awk '{print $1}')
  actual_mode=$(stat -c %a "$path")
  if [ "$actual_size" = "$expected_bytes" ] && [ "$actual_sha" = "$expected_sha" ] && [ "$actual_mode" = 600 ]; then
    printf 'yes\n'
  else
    printf 'mismatch\n'
  fi
else
  printf 'no\n'
fi
REMOTE
}

remote_promote() {
  local base="$1"
  local expected_dump_bytes="$2"
  local expected_dump_sha="$3"
  local kind="$4"
  local expected_manifest_bytes="$5"
  local expected_manifest_sha="$6"
  local temp_name="$7"
  # Pass every remote argument explicitly, including manifest size and digest.
  ssh "${ssh_options[@]}" "$offsite_target" "sh -s -- $offsite_root $base $expected_dump_bytes $expected_dump_sha $kind $expected_manifest_bytes $expected_manifest_sha $temp_name" <<'REMOTE'
set -eu
root=$1
base=$2
expected_dump_bytes=$3
expected_dump_sha=$4
kind=$5
expected_manifest_bytes=$6
expected_manifest_sha=$7
temp_name=$8
directory=$root/postgres
[ "$(realpath -- "$directory")" = "$directory" ] || exit 2
[ "$(stat -c %a "$directory")" = 700 ] || exit 2
case "$temp_name" in *[!A-Za-z0-9._-]*) exit 2 ;; esac

if [ "$kind" = dump ]; then
  final=$directory/$base.dump
  expected_size=$expected_dump_bytes
  expected_sha=$expected_dump_sha
  case "$temp_name" in
    .$base.dump.partial.$expected_dump_sha.*) ;;
    *) exit 2 ;;
  esac
else
  final=$directory/$base.manifest.json
  expected_size=$expected_manifest_bytes
  expected_sha=$expected_manifest_sha
  case "$temp_name" in
    .$base.manifest.json.partial.$expected_manifest_sha.*) ;;
    *) exit 2 ;;
  esac
fi
temporary=$directory/$temp_name

if [ -L "$final" ] || [ -L "$temporary" ]; then
  exit 2
fi

verify_file() {
  path=$1
  [ -L "$path" ] && return 1
  [ -f "$path" ] || return 1
  [ "$(realpath -- "$path" 2>/dev/null || true)" = "$path" ] || return 1
  [ "$(stat -c %s "$path")" = "$expected_size" ] || return 1
  [ "$(sha256sum "$path" | awk '{print $1}')" = "$expected_sha" ] || return 1
  [ "$(stat -c %a "$path")" = 600 ] || return 1
}

if [ -e "$final" ]; then
  verify_file "$final" || exit 3
  exit 0
fi
verify_file "$temporary" || exit 4

# ln is an atomic no-clobber create on the same filesystem. If another worker
# wins the race, verify its final object instead of overwriting it.
if ln "$temporary" "$final" 2>/dev/null; then
  rm "$temporary"
  exit 0
fi
if [ -e "$final" ]; then
  verify_file "$final" || exit 5
  exit 0
fi
exit 6
REMOTE
}

copy_file() {
  local local_file="$1"
  local base="$2"
  local kind="$3"
  local object_bytes="$4"
  local object_sha="$5"
  local dump_bytes="$6"
  local dump_sha="$7"
  local manifest_bytes="$8"
  local manifest_sha="$9"
  local matching_temp
  local temp_name
  local temp_prefix
  local temp_suffix
  local remote_path
  local stage_state

  if [ "$kind" = dump ]; then
    temp_prefix=".$base.dump.partial.$object_sha."
  else
    temp_prefix=".$base.manifest.json.partial.$object_sha."
  fi

  matching_temp=$(remote_find_matching_temp "$base" "$kind" "$object_bytes" "$object_sha")
  if [ "$matching_temp" = none ]; then
    temp_name="$temp_prefix$operation_id"
  else
    case "$matching_temp" in
      "$temp_prefix"*) ;;
      *) fail "remote $kind temporary file has an unsafe name for $base" ;;
    esac
    case "$matching_temp" in
      *[!A-Za-z0-9._-]*) fail "remote $kind temporary file has an unsafe name for $base" ;;
    esac
    temp_suffix=${matching_temp#"$temp_prefix"}
    [ -n "$temp_suffix" ] || fail "remote $kind temporary file has an empty suffix for $base"
    temp_name=$matching_temp
  fi
  remote_path="$offsite_root/postgres/$temp_name"
  stage_state=$(remote_stage_probe "$temp_name" "$object_bytes" "$object_sha")
  case "$stage_state" in
    yes) ;;
    no)
      # A unique operation suffix means a corrupt/aborted temp never blocks a
      # later retry. The old artifact remains visible for separate cleanup.
      scp "${scp_options[@]}" "$local_file" "$offsite_target:$remote_path"
      transferred_files=$((transferred_files + 1))
      stage_state=$(remote_stage_probe "$temp_name" "$object_bytes" "$object_sha")
      [ "$stage_state" = yes ] || fail "remote $kind temporary file failed integrity verification for $base"
      ;;
    conflict|mismatch) fail "remote $kind temporary file conflicts for $base" ;;
    *) fail "unexpected remote $kind temporary state '$stage_state' for $base" ;;
  esac
  remote_promote "$base" "$dump_bytes" "$dump_sha" "$kind" "$manifest_bytes" "$manifest_sha" "$temp_name"
}

remote_init
valid_count=0
processed_count=0
verified_count=0
deferred_count=0
invalid_count=0
transferred_files=0
valid_index=0
while IFS= read -r manifest; do
  [ -n "$manifest" ] || continue
  manifest_name=${manifest##*/}
  case "$manifest_name" in
    telemetry-postgres-????????T??????Z.manifest.json) ;;
    *) record_failure "unexpected manifest name: $manifest_name"; continue ;;
  esac
  base=${manifest_name%.manifest.json}
  case "$base" in
    *[!A-Za-z0-9._TZ-]*) record_failure "unexpected manifest name: $manifest_name"; continue ;;
  esac
  dump="$local_dir/$base.dump"
  if [ ! -f "$dump" ]; then
    record_failure "manifest has no matching dump: $manifest_name"
    continue
  fi

  if ! fields=$(manifest_fields "$manifest"); then
    record_failure "invalid manifest schema: $manifest_name"
    continue
  fi
  expected_bytes=${fields%% *}
  expected_dump_sha=${fields#* }
  manifest_mode=$(stat -c %a "$manifest")
  if [ "$manifest_mode" != 600 ]; then
    record_failure "local manifest must have mode 600: $manifest_name"
    continue
  fi
  manifest_bytes=$(stat -c %s "$manifest")
  manifest_sha=$(sha256sum "$manifest" | awk '{print $1}')
  valid_count=$((valid_count + 1))
  valid_index=$((valid_index + 1))

  # The newest valid snapshot is rechecked each run. Older snapshots already
  # recorded in this local progress journal need no remote dump rehash; a
  # missing state entry becomes a bounded backlog candidate.
  needs_probe=1
  if [ "$valid_index" -ne 1 ] && state_matches "$base" "$expected_bytes" "$expected_dump_sha" "$manifest_bytes" "$manifest_sha"; then
    needs_probe=0
  fi
  if [ "$needs_probe" -eq 0 ]; then
    continue
  fi

  # Scan all names for schema errors, but cap remote probes and large hashes.
  # Newest snapshots are scanned first so a bounded run refreshes the offsite
  # recovery point while state-missing older backlog converges on later runs.
  # A completed probe does not consume the work budget, so the latest check
  # cannot starve older state-missing snapshots.
  force_recheck=0
  if [ "$valid_index" -eq 1 ]; then
    force_recheck=1
  fi
  if [ "$processed_count" -ge "$max_snapshots_per_run" ] && [ "$force_recheck" -eq 0 ]; then
    deferred_count=$((deferred_count + 1))
    continue
  fi

  states=$(remote_probe "$base" "$expected_bytes" "$expected_dump_sha" "$manifest_sha" "$manifest_bytes")
  dump_state=${states%% *}
  manifest_state=${states#* }
  if [ "$dump_state" = yes ] && [ "$manifest_state" = yes ]; then
    record_state "$base" "$expected_bytes" "$expected_dump_sha" "$manifest_bytes" "$manifest_sha"
    verified_count=$((verified_count + 1))
    echo "verified offsite snapshot $base"
    continue
  fi
  processed_count=$((processed_count + 1))
  case "$dump_state" in
    yes) ;;
    no)
      if ! verify_local_dump "$dump" "$expected_bytes" "$expected_dump_sha"; then
        record_failure "local dump integrity does not match manifest: $manifest_name"
        continue
      fi
      copy_file "$dump" "$base" dump "$expected_bytes" "$expected_dump_sha" "$expected_bytes" "$expected_dump_sha" "$manifest_bytes" "$manifest_sha"
      ;;
    conflict|mismatch) fail "remote dump conflicts for $base" ;;
    *) fail "unexpected remote dump state '$dump_state' for $base" ;;
  esac
  case "$manifest_state" in
    yes) ;;
    no) copy_file "$manifest" "$base" manifest "$manifest_bytes" "$manifest_sha" "$expected_bytes" "$expected_dump_sha" "$manifest_bytes" "$manifest_sha" ;;
    conflict|mismatch) fail "remote manifest conflicts for $base" ;;
    *) fail "unexpected remote manifest state '$manifest_state' for $base" ;;
  esac

  states=$(remote_probe "$base" "$expected_bytes" "$expected_dump_sha" "$manifest_sha" "$manifest_bytes")
  [ "$states" = "yes yes" ] || fail "remote final integrity check failed for $base: $states"
  record_state "$base" "$expected_bytes" "$expected_dump_sha" "$manifest_bytes" "$manifest_sha"
  verified_count=$((verified_count + 1))
  echo "verified offsite snapshot $base"
done < <(find "$local_dir" -maxdepth 1 -type f -name 'telemetry-postgres-*.manifest.json' -print | sort -r)

[ "$valid_count" -gt 0 ] || fail "no complete local PostgreSQL snapshots were found"
if [ "$invalid_count" -gt 0 ]; then
  fail "verified $processed_count snapshot(s), but $invalid_count local manifest/pair error(s) remain"
fi
if [ "$deferred_count" -gt 0 ]; then
  echo "deferred $deferred_count older snapshot(s) after processing $processed_count newest candidate(s)"
fi
echo "verified $verified_count PostgreSQL snapshot(s) at $offsite_target:$offsite_root/postgres; processed $processed_count pending candidate(s); transferred $transferred_files file(s) this run"
