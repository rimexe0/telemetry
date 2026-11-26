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

# Browsers install the packed extension from an External Extensions file in
# their own data folder, so Telemetry installs and updates it from outside
# the browser, as browser policy does on Linux (owner request 2026-11-24).
# The browser asks once whether to turn it on, and it can be switched off
# there. Policy cannot do this on a Mac: Chromium force-installs extensions
# from outside its Web Store only on a Mac enrolled in MDM.
external_extension() {
  local crx="$root/extension.crx" manifest="$root/extension/manifest.json" version dir file json
  [[ -f "$crx" && -f "$manifest" ]] || return 0
  version=$(plutil -extract version raw -o - "$manifest" 2>/dev/null) || return 0
  json="{\"external_crx\": \"$crx\", \"external_version\": \"$version\"}"
  # Only browsers that are installed: a data folder left by a removed
  # browser may not be writable (macOS protects other apps' data), and a
  # browser that cannot be written to must not stop the install.
  local -A browsers=(net.imput.helium Helium Google/Chrome "Google Chrome" Chromium Chromium
    BraveSoftware/Brave-Browser "Brave Browser" "Microsoft Edge" "Microsoft Edge")
  for dir in ${(k)browsers}; do
    [[ -d "/Applications/${browsers[$dir]}.app" || -d "$HOME/Applications/${browsers[$dir]}.app" ]] || continue
    [[ -d "$HOME/Library/Application Support/$dir" ]] || continue
    file="$HOME/Library/Application Support/$dir/External Extensions/fachbjhigbkfbbplbnoephgnkgghhbfa.json"
    [[ "$(cat "$file" 2>/dev/null)" == "$json" ]] && continue
    { mkdir -p "${file:h}" && print -r -- "$json" > "$file.new" && mv -f "$file.new" "$file"; } 2>/dev/null ||
      print -- "Could not write the extension file for ${browsers[$dir]}; it is skipped." >&2
  done
}
external_extension

# The latest release's release.txt names it. Download links are not counted
# against GitHub's API limit of 60 requests an hour per address, which other
# programs on the same machine also use; the file is checked against the
# signed list below like every other.
tag=$(curl -fsSL "https://github.com/$repo/releases/latest/download/release.txt" | awk '{print $1}')
[[ "$tag" =~ ^r[0-9]+$ ]] || { log "the latest release has no usable name: $tag"; exit 1; }
# A Mac installed before its updater fetched the packed extension gets its
# release again once, to fetch it: browser policy installs that copy, which
# cannot be switched off (owner request 2026-11-22). The same for the Firefox
# package, which LibreWolf's policy installs.
[[ "$tag" == "$(cat "$installed" 2>/dev/null)" && -f "$root/extension.crx" && -f "$root/extension.xpi" ]] && exit 0
# A release that failed here once is not retried; the next one is.
[[ "$tag" == "$(cat "$refused" 2>/dev/null)" ]] && exit 0

files=(telemetry-desktop-macos.zip telemetry-panel-macos.zip telemetry-extension.tar.gz telemetry-extension.crx telemetry-extension.xpi telemetry-update-macos.sh release.txt)
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

# The browser loads the extension from this folder; the collector tells it when
# the version here is newer, and it reloads itself.
mkdir new/extension
tar -xzf telemetry-extension.tar.gz -C new/extension
[[ -d "$root/extension" ]] && mv "$root/extension" before/extension
mv new/extension "$root/extension"
# Browser policy installs this packed copy from the collector, and the browser
# updates it from there by itself.
install -m 644 telemetry-extension.crx "$root/extension.crx.new"
mv -f "$root/extension.crx.new" "$root/extension.crx"
# LibreWolf's and Firefox's policy installs the Firefox package from the
# collector, and they take its updates from there every hour.
install -m 644 telemetry-extension.xpi "$root/extension.xpi.new"
mv -f "$root/extension.xpi.new" "$root/extension.xpi"
external_extension

echo "$tag" > "$installed"
log "$tag installed"

# Keep the last three downloads, with their saved previous versions.
downloads=("$root"/updates/r*(Nn/))
(( ${#downloads} > 3 )) && rm -rf -- "${(@)downloads[1,-4]}"
exit 0
