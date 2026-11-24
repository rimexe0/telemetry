#!/bin/bash
# Sets up the owner's own MDM (NanoMDM) on the Telemetry server and writes the
# Mac's enrollment profile to ~/mdm/enroll.mobileconfig. Run as the owner's
# user on the server, from this folder; running it again keeps what exists.
#
# Why: Chromium browsers on a Mac install an extension from outside their Web
# Store by policy only when the Mac is enrolled in MDM. Enrolled, Helium
# shows the Telemetry extension as managed by the organization and it cannot
# be switched off or removed there, as on Linux (owner request 2026-11-24).
#
# NanoMDM listens on the loopback. Caddy, run as the owner's user, serves it
# over HTTPS on the server's tailnet address only, with a certificate from a
# private CA that the enrollment profile carries. That CA is name-constrained
# to this server's tailnet name and address, so a Mac that trusts it trusts
# it for nothing else. The MDM may only list and install profiles (see
# enrollment.py). The APNs push certificate (topic) only matters for sending
# the Mac commands later; set TOPIC to it once there is one.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
version=v0.9.0
sum=1f5a65fc92c46618853c02d4e5bb6d8088f39499e4ae1bb79aef9e60cd53b185
root="$HOME/mdm"
public_port=9443
caddy_version=2.11.7
caddy_sum=727b91701a392de6ebc5027509f548bf39979e5216340d0faed8fa5e69c84f8b
policy="${POLICY:-$here/../macos/helium-policy.plist}"

mkdir -p "$root/bin" "$root/ca" "$root/db"
chmod 700 "$root" "$root/ca"
cd "$root"

if [[ ! -x bin/nanomdm ]]; then
  tmp=$(mktemp -d)
  # NANOMDM_ZIP names a copy fetched elsewhere; the server's downloads from
  # GitHub's release storage can stall.
  if [[ -n "${NANOMDM_ZIP:-}" ]]; then
    cp "$NANOMDM_ZIP" "$tmp/nanomdm.zip"
  else
    curl -fsSL --max-time 300 -o "$tmp/nanomdm.zip" "https://github.com/micromdm/nanomdm/releases/download/$version/nanomdm-linux-amd64-$version.zip"
  fi
  echo "$sum  $tmp/nanomdm.zip" | sha256sum -c -
  python3 -c 'import sys, zipfile; zipfile.ZipFile(sys.argv[1]).extractall(sys.argv[2])' "$tmp/nanomdm.zip" "$tmp"
  install -m 755 "$tmp/nanomdm-linux-amd64-$version/nanomdm-linux-amd64" bin/nanomdm
fi

if [[ ! -x bin/caddy ]]; then
  tmp=$(mktemp -d)
  if [[ -n "${CADDY_TGZ:-}" ]]; then
    cp "$CADDY_TGZ" "$tmp/caddy.tar.gz"
  else
    curl -fsSL --max-time 300 -o "$tmp/caddy.tar.gz" "https://github.com/caddyserver/caddy/releases/download/v$caddy_version/caddy_${caddy_version}_linux_amd64.tar.gz"
  fi
  echo "$caddy_sum  $tmp/caddy.tar.gz" | sha256sum -c -
  tar -xzf "$tmp/caddy.tar.gz" -C "$tmp" caddy
  install -m 755 "$tmp/caddy" bin/caddy
fi

read -r host address < <(tailscale status --json | python3 -c 'import json, sys; me = json.load(sys.stdin)["Self"]; print(me["DNSName"].rstrip("."), next(a for a in me["TailscaleIPs"] if ":" not in a))')

if [[ ! -f api.env ]]; then
  (umask 077; echo "NANOMDM_API=$(openssl rand -hex 24)" > api.env)
fi

# The CA that issues the Mac's device identity; NanoMDM accepts only
# identities from it.
if [[ ! -f ca/ca.pem ]]; then
  openssl req -x509 -newkey rsa:2048 -nodes -days 3650 -keyout ca/ca.key -out ca/ca.pem \
    -subj "/CN=Telemetry MDM device CA" \
    -addext "basicConstraints=critical,CA:TRUE" -addext "keyUsage=critical,keyCertSign,cRLSign"
