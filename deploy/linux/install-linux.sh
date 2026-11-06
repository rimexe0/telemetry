#!/bin/bash
# Installs the Telemetry collector on this Linux machine from the newest signed
# release, and can be run again at any time. After it, a timer keeps it current.
# TELEMETRY_MODULES chooses what runs; the default suits a desktop.
#
#   curl -fsSL https://github.com/rimexe0/telemetry/releases/latest/download/install-linux.sh | bash
set -euo pipefail

repo="${TELEMETRY_REPO:-rimexe0/telemetry}"
dir="$HOME/telemetry-collector"
config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/telemetry-update"
units="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
modules="${TELEMETRY_MODULES:-window,activitywatch-api,t3,blocker}"
signer='telemetry-release namespaces="telemetry-release" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMZJo9FHdls/jIDhJyAMAOaDfY8jpT7Im9P7QFam4LSO'

tag=$(curl -fsSL "https://github.com/$repo/releases/latest/download/release.txt" | awk '{print $1}')
[[ "$tag" =~ ^r[0-9]+$ ]] || { echo "the latest release has no usable name: $tag" >&2; exit 1; }
mkdir -p "$dir" "$config_dir" "$units" "$HOME/.local/bin"
work=$(mktemp -d)
cd "$work"
files=(telemetry-desktop telemetry-extension.tar.gz telemetry-extension.crx telemetry-linux-jobs.tar.gz telemetry-update.sh)
for name in SHA256SUMS SHA256SUMS.sig "${files[@]}"; do
  curl -fsSL -o "$name" "https://github.com/$repo/releases/download/$tag/$name"
done
echo "$signer" > "$config_dir/release-signers"
ssh-keygen -Y verify -f "$config_dir/release-signers" -I telemetry-release -n telemetry-release -s SHA256SUMS.sig < SHA256SUMS >/dev/null
grep -E " ($(IFS='|'; echo "${files[*]}"))\$" SHA256SUMS | sha256sum -c --quiet -
echo "$tag verified"

install -m 755 telemetry-desktop "$dir/telemetry-desktop.new"
mv -f "$dir/telemetry-desktop.new" "$dir/telemetry-desktop"
install -m 755 telemetry-update.sh "$HOME/.local/bin/telemetry-update"
tar -xzf telemetry-linux-jobs.tar.gz -C "$units" telemetry-update@.service telemetry-update@.timer
mkdir -p "$dir/extension"
tar -xzf telemetry-extension.tar.gz -C "$dir/extension"
install -m 644 telemetry-extension.crx "$dir/extension.crx"
mkdir -p jobs
tar -xzf telemetry-linux-jobs.tar.gz -C jobs

if [[ ! -f "$dir/device.json" ]]; then
  server="${TELEMETRY_SERVER:-}"
  [[ -n "$server" ]] || read -r -p "Telemetry server address (https://…:8443): " server </dev/tty
  echo "Connecting this machine: open the link and approve it in the panel"
  "$dir/telemetry-desktop" login -server "$server" -config "$dir/device.json"
fi

printf 'KIND=collector\nDIR=$HOME/telemetry-collector\nSERVICE=telemetry-collector\n' > "$config_dir/collector.env"
if [[ ! -f "$units/telemetry-collector.service" ]]; then
  cat > "$units/telemetry-collector.service" <<UNIT
[Unit]
Description=Telemetry collector
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=%h/telemetry-collector/telemetry-desktop run -modules $modules -config %h/telemetry-collector/device.json -queue %h/telemetry-collector/queue.sqlite3
Restart=always
RestartSec=10

[Install]
WantedBy=default.target
UNIT
fi
echo "$tag" > "$dir/.release"
systemctl --user daemon-reload
systemctl --user enable telemetry-collector.service telemetry-update@collector.timer
systemctl --user restart telemetry-collector.service
systemctl --user start telemetry-update@collector.timer
loginctl enable-linger "$USER" 2>/dev/null || echo "Run: sudo loginctl enable-linger $USER, so Telemetry runs without a login."

# Chromium browsers (Helium among them) read policy from /etc/chromium: it
# makes every profile install the Telemetry extension from this collector,
# and it cannot be turned off there.
policy=/etc/chromium/policies/managed/telemetry.json
if ! cmp -s jobs/telemetry-browser-policy.json "$policy"; then
  echo "Installing the browser policy that keeps the Telemetry extension installed (asks for your password)"
  sudo install -D -m 644 jobs/telemetry-browser-policy.json "$policy"
fi

echo "
Done. Telemetry now updates itself.
Restart your browser once; it then installs the Telemetry extension in every profile.
Turn on Allow in Incognito in the extension's Details.
To read physical input, add yourself to the input group once: sudo usermod -aG input $USER, then log in again."
