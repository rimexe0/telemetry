#!/bin/zsh
# Builds Telemetry Desktop.app, signs it with the local signing identity and
# restarts the launchd job. macOS grants Accessibility to the app bundle; the
# stable signature keeps that permission across updates.
set -euo pipefail

repo="${0:A:h:h:h}"
root="$HOME/Library/Application Support/Telemetry"
signing="$root/signing"
keychain="$signing/telemetry-signing.keychain-db"
app="$HOME/Applications/Telemetry Desktop.app"
label="com.rime.telemetry.desktop"

cd "$repo"
staging="$(mktemp -d)/Telemetry Desktop.app"
mkdir -p "$staging/Contents/MacOS"
cp deploy/macos/TelemetryDesktop/Info.plist "$staging/Contents/Info.plist"
go build -trimpath -o "$staging/Contents/MacOS/telemetry-desktop" ./cmd/telemetry-desktop
security unlock-keychain -p "$(cat "$signing/keychain-password")" "$keychain"
identity=$(security find-identity -p codesigning "$keychain" | awk '/Telemetry Desktop Signing/ {print $2; exit}')
codesign -f --keychain "$keychain" -s "$identity" "$staging"
mkdir -p "$HOME/Applications"
rm -rf "$app"
mv "$staging" "$app"
rmdir "${staging:h}"

plist="$HOME/Library/LaunchAgents/$label.plist"
if ! cmp -s deploy/macos/$label.plist "$plist"; then
  cp deploy/macos/$label.plist "$plist"
  launchctl bootout "gui/$(id -u)/$label" 2>/dev/null || true
  # bootout returns before the job is gone.
  while launchctl print "gui/$(id -u)/$label" >/dev/null 2>&1; do sleep 0.5; done
  launchctl bootstrap "gui/$(id -u)" "$plist"
else
  launchctl kickstart -k "gui/$(id -u)/$label"
fi
echo "installed $(codesign -dr - "$app" 2>&1 | tail -1)"
