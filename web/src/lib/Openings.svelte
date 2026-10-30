<script lang="ts">
  import { onDestroy } from 'svelte';
  import { confirmOpening, createOpening, endOpening, getOpenings, saveOpeningRule, type OpeningRule, type OpeningsState } from './api';
  import { confirmPrompt, confirmText, longestOpening, openingStatus, yesSpot } from './openings';

  let state: OpeningsState | null = null;
  let now = new Date();
  let busy = false;
  let error = '';
  let notice = '';
  let asking = false;
  let confirming = false;
  let chosen: string[] = [];
  let everything = false;
  let minutes = 15;
  let purpose = '';
  let editing = false;
  let draft: OpeningRule = {};

  const tick = setInterval(() => { now = new Date(); }, 1000);
  // Reload every minute so an opening that started or ended shows as such.
  const reload = setInterval(() => { void load(); }, 60_000);
  onDestroy(() => { clearInterval(tick); clearInterval(reload); });
  void load();

  $: rule = state?.rule ?? {};
  $: longest = longestOpening(rule);
  $: if (minutes > longest) minutes = longest;
  $: shown = (state?.openings ?? []).filter(opening => openingStatus(opening, now).phase !== 'ended');
  $: usedUp = !!rule.per_day && (state?.used_today ?? 0) >= rule.per_day;
  $: ready = purpose.trim() !== '' && (everything || chosen.length > 0) && minutes >= 1;
  // The first opening waiting for confirmations takes the whole page.
  $: asked = shown.find(opening => openingStatus(opening, now).phase === 'confirming');
  $: yesIn = asked?.next_confirm_at ? Math.max(0, Math.ceil((new Date(asked.next_confirm_at).getTime() - now.getTime()) / 1000)) : 0;

  async function load() {
    try {
      state = await getOpenings();
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Unable to load openings.';
    }
  }

  async function act(work: () => Promise<unknown>) {
    busy = true;
    error = '';
    notice = '';
    try {
      await work();
      await load();
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Something went wrong.';
    } finally {
      busy = false;
    }
  }

  const open = () => act(async () => {
    await createOpening({ purpose: purpose.trim(), group_ids: everything ? [] : chosen, everything, minutes });
    asking = confirming = false;
    chosen = [];
    everything = false;
    purpose = '';
  });

  const saveRule = () => act(async () => {
    const saved = await saveOpeningRule({ wait_minutes: draft.wait_minutes ?? 0, max_minutes: draft.max_minutes ?? 0, per_day: draft.per_day ?? 0,
      confirmations: draft.confirmations ?? 0, confirm_seconds: draft.confirm_seconds ?? 0 });
    editing = false;
    notice = saved.opening_rule_next_from ? `Saved. It asks less, so it starts on ${saved.opening_rule_next_from}.` : 'Saved.';
  });
</script>

