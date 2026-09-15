<script lang="ts">
  import { onMount } from 'svelte';
  import type { CollectionHealth, Observation } from './api';
  import { bytes, collectionDevice, reportLabels, samplePoints, sourceName, stateLabel,
    stateName, stateNeedsAttention, when } from './collection';

  export let data: CollectionHealth | undefined;
  export let loading = false;
  export let error = '';
  export let selectedSource = '';
  export let onsource: (key: string) => void;
  export let oninspect: (observation: Observation) => void;
  export let onrefresh: () => void;
  let selectedDevice = '';
  let now = Date.now();
  onMount(() => {
    const timer = setInterval(() => { now = Date.now(); }, 30_000);
    return () => clearInterval(timer);
  });
  $: if (selectedDevice && !data?.devices.some(d => d.device_id === selectedDevice)) selectedDevice = '';
  $: devices = (data?.devices ?? []).filter(d => !selectedDevice || d.device_id === selectedDevice)
    .map(d => collectionDevice(d, now));
  $: sources = (data?.sources ?? []).filter(s => !selectedDevice || s.device_id === selectedDevice);
  $: reportCount = devices.filter(d => d.reportState === 'recent').length;
</script>

<section class="collection-health" aria-labelledby="collection-health-title">
  <div class="health-heading">
    <div><h2 id="collection-health-title">Collection health</h2>
      <p>Uploads, collector reports and source coverage.</p></div>
    <div class="health-actions">
    {#if data && data.devices.length > 1}
      <label>Report device
        <select bind:value={selectedDevice}>
          <option value="">All devices</option>
          {#each data.devices as device}<option value={device.device_id}>{device.device_id}</option>{/each}
        </select>
      </label>
    {/if}
    <button class="refresh" on:click={onrefresh} disabled={loading}>Refresh collection</button>
    </div>
  </div>
  {#if error}<p class="notice problem" role="alert">{error}{data ? ' Last successful results remain below.' : ''}</p>{/if}
  {#if !data}
    <p class="notice" role="status">{loading ? 'Loading collection reports…' : 'Collection reports are unavailable.'}</p>
  {:else}
    <div class="collection-summary">
      <span><strong>{devices.length}</strong> recorded {devices.length === 1 ? 'device' : 'devices'}</span>
      <span><strong>{sources.length}</strong> recorded {sources.length === 1 ? 'source' : 'sources'}</span>
      <span><strong>{reportCount}</strong> recent collector {reportCount === 1 ? 'report' : 'reports'}</span>
      <span class="checked" role="status">{loading ? 'Refreshing…' : `Read ${when(data.checked_at)}`}</span>
    </div>
    {#if data.sources_truncated}
      <p class="notice problem">This response reached the source limit. Additional sources and devices may be absent.</p>
    {/if}
    <p class="explanation">A recent report is less than 15 minutes old. Source silence does not mean inactivity; health and location depend on their providers.</p>
    <div class="device-grid">
      {#each devices as device (device.device.device_id)}
        {@const sample = device.latest}
        {@const temperature = samplePoints(device.samples, 'temperature')}
        {@const pending = samplePoints(device.samples, 'pending')}
        <article class="device-card">
          <div class="device-heading">
            <h3>{device.device.device_id}</h3>
            <span class="badge" class:problem={['delayed', 'clock', 'unreadable'].includes(device.reportState)}>{reportLabels[device.reportState]}</span>
          </div>
          <p class="receipt">Last server receipt <strong>{when(device.device.last_received_at)}</strong></p>
          {#if sample}
            <p class="receipt">Collector report <strong>{when(sample.observation.collected_at)}</strong></p>
            {#if device.reportState !== 'recent'}
              <p class="notice">These are the last reported values. The device's current state is unknown.</p>
            {/if}
            <div class="metrics">
              <div><span>Pending revisions</span><strong class:attention={sample.pending !== null && sample.pending > 0}>{sample.pending?.toLocaleString() ?? 'Unknown'}</strong></div>
              <div><span>Upload state</span><strong class="status-value" class:attention={stateNeedsAttention(sample.delivery ?? '')}>{stateLabel(sample.delivery)}</strong></div>
              <div><span>Battery</span><strong>{sample.battery === null ? 'Unknown' : `${sample.battery.toFixed(0)}%`}</strong><small>{sample.charging === null ? 'Power source unknown' : sample.charging ? 'Connected to power' : 'Unplugged'}</small></div>
              <div><span>Battery temperature</span><strong>{sample.temperature === null ? 'Unknown' : `${sample.temperature.toFixed(1)} °C`}</strong></div>
            </div>
            {#if sample.constrainedReason}<p class="notice">Collection slowed: {stateLabel(sample.constrainedReason)}.</p>{/if}
            <dl class="details">
              <div><dt>Collection mode</dt><dd>{sample.foreground === null ? 'Unknown' : sample.foreground ? 'Fresh collection' : 'Scheduled fallback'}{sample.cycleSeconds === null ? '' : ` · ${sample.cycleSeconds}s target`}</dd></div>
              <div><dt>Health check target</dt><dd>{sample.healthMinutes === null ? 'Unknown' : `${sample.healthMinutes} min`}</dd></div>
              <div><dt>Last acknowledged upload</dt><dd>{when(sample.lastUploadAt)}</dd></div>
              {#if sample.pending !== null && sample.pending > 0}<div><dt>Oldest pending capture</dt><dd>{when(sample.oldestPendingAt)}</dd></div>{/if}
              <div><dt>Process CPU / memory</dt><dd>{sample.cpu === null ? 'Unknown' : `${sample.cpu.toFixed(2)}% of all cores`} / {bytes(sample.rss)}</dd></div>
              <div><dt>Network</dt><dd>{sample.connected === null ? 'Unknown' : !sample.connected ? 'Unavailable' : sample.metered === null ? 'Available' : sample.metered ? 'Metered' : 'Unmetered'}</dd></div>
              <div><dt>Failed upload batches, cumulative</dt><dd>{sample.failedBatches?.toLocaleString() ?? 'Unknown'}</dd></div>
              <div><dt>Last GPS fix</dt><dd>{when(sample.lastLocationAt)}</dd></div>
            </dl>
            {#if sample.states.length}
              <details class="source-checks" open>
                <summary>Source checks at last report</summary>
                <dl>{#each sample.states.filter(s => !s.name.endsWith('_availability')) as state}
                  {@const availability = sample.states.find(s => s.name === `${state.name}_availability`)}
                  <div><dt>{stateName(state.name)}</dt><dd class:attention={stateNeedsAttention(state.value)}>{stateLabel(state.value)}
                    {#if availability}<small>{stateLabel(availability.value)}</small>{/if}
                  </dd></div>
                {/each}</dl>
              </details>
            {/if}
            <details class="resource-history">
              <summary>Resource history and traffic · {device.samples.length} reports</summary>
              <p class="explanation">Latest retained reports, {when(device.samples[0]?.observation.collected_at)} to {when(sample.observation.collected_at)}. Points mark actual samples; gaps are unmeasured.</p>
              <div class="plots">
                {#each [{ label: 'Battery temperature', graph: temperature, suffix: ' °C' }, { label: 'Pending revisions', graph: pending, suffix: '' }] as plot}
                  <figure><figcaption>{plot.label}<span>{plot.graph.minimum === null ? 'No measurements' : `${plot.graph.minimum}${plot.suffix} to ${plot.graph.maximum}${plot.suffix}`}</span></figcaption>
                    <svg viewBox="0 0 300 60" role="img" aria-label={`${plot.label}, ${plot.graph.points.length} observed samples`}>
                      <line x1="0" y1="59" x2="300" y2="59" />
                      {#each plot.graph.points as point}<circle cx={point.x} cy={point.y} r="2.5"><title>{when(point.at)}: {point.value}{plot.suffix}</title></circle>{/each}
                    </svg>
                  </figure>
                {/each}
              </div>
              <dl class="details">
                <div><dt>App traffic since device boot</dt><dd>Sent {bytes(sample.tx)} / received {bytes(sample.rx)}</dd></div>
                <div><dt>Upload bodies attempted, cumulative</dt><dd>{bytes(sample.attemptedBytes)}</dd></div>
                <div><dt>Metered upload bodies, report's UTC day</dt><dd>{bytes(sample.meteredBytes)}</dd></div>
              </dl>
              <p class="explanation">Upload-body counters include retries and exclude headers, downloads and VPN overhead. They are not a mobile billing total. Failed-batch counts are cumulative, not current failures.</p>
              <button class="inspect" on:click={() => oninspect(sample.observation)}>Inspect latest report</button>
            </details>
          {:else}
            <p class="notice">No collector resource report is available for this device. Server receipts show delivery only. Battery, queue and permissions are unknown here.</p>
          {/if}
        </article>
      {/each}
    </div>
    <div class="source-table-wrap">
      <table>
        <caption>Recorded sources <span>Select a source to inspect its observations below.</span></caption>
        <thead><tr><th>Source</th><th>Device</th><th>Last capture</th><th>Retained records</th></tr></thead>
        <tbody>{#each sources as source (source.source_instance_key)}
          <tr class:chosen={selectedSource === source.source_instance_key}>
            <td><button class="source-link" aria-pressed={selectedSource === source.source_instance_key} on:click={() => onsource(source.source_instance_key)}>{sourceName(source)}</button></td>
            <td>{source.device_id}</td><td>{when(source.last_collected_at)}</td><td>{source.observation_count.toLocaleString()}</td>
          </tr>
        {/each}</tbody>
      </table>
      {#if sources.length === 0}<p class="notice">No sources have been recorded in this response.</p>{/if}
    </div>
  {/if}
</section>

<style>
  .collection-health { margin: 20px 0 28px; }
  .health-heading, .device-heading { display: flex; gap: 12px; align-items: start; justify-content: space-between; }
  .health-actions { display: flex; flex-wrap: wrap; align-items: end; gap: 10px; }
  .refresh { font: inherit; font-size: .74rem; border: 1px solid var(--line); padding: 8px 10px; background: var(--panel); color: var(--fg); cursor: pointer; }
  .refresh:disabled { opacity: .5; cursor: default; }
  h2 { font-size: 1.15rem; margin: 0; }
  h3 { font-size: .9rem; margin: 0; overflow-wrap: anywhere; }
  p { margin: 6px 0; }
  .health-heading p, .explanation, small, .receipt { color: var(--muted); font-size: .76rem; line-height: 1.5; }
  label { display: grid; gap: 4px; font-size: .7rem; }
  select { max-width: 250px; background: var(--panel); color: var(--fg); border: 1px solid var(--line); padding: 8px; }
  .collection-summary { display: flex; flex-wrap: wrap; gap: 12px 24px; border-block: 1px solid var(--line); margin: 14px 0 8px; padding: 12px 0; font-size: .8rem; }
  .collection-summary strong { font-size: 1.2rem; margin-right: 4px; }
  .checked { margin-left: auto; color: var(--muted); align-self: center; }
  .device-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 390px), 1fr)); gap: 12px; margin: 16px 0; align-items: start; }
  .device-card { border: 1px solid var(--line); background: var(--panel); padding: 18px; min-width: 0; }
  .device-heading { align-items: center; margin-bottom: 10px; }
  .badge { border: 1px solid var(--line); padding: 3px 7px; font-size: .68rem; white-space: nowrap; }
  .problem { border-left: 3px solid var(--fg); background: var(--soft); }
  .notice { padding: 10px; background: var(--soft); font-size: .78rem; line-height: 1.5; }
  .metrics { display: grid; grid-template-columns: 1fr 1fr; gap: 18px 12px; margin: 20px 0; }
  .metrics div { display: grid; align-content: start; gap: 4px; }
  .metrics span { font-size: .7rem; color: var(--muted); }
  .metrics strong { font-size: 1.4rem; font-variant-numeric: tabular-nums; line-height: 1.2; }
  .metrics .status-value { font-size: 1rem; }
  .attention { text-decoration: underline dotted; text-underline-offset: 4px; }
  dl { margin: 0; font-size: .73rem; }
  dl div { display: flex; justify-content: space-between; gap: 14px; padding: 6px 0; border-top: 1px solid var(--line); }
  dt { color: var(--muted); }
  dd { margin: 0; text-align: right; overflow-wrap: anywhere; }
  dd small { display: block; font-size: .66rem; }
  details { margin-top: 14px; }
  summary { font-size: .78rem; cursor: pointer; padding: 8px 0; }
  .plots { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; margin: 12px 0; }
  figure { margin: 0; min-width: 0; }
  figcaption { font-size: .7rem; }
  figcaption span { display: block; color: var(--muted); margin: 3px 0 8px; }
  svg { width: 100%; display: block; overflow: visible; }
  svg line { stroke: var(--line); }
  svg circle { fill: var(--fg); }
  .inspect, .source-link { color: var(--fg); background: transparent; border: 0; cursor: pointer; text-decoration: underline; text-underline-offset: 3px; padding: 4px 0; font: inherit; text-align: left; }
  .inspect { font-size: .75rem; }
  .source-table-wrap { overflow-x: auto; }
  table { width: 100%; border-collapse: collapse; font-size: .76rem; }
  caption { font-size: .9rem; text-align: left; padding: 14px 0; }
  caption span { display: block; font-size: .72rem; color: var(--muted); margin-top: 5px; }
  th, td { text-align: left; padding: 9px 12px 9px 0; border-bottom: 1px solid var(--line); }
  th { font-weight: 500; color: var(--muted); }
  td:last-child, th:last-child { text-align: right; }
  td:nth-child(2) { overflow-wrap: anywhere; }
  .chosen { background: var(--soft); }
  @media (max-width: 600px) {
    .health-heading { flex-direction: column; }
    .checked { margin-left: 0; width: 100%; }
    .device-heading { flex-wrap: wrap; }
    .device-card { padding: 13px; }
    th, td { font-size: .69rem; }
    dl div { gap: 8px; }
  }
</style>
