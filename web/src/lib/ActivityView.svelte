<script lang="ts">
  import DayPicker from './DayPicker.svelte';
  import ActivityLabels from './ActivityLabels.svelte';
  import { getActivitySummary, type ActivitySummary } from './api';
  import { deviceName, formatMinutes, observedLine, observedTop } from './activity';
  import { clockLabel } from './plan';

  export let date = '';
  /** Off when the page shows its own day switcher. */
  export let picker = true;

  let summary: ActivitySummary | undefined;
  let loading = false;
  let error = '';
  let loadedFor: string | undefined;

  $: if (loadedFor !== date) void load();

  async function load() {
    loadedFor = date;
    loading = true;
    error = '';
    try {
      summary = await getActivitySummary(date || undefined);
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Unable to load activity.';
    } finally {
      loading = false;
    }
  }

</script>

<section class="activity" aria-labelledby="activity-heading">
  {#if picker}
    <div class="section-title">
      <h2 id="activity-heading">Where the time went</h2>
      <DayPicker value={date} onchange={(value) => (date = value)} disabled={loading} />
    </div>
  {:else}
    <h2 id="activity-heading" class="sr-only">Where the time went</h2>
  {/if}
  {#if error}<p class="error" role="alert">{error}</p>{/if}

  <ActivityLabels show="proposals" onchange={load} />

  {#if loading && !summary}<p class="muted">Loading…</p>{/if}
  {#if summary}
    {#if (summary.insights ?? []).length}
      <ul class="insights">
        {#each summary.insights ?? [] as insight (insight.key)}<li><strong>{insight.text}</strong>{#if insight.detail}<span class="muted small">{insight.detail}</span>{/if}</li>{/each}
      </ul>
    {/if}
    {#if summary.plan_review}
      <h3>Plan and what happened</h3>
      <ul class="review">
        {#each summary.plan_review.blocks as block (block.id)}
          <li>
            <span class="when">{clockLabel(block.start)}–{clockLabel(block.end)}</span>
            <span class="what"><strong>{block.title}</strong>{#if block.closes_distractions}<span class="tag">closes distractions</span>{/if}{#if block.status !== 'past'}<span class="tag">{block.status === 'upcoming' ? 'upcoming' : 'now'}</span>{/if}
              {#if block.observed}<span class="muted small">{observedTop(block.observed)}{#each block.observed.coding ?? [] as thread}{` · agent on “${thread.title}” ${formatMinutes(thread.agent_minutes)}`}{/each}</span>{/if}
            </span>
            <span class="value">{block.observed ? observedLine(block.observed) : ''}</span>
          </li>
        {:else}<li class="muted">{summary.plan_exists ? 'The plan for this day has no blocks.' : 'No plan for this day.'}</li>{/each}
        <li>
          <span class="when"></span>
          <span class="what"><strong>Outside any block</strong><span class="muted small">{observedTop(summary.plan_review.outside_blocks)}</span></span>
          <span class="value">{observedLine(summary.plan_review.outside_blocks)}</span>
        </li>
      </ul>
    {/if}
    <div class="devices">
      {#each summary.devices as device (device.device)}
        <div class="device">
          <h3>{deviceName(device.device)} <span class="muted small">{`${formatMinutes(device.active_minutes)} active${device.no_data_minutes >= 15 ? ` · no data for ${formatMinutes(device.no_data_minutes)}` : ''}`}</span></h3>
          {#if (device.labels ?? []).length}
            <ul class="bars">
              {#each device.labels ?? [] as item (item.name)}
                <li><span class="name" class:unlabeled={item.name === 'unlabeled'}>{item.name}</span><span class="bar" style={`width:${Math.max(2, (item.minutes / Math.max(device.active_minutes, 1)) * 100)}%`}></span><span class="value">{formatMinutes(item.minutes)}</span></li>
              {/each}
            </ul>
          {/if}
          <h4>Apps</h4>
          <ul>{#each device.apps ?? [] as item (item.name)}<li><code>{item.name}</code>{#if item.label && item.label !== 'unlabeled'}<span class="tag">{item.label}</span>{/if}<span class="value">{formatMinutes(item.minutes)}</span></li>{:else}<li class="muted">Nothing recorded.</li>{/each}</ul>
          {#if (device.sites ?? []).length}
            <h4>Sites</h4>
            <ul>{#each device.sites ?? [] as item (item.name)}<li><code>{item.name}</code>{#if item.label && item.label !== 'unlabeled'}<span class="tag">{item.label}</span>{/if}<span class="value">{formatMinutes(item.minutes)}</span></li>{/each}</ul>
          {/if}
        </div>
      {/each}
    </div>
  {/if}

  <p class="muted small">Labels are kept in Settings, under What counts as a distraction.</p>
</section>

<style>
  .activity h3 { margin: 12px 0 4px; font-size: .95rem; }
  .activity h4 { margin: 8px 0 2px; font-size: .8rem; color: var(--muted); }
  ul { list-style: none; margin: 4px 0; padding: 0; }
  li { align-items: center; border-bottom: 1px solid var(--line); display: flex; gap: 8px; font-size: .85rem; padding: 4px 0; }
  code { overflow-wrap: anywhere; }
  .value { margin-left: auto; white-space: nowrap; }
  .tag { border: 1px solid var(--line); font-size: .68rem; padding: 0 4px; text-transform: uppercase; }
  .bars li { border: 0; padding: 2px 0; }
  .bars .name { flex: 0 0 7rem; overflow: hidden; text-overflow: ellipsis; }
  .bars .unlabeled { color: var(--muted); }
  .bar { background: var(--text, currentColor); height: 8px; opacity: .55; }
  .insights li { align-items: baseline; flex-wrap: wrap; }
  .review li { flex-wrap: wrap; }
  .review .value { white-space: normal; }
  .review .when { flex: 0 0 6.2rem; white-space: nowrap; }
  .review .what { min-width: 0; }
  .review .what .tag { margin-left: 6px; }
  .review .what .small { display: block; }
  .muted { color: var(--muted); }
  .small { font-size: .78rem; }
  .sr-only { height: 1px; margin: -1px; overflow: hidden; position: absolute; width: 1px; }
</style>
