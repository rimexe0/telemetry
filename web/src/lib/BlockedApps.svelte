<script lang="ts">
  import { decidePolicyProposal, getAppSuggestions, getPolicy, listPolicyProposals, savePolicy, type AppUsageSuggestion, type PolicyConfig, type PolicyProposal } from './api';
  import { packagesOf, setPackage, type ListName } from './blocklist';

  let config: PolicyConfig = { distractions: [], essentials: [] };
  let version = 0;
  let suggestions: AppUsageSuggestion[] = [];
  let proposals: PolicyProposal[] = [];
  const actionText = { block: 'Block', always_allow: 'Always allow', unlist: 'Remove from lists' };
  let loaded = false;
  let busy = false;
  let error = '';
  let saved = '';

  $: if (!loaded) void load();
  $: blocked = packagesOf(config.distractions ?? []);
  $: allowed = packagesOf(config.essentials ?? []);

  async function load() {
    loaded = true;
    try {
      const [policy, apps, pending] = await Promise.all([getPolicy(), getAppSuggestions(), listPolicyProposals()]);
      proposals = pending.filter(item => item.status === 'pending');
      if (policy) {
        config = { ...policy.config, distractions: policy.config.distractions ?? [], essentials: policy.config.essentials ?? [] };
        version = policy.id;
      }
      suggestions = apps;
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Unable to load blocked apps.';
    }
  }

  async function decide(proposal: PolicyProposal, accept: boolean) {
    busy = true;
    error = '';
    saved = '';
    try {
      const decided = await decidePolicyProposal(proposal, accept);
      if (decided.result_policy_version_id) saved = `Saved as version ${decided.result_policy_version_id}.`;
      loaded = false;
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Could not save the decision.';
    } finally {
      busy = false;
    }
  }

  async function change(pkg: string, target: ListName | null) {
    busy = true;
    error = '';
    saved = '';
    try {
      const policy = await savePolicy(setPackage(config, pkg, target), `${target ?? 'removed'}: ${pkg}`);
      config = policy.config;
      version = policy.id;
      saved = `Saved as version ${policy.id}.`;
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Could not save.';
    } finally {
      busy = false;
    }
  }
</script>

<section class="blocked-apps" aria-labelledby="blocked-apps-heading">
  <h3 id="blocked-apps-heading">Blocked apps</h3>
  <p class="muted small">Blocked during work and breaks, never in free time. Always-allowed apps are never blocked. Policy version {version || 'none yet'}.</p>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  {#if saved}<p class="muted small" role="status">{saved}</p>{/if}
  {#each proposals as proposal (proposal.id)}
    <div class="proposal" role="group" aria-label="Blocklist proposal">
      <strong>Blocklist proposal</strong> <span class="muted small">{proposal.reason}</span>
      <ul>{#each proposal.changes as item}<li>{actionText[item.action]} <code>{item.android_package}</code></li>{/each}</ul>
      <span class="actions"><button disabled={busy} on:click={() => decide(proposal, true)}>Accept</button>
        <button class="quiet-button" disabled={busy} on:click={() => decide(proposal, false)}>Reject</button></span>
    </div>
  {/each}
  <ul>
    {#each [...blocked] as pkg (pkg)}<li><span class="tag">blocked</span> <code>{pkg}</code> <button class="quiet-button" disabled={busy} on:click={() => change(pkg, null)}>Remove</button></li>{/each}
    {#each [...allowed] as pkg (pkg)}<li><span class="tag">always allowed</span> <code>{pkg}</code> <button class="quiet-button" disabled={busy} on:click={() => change(pkg, null)}>Remove</button></li>{/each}
  </ul>
  <h4>Your most used phone apps (30 days)</h4>
  <ul>
    {#each suggestions.filter(item => !blocked.has(item.package) && !allowed.has(item.package)) as item (item.package)}
      <li><code>{item.package}</code> <span class="muted small">{item.events} events</span>
        <span class="actions"><button disabled={busy} on:click={() => change(item.package, 'distractions')}>Block</button>
        <button class="quiet-button" disabled={busy} on:click={() => change(item.package, 'essentials')}>Always allow</button></span></li>
    {:else}<li class="muted">No phone usage yet.</li>{/each}
  </ul>
</section>

<style>
  .blocked-apps { border-top: 1px solid var(--line); margin-top: 16px; padding-top: 10px; }
  h3, h4 { margin: 6px 0; }
  ul { list-style: none; margin: 6px 0; padding: 0; }
  li { align-items: center; border-bottom: 1px solid var(--line); display: flex; flex-wrap: wrap; font-size: .85rem; gap: 8px; padding: 5px 0; }
  code { overflow-wrap: anywhere; }
  .actions { display: flex; gap: 6px; margin-left: auto; }
  .tag { border: 1px solid var(--line); font-size: .68rem; padding: 0 4px; text-transform: uppercase; }
  .proposal { border: 1px solid var(--line); margin: 8px 0; padding: 8px; }
  .proposal li { border: 0; padding: 1px 0; }
  .proposal .actions { margin-left: 0; }
  .muted { color: var(--muted); }
  .small { font-size: .78rem; }
</style>
