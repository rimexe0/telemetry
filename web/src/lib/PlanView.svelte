<script lang="ts">
  import type { Activity, AlignmentView, Plan, PlanBlock, PlanInput, PlanVersionSummary, PlanningSettings } from './api';
  import type { PlanAssessment } from './planActual';
  import { activityStates, activityStateLabel, activityStatuses, clockLabel, fixedOverlaps, planTotals, sortBlocks, stateLabel, statusLabel, toBlockLocalTime } from './plan';

  export let day: string;
  export let activities: Activity[] = [];
  export let plan: Plan | null = null;
  export let activitiesLoading = false;
  export let planLoading = false;
  export let activitiesError = '';
  export let planError = '';
  export let planVersions: PlanVersionSummary[] = [];
  export let planningSettings: PlanningSettings | undefined;
  export let assessments: Record<number, PlanAssessment> = {};
  export let planEvidenceLoading = false;
  export let planEvidenceError = '';
  export let currentAlignment: AlignmentView | undefined;
  export let currentAlignmentError = '';

  export let onrefresh: () => void = () => {};
  export let onsave: (input: PlanInput) => Promise<void> = async () => {};
  export let onsetstate: (blockId: number, state: PlanBlock['state'], note: string, explanationCode?: string, action?: 'state' | 'explanation' | 'return') => Promise<void> = async () => {};
  export let oncreateactivity: (input: { title: string; notes?: string; next_action: string; resume_marker?: string; status?: Activity['status']; activity_state?: Activity['activity_state']; priority?: number; deadline?: string; effort_estimate_minutes?: number }) => Promise<void> = async () => {};
  export let onupdateactivity: (activity: Activity) => Promise<void> = async () => {};
  export let onupdatesettings: (enabled: boolean) => Promise<void> = async () => {};

  type Draft = { key: number; activityId?: number; title: string; hhmm: string; duration: number; fixed: boolean; focus: boolean };
  type ActivityDraft = { title: string; notes: string; next_action: string; resume_marker: string; status: Activity['status']; activity_state: Activity['activity_state']; priority: number; deadline: string; effort: string };
  let editing = false;
  let draft: Draft[] = [];
  let planNote = '';
  let noteFor = 0;
  let noteText = '';
  let explanationCode = 'other';
  let explanationState: PlanBlock['state'] = 'switched';
  let saving = false;
  let saveError = '';
  let stateBusy = false;
  let stateError = '';
  let newTitle = '';
  let newNotes = '';
  let newAction = '';
  let newResume = '';
  let newEffort = '';
  let newStatus: Activity['status'] = 'active';
  let newActivityState: Activity['activity_state'] = 'growth';
  let newPriority = 0;
  let newDeadline = '';
  let activityBusy = false;
  let activityError = '';
  let editingActivityId = 0;
  let activityDraft: ActivityDraft | undefined;
  let settingsBusy = false;
  let settingsError = '';

  const toDraft = (block: PlanBlock): Draft => ({
    key: block.id,
    activityId: block.activity_id,
    title: block.activity_id ? '' : block.title,
    hhmm: clockLabel(block.start_time),
    duration: block.duration_minutes,
    fixed: block.fixed,
    focus: block.focus ?? false
  });

  const startEditing = () => {
    draft = (plan?.blocks ?? []).map(toDraft);
    planNote = plan?.note ?? '';
    saveError = '';
    editing = true;
  };

  const addDraft = () => {
    const last = draft[draft.length - 1];
    const hhmm = last
      ? toBlockLocalTime(new Date(`${day}T${last.hhmm}:00+03:00`).getTime() + last.duration * 60_000).toISOString().slice(11, 16)
      : '09:00';
    draft = [...draft, { key: Date.now(), activityId: undefined, title: '', hhmm, duration: 30, fixed: false, focus: false }];
  };

  const draftsValid = () => draft.every(row => /^([01]\d|2[0-3]):[0-5]\d$/.test(row.hhmm) && row.duration >= 1 && (row.activityId || row.title.trim()));

  const draftInput = (): PlanInput => ({
    note: planNote.trim(),
    blocks: draft.map(row => ({
      activity_id: row.activityId,
      title: row.activityId ? undefined : row.title.trim(),
      start_time: `${day}T${row.hhmm}:00+03:00`,
      duration_minutes: row.duration,
      fixed: row.fixed,
      focus: row.focus
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
  const returnToBlock = async (block: PlanBlock) => {
    await applyBlockState(block.id, 'active', '', '', 'return');
  };
  const confirmNote = async () => {
    const blockId = noteFor;
    if (await applyBlockState(blockId, explanationState, noteText.trim(), explanationCode, 'explanation')) noteFor = 0;
  };

  const addActivity = async () => {
    if (!newTitle.trim()) return;
    activityBusy = true;
    activityError = '';
    const effort = Number.parseInt(newEffort, 10);
    try {
      await oncreateactivity({
        title: newTitle.trim(), notes: newNotes.trim(), next_action: newAction.trim(), resume_marker: newResume.trim(),
        status: newStatus, activity_state: newActivityState, priority: newPriority,
        ...(newDeadline ? { deadline: `${newDeadline}T23:59:59+03:00` } : {}),
        ...(Number.isFinite(effort) && effort > 0 ? { effort_estimate_minutes: effort } : {})
      });
      newTitle = ''; newNotes = ''; newAction = ''; newResume = ''; newEffort = ''; newPriority = 0; newDeadline = '';
      newStatus = 'active'; newActivityState = 'growth';
    } catch (cause) {
      activityError = cause instanceof Error ? cause.message : 'Creating the activity failed.';
    } finally {
      activityBusy = false;
    }
  };

  const setStatus = async (activity: Activity, status: Activity['status']) => {
    activityBusy = true;
    activityError = '';
    try { await onupdateactivity({ ...activity, status }); }
    catch (cause) { activityError = cause instanceof Error ? cause.message : 'Saving the task state failed.'; }
    finally { activityBusy = false; }
  };

  const beginActivityEdit = (activity: Activity) => {
    editingActivityId = activity.id;
    activityDraft = {
      title: activity.title, notes: activity.notes, next_action: activity.next_action, resume_marker: activity.resume_marker,
      status: activity.status, activity_state: activity.activity_state, priority: activity.priority,
      deadline: activity.deadline ? activity.deadline.slice(0, 10) : '', effort: activity.effort_estimate_minutes?.toString() ?? ''
    };
    activityError = '';
  };

  const saveActivityEdit = async (activity: Activity) => {
    if (!activityDraft) return;
    activityBusy = true;
    activityError = '';
    const effort = Number.parseInt(activityDraft.effort, 10);
    try {
      await onupdateactivity({
        ...activity,
        title: activityDraft.title.trim(), notes: activityDraft.notes.trim(), next_action: activityDraft.next_action.trim(),
        resume_marker: activityDraft.resume_marker.trim(), status: activityDraft.status, activity_state: activityDraft.activity_state,
        priority: activityDraft.priority, deadline: activityDraft.deadline ? `${activityDraft.deadline}T23:59:59+03:00` : undefined,
        effort_estimate_minutes: Number.isFinite(effort) && effort > 0 ? effort : undefined
      });
      editingActivityId = 0;
      activityDraft = undefined;
    } catch (cause) {
      activityError = cause instanceof Error ? cause.message : 'Saving the activity failed.';
    } finally {
      activityBusy = false;
    }
  };

  const updateGate = async (enabled: boolean) => {
    settingsBusy = true;
    settingsError = '';
    try { await onupdatesettings(enabled); }
    catch (cause) { settingsError = cause instanceof Error ? cause.message : 'Saving the planning setting failed.'; }
    finally { settingsBusy = false; }
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
  {#if stateError}<p class="error" role="alert">{stateError}</p>{/if}
  {#if planEvidenceError}<p class="error" role="alert">Evidence: {planEvidenceError}</p>{/if}
  {#if currentAlignmentError}<p class="error" role="alert">Execution alignment: {currentAlignmentError}</p>{/if}

  {#if !editing && plan}
    <p class="summary">
      Version {plan.version_id} saved {clockLabel(plan.created_at)} (Istanbul) ·
      <strong>{totals.doneBlocks}</strong>/{totals.totalBlocks} done ·
      {totals.doneMinutes}m done, {totals.plannedMinutes}m remaining
      {#if totals.otherMinutes}, {totals.otherMinutes}m set aside{/if}
    </p>
    {#if currentAlignment}
      <section class="current-alignment state-{currentAlignment.alignment_state}" aria-label="Current execution alignment">
        <div class="assessment-head"><strong>Current execution: {currentAlignment.alignment_state.replace('_', ' ')}</strong><span>{currentAlignment.execution_state ?? 'No active session'}</span></div>
        <p>{currentAlignment.reason}</p>
        {#if currentAlignment.activity_title}<p class="muted">Task: {currentAlignment.activity_title}{#if currentAlignment.next_action} · Next: {currentAlignment.next_action}{/if}</p>{/if}
        <p class="muted">Evidence: {currentAlignment.source_freshness}. {currentAlignment.freshness_note}</p>
      </section>
    {/if}
    <ol class="blocks">
      {#each sorted as block (block.id)}
        <li class="block state-{block.state}">
          <div class="block-line">
            <span class="time">{clockLabel(block.start_time)}–{clockLabel(new Date(new Date(block.start_time).getTime() + block.duration_minutes * 60_000).toISOString())}</span>
            <strong>{block.title}</strong>
            {#if block.activity_id && activityById.get(block.activity_id)?.activity_state}<span class="tag">{activityStateLabel[activityById.get(block.activity_id)!.activity_state]}</span>{/if}
            {#if block.fixed}<span class="tag">fixed</span>{/if}
            {#if block.focus}<span class="tag">focus</span>{/if}
            <span class="tag state">{stateLabel[block.state]}</span>
          </div>
          {#if block.activity_id && activityById.get(block.activity_id)?.next_action}<p class="next"><span class="muted">Next / resume:</span> {activityById.get(block.activity_id)?.next_action}</p>{/if}
          {#if block.state_note}<p class="note">“{block.state_note}”</p>{/if}
          {#if block.explanation_code}<p class="explanation"><span class="muted">Owner explanation:</span> {block.explanation_code.replaceAll('_', ' ')}{#if block.explanation_provenance?.recorded_by} · {String(block.explanation_provenance.recorded_by)}{/if}</p>{/if}
          {#if assessments[block.id]}
            {@const assessment = assessments[block.id]}
            <div class="assessment status-{assessment.status}">
              <div class="assessment-head"><strong>{assessment.status === 'consistent' ? 'Consistent with plan' : assessment.status === 'possible_drift' ? 'Possible drift' : 'Insufficient evidence / unknown'}</strong><span>{assessment.source_freshness === 'fresh' ? 'Sources fresh' : assessment.source_freshness === 'stale' ? 'Sources stale' : 'Sources missing'}</span></div>
              <p>{assessment.reason}</p>
              <p class="muted">{assessment.inference} Source receipt: {assessment.freshness_note}.</p>
              {#if assessment.status === 'possible_drift'}<button class="quiet-button" disabled={stateBusy} on:click={() => askNote(block, 'switched')}>What happened?</button>{/if}
              {#if assessment.evidence.length}
                <details class="evidence-details"><summary>Observed evidence ({assessment.evidence.length})</summary><ul>
                  {#each assessment.evidence.slice(0, 8) as item (item.source_instance_key + item.source_record_key)}
                    <li><span>{item.summary}</span><small>{item.origin === 'observed_agent_activity' ? 'agent activity' : 'device activity'} · {item.relationship.replace('_', ' ')}{#if item.related_activity_title} · matches “{item.related_activity_title}”{/if} · {clockLabel(item.occurred_at)}</small></li>
                  {/each}
                </ul></details>
              {/if}
            </div>
          {:else if planEvidenceLoading}
            <p class="muted evidence-loading">Checking collected activity…</p>
          {/if}
          {#if noteFor === block.id}
            <div class="note-editor">
              <label>What changed?
                <select bind:value={explanationCode}>
                  <option value="distracted">I got distracted</option>
                  <option value="blocked">The planned task became blocked</option>
                  <option value="urgent">Something urgent happened</option>
                  <option value="intentional_switch">I intentionally switched tasks</option>
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
              <input type="text" placeholder="Optional detail (kept with the plan)" bind:value={noteText} />
              <button class="quiet-button" disabled={stateBusy} on:click={() => noteFor = 0}>Cancel</button>
              <button class="quiet-button primary" disabled={stateBusy} on:click={confirmNote}>{stateBusy ? 'Recording…' : 'Record explanation'}</button>
            </div>
          {:else}
            <div class="state-buttons">
              {#if block.state !== 'done'}<button class="quiet-button" disabled={stateBusy} on:click={() => settle(block, 'done')}>Done</button>{/if}
              {#if block.state !== 'active'}<button class="quiet-button" disabled={stateBusy} on:click={() => settle(block, 'active')}>Started</button>{/if}
              {#if block.state !== 'active'}<button class="quiet-button" disabled={stateBusy} on:click={() => returnToBlock(block)}>Return</button>{/if}
              {#if block.state !== 'skipped'}<button class="quiet-button" disabled={stateBusy} on:click={() => settle(block, 'skipped')}>Skipped</button>{/if}
              {#if block.state !== 'switched'}<button class="quiet-button" disabled={stateBusy} on:click={() => settle(block, 'switched')}>Switched</button>{/if}
              {#if block.state !== 'blocked'}<button class="quiet-button" disabled={stateBusy} on:click={() => settle(block, 'blocked')}>Blocked</button>{/if}
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
    <label class="plan-note">Plan note <input type="text" maxlength="500" placeholder="Why this version is different" bind:value={planNote} /></label>
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
          {#if !row.activityId}<label class="fixed" title="Close distractions during this block"><input type="checkbox" bind:checked={row.focus} /> focus</label>{/if}
          <button class="quiet-button" on:click={() => draft = draft.filter((_, i) => i !== index)} aria-label="Remove block">✕</button>
        </div>
      {/each}
    </div>
    <button class="quiet-button" on:click={addDraft}>Add block</button>
  {/if}

  <section class="planning-settings" aria-labelledby="planning-settings-heading">
    <div class="section-head"><div><h3 id="planning-settings-heading">Planning settings</h3><p class="muted">Controls what the phone blocks when there is no plan for today.</p></div></div>
    <label class="setting-row"><input type="checkbox" checked={planningSettings?.daily_plan_gate_enabled ?? false} disabled={settingsBusy || !planningSettings} on:change={(event) => updateGate(event.currentTarget.checked)} /> Daily-plan gate preference</label>
    <p class="muted setting-note">On: while today has no plan, your blocked apps stay closed until you plan the day. Off: a day without a plan counts as free time.</p>
    {#if settingsError}<p class="error" role="alert">{settingsError}</p>{/if}
  </section>

  {#if planVersions.length}
    <details class="version-history">
      <summary>Plan history ({planVersions.length} version{planVersions.length === 1 ? '' : 's'})</summary>
      <ol>
        {#each planVersions as version (version.version_id)}
          <li class:current={plan?.version_id === version.version_id}><span>v{version.version_id}</span><span>{clockLabel(version.created_at)} · {version.block_count} block{version.block_count === 1 ? '' : 's'}</span>{#if version.note}<span class="muted">{version.note}</span>{/if}</li>
        {/each}
      </ol>
    </details>
  {/if}

  <div class="section-head activities-head">
    <div><h3>Activities</h3><p class="muted">Owned by you; nothing is pre-seeded. Phase and task state stay separate.</p></div>
    <button class="quiet-button" on:click={onrefresh} disabled={activitiesLoading}>{activitiesLoading ? 'Refreshing…' : 'Refresh'}</button>
  </div>
  {#if activitiesError}<p class="error" role="alert">{activitiesError}</p>{/if}
  <ul class="activity-list">
    {#each activities as activity (activity.id)}
      <li>
        <div class="activity-line">
          <strong>{activity.title}</strong>
          <div class="activity-actions">
            <span class="tag">{activityStateLabel[activity.activity_state]}</span>
            <select disabled={activityBusy} value={activity.status} on:change={(event) => setStatus(activity, event.currentTarget.value as Activity['status'])} aria-label="Task state for {activity.title}">
              {#if activity.status === 'done'}<option value="done">Completed</option>{/if}
              {#each activityStatuses as status}<option value={status}>{statusLabel[status]}</option>{/each}
            </select>
            <button class="quiet-button" on:click={() => beginActivityEdit(activity)}>Edit</button>
          </div>
        </div>
        {#if activity.notes}<p class="muted next">Constraints: {activity.notes}</p>{/if}
        {#if activity.next_action}<p class="muted next">Next: {activity.next_action}</p>{/if}
        {#if activity.resume_marker}<p class="muted next">Resume marker: {activity.resume_marker}</p>{/if}
        {#if activity.priority || activity.deadline || activity.effort_estimate_minutes}<p class="muted next">Priority {activity.priority}{#if activity.deadline} · due {activity.deadline.slice(0, 10)}{/if}{#if activity.effort_estimate_minutes} · estimate {activity.effort_estimate_minutes}m{/if}</p>{/if}
        {#if editingActivityId === activity.id && activityDraft}
          <form class="activity-editor" on:submit|preventDefault={() => saveActivityEdit(activity)}>
            <input type="text" required maxlength="500" aria-label="Activity title" bind:value={activityDraft.title} />
            <textarea maxlength="500" aria-label="Activity notes" placeholder="Practical constraints or context" bind:value={activityDraft.notes}></textarea>
            <input type="text" maxlength="500" aria-label="Next action" placeholder="Next action" bind:value={activityDraft.next_action} />
            <input type="text" maxlength="500" aria-label="Resume marker" placeholder="Resume marker" bind:value={activityDraft.resume_marker} />
            <div class="editor-grid">
              <label>Phase <select bind:value={activityDraft.activity_state}>{#each activityStates as state}<option value={state}>{activityStateLabel[state]}</option>{/each}</select></label>
              <label>Task state <select bind:value={activityDraft.status}>{#if activityDraft.status === 'done'}<option value="done">Completed</option>{/if}{#each activityStatuses as status}<option value={status}>{statusLabel[status]}</option>{/each}</select></label>
              <label>Priority <input type="number" min="-1000" max="1000" bind:value={activityDraft.priority} /></label>
              <label>Deadline <input type="date" bind:value={activityDraft.deadline} /></label>
              <label>Estimate <input type="number" min="1" max="10080" placeholder="minutes" bind:value={activityDraft.effort} /></label>
            </div>
            <div class="state-buttons"><button class="quiet-button primary" type="submit" disabled={activityBusy}>Save activity</button><button class="quiet-button" type="button" on:click={() => { editingActivityId = 0; activityDraft = undefined; }}>Cancel</button></div>
          </form>
        {/if}
      </li>
    {:else}
      <p class="empty">{activitiesLoading ? 'Loading activities…' : 'No activities yet. Add the first one below.'}</p>
    {/each}
  </ul>
  <form class="new-activity" on:submit|preventDefault={addActivity}>
    <input type="text" placeholder="Activity title" bind:value={newTitle} required />
    <input type="text" placeholder="Notes / constraints" bind:value={newNotes} />
    <input type="text" placeholder="Next action" bind:value={newAction} />
    <input type="text" placeholder="Resume marker" bind:value={newResume} />
    <select bind:value={newActivityState} aria-label="Activity phase">{#each activityStates as state}<option value={state}>{activityStateLabel[state]}</option>{/each}</select>
    <select bind:value={newStatus} aria-label="Task state">{#each activityStatuses as status}<option value={status}>{statusLabel[status]}</option>{/each}</select>
    <input class="priority" type="number" min="-1000" max="1000" placeholder="Priority" bind:value={newPriority} />
    <input type="date" aria-label="Deadline" bind:value={newDeadline} />
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
  .block.state-skipped, .block.state-switched, .block.state-blocked, .block.state-unknown { opacity: .72; }
  .block.state-active { border-left-color: var(--fg); background: var(--soft); }
  .block-line { align-items: baseline; display: flex; flex-wrap: wrap; gap: 8px; }
  .time { color: var(--muted); font-variant-numeric: tabular-nums; }
  .tag { border: 1px solid var(--line); font-size: .7rem; padding: 0 5px; text-transform: uppercase; }
  .note { color: var(--muted); font-size: .8rem; margin: 4px 0 0; }
  .next, .explanation { font-size: .8rem; margin: 3px 0 0; }
  .state-buttons, .note-editor { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 6px; }
  .note-editor { align-items: end; }
  .note-editor label { color: var(--muted); display: flex; flex-direction: column; font-size: .75rem; gap: 2px; }
  .note-editor input { flex: 1; min-width: 200px; }
  .assessment { border: 1px solid var(--line); margin-top: 7px; padding: 7px 8px; }
  .assessment.status-consistent { border-left: 3px solid var(--fg); }
  .assessment.status-possible_drift { border-left: 3px solid var(--muted); }
  .current-alignment { border: 1px solid var(--line); border-left: 3px solid var(--muted); margin: 8px 0; padding: 7px 8px; }
  .current-alignment.state-aligned { border-left-color: var(--fg); }
  .current-alignment p { font-size: .8rem; margin: 3px 0; }
  .assessment p { font-size: .8rem; margin: 3px 0; }
  .assessment-head { align-items: baseline; display: flex; gap: 8px; justify-content: space-between; }
  .assessment-head span { color: var(--muted); font-size: .72rem; }
  .evidence-details { margin-top: 5px; }
  .evidence-details summary { cursor: pointer; font-size: .78rem; }
  .evidence-details ul { list-style: none; margin: 5px 0 0; padding: 0; }
  .evidence-details li { border-top: 1px solid var(--line); display: flex; flex-wrap: wrap; gap: 6px; justify-content: space-between; padding: 4px 0; }
  .evidence-details small { color: var(--muted); }
  .evidence-loading { font-size: .78rem; margin: 5px 0; }
  .hint { font-size: .8rem; }
  .plan-note { display: flex; flex-direction: column; font-size: .8rem; gap: 3px; margin-top: 8px; }
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
  .activity-actions { align-items: center; display: flex; flex-wrap: wrap; gap: 6px; }
  .activity-editor { background: var(--soft); display: flex; flex-direction: column; gap: 6px; margin-top: 8px; padding: 8px; }
  .activity-editor textarea { min-height: 54px; resize: vertical; }
  .editor-grid { display: flex; flex-wrap: wrap; gap: 6px; }
  .editor-grid label { color: var(--muted); display: flex; flex-direction: column; font-size: .75rem; gap: 2px; }
  .editor-grid input, .editor-grid select { min-width: 100px; }
  .new-activity { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 10px; }
  .new-activity input { flex: 1; min-width: 140px; }
  .new-activity select, .new-activity input[type="date"] { min-width: 120px; }
  .new-activity .effort, .new-activity .priority { flex: 0; width: 90px; }
  .planning-settings { border-top: 1px solid var(--line); margin-top: 18px; padding-top: 10px; }
  .setting-row { align-items: center; display: flex; gap: 7px; margin-top: 8px; }
  .setting-note { font-size: .78rem; margin: 4px 0 0 23px; }
  .version-history { border-top: 1px solid var(--line); margin-top: 10px; padding-top: 8px; }
  .version-history summary { cursor: pointer; font-size: .82rem; }
  .version-history ol { color: var(--muted); display: flex; flex-direction: column; gap: 4px; margin: 6px 0 0; padding-left: 22px; }
  .version-history li { display: flex; flex-wrap: wrap; gap: 8px; font-size: .78rem; }
  .version-history li.current { color: var(--fg); font-weight: 600; }
</style>
