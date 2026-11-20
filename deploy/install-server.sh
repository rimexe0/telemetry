#!/bin/bash
# Sets up the Telemetry server on an always-on Linux machine with Docker (a
# home server, a desktop that stays on, a VPS) from the newest signed release.
# It can be run again at any time: secrets, data and settings stay. After it,
# a timer keeps the server current.
#
#   curl -fsSL https://github.com/rimexe0/telemetry/releases/latest/download/install-server.sh | bash
#
# PostgreSQL runs in Docker; the server runs as a systemd user service beside
# it, listening on loopback only. With Tailscale on this machine it is
# published to your tailnet over HTTPS with `tailscale serve`; devices pair
# only over HTTPS. No account anywhere else is needed: the panel signs in with
# a token this script makes.
#
# Settings, all optional:
#   TELEMETRY_DIR          where it lives (default ~/telemetry)
#   TELEMETRY_PORT         loopback port of the server (default 18080)
#   TELEMETRY_DB_PORT      loopback port of PostgreSQL (default 55432)
#   TELEMETRY_HTTPS_PORT   tailnet HTTPS port for tailscale serve (default 8443)
#   TELEMETRY_PUBLIC_URL   the server's HTTPS address when you publish it
#                          yourself instead of with Tailscale
#   TELEMETRY_NAME         name of the service, Docker project and updater
#                          (default telemetry), for a second copy beside one
set -euo pipefail

repo="${TELEMETRY_REPO:-rimexe0/telemetry}"
dir="${TELEMETRY_DIR:-$HOME/telemetry}"
name="${TELEMETRY_NAME:-telemetry}"
port="${TELEMETRY_PORT:-18080}"
db_port="${TELEMETRY_DB_PORT:-55432}"
https_port="${TELEMETRY_HTTPS_PORT:-8443}"
public_url="${TELEMETRY_PUBLIC_URL:-}"
# For testing a build before it is released: a folder with telemetryd,
# panel.tar.gz, telemetry-update.sh and telemetry-linux-jobs.tar.gz. Nothing
# in it is checked against the release signature.
local_files="${TELEMETRY_LOCAL_FILES:-}"
config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/telemetry-update"
units="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
signer='telemetry-release namespaces="telemetry-release" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMZJo9FHdls/jIDhJyAMAOaDfY8jpT7Im9P7QFam4LSO'
# The updater's own name for the default install is "server".
target=server
[[ "$name" == telemetry ]] || target="$name"

say() { printf '\n== %s\n' "$*"; }
fail() { printf '\nCannot continue: %s\n' "$*" >&2; exit 1; }
tty_available() { { : </dev/tty; } 2>/dev/null; }
listening() { ss -ltnH 2>/dev/null | awk '{print $4}' | grep -qE "^(127\.0\.0\.1|0\.0\.0\.0|\*|\[::\]|\[::1\]):$1\$"; }

say "Checking this machine"
[[ "$(uname -s)" == Linux ]] || fail "the server runs on Linux. On a Mac, install only the collector (install.sh) and run the server on an always-on Linux machine."
for command in curl openssl ssh-keygen sha256sum tar flock ss systemctl; do
  command -v "$command" >/dev/null || fail "$command is missing; install it with your package manager and run this again."
done
command -v docker >/dev/null || fail "Docker is missing. Install Docker Engine (https://docs.docker.com/engine/install/) and run this again."
docker compose version >/dev/null 2>&1 || fail "the Docker Compose plugin is missing (docker compose). Install it (https://docs.docker.com/compose/install/linux/) and run this again."
docker info >/dev/null 2>&1 || fail "Docker does not answer for $USER. Start it (sudo systemctl enable --now docker) and let your account use it (sudo usermod -aG docker $USER, then log out and in again)."
systemctl --user show-environment >/dev/null 2>&1 || fail "systemd user services are not available in this session. Log in to this machine directly (or with ssh) as $USER, not through su or sudo."
installed_before=false
[[ -f "$dir/.env" ]] && installed_before=true
if ! $installed_before; then
  listening "$port" && fail "port $port is taken; choose another with TELEMETRY_PORT=..."
  listening "$db_port" && fail "port $db_port is taken; choose another with TELEMETRY_DB_PORT=..."