fi
if [[ ! -f ca/mac.p12 ]]; then
  openssl req -newkey rsa:2048 -nodes -keyout ca/mac.key -out ca/mac.csr -subj "/CN=Telemetry MDM Mac identity"
  printf 'basicConstraints=CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=clientAuth\n' > ca/mac.ext
  openssl x509 -req -in ca/mac.csr -CA ca/ca.pem -CAkey ca/ca.key -CAcreateserial -days 3650 -extfile ca/mac.ext -out ca/mac.pem
  (umask 077; openssl rand -hex 16 > ca/mac.p12.password)
  # Algorithms macOS imports.
  openssl pkcs12 -export -inkey ca/mac.key -in ca/mac.pem -out ca/mac.p12 -name "Telemetry MDM" \
    -certpbe PBE-SHA1-3DES -keypbe PBE-SHA1-3DES -macalg sha1 -passout "file:ca/mac.p12.password"
fi

# The HTTPS certificate, from a CA that may only name this server.
mkdir -p tls
if [[ ! -f tls/ca.pem ]]; then
  openssl req -x509 -newkey rsa:2048 -nodes -days 3650 -keyout tls/ca.key -out tls/ca.pem \
    -subj "/CN=Telemetry MDM server CA ($host)" \
    -addext "basicConstraints=critical,CA:TRUE,pathlen:0" -addext "keyUsage=critical,keyCertSign,cRLSign" \
    -addext "nameConstraints=critical,permitted;DNS:$host,permitted;IP:$address/255.255.255.255"
fi
if [[ ! -f tls/server.pem ]] || ! openssl x509 -checkend $((30 * 86400)) -noout -in tls/server.pem >/dev/null; then
  openssl req -newkey rsa:2048 -nodes -keyout tls/server.key -out tls/server.csr -subj "/CN=$host"
  printf 'basicConstraints=CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\nsubjectAltName=DNS:%s,IP:%s\n' "$host" "$address" > tls/server.ext
  # macOS accepts at most 825 days for a TLS server certificate.
  openssl x509 -req -in tls/server.csr -CA tls/ca.pem -CAkey tls/ca.key -CAcreateserial -days 800 -extfile tls/server.ext -out tls/server.pem
fi
cat > Caddyfile <<CADDY
{
	admin off
	auto_https off
	persist_config off
	# macOS's MDM client failed on HTTP/3 ("bad URL", 2026-11-24).
	servers {
		protocols h1 h2
	}
}
https://$host:$public_port {
	bind $address
	tls $root/tls/server.pem $root/tls/server.key
	reverse_proxy 127.0.0.1:9004
}
CADDY

# A topic is required in the profile; until a push certificate names the
# real one, this stands in and the Mac only talks to the MDM when it chooses.
[[ -f topic ]] || echo "com.apple.mgmt.External.$(cat /proc/sys/kernel/random/uuid)" > topic
[[ -n "${TOPIC:-}" ]] && echo "$TOPIC" > topic

mkdir -p "$HOME/.config/systemd/user"
for unit in telemetry-mdm telemetry-mdm-tls; do
  install -m 644 "$here/$unit.service" "$HOME/.config/systemd/user/$unit.service"
done
systemctl --user daemon-reload
for unit in telemetry-mdm telemetry-mdm-tls; do
  systemctl --user enable "$unit.service"
  systemctl --user restart "$unit.service"
done

python3 "$here/enrollment.py" "https://$host:$public_port" "$(cat topic)" ca/mac.p12 "$(cat ca/mac.p12.password)" "$policy" enroll.mobileconfig tls/ca.pem
chmod 600 enroll.mobileconfig
echo "NanoMDM runs at https://$host:$public_port/mdm (tailnet only); enrollment profile: $root/enroll.mobileconfig"
