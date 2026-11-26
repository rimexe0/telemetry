<script lang="ts">
  import { onDestroy } from 'svelte';
  import { APIError, cancelDoor, getBudget, getLock, setHabitDone, type BudgetDay, type BudgetState } from './api';
  import { bedtimeLine, budgetHistory, doorLive, doorStatus, formatBudgetMinutes, habitsDoneText, rateText } from './budget';
  import { shortDate } from './lock';
  import DoorScreen from './DoorScreen.svelte';

  /** Also show the last 30 days from the day snapshots. */
  export let history = false;
  /** Changes when habits were edited elsewhere, to load again. */
  export let refreshKey = 0;
  /** Called after a tick or untick, so other views can load again. */
  export let onticked: () => void = () => {};
  /** Today's budget day once loaded, for the heatmap beside it. */
  export let day = '';

  let state: BudgetState | null = null;
  let days: BudgetDay[] = [];
  let loaded = false;
  let missing = false;
  let error = '';
  let tickError = '';
  let busyHabit = 0;
  let doorBusy = false;
  let showDoor = false;
  let bedtimeEnd = '';
  let now = new Date();

  const clock = (at?: string | null) => (at ? new Date(at).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit', hour12: false, timeZone: 'Europe/Istanbul' }) : '');
  const tick = setInterval(() => { now = new Date(); }, 1000);
  const reload = setInterval(() => { if (!showDoor && document.visibilityState === 'visible') void load(); }, 30_000);
  onDestroy(() => { clearInterval(tick); clearInterval(reload); });

  $: void load(refreshKey);
  void getLock().then(lock => { bedtimeEnd = lock.bedtime?.enabled ? lock.bedtime.end ?? '' : ''; }).catch(() => undefined);

  async function load(_key = refreshKey) {
    try {
      const result = await getBudget(history ? 30 : 1);
      state = result.today;
      days = result.days;
      if (state?.day) day = state.day;
      error = '';
      missing = false;
    } catch (cause) {
      missing = cause instanceof APIError && cause.status === 404;
      error = missing ? '' : cause instanceof Error ? cause.message : 'Unable to load the budget.';
    } finally {
      loaded = true;
    }
  }

  async function toggle(id: number, done: boolean) {
    busyHabit = id;
    tickError = '';
    try {
      await setHabitDone(id, done, state?.day);
      await load();
      onticked();
    } catch (cause) {
      tickError = cause instanceof Error ? cause.message : 'The tick was not saved.';
    } finally {
      busyHabit = 0;
    }
  }

  async function cancel(id: number) {
    doorBusy = true;
    error = '';
    try {
      await cancelDoor(id);
      await load();
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'The door could not be cancelled.';
    } finally {
      doorBusy = false;
    }
  }

  $: habits = state?.habits ?? [];
  $: doneCount = habits.filter(habit => habit.done).length;
  $: door = doorLive(state?.door, now) ? state?.door ?? null : null;
  $: status = door ? doorStatus(door, now) : null;
  $: bedtime = state ? bedtimeLine(state, now, bedtimeEnd) : '';
  $: rows = state && history ? budgetHistory(days, state.day, 30) : [];
  $: scale = Math.max(1, ...rows.map(row => Math.max(row.record?.budget ?? 0, row.record?.spent ?? 0)));
</script>

<section class="today-budget" aria-labelledby="today-habits-heading">
  <div class="columns">
    <div class="habits-part">
      <div class="title">
        <h3 id="today-habits-heading">Today's habits</h3>
        {#if habits.length}<span class="muted small">{doneCount} of {habits.length} done</span>{/if}
      </div>
      {#if !loaded}<p class="muted small">Loading…</p>
      {:else if missing}<p class="muted small">This server does not have habits and the budget yet.</p>
      {:else if !habits.length}<p class="muted small">No habits scheduled today. Add your own on the Habits page.</p>
      {:else}
        <ul class="habit-list">
          {#each habits as habit (habit.id)}
            <li class:done={habit.done}>
              <label>
                <input type="checkbox" checked={habit.done} disabled={busyHabit !== 0}
                  on:change={(event) => toggle(habit.id, event.currentTarget.checked)} />
                <span class="name">{habit.name}</span>
              </label>
              {#if state?.enabled && habit.value > 0}
                <span class="worth small" title="Budget minutes this habit is worth today">{habit.done ? `+${formatBudgetMinutes(habit.value)} earned` : `worth ${formatBudgetMinutes(habit.value)}`}</span>
              {/if}
            </li>
          {/each}
        </ul>
      {/if}
      {#if tickError}<p class="error" role="alert">{tickError}</p>{/if}
    </div>

    {#if state}
      <div class="budget-part" aria-labelledby="budget-heading">
        <div class="title">
          <h3 id="budget-heading">Distraction budget</h3>
          <span class="muted small">{state.enabled ? shortDate(state.day) : 'off'}</span>
        </div>
        {#if !state.enabled}
          <p class="muted small">The budget is off. Distractions follow the other rules.</p>
        {:else}
          <p class="left"><strong>{formatBudgetMinutes(state.left)}</strong> left of {formatBudgetMinutes(state.budget)}</p>
          <p class="small">{rateText(state.rate)}{state.in_distraction_time ? '' : state.next_distraction_time ? ` · distraction time from ${clock(state.next_distraction_time)}` : ''}</p>
          {#if bedtime}<p class="small bedtime">{bedtime}</p>{/if}
          <dl>
            <div><dt>Day's budget</dt><dd>{formatBudgetMinutes(state.budget)}</dd></div>
            <div><dt>Earned by habits</dt><dd>+{formatBudgetMinutes(state.earned)}</dd></div>
            <div><dt>Habits open yesterday</dt><dd>{state.penalty ? `−${formatBudgetMinutes(state.penalty)}` : '0 min'}</dd></div>
            <div><dt>Borrowed by yesterday</dt><dd>{state.debt ? `−${formatBudgetMinutes(state.debt)}` : '0 min'}</dd></div>
            <div><dt>Used</dt><dd>{formatBudgetMinutes(state.spent)}</dd></div>
            <div><dt>Left</dt><dd>{formatBudgetMinutes(state.left)}</dd></div>
            {#if state.debt_tomorrow}<div><dt>Borrowed from tomorrow</dt><dd>{formatBudgetMinutes(state.debt_tomorrow)}</dd></div>{/if}
            <div><dt>Tomorrow can lend</dt><dd>{formatBudgetMinutes(state.borrowable)}</dd></div>
          </dl>

          <div class="door">
            {#if door && status}
              <span class="tag">door {status.phase}</span>
              <span class="small">{status.text}</span>
              {#if status.phase === 'waiting'}<button class="quiet-button" disabled={doorBusy} on:click={() => cancel(door.id)}>Cancel</button>{/if}
            {:else if state.door_available}
              <button class="quiet-button" on:click={() => { showDoor = true; }}>Make a door</button>
              <span class="muted small">{state.settings.wait_minutes} min wait, then {state.in_distraction_time ? 'until the distraction time ends' : `${state.settings.window_use_minutes} min of use in ${state.settings.window_minutes} min`}.</span>
            {:else}
              <span class="muted small">No door now{state.door_unavailable_reason ? `: ${state.door_unavailable_reason}` : '.'}{state.next_door_at ? ` The next door can open from ${clock(state.next_door_at)}.` : ''}</span>
            {/if}
          </div>
        {/if}
      </div>
    {/if}
  </div>
  {#if error}<p class="error" role="alert">{error}</p>{/if}

  {#if history && state}
    <details class="history" open>
      <summary>Last 30 days</summary>
      <div class="table-wrap">
        <table>
          <thead><tr><th>Day</th><th>Budget</th><th>Used</th><th><span class="sr-only">Used of budget</span></th><th>Passed on</th><th>Habits</th></tr></thead>
          <tbody>
            {#each rows as row (row.day)}
              <tr>
                <td>{shortDate(row.day)}</td>
                {#if row.record}
                  <td>{formatBudgetMinutes(row.record.budget)}</td>
                  <td>{formatBudgetMinutes(row.record.spent)}</td>
                  <td class="bar-cell"><span class="bar" aria-hidden="true"><span class="budget-bar" style="width:{(row.record.budget / scale) * 100}%"></span><span class="spent-bar" style="width:{(row.record.spent / scale) * 100}%"></span></span></td>
                  <td>{row.record.debt_passed_on ? formatBudgetMinutes(row.record.debt_passed_on) : '—'}</td>
                  <td>{habitsDoneText(row.record)}</td>
                {:else}
                  <td class="muted" colspan="5">No record</td>
                {/if}
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      <p class="muted small">A day without a record was not written by the server; nothing is assumed about it.</p>
    </details>
  {/if}
</section>

{#if showDoor && state}
  <DoorScreen {state} {bedtimeEnd} onclose={() => { showDoor = false; void load(); }} onchanged={() => void load()} />
{/if}

<style>
  .today-budget { border-top: 1px solid var(--line); margin-top: 12px; padding-top: 8px; }
  .columns { display: grid; gap: 12px 24px; grid-template-columns: minmax(0, 1fr); }
  @media (min-width: 860px) { .columns { grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); } }
  .title { align-items: baseline; display: flex; flex-wrap: wrap; gap: 8px; }
  h3 { font-size: 1rem; margin: 4px 0; }
  p { margin: 4px 0; }
  .habit-list { list-style: none; margin: 4px 0; padding: 0; }
  .habit-list li { align-items: center; border-bottom: 1px solid var(--line); display: flex; flex-wrap: wrap; gap: 8px; min-height: 40px; padding: 4px 0; }
  .habit-list label { align-items: center; cursor: pointer; display: flex; flex: 1; gap: 10px; min-width: 0; }
  .habit-list input[type='checkbox'] { height: 20px; width: 20px; }
  .habit-list input[type='checkbox']::before { height: 12px; width: 12px; }
  .habit-list .name { font-size: 1rem; overflow-wrap: anywhere; }
  .habit-list li.done .name { color: var(--muted); }
  .worth { color: var(--muted); white-space: nowrap; }
  .left { font-size: 1.05rem; }
  .left strong { font-size: 1.6rem; letter-spacing: -.04em; }
  .bedtime { border-left: 3px solid var(--fg); padding-left: 8px; }
  dl { border-top: 1px solid var(--line); display: grid; gap: 3px; margin: 8px 0 0; padding-top: 6px; }
  dl div { display: flex; gap: 12px; justify-content: space-between; }
  dt { color: var(--muted); font-size: .75rem; }
  dd { font-size: .8rem; margin: 0; text-align: right; }
  .door { align-items: center; display: flex; flex-wrap: wrap; gap: 8px; margin-top: 10px; }
  .tag { border: 1px solid var(--line); font-size: .68rem; padding: 0 4px; text-transform: uppercase; }
  .history { margin-top: 12px; }
  summary { cursor: pointer; font-size: .85rem; font-weight: 700; }
  .table-wrap { border: 1px solid var(--line); margin-top: 6px; max-height: 420px; overflow: auto; }
  table { border-collapse: collapse; min-width: 520px; width: 100%; }
  th, td { border-bottom: 1px solid var(--line); font-size: .75rem; padding: 5px 8px; text-align: left; }
  th { background: var(--soft); font-size: .68rem; letter-spacing: .05em; text-transform: uppercase; }
  .bar-cell { width: 30%; }
  .bar { display: block; height: 8px; position: relative; }
  .budget-bar, .spent-bar { height: 100%; left: 0; position: absolute; top: 0; }
  .budget-bar { border: 1px solid var(--line); }
  .spent-bar { background: var(--fg); }
  .muted { color: var(--muted); }
  .small { font-size: .78rem; }
  .error { background: var(--soft); border-left: 4px solid var(--fg); margin: 8px 0; padding: 9px; }
  .sr-only { height: 1px; margin: -1px; overflow: hidden; position: absolute; width: 1px; }
</style>
