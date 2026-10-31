<script lang="ts">
  import type { Activity, AlignmentView, Plan, PlanBlock, PlanInput, PlanVersionSummary } from './api';
  import type { PlanAssessment } from './planActual';
  import ReplanReview from './ReplanReview.svelte';
  import TaskFields from './TaskFields.svelte';
  import {
    activityStates, activityStateLabel, activityStatuses, blockEnd, blockNow, clockLabel, dayLabel, fixedOverlaps, planTotals,
    primaryAction, shiftDay, sortBlocks, stateLabel, statusLabel, toBlockLocalTime
  } from './plan';

  export let day: string;
  export let activities: Activity[] = [];
  export let plan: Plan | null = null;
  export let activitiesLoading = false;
  export let planLoading = false;
  export let activitiesError = '';
  export let planError = '';
  export let planVersions: PlanVersionSummary[] = [];
  export let assessments: Record<number, PlanAssessment> = {};
  export let planEvidenceLoading = false;
  export let planEvidenceError = '';
  export let currentAlignment: AlignmentView | undefined;
  export let currentAlignmentError = '';
  export let today = '';

  export let onrefresh: () => void = () => {};
  export let onchangeday: (day: string) => void = () => {};
  export let onsave: (input: PlanInput) => Promise<void> = async () => {};
  export let onsetstate: (blockId: number, state: PlanBlock['state'], note: string, explanationCode?: string, action?: 'state' | 'explanation' | 'return') => Promise<void> = async () => {};
  export let oncreateactivity: (input: { title: string; notes?: string; next_action: string; resume_marker?: string; status?: Activity['status']; activity_state?: Activity['activity_state']; priority?: number; deadline?: string; effort_estimate_minutes?: number }) => Promise<void> = async () => {};
  export let onupdateactivity: (activity: Activity) => Promise<void> = async () => {};

  type Draft = { key: number; activityId?: number; title: string; hhmm: string; duration: number; fixed: boolean; focus: boolean };
  type TaskDraft = { title: string; notes: string; next_action: string; resume_marker: string; status: Activity['status']; activity_state: Activity['activity_state']; priority: number; deadline: string; effort: string };

  const emptyTask = (): TaskDraft => ({ title: '', notes: '', next_action: '', resume_marker: '', status: 'active', activity_state: 'growth', priority: 0, deadline: '', effort: '' });

  let editing = false;
  let draft: Draft[] = [];
  let planNote = '';
  let saving = false;
  let saveError = '';
  let replanOpen = false;

  let noteFor = 0;
  let noteText = '';
  let explanationCode = 'other';
  let explanationState: PlanBlock['state'] = 'switched';
  let stateBusy = false;
  let stateError = '';

  let addingTask = false;
  let newTask = emptyTask();
  let editingTaskId = 0;
  let taskDraft: TaskDraft | undefined;
  let taskBusy = false;
  let taskError = '';
  let showFinished = false;

  const finishedStatuses: Activity['status'][] = ['done', 'completed', 'abandoned'];

  // ---- day plan ----
  const toDraft = (block: PlanBlock): Draft => ({
    key: block.id, activityId: block.activity_id, title: block.activity_id ? '' : block.title,
    hhmm: clockLabel(block.start_time), duration: block.duration_minutes, fixed: block.fixed, focus: block.focus ?? false
  });

  const startEditing = () => {
    draft = (plan?.blocks ?? []).map(toDraft);
    if (!draft.length) addDraft();
    planNote = plan?.note ?? '';
    saveError = '';
    editing = true;
  };

  function addDraft() {
    const last = draft[draft.length - 1];
    const hhmm = last
      ? toBlockLocalTime(new Date(`${day}T${last.hhmm}:00+03:00`).getTime() + last.duration * 60_000).toISOString().slice(11, 16)
      : '09:00';
    draft = [...draft, { key: Date.now(), activityId: undefined, title: '', hhmm, duration: 30, fixed: false, focus: false }];
  }

  const draftsValid = () => draft.every(row => /^([01]\d|2[0-3]):[0-5]\d$/.test(row.hhmm) && row.duration >= 1 && (row.activityId || row.title.trim()));

  const save = async () => {
    saving = true;
    saveError = '';
    try {
      await onsave({
        note: planNote.trim(),
        blocks: draft.map(row => ({
          activity_id: row.activityId, title: row.activityId ? undefined : row.title.trim(),
          start_time: `${day}T${row.hhmm}:00+03:00`, duration_minutes: row.duration, fixed: row.fixed, focus: row.focus
        }))
      });
      editing = false;
    } catch (cause) {
      saveError = cause instanceof Error ? cause.message : 'Saving the plan failed.';
    } finally {
      saving = false;
    }
  };

  // ---- block states ----
  const askNote = (block: PlanBlock, state: PlanBlock['state'] = 'switched') => {
    noteFor = block.id;
    noteText = '';
    explanationCode = 'other';
    explanationState = state;
  };
  const applyBlockState = async (blockId: number, state: PlanBlock['state'], note = '', code = '', action: 'state' | 'explanation' | 'return' = 'state'): Promise<boolean> => {
    stateBusy = true;
    stateError = '';
    try { await onsetstate(blockId, state, note, code, action); return true; }
    catch (cause) { stateError = cause instanceof Error ? cause.message : 'Saving the block state failed.'; return false; }
    finally { stateBusy = false; }
  };
  const settle = async (block: PlanBlock, state: PlanBlock['state']) => {
    if (state === 'skipped' || state === 'switched' || state === 'blocked' || state === 'unknown') { askNote(block, state); return; }
    await applyBlockState(block.id, state);
  };
  const runPrimary = async (block: PlanBlock) => {
    const action = primaryAction(block.state);
    if (!action) return;
    if (action.ret) await applyBlockState(block.id, 'active', '', '', 'return');
    else await settle(block, action.state);
  };
  const confirmNote = async () => {
    if (await applyBlockState(noteFor, explanationState, noteText.trim(), explanationCode, 'explanation')) noteFor = 0;
  };
  // The other choices, under "More": everything but the main action and the
  // state the block is already in.
  const moreActions = (block: PlanBlock) => {
    const choices: { state: PlanBlock['state']; label: string; ret?: boolean }[] = [
      { state: 'active', label: 'Started' }, { state: 'done', label: 'Done' }, { state: 'active', label: 'Return to it', ret: true },
      { state: 'skipped', label: 'Skipped' }, { state: 'switched', label: 'Switched' }, { state: 'blocked', label: 'Blocked' }
    ];
    const main = primaryAction(block.state);
    return choices.filter(choice => choice.state !== block.state && !(main && choice.state === main.state && !!choice.ret === !!main.ret));
  };

  // ---- tasks ----
  const toPayload = (task: TaskDraft) => {
    const effort = Number.parseInt(task.effort, 10);
    return {
      title: task.title.trim(), notes: task.notes.trim(), next_action: task.next_action.trim(), resume_marker: task.resume_marker.trim(),
      status: task.status, activity_state: task.activity_state, priority: task.priority,
      deadline: task.deadline ? `${task.deadline}T23:59:59+03:00` : undefined,
      effort_estimate_minutes: Number.isFinite(effort) && effort > 0 ? effort : undefined
    };
  };
  const taskAct = async (work: () => Promise<void>) => {
    taskBusy = true;
    taskError = '';
    try { await work(); }
    catch (cause) { taskError = cause instanceof Error ? cause.message : 'Saving the task failed.'; }
    finally { taskBusy = false; }
  };
  const addTask = () => taskAct(async () => {
    if (!newTask.title.trim()) return;
    await oncreateactivity(toPayload(newTask));
    newTask = emptyTask();
    addingTask = false;
  });
  const setStatus = (activity: Activity, status: Activity['status']) => taskAct(() => onupdateactivity({ ...activity, status }));
  const beginTaskEdit = (activity: Activity) => {
    editingTaskId = activity.id;
    taskDraft = {
      title: activity.title, notes: activity.notes, next_action: activity.next_action, resume_marker: activity.resume_marker,
      status: activity.status, activity_state: activity.activity_state, priority: activity.priority,
      deadline: activity.deadline ? activity.deadline.slice(0, 10) : '', effort: activity.effort_estimate_minutes?.toString() ?? ''
    };
  };
  const saveTaskEdit = (activity: Activity) => taskAct(async () => {
    if (!taskDraft) return;
    await onupdateactivity({ ...activity, ...toPayload(taskDraft) });
    editingTaskId = 0;
    taskDraft = undefined;
  });

  const hours = (minutes: number) => minutes >= 60 ? `${Math.floor(minutes / 60)}h${minutes % 60 ? ` ${minutes % 60}m` : ''}` : `${minutes}m`;

  $: sorted = sortBlocks(plan?.blocks ?? []);
  $: totals = planTotals(sorted);
  $: progress = totals.doneMinutes + totals.plannedMinutes > 0 ? Math.round(100 * totals.doneMinutes / (totals.doneMinutes + totals.plannedMinutes)) : 0;
  $: now = Date.now();
  $: overlaps = editing ? fixedOverlaps(draft.map((row, index) => ({
    id: index, title: row.title || `#${row.activityId}`, start_time: `${day}T${row.hhmm}:00+03:00`,
    duration_minutes: row.duration, fixed: row.fixed, state: 'planned' as const, state_note: '', position: index
  }))) : [];
  $: activityById = new Map(activities.map(activity => [activity.id, activity]));
  $: openTasks = activities.filter(activity => !finishedStatuses.includes(activity.status)).sort((a, b) => b.priority - a.priority);
  $: finishedTasks = activities.filter(activity => finishedStatuses.includes(activity.status));
  $: isToday = day === today;
