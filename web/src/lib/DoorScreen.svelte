<script lang="ts">
  import { onDestroy } from 'svelte';
  import { cancelDoor, createDoor, doorBack, type BudgetState, type Door } from './api';
  import { bedtimeLine, doorCostLines, doorStatus, formatBudgetMinutes } from './budget';

  // The same screen the devices show before anything opens: the cost, the
  // habits, the owner's reasons, then Back or Still want it. Facts only.
  export let state: BudgetState;
  export let bedtimeEnd = '';
  export let onclose: () => void = () => {};
  export let onchanged: () => void = () => {};

  let door: Door | null = null;
  let busy = false;
  let error = '';
  let now = new Date();
  const tick = setInterval(() => { now = new Date(); }, 1000);
  onDestroy(() => clearInterval(tick));

  $: habits = state.habits ?? [];
  $: reasons = (state.reasons ?? []).filter(line => line.trim());
  $: bedtime = bedtimeLine(state, now, bedtimeEnd);
  $: status = door ? doorStatus(door, now) : null;

  async function back() {
    // Back counts as returning. Recording it must never keep the screen open.
    await doorBack().catch(() => undefined);
    onclose();
  }

  async function stillWant() {
    busy = true;
    error = '';
    try {
      door = await createDoor();
      onchanged();
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'No door could be made.';
    } finally {
      busy = false;
    }
  }

  async function cancel() {
    if (!door) return;
    busy = true;
    error = '';
    try {
      await cancelDoor(door.id);
      onchanged();
      onclose();
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'The door could not be cancelled.';
    } finally {
      busy = false;
    }
  }
</script>

<svelte:window on:keydown={(event) => { if (event.key === 'Escape' && !door && !busy) void back(); }} />

<div class="door-screen" role="dialog" aria-modal="true" aria-labelledby="door-heading">
  <div class="door-card">
    {#if !door}
      <h2 id="door-heading">Open distractions now</h2>
      {#each doorCostLines(state) as line}<p class="cost">{line}</p>{/each}
      {#if bedtime}<p class="bedtime">{bedtime}</p>{/if}

      {#if habits.length}
        <h3>Today's habits</h3>
        <ul class="habits">
          {#each habits as habit (habit.id)}
            <li class:done={habit.done}>
              <span class="mark" aria-hidden="true">{habit.done ? '■' : '□'}</span>
              <span>{habit.name}</span>
              <span class="muted small">{habit.done ? 'done' : state.enabled && habit.value > 0 ? `would earn ${formatBudgetMinutes(habit.value)}` : 'open'}</span>
            </li>
          {/each}
        </ul>
      {/if}

      {#if reasons.length}
        <h3>Your reasons</h3>
        <ul class="reasons">{#each reasons as line}<li>{line}</li>{/each}</ul>
      {/if}

      {#if error}<p class="error" role="alert">{error}</p>{/if}
      <div class="actions">
        <!-- svelte-ignore a11y_autofocus -->
        <button autofocus disabled={busy} on:click={back}>Back</button>
        <button class="quiet-button" disabled={busy || !!error} on:click={stillWant}>{busy ? 'Making the door…' : 'Still want it'}</button>
      </div>
      <p class="muted small">Still want it starts a {state.settings.wait_minutes} min wait. It can be cancelled during the wait at no cost.</p>
    {:else}
      <h2 id="door-heading">{status?.phase === 'waiting' ? 'The door is waiting' : 'The door is open'}</h2>
      <p class="countdown" role="timer" aria-live="off">{status?.text}</p>
      {#if door.use_minutes !== null && door.use_minutes !== undefined}
        <p class="muted">Then {door.use_minutes} min of use{state.settings.grace_minutes ? `, plus ${state.settings.grace_minutes} min to close or save` : ''}.</p>
      {:else if door.borrowing}
        <p class="muted">It lasts until the distraction time ends or the borrowing runs out.</p>
      {/if}
      {#if error}<p class="error" role="alert">{error}</p>{/if}
      <div class="actions">
        {#if status?.phase === 'waiting'}
          <button disabled={busy} on:click={cancel}>Cancel</button>
        {:else}
          <button class="quiet-button" on:click={onclose}>Close</button>
        {/if}
      </div>
    {/if}
  </div>
</div>

<style>
  .door-screen { align-items: flex-start; background: var(--bg); display: flex; inset: 0; justify-content: center; overflow-y: auto; padding: 32px 16px; position: fixed; z-index: 50; }
  .door-card { background: var(--panel); border: 1px solid var(--fg); display: flex; flex-direction: column; gap: 10px; max-width: 560px; padding: 22px; width: 100%; }
  h2 { margin: 0 0 4px; }
  h3 { font-size: .9rem; margin: 8px 0 0; }
  p { margin: 0; }
  .cost { font-size: 1.05rem; }
  .bedtime { border-left: 3px solid var(--fg); padding-left: 8px; }
  ul { list-style: none; margin: 0; padding: 0; }
  .habits li { align-items: baseline; border-bottom: 1px solid var(--line); display: flex; gap: 8px; padding: 5px 0; }
  .habits li .small { margin-left: auto; }
  .reasons li { border-left: 2px solid var(--line); margin: 4px 0; padding: 2px 8px; }
  .actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 8px; }
  .countdown { font-size: 2rem; letter-spacing: -.03em; }
  .muted { color: var(--muted); }
  .small { font-size: .78rem; }
  .error { background: var(--soft); border-left: 4px solid var(--fg); padding: 9px; }
</style>
