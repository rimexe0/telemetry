#!/bin/bash
# Installs the newest signed Telemetry release on this Linux machine and puts
# the previous version back when the updated service does not come up
# healthy. telemetry-update@<target>.timer runs it as
# `telemetry-update <target>`, reading ~/.config/telemetry-update/<target>.env:
#
#   KIND=server     DIR=$HOME/telemetry            SERVICE=telemetry
#                   HEALTH_URL=https://<server>:8443/healthz
#   KIND=collector  DIR=$HOME/telemetry-collector  SERVICE=telemetry-collector
#
# Releases are trusted only when SHA256SUMS carries a signature from a key in
# ~/.config/telemetry-update/release-signers.
set -euo pipefail

target="${1:?usage: telemetry-update <target>}"
config="${XDG_CONFIG_HOME:-$HOME/.config}/telemetry-update"
source "$config/$target.env"
REPO="${REPO:-rimexe0/telemetry}"
SIGNERS="${SIGNERS:-$config/release-signers}"
installed="$DIR/.release"
refused="$DIR/.release-failed"

log() { echo "telemetry-update[$target]: $*"; }

exec 9>"$DIR/.update.lock"
flock -n 9 || exit 0

# The latest release's release.txt names it. Download links are not counted
# against GitHub's API limit of 60 requests an hour per address, which other
# programs on the same machine also use; the file is checked against the
# signed list below like every other.
tag=$(curl -fsSL "https://github.com/$REPO/releases/latest/download/release.txt" | awk '{print $1}')
[[ "$tag" =~ ^r[0-9]+$ ]] || { log "the latest release has no usable name: $tag"; exit 1; }
[[ "$tag" == "$(cat "$installed" 2>/dev/null)" ]] && exit 0
# A release that failed here once is not retried; the next one is.
[[ "$tag" == "$(cat "$refused" 2>/dev/null)" ]] && exit 0

case "$KIND" in
  server) files=(telemetryd telemetry-places panel.tar.gz migrations.txt) ;;
  collector) files=(telemetry-desktop) ;;
  *) log "unknown KIND $KIND"; exit 1 ;;
esac
files+=(telemetry-update.sh release.txt)

work="$DIR/updates/$tag"
mkdir -p "$work"
cd "$work"
for name in SHA256SUMS SHA256SUMS.sig "${files[@]}"; do
  curl -fsSL -o "$name" "https://github.com/$REPO/releases/download/$tag/$name" || { log "$tag has no $name"; exit 1; }
done
ssh-keygen -Y verify -f "$SIGNERS" -I telemetry-release -n telemetry-release -s SHA256SUMS.sig < SHA256SUMS >/dev/null
for name in "${files[@]}"; do
  grep -q " $name\$" SHA256SUMS || { log "$name is not in the signed list"; exit 1; }
done
grep -E " ($(IFS='|'; echo "${files[*]}"))\$" SHA256SUMS | sha256sum -c --quiet -
log "installing $(cat release.txt)"

# Healthy means running, and for the server answering its health check,
# without restarting for half a minute.
healthy() {
  sleep 5
  local since
  since=$(systemctl --user show -p ActiveEnterTimestampMonotonic --value "$SERVICE")
  for _ in 1 2 3 4 5 6; do
    sleep 5
    systemctl --user is-active --quiet "$SERVICE" || return 1
    [[ "$(systemctl --user show -p ActiveEnterTimestampMonotonic --value "$SERVICE")" == "$since" ]] || return 1
  done
  [[ -z "${HEALTH_URL:-}" ]] || curl -fsS -m 10 -o /dev/null "$HEALTH_URL"
}

swap_in() {
  # Keeps the running version next to the download so it can be put back.
  local from="$1" to="$2"
  cp -p "$to" "$work/$(basename "$to").before"
  install -m 755 "$from" "$to.new"
  mv -f "$to.new" "$to"
}

put_back() {
  local to="$1"
  install -m 755 "$work/$(basename "$to").before" "$to.new"
  mv -f "$to.new" "$to"
}

if [[ "$KIND" == server ]]; then
  # A release with new migrations changes the database: snapshot it first.
  if ! cmp -s migrations.txt "$DIR/.migrations" && systemctl --user cat telemetry-backup.service >/dev/null 2>&1; then
    log "new migrations; taking a database snapshot first"
    systemctl --user start telemetry-backup.service
  fi
  swap_in telemetryd "$DIR/bin/telemetryd"
  [[ -e "$DIR/bin/telemetry-places" ]] && swap_in telemetry-places "$DIR/bin/telemetry-places"
  tar -xzf panel.tar.gz
  mv "$DIR/web/build" "$work/build.before"
  mv build "$DIR/web/build"
else
  swap_in telemetry-desktop "$DIR/telemetry-desktop"
fi

systemctl --user restart "$SERVICE" || true
if ! healthy; then
  log "$tag is not healthy; putting the previous version back"
  if [[ "$KIND" == server ]]; then
    put_back "$DIR/bin/telemetryd"
    [[ -e "$work/telemetry-places.before" ]] && put_back "$DIR/bin/telemetry-places"
    mv "$DIR/web/build" "$work/build.failed"
    mv "$work/build.before" "$DIR/web/build"
  else
    put_back "$DIR/telemetry-desktop"
  fi
  systemctl --user restart "$SERVICE"
  echo "$tag" > "$refused"
  exit 1
fi

echo "$tag" > "$installed"
[[ "$KIND" == server ]] && cp migrations.txt "$DIR/.migrations"
log "$tag installed"

# The updater updates itself last, once the release has proven healthy.
self=$(readlink -f "$0")
if ! cmp -s telemetry-update.sh "$self" && bash -n telemetry-update.sh; then
  install -m 755 telemetry-update.sh "$self.new"
  mv -f "$self.new" "$self"
fi

# Keep the last three downloads, with their saved previous versions.
ls -1d "$DIR"/updates/r* | sort -V | head -n -3 | while read -r old; do rm -rf -- "$old"; done
