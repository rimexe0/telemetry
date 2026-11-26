<script lang="ts">
  import { onDestroy } from 'svelte';
  import { APIError, getDeviceNow, type DeviceNow } from './api';
  import { deriveNowView, type NowAction, type NowSnapshot, type NowView } from './execution';
  import { incidentText, trackingView } from './today';
  import ExecutionNow from './ExecutionNow.svelte';
  import TodayBudget from './TodayBudget.svelte';

  // Today: the day's habits, the distraction time left, what is happening
  // right now, and any open collection problem in one quiet line. No plan
  // section, and no follow-ups (owner 2026-11-26).
  export let snapshot: NowSnapshot | undefined;
  export let loading = false;
  export let error = '';
  export let onaction: (view: NowView, action: NowAction) => Promise<void> = async () => {};
  export let onopenplan: () => void = () => {};
  export let onrefresh: () => void = () => {};

  let deviceNow: DeviceNow | null = null;
  let nowError = '';
  let unavailable = false;
  let now = Date.now();

  async function load() {
    try {
      deviceNow = await getDeviceNow();
      nowError = '';
      unavailable = false;
    } catch (cause) {
      unavailable = cause instanceof APIError && cause.status === 404;
      nowError = unavailable ? '' : cause instanceof Error ? cause.message : 'Unable to read what is happening now.';
    } finally {
      now = Date.now();
    }
  }
  void load();
  const reload = setInterval(() => { if (document.visibilityState === 'visible') void load(); }, 30_000);
  const minute = setInterval(() => { now = Date.now(); }, 30_000);
  onDestroy(() => { clearInterval(reload); clearInterval(minute); });

  $: tracking = trackingView(deviceNow?.tracking);
  $: incidents = (deviceNow?.collection_incidents ?? []).filter(incident => !incident.ended_at);
  $: execution = snapshot ? deriveNowView(snapshot, new Date(now)) : undefined;
  $: showExecution = !!execution && execution.kind !== 'plan_your_day';
  $: planGate = execution?.kind === 'plan_your_day' && execution.planGateEnabled;
</script>

<TodayBudget />

<section class="card right-now" aria-labelledby="right-now-label">
  <div class="card-head">
    <h2 id="right-now-label" class="card-label">Right now</h2>
    {#if tracking?.since}<span class="muted small">since {tracking.since}</span>{/if}
  </div>
  {#if tracking}
    <p class="line">{tracking.line}</p>
    {#each tracking.notes as note}<p class="small note">{note}</p>{/each}
  {:else if unavailable}
    <p class="muted small">This server does not say what is on the Mac yet.</p>
  {:else if nowError}
    <p class="muted small">Unknown: {nowError}</p>
  {:else if !deviceNow}
    <p class="muted small">Loading…</p>
  {:else}
    <p class="muted small">Unknown: nothing was read from the Mac.</p>
  {/if}
  {#if showExecution}
    <ExecutionNow {snapshot} {loading} {error} {onaction} {onopenplan} {onrefresh} />
  {:else if planGate}
    <p class="muted small">Blocked apps stay closed until today has a plan. <a href="#activity/plan">Plan</a></p>
  {:else if error && !loading}
    <p class="muted small">The current task is unknown: {error}</p>
  {/if}
</section>

{#if incidents.length}
  <p class="incident small"><a href="#settings/sources">{incidentText(incidents[0])}{incidents.length > 1 ? `, and ${incidents.length - 1} more` : ''} ›</a></p>
{/if}

<style>
  .line { font-size: 1.05rem; margin: 6px 0; overflow-wrap: anywhere; }
  .note { border-left: 3px solid var(--fg); margin: 6px 0; padding-left: 8px; }
  .incident { color: var(--muted); margin: 4px 0 12px; }
  .incident a { align-items: center; color: var(--muted); display: inline-flex; min-height: 44px; text-decoration: none; }
  .incident a:hover, .incident a:focus-visible { color: var(--fg); text-decoration: underline; }
</style>
