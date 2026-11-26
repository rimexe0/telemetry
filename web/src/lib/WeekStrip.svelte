<script lang="ts">
  import type { WeekCell } from './habits';

  // One habit's last seven days as small cells, today outlined. The weekday
  // letters go under the last strip only.
  export let cells: WeekCell[] = [];
  export let letters = false;
</script>

<div class="strip" role="list" aria-label="Last 7 days">
  {#each cells as cell (cell.day)}
    <span class="cell {cell.state}" class:today={cell.today} role="listitem" aria-label={cell.title} title={cell.title}></span>
  {/each}
</div>
{#if letters}
  <div class="strip letters" aria-hidden="true">
    {#each cells as cell (cell.day)}<span class:today={cell.today}>{cell.letter}</span>{/each}
  </div>
{/if}

<style>
  .strip { display: grid; gap: 4px; grid-template-columns: repeat(7, 14px); }
  .cell { box-sizing: border-box; display: block; height: 14px; width: 14px; }
  .cell.done { background: var(--done); }
  .cell.missed { background: var(--soft); border: 1px solid var(--line); }
  .cell.none { border: 1px dashed var(--line); }
  .cell.unscheduled { position: relative; }
  .cell.unscheduled::after { background: var(--line); content: ''; height: 2px; left: 6px; position: absolute; top: 6px; width: 2px; }
  .cell.unknown { background: repeating-linear-gradient(135deg, var(--soft) 0 2px, transparent 2px 4px); }
  /* Today not yet done: the outline alone. */
  .cell.open { background: transparent; }
  .cell.today { outline: 1px solid var(--fg); outline-offset: 1px; }
  .letters { color: var(--muted); font-size: .6rem; margin-top: 3px; }
  .letters span { text-align: center; }
  .letters .today { color: var(--fg); font-weight: 700; }
</style>
