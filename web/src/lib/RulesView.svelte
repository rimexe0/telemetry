<script lang="ts">
  import type { Activity, PlanningSettings } from './api';
  import BlockedApps from './BlockedApps.svelte';
  import ContextRules from './ContextRules.svelte';
  import MorningCode from './MorningCode.svelte';
  import SettingsLock from './SettingsLock.svelte';

  export let activities: Activity[] = [];
  export let planningSettings: PlanningSettings | undefined;
  export let onupdatesettings: (enabled: boolean) => Promise<void> = async () => {};

  let gateBusy = false;
  let gateError = '';

  const sections = [
    { id: 'rule-no-plan', label: 'No plan' }, { id: 'rule-lock', label: 'Settings lock' }, { id: 'rule-bedtime', label: 'Bedtime' },
    { id: 'rule-extension', label: 'Browser extension' }, { id: 'rule-morning', label: 'Morning code' }, { id: 'rule-apps', label: 'Blocked apps' },
    { id: 'rule-projects', label: 'Project rules' }
  ];

  async function updateGate(enabled: boolean) {
    gateBusy = true;
    gateError = '';
    try { await onupdatesettings(enabled); }
    catch (cause) { gateError = cause instanceof Error ? cause.message : 'Saving failed.'; }
    finally { gateBusy = false; }
  }

  const jump = (id: string) => document.getElementById(id)?.scrollIntoView({ behavior: 'smooth', block: 'start' });
</script>

<div class="rules">
  <nav class="jump" aria-label="Rules sections">
    {#each sections as section}<button class="quiet-button" on:click={() => jump(section.id)}>{section.label}</button>{/each}
  </nav>
  <p class="muted small intro">What is blocked and when. Distraction times and openings are set on the Now page and through the assistant.</p>

  <div class="grid">
    <div class="card" id="rule-no-plan">
      <section aria-labelledby="no-plan-heading">
        <h3 id="no-plan-heading">When the day has no plan</h3>
        <label class="check"><input type="checkbox" checked={planningSettings?.daily_plan_gate_enabled ?? false} disabled={gateBusy || !planningSettings}
          on:change={(event) => updateGate(event.currentTarget.checked)} /> Keep blocked apps closed until I plan the day</label>
        <p class="muted small">Off: a day without a plan still keeps distractions closed outside distraction times, but nothing else waits for the plan.</p>
        {#if gateError}<p class="error" role="alert">{gateError}</p>{/if}
      </section>
    </div>
    <div class="card" id="rule-lock"><SettingsLock part="lock" /></div>
    <div class="card" id="rule-bedtime"><SettingsLock part="bedtime" /></div>
    <div class="card" id="rule-extension"><SettingsLock part="extension" /></div>
    <div class="card" id="rule-morning"><MorningCode /></div>
    <div class="card wide" id="rule-apps"><BlockedApps /></div>
    <div class="card wide" id="rule-projects"><ContextRules {activities} /></div>
  </div>
</div>

<style>
  .rules { margin-top: 8px; }
  .jump { display: flex; flex-wrap: wrap; gap: 6px; }
  .jump button { font-size: .75rem; padding: 4px 9px; }
  .intro { margin: 8px 0 12px; }
  .grid { display: grid; gap: 12px; grid-template-columns: minmax(0, 1fr); }
  @media (min-width: 980px) { .grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } .wide { grid-column: 1 / -1; } }
  .card { background: var(--panel); border: 1px solid var(--line); padding: 12px 14px; scroll-margin-top: 12px; }
  .card :global(> section) { border: 0; margin: 0; padding: 0; }
  .card :global(h3) { font-size: 1rem; margin: 0 0 4px; }
  .check { align-items: center; display: flex; font-size: .85rem; gap: 8px; margin: 8px 0 4px; }
  .muted { color: var(--muted); }
  .small { font-size: .78rem; }
</style>
