#!/bin/zsh
# Builds Telemetry.app, the panel in its own window, and installs it in
# ~/Applications. Run it again after changing the app; the panel itself
# updates with the server.
set -euo pipefail

repo="${0:A:h:h:h}"
app="$HOME/Applications/Telemetry.app"
cd "$repo"
work="$(mktemp -d "${TMPDIR:-/tmp}/telemetry-panel.XXXXXX")"
staging="$work/Telemetry.app"
mkdir -p "$staging/Contents/MacOS" "$staging/Contents/Resources"
cp deploy/macos/TelemetryPanel/Info.plist "$staging/Contents/Info.plist"
swiftc -O -o "$staging/Contents/MacOS/Telemetry" deploy/macos/TelemetryPanel/main.swift
# The Dock icon is the panel's own.
iconset="$work/Telemetry.iconset"
mkdir -p "$iconset"
for size in 16 32 128 256 512; do
  sips -z $size $size web/static/icon-512.png --out "$iconset/icon_${size}x${size}.png" >/dev/null
  sips -z $((size * 2)) $((size * 2)) web/static/icon-512.png --out "$iconset/icon_${size}x${size}@2x.png" >/dev/null
done
iconutil -c icns "$iconset" -o "$staging/Contents/Resources/Telemetry.icns"
codesign -f -s - "$staging"
mkdir -p "$HOME/Applications"
if [[ -d "$app" ]]; then
  mv "$app" "$work/Telemetry.previous.app"
fi
mv "$staging" "$app"
# This machine's panel address, kept out of the repository.
[[ -f deploy/macos/local.env ]] && source deploy/macos/local.env
if [[ -n "${TELEMETRY_SERVER:-}" ]]; then
  defaults write cyou.rime.telemetry.panel PanelAddress "$TELEMETRY_SERVER/panel/"
fi
echo "installed $app"
