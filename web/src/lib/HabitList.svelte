<script lang="ts">
  import { createHabit, listHabits, updateHabit, type Habit, type HabitChange } from './api';
  import { moveHabit, normalizeWeekdays, sortHabits, weekdays, weekdaysText } from './habits';

  // The owner's own habits: nothing is seeded. Archiving keeps the history.
  export let onchange: () => void = () => {};

  let habits: Habit[] = [];
  let loaded = false;
  let busy = false;
  let error = '';
  let newName = '';
  let editing = 0;
  let draftName = '';
  let draftDays: number[] = [];

  $: ({ active, archived } = sortHabits(habits));
  void load();

  async function load() {
    try {
      habits = await listHabits();
      error = '';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Unable to load habits.';
    } finally {
      loaded = true;
    }
  }

  async function act(work: () => Promise<unknown>) {
    busy = true;
    error = '';
    try {
      await work();
      await load();
      onchange();
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'The change was not saved.';
      await load();
    } finally {
      busy = false;
    }
  }

  const add = () => act(async () => {
    await createHabit({ name: newName.trim() });
    newName = '';
  });

  function edit(habit: Habit) {
    editing = habit.id;
    draftName = habit.name;
    const days = normalizeWeekdays(habit.weekdays ?? []);
    draftDays = days.length ? days : weekdays.map(item => item.day);
  }

  const save = (habit: Habit) => act(async () => {
    const change: HabitChange = {};
    if (draftName.trim() && draftName.trim() !== habit.name) change.name = draftName.trim();
    const days = normalizeWeekdays(draftDays);
    if (JSON.stringify(days) !== JSON.stringify(normalizeWeekdays(habit.weekdays ?? []))) change.weekdays = days;
    if (Object.keys(change).length) await updateHabit(habit.id, change);
    editing = 0;
  });

  // Moves change positions one at a time, top first.
  const move = (habit: Habit, delta: -1 | 1) => act(async () => {
    for (const change of moveHabit(active, habit.id, delta)) await updateHabit(change.id, { position: change.position });
  });

  const archive = (habit: Habit, value: boolean) => act(() => updateHabit(habit.id, { archived: value }));
</script>

<section class="habit-manage" aria-labelledby="habit-manage-heading">
  <h3 id="habit-manage-heading">Your habits</h3>
  <p class="muted small">Your own habits, ticked by hand. Each is worth the same share of the bonus on the days it is scheduled.</p>
  {#if error}<p class="error" role="alert">{error}</p>{/if}

  <form class="row" on:submit|preventDefault={add}>
    <label class="grow"><span class="sr-only">New habit</span><input placeholder="New habit" maxlength="120" bind:value={newName} /></label>
    <button disabled={busy || !newName.trim()}>Add</button>
  </form>

  {#if !loaded}<p class="muted small">Loading…</p>
  {:else if !active.length}<p class="muted small">No habits yet.</p>
  {:else}
    <ol class="list">
      {#each active as habit, index (habit.id)}
        <li>
          {#if editing === habit.id}
            <form class="edit" on:submit|preventDefault={() => save(habit)}>
              <label class="grow"><span class="sr-only">Name</span><input maxlength="120" bind:value={draftName} /></label>
              <fieldset class="days">
                <legend class="sr-only">Days it applies</legend>
                {#each weekdays as item}
                  <label title={item.long}><input type="checkbox" bind:group={draftDays} value={item.day} /> {item.short}</label>
                {/each}
              </fieldset>
              <button disabled={busy || !draftName.trim()}>Save</button>
              <button type="button" class="quiet-button" on:click={() => { editing = 0; }}>Cancel</button>
              <span class="muted small">All days or none means every day.</span>
            </form>
          {:else}
            <span class="name">{habit.name}</span>
            <span class="muted small">{weekdaysText(habit.weekdays)}</span>
            <span class="buttons">
              <button class="quiet-button" disabled={busy || index === 0} aria-label="Move {habit.name} up" on:click={() => move(habit, -1)}>↑</button>
              <button class="quiet-button" disabled={busy || index === active.length - 1} aria-label="Move {habit.name} down" on:click={() => move(habit, 1)}>↓</button>
              <button class="quiet-button" disabled={busy} on:click={() => edit(habit)}>Edit</button>
              <button class="quiet-button" disabled={busy} on:click={() => archive(habit, true)}>Archive</button>
            </span>
          {/if}
        </li>
      {/each}
    </ol>
  {/if}

  {#if archived.length}
    <details>
      <summary>Archived ({archived.length})</summary>
      <p class="muted small">Archived habits keep their history in the heatmap.</p>
      <ul class="list">
        {#each archived as habit (habit.id)}
          <li>
            <span class="name">{habit.name}</span>
            <span class="buttons"><button class="quiet-button" disabled={busy} on:click={() => archive(habit, false)}>Bring back</button></span>
          </li>
        {/each}
      </ul>
    </details>
  {/if}
</section>

<style>
  .habit-manage { border-top: 1px solid var(--line); margin-top: 16px; padding-top: 8px; }
  h3 { font-size: 1rem; margin: 4px 0; }
  p { margin: 4px 0; }
  .row, .edit { align-items: center; display: flex; flex-wrap: wrap; gap: 8px; margin: 6px 0; }
  .grow { display: flex; flex: 1; min-width: 12em; }
  .grow input { width: 100%; }
  .list { list-style: none; margin: 6px 0; padding: 0; }
  .list li { align-items: center; border-bottom: 1px solid var(--line); display: flex; flex-wrap: wrap; font-size: .85rem; gap: 8px; padding: 5px 0; }
  .list .name { overflow-wrap: anywhere; }
  .buttons { display: flex; flex-wrap: wrap; gap: 6px; margin-left: auto; }
  .edit { flex: 1; }
  .days { border: 0; display: flex; flex-wrap: wrap; gap: 4px 10px; margin: 0; padding: 0; }
  .days label { align-items: center; display: flex; font-size: .78rem; gap: 4px; }
  details { margin-top: 8px; }
  summary { cursor: pointer; font-size: .82rem; }
  .muted { color: var(--muted); }
  .small { font-size: .78rem; }
  .error { background: var(--soft); border-left: 4px solid var(--fg); margin: 8px 0; padding: 9px; }
  .sr-only { height: 1px; margin: -1px; overflow: hidden; position: absolute; width: 1px; }
</style>
