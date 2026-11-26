<script lang="ts">
  import { decideLabelProposal, getActivityLabels, removeActivityLabel, type ActivityLabel, type LabelProposal } from './api';
  import { describeChange } from './activity';

  // The owner's labels for apps and sites, which decide what counts as a
  // distraction, and the assistant's label proposals waiting for a yes or no.
  /** 'proposals' shows only the waiting proposals, for the Apps view. */
  export let show: 'all' | 'proposals' = 'all';
  /** Called after a decision or a removal, so views using labels load again. */
  export let onchange: () => void = () => {};

  let labels: ActivityLabel[] = [];
  let proposals: LabelProposal[] = [];
  let loaded = false;
  let busy = false;
  let error = '';

  $: pending = proposals.filter(item => item.status === 'pending');
  void load();

  async function load() {
    try {
      const current = await getActivityLabels();
      labels = current.labels;
      proposals = current.proposals;
      error = '';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Unable to load labels.';
    } finally {
      loaded = true;
    }
  }

  async function act(work: () => Promise<unknown>, failure: string) {
    busy = true;
    error = '';
    try {
      await work();
      await load();
      onchange();
    } catch (cause) {
      error = cause instanceof Error ? cause.message : failure;
    } finally {
      busy = false;
    }
  }

  const decide = (proposal: LabelProposal, accept: boolean) => act(() => decideLabelProposal(proposal, accept), 'Could not save the decision.');
  const remove = (label: ActivityLabel) => act(() => removeActivityLabel(label.id), 'Could not remove the label.');
</script>

{#if error}<p class="error" role="alert">{error}</p>{/if}

{#each pending as proposal (proposal.id)}
  <div class="proposal" role="group" aria-label="Label proposal">
    <strong>Label proposal</strong>
    <span class="muted small">{proposal.reason}</span>
    <ul>{#each proposal.changes as change}<li>{describeChange(change)}</li>{/each}</ul>
    <div class="actions">
      <button disabled={busy} on:click={() => decide(proposal, true)}>Accept</button>
      <button class="quiet-button" disabled={busy} on:click={() => decide(proposal, false)}>Reject</button>
    </div>
  </div>
{/each}

{#if show === 'all'}
  <section class="labels" aria-labelledby="labels-heading">
    <h3 id="labels-heading">Labels</h3>
    <p class="muted small">Tell your assistant what an app or site is to you; it proposes a label and you accept it here.</p>
    <ul>
      {#each labels as label (label.id)}<li><span class="tag">{label.label}</span> <code>{label.match}</code> <span class="muted small">{label.kind}</span><button class="quiet-button push" disabled={busy} on:click={() => remove(label)}>Remove</button></li>{:else}<li class="muted">{loaded ? 'No labels yet.' : 'Loading…'}</li>{/each}
    </ul>
  </section>
{/if}

<style>
  h3 { font-size: .95rem; margin: 12px 0 4px; }
  ul { list-style: none; margin: 4px 0; padding: 0; }
  li { align-items: center; border-bottom: 1px solid var(--line); display: flex; flex-wrap: wrap; font-size: .85rem; gap: 8px; padding: 4px 0; }
  code { overflow-wrap: anywhere; }
  .push { margin-left: auto; }
  .tag { border: 1px solid var(--line); font-size: .68rem; padding: 0 4px; text-transform: uppercase; }
  .proposal { border: 1px solid var(--fg); margin: 8px 0; padding: 8px; }
  .proposal ul li { border: 0; padding: 1px 0; }
  .actions { display: flex; gap: 6px; }
  .muted { color: var(--muted); }
  .small { font-size: .78rem; }
  .error { background: var(--soft); border-left: 4px solid var(--fg); margin: 8px 0; padding: 9px; }
</style>
