# Telemetry

Telemetry is a personal executive-function system. It collects activity from a
Mac, a Linux desktop, an Android phone and watch, keeps that history, and uses
it to help one person start what they planned, notice drift, return after
interruptions and keep distractions closed while they work. Assistants connect
to it through an authenticated MCP server; the web panel holds the day plan,
the rules and approvals.

It started as one person's own setup. It can now be set up by anyone with an
always-on Linux machine and [Tailscale](https://tailscale.com); see Installing.

## Parts

- `cmd/telemetryd`: the central service (Go, PostgreSQL), with the HTTP API,
  the MCP server and the panel (`web/`, SvelteKit).
- `cmd/telemetry-desktop`: the desktop collector and blocker for macOS and
  Linux.
- `android/`: the phone and watch collector and blocker.
- `extensions/telemetry-blocker`: the browser extension.
- `deploy/`: service units, launchd jobs and install scripts.

## Installing

Everything installs from the newest signed release, can be run again at any
time, and then updates itself.

**What you need**

- One Linux machine that stays on (a home server, a desktop that is never
  shut down, a small VPS) with Docker and the Docker Compose plugin. It holds
  the database and the panel.
- [Tailscale](https://tailscale.com/download) on that machine and on every
  computer and phone you connect, all in the same tailnet, with MagicDNS and
  HTTPS certificates turned on in the admin console's DNS page. Devices reach
  the server only over HTTPS, and Tailscale gives it a private address and
  certificate without opening anything to the internet.
- No account anywhere else: the panel signs in with a token the server
  setup makes.

**1. The server**, on the always-on Linux machine, as your own user (not root):

```sh
curl -fsSL https://github.com/rimexe0/telemetry/releases/latest/download/install-server.sh | bash
```

It checks what is missing and says how to get it, makes every secret, starts
PostgreSQL in Docker and the server as a systemd user service, takes a daily
database snapshot, and publishes the server to your tailnet with
`tailscale serve` (at `https://<machine>.<tailnet>.ts.net:8443`). At the end
it prints the panel address and token and the commands below, filled in, and
saves them in `~/telemetry/setup.txt`. If Tailscale asks for permission, run
`sudo tailscale set --operator=$USER` once and run the script again.

**2. Each Mac or Linux computer** that should collect and block:

```sh
curl -fsSL https://github.com/rimexe0/telemetry/releases/latest/download/install.sh | bash
```

It asks for the server address, then shows a link: open it, or click Connect
on the "wants to connect" banner in the panel. It then says what is left for
you to allow (on a Mac, Accessibility) and installs the browser extension by
policy in Chromium browsers (Helium, Chrome, Chromium, Brave, Edge).

**3. An Android phone**: install Tailscale, then download and install
[`telemetry-phone.apk`](https://github.com/rimexe0/telemetry/releases/latest/download/telemetry-phone.apk)
(allow installing from your browser when asked). Open Telemetry, enter the
server address, tap Connect and click Connect on the banner in the panel; the
app then walks through its permissions. Add
`https://github.com/rimexe0/telemetry` to [Obtainium](https://obtainium.imranr.dev)
for updates. With the phone connected to a Mac by USB with USB debugging on,
`install-phone.sh` does the same and grants the permissions over adb:

```sh
curl -fsSL https://github.com/rimexe0/telemetry/releases/latest/download/install-phone.sh | zsh
```

**4. An assistant (recommended, not required).** Telemetry collects, blocks
and shows your day without one. An MCP-capable assistant can read your day
and propose changes to your labels, tasks and plans, which apply only when
you approve them. The server's setup prints these with your address and
token filled in:

```sh
# Claude Code
claude mcp add --scope user --transport http telemetry https://<server>/mcp --header "Authorization: Bearer <read token>"

# Codex
export TELEMETRY_MCP_TOKEN=<read token>
codex mcp add telemetry --url https://<server>/mcp --bearer-token-env-var TELEMETRY_MCP_TOKEN
```

Other MCP clients: streamable HTTP at `https://<server>/mcp` with the header
`Authorization: Bearer <read token>`.

Optionally the server can also run an assistant by itself: judging unknown
apps during a focus session (`TELEMETRY_JUDGE_COMMAND`), working out what you
were doing (`TELEMETRY_INTENT_COMMAND`) and writing to you when you drift
(`TELEMETRY_OUTREACH_COMPOSE_COMMAND`, `TELEMETRY_OUTREACH_SEND_COMMAND`).
Each is a command that takes the prompt as its last argument, such as
`claude -p` or `codex exec`; set them in `~/telemetry/.env` and run
`systemctl --user restart telemetry`.

The browser extension reloads itself when the updater brings a new version.

## Building

```sh
go test ./...
(cd web && pnpm install && pnpm build)
```

Each machine's own addresses stay out of the repository:

- Phone: `telemetryServerUrl`, `telemetryRelayUrl` and `telemetryRelayKey` in
  `android/local.properties`.
- Mac: `TELEMETRY_SERVER`, `TELEMETRY_RELAY` and `TELEMETRY_RELAY_KEY` in
  `deploy/macos/local.env`, read by `install-panel.sh` and
  `install-desktop.sh`.

## Licence

MIT, see [LICENSE](LICENSE). Use it however you like; keep the copyright notice.
