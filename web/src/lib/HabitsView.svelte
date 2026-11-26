<script lang="ts">
  import BudgetHistory from './BudgetHistory.svelte';
  import HabitHeatmap from './HabitHeatmap.svelte';
  import HabitList from './HabitList.svelte';
  import ReasonsEditor from './ReasonsEditor.svelte';

  // The year of every habit, then the owner's reasons, then the habits
  // themselves. Ticking is on Today. An edit loads the heatmap again.
  let edits = 0;
  let day = '';
</script>

<div class="habits-view">
  <HabitHeatmap refreshKey={edits} openDay={day} />
  <ReasonsEditor />
  <HabitList onchange={() => { edits += 1; }} />
  <BudgetHistory refreshKey={edits} bind:day />
</div>
