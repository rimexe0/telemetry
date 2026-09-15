#!/bin/zsh
# Runs one bounded, selected-project T3 Code capture and drains its queue.
# Launchd invokes this script periodically; all source and delivery state stays
# in the owner-only queue when either side is unavailable.
set -euo pipefail

collector_root="$HOME/Library/Application Support/Telemetry"
collector="${TELEMETRY_PROJECTS_COLLECTOR:-$collector_root/bin/telemetry-projects}"
drainer="${TELEMETRY_PROJECTS_DRAINER:-$collector_root/bin/telemetry-activitywatch}"
state_db="${TELEMETRY_PROJECTS_DB:-$HOME/.t3/userdata/state.sqlite}"
usage_cache="${TELEMETRY_PROJECTS_USAGE_CACHE:-$HOME/.t3/userdata/usage-scan-cache.json}"
project_path="${TELEMETRY_PROJECTS_PATH:-/Users/ri/code/personal/Telemetry}"
device_id="${TELEMETRY_PROJECTS_DEVICE:-mac-00000001}"
queue_path="${TELEMETRY_PROJECTS_QUEUE:-$collector_root/t3code-projects.queue.sqlite3}"
from="${TELEMETRY_PROJECTS_FROM:-2026-09-12T00:00:00Z}"
overlap="${TELEMETRY_PROJECTS_OVERLAP:-15m}"
limit="${TELEMETRY_PROJECTS_LIMIT:-1000}"
pass_timeout="${TELEMETRY_PROJECTS_TIMEOUT:-20s}"
drain_limit="${TELEMETRY_PROJECTS_DRAIN_LIMIT:-100}"
drain_max_batches="${TELEMETRY_PROJECTS_DRAIN_MAX_BATCHES:-10}"
server="${TELEMETRY_PROJECTS_SERVER:-https://server.example.ts.net:8443}"

same_file() {
  local first="$1"
  local second="$2"
  [[ "$first" == "$second" ]] && return 0
  [[ -e "$first" && -e "$second" ]] || return 1
  [[ "$first" -ef "$second" ]]
}

queue_aliases_source() {
  local source="$1"
  [[ -n "$source" ]] || return 1
  same_file "$queue_path" "$source"
}

# This preflight is intentionally independent of the collector binary. The
# collector also validates these paths before opening its queue, but a failed
# capture must never fall through to the generic drainer and open an aliased
# queue with WAL enabled.
if queue_aliases_source "$state_db" || queue_aliases_source "$usage_cache"; then
  print -u2 -- "Telemetry project queue aliases a selected source; refusing capture and drain"
  exit 1
fi

pass_status=0
if ! "$collector" sync \
  -queue "$queue_path" \
  -db "$state_db" \
  -usage-cache "$usage_cache" \
  -device "$device_id" \
  -project "$project_path" \
  -from "$from" \
  -overlap "$overlap" \
  -limit "$limit" \
  -timeout "$pass_timeout" \
  -git; then
  # A source outage must not prevent already captured records from draining.
  pass_status=1
fi

delivery=()
if [[ -f "$collector_root/device.json" ]]; then
  delivery=(-config "$collector_root/device.json")
elif [[ -f "$collector_root/activitywatch-codex.ingest-token" ]]; then
  delivery=(-server "$server" -ingest-token-file "$collector_root/activitywatch-codex.ingest-token")
else
  print -u2 -- "Telemetry project queue retained locally: no approved delivery configuration"
  exit 1
fi

if ! "$drainer" drain \
  -queue "$queue_path" \
  -device "$device_id" \
  "${delivery[@]}" \
  -limit "$drain_limit" \
  -all \
  -max-batches "$drain_max_batches"; then
  pass_status=1
fi

exit "$pass_status"
