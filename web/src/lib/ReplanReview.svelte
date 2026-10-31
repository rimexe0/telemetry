<script lang="ts">
  import { acceptReplanProposal, createReplanProposal, listReplanProposals, rejectReplanProposal, type Plan, type PlanChangeProposal } from './api';
  import { dayTime, describeChange, kindLabel, visibleChanges } from './replan';

  export let day: string;
  export let plan: Plan | null;
  export let onchanged: () => void = () => {};

  let proposals: PlanChangeProposal[] = [];
  let loadedFor = '';
  let error = '';
  let busy = false;
  /** The form is opened from the plan header's "Something changed". */
  export let open = false;
  let reason = '';
  let start = '';
  let end = '';
  let availableUntil = '';
  let showUnchanged = false;

  $: key = `${day}:${plan?.version_id ?? 0}`;
  $: if (plan && key !== loadedFor) void load(key);
  $: pending = proposals.filter(proposal => proposal.status === 'pending');

  async function load(forKey: string) {
    loadedFor = forKey;
    try {
      proposals = await listReplanProposals(day);
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Unable to load replan proposals.';
    }
  }

  async function act(work: () => Promise<unknown>, changedPlan = false) {
    busy = true;
    error = '';
    try {
      await work();
      if (changedPlan) onchanged();
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'The server did not accept this change.';
    } finally {
      busy = false;
      await load(key);
    }
  }

  function prepare() {
    if (!plan) return;
    const basePlan = plan;
    void act(async () => {
      await createReplanProposal(day, {
        base_plan_version_id: basePlan.version_id,
        reason,
        disruption: { start: dayTime(day, start), end: end ? dayTime(day, end) : undefined, available_until: dayTime(day, availableUntil) }
      });
      open = false;
      reason = '';
    });
  }
</script>

{#if plan && (open || pending.length || error)}
  <section class="replan" aria-label="Replan after a disruption">
    {#if open}
      <form on:submit|preventDefault={prepare}>
        <label>What changed? <input bind:value={reason} maxlength="500" required placeholder="e.g. appointment ran late" /></label>
        <label>Disrupted from <input type="time" bind:value={start} required /></label>
        <label>until <input type="time" bind:value={end} /></label>
        <label>Available until <input type="time" bind:value={availableUntil} required /></label>
        <button type="submit" disabled={busy}>Prepare proposal</button>
        <button type="button" class="quiet-button" on:click={() => open = false}>Cancel</button>
      </form>
      <p class="muted small">Fixed and finished blocks stay where they are. Unfinished work moves later in its current order; anything that does not fit is parked, not shortened. Nothing changes until you accept.</p>
    {/if}
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    {#each pending as proposal (proposal.id)}
      <div class="proposal">
        <p><strong>Proposal #{proposal.id}:</strong> {proposal.reason} <span class="muted small">against version {proposal.base_plan_version_id}</span></p>
        {#each proposal.warnings as warning}<p class="warning">⚠ {warning.detail}</p>{/each}
        <ul>
          {#each visibleChanges(proposal.changes, showUnchanged) as change}
            <li class="kind-{change.kind}"><span class="kind">{kindLabel[change.kind]}</span> {change.title}{change.fixed ? ' (fixed)' : ''} <span class="muted">{describeChange(change)}</span>{#if change.detail}<span class="muted small"> — {change.detail}</span>{/if}</li>
          {:else}<li class="muted">No block changes.</li>{/each}
        </ul>
        <label class="small"><input type="checkbox" bind:checked={showUnchanged} /> show unchanged blocks</label>
        <div class="actions">
          <button class="primary" disabled={busy} on:click={() => act(() => acceptReplanProposal(day, proposal), true)}>Accept</button>
          <button disabled={busy} on:click={() => act(() => rejectReplanProposal(day, proposal.id))}>Reject</button>
        </div>
      </div>
    {/each}
  </section>
{/if}

<style>
  .replan { border: 1px solid var(--line); margin: 10px 0; padding: 10px; }
  form { align-items: end; display: flex; flex-wrap: wrap; gap: 6px; }
  form label { color: var(--muted); display: flex; flex-direction: column; font-size: .75rem; gap: 2px; }
  form label:first-child { flex: 1; min-width: 200px; }
  .proposal { background: var(--soft); margin-top: 10px; padding: 8px; }
  .proposal p { margin: 3px 0; }
  .proposal ul { list-style: none; margin: 6px 0; padding: 0; }
  .proposal li { border-top: 1px solid var(--line); font-size: .85rem; padding: 3px 0; }
  .kind { border: 1px solid var(--line); display: inline-block; font-size: .68rem; margin-right: 4px; min-width: 88px; padding: 0 4px; text-transform: uppercase; }
  .kind-parked .kind, .kind-fixed_conflict .kind { border-color: var(--fg); }
  .warning { font-size: .85rem; }
  .actions { display: flex; gap: 6px; margin-top: 6px; }
  .primary { border: 1px solid var(--fg); }
  .muted { color: var(--muted); }
  .small { font-size: .78rem; }
</style>