fi
echo "ok"

say "Downloading the newest signed release"
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
cd "$work"
files=(telemetryd panel.tar.gz migrations.txt telemetry-update.sh telemetry-linux-jobs.tar.gz release.txt)
mkdir -p "$config_dir"
echo "$signer" > "$config_dir/release-signers"
if [[ -n "$local_files" ]]; then
  for file in "${files[@]}"; do
    [[ -f "$local_files/$file" ]] || { [[ "$file" == release.txt || "$file" == migrations.txt ]] && continue; fail "$local_files has no $file"; }
    cp "$local_files/$file" .
  done
  [[ -f release.txt ]] || echo "local $(date -u +%Y%m%dT%H%M%SZ)" > release.txt
  echo "using unsigned local files from $local_files"
else
  tag=$(curl -fsSL "https://github.com/$repo/releases/latest/download/release.txt" | awk '{print $1}')
  [[ "$tag" =~ ^r[0-9]+$ ]] || fail "the latest release has no usable name: $tag"
  for file in SHA256SUMS SHA256SUMS.sig "${files[@]}"; do
    curl -fsSL -o "$file" "https://github.com/$repo/releases/download/$tag/$file"
  done
  ssh-keygen -Y verify -f "$config_dir/release-signers" -I telemetry-release -n telemetry-release -s SHA256SUMS.sig < SHA256SUMS >/dev/null
  grep -E " ($(IFS='|'; echo "${files[*]}"))\$" SHA256SUMS | sha256sum -c --quiet -
  echo "$tag verified"
fi
tag=$(awk '{print $1}' release.txt)

say "Installing into $dir"
umask 077
mkdir -p "$dir/bin" "$dir/web" "$dir/backups" "$units" "$HOME/.local/bin"
chmod 700 "$dir"
install -m 755 telemetryd "$dir/bin/telemetryd.new"
mv -f "$dir/bin/telemetryd.new" "$dir/bin/telemetryd"
mkdir panel
tar -xzf panel.tar.gz -C panel
chmod -R u=rwX,go=rX panel/build
[[ -d "$dir/web/build" ]] && mv "$dir/web/build" "$work/build.before"
mv panel/build "$dir/web/build"
[[ -f migrations.txt ]] && install -m 644 migrations.txt "$dir/.migrations"

env_file="$dir/.env"
if [[ ! -f "$env_file" ]]; then
  secret() { openssl rand -hex 32; }
  database_password=$(secret)
  cat > "$env_file" <<ENV
# Telemetry server settings. Keep this file private: it holds every secret.
# After changing it: systemctl --user restart $name

POSTGRES_PASSWORD=$database_password
TELEMETRY_DATABASE_URL=postgres://telemetry:$database_password@127.0.0.1:$db_port/telemetry?sslmode=disable
TELEMETRY_LISTEN=127.0.0.1:$port
TELEMETRY_PANEL_DIR=$dir/web/build
# The panel signs in with this token.
TELEMETRY_PANEL_TOKEN=$(secret)
TELEMETRY_PANEL_SESSION_SECRET=$(secret)
# Assistants read and propose changes over MCP with this token.
TELEMETRY_READ_TOKEN=$(secret)
# Paired devices get their own credentials; this one is for older collectors.
TELEMETRY_INGEST_TOKEN=$(secret)
# The HTTPS address devices and the panel use; pairing needs it.
TELEMETRY_PUBLIC_URL=

