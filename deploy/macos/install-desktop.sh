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

# This machine's relay address and key, kept out of the repository.
[[ -f deploy/macos/local.env ]] && source deploy/macos/local.env
wanted="$(mktemp)"
sed -e "s#https://relay.example.com#${TELEMETRY_RELAY:-https://relay.example.com}#" \
  ${TELEMETRY_RELAY_KEY:+-e "s#<string>BDQA[^<]*</string>#<string>$TELEMETRY_RELAY_KEY</string>#"} \
  deploy/macos/$label.plist > "$wanted"
plist="$HOME/Library/LaunchAgents/$label.plist"
if ! cmp -s "$wanted" "$plist"; then
  cp "$wanted" "$plist"
  launchctl bootout "gui/$(id -u)/$label" 2>/dev/null || true
  # bootout returns before the job is gone.
  while launchctl print "gui/$(id -u)/$label" >/dev/null 2>&1; do sleep 0.5; done
  launchctl bootstrap "gui/$(id -u)" "$plist"
else
  launchctl kickstart -k "gui/$(id -u)/$label"
fi
# The guard starts the collector again within a minute if it is unloaded.
guard="com.rime.telemetry.guard"
guard_plist="$HOME/Library/LaunchAgents/$guard.plist"
if ! cmp -s deploy/macos/$guard.plist "$guard_plist"; then
  cp deploy/macos/$guard.plist "$guard_plist"
  launchctl bootout "gui/$(id -u)/$guard" 2>/dev/null || true
  while launchctl print "gui/$(id -u)/$guard" >/dev/null 2>&1; do sleep 0.5; done
  launchctl bootstrap "gui/$(id -u)" "$guard_plist"
fi
echo "installed $(codesign -dr - "$app" 2>&1 | tail -1)"
