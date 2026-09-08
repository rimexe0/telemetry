<script lang="ts">
  import type { Observation } from './api';
  import { describeEvent, formatDuration, type EventStage } from './event-detail';

  export let observation: Observation;
  export let observations: Observation[] = [];
  export let compact = false;

  $: description = describeEvent(observation, observations);
  $: stageStart = description.stages.length ? Math.min(...description.stages.map(stage => stage.start)) : 0;
  $: stageEnd = description.stages.length ? Math.max(...description.stages.map(stage => stage.end)) : 1;
  $: stageSpan = Math.max(1, stageEnd - stageStart);

  const stageY: Record<string, number> = { Light: 10, Deep: 38, REM: 66, Awake: 94, Sleeping: 122, Unknown: 150 };

  function stageX(stage: EventStage, start: number, span: number) {
    return ((stage.start - start) / span) * 960;
  }

  function stageWidth(stage: EventStage, span: number) {
    return Math.max(2, ((stage.end - stage.start) / span) * 960);
  }

  function stageClass(label: string) {
    return `stage-${label.toLowerCase().replaceAll(/[^a-z0-9]+/g, '-')}`;
  }

  function stageClock(time: number) {
    return new Intl.DateTimeFormat('en-GB', { hour: '2-digit', minute: '2-digit', timeZone: 'Europe/Istanbul' }).format(new Date(time));
  }

  function stageText(stage: EventStage) {
    return `${stage.label} · ${stageClock(stage.start)}–${stageClock(stage.end)} · ${formatDuration(stage.end - stage.start)}`;
  }

  function sourceJSON(value: unknown) {
    return JSON.stringify(value, null, 2);
  }
</script>

