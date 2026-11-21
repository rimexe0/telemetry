<script lang="ts">
  import { onDestroy } from 'svelte';
  import { cancelSetupInvite, createSetupCode, getPairingSettings, listSetupInvites, savePairingSettings, type PairingSettings, type SetupCode } from './api';

  // Called when a device connects with the code, so the list can refresh.
  export let onconnected: () => void = () => {};

  let code: SetupCode | null = null;
  let busy = false;
  let error = '';
  let connectedName = '';
  let copied = '';
  let now = Date.now();
  let settings: PairingSettings | null = null;
  let relayDraft = '';
  let relaySaved = '';
  let poll: ReturnType<typeof setInterval> | undefined;
  const tick = setInterval(() => { now = Date.now(); }, 1000);
  onDestroy(() => { clearInterval(tick); if (poll) clearInterval(poll); });

  void getPairingSettings().then(value => { settings = value; relayDraft = value.relay_url; }).catch(() => {});

  $: left = code ? Math.max(0, new Date(code.expires_at).getTime() - now) : 0;
  $: minutesLeft = Math.ceil(left / 60_000);
  $: expired = code !== null && left === 0 && !connectedName;

  async function make() {
    busy = true;
    error = '';
    connectedName = '';
    try {
      code = await createSetupCode();
      if (poll) clearInterval(poll);
      poll = setInterval(watch, 3000);
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Could not make a setup code.';
    } finally {
      busy = false;
    }
  }

  // Shows when the code was used, and by which device.
  async function watch() {
    if (!code) return;
    try {
      const invite = (await listSetupInvites()).find(item => item.invite_id === code?.invite_id);
      if (invite?.status === 'used') {
        connectedName = invite.used_by_name || invite.used_by_device || 'A device';
        if (poll) clearInterval(poll);
        onconnected();
      } else if (invite && invite.status !== 'open' && poll) {
        clearInterval(poll);
      }
    } catch {
      // Keep the code on screen; the next check may work.
    }
  }

  async function cancel() {
    if (code && !connectedName) await cancelSetupInvite(code.invite_id).catch(() => {});
    if (poll) clearInterval(poll);
    code = null;
    connectedName = '';
  }

  async function copy(name: string, text: string) {
    try {
      await navigator.clipboard.writeText(text);
      copied = name;
      setTimeout(() => { if (copied === name) copied = ''; }, 1600);
    } catch {
      error = 'Copying was blocked; select the text and copy it instead.';
    }
  }

  async function saveRelay() {
    error = '';
    try {
      settings = await savePairingSettings(relayDraft.trim());
      relayDraft = settings.relay_url;
      relaySaved = settings.relay_url ? 'Saved. New setup codes include it.' : 'Cleared.';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Could not save the relay address.';
    }
  }
</script>

<section class="add" aria-labelledby="add-heading">
  <div class="section-title">
    <h2 id="add-heading">Add a device</h2>
    {#if !code}<button on:click={make} disabled={busy}>{busy ? 'Making a code…' : 'Add a device'}</button>{/if}
  </div>
  {#if !code}
    <p class="muted">Makes a one-time setup code. The new device connects with it straight away, with nothing to approve afterwards. Nothing is sent anywhere but your own server.</p>
  {/if}
  {#if error}<p class="error" role="alert">{error}</p>{/if}

  {#if code}
    {#if connectedName}
      <p class="done" role="status"><strong>{connectedName}</strong> connected.</p>
      <button class="quiet-button" on:click={cancel}>Close</button>
    {:else if expired}
      <p class="muted">This code expired unused.</p>
      <button on:click={make} disabled={busy}>Make a new code</button>
    {:else}
      <div class="code-line">
        <span class="label">Setup code</span>
        <strong class="code">{code.code}</strong>
        <span class="muted">works once, for {minutesLeft} more minute{minutesLeft === 1 ? '' : 's'}</span>
      </div>
      <div class="ways">
        <div class="way">
          <h3>Phone</h3>
          <img class="qr" src={code.qr} alt="QR code that sets up the Telemetry phone app" width="220" height="220" />
          <p>Scan this with the phone's camera. If the app isn't installed yet, the page it opens offers it first.</p>
          <p class="muted">Or, in the app, type the server address <code>{code.server_url}</code> and the code above.</p>
          {#if !code.relay_url}<p class="muted">The phone needs Tailscale on to reach your server.</p>{/if}
        </div>
        <div class="way">
          <h3>Mac</h3>
          <p>Paste this into Terminal. It installs everything and connects with this code.</p>
          <pre>{code.commands.mac}</pre>
          <button class="quiet-button" on:click={() => copy('mac', code?.commands.mac ?? '')}>{copied === 'mac' ? 'Copied' : 'Copy'}</button>
          <h3>Linux</h3>
          <pre>{code.commands.linux}</pre>
          <button class="quiet-button" on:click={() => copy('linux', code?.commands.linux ?? '')}>{copied === 'linux' ? 'Copied' : 'Copy'}</button>
          <p class="muted">An agent can run it for you, too. Each code works for one device; make another for the next.</p>
        </div>
      </div>
      <button class="quiet-button" on:click={cancel}>Cancel this code</button>
    {/if}
  {/if}

  {#if settings?.relay_available}
    <details class="relay" open={!settings.relay_url}>
      <summary>Relay for phones without Tailscale</summary>
      <p class="muted">This server has a sealed relay. Enter its public address so setup codes carry it, and phones can send without Tailscale.</p>
      <div class="relay-row">
        <input type="url" placeholder="https://relay.example.com" bind:value={relayDraft} aria-label="Relay address" />
        <button class="quiet-button" on:click={saveRelay}>Save</button>
      </div>
      {#if relaySaved}<p class="muted" role="status">{relaySaved}</p>{/if}
    </details>
  {/if}
</section>

<style>
  .add { border: 1px solid var(--fg); background: var(--panel); margin: 16px 0; padding: 12px; }
  .add h3 { font-size: .9rem; margin: 12px 0 6px; }
  .code-line { align-items: baseline; display: flex; flex-wrap: wrap; gap: 10px; margin: 8px 0 4px; }
  .label { color: var(--muted); font-size: .78rem; text-transform: uppercase; letter-spacing: .08em; }
  .code { font-family: ui-monospace, Menlo, monospace; font-size: 1.6rem; letter-spacing: .08em; }
  .ways { display: grid; gap: 18px; grid-template-columns: repeat(auto-fit, minmax(260px, 1fr)); margin: 8px 0 12px; }
  .way { border-top: 1px solid var(--line); min-width: 0; }
  .qr { background: #fff; display: block; height: auto; image-rendering: pixelated; max-width: 220px; padding: 8px; width: 100%; }
  pre { background: var(--soft); font-size: .74rem; margin: 6px 0; overflow-x: auto; padding: 8px; white-space: pre-wrap; word-break: break-all; }
  code { overflow-wrap: anywhere; }
  .done { font-size: 1.05rem; }
  .relay { border-top: 1px solid var(--line); margin-top: 12px; padding-top: 8px; }
  .relay summary { cursor: pointer; }
  .relay-row { display: flex; gap: 8px; }
  .relay-row input { flex: 1; min-width: 0; }
  .muted { color: var(--muted); }
  .error { color: #e5484d; }
</style>
