#!/bin/zsh
# Runs one low-priority ActivityWatch collection pass. Launchd schedules it
# every minute by default. The collector exits after this bounded pass and
# relies on its SQLite queue for retries after Fedora or network failures.
set -euo pipefail

collector_root="$HOME/Library/Application Support/Telemetry"
overlap="${TELEMETRY_AW_OVERLAP:-15m}"
event_limit="${TELEMETRY_AW_EVENT_LIMIT:-1000}"
max_batches="${TELEMETRY_AW_CODEX_MAX_BATCHES:-10}"
pass_timeout="${TELEMETRY_AW_PASS_TIMEOUT:-20s}"
http_timeout="${TELEMETRY_AW_HTTP_TIMEOUT:-5s}"
collector="${TELEMETRY_AW_COLLECTOR:-$collector_root/bin/telemetry-activitywatch}"
if [[ -f "$collector_root/device.json" ]]; then
  delivery=(-config "$collector_root/device.json")
else
  delivery=(-server https://server.example.ts.net:8443 -ingest-token-file "$collector_root/activitywatch-codex.ingest-token")
fi
exec "$collector" sync \
  -url http://127.0.0.1:5600 \
  -queue "$collector_root/activitywatch-codex.queue.sqlite3" \
  -device mac-00000001 \
  -bucket aw-watcher-codex_derived_plagiarism.local \
  -overlap "$overlap" \
  -limit "$event_limit" \
  -timeout "$pass_timeout" \
  -http-timeout "$http_timeout" \
  "${delivery[@]}" \
  -drain-all \
  -max-batches "$max_batches"
