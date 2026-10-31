# Telemetry

Telemetry is a personal executive-function system. It collects activity from a
Mac, a Linux desktop, an Android phone and watch, keeps that history, and uses
it to help one person start what they planned, notice drift, return after
interruptions and keep distractions closed while they work. Assistants connect
to it through an authenticated MCP server; the web panel holds the day plan,
the rules and approvals.

It is built for its owner's own setup and is not yet packaged for other people.

## Parts

- `cmd/telemetryd`: the central service (Go, PostgreSQL), with the HTTP API,
  the MCP server and the panel (`web/`, SvelteKit).
- `cmd/telemetry-desktop`: the desktop collector and blocker for macOS and
  Linux.
- `android/`: the phone and watch collector and blocker.
- `extensions/telemetry-blocker`: the browser extension.
- `deploy/`: service units, launchd jobs and install scripts.

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
