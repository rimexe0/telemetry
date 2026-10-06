#!/bin/zsh
# Connects adb to the phone without hunting for it. Wireless debugging turns
# itself off when the phone changes networks, so with root the phone's adb is
# kept listening on port 5555 (set once with --setup while wireless debugging
# is on); the address comes from the phone's own last report to Telemetry,
# with wireless-debugging discovery as the fallback. adb still asks the phone
# to trust this Mac's key, so the port does not let strangers in.
set -euo pipefail
port=5555
connected() { adb devices | awk -F'\t' '$2=="device"{print $1; exit}'; }

if [[ "${1:-}" == "--setup" ]]; then
  device=$(connected)
  [[ -n "$device" ]] || { echo "Turn on wireless debugging and run this again."; exit 1; }
  adb -s "$device" shell "su -c 'setprop persist.adb.tcp.port $port; setprop service.adb.tcp.port $port; stop adbd; start adbd'" || true
  echo "adb now listens on port $port and keeps doing so after restarts."
  exit 0
fi

[[ -n "$(connected)" ]] && { connected; exit 0; }
# The phone reports its Wi-Fi address every minute.
address=$(ssh datalix "docker exec telemetry-postgres-1 psql -U telemetry -d telemetry -Atc \"select r.projection->>'wifi_ipv4' from observations o join observation_revisions r on r.id=o.effective_revision_id where o.kind='android.device_state' and r.projection ? 'wifi_ipv4' order by o.occurred_at desc limit 1\"" 2>/dev/null || true)
if [[ -n "$address" ]]; then
  timeout 8 adb connect "$address:$port" >/dev/null 2>&1 || true
fi
if [[ -z "$(connected)" ]]; then
  service=$(adb mdns services 2>/dev/null | awk '/_adb-tls-connect/{print $NF; exit}')
  [[ -n "$service" ]] && timeout 8 adb connect "$service" >/dev/null 2>&1 || true
fi
device=$(connected)
[[ -n "$device" ]] || { echo "Phone not found (last Wi-Fi address: ${address:-unknown})." >&2; exit 1; }
echo "$device"
