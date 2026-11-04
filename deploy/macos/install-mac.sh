#!/bin/zsh
# Installs Telemetry on this Mac from the newest signed release, and can be run
# again at any time; what is already set up stays. After it, the updater keeps
# everything current. It asks for what only you can give: the server's address
# the first time, approving this Mac in the panel, and your password once for
# the system watcher. Then come the permissions.
#
#   curl -fsSL https://github.com/rimexe0/telemetry/releases/latest/download/install-mac.sh | zsh
set -euo pipefail

repo="${TELEMETRY_REPO:-rimexe0/telemetry}"
root="$HOME/Library/Application Support/Telemetry"
apps="$HOME/Applications"
desktop="$apps/Telemetry Desktop.app"
panel="$apps/Telemetry.app"
agents="$HOME/Library/LaunchAgents"
signer='telemetry-release namespaces="telemetry-release" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMZJo9FHdls/jIDhJyAMAOaDfY8jpT7Im9P7QFam4LSO'

say() { print -- "\n== $*"; }
ask() { local answer; read -r "answer?$1: " </dev/tty; print -- "$answer"; }

# Addresses: from the environment, then this Mac's earlier setup, then asked.
config="$root/device.json"
server="${TELEMETRY_SERVER:-}"
relay="${TELEMETRY_RELAY:-}"
relay_key="${TELEMETRY_RELAY_KEY:-}"
job="$agents/com.rime.telemetry.desktop.plist"
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
if [[ ! -f "$config" && -z "$server" ]]; then
  server=$(ask "Telemetry server address (https://…:8443)")
fi
if [[ -z "$relay" ]]; then
  relay=$(ask "Relay address (empty for none)")
  [[ -z "$relay" ]] || relay_key=$(ask "Relay key (from the server log)")
fi

say "Downloading the newest signed release"
tag=$(curl -fsSL "https://github.com/$repo/releases/latest/download/release.txt" | awk '{print $1}')
[[ "$tag" =~ ^r[0-9]+$ ]] || { print "the latest release has no usable name: $tag" >&2; exit 1; }
mkdir -p "$root/bin" "$root/updates" "$apps" "$agents"
work=$(mktemp -d "$root/updates/install-$tag.XXXXXX")
cd "$work"
files=(telemetry-desktop-macos.zip telemetry-panel-macos.zip telemetry-extension.tar.gz telemetry-macos-jobs.tar.gz telemetry-update-macos.sh release.txt)
for name in SHA256SUMS SHA256SUMS.sig $files; do
  curl -fsSL -o "$name" "https://github.com/$repo/releases/download/$tag/$name"
done
print -- "$signer" > "$root/release-signers"
ssh-keygen -Y verify -f "$root/release-signers" -I telemetry-release -n telemetry-release -s SHA256SUMS.sig < SHA256SUMS >/dev/null
grep -E " (${(j:|:)files})\$" SHA256SUMS | shasum -a 256 -c --quiet -
print "$tag verified"

say "Installing the apps"
mkdir new jobs before
ditto -x -k telemetry-desktop-macos.zip new
ditto -x -k telemetry-panel-macos.zip new
tar -xzf telemetry-macos-jobs.tar.gz -C jobs
mkdir new/extension
tar -xzf telemetry-extension.tar.gz -C new/extension
codesign --verify --strict "new/Telemetry Desktop.app"
[[ -d "$desktop" ]] && mv "$desktop" before/
mv "new/Telemetry Desktop.app" "$desktop"
[[ -d "$panel" ]] && mv "$panel" before/
mv new/Telemetry.app "$panel"
[[ -d "$root/extension" ]] && mv "$root/extension" before/extension
mv new/extension "$root/extension"
install -m 755 telemetry-update-macos.sh "$root/bin/telemetry-update"
collector="$desktop/Contents/MacOS/telemetry-desktop"

if [[ ! -f "$config" ]]; then
  say "Connecting this Mac: open the link and approve it in the panel"
  "$collector" login -server "$server" -config "$config"
fi
[[ -z "$server" ]] || defaults write cyou.rime.telemetry.panel PanelAddress "$server/panel/"

say "Starting Telemetry"
for label in com.rime.telemetry.desktop com.rime.telemetry.guard com.rime.telemetry.update; do
  sed -e "s#/Users/ri/#$HOME/#g" "jobs/$label.plist" > "$agents/$label.plist"
done
collector_job="$agents/com.rime.telemetry.desktop.plist"
if [[ -n "$relay" ]]; then
  sed -i '' -e "s#https://relay.example.com#$relay#" -e "s#<string>BDQA[^<]*</string>#<string>$relay_key</string>#" "$collector_job"
else
  # No relay: leave out -relay, its address, -relay-key and its key.
  for index in 9 8 7 6; do
    plutil -remove "ProgramArguments.$index" "$collector_job"
  done
fi
domain="gui/$(id -u)"
for label in com.rime.telemetry.desktop com.rime.telemetry.guard com.rime.telemetry.update; do
  launchctl bootout "$domain/$label" 2>/dev/null || true
  while launchctl print "$domain/$label" >/dev/null 2>&1; do sleep 0.5; done
  launchctl enable "$domain/$label" 2>/dev/null || true
  # The system watcher may have loaded it again in the meantime.
  launchctl bootstrap "$domain" "$agents/$label.plist" || launchctl print "$domain/$label" >/dev/null
done
print -- "$tag" > "$root/.release"

say "Installing the system watcher (asks for your password)"
sudo zsh jobs/install-watchdog.sh

say "What is left is yours to grant"
"$collector" permissions || true
open "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility" 2>/dev/null || true
print -- "
1. Accessibility: turn on Telemetry Desktop in the settings window just opened.
2. Browser extension, once in each browser:
   open chrome://extensions, turn on Developer mode, choose Load unpacked, and pick:
   $root/extension
   Then open its Details and turn on Allow in Incognito. Updates come by themselves.
3. Phone: connect it by USB with USB debugging on, and run install-phone.sh.

Done. Telemetry now updates itself."
