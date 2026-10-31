<script lang="ts">
  import { getMorning, newMorningCode, previewMorningRule, saveMorningRule, type MorningRule, type MorningState } from './api';
  import { saveWarning } from './lock';

  let state: MorningState | null = null;
  let draft: MorningRule = { enabled: false, from_wake: true, until: '' };
  let start: 'wake' | 'time' = 'wake';
  let busy = false;
  let error = '';
  let notice = '';
  let code: { address: string; image: string } | null = null;
  let confirmMessage = '';
  let confirmRun: (() => Promise<void>) | null = null;

  void load();

  async function load() {
    try {
      state = await getMorning();
      draft = { ...state.rule };
      start = draft.from ? 'time' : 'wake';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Unable to load the morning code.';
    }
  }

  async function act(work: () => Promise<void>) {
    busy = true;
    error = '';
    notice = '';
    try {
      await work();
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Something went wrong.';
    } finally {
      busy = false;
    }
  }

  const save = () => act(async () => {
    const rule: MorningRule = { enabled: draft.enabled, until: draft.until, lock_all: draft.lock_all,
      from_wake: start === 'wake', from: start === 'time' ? draft.from : '' };
    const preview = await previewMorningRule(rule);
    const run = async () => {
      await saveMorningRule(rule);
      notice = preview.waits_until.morning ? `Saved. It starts on ${preview.waits_until.morning}.` : 'Saved.';
      await load();
    };
    const message = saveWarning(preview.waits_until.morning);
    if (message) { confirmMessage = message; confirmRun = run; return; }
    await run();
  });
  const confirmSave = () => act(async () => {
    const run = confirmRun;
    confirmMessage = '';
    confirmRun = null;
    if (run) await run();
  });

  const make = () => act(async () => {
    code = await newMorningCode();
    await load();
  });
</script>

<section class="morning" aria-labelledby="morning-heading">
  <h3 id="morning-heading">Morning code</h3>
  <p class="muted small">
    {#if state?.holding}Holding now: scan the printed code to open the day.
    {:else if state?.scanned_at}Scanned {new Date(state.scanned_at).toLocaleString()}.
    {:else if state?.rule.enabled}On.{:else}Off.{/if}
    {#if state?.code_made} Current code made {new Date(state.code_made).toLocaleDateString()}.{:else} No code printed yet.{/if}
  </p>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  {#if confirmMessage}
    <div class="confirm" role="alertdialog" aria-label="Confirm change">
      <p>{confirmMessage} Save anyway?</p>
      <button disabled={busy} on:click={confirmSave}>Save anyway</button>
      <button class="quiet-button" disabled={busy} on:click={() => { confirmMessage = ''; confirmRun = null; }}>Cancel</button>
    </div>
  {/if}
  {#if notice}<p class="muted small" role="status">{notice}</p>{/if}
  {#if state?.next}<p class="muted small">From {state.next_from}: {state.next.enabled ? `until ${state.next.until}` : 'off'}.</p>{/if}

  <form class="row" on:submit|preventDefault={save}>
    <label><input type="checkbox" bind:checked={draft.enabled} /> On</label>
    <label>From <select bind:value={start}><option value="wake">waking</option><option value="time">a time</option></select></label>
    {#if start === 'time'}<input type="time" bind:value={draft.from} required />{/if}
    <label>Gives up at <input type="time" bind:value={draft.until} required /></label>
    <label><input type="checkbox" bind:checked={draft.lock_all} /> Everything but essentials</label>
    <button disabled={busy}>Save</button>
  </form>
  <p class="muted small">Keep the camera and browser labelled essential, so the phone can scan while the rest is shut. Asking less starts tomorrow.</p>

  <div class="row">
    <button class="quiet-button" disabled={busy || state?.holding} on:click={make}>{state?.code_made ? 'Make a new code' : 'Make the code'}</button>
    {#if state?.code_made}<span class="muted small">A new code retires the printed one.</span>{/if}
  </div>
  {#if code}
    <div class="sticker">
      <img src={code.image} alt="Morning code" />
      <button on:click={() => window.print()}>Print</button>
      <p class="muted small">Shown once. Print it, then close this.</p>
      <button class="quiet-button" on:click={() => { code = null; }}>Close</button>
    </div>
  {/if}
</section>

<style>
  .morning { border-top: 1px solid var(--line); margin-top: 16px; padding-top: 10px; }
  h3 { margin: 6px 0; }
  .row { align-items: center; display: flex; flex-wrap: wrap; gap: 8px; margin: 6px 0; font-size: .85rem; }
  .sticker { align-items: center; display: flex; flex-direction: column; gap: 6px; margin: 8px 0; }
  .sticker img { image-rendering: pixelated; max-width: 260px; width: 100%; }
  .confirm { align-items: center; border: 1px solid var(--fg); display: flex; flex-wrap: wrap; gap: 8px; margin: 8px 0; padding: 8px; }
  .confirm p { flex: 1 1 100%; font-size: .85rem; margin: 0; }
  .muted { color: var(--muted); }
  .small { font-size: .78rem; }
  @media print {
    :global(body *) { visibility: hidden; }
    .sticker img { visibility: visible; position: fixed; left: 0; top: 0; width: 100%; max-width: none; }
  }
</style>
