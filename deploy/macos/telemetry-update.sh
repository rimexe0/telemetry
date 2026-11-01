#!/bin/zsh
# Installs the newest signed Telemetry release on this Mac: Telemetry
# Desktop.app (the collector and blocker) and Telemetry.app (the panel). Run
# every ten minutes by com.rime.telemetry.update.plist. The collector is put
# back to the previous version when it does not keep running afterwards.
#
# Releases are trusted only when SHA256SUMS carries a signature from a key in
# release-signers, and the collector only when it is signed with the same
# certificate as the installed one, which keeps its Accessibility permission.
set -euo pipefail

repo="${TELEMETRY_REPO:-rimexe0/telemetry}"
root="$HOME/Library/Application Support/Telemetry"
signers="$root/release-signers"
desktop="$HOME/Applications/Telemetry Desktop.app"
panel="$HOME/Applications/Telemetry.app"
label="com.rime.telemetry.desktop"
installed="$root/.release"
refused="$root/.release-failed"

log() { echo "$(date -u +%FT%TZ) telemetry-update: $*"; }

# The latest release's release.txt names it. Download links are not counted
# against GitHub's API limit of 60 requests an hour per address, which other
# programs on the same machine also use; the file is checked against the
# signed list below like every other.
tag=$(curl -fsSL "https://github.com/$repo/releases/latest/download/release.txt" | awk '{print $1}')
[[ "$tag" =~ ^r[0-9]+$ ]] || { log "the latest release has no usable name: $tag"; exit 1; }
[[ "$tag" == "$(cat "$installed" 2>/dev/null)" ]] && exit 0
# A release that failed here once is not retried; the next one is.
[[ "$tag" == "$(cat "$refused" 2>/dev/null)" ]] && exit 0

files=(telemetry-desktop-macos.zip telemetry-panel-macos.zip telemetry-update-macos.sh release.txt)
self="${0:A}"
work="$root/updates/$tag"
mkdir -p "$work"
cd "$work"
for name in SHA256SUMS SHA256SUMS.sig $files; do
  curl -fsSL -o "$name" "https://github.com/$repo/releases/download/$tag/$name" || { log "$tag has no $name"; exit 1; }
done
ssh-keygen -Y verify -f "$signers" -I telemetry-release -n telemetry-release -s SHA256SUMS.sig < SHA256SUMS >/dev/null
for name in $files; do
  grep -q " $name\$" SHA256SUMS || { log "$name is not in the signed list"; exit 1; }
done
grep -E " (${(j:|:)files})\$" SHA256SUMS | shasum -a 256 -c --quiet -
# A newer updater runs first, so fixes to it reach the Mac even when the old
# one could not install a release.
if ! cmp -s telemetry-update-macos.sh "$self" && zsh -n telemetry-update-macos.sh; then
  install -m 755 telemetry-update-macos.sh "$self.new"
  mv -f "$self.new" "$self"
  exec /bin/zsh "$self"
fi
log "installing $(cat release.txt)"

rm -rf new && mkdir new
ditto -x -k telemetry-desktop-macos.zip new
ditto -x -k telemetry-panel-macos.zip new
codesign --verify --strict "new/Telemetry Desktop.app"
requirement() { codesign -dr - "$1" 2>&1 | grep '^designated'; }
if [[ "$(requirement "new/Telemetry Desktop.app")" != "$(requirement "$desktop")" ]]; then
  log "$tag is signed differently from the installed collector; not installing it"
  echo "$tag" > "$refused"
  exit 1
fi

job="gui/$(id -u)/$label"
# launchd ties a loaded job to its program's code signature and will not
# start a replaced app until the job is loaded again. The guard runs the same
# app.
reload() {
  local name
  for name in $label com.rime.telemetry.guard; do
    launchctl bootout "gui/$(id -u)/$name" 2>/dev/null || true
    while launchctl print "gui/$(id -u)/$name" >/dev/null 2>&1; do sleep 0.5; done
    # The system watchdog may have loaded it again in the meantime.
    launchctl bootstrap "gui/$(id -u)" "$HOME/Library/LaunchAgents/$name.plist" || launchctl print "gui/$(id -u)/$name" >/dev/null
  done
}
pid() { launchctl print "$job" 2>/dev/null | awk '$1 == "pid" {print $3; exit}'; }
# Healthy means running with the same process for half a minute.
healthy() {
  sleep 5
  local first
  first=$(pid)
  [[ -n "$first" ]] || return 1
  sleep 30
  [[ "$(pid)" == "$first" ]]
}

rm -rf "before" && mkdir before
mv "$desktop" "before/"
mv "new/Telemetry Desktop.app" "$desktop"
reload || true
if ! healthy; then
  log "$tag did not keep running; putting the previous collector back"
  mv "$desktop" "new/Telemetry Desktop.failed.app"
  mv "before/Telemetry Desktop.app" "$desktop"
  reload
  echo "$tag" > "$refused"
  exit 1
fi

# The panel holds no state; a running copy keeps working until it is reopened.
[[ -d "$panel" ]] && mv "$panel" "before/"
mv "new/Telemetry.app" "$panel"

echo "$tag" > "$installed"
log "$tag installed"

# Keep the last three downloads, with their saved previous versions.
downloads=("$root"/updates/r*(Nn/))
(( ${#downloads} > 3 )) && rm -rf -- "${(@)downloads[1,-4]}"
exit 0
