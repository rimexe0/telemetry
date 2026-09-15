<script lang="ts">
  import type { Activity, Plan, PlanBlock, PlanInput } from './api';
  import { clockLabel, fixedOverlaps, planTotals, sortBlocks, stateLabel, statusLabel, activityStatuses, toBlockLocalTime } from './plan';

  export let day: string;
  export let activities: Activity[] = [];
  export let plan: Plan | null = null;
  export let activitiesLoading = false;
  export let planLoading = false;
  export let activitiesError = '';
  export let planError = '';

  export let onrefresh: () => void = () => {};
  export let onsave: (input: PlanInput) => Promise<void> = async () => {};
  export let onsetstate: (blockId: number, state: PlanBlock['state'], note: string) => Promise<void> = async () => {};
  export let oncreateactivity: (input: { title: string; next_action: string; effort_estimate_minutes?: number }) => Promise<void> = async () => {};
  export let onupdateactivity: (activity: Activity) => Promise<void> = async () => {};

  type Draft = { key: number; activityId?: number; title: string; hhmm: string; duration: number; fixed: boolean };
  let editing = false;
  let draft: Draft[] = [];
  let noteFor = 0;
  let noteText = '';
  let saving = false;
  let saveError = '';
  let newTitle = '';
  let newAction = '';
  let newEffort = '';
  let activityBusy = false;
  let activityError = '';

  const toDraft = (block: PlanBlock): Draft => ({
    key: block.id,
    activityId: block.activity_id,
    title: block.activity_id ? '' : block.title,
    hhmm: clockLabel(block.start_time),
    duration: block.duration_minutes,
    fixed: block.fixed
  });

  const startEditing = () => {
    draft = (plan?.blocks ?? []).map(toDraft);
    saveError = '';
    editing = true;
  };

  const addDraft = () => {
    const last = draft[draft.length - 1];
    const hhmm = last
      ? toBlockLocalTime(new Date(`${day}T${last.hhmm}:00+03:00`).getTime() + last.duration * 60_000).toISOString().slice(11, 16)
      : '09:00';
    draft = [...draft, { key: Date.now(), activityId: undefined, title: '', hhmm, duration: 30, fixed: false }];
  };

  const draftsValid = () => draft.every(row => /^([01]\d|2[0-3]):[0-5]\d$/.test(row.hhmm) && row.duration >= 1 && (row.activityId || row.title.trim()));

  const draftInput = (): PlanInput => ({
    note: plan?.note ?? '',
    blocks: draft.map(row => ({
      activity_id: row.activityId,
      title: row.activityId ? undefined : row.title.trim(),
      start_time: `${day}T${row.hhmm}:00+03:00`,
      duration_minutes: row.duration,
      fixed: row.fixed
    }))
  });

  const save = async () => {
    saving = true;
    saveError = '';
    try {
      await onsave(draftInput());
      editing = false;
    } catch (cause) {
      saveError = cause instanceof Error ? cause.message : 'Saving the plan failed.';
    } finally {
      saving = false;
    }
  };

  const askNote = (blockId: number) => { noteFor = blockId; noteText = ''; };
  const settle = async (block: PlanBlock, state: PlanBlock['state']) => {
    if (state === 'skipped' || state === 'switched') { askNote(block.id); return; }
    await onsetstate(block.id, state, '');
  };
  const confirmNote = async (state: PlanBlock['state']) => {
    const blockId = noteFor;
    noteFor = 0;
    await onsetstate(blockId, state, noteText.trim());
  };

  const addActivity = async () => {
    if (!newTitle.trim()) return;
    activityBusy = true;
    activityError = '';
    const effort = Number.parseInt(newEffort, 10);
    try {
      await oncreateactivity({ title: newTitle.trim(), next_action: newAction.trim(), ...(Number.isFinite(effort) && effort > 0 ? { effort_estimate_minutes: effort } : {}) });
      newTitle = ''; newAction = ''; newEffort = '';
    } catch (cause) {
      activityError = cause instanceof Error ? cause.message : 'Creating the activity failed.';
    } finally {
      activityBusy = false;
    }
  };

  const setStatus = async (activity: Activity, status: Activity['status']) => {
    activityBusy = true;
    activityError = '';
    try { await onupdateactivity({ ...activity, status }); } finally { activityBusy = false; }
  };

  $: sorted = sortBlocks(plan?.blocks ?? []);
  $: totals = planTotals(sorted);
  $: overlaps = editing ? fixedOverlaps(draft.map((row, index) => ({
    id: index, title: row.title || `#${row.activityId}`, start_time: `${day}T${row.hhmm}:00+03:00`,
    duration_minutes: row.duration, fixed: row.fixed, state: 'planned' as const, state_note: '', position: index
  }))) : [];
  $: activityById = new Map(activities.map(activity => [activity.id, activity]));
