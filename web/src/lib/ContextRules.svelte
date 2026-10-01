<script lang="ts">
  import { createContextRule, disableContextRule, listContextRules, type Activity, type ActivityContextRule, type ContextSignal } from './api';

  export let activities: Activity[] = [];

  const signalLabel: Record<ContextSignal, string> = {
    path_prefix: 'Folder / repository',
    domain: 'Web domain',
    url_prefix: 'URL prefix',
    workspace_id: 'T3 workspace id',
    app: 'App'
  };
  const placeholder: Record<ContextSignal, string> = {
    path_prefix: '/Users/you/code/project',
    domain: 'docs.example.com',
    url_prefix: 'https://github.com/owner/repo',
    workspace_id: 't3:<project id>',
    app: 'T3 Code or com.example.app'
  };

  let rules: ActivityContextRule[] = [];
  let loaded = false;
  let error = '';
  let busy = false;
  let activityId = 0;
  let signal: ContextSignal = 'path_prefix';
  let pattern = '';

  $: if (!loaded) void load();
  $: title = (id: number) => activities.find(activity => activity.id === id)?.title ?? `Activity #${id}`;

  async function load() {
    loaded = true;
    try {
      rules = await listContextRules();
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Unable to load project rules.';
    }
  }

  async function act(work: () => Promise<unknown>) {
    busy = true;
    error = '';
    try {
      await work();
      pattern = '';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'The server did not accept this change.';
    } finally {
      busy = false;
      await load();
    }
  }
</script>

<section class="context-rules" aria-labelledby="context-rules-heading">
  <h3 id="context-rules-heading">Project rules</h3>
  <p class="muted small">Link an activity to its folders, repositories, sites or T3 workspaces. Alignment uses these instead of app names, so the same tool can count for different projects.</p>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  <ul>
    {#each rules as rule (rule.id)}
      <li><strong>{title(rule.activity_id)}</strong> <span class="muted">{signalLabel[rule.signal]}</span> <code>{rule.pattern}</code>
        <button class="quiet-button" disabled={busy} on:click={() => act(() => disableContextRule(rule.id))}>Remove</button></li>
    {:else}<li class="muted">No project rules yet.</li>{/each}
  </ul>
  <form on:submit|preventDefault={() => act(() => createContextRule({ activity_id: activityId, signal, pattern }))}>
    <select bind:value={activityId} aria-label="Activity" required>
      <option value={0} disabled>Activity…</option>
      {#each activities as activity (activity.id)}<option value={activity.id}>{activity.title}</option>{/each}
    </select>
    <select bind:value={signal} aria-label="Signal">
      {#each Object.entries(signalLabel) as [value, label]}<option {value}>{label}</option>{/each}
    </select>
    <input bind:value={pattern} placeholder={placeholder[signal]} aria-label="Pattern" required maxlength="500" />
    <button type="submit" disabled={busy || !activityId}>Add rule</button>
  </form>
</section>

<style>
  .context-rules { border-top: 1px solid var(--line); margin-top: 16px; padding-top: 10px; }
  h3 { margin: 0; }
  ul { list-style: none; margin: 6px 0; padding: 0; }
  li { align-items: center; border-bottom: 1px solid var(--line); display: flex; flex-wrap: wrap; font-size: .85rem; gap: 8px; padding: 4px 0; }
  li button { margin-left: auto; }
  form { display: flex; flex-wrap: wrap; gap: 6px; }
  form input { flex: 1; min-width: 220px; }
  .muted { color: var(--muted); }
  .small { font-size: .78rem; }
</style>
