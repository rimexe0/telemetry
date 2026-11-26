<script lang="ts">
  import { onDestroy } from 'svelte';
  import { APIError, followUpDone, getDeviceNow, type DeviceNow, type FollowUp } from './api';
  import { deriveNowView, type NowAction, type NowSnapshot, type NowView } from './execution';
  import { clock, incidentText, todaysFollowUps, trackingView } from './today';
  import ExecutionNow from './ExecutionNow.svelte';
  import TodayBudget from './TodayBudget.svelte';

  // Today: due follow-ups (only when there are some), the distraction time
  // left, the day's habits, what is happening right now, and any open
  // collection problem in one quiet line. No plan section.
  export let snapshot: NowSnapshot | undefined;
  export let loading = false;
  export let error = '';
  export let onaction: (view: NowView, action: NowAction) => Promise<void> = async () => {};
  export let onopenplan: () => void = () => {};
  export let onrefresh: () => void = () => {};

  let deviceNow: DeviceNow | null = null;
  let nowError = '';
  let unavailable = false;
  let doneBusy = 0;
  let doneError = '';
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

  async function done(item: FollowUp) {
    doneBusy = item.id;
    doneError = '';
    try {
      await followUpDone(item.id);
      if (deviceNow) deviceNow = { ...deviceNow, follow_ups: deviceNow.follow_ups.filter(row => row.id !== item.id) };
      void load();
    } catch (cause) {
      doneError = cause instanceof Error ? cause.message : 'It was not marked done.';
    } finally {
      doneBusy = 0;
    }
  }

  $: followUps = todaysFollowUps(deviceNow?.follow_ups ?? [], now);
  $: tracking = trackingView(deviceNow?.tracking);
  $: incidents = (deviceNow?.collection_incidents ?? []).filter(incident => !incident.ended_at);
  $: execution = snapshot ? deriveNowView(snapshot, new Date(now)) : undefined;
  $: showExecution = !!execution && execution.kind !== 'plan_your_day';
  $: planGate = execution?.kind === 'plan_your_day' && execution.planGateEnabled;
</script>

{#if followUps.due.length || followUps.later.length}
  <section class="card follow-ups" aria-labelledby="follow-ups-label">
    <div class="card-head">
      <h2 id="follow-ups-label" class="card-label">Follow-ups</h2>
      {#if followUps.due.length}<span class="muted small">{followUps.due.length} due</span>{/if}
    </div>
    <ul>
      {#each [...followUps.due, ...followUps.later] as item (item.id)}
        {@const due = followUps.due.includes(item)}
        <li class:due>
          <span class="when small" class:muted={!due}>{clock(item.due_at)}</span>
          <span class="title">{item.title}</span>
          <button class="quiet-button" disabled={doneBusy !== 0} on:click={() => done(item)}>{doneBusy === item.id ? '…' : 'Done'}</button>
        </li>
      {/each}
    </ul>
    {#if doneError}<p class="error" role="alert">{doneError}</p>{/if}
  </section>
{/if}

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
  .follow-ups ul { list-style: none; margin: 4px 0 0; padding: 0; }
  .follow-ups li { align-items: center; border-bottom: 1px solid var(--line); display: flex; gap: 10px; min-height: 48px; }
  .follow-ups li:last-child { border-bottom: 0; }
  .follow-ups .when { flex: 0 0 3.2em; }
  .follow-ups .title { flex: 1; min-width: 0; overflow-wrap: anywhere; }
  .follow-ups li.due .title { font-weight: 700; }
  .follow-ups button { min-height: 40px; min-width: 64px; }
  .line { font-size: 1.05rem; margin: 6px 0; overflow-wrap: anywhere; }
  .note { border-left: 3px solid var(--fg); margin: 6px 0; padding-left: 8px; }
  .incident { color: var(--muted); margin: 4px 0 12px; }
  .incident a { align-items: center; color: var(--muted); display: inline-flex; min-height: 44px; text-decoration: none; }
  .incident a:hover, .incident a:focus-visible { color: var(--fg); text-decoration: underline; }
</style>