<section class="openings" aria-labelledby="openings-heading">
  <div class="section-title">
    <h3 id="openings-heading">Openings</h3>
    <span class="muted small">
      {[rule.wait_minutes ? `${rule.wait_minutes} min wait` : '', rule.max_minutes ? `at most ${rule.max_minutes} min` : '',
        rule.per_day ? `${state?.used_today ?? 0} of ${rule.per_day} today` : `${state?.used_today ?? 0} today`].filter(Boolean).join(' · ')}
    </span>
    <button class="quiet-button" disabled={busy} on:click={() => { editing = !editing; draft = { ...rule }; }}>Rule</button>
    {#if !asking}<button disabled={busy || usedUp || !state} on:click={() => { asking = true; confirming = false; }}>Open something</button>{/if}
  </div>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  {#if notice}<p class="muted small" role="status">{notice}</p>{/if}
  {#if state?.next}<p class="muted small">From {state.next_from}: {state.next.wait_minutes ?? 0} min wait, at most {state.next.max_minutes || 'any'} min, {state.next.per_day || 'any number'} a day.</p>{/if}

  {#if editing}
    <form class="row" on:submit|preventDefault={saveRule}>
      <label>Wait <input type="number" min="0" max="240" bind:value={draft.wait_minutes} /> min</label>
      <label>At most <input type="number" min="0" max="1440" bind:value={draft.max_minutes} /> min</label>
      <label>Per day <input type="number" min="0" max="50" bind:value={draft.per_day} /></label>
      <label>Confirm <input type="number" min="0" max="30" bind:value={draft.confirmations} /> times</label>
      <label>each after <input type="number" min="0" max="120" bind:value={draft.confirm_seconds} /> s</label>
      <button disabled={busy}>Save</button>
      <span class="muted small">0 leaves it out. Asking less starts tomorrow.</span>
    </form>
  {/if}

  {#if asking}
    {#if !confirming}
      <form class="ask" on:submit|preventDefault={() => { confirming = true; }}>
        <div class="row">
          {#each state?.distractions ?? [] as group (group.id)}
            <label><input type="checkbox" bind:group={chosen} value={group.id} disabled={everything} /> {group.label || group.id}</label>
          {/each}
          <label><input type="checkbox" bind:checked={everything} /> Everything</label>
        </div>
        <div class="row">
          <label>For <input type="number" min="1" max={longest} bind:value={minutes} /> min</label>
          <input class="purpose" placeholder="What for" maxlength="300" bind:value={purpose} />
          <button disabled={!ready}>Next</button>
          <button type="button" class="quiet-button" on:click={() => { asking = false; }}>Cancel</button>
        </div>
      </form>
    {:else}
      <div class="row confirm" role="group" aria-label="Confirm opening">
        <span>{confirmText(rule, state?.used_today ?? 0, minutes, now)}</span>
        <button disabled={busy} on:click={open}>Yes, open it</button>
        <button class="quiet-button" disabled={busy} on:click={() => { asking = confirming = false; }}>No</button>
      </div>
    {/if}
  {/if}

  {#if shown.length}
    <ul>
      {#each shown as opening (opening.id)}
        {@const status = openingStatus(opening, now)}
        <li>
          <span class="tag">{status.phase}</span>
          <span>{opening.purpose}</span>
          <span class="muted small">{status.text}</span>
          <button class="quiet-button" disabled={busy} on:click={() => act(() => endOpening(opening.id))}>{status.phase === 'waiting' ? 'Cancel' : 'Close now'}</button>
        </li>
      {/each}
    </ul>
  {/if}

  {#if asked}
    {@const page = asked.confirmations ?? 0}
    {@const spot = yesSpot(asked.id, page)}
    <div class="confirm-page" role="dialog" aria-modal="true" aria-labelledby="confirm-question">
      <p class="muted small">Page {page + 1} of {asked.confirmations_needed}</p>
      <h2 id="confirm-question">{confirmPrompt(page)}</h2>
      <p>You asked to open {asked.everything ? 'everything' : 'distractions'} for {asked.length_minutes} minutes: {asked.purpose}</p>
      <button class="back" disabled={busy} on:click={() => act(() => endOpening(asked.id))}>No, back to work</button>
      {#key `${asked.id}-${page}`}
        <button class="yes quiet-button" style="top:{spot.top}%;left:{spot.left}%" disabled={busy || yesIn > 0}
          on:click={() => act(() => confirmOpening(asked.id))}>{yesIn > 0 ? `Yes (${yesIn})` : 'Yes'}</button>
      {/key}
    </div>
  {/if}
</section>

<style>
  .openings { border-top: 1px solid var(--line); margin-top: 12px; padding-top: 8px; }
  .section-title { align-items: center; display: flex; flex-wrap: wrap; gap: 8px; }
  .section-title h3 { margin: 4px 0; }
  .section-title .muted { margin-right: auto; }
  .row { align-items: center; display: flex; flex-wrap: wrap; gap: 8px; margin: 6px 0; font-size: .85rem; }
  .row input[type='number'] { width: 4.5em; }
  .purpose { flex: 1; min-width: 12em; }
  .confirm span { flex: 1; }
  ul { list-style: none; margin: 6px 0; padding: 0; }
  li { align-items: center; border-bottom: 1px solid var(--line); display: flex; flex-wrap: wrap; font-size: .85rem; gap: 8px; padding: 5px 0; }
  li button { margin-left: auto; }
  .tag { border: 1px solid var(--line); font-size: .68rem; padding: 0 4px; text-transform: uppercase; }
  .confirm-page { background: var(--panel, #f4f1ea); inset: 0; padding: 48px 24px; position: fixed; z-index: 50; }
  .confirm-page h2 { margin: 8px 0; }
  .confirm-page .back { margin-top: 12px; }
  .confirm-page .yes { position: absolute; transform: translate(-50%, -50%); }
  .muted { color: var(--muted); }
  .small { font-size: .78rem; }
</style>
