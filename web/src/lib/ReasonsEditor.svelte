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
  let editing = false;

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
      editing = false;
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Your reasons were not saved.';
    } finally {
      busy = false;
    }
  }
</script>

<section class="card reasons" aria-labelledby="reasons-heading">
  <div class="card-head">
    <h2 id="reasons-heading" class="card-label">Why I'm doing this</h2>
    {#if !editing}<button class="quiet-button" disabled={!loaded} on:click={() => { editing = true; notice = ''; }}>Edit</button>{/if}
  </div>
  {#if !editing}
    {#if !loaded}<p class="muted small">Loading…</p>
    {:else if lines(saved).length}
      <ul>{#each lines(saved) as line}<li>{line}</li>{/each}</ul>
    {:else}
      <p class="muted small">No reasons yet. What you want in life and why, in your own words; the door screen shows them before anything opens.</p>
    {/if}
    {#if notice}<p class="muted small" role="status">{notice}</p>{/if}
  {:else}
    <p class="muted small">One per line. The door screen shows them before anything opens.</p>
    <form on:submit|preventDefault={save}>
      <label for="reasons-text" class="sr-only">Your reasons, one per line</label>
      <textarea id="reasons-text" rows="5" disabled={!loaded} bind:value={text}></textarea>
      <div class="row">
        <button disabled={busy || !loaded || !changed}>{busy ? 'Saving…' : 'Save'}</button>
        <button type="button" class="quiet-button" disabled={busy} on:click={() => { text = saved; editing = false; }}>Cancel</button>
      </div>
    </form>
  {/if}
  {#if error}<p class="error" role="alert">{error}</p>{/if}
</section>

<style>
  p { margin: 4px 0; }
  ul { list-style: none; margin: 8px 0 0; padding: 0; }
  li { border-left: 2px solid var(--line); margin: 6px 0; padding: 2px 10px; }
  form { display: grid; gap: 6px; margin-top: 6px; }
  textarea { width: 100%; }
  .row { align-items: center; display: flex; flex-wrap: wrap; gap: 8px; }
  .muted { color: var(--muted); }
  .small { font-size: .78rem; }
  .error { background: var(--soft); border-left: 4px solid var(--fg); margin: 8px 0; padding: 9px; }
  .sr-only { height: 1px; margin: -1px; overflow: hidden; position: absolute; width: 1px; }
</style>
