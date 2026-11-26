<script lang="ts">
  import HabitHeatmap from './HabitHeatmap.svelte';
  import HabitList from './HabitList.svelte';
  import ReasonsEditor from './ReasonsEditor.svelte';
  import TodayBudget from './TodayBudget.svelte';

  // Today's habits and budget first, then the year, then the habits and
  // reasons themselves. A tick or an edit loads the others again.
  let ticks = 0;
  let edits = 0;
  let day = '';
</script>

<div class="habits-view">
  <TodayBudget history refreshKey={edits} bind:day onticked={() => { ticks += 1; }} />
  <HabitHeatmap refreshKey={ticks + edits} openDay={day} />
  <div class="manage">
    <HabitList onchange={() => { edits += 1; }} />
    <ReasonsEditor />
  </div>
</div>

<style>
  .habits-view { margin-top: 8px; }
  .manage { display: grid; gap: 0 24px; grid-template-columns: minmax(0, 1fr); }
  @media (min-width: 980px) { .manage { grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); } }
</style>
