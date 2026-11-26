<script lang="ts">
  import { whenFresh } from './fresh';
  import { onDestroy } from 'svelte';
  import { APIError, cancelDoor, getBudget, getHabitHeatmap, getLock, getPolicy, setHabitDone, type BudgetState, type Heatmap } from './api';
  import { bedtimeLine, budgetInForce, doorLive, doorStatus, formatBudgetMinutes } from './budget';
  import { weekStrip } from './habits';
  import { budgetLine, clock, habitWorth, leftShare } from './today';
  import WeekStrip from './WeekStrip.svelte';

  // Today's two cards: the day's habits to tick, each with its last seven
  // days, first, since they are the point of the day (owner 2026-11-26); then
  // the distraction time left, kept small. There is no way to open
  // distractions here: the devices ask when the owner opens one.

  /** Changes when habits were edited elsewhere, to load again. */
  export let refreshKey = 0;
  /** Called after a tick or untick, so other views can load again. */
  export let onticked: () => void = () => {};

  let state: BudgetState | null = null;
  let heatmap: Heatmap | null = null;
  let loaded = false;
  let missing = false;
  let error = '';
  let tickError = '';
  let busyHabit = 0;
  let doorBusy = false;
  let bedtimeEnd = '';
  let distractionRate: number | undefined;
  let now = new Date();

  const tick = setInterval(() => { now = new Date(); }, 1000);
  const reload = setInterval(() => { if (document.visibilityState === 'visible') void load(); }, 30_000);
  onDestroy(() => { clearInterval(tick); clearInterval(reload); });

  $: void load(refreshKey);
  whenFresh(() => load());
  void getLock().then(lock => { bedtimeEnd = lock.bedtime?.enabled ? lock.bedtime.end ?? '' : ''; }).catch(() => undefined);
  // The rate in distraction times is a setting, not part of today's state.
  void getPolicy().then(policy => { distractionRate = policy ? budgetInForce(policy.config, new Date())?.rate_distraction : undefined; }).catch(() => undefined);

  async function load(_key = refreshKey) {
    try {
      const [result, weeks] = await Promise.all([getBudget(1), getHabitHeatmap(7).catch(() => null)]);
      state = result.today;
      heatmap = weeks;
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
      await load();
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
  $: bedtimeLabel = !state ? '' : state.rate.kind === 'bedtime' ? (bedtimeEnd ? `bedtime until ${bedtimeEnd}` : 'bedtime now') : state.bedtime_at ? `bedtime ${clock(state.bedtime_at)}` : '';
  $: share = state ? leftShare(state) : 0;
  $: today = state?.day ?? '';
  $: strips = Object.fromEntries(habits.map(habit => [habit.id, weekStrip(habit.name, heatmap?.habits.find(row => row.id === habit.id)?.days, today, habit.done)]));
</script>

<section class="card habits-card" aria-labelledby="habits-label">
  <div class="card-head">
    <h2 id="habits-label" class="card-label">Habits</h2>
    {#if habits.length}<span class="muted small">{doneCount} of {habits.length}</span>{/if}
  </div>
  {#if !loaded}<p class="muted small">Loading…</p>
  {:else if missing}<p class="muted small">This server does not have habits yet.</p>
  {:else if !state}<p class="muted small">Unknown: the habits could not be loaded.</p>
  {:else if !habits.length}<p class="muted small">No habits scheduled today. <a href="#habits">Add your own on Habits</a>.</p>
  {:else}
    <ul class="habit-list">
      {#each habits as habit, index (habit.id)}
        <li class:done={habit.done}>
          <label>
            <input type="checkbox" checked={habit.done} disabled={busyHabit !== 0}
              on:change={(event) => toggle(habit.id, event.currentTarget.checked)} />
            <span class="name">{habit.name}</span>
            {#if state.enabled && habitWorth(habit.value)}
              <span class="worth small" title={habit.done ? `Earned ${formatBudgetMinutes(habit.value)} of distraction time today` : `Worth ${formatBudgetMinutes(habit.value)} of distraction time today`}>{habitWorth(habit.value)}</span>
            {/if}
          </label>
          <div class="week"><WeekStrip cells={strips[habit.id] ?? []} letters={index === habits.length - 1} /></div>
        </li>
      {/each}
    </ul>
  {/if}
  {#if tickError}<p class="error" role="alert">{tickError}</p>{/if}
</section>

<section class="card budget-card" aria-labelledby="budget-label">
  <div class="card-head">
    <h2 id="budget-label" class="card-label">Distraction time</h2>
    {#if bedtimeLabel}<span class="muted small">{bedtimeLabel}</span>{/if}
  </div>
  {#if !loaded}<p class="muted small">Loading…</p>
  {:else if missing}<p class="muted small">This server does not have the distraction budget yet.</p>
  {:else if !state}<p class="muted small">Unknown: the budget could not be loaded.</p>
  {:else if !state.enabled}<p class="muted small">The budget is off. Distractions follow the other rules.</p>
  {:else}
    <p class="left"><strong>{formatBudgetMinutes(state.left)}</strong> <span class="muted small">left of {formatBudgetMinutes(state.budget)} · {budgetLine(state, distractionRate)}</span></p>
    <div class="bar" role="meter" aria-label="Distraction time left" aria-valuemin={0} aria-valuemax={Math.max(0, state.budget)} aria-valuenow={Math.max(0, Math.min(state.left, state.budget))}>
      <span style="width:{share * 100}%"></span>
    </div>
    {#if bedtime}<p class="small note">{bedtime}</p>{/if}
    {#if door && status}
      <p class="small door"><span class="tag">door {status.phase}</span> {status.text}
        {#if status.phase === 'waiting'}<button class="link-button" disabled={doorBusy} on:click={() => cancel(door.id)}>Cancel</button>{/if}</p>
    {/if}
    <details class="sum">
      <summary class="small">How today's time adds up</summary>
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
    </details>
  {/if}
  {#if error}<p class="error" role="alert">{error}</p>{/if}
</section>


<style>
  .left { align-items: baseline; display: flex; flex-wrap: wrap; gap: 4px 10px; margin: 6px 0 8px; }
  .left strong { font-size: 1.25rem; letter-spacing: -.03em; line-height: 1.2; }
  .bar { background: var(--soft); border: 1px solid var(--line); height: 6px; margin-bottom: 4px; }
  .bar span { background: var(--fg); display: block; height: 100%; }
  .note { border-left: 3px solid var(--fg); padding-left: 8px; }
  .door { margin: 8px 0 0; }
  .link-button { background: none; border: 0; color: var(--fg); cursor: pointer; min-height: 44px; padding: 0; text-decoration: underline; }
  .tag { border: 1px solid var(--line); font-size: .68rem; padding: 0 4px; text-transform: uppercase; }
  .sum { margin-top: 10px; }
  .sum summary { color: var(--muted); cursor: pointer; }
  dl { border-top: 1px solid var(--line); display: grid; gap: 3px; margin: 6px 0 0; padding-top: 6px; }
  dl div { display: flex; gap: 12px; justify-content: space-between; }
  dt { color: var(--muted); font-size: .75rem; }
  dd { font-size: .8rem; margin: 0; text-align: right; }
  .habit-list { list-style: none; margin: 4px 0 0; padding: 0; }
  .habit-list li { border-bottom: 1px solid var(--line); padding: 2px 0 10px; }
  .habit-list li:last-child { border-bottom: 0; padding-bottom: 2px; }
  .habit-list label { align-items: center; cursor: pointer; display: flex; gap: 12px; min-height: 44px; }
  .habit-list input[type='checkbox'] { height: 20px; width: 20px; }
  .habit-list input[type='checkbox']::before { height: 12px; width: 12px; }
  .name { flex: 1; font-size: .95rem; min-width: 0; overflow-wrap: anywhere; }
  li.done .name { color: var(--muted); }
  .worth { color: var(--muted); white-space: nowrap; }
  .week { padding-left: 32px; }
  /* The week strip spans the whole row (owner 2026-11-26). */
</style>
