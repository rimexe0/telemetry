<script lang="ts">
  import { getLock, previewLock, saveLock, setPin, unlockWithPin, type Bedtime, type ExtensionRule, type LockResponse, type SettingsLock } from './api';
  import { formatWindows, parseWindows, saveWarning } from './lock';

  /** Which setting this card shows; each loads the same settings. */
  export let part: 'lock' | 'bedtime' | 'extension' = 'lock';

  let data: LockResponse | null = null;
  let busy = false;
  let error = '';
  let notice = '';
  let enabled = false;
  let lockTimes = '';
  let pinTimes = '';
  let unlockMinutes = 15;
  let bedtime: Bedtime = { enabled: false, start: '', end: '' };
  let extension: ExtensionRule = { enabled: false, quiet_seconds: 60 };
  let currentPin = '';
  let newPin = '';
  let enteredPin = '';
  // A save that needs a second look: a very long bedtime, or one that only
  // starts on a later day.
  let confirmMessage = '';
  let confirmRun: (() => Promise<string>) | null = null;

  void load();

  async function load() {
    try {
      data = await getLock();
      enabled = data.lock.enabled;
      lockTimes = formatWindows(data.lock.windows);
      pinTimes = formatWindows(data.lock.pin_windows);
      unlockMinutes = data.lock.unlock_minutes || 15;
      bedtime = { ...data.bedtime };
      extension = { enabled: data.extension?.enabled ?? false, quiet_seconds: data.extension?.quiet_seconds || 60 };
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Unable to load the lock.';
    }
  }

  async function act(work: () => Promise<string>) {
    busy = true;
    error = '';
    notice = '';
    try {
      notice = await work();
      await load();
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Something went wrong.';
    } finally {
      busy = false;
    }
  }

  const time = (at?: string) => (at ? new Date(at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: false }) : '');

  type Input = { lock?: SettingsLock; bedtime?: Bedtime; extension?: ExtensionRule };
  // Asks the server first whether the change would wait a day, and asks the
  // owner to confirm when it would or when bedtime is very long.
  const guardedSave = (input: Input, key: 'lock' | 'bedtime' | 'extension') => act(async () => {
    const preview = await previewLock(input);
    const run = async () => { await saveLock(input); return preview.waits_until[key] ? `Saved. It starts on ${preview.waits_until[key]}.` : 'Saved.'; };
    const message = saveWarning(preview.waits_until[key], input.bedtime);
    if (message) {
      confirmMessage = message;
      confirmRun = run;
      return '';
    }
    return run();
  });
  const confirmSave = () => act(async () => {
    const run = confirmRun;
    confirmMessage = '';
    confirmRun = null;
    return run ? run() : '';
  });
  const saveRule = () => {
    const windows = parseWindows(lockTimes), pinWindows = parseWindows(pinTimes);
    if (!windows || !pinWindows) { error = 'Write times as 09:00-18:00, separated by commas.'; return; }
    return guardedSave({ lock: { enabled, windows, pin_windows: pinWindows, unlock_minutes: unlockMinutes } }, 'lock');
  };
  const saveBedtime = () => guardedSave({ bedtime }, 'bedtime');
  const saveExtension = () => guardedSave({ extension }, 'extension');
  const savePin = () => act(async () => {
    await setPin(currentPin, newPin);
    currentPin = newPin = '';
    return 'PIN saved.';
  });
  const unlock = () => act(async () => {
    const until = await unlockWithPin(enteredPin);
    enteredPin = '';
    return `Unlocked until ${time(until)}.`;
  });
</script>

