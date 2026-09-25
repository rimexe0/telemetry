<script lang="ts">
  import { deriveNowView, executionStateLabel, type NowAction, type NowSnapshot, type NowView } from './execution';
  import { clockLabel } from './plan';

  export let snapshot: NowSnapshot | undefined;
  export let loading = false;
  export let error = '';
  export let onaction: (view: NowView, action: NowAction) => Promise<void> = async () => {};
  export let onopenplan: () => void = () => {};
  export let onrefresh: () => void = () => {};

  let busy = false;
  let actionError = '';
  let panel: 'none' | 'stuck' | 'stuck_unknown' | 'stuck_overwhelm' | 'switch' | 'waiting' = 'none';
  let resumeDraft = '';
  let waitingNote = '';
  let lastKey = '';

  $: view = snapshot ? deriveNowView(snapshot, new Date()) : undefined;
  // Close any open sub-flow when the authoritative state moves to another session or state.
  $: {
    const key = view ? `${view.kind}:${'primary' in view ? `${view.primary.id}:${view.primary.state}` : ''}` : '';
    if (key !== lastKey) {
      lastKey = key;
      if (panel !== 'stuck_unknown' && panel !== 'stuck_overwhelm') panel = 'none';
    }
  }

  async function run(action: NowAction, after?: () => void) {
    if (!view || busy) return;
    busy = true;
    actionError = '';
    try {
      await onaction(view, action);
      after?.();
    } catch (cause) {
      actionError = cause instanceof Error ? cause.message : 'The server did not accept this change.';
    } finally {
      busy = false;
    }
  }

  function openSwitch(resumeMarker: string) {
    resumeDraft = resumeMarker;
    panel = 'switch';
  }

  const time = (value: string) => clockLabel(value);
  const blockTime = (view: NowView) => ('task' in view && view.task.block ? `${time(view.task.block.start_time)} · ${view.task.block.duration_minutes} min` : '');
</script>

