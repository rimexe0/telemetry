#!/bin/zsh
# Builds telemetry-desktop, signs it with the local signing identity and
# restarts the launchd job. A stable signature keeps the Accessibility
# permission across updates; an ad-hoc signature would lose it every build.
set -euo pipefail

repo="${0:A:h:h:h}"
root="$HOME/Library/Application Support/Telemetry"
signing="$root/signing"
keychain="$signing/telemetry-signing.keychain-db"
binary="$root/bin/telemetry-desktop"
label="com.rime.telemetry.desktop"

cd "$repo"
go build -trimpath -o "$binary.new" ./cmd/telemetry-desktop
security unlock-keychain -p "$(cat "$signing/keychain-password")" "$keychain"
identity=$(security find-identity -p codesigning "$keychain" | awk '/Telemetry Desktop Signing/ {print $2; exit}')
codesign -f --keychain "$keychain" -s "$identity" -i cyou.rime.telemetry.desktop "$binary.new"
mv "$binary.new" "$binary"

plist="$HOME/Library/LaunchAgents/$label.plist"
if ! cmp -s deploy/macos/$label.plist "$plist"; then
  cp deploy/macos/$label.plist "$plist"
  launchctl bootout "gui/$(id -u)/$label" 2>/dev/null || true
  launchctl bootstrap "gui/$(id -u)" "$plist"
else
  launchctl kickstart -k "gui/$(id -u)/$label"
fi
echo "installed $(codesign -dr - "$binary" 2>&1 | tail -1)"
