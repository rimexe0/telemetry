#!/bin/zsh
# Runs one bounded pass for the selected live ActivityWatch streams. Launchd
# invokes it every minute by default; each bucket keeps an independent SQLite queue
# and watermark so a failure cannot skip a different source.
set -euo pipefail

collector_root="$HOME/Library/Application Support/Telemetry"
overlap="${TELEMETRY_AW_OVERLAP:-15m}"
event_limit="${TELEMETRY_AW_EVENT_LIMIT:-1000}"
max_batches="${TELEMETRY_AW_MAX_BATCHES:-10}"
pass_timeout="${TELEMETRY_AW_PASS_TIMEOUT:-20s}"
http_timeout="${TELEMETRY_AW_HTTP_TIMEOUT:-5s}"
if [[ -f "$collector_root/device.json" ]]; then
  delivery=(-config "$collector_root/device.json")
else
  delivery=(-server https://server.example.ts.net:8443 -ingest-token-file "$collector_root/activitywatch-codex.ingest-token")
fi
collector="${TELEMETRY_AW_COLLECTOR:-$collector_root/bin/telemetry-activitywatch}"
common=(
  -url http://127.0.0.1:5600
  -device mac-00000001
  -overlap "$overlap"
  -limit "$event_limit"
  -timeout "$pass_timeout"
  -http-timeout "$http_timeout"
  "${delivery[@]}"
  -drain-all
  -max-batches "$max_batches"
)

pass_status=0
for bucket in \
  aw-watcher-window_plagiarism.local \
  aw-watcher-afk_plagiarism.local \
  aw-watcher-web-chrome_plagiarism.local; do
  exec_args=(sync "${common[@]}" -queue "$collector_root/activitywatch-$bucket.queue.sqlite3" -bucket "$bucket")
  if ! "$collector" "${exec_args[@]}"; then
    # Keep collecting independent sources even when ActivityWatch or delivery
    # fails for one bucket. The aggregate status keeps the failure visible to
    # launchd and makes the next scheduled pass retry it.
    pass_status=1
  fi
done
exit "$pass_status"