# Optional: an assistant the server runs by itself. Each is one command that
# takes the prompt as its last argument and prints the answer, run as $USER
# without your shell's PATH, so give the full path. Leave them empty and
# Telemetry still collects, blocks and answers assistants over MCP.
#
# Judges unknown apps and sites during a focus session, for example
#   TELEMETRY_JUDGE_COMMAND=/home/you/.local/bin/claude -p
#   TELEMETRY_JUDGE_COMMAND=/usr/local/bin/codex exec
TELEMETRY_JUDGE_COMMAND=
# Works out what you were trying to do from all activity (default: the judge).
TELEMETRY_INTENT_COMMAND=
# Writes to you when you drift: one command words the message, one sends its
# standard input (to Telegram, Signal, email, ...).
TELEMETRY_OUTREACH_COMPOSE_COMMAND=
TELEMETRY_OUTREACH_SEND_COMMAND=
ENV
  echo "made new secrets in $env_file"
else
  echo "keeping the settings and secrets in $env_file"
fi
chmod 600 "$env_file"
value() { sed -n "s/^$1=//p" "$env_file" | tail -n 1; }
set_value() {
  if grep -q "^$1=" "$env_file"; then
    sed -i "s#^$1=.*#$1=$2#" "$env_file"
  else
    echo "$1=$2" >> "$env_file"
  fi
}
port=$(value TELEMETRY_LISTEN | sed 's/.*://')
db_port=$(value TELEMETRY_DATABASE_URL | sed -E 's#.*@[^:]+:([0-9]+)/.*#\1#')

cat > "$dir/compose.yaml" <<COMPOSE
# Written by install-server.sh; changes are replaced on the next run.
name: $name
services:
  postgres:
    image: postgres:16-alpine
    restart: unless-stopped
    environment:
      POSTGRES_DB: telemetry
      POSTGRES_USER: telemetry
      POSTGRES_PASSWORD: \${POSTGRES_PASSWORD:?set POSTGRES_PASSWORD in .env}
    ports:
      - "127.0.0.1:$db_port:5432"
    volumes:
      - postgres:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U telemetry -d telemetry"]
      interval: 5s
      timeout: 3s
      retries: 12
volumes:
  postgres: {}
COMPOSE

docker_bin=$(command -v docker)
compose="$docker_bin compose --project-name $name --env-file $dir/.env --file $dir/compose.yaml"
cat > "$dir/backup.sh" <<BACKUP
#!/bin/bash
# A daily snapshot of the database into $dir/backups. Nothing is deleted:
# copy or prune old snapshots yourself.
set -euo pipefail
cd "$dir/backups"
exec 9>.backup.lock
flock -n 9 || exit 0
file="telemetry-\$(date -u +%Y%m%dT%H%M%SZ).dump"
$compose exec -T postgres sh -c 'pg_dump --format=custom --no-owner --no-privileges -U "\$POSTGRES_USER" "\$POSTGRES_DB"' > "\$file.partial"
mv "\$file.partial" "\$file"
echo "saved $dir/backups/\$file"
BACKUP
chmod 700 "$dir/backup.sh"

cat > "$units/$name.service" <<UNIT
# Written by install-server.sh.
[Unit]
Description=Telemetry server
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
EnvironmentFile=$dir/.env
WorkingDirectory=$dir
ExecStartPre=$compose up --detach --wait
ExecStartPre=$dir/bin/telemetryd -migrate
ExecStart=$dir/bin/telemetryd
Restart=on-failure
RestartSec=5s
TimeoutStartSec=10min
TimeoutStopSec=20s
NoNewPrivileges=true

[Install]
WantedBy=default.target
UNIT
cat > "$units/$name-backup.service" <<UNIT
[Unit]
Description=Telemetry database snapshot

[Service]
Type=oneshot
ExecStart=$dir/backup.sh
Nice=10
UNIT
cat > "$units/$name-backup.timer" <<UNIT
[Unit]
Description=Daily Telemetry database snapshot

[Timer]
OnCalendar=daily
RandomizedDelaySec=1h
Persistent=true

[Install]
WantedBy=timers.target
UNIT

