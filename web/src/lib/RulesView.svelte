<script lang="ts">
  import { onDestroy } from 'svelte';
  import { getLock, unlockWithPin, type Activity, type LockResponse, type PlanningSettings } from './api';
  import { formatWindows } from './lock';
  import BlockedApps from './BlockedApps.svelte';
  import ContextRules from './ContextRules.svelte';
  import MorningCode from './MorningCode.svelte';
  import SettingsLock from './SettingsLock.svelte';

  export let activities: Activity[] = [];
  export let planningSettings: PlanningSettings | undefined;
  export let onupdatesettings: (enabled: boolean) => Promise<void> = async () => {};
  export let onleave: () => void = () => {};

  // While the settings lock holds, the rules are not shown at all: only the
  // PIN, entered by the person who holds it (owner request 2026-10-31).
  let lockState: LockResponse | null = null;
  let pin = '';
  let pinBusy = false;
  let pinError = '';
  async function loadLock() {
    try { lockState = await getLock(); } catch { lockState = null; }
  }
  void loadLock();
  const refresh = setInterval(loadLock, 30_000);
  onDestroy(() => clearInterval(refresh));
  $: locked = !!lockState?.lock.enabled && !!lockState?.state.locked;
  $: unlockedUntil = lockState?.state.unlocked_until;
  const clock = (at?: string) => (at ? new Date(at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: false }) : '');
  async function unlock() {
    pinBusy = true;
    pinError = '';
    try {
      await unlockWithPin(pin);
      pin = '';
      await loadLock();
    } catch (cause) {
      pinError = cause instanceof Error ? cause.message.replace(/^blocking settings are locked: /, '') : 'The PIN was not accepted.';
      await loadLock();
    } finally {
      pinBusy = false;
    }
  }

  let gateBusy = false;
  let gateError = '';

  const sections = [
    { id: 'rule-no-plan', label: 'No plan' }, { id: 'rule-lock', label: 'Settings lock' }, { id: 'rule-bedtime', label: 'Bedtime' },
    { id: 'rule-extension', label: 'Browser extension' }, { id: 'rule-morning', label: 'Morning code' }, { id: 'rule-apps', label: 'Blocked apps' },
    { id: 'rule-projects', label: 'Project rules' }
  ];

  async function updateGate(enabled: boolean) {
    gateBusy = true;
    gateError = '';
    try { await onupdatesettings(enabled); }
    catch (cause) { gateError = cause instanceof Error ? cause.message : 'Saving failed.'; }
    finally { gateBusy = false; }
  }

  const jump = (id: string) => document.getElementById(id)?.scrollIntoView({ behavior: 'smooth', block: 'start' });
</script>

{#if locked && lockState}
  <div class="pin-gate" role="dialog" aria-modal="true" aria-labelledby="pin-heading">
    <form class="pin-card" on:submit|preventDefault={unlock}>
      <h2 id="pin-heading">Rules are locked</h2>
      <p class="muted">
        {lockState.state.until ? `Locked until ${clock(lockState.state.until)}.` : 'Locked at all times.'}
        The person who holds the PIN enters it here; it opens the rules for {lockState.lock.unlock_minutes} minutes, and changes made then apply at once.
      </p>
      {#if !lockState.state.pin_open}
        <p class="muted">The PIN is accepted only at {formatWindows(lockState.lock.pin_windows)}.</p>
      {/if}
      <!-- svelte-ignore a11y_autofocus -->
      <input class="pin" type="password" inputmode="numeric" autocomplete="off" aria-label="PIN" autofocus
        disabled={pinBusy || !lockState.state.pin_open || lockState.state.tries_left === 0} bind:value={pin} />
      <button disabled={pinBusy || !pin || !lockState.state.pin_open || lockState.state.tries_left === 0}>{pinBusy ? 'Checking…' : 'Unlock'}</button>
      <p class="muted small">{lockState.state.tries_left} {lockState.state.tries_left === 1 ? 'try' : 'tries'} left this hour</p>
      {#if pinError}<p class="error" role="alert">{pinError}</p>{/if}
      <button type="button" class="quiet-button" on:click={onleave}>Back</button>
    </form>
  </div>
{:else}
<div class="rules">
  {#if unlockedUntil}<p class="unlocked" role="status">Unlocked with the PIN until {clock(unlockedUntil)}. Changes made now apply at once.</p>{/if}
  <nav class="jump" aria-label="Rules sections">
    {#each sections as section}<button class="quiet-button" on:click={() => jump(section.id)}>{section.label}</button>{/each}
  </nav>
  <p class="muted small intro">What is blocked and when. Distraction times and openings are set on the Now page and through the assistant.</p>

  <div class="grid">
    <div class="card" id="rule-no-plan">
      <section aria-labelledby="no-plan-heading">
        <h3 id="no-plan-heading">When the day has no plan</h3>
        <label class="check"><input type="checkbox" checked={planningSettings?.daily_plan_gate_enabled ?? false} disabled={gateBusy || !planningSettings}
          on:change={(event) => updateGate(event.currentTarget.checked)} /> Keep blocked apps closed until I plan the day</label>
        <p class="muted small">Off: a day without a plan still keeps distractions closed outside distraction times, but nothing else waits for the plan.</p>
        {#if gateError}<p class="error" role="alert">{gateError}</p>{/if}
      </section>
    </div>
    <div class="card" id="rule-lock"><SettingsLock part="lock" /></div>
    <div class="card" id="rule-bedtime"><SettingsLock part="bedtime" /></div>
    <div class="card" id="rule-extension"><SettingsLock part="extension" /></div>
    <div class="card" id="rule-morning"><MorningCode /></div>
    <div class="card wide" id="rule-apps"><BlockedApps /></div>
    <div class="card wide" id="rule-projects"><ContextRules {activities} /></div>
  </div>
</div>
{/if}

<style>
  .rules { margin-top: 8px; }
  .jump { display: flex; flex-wrap: wrap; gap: 6px; }
  .jump button { font-size: .75rem; padding: 4px 9px; }
  .intro { margin: 8px 0 12px; }
  .grid { display: grid; gap: 12px; grid-template-columns: minmax(0, 1fr); }
  @media (min-width: 980px) { .grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } .wide { grid-column: 1 / -1; } }
  .card { background: var(--panel); border: 1px solid var(--line); padding: 12px 14px; scroll-margin-top: 12px; }
  .card :global(> section) { border: 0; margin: 0; padding: 0; }
  .card :global(h3) { font-size: 1rem; margin: 0 0 4px; }
  .check { align-items: center; display: flex; font-size: .85rem; gap: 8px; margin: 8px 0 4px; }
  .pin-gate { align-items: center; background: var(--bg); display: flex; inset: 0; justify-content: center; padding: 20px; position: fixed; z-index: 40; }
  .pin-card { background: var(--panel); border: 1px solid var(--fg); display: flex; flex-direction: column; gap: 12px; max-width: 380px; padding: 24px; width: 100%; }
  .pin-card h2 { margin: 0; }
  .pin-card p { margin: 0; }
  .pin { font-size: 1.6rem; letter-spacing: .4em; min-height: 52px; text-align: center; }
  .unlocked { border-left: 3px solid var(--fg); font-size: .85rem; margin: 10px 0 0; padding: 4px 10px; }
  .muted { color: var(--muted); }
  .small { font-size: .78rem; }
</style>
