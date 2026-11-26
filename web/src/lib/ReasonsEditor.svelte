<script lang="ts">
  import { getReasons, saveReasons } from './api';

  // The owner's own lines about what they want in life and why. The door
  // screen shows them; nothing is suggested or seeded.
  let text = '';
  let saved = '';
  let loaded = false;
  let busy = false;
  let error = '';
  let notice = '';

  const lines = (value: string) => value.split('\n').map(line => line.trim()).filter(Boolean);
  $: changed = lines(text).join('\n') !== lines(saved).join('\n');
  void load();

  async function load() {
    try {
      saved = text = (await getReasons()).join('\n');
      error = '';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Unable to load your reasons.';
    } finally {
      loaded = true;
    }
  }

  async function save() {
    busy = true;
    error = '';
    notice = '';
    try {
      saved = text = (await saveReasons(lines(text))).join('\n');
      notice = 'Saved. The door screen shows them from now on.';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Your reasons were not saved.';
    } finally {
      busy = false;
    }
  }
</script>

<section class="reasons" aria-labelledby="reasons-heading">
  <h3 id="reasons-heading">Your reasons</h3>
  <p class="muted small">What you want in life and why, in your own words. One per line. The door screen shows them before anything opens.</p>
  <form on:submit|preventDefault={save}>
    <label for="reasons-text" class="sr-only">Your reasons, one per line</label>
    <textarea id="reasons-text" rows="5" disabled={!loaded} bind:value={text}></textarea>
    <div class="row">
      <button disabled={busy || !loaded || !changed}>{busy ? 'Saving…' : 'Save'}</button>
      {#if changed}<button type="button" class="quiet-button" disabled={busy} on:click={() => { text = saved; }}>Undo changes</button>{/if}
      {#if notice}<span class="muted small" role="status">{notice}</span>{/if}
    </div>
  </form>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
</section>

<style>
  .reasons { border-top: 1px solid var(--line); margin-top: 16px; padding-top: 8px; }
  h3 { font-size: 1rem; margin: 4px 0; }
  p { margin: 4px 0; }
  form { display: grid; gap: 6px; }
  textarea { width: 100%; }
  .row { align-items: center; display: flex; flex-wrap: wrap; gap: 8px; }
  .muted { color: var(--muted); }
  .small { font-size: .78rem; }
  .error { background: var(--soft); border-left: 4px solid var(--fg); margin: 8px 0; padding: 9px; }
  .sr-only { height: 1px; margin: -1px; overflow: hidden; position: absolute; width: 1px; }
</style>