# The updater installs each new signed release and puts the previous one
# back when the server does not come up healthy.
install -m 755 telemetry-update.sh "$HOME/.local/bin/telemetry-update"
tar -xzf telemetry-linux-jobs.tar.gz -C "$units" telemetry-update@.service telemetry-update@.timer
printf 'KIND=server\nDIR=%s\nSERVICE=%s\nHEALTH_URL=http://127.0.0.1:%s/healthz\n' "$dir" "$name" "$port" > "$config_dir/$target.env"
[[ -n "$local_files" ]] || echo "$tag" > "$dir/.release"

say "Starting the server (the first start downloads PostgreSQL)"
systemctl --user daemon-reload
systemctl --user enable --quiet "$name.service" "$name-backup.timer"
systemctl --user start "$name-backup.timer"
if [[ -z "$local_files" ]]; then
  systemctl --user enable --quiet --now "telemetry-update@$target.timer"
else
  # The updater would replace the local build with the release.
  echo "local files: automatic updates stay off (systemctl --user enable --now telemetry-update@$target.timer)"
fi
systemctl --user restart "$name.service" || fail "the server did not start. See: journalctl --user -u $name -n 50"
healthy() {
  for _ in $(seq 1 "${1:-120}"); do
    curl -fsS -m 3 -o /dev/null "http://127.0.0.1:$port/healthz" 2>/dev/null && return 0
    sleep 2
  done
  return 1
}
healthy || fail "the server did not come up. See: journalctl --user -u $name -n 50"
echo "running on http://127.0.0.1:$port"
if ! loginctl show-user "$USER" -p Linger 2>/dev/null | grep -q yes; then
  loginctl enable-linger "$USER" 2>/dev/null || { tty_available && sudo loginctl enable-linger "$USER" </dev/tty; } || \
    echo "Run once: sudo loginctl enable-linger $USER  (so the server keeps running after you log out and after a restart)"
fi

# HTTPS: the address given, the one saved earlier, or Tailscale's.
[[ -n "$public_url" ]] || public_url=$(value TELEMETRY_PUBLIC_URL)
if [[ -z "$public_url" ]] && command -v tailscale >/dev/null && tailscale status >/dev/null 2>&1; then
  say "Publishing the server to your tailnet with tailscale serve"
  host=$(tailscale status --json | grep -m1 '"DNSName"' | sed -E 's/.*"DNSName": *"([^"]*)\.?".*/\1/; s/\.$//')
  # Waits at most a minute: a tailnet without HTTPS certificates asks for
  # them to be turned on and would wait for that.
  serve() { timeout 60 tailscale serve --bg --https="$https_port" "http://127.0.0.1:$port" 2>&1; }
  if tailscale serve status 2>/dev/null | grep -q ":$https_port\b" && ! tailscale serve status 2>/dev/null | grep -A3 ":$https_port\b" | grep -q "127.0.0.1:$port"; then
    echo "Tailscale already serves something else on port $https_port; choose another with TELEMETRY_HTTPS_PORT=..."
  else
    served=true
    output=$(serve) || served=false
    if ! $served && grep -qi "access denied\|operator" <<<"$output"; then
      if sudo -n tailscale set --operator="$USER" 2>/dev/null || { tty_available && echo "tailscale serve needs permission once (asks for your password)" && sudo tailscale set --operator="$USER" </dev/tty; }; then
        served=true
        output=$(serve) || served=false
      else
        output="Tailscale needs permission once. Run: sudo tailscale set --operator=$USER  and then run this script again."
      fi
    fi
    if $served && [[ -n "$host" ]]; then
      public_url="https://$host:$https_port"
      echo "published at $public_url"
    else
      echo "$output"
      echo "Not published. If Tailscale asked to turn on HTTPS: in the admin console's DNS page turn on MagicDNS and HTTPS certificates, then run this again."
    fi
  fi
