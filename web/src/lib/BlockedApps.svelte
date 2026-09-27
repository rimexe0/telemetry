<script lang="ts">
  import { getAppSuggestions, getPolicy, savePolicy, type AppUsageSuggestion, type PolicyConfig } from './api';
  import { packagesOf, setPackage, type ListName } from './blocklist';

  let config: PolicyConfig = { distractions: [], essentials: [] };
  let version = 0;
  let suggestions: AppUsageSuggestion[] = [];
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
      const [policy, apps] = await Promise.all([getPolicy(), getAppSuggestions()]);
      if (policy) {
        config = { ...policy.config, distractions: policy.config.distractions ?? [], essentials: policy.config.essentials ?? [] };
        version = policy.id;
      }
      suggestions = apps;
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Unable to load blocked apps.';
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
  .muted { color: var(--muted); }
  .small { font-size: .78rem; }
</style>
