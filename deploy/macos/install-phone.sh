#!/bin/zsh
# Sets a phone up over USB from this Mac, using the addresses of this Mac's
# Telemetry: installs the newest signed app if it is missing, gives it its
# addresses and every permission adb can grant. All that is left is approving
# the phone in the panel. Obtainium keeps the app current afterwards.
#
#   curl -fsSL https://github.com/rimexe0/telemetry/releases/latest/download/install-phone.sh | zsh
set -euo pipefail

repo="${TELEMETRY_REPO:-rimexe0/telemetry}"
root="$HOME/Library/Application Support/Telemetry"
package=cyou.rime.telemetry.collector
job="$HOME/Library/LaunchAgents/com.rime.telemetry.desktop.plist"
adb=$(command -v adb || print "$HOME/Library/Android/sdk/platform-tools/adb")
[[ -x "$adb" ]] || { print "adb was not found; install Android platform-tools" >&2; exit 1; }

server="${TELEMETRY_SERVER:-}"
relay="${TELEMETRY_RELAY:-}"
relay_key="${TELEMETRY_RELAY_KEY:-}"
if [[ -z "$server" && -f "$root/device.json" ]]; then
  server=$(jq -r '.server_url // .base_url // empty' "$root/device.json" 2>/dev/null || true)
fi
if [[ -z "$relay" && -f "$job" ]]; then
  arguments=$(plutil -extract ProgramArguments raw -o - "$job" 2>/dev/null || print 0)
  for (( i = 0; i < arguments; i++ )); do
    value=$(plutil -extract "ProgramArguments.$i" raw -o - "$job")
    case "$value" in
      -relay) relay=$(plutil -extract "ProgramArguments.$((i + 1))" raw -o - "$job") ;;
      -relay-key) relay_key=$(plutil -extract "ProgramArguments.$((i + 1))" raw -o - "$job") ;;
    esac
  done
fi
[[ -n "$server" ]] || read -r "server?Telemetry server address (https://…:8443): " </dev/tty

print "Waiting for the phone: connect it by USB, turn on USB debugging and allow this Mac."
"$adb" wait-for-usb-device
shell() { "$adb" -d shell "$@"; }

if ! shell pm path "$package" 2>/dev/null | grep -q package:; then
  print "Installing the newest signed app"
  work=$(mktemp -d)
  tag=$(curl -fsSL "https://github.com/$repo/releases/latest/download/release.txt" | awk '{print $1}')
  for name in SHA256SUMS SHA256SUMS.sig telemetry-phone.apk; do
    curl -fsSL -o "$work/$name" "https://github.com/$repo/releases/download/$tag/$name"
  done
  ssh-keygen -Y verify -f "$root/release-signers" -I telemetry-release -n telemetry-release -s "$work/SHA256SUMS.sig" < "$work/SHA256SUMS" >/dev/null
  (cd "$work" && grep ' telemetry-phone.apk$' SHA256SUMS | shasum -a 256 -c --quiet -)
  "$adb" -d install "$work/telemetry-phone.apk"
fi

print "Granting permissions"
for permission in ACCESS_FINE_LOCATION ACCESS_COARSE_LOCATION ACCESS_BACKGROUND_LOCATION POST_NOTIFICATIONS READ_CALENDAR READ_SMS WRITE_SECURE_SETTINGS \
    health.READ_STEPS health.READ_HEART_RATE health.READ_SLEEP health.READ_EXERCISE health.READ_HEALTH_DATA_IN_BACKGROUND; do
  shell pm grant "$package" "android.permission.$permission" 2>/dev/null || print "  not granted: $permission"
done
shell appops set "$package" GET_USAGE_STATS allow
shell cmd notification allow_listener "$package/.TelemetryNotificationListenerService"
service="$package/.TelemetryAccessibilityService"
current=$(shell settings get secure enabled_accessibility_services | tr -d '\r')
case "$current" in
  *"$service"*) ;;
  null|"") shell settings put secure enabled_accessibility_services "$service" ;;
  *) shell settings put secure enabled_accessibility_services "$current:$service" ;;
esac
shell settings put secure accessibility_enabled 1
shell dumpsys deviceidle whitelist "+$package" >/dev/null

print "Giving the app its addresses"
encode() { python3 -I -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1], safe=""))' "$1"; }
link="telemetry://setup?server=$(encode "$server")"
[[ -z "$relay" ]] || link="$link&relay=$(encode "$relay")&key=$(encode "$relay_key")"
shell am start -a android.intent.action.VIEW -d "'$link'" "$package" >/dev/null

print "
On the phone, tap Connect, then click Connect on the banner in the Telemetry panel.
Then add https://github.com/$repo to Obtainium for updates."