fi
if [[ -n "$public_url" ]]; then
  public_url="${public_url%/}"
  if [[ "$(value TELEMETRY_PUBLIC_URL)" != "$public_url" ]]; then
    set_value TELEMETRY_PUBLIC_URL "$public_url"
    systemctl --user restart "$name.service" || fail "the server did not start. See: journalctl --user -u $name -n 50"
    healthy || fail "the server did not come up. See: journalctl --user -u $name -n 50"
  fi
  curl -fsS -m 15 -o /dev/null "$public_url/healthz" 2>/dev/null || echo "Note: $public_url does not answer from this machine yet; Tailscale can take a minute for a new certificate."
fi

panel_token=$(value TELEMETRY_PANEL_TOKEN)
read_token=$(value TELEMETRY_READ_TOKEN)
release="https://github.com/$repo/releases/latest/download"
address="${public_url:-https://<this server>}"
summary="$dir/setup.txt"
{
  echo "Telemetry server $tag"
  echo
  if [[ -z "$public_url" ]]; then
    echo "NOT REACHABLE OVER HTTPS YET. Devices pair only over HTTPS. Install Tailscale"
    echo "(https://tailscale.com/download) and run this script again, or publish"
    echo "http://127.0.0.1:$port over HTTPS yourself and run it again with"
    echo "TELEMETRY_PUBLIC_URL=https://your.address."
    echo "Until then the panel is reachable through an SSH tunnel:"
    echo "  ssh -L $port:127.0.0.1:$port $USER@$(hostname)   then open http://127.0.0.1:$port/panel/"
    echo
  fi
  echo "PANEL"
  echo "  $address/panel/"
  echo "  Sign in with the panel token: $panel_token"
  echo
  echo "COMPUTERS (Mac or Linux), on each one that should collect and block:"
  echo "  curl -fsSL $release/install.sh | TELEMETRY_SERVER=$address bash"
  echo "  Then click Connect on the 'wants to connect' banner in the panel."
  echo
  echo "ANDROID PHONE"
  echo "  1. Install Tailscale on the phone and sign in to the same tailnet."
  echo "  2. Download and install the app: $release/telemetry-phone.apk"
  echo "     (allow installing from your browser when Android asks)."
  echo "  3. Open Telemetry, enter $address as the server and tap Connect."
  echo "  4. Click Connect on the banner in the panel, then follow the app's steps."
  echo "  For updates, add https://github.com/$repo to Obtainium."
  echo
  echo "ASSISTANT (recommended, not required): any MCP client can read your day and"
  echo "propose changes you approve. MCP address: $address/mcp"
  echo "  Header: Authorization: Bearer $read_token"
  echo "  Claude Code:"
  echo "    claude mcp add --scope user --transport http telemetry $address/mcp --header \"Authorization: Bearer $read_token\""
  echo "  Codex (token in an environment variable, e.g. in your shell profile):"
  echo "    export TELEMETRY_MCP_TOKEN=$read_token"
  echo "    codex mcp add telemetry --url $address/mcp --bearer-token-env-var TELEMETRY_MCP_TOKEN"
  echo
  echo "OPTIONAL: let the server run an assistant by itself (judging unknown apps in a"
  echo "focus session, working out what you were doing, writing to you on drift):"
  echo "set TELEMETRY_JUDGE_COMMAND and the others in $env_file,"
  echo "then: systemctl --user restart $name"
  echo
  echo "Data: Docker volume ${name}_postgres; daily snapshots in $dir/backups."
  echo "Logs: journalctl --user -u $name -f"
  echo "Updates install by themselves (telemetry-update@$target.timer)."
} > "$summary"
chmod 600 "$summary"

say "Done"
cat "$summary"
if command -v qrencode >/dev/null && [[ -n "$public_url" ]]; then
  echo
  echo "Scan with the phone to download the app:"
  qrencode -t ansiutf8 "$release/telemetry-phone.apk"
fi
echo
echo "This is saved in $summary (only you can read it)."