</script>


<section class="plan-page" aria-labelledby="plan-heading">
  <header class="day-head">
    <div class="day-nav">
      <button class="quiet-button" aria-label="Previous day" on:click={() => onchangeday(shiftDay(day, -1))}>‹</button>
      <h2 id="plan-heading">{dayLabel(day)}</h2>
      <button class="quiet-button" aria-label="Next day" on:click={() => onchangeday(shiftDay(day, 1))}>›</button>
      {#if !isToday && today}<button class="quiet-button" on:click={() => onchangeday(today)}>Today</button>{/if}
    </div>
    <div class="head-actions">
      {#if editing}
        <button class="quiet-button" on:click={() => { editing = false; }} disabled={saving}>Cancel</button>
        <button on:click={save} disabled={saving || !draftsValid() || overlaps.length > 0}>{saving ? 'Saving…' : 'Save plan'}</button>
      {:else}
        {#if plan}<button class="quiet-button" on:click={() => { replanOpen = !replanOpen; }}>Something changed</button>{/if}
        <button class="quiet-button" on:click={onrefresh} disabled={planLoading} aria-label="Refresh">{planLoading ? '…' : '↻'}</button>
        <button on:click={startEditing}>{plan ? 'Edit plan' : 'Plan this day'}</button>
      {/if}
    </div>
  </header>

  {#if plan && !editing}
    <div class="progress" aria-label="Progress">
      <div class="bar"><span style="width:{progress}%"></span></div>
      <p>{totals.doneBlocks} of {totals.totalBlocks} done · {hours(totals.plannedMinutes)} left{#if totals.otherMinutes} · {hours(totals.otherMinutes)} set aside{/if}</p>
    </div>
  {/if}
  {#if isToday && currentAlignment && !editing}
    <p class="alignment state-{currentAlignment.alignment_state}">
      <strong>Now: {currentAlignment.alignment_state.replace('_', ' ')}.</strong> {currentAlignment.reason}
      {#if currentAlignment.next_action}<span class="muted">Next: {currentAlignment.next_action}</span>{/if}
    </p>
  {/if}

  {#each [planError, saveError, stateError] as message}{#if message}<p class="error" role="alert">{message}</p>{/if}{/each}

  <ReplanReview {day} {plan} bind:open={replanOpen} onchanged={onrefresh} />

  <div class="columns">
    <div class="day-col">
      {#if editing}
        <div class="editor">
          {#if overlaps.length > 0}<p class="error" role="alert">Two fixed blocks overlap. Change a time or untick fixed on one.</p>{/if}
          <div class="draft-head" aria-hidden="true"><span>Start</span><span>What</span><span>Min</span><span></span></div>
          {#each draft as row, index (row.key)}
            <div class="draft-row">
              <input class="time" type="time" aria-label="Start" bind:value={row.hhmm} />
              <div class="what">
                <select aria-label="Task" bind:value={row.activityId}>
                  <option value={undefined}>One-off block…</option>
                  {#each activities.filter(a => !finishedStatuses.includes(a.status)) as activity (activity.id)}<option value={activity.id}>{activity.title}</option>{/each}
                </select>
                {#if !row.activityId}<input type="text" placeholder="What is this block?" aria-label="Block title" bind:value={row.title} />{/if}
                <span class="flags">
                  <label title="Keep this time when replanning"><input type="checkbox" bind:checked={row.fixed} /> fixed</label>
                  {#if !row.activityId}<label title="Close distractions during this block"><input type="checkbox" bind:checked={row.focus} /> focus</label>{/if}
                </span>
              </div>
              <input class="minutes" type="number" min="1" max="1440" aria-label="Minutes" bind:value={row.duration} />
              <button class="quiet-button" on:click={() => draft = draft.filter((_, i) => i !== index)} aria-label="Remove block">✕</button>
            </div>
          {/each}
          <button class="quiet-button add" on:click={addDraft}>+ Add block</button>
          <label class="plan-note">Note for this version <input type="text" maxlength="500" placeholder="Optional: why it changed" bind:value={planNote} /></label>
          <p class="muted small">Istanbul time. Saving keeps the earlier version.</p>
        </div>
      {:else if plan && sorted.length}
        <ol class="blocks">
          {#each sorted as block (block.id)}
            {@const task = block.activity_id ? activityById.get(block.activity_id) : undefined}
            {@const action = primaryAction(block.state)}
            {@const assessment = assessments[block.id]}
            <li class="block state-{block.state}" class:now={isToday && blockNow(block, now)}>
              <div class="when"><span>{clockLabel(block.start_time)}</span><span class="muted">{clockLabel(blockEnd(block))}</span></div>
              <div class="body">
                <div class="title-line">
                  <strong>{block.title}</strong>
                  {#if block.fixed}<span class="tag">fixed</span>{/if}
                  {#if block.focus}<span class="tag">focus</span>{/if}
                  {#if block.state !== 'planned'}<span class="tag state">{stateLabel[block.state]}</span>{/if}
                </div>
                {#if task?.next_action}<p class="next">→ {task.next_action}</p>{/if}
                {#if block.state_note || block.explanation_code}
                  <p class="muted small">{#if block.explanation_code}{block.explanation_code.replaceAll('_', ' ')}{/if}{#if block.state_note}{block.explanation_code ? ': ' : ''}“{block.state_note}”{/if}</p>
                {/if}
                {#if assessment && assessment.status !== 'unknown'}
                  <details class="evidence status-{assessment.status}">
                    <summary>{assessment.status === 'consistent' ? 'On plan' : 'Possible drift'}</summary>
                    <p>{assessment.reason}</p>
                    <p class="muted small">{assessment.inference} {assessment.freshness_note}.</p>
                    {#if assessment.status === 'possible_drift'}<button class="quiet-button" disabled={stateBusy} on:click={() => askNote(block, 'switched')}>What happened?</button>{/if}
                    {#if assessment.evidence.length}
                      <ul>
                        {#each assessment.evidence.slice(0, 8) as item (item.source_instance_key + item.source_record_key)}
                          <li><span>{item.summary}</span><small>{clockLabel(item.occurred_at)}{#if item.related_activity_title} · {item.related_activity_title}{/if}</small></li>
                        {/each}
                      </ul>
                    {/if}
                  </details>
                {/if}
                {#if noteFor === block.id}
                  <div class="note-editor">
                    <label>What changed?
                      <select bind:value={explanationCode}>
                        <option value="distracted">I got distracted</option>
                        <option value="blocked">The task became blocked</option>
                        <option value="urgent">Something urgent happened</option>
                        <option value="intentional_switch">I switched on purpose</option>
                        <option value="unrealistic_plan">The plan was unrealistic</option>
                        <option value="finished_early">I finished early</option>
                        <option value="other">Other</option>
                      </select>
                    </label>
                    <label>Result
                      <select bind:value={explanationState}>
                        <option value="active">Return / continue</option>
                        <option value="waiting">Waiting</option>
                        <option value="done">Completed</option>
                        <option value="blocked">Blocked</option>
                        <option value="skipped">Skipped</option>
                        <option value="switched">Switched</option>
                        <option value="unknown">Unknown</option>
                      </select>
                    </label>
                    <label class="grow">Detail <input type="text" placeholder="Optional" bind:value={noteText} /></label>
                    <button disabled={stateBusy} on:click={confirmNote}>{stateBusy ? 'Saving…' : 'Save'}</button>
                    <button class="quiet-button" disabled={stateBusy} on:click={() => noteFor = 0}>Cancel</button>
                  </div>
                {/if}
              </div>
              {#if noteFor !== block.id}
                <div class="actions">
                  {#if action}<button disabled={stateBusy} on:click={() => runPrimary(block)}>{action.label}</button>{/if}
                  <details class="more">
                    <summary aria-label="More actions">⋯</summary>
                    <div class="menu">
                      {#each moreActions(block) as choice}
                        <button class="quiet-button" disabled={stateBusy}
                          on:click={() => choice.ret ? applyBlockState(block.id, 'active', '', '', 'return') : settle(block, choice.state)}>{choice.label}</button>
                      {/each}
                    </div>
                  </details>
                </div>
              {/if}
            </li>
          {/each}
        </ol>
        {#if planEvidenceLoading}<p class="muted small">Checking what was actually done…</p>{/if}
        {#if planEvidenceError}<p class="muted small">Could not check the activity records: {planEvidenceError}</p>{/if}
      {:else}
        <div class="empty">
          <p>{planLoading ? 'Loading…' : 'Nothing planned for this day.'}</p>
          {#if !planLoading}<button on:click={startEditing}>Plan this day</button>{/if}
        </div>
      {/if}

      {#if planVersions.length > 1 && !editing}
        <details class="history">
          <summary>Earlier versions ({planVersions.length - 1})</summary>
          <ol>
            {#each planVersions as version (version.version_id)}
              <li class:current={plan?.version_id === version.version_id}>v{version.version_id} · {clockLabel(version.created_at)} · {version.block_count} block{version.block_count === 1 ? '' : 's'}{#if version.note} · {version.note}{/if}</li>
            {/each}
          </ol>
        </details>
      {/if}
      {#if currentAlignmentError && isToday}<p class="muted small">Could not read what you are doing now: {currentAlignmentError}</p>{/if}
    </div>

    <aside class="tasks-col" aria-labelledby="tasks-heading">
      <div class="tasks-head">
        <h3 id="tasks-heading">Tasks</h3>
        <button class={addingTask ? 'quiet-button' : ''} on:click={() => { addingTask = !addingTask; }}>{addingTask ? 'Cancel' : '+ New task'}</button>
      </div>
      {#if taskError}<p class="error" role="alert">{taskError}</p>{/if}
      {#if activitiesError}<p class="error" role="alert">{activitiesError}</p>{/if}
      {#if addingTask}
        <form class="task-form" on:submit|preventDefault={addTask}>
          <TaskFields bind:task={newTask} />
          <button type="submit" disabled={taskBusy || !newTask.title.trim()}>Add task</button>
        </form>
      {/if}
      <ul class="tasks">
        {#each openTasks as activity (activity.id)}
          <li>
            {#if editingTaskId === activity.id && taskDraft}
              <form class="task-form" on:submit|preventDefault={() => saveTaskEdit(activity)}>
                <TaskFields bind:task={taskDraft} />
                <div class="form-actions">
                  <button type="submit" disabled={taskBusy}>Save</button>
                  <button type="button" class="quiet-button" on:click={() => { editingTaskId = 0; taskDraft = undefined; }}>Cancel</button>
                </div>
              </form>
            {:else}
              <div class="task-line">
                <button class="link" on:click={() => beginTaskEdit(activity)} title="Edit">{activity.title}</button>
                <select class="status" disabled={taskBusy} value={activity.status} on:change={(event) => setStatus(activity, event.currentTarget.value as Activity['status'])} aria-label="State of {activity.title}">
                  {#each activityStatuses as status}<option value={status}>{statusLabel[status]}</option>{/each}
                </select>
              </div>
              {#if activity.next_action}<p class="next">→ {activity.next_action}</p>{/if}
              {#if activity.activity_state !== 'growth' || activity.deadline || activity.effort_estimate_minutes}
                <p class="muted small">{activityStateLabel[activity.activity_state]}{#if activity.deadline} · due {activity.deadline.slice(0, 10)}{/if}{#if activity.effort_estimate_minutes} · ~{hours(activity.effort_estimate_minutes)}{/if}</p>
              {/if}
            {/if}
          </li>
        {:else}
          <li class="muted small">{activitiesLoading ? 'Loading…' : 'No open tasks.'}</li>
        {/each}
      </ul>
      {#if finishedTasks.length}
        <button class="quiet-button finished-toggle" on:click={() => { showFinished = !showFinished; }}>{showFinished ? 'Hide' : 'Show'} finished ({finishedTasks.length})</button>
        {#if showFinished}
          <ul class="tasks finished">
            {#each finishedTasks as activity (activity.id)}
              <li><div class="task-line"><span>{activity.title}</span>
                <select class="status" disabled={taskBusy} value={activity.status} on:change={(event) => setStatus(activity, event.currentTarget.value as Activity['status'])} aria-label="State of {activity.title}">
                  {#if activity.status === 'done'}<option value="done">Completed</option>{/if}
                  {#each activityStatuses as status}<option value={status}>{statusLabel[status]}</option>{/each}
                </select></div></li>
            {/each}
          </ul>
        {/if}
      {/if}
    </aside>
  </div>
</section>

<style>
  .plan-page { margin-top: 8px; }
  .day-head { align-items: center; border-bottom: 1px solid var(--line); display: flex; flex-wrap: wrap; gap: 10px; justify-content: space-between; padding-bottom: 10px; }
  .day-nav, .head-actions { align-items: center; display: flex; gap: 6px; }
  .day-nav h2 { font-size: 1.3rem; margin: 0 6px; min-width: 9ch; text-align: center; }
  .day-head button { font-size: .85rem; line-height: 1.2; padding: 6px 11px; }
  .progress { align-items: center; display: flex; gap: 12px; margin: 10px 0 4px; }
  .progress p { color: var(--muted); font-size: .8rem; margin: 0; white-space: nowrap; }
  .bar { background: var(--soft); flex: 1; height: 6px; max-width: 320px; }
  .bar span { background: var(--fg); display: block; height: 100%; }
  .alignment { border-left: 3px solid var(--muted); font-size: .82rem; margin: 6px 0; padding: 2px 8px; }
  .alignment.state-aligned { border-left-color: var(--fg); }
  .alignment .muted { margin-left: 6px; }

  .columns { align-items: start; display: grid; gap: 24px; grid-template-columns: minmax(0, 1fr); margin-top: 10px; }
  @media (min-width: 980px) { .columns { grid-template-columns: minmax(0, 1fr) 340px; } }

  .blocks { list-style: none; margin: 0; padding: 0; }
  .block { align-items: start; border-bottom: 1px solid var(--line); display: grid; gap: 12px; grid-template-columns: 52px minmax(0, 1fr) auto; padding: 10px 6px; }
  .block.now { background: var(--soft); box-shadow: inset 3px 0 0 var(--fg); }
  .block.state-done .body strong { text-decoration: line-through; }
  .block.state-done, .block.state-skipped, .block.state-switched { opacity: .6; }
  .when { display: flex; flex-direction: column; font-size: .82rem; font-variant-numeric: tabular-nums; }
  .title-line { align-items: baseline; display: flex; flex-wrap: wrap; gap: 6px; }
  .next { font-size: .82rem; margin: 3px 0 0; }
  .tag { border: 1px solid var(--line); color: var(--muted); font-size: .66rem; padding: 0 4px; text-transform: uppercase; }
  .tag.state { border-color: var(--fg); color: var(--fg); }
  .actions { align-items: center; display: flex; gap: 4px; }
  .actions button { padding: 5px 10px; }
  .more { position: relative; }
  .more summary { border: 1px solid var(--line); cursor: pointer; list-style: none; padding: 4px 9px; }
  .more summary::-webkit-details-marker { display: none; }
  .menu { background: var(--panel); border: 1px solid var(--fg); display: flex; flex-direction: column; min-width: 140px; position: absolute; right: 0; top: calc(100% + 4px); z-index: 5; }
  .menu button { border: 0; text-align: left; }
  .evidence { font-size: .8rem; margin-top: 5px; }
  .evidence summary { cursor: pointer; display: inline-block; }
  .evidence.status-possible_drift summary { font-weight: 700; }
  .evidence p { margin: 4px 0; }
  .evidence ul { list-style: none; margin: 4px 0 0; padding: 0; }
  .evidence li { border-top: 1px solid var(--line); display: flex; gap: 8px; justify-content: space-between; padding: 3px 0; }
  .evidence small { color: var(--muted); white-space: nowrap; }
  .note-editor { align-items: end; display: flex; flex-wrap: wrap; gap: 6px; margin-top: 8px; }
  .note-editor label, .task-form label, .plan-note { color: var(--muted); display: flex; flex-direction: column; font-size: .72rem; gap: 2px; }
  .note-editor .grow { flex: 1; min-width: 160px; }
  .empty { border: 1px dashed var(--line); padding: 24px; text-align: center; }
  .empty p { color: var(--muted); margin: 0 0 10px; }

  .editor { display: flex; flex-direction: column; gap: 6px; }
  .draft-head, .draft-row { align-items: start; display: grid; gap: 8px; grid-template-columns: 116px minmax(0, 1fr) 70px 34px; }
  .draft-head { color: var(--muted); font-size: .72rem; }
  .what { display: flex; flex-direction: column; gap: 4px; }
  .flags { color: var(--muted); display: flex; font-size: .75rem; gap: 10px; }
  .add { align-self: flex-start; }
  .plan-note { margin-top: 6px; }

  .history { color: var(--muted); font-size: .8rem; margin-top: 14px; }
  .history summary { cursor: pointer; }
  .history ol { margin: 6px 0 0; padding-left: 20px; }
  .history li.current { color: var(--fg); }

  .tasks-col { border-left: 1px solid var(--line); padding-left: 16px; }
  @media (max-width: 979px) { .tasks-col { border-left: 0; border-top: 1px solid var(--line); padding: 12px 0 0; } }
  .tasks-head { align-items: center; display: flex; justify-content: space-between; margin-bottom: 6px; }
  .tasks-head h3 { margin: 0; }
  .tasks-head button { padding: 5px 10px; }
  .tasks { list-style: none; margin: 0; padding: 0; }
  .tasks li { border-bottom: 1px solid var(--line); padding: 7px 0; }
  .task-line { align-items: center; display: flex; gap: 8px; justify-content: space-between; }
  .link { background: none; border: 0; color: var(--fg); font-weight: 700; padding: 0; text-align: left; }
  .link:hover, .link:focus-visible { outline: none; text-decoration: underline; }
  .status { font-size: .75rem; max-width: 110px; }
  .task-form { display: grid; gap: 6px; grid-template-columns: 1fr 1fr; margin: 6px 0; }
  .task-form .wide, .task-form > button, .form-actions { grid-column: 1 / -1; }
  .form-actions { display: flex; gap: 6px; }
  .finished-toggle { font-size: .75rem; margin-top: 8px; padding: 4px 8px; }
  .finished { opacity: .7; }
  .muted { color: var(--muted); }
  .small { font-size: .78rem; }
  .error { margin: 6px 0; }
</style>
