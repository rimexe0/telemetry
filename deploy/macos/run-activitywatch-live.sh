#!/bin/zsh
# Runs one bounded pass for the selected live ActivityWatch streams. Launchd
# invokes it every five minutes; each bucket keeps an independent SQLite queue
# and watermark so a failure cannot skip a different source.
set -euo pipefail

collector_root="$HOME/Library/Application Support/Telemetry"
if [[ -f "$collector_root/device.json" ]]; then
  delivery=(-config "$collector_root/device.json")
else
  delivery=(-server https://server.example.ts.net:8443 -ingest-token-file "$collector_root/activitywatch-codex.ingest-token")
fi
collector="$collector_root/bin/telemetry-activitywatch"
common=(
  -url http://127.0.0.1:5600
  -device mac-00000001
  -overlap 15m
  -limit 1000
  "${delivery[@]}"
  -drain-all
  -max-batches 50
)

for bucket in \
  aw-watcher-window_plagiarism.local \
  aw-watcher-afk_plagiarism.local \
  aw-watcher-web-chrome_plagiarism.local; do
  exec_args=(sync "${common[@]}" -queue "$collector_root/activitywatch-$bucket.queue.sqlite3" -bucket "$bucket")
  "$collector" "${exec_args[@]}"
done
