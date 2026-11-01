#!/bin/zsh
# Installs the system watchdog: a launchd daemon that runs as root every
# minute and starts the owner's collector again when it was stopped. Run once
# with sudo after install-desktop.sh, and again after changing guard.go:
#
#   sudo deploy/macos/install-watchdog.sh
#
# Telemetry itself stays the owner's ordinary app. The watchdog runs a copy
# of the collector program that only root can change, so no program the
# owner (or anything running as them) can write ever runs as root.
set -euo pipefail

[[ $EUID -eq 0 && -n "${SUDO_UID:-}" ]] || { echo "run with sudo from the owner's account" >&2; exit 1; }
repo="${0:A:h:h:h}"
owner_home=$(dscl . -read "/Users/$SUDO_USER" NFSHomeDirectory | awk '{print $2}')
app="$owner_home/Applications/Telemetry Desktop.app/Contents/MacOS/telemetry-desktop"
job="$owner_home/Library/LaunchAgents/com.rime.telemetry.desktop.plist"
label="com.rime.telemetry.watchdog"
[[ -x "$app" && -f "$job" ]] || { echo "install the collector first (deploy/macos/install-desktop.sh)" >&2; exit 1; }

install -d -o root -g wheel -m 755 /Library/PrivilegedHelperTools "/Library/Application Support/Telemetry"
install -o root -g wheel -m 755 "$app" /Library/PrivilegedHelperTools/telemetry-watchdog
# The job file is put back from this copy if the owner's is deleted.
install -o root -g wheel -m 644 "$job" "/Library/Application Support/Telemetry/com.rime.telemetry.desktop.plist"
sed -e "s#__UID__#$SUDO_UID#" -e "s#__HOME__#$owner_home#" "$repo/deploy/macos/$label.plist" > "/Library/LaunchDaemons/$label.plist"
chown root:wheel "/Library/LaunchDaemons/$label.plist"
chmod 644 "/Library/LaunchDaemons/$label.plist"

launchctl bootout "system/$label" 2>/dev/null || true
while launchctl print "system/$label" >/dev/null 2>&1; do sleep 0.5; done
launchctl bootstrap system "/Library/LaunchDaemons/$label.plist"
sleep 2
tail -3 /Library/Logs/telemetry-watchdog.log 2>/dev/null || true
echo "watchdog installed; it checks the collector every minute"
