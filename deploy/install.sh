#!/bin/bash
# Installs the Telemetry collector and blocker on this Mac or Linux computer
# from the newest signed release, connecting it to your Telemetry server. It
# runs the installer for this system (install-mac.sh or install-linux.sh) and
# can be run again at any time. The server comes first: install-server.sh.
#
#   curl -fsSL https://github.com/rimexe0/telemetry/releases/latest/download/install.sh | bash
#   curl -fsSL .../install.sh | TELEMETRY_SERVER=https://server.example.ts.net:8443 bash
set -euo pipefail

repo="${TELEMETRY_REPO:-rimexe0/telemetry}"
signer='telemetry-release namespaces="telemetry-release" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMZJo9FHdls/jIDhJyAMAOaDfY8jpT7Im9P7QFam4LSO'
fail() { printf 'Cannot continue: %s\n' "$*" >&2; exit 1; }

case "$(uname -s)" in
  Darwin) script=install-mac.sh; shell=zsh; checksum="shasum -a 256"; config="$HOME/Library/Application Support/Telemetry/device.json" ;;
  Linux) script=install-linux.sh; shell=bash; checksum="sha256sum"; config="$HOME/telemetry-collector/device.json" ;;
  *) fail "Telemetry's collector runs on macOS and Linux; for Android install the app (telemetry-phone.apk)." ;;
esac
for command in curl ssh-keygen "$shell"; do
  command -v "$command" >/dev/null || fail "$command is missing; install it and run this again."
done

# The server's address, unless this computer is already connected.
server="${TELEMETRY_SERVER:-${1:-}}"
if [[ -z "$server" && ! -f "$config" ]]; then
  echo "The address of your Telemetry server is printed at the end of install-server.sh"
  echo "(and kept in ~/telemetry/setup.txt on the server), such as https://server.your-tailnet.ts.net:8443"
  read -r -p "Telemetry server address: " server </dev/tty
fi
server="${server%/}"
if [[ -n "$server" ]]; then
  [[ "$server" == https://* ]] || fail "the server address must start with https:// (devices pair only over HTTPS)."
  curl -fsS -m 15 -o /dev/null "$server/healthz" || fail "$server does not answer. Is Tailscale on here, on the same tailnet as the server?"
fi

work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
cd "$work"
tag=$(curl -fsSL "https://github.com/$repo/releases/latest/download/release.txt" | awk '{print $1}')
[[ "$tag" =~ ^r[0-9]+$ ]] || fail "the latest release has no usable name: $tag"
for name in SHA256SUMS SHA256SUMS.sig "$script"; do
  curl -fsSL -o "$name" "https://github.com/$repo/releases/download/$tag/$name"
done
echo "$signer" > signers
ssh-keygen -Y verify -f signers -I telemetry-release -n telemetry-release -s SHA256SUMS.sig < SHA256SUMS >/dev/null
grep " $script\$" SHA256SUMS | $checksum -c --quiet -
echo "$script from $tag verified"

# Strangers have no relay: an empty TELEMETRY_RELAY skips its question.
export TELEMETRY_REPO="$repo" TELEMETRY_SERVER="$server" TELEMETRY_RELAY="${TELEMETRY_RELAY-}"
"$shell" "$work/$script"
