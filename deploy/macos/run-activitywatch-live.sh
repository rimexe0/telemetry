#!/bin/zsh
# Runs one bounded pass for the selected live ActivityWatch streams. Launchd
# invokes it every five minutes; each bucket keeps an independent SQLite queue
# and watermark so a failure cannot skip a different source.
set -euo pipefail

collector_root="$HOME/Library/Application Support/Telemetry"
collector="$collector_root/bin/telemetry-activitywatch"
common=(
  -url http://127.0.0.1:5600
  -device mac-00000001
  -overlap 15m
  -limit 1000
  -server http://100.64.0.2:18080
  -ingest-token-file "$collector_root/activitywatch-codex.ingest-token"
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