<section class="lock" aria-labelledby="{part}-heading">
  {#if part === 'lock'}
  <h3 id="lock-heading">Settings lock</h3>
  <p class="muted small">
    {#if !data}Loading…
    {:else if data.state.unlocked_until}Unlocked by the PIN until {time(data.state.unlocked_until)}.
    {:else if data.state.locked}Locked{data.state.until ? ` until ${time(data.state.until)}` : ''}: nothing can loosen blocking.
    {:else if data.lock.enabled}On, not holding now.{:else}Off.{/if}
    {#if data?.lock.enabled} PIN {data.state.pin_open ? 'accepted now' : `accepted ${formatWindows(data.lock.pin_windows) || 'any time'}`}.{/if}
  </p>
  {#if data?.lock_next}<p class="muted small">From {data.lock_next_from}: {data.lock_next.enabled ? formatWindows(data.lock_next.windows) || 'always' : 'off'}.</p>{/if}

  {#if data?.lock.enabled && data.state.pin_set}
    <form class="row" on:submit|preventDefault={unlock}>
      <label>PIN <input type="password" inputmode="numeric" autocomplete="off" bind:value={enteredPin} /></label>
      <button disabled={busy || !enteredPin || !data.state.pin_open || data.state.tries_left === 0}>Unlock</button>
      <span class="muted small">{data.state.tries_left} tries left this hour</span>
    </form>
  {/if}

  <form class="row" on:submit|preventDefault={saveRule}>
    <label><input type="checkbox" bind:checked={enabled} /> On</label>
    <label>Locked <input class="times" placeholder="always" bind:value={lockTimes} /></label>
    <label>PIN works <input class="times" placeholder="any time" bind:value={pinTimes} /></label>
    <label>for <input type="number" min="1" max="240" bind:value={unlockMinutes} /> min</label>
    <button disabled={busy}>Save</button>
  </form>

  <form class="row" on:submit|preventDefault={savePin}>
    {#if data?.state.pin_set}<label>Current PIN <input type="password" inputmode="numeric" autocomplete="off" bind:value={currentPin} /></label>{/if}
    <label>{data?.state.pin_set ? 'New PIN' : 'PIN'} <input type="password" inputmode="numeric" autocomplete="off" bind:value={newPin} /></label>
    <button disabled={busy || !newPin}>{data?.state.pin_set ? 'Change PIN' : 'Set PIN'}</button>
    <span class="muted small">Let the person who holds it type it.</span>
  </form>
  <p class="muted small">Times as 09:00-18:00, separated by commas. Empty locked times means always.</p>
  {:else if part === 'bedtime'}
  <h3 id="bedtime-heading">Bedtime</h3>
  <p class="muted small">{bedtime.enabled ? `On, ${bedtime.start}–${bedtime.end}.` : 'Off.'} Screens lock when used; nothing is closed, so agents keep running.</p>
  {#if data?.bedtime_next}<p class="muted small">From {data.bedtime_next_from}: {data.bedtime_next.enabled ? `${data.bedtime_next.start}–${data.bedtime_next.end}` : 'off'}.</p>{/if}
  <form class="row" on:submit|preventDefault={saveBedtime}>
    <label><input type="checkbox" bind:checked={bedtime.enabled} /> On</label>
    <label>From <input type="time" bind:value={bedtime.start} /></label>
    <label>to <input type="time" bind:value={bedtime.end} /></label>
    <button disabled={busy}>Save</button>
  </form>
  {:else}
  <h3 id="extension-heading">Browser extension</h3>
  <p class="muted small">{extension.enabled ? `On: a browser without a running extension is hidden after ${extension.quiet_seconds} s while sites are blocked.` : 'Off.'} Hidden, never quit; its Extensions page stays usable.</p>
  {#if data?.extension_next}<p class="muted small">From {data.extension_next_from}: {data.extension_next.enabled ? `${data.extension_next.quiet_seconds} s` : 'off'}.</p>{/if}
  <form class="row" on:submit|preventDefault={saveExtension}>
    <label><input type="checkbox" bind:checked={extension.enabled} /> Hide a browser without it</label>
    <label>after <input type="number" min="30" max="3600" bind:value={extension.quiet_seconds} /> s</label>
    <button disabled={busy}>Save</button>
  </form>
  {/if}
  {#if confirmMessage}
    <div class="confirm" role="alertdialog" aria-label="Confirm change">
      <p>{confirmMessage} Save anyway?</p>
      <button disabled={busy} on:click={confirmSave}>Save anyway</button>
      <button class="quiet-button" disabled={busy} on:click={() => { confirmMessage = ''; confirmRun = null; }}>Cancel</button>
    </div>
  {/if}
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  {#if notice}<p class="muted small" role="status">{notice}</p>{/if}
</section>

<style>
  .lock { border-top: 1px solid var(--line); margin-top: 16px; padding-top: 10px; }
  h3 { margin: 6px 0; }
  .row { align-items: center; display: flex; flex-wrap: wrap; gap: 8px; margin: 6px 0; font-size: .85rem; }
  .row input[type='number'] { width: 4.5em; }
  .row input[type='password'] { width: 8em; }
  .times { width: 14em; }
  .confirm { align-items: center; border: 1px solid var(--fg); display: flex; flex-wrap: wrap; gap: 8px; margin-top: 8px; padding: 8px; }
  .confirm p { flex: 1 1 100%; font-size: .85rem; margin: 0; }
  .muted { color: var(--muted); }
  .small { font-size: .78rem; }
</style>
