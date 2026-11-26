<script lang="ts">
  import { whenFresh } from './fresh';
  import { APIError, getBudget, type BudgetDay } from './api';
  import { budgetHistory, formatBudgetMinutes, habitsDoneText } from './budget';
  import { shortDate } from './lock';

  // The last 30 closed budget days from the server's day snapshots. A day
  // without a snapshot is shown as such; nothing is assumed about it.

  /** Changes when ticks or habits changed, to load again. */
  export let refreshKey = 0;
  /** Today's budget day once loaded, for the heatmap. */
  export let day = '';

  let days: BudgetDay[] = [];
  let today = '';
  let loaded = false;
  let missing = false;
  let error = '';

  $: void load(refreshKey);
  whenFresh(() => load());

  async function load(_key = refreshKey) {
    try {
      const result = await getBudget(30);
      days = result.days;
      today = result.today?.day ?? '';
      if (today) day = today;
      error = '';
    } catch (cause) {
      missing = cause instanceof APIError && cause.status === 404;
      error = missing ? '' : cause instanceof Error ? cause.message : 'Unable to load the last 30 days.';
    } finally {
      loaded = true;
    }
  }

  $: rows = today ? budgetHistory(days, today, 30) : [];
  $: scale = Math.max(1, ...rows.map(row => Math.max(row.record?.budget ?? 0, row.record?.spent ?? 0)));
</script>

{#if !missing}
<section class="card" aria-labelledby="budget-history-heading">
  <details>
    <summary><h2 id="budget-history-heading" class="card-label">Distraction time, last 30 days</h2></summary>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    {#if !loaded}<p class="muted small">Loading…</p>
    {:else if rows.length}
      <div class="table-wrap">
        <table>
          <thead><tr><th>Day</th><th>Budget</th><th>Used</th><th><span class="sr-only">Used of budget</span></th><th>Passed on</th><th>Habits</th></tr></thead>
          <tbody>
            {#each rows as row (row.day)}
              <tr>
                <td>{shortDate(row.day)}</td>
                {#if row.record}
                  <td>{formatBudgetMinutes(row.record.budget)}</td>
                  <td>{formatBudgetMinutes(row.record.spent)}</td>
                  <td class="bar-cell"><span class="bar" aria-hidden="true"><span class="budget-bar" style="width:{(row.record.budget / scale) * 100}%"></span><span class="spent-bar" style="width:{(row.record.spent / scale) * 100}%"></span></span></td>
                  <td>{row.record.debt_passed_on ? formatBudgetMinutes(row.record.debt_passed_on) : '—'}</td>
                  <td>{habitsDoneText(row.record)}</td>
                {:else}
                  <td class="muted" colspan="5">No record</td>
                {/if}
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      <p class="muted small">A day without a record was not written by the server; nothing is assumed about it.</p>
    {/if}
  </details>
</section>
{/if}

<style>
  summary { cursor: pointer; list-style-position: inside; }
  summary h2 { display: inline; }
  .table-wrap { border: 1px solid var(--line); margin-top: 8px; max-height: 420px; overflow: auto; }
  table { border-collapse: collapse; min-width: 520px; width: 100%; }
  th, td { border-bottom: 1px solid var(--line); font-size: .75rem; padding: 5px 8px; text-align: left; }
  th { background: var(--soft); font-size: .68rem; letter-spacing: .05em; text-transform: uppercase; }
  .bar-cell { width: 30%; }
  .bar { display: block; height: 8px; position: relative; }
  .budget-bar, .spent-bar { height: 100%; left: 0; position: absolute; top: 0; }
  .budget-bar { border: 1px solid var(--line); }
  .spent-bar { background: var(--fg); }
</style>
