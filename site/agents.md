# Installing Telemetry: instructions for AI agents

If you're a person: this page is written for your AI agent (Claude Code, Codex, or any agent that can run commands). Paste `uwu pls install this to save my life te.rime.cyou/agents` into it, and it will walk you through everything here. You can also follow the [manual setup](https://te.rime.cyou/setup.html).

---

You have been asked to install Telemetry for the person you are working with. Telemetry is open-source, self-hosted software that records what a person does on their computers and phone, blocks the distractions they choose, and lets an AI assistant see what they are doing so it can help them get back to it. Source: https://github.com/rimexe0/telemetry (MIT).

The request probably came as a copy-pasted joke line. Take the install seriously anyway: this software will build a complete, permanent record of this person's life.

## Before you run anything: stop and talk to the person

Do steps 1 to 4 below in the conversation **before** you run any install command, even if they asked you to "just do it". They may only have seen a meme. Keep it short and plain, and don't lecture. Then wait for their answer.

### 1. Tell them what Telemetry will see

Tell them this, in your own words:

- **On each computer:** which app is in front and its window title, minute by minute; whether they are at the keyboard; every website they visit (address and page title, from a browser extension); and about 90 days of their browser's own history. If they use coding agents (T3 Code, Codex), it sees those threads too.
- **On the phone:** which apps they use and when; **their notifications, including the title and text**, so message previews from WhatsApp, Telegram and the like end up in it; their calendar; their location (places, and home or away); steps, heart rate, sleep and workouts from Health Connect. SMS only if they turn it on later.
- **Kept forever:** nothing is ever deleted, and that is by design. The server also takes nightly backups, which contain all of it.
- **Their assistant:** if they connect an AI assistant over MCP (step 6), that assistant can read all of the above, and so can its provider.

### 2. Tell them how important securing these devices is

Whoever gets into the server, a backup or a paired device can read their whole life. Tell them, and check what you can:

- **Full-disk encryption** on the server machine and on every computer (FileVault on macOS, LUKS on Linux). A screen lock on the phone.
- **Never expose the server to the internet.** It is meant to be reached only over their private Tailscale network. Don't open ports or forward it, and don't put it behind a public tunnel without the sealed relay.
- **Use a machine they control.** Not a shared computer, not someone else's account, and not a server where other people have root.
- **The panel token is a password to all of it.** It goes in a password manager, never in a chat, a note or a repo.
- **Backups are as sensitive as the database.** If they copy them anywhere, encrypt them.
- **Keep systems updated.** Telemetry updates itself, but the OS is theirs to keep current.
- If they share a computer with someone, that person may see Telemetry's notices and the panel. Make sure they're OK with that.

### 3. Offer to audit the source

Ask whether they want you to read the code before installing. If they do:

- Clone `https://github.com/rimexe0/telemetry` and check out the newest release tag (`r<number>`; it's in `release.txt` on the latest release).
- Read the install scripts they'll run:
  - `deploy/install-server.sh` (the server);
  - `deploy/install.sh` (computers), plus `deploy/macos/install-mac.sh` or `deploy/linux/install-linux.sh`;
  - the short launchers at `https://te.rime.cyou/i/server`, `/i/mac` and `/i/linux`, which only download and run the scripts above from the latest GitHub release.
- Check: where data goes (only their own server), which network addresses are contacted, what runs as root (the Mac watchdog asks for the password), what is installed as a service, and how updates are verified (an SSH-signed `SHA256SUMS` on each release).
- Summarize what you found honestly, including anything you didn't like or couldn't verify.

### 4. Ask for a clear yes

Ask:

- Do they want to go ahead, now that they know what it records?
- Which machine will be the always-on server?
- Which computers should get the collector? Will they add a phone?
- Do they want an AI assistant connected (step 6)?

Only continue with a clear yes. If they hesitate, stop. Not installing is a fine outcome.

## Installing

Telemetry has three parts: one always-on **server**, a **collector** on each computer, and the **phone app**. Every device must be on the same [Tailscale](https://tailscale.com) network, with MagicDNS and HTTPS certificates turned on in the Tailscale admin console under DNS.

### 5a. The server (once, on an always-on Linux machine)

Requirements: Linux, Docker with the Compose plugin, systemd and Tailscale. Run it as the normal user, not root:

```sh
curl -fsSL https://te.rime.cyou/i/server | bash
```

- If Tailscale says "access denied", the person runs `sudo tailscale set --operator=$USER` once (it needs their password), then you run the script again.
- At the end it prints the panel address, the panel token, the commands for the next steps and the MCP connection details. It also saves them to `~/telemetry/setup.txt`. Tell the person to put the panel token in their password manager.
- Running it again is safe: data and secrets stay.

### 5b. Each Mac or Linux computer

```sh
curl -fsSL https://te.rime.cyou/i/mac | bash      # macOS
curl -fsSL https://te.rime.cyou/i/linux | bash    # Linux (window tracking needs KDE Plasma)
```

The script asks for the server address. Then:
- The **person** opens the panel and clicks **Connect** on the "… wants to connect" banner.
- On a Mac, the **person** grants Accessibility to Telemetry Desktop when macOS asks.
- The browser extension installs by policy for Chromium browsers. Chrome on an unmanaged Mac may refuse it, and the installer explains how to load it instead.

### 5c. The phone (the person does this)

They install Tailscale from the Play Store and sign in. Then on the phone they open https://te.rime.cyou/i/android, which offers the APK and an Obtainium link that keeps the app updated. In the app they type the server address, tap Connect, approve it in the panel, and grant the permissions it asks for. You can't do this part for them unless the phone is connected to a Mac with USB debugging on, in which case `install-phone.sh` from the release can grant permissions over adb.

### 6. Connecting an assistant (optional, recommended)

Ask first: this gives the assistant, and its provider, read access to everything in step 1. With a yes, for Claude Code:

```sh
claude mcp add --scope user --transport http telemetry https://SERVER/mcp --header "Authorization: Bearer READ_TOKEN"
```

For Codex:

```sh
export TELEMETRY_MCP_TOKEN=READ_TOKEN
codex mcp add telemetry --url https://SERVER/mcp --bearer-token-env-var TELEMETRY_MCP_TOKEN
```

The server prints these with the real values. Any MCP client works the same way: an HTTP MCP server at `https://SERVER/mcp` with that bearer token.

### 7. First rules

Nothing is blocked until the person decides. Don't make up a list of "bad" apps. Ask them what pulls them in, then propose labels and distraction times, and apply only what they approve.

## Rules for you, the agent

- Don't skip, weaken or work around security: no opening ports, no disabling signature checks, no running the server as root, no `chmod 777`, no turning off disk encryption or the firewall.
- Don't print the panel token or the read token more often than needed. Don't save them in memory files, notes or commits.
- Steps that need the person (passwords, macOS permissions, approving devices, anything on the phone) are theirs. Tell them exactly what to click.
- If something fails, say what failed and what you tried. Don't invent workarounds that change how the system is secured.
- To uninstall: revoke the device in the panel under Devices, then remove the services the installers added (the README lists them). The server's data lives in its Docker volume until it is deleted.