<section class="now" aria-labelledby="now-heading">
  {#if error}
    <p class="error" role="alert">{error}</p>
  {/if}
  {#if !view}
    <h2 id="now-heading" class="headline">NOW</h2>
    <p class="muted">{loading ? 'Loading current task…' : 'Current task is unavailable. Nothing is assumed until the server responds.'}</p>
    {#if !loading}<button on:click={onrefresh}>Retry</button>{/if}
  {:else if view.kind === 'plan_your_day'}
    <h2 id="now-heading" class="headline">PLAN YOUR DAY</h2>
    <p class="muted">{view.day}</p>
    <p>{view.planGateEnabled ? 'Plan-gated use needs a reviewed plan for today.' : 'There is no plan for today yet.'} Choose what matters before starting.</p>
    <button class="primary" on:click={onopenplan}>Open plan</button>
  {:else}
    <div class="head">
      <h2 id="now-heading" class="headline">{view.kind === 'recovery' ? 'OFFLINE RECOVERY' : 'NOW'}</h2>
      {#if view.kind !== 'recovery'}<span class="state">{executionStateLabel[view.kind]}</span>{/if}
    </div>

    {#if view.kind === 'choose'}
      {#if view.startable.length}
        <p>No single block is current. Choose what to start:</p>
        <ul class="choices">
          {#each view.startable as block (block.id)}
            <li><span class="muted">{time(block.start_time)}</span> {block.title}
              <button disabled={busy} on:click={() => block.activity_id && run({ type: 'start', block, activityId: block.activity_id })}>Start</button></li>
          {/each}
        </ul>
      {:else}
        <p>Nothing startable remains in today's plan.</p>
      {/if}
      {#if view.unlinked.length}
        <p class="muted small">Blocks without an activity cannot be tracked: {view.unlinked.map(block => block.title).join(', ')}.</p>
      {/if}
      <button on:click={onopenplan}>Replan</button>
    {:else if view.kind === 'ready'}
      <p class="title">{view.task.title}</p>
      <p class="muted">{view.upcoming ? 'Next' : 'Scheduled now'} · {blockTime(view)}</p>
      {#if view.task.resumeMarker}<p class="resume"><strong>RETURN HERE:</strong> {view.task.resumeMarker}</p>{/if}
      <p class="next"><strong>Next action:</strong> {view.task.nextAction || 'No next action recorded.'}</p>
      <div class="controls">
        <button class="primary" disabled={busy} on:click={() => view?.kind === 'ready' && run({ type: 'start', block: view.block, activityId: view.activityId })}>Start</button>
        <button disabled={busy} on:click={onopenplan}>Replan</button>
      </div>
    {:else if view.kind === 'recovery'}
      <p class="title">{view.task.title}</p>
      <p class="muted">Since {time(view.startedAt)}. This time is for stepping away from screens.</p>
      {#if view.task.resumeMarker}<p class="resume"><strong>RETURN HERE:</strong> {view.task.resumeMarker}</p>{/if}
      <div class="controls">
        <button class="primary" disabled={busy} on:click={() => run({ type: 'recovery_end' })}>I'm back</button>
        <button disabled={busy} on:click={onopenplan}>Replan</button>
      </div>
    {:else}
      {#if view.kind === 'interrupted' && view.task.resumeMarker}
        <p class="resume prominent"><strong>RETURN HERE:</strong> {view.task.resumeMarker}</p>
      {/if}
      <p class="title">{view.task.title}</p>
      {#if blockTime(view)}<p class="muted">{blockTime(view)}</p>{/if}
      <p class="next"><strong>Next action:</strong> {view.task.nextAction || 'No next action recorded.'}</p>
      {#if view.kind !== 'interrupted' && view.task.resumeMarker}<p class="resume"><strong>Resume marker:</strong> {view.task.resumeMarker}</p>{/if}

      {#if view.kind === 'waiting'}
        <p class="waiting">{view.ready ? 'Ready: the external work has finished. Return to this task.' : `Waiting on ${view.waitingNote || 'an agent, build or download'}.`}</p>
        {#if view.interleave && view.interleaveTask}
          <p class="interleave">Doing meanwhile: {view.interleaveTask.title}</p>
        {/if}
        <div class="controls">
          <button class="primary" disabled={busy} on:click={() => run({ type: 'resume' })}>Resume work</button>
          <button disabled={busy} on:click={() => openSwitch(view?.kind === 'waiting' ? view.task.resumeMarker : '')}>Switch / replan</button>
          <button disabled={busy} on:click={() => run({ type: 'recovery_start', overwhelmed: false })}>Offline recovery</button>
        </div>
      {:else if view.kind === 'interrupted'}
        <div class="controls">
          <button class="primary" disabled={busy} on:click={() => run({ type: 'resume' })}>Resume</button>
          <button disabled={busy} on:click={onopenplan}>Replan</button>
          <button disabled={busy} on:click={() => run({ type: 'done' })}>Done</button>
          <button disabled={busy} on:click={() => run({ type: 'recovery_start', overwhelmed: false })}>Offline recovery</button>
        </div>
      {:else}
        <div class="controls">
          <button disabled={busy} on:click={() => panel = panel === 'waiting' ? 'none' : 'waiting'}>Waiting…</button>
          <button disabled={busy} on:click={() => panel = panel === 'stuck' ? 'none' : 'stuck'}>Stuck</button>
          <button disabled={busy} on:click={() => openSwitch(view?.kind === 'active' ? view.task.resumeMarker : '')}>Switch / replan</button>
          <button disabled={busy} on:click={() => run({ type: 'done' })}>Done</button>
          <button disabled={busy} on:click={() => run({ type: 'recovery_start', overwhelmed: false })}>Offline recovery</button>
        </div>
      {/if}

      {#if panel === 'waiting'}
        <form class="subflow" on:submit|preventDefault={() => run({ type: 'waiting', note: waitingNote }, () => waitingNote = '')}>
          <label>What are you waiting on? <input bind:value={waitingNote} maxlength="500" placeholder="optional, e.g. agent run, CI" /></label>
          <button type="submit" disabled={busy}>Mark waiting</button>
        </form>
      {:else if panel === 'stuck'}
        <div class="subflow">
          <button disabled={busy} on:click={() => run({ type: 'stuck_unknown' }, () => panel = 'stuck_unknown')}>I don't know what to do next</button>
          <button disabled={busy} on:click={() => run({ type: 'stuck_overwhelm' }, () => panel = 'stuck_overwhelm')}>I know what to do, but I'm overwhelmed</button>
        </div>
      {:else if panel === 'stuck_unknown'}
        <div class="subflow">
          <p>Recorded. The task stays current. Write down the smallest next step you can see, or replan if the task itself is unclear.</p>
          <button on:click={onopenplan}>Edit next action in plan</button>
          <button on:click={() => panel = 'none'}>Close</button>
        </div>
      {:else if panel === 'stuck_overwhelm'}
        <div class="subflow">
          <p>Recorded. The next action is kept as it is. You can take a smaller step, step away, or change the plan.</p>
          <button disabled={busy} on:click={() => run({ type: 'recovery_start', overwhelmed: true }, () => panel = 'none')}>Offline recovery</button>
          <button on:click={() => openSwitch(view && 'task' in view ? view.task.resumeMarker : '')}>Switch / replan</button>
          <button on:click={() => panel = 'none'}>Keep going</button>
        </div>
      {:else if panel === 'switch'}
        <form class="subflow" on:submit|preventDefault={() => run({ type: 'switch', resumeMarker: resumeDraft }, onopenplan)}>
          <label>Where should you pick this up again? <input bind:value={resumeDraft} maxlength="500" placeholder="e.g. halfway through section 2" /></label>
          <button type="submit" disabled={busy}>Save and replan</button>
          <button type="button" on:click={() => panel = 'none'}>Cancel</button>
        </form>
      {/if}
    {/if}
  {/if}
  {#if actionError}<p class="error" role="alert">Not changed: {actionError}</p>{/if}
</section>

<style>
  .now { border-top: 1px solid var(--line); margin-top: 20px; max-width: 720px; padding-top: 12px; }
  .head { align-items: baseline; display: flex; gap: 10px; justify-content: space-between; }
  .headline { font-size: 1.6rem; letter-spacing: .04em; margin: 0 0 8px; }
  .state { border: 1px solid var(--line); font-size: .72rem; padding: 1px 6px; text-transform: uppercase; }
  .title { font-size: 1.2rem; font-weight: 600; margin: 4px 0; }
  .next, .resume, .waiting, .interleave { margin: 6px 0; }
  .resume.prominent { background: var(--soft); border-left: 3px solid var(--fg); font-size: 1.05rem; padding: 8px; }
  .muted { color: var(--muted); }
  .small { font-size: .8rem; }
  .primary { border: 1px solid var(--fg); }
  .controls, .subflow { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 10px; }
  .subflow { align-items: end; background: var(--soft); padding: 8px; }
  .subflow p { flex-basis: 100%; margin: 0 0 4px; }
  .subflow label { color: var(--muted); display: flex; flex: 1; flex-direction: column; font-size: .75rem; gap: 2px; min-width: 220px; }
  .choices { list-style: none; padding: 0; }
  .choices li { align-items: center; border-bottom: 1px solid var(--line); display: flex; gap: 8px; padding: 4px 0; }
  .choices button { margin-left: auto; }
</style>