</script>

<section class="plan-view" aria-labelledby="plan-heading">
  <div class="section-head">
    <div><h2 id="plan-heading">Day plan</h2><p class="muted">{day} · {totals.totalBlocks} block{totals.totalBlocks === 1 ? '' : 's'}</p></div>
    <div class="head-actions">
      {#if editing}
        <button class="quiet-button" on:click={() => { editing = false; }} disabled={saving}>Cancel</button>
        <button class="quiet-button primary" on:click={save} disabled={saving || !draftsValid() || overlaps.length > 0}>{saving ? 'Saving…' : 'Save as new version'}</button>
      {:else}
        <button class="quiet-button" on:click={onrefresh} disabled={planLoading}>{planLoading ? 'Refreshing…' : 'Refresh'}</button>
        <button class="quiet-button primary" on:click={startEditing}>Edit plan</button>
      {/if}
    </div>
  </div>

  {#if planError}<p class="error" role="alert">{planError}</p>{/if}
  {#if saveError}<p class="error" role="alert">{saveError}</p>{/if}

  {#if !editing && plan}
    <p class="summary">
      Version {plan.version_id} saved {clockLabel(plan.created_at)} (Istanbul) ·
      <strong>{totals.doneBlocks}</strong>/{totals.totalBlocks} done ·
      {totals.doneMinutes}m done, {totals.plannedMinutes}m remaining
      {#if totals.otherMinutes}, {totals.otherMinutes}m skipped/switched{/if}
    </p>
    <ol class="blocks">
      {#each sorted as block (block.id)}
        <li class="block state-{block.state}">
          <div class="block-line">
            <span class="time">{clockLabel(block.start_time)}–{clockLabel(new Date(new Date(block.start_time).getTime() + block.duration_minutes * 60_000).toISOString())}</span>
            <strong>{block.title}</strong>
            {#if block.fixed}<span class="tag">fixed</span>{/if}
            <span class="tag state">{stateLabel[block.state]}</span>
          </div>
          {#if block.state_note}<p class="note">“{block.state_note}”</p>{/if}
          {#if noteFor === block.id}
            <div class="note-editor">
              <input type="text" placeholder="What happened? (kept with the plan)" bind:value={noteText} />
              <button class="quiet-button" on:click={() => noteFor = 0}>Cancel</button>
              <button class="quiet-button primary" on:click={() => confirmNote(block.state === 'skipped' ? 'skipped' : 'switched')}>Record</button>
            </div>
          {:else}
            <div class="state-buttons">
              {#if block.state !== 'done'}<button class="quiet-button" on:click={() => settle(block, 'done')}>Done</button>{/if}
              {#if block.state !== 'active'}<button class="quiet-button" on:click={() => settle(block, 'active')}>Started</button>{/if}
              {#if block.state !== 'skipped'}<button class="quiet-button" on:click={() => settle(block, 'skipped')}>Skipped</button>{/if}
              {#if block.state !== 'switched'}<button class="quiet-button" on:click={() => settle(block, 'switched')}>Switched</button>{/if}
            </div>
          {/if}
        </li>
      {/each}
    </ol>
  {:else if !editing}
    <p class="empty">{planLoading ? 'Loading the plan…' : 'Nothing planned for this day yet. Use “Edit plan” to start.'}</p>
  {/if}

  {#if editing}
    <p class="muted hint">Times are Istanbul local. Saving keeps the previous version; nothing is overwritten.</p>
    {#if overlaps.length > 0}<p class="error" role="alert">Two fixed blocks overlap. Adjust their times or mark one as movable.</p>{/if}
    <div class="draft-rows">
      {#each draft as row, index (row.key)}
        <div class="draft-row">
          <input class="time" type="time" bind:value={row.hhmm} />
          <select bind:value={row.activityId}>
            <option value={undefined}>One-off block</option>
            {#each activities.filter(a => a.status !== 'abandoned') as activity (activity.id)}
              <option value={activity.id}>{activity.title}</option>
            {/each}
          </select>
          {#if !row.activityId}<input class="title" type="text" placeholder="What is this block?" bind:value={row.title} />{/if}
          <input class="duration" type="number" min="1" max="1440" bind:value={row.duration} title="Minutes" />
          <label class="fixed"><input type="checkbox" bind:checked={row.fixed} /> fixed</label>
          <button class="quiet-button" on:click={() => draft = draft.filter((_, i) => i !== index)} aria-label="Remove block">✕</button>
        </div>
      {/each}
    </div>
    <button class="quiet-button" on:click={addDraft}>Add block</button>
  {/if}

  <div class="section-head activities-head">
    <div><h3>Activities</h3><p class="muted">Owned by you; nothing is pre-seeded.</p></div>
    <button class="quiet-button" on:click={onrefresh} disabled={activitiesLoading}>{activitiesLoading ? 'Refreshing…' : 'Refresh'}</button>
  </div>
  {#if activitiesError}<p class="error" role="alert">{activitiesError}</p>{/if}
  <ul class="activity-list">
    {#each activities as activity (activity.id)}
      <li>
        <div class="activity-line">
          <strong>{activity.title}</strong>
          <select disabled={activityBusy} value={activity.status} on:change={(event) => setStatus(activity, event.currentTarget.value as Activity['status'])} aria-label="Status for {activity.title}">
            {#each activityStatuses as status}<option value={status}>{statusLabel[status]}</option>{/each}
          </select>
        </div>
        {#if activity.next_action}<p class="muted next">Next: {activity.next_action}</p>{/if}
        {#if activity.effort_estimate_minutes}<p class="muted next">Estimate: {activity.effort_estimate_minutes}m</p>{/if}
      </li>
    {:else}
      <p class="empty">{activitiesLoading ? 'Loading activities…' : 'No activities yet. Add the first one below.'}</p>
    {/each}
  </ul>
  <form class="new-activity" on:submit|preventDefault={addActivity}>
    <input type="text" placeholder="Activity title" bind:value={newTitle} required />
    <input type="text" placeholder="Next action (optional)" bind:value={newAction} />
    <input class="effort" type="number" min="1" max="10080" placeholder="Est. minutes" bind:value={newEffort} />
    <button class="quiet-button primary" type="submit" disabled={activityBusy || !newTitle.trim()}>Add activity</button>
  </form>
  {#if activityError}<p class="error" role="alert">{activityError}</p>{/if}
</section>

<style>
  .plan-view { border-top: 1px solid var(--line); margin-top: 20px; padding-top: 12px; }
  .section-head { align-items: center; display: flex; flex-wrap: wrap; gap: 8px; justify-content: space-between; }
  .section-head h2, .section-head h3 { margin: 0; }
  .section-head p { margin: 2px 0 0; }
  .head-actions { display: flex; gap: 6px; }
  .primary { border: 1px solid var(--fg); }
  .summary { color: var(--muted); font-size: .85rem; }
  .blocks { list-style: none; margin: 8px 0; padding: 0; }
  .block { border-left: 3px solid var(--line); margin: 6px 0; padding: 4px 8px; }
  .block.state-done { border-left-color: var(--fg); }
  .block.state-skipped, .block.state-switched { opacity: .65; }
  .block.state-active { border-left-color: var(--fg); background: var(--soft); }
  .block-line { align-items: baseline; display: flex; flex-wrap: wrap; gap: 8px; }
  .time { color: var(--muted); font-variant-numeric: tabular-nums; }
  .tag { border: 1px solid var(--line); font-size: .7rem; padding: 0 5px; text-transform: uppercase; }
  .note { color: var(--muted); font-size: .8rem; margin: 4px 0 0; }
  .state-buttons, .note-editor { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 6px; }
  .note-editor input { flex: 1; min-width: 200px; }
  .hint { font-size: .8rem; }
  .draft-rows { display: flex; flex-direction: column; gap: 6px; margin: 10px 0; }
  .draft-row { align-items: center; display: flex; flex-wrap: wrap; gap: 6px; }
  .draft-row .time { width: 108px; }
  .draft-row select { max-width: 220px; }
  .draft-row .title { flex: 1; min-width: 140px; }
  .draft-row .duration { width: 76px; }
  .fixed { color: var(--muted); font-size: .8rem; }
  .activities-head { margin-top: 18px; }
  .activity-list { list-style: none; margin: 6px 0; padding: 0; }
  .activity-list li { border-bottom: 1px solid var(--line); padding: 6px 0; }
  .activity-line { align-items: center; display: flex; gap: 10px; justify-content: space-between; }
  .next { margin: 2px 0 0; }
  .new-activity { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 10px; }
  .new-activity input { flex: 1; min-width: 140px; }
  .new-activity .effort { flex: 0; width: 110px; }
</style>
