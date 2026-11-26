<script lang="ts">
  import { getHabitHeatmap, type Heatmap } from './api';
  import { cellState, cellTitle, combinedLevel, combinedTitle, heatmapLayout, runsText } from './habits';
  import { dayKey } from './health';

  /** Changes when habits or ticks changed, to load again. */
  export let refreshKey = 0;
  /** Today's budget day, from the budget. */
  export let openDay = '';

  const span = 365;
  let data: Heatmap | null = null;
  let error = '';
  let loading = true;

  $: void load(refreshKey);

  async function load(_key = refreshKey) {
    loading = true;
    try {
      data = await getHabitHeatmap(span);
      error = '';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Unable to load the heatmap.';
    } finally {
      loading = false;
    }
  }

  // The grid ends on the open budget day, which starts at the end of bedtime,
  // not at midnight; the calendar day stands in until it is known.
  $: today = openDay || dayKey(Date.now());
  $: layout = heatmapLayout(today, span);
  $: habits = [...(data?.habits ?? [])].sort((a, b) => Number(!!a.archived_day) - Number(!!b.archived_day));

  // Each box opens at its newest days, as GitHub's does.
  function scrollEnd(node: HTMLElement, _weeks: number) {
    const go = () => { node.scrollLeft = node.scrollWidth; };
    requestAnimationFrame(go);
    return { update: go };
  }
  const weekdayLabels = ['Mon', '', 'Wed', '', 'Fri', '', ''];
</script>

<section class="heatmap" aria-labelledby="heatmap-heading">
  <div class="title">
    <h3 id="heatmap-heading">The last year</h3>
    {#if loading && data}<span class="muted small">Refreshing…</span>{/if}
  </div>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  {#if !data}
    {#if loading}<p class="muted small">Loading…</p>{/if}
  {:else if !habits.length}
    <p class="muted small">No habits yet. Once you add one, its days show here.</p>
  {:else}
    <!-- A box that scrolls sideways takes focus, so the keyboard can scroll it. -->
    <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
    <div class="scroll" use:scrollEnd={layout.weeks.length} tabindex="0" role="region" aria-label="Habit heatmap, scrolls sideways">
      <div class="band">
        <div class="band-head"><strong>All habits</strong><span class="muted small">Share of the day's habits done</span></div>
        <div class="months" style="--weeks:{layout.weeks.length}" aria-hidden="true">
          {#each layout.months as month}<span style="grid-column:{month.column + 2}">{month.label}</span>{/each}
        </div>
        <div class="grid" style="--weeks:{layout.weeks.length}">
          {#each weekdayLabels as label, row}<span class="weekday" style="grid-row:{row + 1}" aria-hidden="true">{label}</span>{/each}
          {#each layout.weeks as week, column}
            {#each week as day, row}
              {#if day}
                {@const share = data.combined[day]}
                {@const title = combinedTitle(day, share)}
                <span class="cell level-{combinedLevel(share)}" style="grid-column:{column + 2};grid-row:{row + 1}" role="img" aria-label={title} {title}></span>
              {/if}
            {/each}
          {/each}
        </div>
      </div>

      {#each habits as habit (habit.id)}
        <div class="band">
          <div class="band-head">
            <strong>{habit.name}</strong>
            {#if habit.archived_day}<span class="tag">archived</span>{/if}
            <span class="small">{runsText(habit.current_run, habit.longest_run)}</span>
          </div>
          <div class="grid" style="--weeks:{layout.weeks.length}">
            {#each weekdayLabels as label, row}<span class="weekday" style="grid-row:{row + 1}" aria-hidden="true">{label}</span>{/each}
            {#each layout.weeks as week, column}
              {#each week as day, row}
                {#if day}
                  {@const state = cellState(habit.days?.[day], day, today)}
                  {@const title = cellTitle(habit.name, day, state)}
                  <span class="cell {state}" style="grid-column:{column + 2};grid-row:{row + 1}" role="img" aria-label={title} {title}></span>
                {/if}
              {/each}
            {/each}
          </div>
        </div>
      {/each}
    </div>
    <ul class="legend" aria-label="Legend">
      <li><span class="cell done"></span>done</li>
      <li><span class="cell missed"></span>not done</li>
      <li><span class="cell open"></span>open today</li>
      <li><span class="cell unscheduled"></span>not scheduled</li>
      <li><span class="cell none"></span>before the habit</li>
      <li><span class="cell unknown"></span>no record</li>
    </ul>
    <p class="muted small">Days a habit is not scheduled don't end its run. A day with no record is shown as such, never as not done.</p>
  {/if}
</section>

<style>
  .heatmap { --cell: 11px; --gap: 2px; border-top: 1px solid var(--line); margin-top: 16px; padding-top: 8px; }
  .title { align-items: baseline; display: flex; gap: 8px; }
  h3 { font-size: 1rem; margin: 4px 0; }
  .scroll { border: 1px solid var(--line); max-width: 100%; overflow-x: auto; padding: 8px 10px; }
  .scroll:focus-visible { outline: 2px solid var(--fg); outline-offset: 2px; }
  .band { margin-bottom: 12px; width: max-content; }
  .band:last-child { margin-bottom: 0; }
  .band-head { align-items: baseline; display: flex; flex-wrap: wrap; font-size: .85rem; gap: 8px; left: 0; margin-bottom: 3px; max-width: calc(100vw - 60px); position: sticky; }
  .months, .grid { display: grid; gap: var(--gap); grid-template-columns: 26px repeat(var(--weeks), var(--cell)); }
  .months { color: var(--muted); font-size: .65rem; height: 14px; }
  .months span { white-space: nowrap; }
  .grid { grid-template-rows: repeat(7, var(--cell)); }
  .weekday { color: var(--muted); font-size: .6rem; grid-column: 1; line-height: var(--cell); }
  .cell { box-sizing: border-box; display: inline-block; height: var(--cell); width: var(--cell); }
  .cell.done { background: var(--fg); }
  .cell.missed { background: var(--soft); border: 1px solid var(--line); }
  .cell.open { border: 1px solid var(--fg); }
  .cell.unscheduled { border: 1px dashed var(--line); }
  .cell.none { background: transparent; }
  .cell.none::after { background: var(--line); content: ''; display: block; height: 1px; margin: 5px auto 0; width: 3px; }
  .cell.unknown, .cell.level--1 { background: repeating-linear-gradient(135deg, var(--soft) 0 2px, transparent 2px 4px); }
  .cell.level-0 { background: var(--soft); border: 1px solid var(--line); }
  .cell.level-1 { background: color-mix(in srgb, var(--fg) 30%, var(--panel)); }
  .cell.level-2 { background: color-mix(in srgb, var(--fg) 55%, var(--panel)); }
  .cell.level-3 { background: color-mix(in srgb, var(--fg) 78%, var(--panel)); }
  .cell.level-4 { background: var(--fg); }
  .legend { color: var(--muted); display: flex; flex-wrap: wrap; font-size: .72rem; gap: 4px 14px; list-style: none; margin: 8px 0 4px; padding: 0; }
  .legend li { align-items: center; display: flex; gap: 5px; }
  .tag { border: 1px solid var(--line); font-size: .65rem; padding: 0 4px; text-transform: uppercase; }
  .muted { color: var(--muted); }
  .small { font-size: .78rem; }
  .error { background: var(--soft); border-left: 4px solid var(--fg); margin: 8px 0; padding: 9px; }
</style>
