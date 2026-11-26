<script lang="ts">
  import type { CollectionHealth, Observation, SourceSummary } from './api';
  import CollectionStatus from './CollectionStatus.svelte';
  import TimeSeries from './TimeSeries.svelte';

  // Settings › Data sources: what each device last sent, the sources, and
  // the recent records with their revisions. The page loads the data.
  export let collectionStatus: CollectionHealth | undefined;
  export let collectionError = '';
  export let error = '';
  export let loading = false;
  export let sources: SourceSummary[] = [];
  export let observations: Observation[] = [];
  export let selectedSource = '';
  export let range: '24h' | '7d' = '24h';
  export let updatedAt: Date | undefined;
  export let onsource: (key: string) => void;
  export let onrange: (range: '24h' | '7d') => void;
  export let oninspect: (observation: Observation) => void;
  export let onrefresh: () => void;

  function date(value?: string) {
    return value ? new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short', timeZone: 'Europe/Istanbul' }).format(new Date(value)) : 'No collection yet';
  }

  function relative(value?: string) {
    if (!value) return 'Unknown';
    const minutes = Math.max(0, Math.round((Date.now() - Date.parse(value)) / 60_000));
    if (minutes < 1) return 'Now';
    if (minutes < 60) return `${minutes} min ago`;
    const hours = Math.floor(minutes / 60);
    return `${hours} hr${hours === 1 ? '' : 's'} ago`;
  }

  function sourceLabel(source: SourceSummary) {
    const key = source.source_instance_key;
    if (key.includes('aw-watcher-window')) return 'Computer windows';
    if (key.includes('aw-watcher-afk')) return 'Keyboard / mouse';
    if (key.includes('aw-watcher-web')) return 'Browser tabs';
    if (key.includes('aw-watcher-codex')) return 'Codex';
    return ({ health_connect: 'Health Connect', android_usage: 'Phone apps' } as Record<string, string>)[source.source_kind] ?? source.source_kind;
  }

  function fingerprint(value: string) {
    return value.length > 18 ? `${value.slice(0, 10)}…${value.slice(-6)}` : value;
  }
</script>

