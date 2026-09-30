<script lang="ts">
  import DayPicker from './DayPicker.svelte';
  import { decideLabelProposal, getActivityLabels, getActivitySummary, removeActivityLabel, type ActivityLabel, type ActivitySummary, type LabelProposal } from './api';
  import { describeChange, deviceName, formatMinutes } from './activity';

  export let date = '';

  let summary: ActivitySummary | undefined;
  let labels: ActivityLabel[] = [];
  let proposals: LabelProposal[] = [];
  let loading = false;
  let busy = false;
  let error = '';
  let loadedFor: string | undefined;

  $: if (loadedFor !== date) void load();
  $: pending = proposals.filter(item => item.status === 'pending');

  async function load() {
    loadedFor = date;
    loading = true;
    error = '';
    try {
      const [day, current] = await Promise.all([getActivitySummary(date || undefined), getActivityLabels()]);
      summary = day;
      labels = current.labels;
      proposals = current.proposals;
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Unable to load activity.';
    } finally {
      loading = false;
    }
  }

  async function decide(proposal: LabelProposal, accept: boolean) {
    busy = true;
    error = '';
    try {
      await decideLabelProposal(proposal, accept);
      await load();
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Could not save the decision.';
    } finally {
      busy = false;
    }
  }

  async function remove(label: ActivityLabel) {
    busy = true;
    error = '';
    try {
      await removeActivityLabel(label.id);
      await load();
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Could not remove the label.';
    } finally {
      busy = false;
    }
  }
</script>

<section class="activity" aria-labelledby="activity-heading">
  <div class="section-title">
    <h2 id="activity-heading">Where the time went</h2>
    <DayPicker value={date} onchange={(value) => (date = value)} disabled={loading} />
  </div>
  {#if error}<p class="error" role="alert">{error}</p>{/if}

  {#each pending as proposal (proposal.id)}
    <div class="proposal" role="group" aria-label="Label proposal">
      <strong>Label proposal</strong>
      <span class="muted small">{proposal.reason}</span>
      <ul>{#each proposal.changes as change}<li>{describeChange(change)}</li>{/each}</ul>
      <div class="actions">
        <button disabled={busy} on:click={() => decide(proposal, true)}>Accept</button>
        <button class="quiet-button" disabled={busy} on:click={() => decide(proposal, false)}>Reject</button>
      </div>
    </div>
  {/each}

  {#if loading && !summary}<p class="muted">Loading…</p>{/if}
  {#if summary}
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

  <h3>Labels</h3>
  <p class="muted small">Tell your assistant what an app or site is to you; it proposes a label and you accept it here.</p>
  <ul>
    {#each labels as label (label.id)}<li><span class="tag">{label.label}</span> <code>{label.match}</code> <span class="muted small">{label.kind}</span><button class="quiet-button push" disabled={busy} on:click={() => remove(label)}>Remove</button></li>{:else}<li class="muted">No labels yet.</li>{/each}
  </ul>
</section>

<style>
  .activity h3 { margin: 12px 0 4px; font-size: .95rem; }
  .activity h4 { margin: 8px 0 2px; font-size: .8rem; color: var(--muted); }
  ul { list-style: none; margin: 4px 0; padding: 0; }
  li { align-items: center; border-bottom: 1px solid var(--line); display: flex; gap: 8px; font-size: .85rem; padding: 4px 0; }
  code { overflow-wrap: anywhere; }
  .value { margin-left: auto; white-space: nowrap; }
  .push { margin-left: auto; }
  .tag { border: 1px solid var(--line); font-size: .68rem; padding: 0 4px; text-transform: uppercase; }
  .bars li { border: 0; padding: 2px 0; }
  .bars .name { flex: 0 0 7rem; overflow: hidden; text-overflow: ellipsis; }
  .bars .unlabeled { color: var(--muted); }
  .bar { background: var(--text, currentColor); height: 8px; opacity: .55; }
  .proposal { border: 1px solid var(--line); margin: 8px 0; padding: 8px; }
  .proposal ul li { border: 0; padding: 1px 0; }
  .actions { display: flex; gap: 6px; }
  .muted { color: var(--muted); }
  .small { font-size: .78rem; }
</style>
