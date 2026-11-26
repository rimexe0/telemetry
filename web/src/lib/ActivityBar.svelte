<script lang="ts">
  import DayPicker from './DayPicker.svelte';
  import { activitySections, routeHash, type ActivitySection } from './nav';

  // Activity's own controls: the day (‹ day ›) and which view of it. Health
  // covers the last weeks, so it has no day.
  export let section: ActivitySection = 'timeline';
  export let day = '';
  export let today = '';
  export let onday: (day: string) => void = () => {};
</script>

<div class="activity-bar">
  <div class="segments" role="tablist" aria-label="Activity views">
    {#each activitySections as item (item.section)}
      <a role="tab" href={routeHash({ place: 'activity', section: item.section, day }, today)} class:active={section === item.section}
        aria-selected={section === item.section}>{item.label}</a>
    {/each}
  </div>
  {#if section !== 'health'}
    <div class="day"><DayPicker value={day} onchange={onday} max={section === 'plan' ? '' : today} /></div>
  {:else}
    <p class="muted small day-note">The last five weeks</p>
  {/if}
</div>

<style>
  .activity-bar { align-items: center; display: flex; flex-wrap: wrap; gap: 10px 16px; justify-content: space-between; margin-bottom: 14px; }
  .segments { border: 1px solid var(--fg); display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); min-width: min(100%, 400px); }
  .segments a { align-items: center; color: var(--fg); display: flex; font-size: .82rem; justify-content: center; min-height: 36px; padding: 0 8px; text-decoration: none; }
  .segments a + a { border-left: 1px solid var(--fg); }
  .segments a.active { background: var(--fg); color: var(--bg); font-weight: 700; }
  .segments a:focus-visible { outline: 2px solid var(--fg); outline-offset: 2px; }
  .day-note { margin: 0; }
  @media (max-width: 699px) {
    .activity-bar { align-items: stretch; flex-direction: column; }
    .segments { min-width: 0; width: 100%; }
    .segments a { min-height: 44px; padding: 0 2px; }
  }
</style>