{#if error}<p class="error" role="alert">{error}</p>{/if}

<CollectionStatus data={collectionStatus} {loading} error={collectionError} {selectedSource}
  {onsource} {oninspect} {onrefresh} />

{#if !collectionStatus}<section aria-labelledby="sources-heading">
  <div class="section-title">
    <h2 id="sources-heading">Sources</h2>
    <span class="count">{sources.length} recorded</span>
  </div>
  <div class="source-grid">
    {#each sources as source (source.source_instance_key)}
      <button class:chosen={selectedSource === source.source_instance_key} class="source-card" on:click={() => onsource(source.source_instance_key)}>
        <span class="source-kind">{sourceLabel(source)}</span>
        <strong>{source.observation_count.toLocaleString()}</strong>
        <span>retained observations</span>
        <dl>
          <div><dt>Device</dt><dd>{source.device_id}</dd></div>
          <div><dt>Last collection</dt><dd>{relative(source.last_collected_at)}</dd></div>
        </dl>
      </button>
    {:else}
      <p class="empty">No sources</p>
    {/each}
  </div>
</section>{/if}

<section class="controls" aria-label="History controls">
  <div class="control-group">
    <span>Observation window</span>
    <button class:active={range === '24h'} on:click={() => onrange('24h')}>24 hours</button>
    <button class:active={range === '7d'} on:click={() => onrange('7d')}>7 days</button>
  </div>
  <p class="updated">{loading ? 'Refreshing…' : updatedAt ? `Last refreshed ${date(updatedAt.toISOString())}` : 'No data loaded'}</p>
</section>

<section class="timeline-section" aria-labelledby="timeline-heading">
  <div class="section-title">
    <h2 id="timeline-heading">Observation times</h2>
    <span class="count">{observations.length.toLocaleString()} returned in this window</span>
  </div>
  <div class="timeline-frame">
    <TimeSeries {observations} />
    {#if observations.length === 0}<p class="empty chart-empty">No records</p>{/if}
  </div>
  <p class="caption">Records/hour · Limit 1,000</p>
</section>

<section class="records" aria-labelledby="records-heading">
  <div class="section-title">
    <h2 id="records-heading">Recent records</h2>
    {#if selectedSource}<button class="quiet-button" on:click={() => onsource(selectedSource)}>Clear source filter</button>{/if}
  </div>
  <div class="table-wrap">
    <table>
      <thead><tr><th>Occurred</th><th>Source</th><th>Kind</th><th>Revision</th><th><span class="sr-only">Inspect</span></th></tr></thead>
      <tbody>
        {#each observations as observation (observation.source_instance_key + observation.source_record_key)}
          <tr>
            <td>{date(observation.occurred_at)}</td>
            <td>{observation.source_kind}</td>
            <td>{observation.kind}</td>
            <td><code>{fingerprint(observation.revision_digest)}</code></td>
            <td><button class="inspect" on:click={() => oninspect(observation)}>Inspect</button></td>
          </tr>
        {:else}
          <tr><td class="empty" colspan="5">No records</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
</section>

<style>
  h2 { font-size: 1.05rem; letter-spacing: -.02em; margin: 0; }
  .controls, .timeline-frame, .source-card, .table-wrap { border: 1px solid var(--fg); background: var(--panel); }
  .controls { display: flex; justify-content: space-between; gap: 12px; align-items: center; margin: 16px 0 26px; padding: 9px 10px; }
  .control-group { display: flex; align-items: center; flex-wrap: wrap; gap: 6px; }
  .control-group span, .updated, .count, .caption { color: var(--muted); font-size: .8rem; }
  .control-group button.active { background: var(--panel); color: var(--fg); outline: 2px solid var(--fg); outline-offset: 2px; }
  .updated { margin: 0; text-align: right; }
  .section-title { display: flex; align-items: flex-end; justify-content: space-between; gap: 12px; margin-bottom: 9px; }
  .source-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); gap: 8px; }
  .source-card { min-height: 150px; color: var(--fg); display: flex; flex-direction: column; align-items: flex-start; padding: 13px; text-align: left; }
  .source-card.chosen { outline: 2px solid var(--fg); outline-offset: 2px; }
  .source-card strong { font-size: 2rem; letter-spacing: -.08em; margin: 16px 0 1px; }
  .source-kind { font-size: .78rem; font-weight: 700; }
  dl { margin: 0; }
  .source-card dl { border-top: 1px solid var(--line); display: grid; gap: 5px; margin-top: auto; padding-top: 8px; width: 100%; }
  dl div { display: flex; justify-content: space-between; gap: 12px; }
  dt { color: var(--muted); font-size: .72rem; }
  dd { margin: 0; overflow-wrap: anywhere; text-align: right; }
  .timeline-section, .records { margin-top: 28px; }
  .timeline-frame { min-height: 260px; padding: 12px; }
  .chart-empty { margin: 0; }
  .caption { margin: 7px 0 0; line-height: 1.5; }
  .table-wrap { overflow: auto; max-height: 480px; position: relative; }
  table { border-collapse: collapse; min-width: 760px; width: 100%; }
  th, td { border-bottom: 1px solid var(--line); padding: 9px 11px; text-align: left; font-size: .78rem; }
  th { background: var(--soft); font-size: .72rem; letter-spacing: .05em; text-transform: uppercase; }
  tr:last-child td { border-bottom: 0; }
  code { font-size: .75rem; }
  .inspect { background: var(--panel); color: var(--fg); padding: 6px 9px; font-size: .75rem; }
  .empty { color: var(--muted); padding: 24px; }
  @media (max-width: 620px) {
    .controls, .section-title { align-items: flex-start; flex-direction: column; }
    .updated { text-align: left; }
  }
</style>
