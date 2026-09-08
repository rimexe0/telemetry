#!/bin/zsh
# Runs one low-priority ActivityWatch collection pass. Launchd schedules it;
# the collector exits after this bounded pass and relies on its SQLite queue for
# retries after Fedora or network failures.
set -euo pipefail

collector_root="$HOME/Library/Application Support/Telemetry"
if [[ -f "$collector_root/device.json" ]]; then
  delivery=(-config "$collector_root/device.json")
else
  delivery=(-server https://server.example.ts.net:8443 -ingest-token-file "$collector_root/activitywatch-codex.ingest-token")
fi
exec "$collector_root/bin/telemetry-activitywatch" sync \
  -url http://127.0.0.1:5600 \
  -queue "$collector_root/activitywatch-codex.queue.sqlite3" \
  -device mac-00000001 \
  -bucket aw-watcher-codex_derived_plagiarism.local \
  -overlap 15m \
  -limit 1000 \
  "${delivery[@]}" \
  -drain-all \
  -max-batches 500
