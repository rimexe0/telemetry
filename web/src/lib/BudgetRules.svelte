<script lang="ts">
  import { getPolicy, savePolicy, type BudgetSettings, type PolicyConfig } from './api';
  import { budgetAsksLess, budgetInForce, budgetSummary, emptyBudget } from './budget';
  import { formatWindows, waitStart } from './lock';

  // The budget's numbers are the owner's, kept in the policy; nothing is
  // seeded. Asking less waits for the next day, as the other rules do.
  let config: PolicyConfig | null = null;
  let current: BudgetSettings | undefined;
  let draft: BudgetSettings = { ...emptyBudget };
  let busy = false;
  let error = '';
  let notice = '';
  let confirmMessage = '';

  void load();

  async function load() {
    try {
      const policy = await getPolicy();
      config = policy?.config ?? { distractions: [], essentials: [] };
      current = budgetInForce(config, new Date());
      draft = { ...emptyBudget, ...current };
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Unable to load the budget settings.';
    }
  }

  $: waiting = config?.budget_next && config.budget_next_from && budgetInForce(config, new Date()) !== config.budget_next ? config.budget_next : undefined;
  $: needsClosed = draft.enabled && !config?.closed_by_default;

  function save() {
    error = '';
    notice = '';
    if (budgetAsksLess(current, draft)) {
      confirmMessage = 'This asks less than now, so it only starts when the next bedtime ends; until then the budget stays as it is. It is recorded as a loosening.';
      return;
    }
    void write();
  }

  async function write() {
    if (!config) return;
    busy = true;
    confirmMessage = '';
    try {
      const policy = await savePolicy({ ...config, budget: { ...draft } }, 'Distraction budget set in the panel');
      const from = policy.config.budget_next && policy.config.budget_next_from;
      notice = from ? `Saved. It starts at ${waitStart(from)}.` : 'Saved.';
      await load();
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Saving failed.';
    } finally {
      busy = false;
    }
  }

  type Field = { key: Exclude<keyof BudgetSettings, 'enabled'>; label: string; unit: string; min?: number; max?: number };
  const groups: { title: string; fields: Field[] }[] = [
    { title: 'The day', fields: [
      { key: 'base_minutes', label: 'Each day starts with', unit: 'min', max: 1440 },
      { key: 'max_minutes', label: 'With habits, up to', unit: 'min', max: 1440 },
      { key: 'floor_minutes', label: 'Never under', unit: 'min', max: 1440 },
      { key: 'habits_for_max', label: 'Habits for the full bonus', unit: '', min: 1, max: 50 }
    ] },
    { title: 'What a minute costs', fields: [
      { key: 'rate_distraction', label: 'In distraction times', unit: '×', max: 100 },
      { key: 'rate_day', label: 'In the daytime', unit: '×', max: 100 },
      { key: 'rate_bedtime', label: 'In bedtime', unit: '×', max: 100 },
      { key: 'rate_grayscale_off', label: 'Added while grayscale is kept off', unit: '×', max: 100 },
      { key: 'borrow_multiplier', label: 'A minute borrowed costs tomorrow', unit: '×', min: 1, max: 100 }
    ] },
    { title: 'The door', fields: [
      { key: 'wait_minutes', label: 'Wait before it opens', unit: 'min', max: 240 },
      { key: 'window_minutes', label: 'Outside distraction times, a window lasts', unit: 'min', min: 1, max: 1440 },
      { key: 'window_use_minutes', label: 'with use of', unit: 'min', max: 1440 },
      { key: 'grace_minutes', label: 'and to close or save', unit: 'min', max: 60 }
    ] }
  ];
</script>

<section class="budget-rules" aria-labelledby="budget-rules-heading">
  <h3 id="budget-rules-heading">Distraction budget</h3>
  <p class="muted small">{budgetSummary(current)}</p>
  {#if waiting}<p class="muted small">From {waitStart(config?.budget_next_from)}: {budgetSummary(waiting)}</p>{/if}
  <p class="muted small">1× hours are the distraction times{config?.distraction_times?.length ? `: ${formatWindows(config.distraction_times)}` : ', none set'}. Habits earn the rest of the day's budget up to the maximum.</p>

  <form on:submit|preventDefault={save}>
    <label class="check"><input type="checkbox" bind:checked={draft.enabled} /> On</label>
    {#if needsClosed}<p class="muted small">The budget needs distractions closed by default, which is off now.</p>{/if}
    {#each groups as group}
      <fieldset>
        <legend>{group.title}</legend>
        {#each group.fields as field}
          <label>{field.label} <input type="number" min={draft.enabled ? field.min ?? 0 : 0} max={field.max} step="1" required bind:value={draft[field.key]} />{field.unit ? ` ${field.unit}` : ''}</label>
        {/each}
      </fieldset>
    {/each}
    <div class="row">
      <button disabled={busy || !config}>Save</button>
      <span class="muted small">Asking less starts after the next bedtime. Asking more applies at once.</span>
    </div>
  </form>

  {#if confirmMessage}
    <div class="confirm" role="alertdialog" aria-label="Confirm change">
      <p>{confirmMessage} Save anyway?</p>
      <button disabled={busy} on:click={write}>Save anyway</button>
      <button class="quiet-button" disabled={busy} on:click={() => { confirmMessage = ''; }}>Cancel</button>
    </div>
  {/if}
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  {#if notice}<p class="muted small" role="status">{notice}</p>{/if}
</section>

<style>
  h3 { margin: 6px 0; }
  p { margin: 4px 0; }
  form { display: grid; gap: 6px; }
  fieldset { border: 0; border-top: 1px solid var(--line); display: flex; flex-wrap: wrap; gap: 6px 14px; margin: 4px 0 0; padding: 6px 0 0; }
  legend { font-size: .75rem; font-weight: 700; padding: 0 6px 0 0; }
  fieldset label { align-items: center; display: flex; font-size: .82rem; font-weight: 400; gap: 6px; }
  input[type='number'] { width: 4.8em; }
  .check { align-items: center; display: flex; font-size: .85rem; gap: 8px; }
  .row { align-items: center; display: flex; flex-wrap: wrap; gap: 8px; margin-top: 4px; }
  .confirm { align-items: center; border: 1px solid var(--fg); display: flex; flex-wrap: wrap; gap: 8px; margin-top: 8px; padding: 8px; }
  .confirm p { flex: 1 1 100%; font-size: .85rem; margin: 0; }
  .muted { color: var(--muted); }
  .small { font-size: .78rem; }
  .error { background: var(--soft); border-left: 4px solid var(--fg); margin: 8px 0; padding: 9px; }
</style>