<article class:compact class="event-detail">
  <header class="event-heading">
    <div>
      <h2>{description.title}</h2>
      {#if description.subtitle}<p>{description.subtitle}</p>{/if}
    </div>
  </header>

  {#if description.fields.length}
    <dl class="fields">
      {#each description.fields as field}
        <div class="field">
          <dt>{field.label}</dt>
          <dd>{field.value}</dd>
        </div>
      {/each}
    </dl>
  {/if}

  {#if description.stages.length}
    <section class="stage-section" aria-labelledby="sleep-stages-heading">
      <div class="section-heading">
        <h3 id="sleep-stages-heading">Sleep stages</h3>
        <span>{formatDuration(stageEnd - stageStart)}</span>
      </div>
      <svg class="stage-chart" viewBox="0 0 960 178" role="img" aria-label="Sleep stage timeline">
        <line class="stage-axis" x1="0" y1="172" x2="960" y2="172" />
        {#each description.stages as stage}
          <rect class={`stage ${stageClass(stage.label)}`} x={stageX(stage, stageStart, stageSpan)} y={stageY[stage.label] ?? stageY.Unknown} width={stageWidth(stage, stageSpan)} height="20" rx="2" aria-label={stageText(stage)}>
            <title>{stageText(stage)}</title>
          </rect>
        {/each}
      </svg>
      <div class="stage-times"><span>{new Date(stageStart).toLocaleTimeString('en-GB', {timeZone:'Europe/Istanbul',hour:'2-digit',minute:'2-digit'})}</span><span>{new Date(stageEnd).toLocaleTimeString('en-GB', {timeZone:'Europe/Istanbul',hour:'2-digit',minute:'2-digit'})}</span></div>
      <div class="stage-legend" aria-label="Sleep stage legend">
        {#each [...new Set(description.stages.map(stage => stage.label))] as label}
          <span><i class={`legend-swatch ${stageClass(label)}`}></i>{label}</span>
        {/each}
      </div>
    </section>
  {/if}

  <details class="source">
    <summary>Source</summary>
    <dl class="source-fields">
      <div><dt>Kind</dt><dd>{observation.source_kind}</dd></div>
      <div><dt>Device</dt><dd>{observation.device_id}</dd></div>
      <div><dt>Record</dt><dd>{observation.source_record_key}</dd></div>
      <div><dt>Collected</dt><dd>{observation.collected_at}</dd></div>
      <div><dt>Revision</dt><dd>{observation.revision_digest}</dd></div>
    </dl>
    <pre>{sourceJSON(observation.original_payload)}</pre>
  </details>
</article>

<style>
  .stage-times { display:flex; justify-content:space-between; color:var(--muted); font-size:11px; margin:3px 0 8px; }
  .event-detail {
    --event-bg: var(--panel, #111);
    --event-fg: var(--fg, #eee);
    --event-muted: var(--muted, #a6a6a6);
    --event-line: var(--line, #3b3b3b);
    background: var(--event-bg);
    color: var(--event-fg);
    border: 1px solid var(--event-line);
    padding: 1rem;
  }

  .event-heading,
  .section-heading {
    align-items: baseline;
    display: flex;
    gap: 1rem;
    justify-content: space-between;
  }

  h2,
  h3,
  p {
    margin: 0;
  }

  h2 {
    font-size: 1.25rem;
    letter-spacing: -0.02em;
  }

  h3 {
    font-size: 0.82rem;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }

  .event-heading p,
  .section-heading span {
    color: var(--event-muted);
    font-size: 0.78rem;
  }



  .fields {
    border-top: 1px solid var(--event-line);
    display: grid;
    gap: 0.65rem 1rem;
    grid-template-columns: repeat(auto-fit, minmax(9rem, 1fr));
    margin: 1rem 0 0;
    padding-top: 0.9rem;
  }

  .field dt,
  .source-fields dt {
    color: var(--event-muted);
    font-size: 0.68rem;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }

  .field dd,
  .source-fields dd {
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
    font-size: 0.82rem;
    margin: 0.15rem 0 0;
    overflow-wrap: anywhere;
  }

  .stage-section {
    border-top: 1px solid var(--event-line);
    margin-top: 1rem;
    padding-top: 0.9rem;
  }

  .stage-chart {
    display: block;
    height: auto;
    margin-top: 0.65rem;
    max-height: 10rem;
    width: 100%;
  }

  .stage-axis {
    stroke: var(--event-line);
  }

  .stage {
    fill: var(--soft, #4a4a4a);
    outline: none;
  }

  .stage-light,
  .legend-swatch.stage-light { fill: #b7b7b7; background: #b7b7b7; }
  .stage-deep,
  .legend-swatch.stage-deep { fill: #666; background: #666; }
  .stage-rem,
  .legend-swatch.stage-rem { fill: #8f8f8f; background: #8f8f8f; }
  .stage-awake,
  .legend-swatch.stage-awake { fill: #ededed; background: #ededed; }
  .stage-sleeping,
  .legend-swatch.stage-sleeping { fill: #999; background: #999; }
  .stage-unknown,
  .legend-swatch.stage-unknown { fill: #3b3b3b; background: #3b3b3b; }

  .stage:hover,
  .stage:focus-visible {
    stroke: var(--event-fg);
    stroke-width: 2;
  }

  .stage-legend {
    display: flex;
    flex-wrap: wrap;
    gap: 0.65rem 1rem;
    margin-top: 0.35rem;
  }

  .stage-legend span {
    align-items: center;
    color: var(--event-muted);
    display: inline-flex;
    font-size: 0.72rem;
    gap: 0.35rem;
  }

  .legend-swatch {
    border: 1px solid var(--event-line);
    display: inline-block;
    height: 0.55rem;
    width: 0.55rem;
  }

  .source {
    border-top: 1px solid var(--event-line);
    margin-top: 1rem;
    padding-top: 0.7rem;
  }

  .source summary {
    color: var(--event-muted);
    cursor: pointer;
    font-size: 0.75rem;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }

  .source-fields {
    display: grid;
    gap: 0.5rem 1rem;
    grid-template-columns: repeat(auto-fit, minmax(11rem, 1fr));
    margin: 0.8rem 0;
  }

  .source-fields div {
    min-width: 0;
  }

  pre {
    background: color-mix(in srgb, var(--event-fg) 6%, transparent);
    border: 1px solid var(--event-line);
    font: 0.7rem/1.45 ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
    max-height: 18rem;
    overflow: auto;
    padding: 0.7rem;
    white-space: pre-wrap;
    word-break: break-word;
  }

  .compact {
    padding: 0.75rem;
  }

  .compact .fields {
    gap: 0.45rem 0.8rem;
    margin-top: 0.75rem;
    padding-top: 0.7rem;
  }

  .compact .stage-section,
  .compact .source {
    margin-top: 0.75rem;
    padding-top: 0.7rem;
  }

  @media (max-width: 560px) {
    .event-heading {
      align-items: flex-start;
      flex-direction: column;
      gap: 0.3rem;
    }
  }
</style>
