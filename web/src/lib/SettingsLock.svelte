<script lang="ts">
  import { getLock, saveLock, setPin, unlockWithPin, type Bedtime, type LockResponse } from './api';
  import { formatWindows, parseWindows } from './lock';

  let data: LockResponse | null = null;
  let busy = false;
  let error = '';
  let notice = '';
  let enabled = false;
  let lockTimes = '';
  let pinTimes = '';
  let unlockMinutes = 15;
  let bedtime: Bedtime = { enabled: false, start: '', end: '' };
  let currentPin = '';
  let newPin = '';
  let enteredPin = '';

  void load();

  async function load() {
    try {
      data = await getLock();
      enabled = data.lock.enabled;
      lockTimes = formatWindows(data.lock.windows);
      pinTimes = formatWindows(data.lock.pin_windows);
      unlockMinutes = data.lock.unlock_minutes || 15;
      bedtime = { ...data.bedtime };
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

  const saveRule = () => act(async () => {
    const windows = parseWindows(lockTimes), pinWindows = parseWindows(pinTimes);
    if (!windows || !pinWindows) throw new Error('Write times as 09:00-18:00, separated by commas.');
    const saved = await saveLock({ lock: { enabled, windows, pin_windows: pinWindows, unlock_minutes: unlockMinutes } });
    return saved.lock_next_from ? `Saved. It asks less, so it starts on ${saved.lock_next_from}.` : 'Saved.';
  });
  const saveBedtime = () => act(async () => {
    const saved = await saveLock({ bedtime });
    return saved.bedtime_next_from ? `Saved. It asks less, so it starts on ${saved.bedtime_next_from}.` : 'Saved.';
  });
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

<section class="lock" aria-labelledby="lock-heading">
  <h3 id="lock-heading">Settings lock</h3>
  <p class="muted small">
    {#if !data}Loading…
    {:else if data.state.unlocked_until}Unlocked by the PIN until {time(data.state.unlocked_until)}.
    {:else if data.state.locked}Locked{data.state.until ? ` until ${time(data.state.until)}` : ''}: nothing can loosen blocking.
    {:else if data.lock.enabled}On, not holding now.{:else}Off.{/if}
    {#if data?.lock.enabled} PIN {data.state.pin_open ? 'accepted now' : `accepted ${formatWindows(data.lock.pin_windows) || 'any time'}`}.{/if}
  </p>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  {#if notice}<p class="muted small" role="status">{notice}</p>{/if}
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

  <h4>Bedtime</h4>
  <form class="row" on:submit|preventDefault={saveBedtime}>
    <label><input type="checkbox" bind:checked={bedtime.enabled} /> On</label>
    <label>From <input type="time" bind:value={bedtime.start} /></label>
    <label>to <input type="time" bind:value={bedtime.end} /></label>
    <button disabled={busy}>Save</button>
    <span class="muted small">Screens lock when used; nothing is closed.</span>
  </form>
</section>

<style>
  .lock { border-top: 1px solid var(--line); margin-top: 16px; padding-top: 10px; }
  h3, h4 { margin: 6px 0; }
  .row { align-items: center; display: flex; flex-wrap: wrap; gap: 8px; margin: 6px 0; font-size: .85rem; }
  .row input[type='number'] { width: 4.5em; }
  .row input[type='password'] { width: 8em; }
  .times { width: 14em; }
  .muted { color: var(--muted); }
  .small { font-size: .78rem; }
</style>
