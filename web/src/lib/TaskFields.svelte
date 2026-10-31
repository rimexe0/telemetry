<script lang="ts">
  import type { Activity } from './api';
  import { activityStates, activityStateLabel, activityStatuses, statusLabel } from './plan';

  export let task: {
    title: string; notes: string; next_action: string; resume_marker: string; status: Activity['status'];
    activity_state: Activity['activity_state']; priority: number; deadline: string; effort: string;
  };
</script>

<label class="wide">Title <input type="text" required maxlength="500" bind:value={task.title} /></label>
<label class="wide">Next step <input type="text" maxlength="500" placeholder="The smallest concrete thing to do" bind:value={task.next_action} /></label>
<label class="wide">Notes <input type="text" maxlength="500" placeholder="Constraints or context" bind:value={task.notes} /></label>
<label class="wide">Resume marker <input type="text" maxlength="500" placeholder="Where you left off" bind:value={task.resume_marker} /></label>
<label>Phase <select bind:value={task.activity_state}>{#each activityStates as state}<option value={state}>{activityStateLabel[state]}</option>{/each}</select></label>
<label>State <select bind:value={task.status}>{#if task.status === 'done'}<option value="done">Completed</option>{/if}{#each activityStatuses as status}<option value={status}>{statusLabel[status]}</option>{/each}</select></label>
<label>Priority <input type="number" min="-1000" max="1000" bind:value={task.priority} /></label>
<label>Estimate (min) <input type="number" min="1" max="10080" bind:value={task.effort} /></label>
<label>Deadline <input type="date" bind:value={task.deadline} /></label>

<style>
  label { color: var(--muted); display: flex; flex-direction: column; font-size: .72rem; gap: 2px; }
  .wide { grid-column: 1 / -1; }
</style>
