<script lang="ts">
  import type { Observation } from './api';
  import DayPicker from './DayPicker.svelte';
  import { dayStart, dayKey, deriveHealth, formatMinutes, originLabel } from './health';

  export let observations: Observation[] = [];
  export let loading = false;
  export let error = '';
  export let oninspect: (observation: Observation) => void = () => undefined;

  const clock = (time: number) => new Date(time).toLocaleTimeString('en-GB', {timeZone:'Europe/Istanbul', hour:'2-digit', minute:'2-digit'});
  const DAY_MS = 24 * 60 * 60 * 1000;
  const HOUR_MS = 60 * 60 * 1000;
  const HEART_BIN_MS = 5 * 60 * 1000;
  const HEART_POINT_LIMIT = 3000;

  let selectedOrigin = '';
  let selectedDay = '';

  type HeartPoint = {
    time: number;
    bpm: number;
    observation: Observation;
    label?: string;
  };

  function validDay(value: string): boolean {
    return /^\d{4}-\d{2}-\d{2}$/.test(value) && !Number.isNaN(dayStart(value));
  }

  function shiftDay(date: string, amount: number): string {
    const shifted = new Date(dayStart(date) + amount * DAY_MS);
    return dayKey(shifted.getTime());
  }

  function dayText(date: string): string {
    const value = new Date(dayStart(date) + 12 * HOUR_MS);
    return new Intl.DateTimeFormat('en-GB', {
      timeZone: 'Europe/Istanbul',
      weekday: 'short',
      day: '2-digit',
      month: 'short'
    }).format(value);
  }

  function fullDayText(date: string): string {
    const value = new Date(dayStart(date) + 12 * HOUR_MS);
    return new Intl.DateTimeFormat('en-GB', {
      timeZone: 'Europe/Istanbul',
      weekday: 'long',
      day: 'numeric',
      month: 'long',
      year: 'numeric'
    }).format(value);
  }

  function numberText(value: number | null | undefined): string {
    return value === null || value === undefined ? '—' : value.toLocaleString('en-US');
  }

  function barHeight(value: number | null, maximum: number): number {
    if (value === null) return 1;
    return Math.max(2, Math.min(54, value / maximum * 54));
  }

  function extractNumber(value: unknown, keys: string[]): number | null {
    if (!value || typeof value !== 'object') return null;
    const record = value as Record<string, unknown>;
    for (const key of keys) {
      const candidate = record[key];
      if (typeof candidate === 'number' && Number.isFinite(candidate)) return candidate;
      if (typeof candidate === 'string' && candidate.trim() !== '' && Number.isFinite(Number(candidate))) {
        return Number(candidate);
      }
    }
    return null;
  }

  function stepRecordText(observation: Observation): string {
    const value = extractNumber(observation.original_payload, ['count']);
    return value === null ? observation.kind : `${numberText(value)} steps`;
  }

  function intervalPosition(start: number, end: number, date: string): { x: number; width: number } | null {
    const boundsStart = dayStart(date);
    const boundsEnd = boundsStart + DAY_MS;
    const clippedStart = Math.max(boundsStart, start);
    const clippedEnd = Math.min(boundsEnd, end);
    if (!Number.isFinite(clippedStart) || !Number.isFinite(clippedEnd) || clippedEnd <= clippedStart) return null;
    return {
      x: ((clippedStart - boundsStart) / DAY_MS) * 100,
      width: ((clippedEnd - clippedStart) / DAY_MS) * 100
    };
  }

  function pointX(time: number, date: string): number {
    return Math.max(0, Math.min(100, ((time - dayStart(date)) / DAY_MS) * 100));
  }

  function pointY(bpm: number, minimum: number, maximum: number): number {
    const span = Math.max(1, maximum - minimum);
    return 92 - ((bpm - minimum) / span) * 76;
  }

  function median(values: number[]): number | null {
    if (values.length === 0) return null;
    const ordered = [...values].sort((left, right) => left - right);
    const middle = Math.floor(ordered.length / 2);
    return ordered.length % 2 === 0 ? (ordered[middle - 1] + ordered[middle]) / 2 : ordered[middle];
  }

  function keydownInspect(event: KeyboardEvent, observation: Observation): void {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      oninspect(observation);
    }
  }

  function reducedHeartPoints(samples: HeartPoint[]): { points: HeartPoint[]; binned: boolean } {
    if (samples.length <= HEART_POINT_LIMIT) return { points: samples, binned: false };

    const bins = new Map<number, { low: HeartPoint; high: HeartPoint }>();
    for (const sample of samples) {
      const bucket = Math.floor(sample.time / HEART_BIN_MS) * HEART_BIN_MS;
      const current = bins.get(bucket);
      if (!current) {
        bins.set(bucket, { low: sample, high: sample });
      } else {
        if (sample.bpm < current.low.bpm) current.low = sample;
        if (sample.bpm > current.high.bpm) current.high = sample;
      }
    }

    const points: HeartPoint[] = [];
    for (const [bucket, pair] of [...bins.entries()].sort(([left], [right]) => left - right)) {
      points.push({ ...pair.low, label: '5-minute minimum' });
      if (pair.high.observation !== pair.low.observation || pair.high.bpm !== pair.low.bpm) {
        points.push({ ...pair.high, label: '5-minute maximum' });
      }
    }
    return { points, binned: true };
  }

  function sourceHasSleep(day: { sleep: unknown[]; sleepMinutes: number | null }): boolean {
    return day.sleep.length > 0 || day.sleepMinutes !== null;
  }

  $: observationDays = observations
    .map((observation) => {
      try {
        return dayKey(observation.occurred_at);
      } catch {
        return '';
      }
    })
    .filter(validDay)
    .sort();
  $: latestDay = observationDays.at(-1) ?? '';
  $: fromDay = latestDay ? shiftDay(latestDay, -29) : '';
  $: toDay = latestDay;
  $: catalogue = latestDay ? deriveHealth(observations, '', fromDay, toDay) : { days: [], origins: [], warnings: [] };
  $: origins = catalogue.origins;
  $: preferredOrigin = origins.find((origin: string) => /samsung|shealth/i.test(originLabel(origin))) ?? origins[0] ?? '';
  $: if (!origins.includes(selectedOrigin)) selectedOrigin = preferredOrigin;
  $: health = latestDay ? deriveHealth(observations, selectedOrigin, fromDay, toDay) : { days: [], origins: [], warnings: [] };
  $: days = health.days;
  $: latestDayWithSleep = [...days].reverse().find((day) => sourceHasSleep(day))?.date ?? days.at(-1)?.date ?? '';
  $: if (!selectedDay || !days.some((day) => day.date === selectedDay)) selectedDay = latestDayWithSleep;
  $: selectedHealthDay = days.find((day) => day.date === selectedDay) ?? null;
  $: dayStepMaximum = Math.max(1, ...days.map((day) => day.steps ?? 0));
  $: daySleepMaximum = Math.max(1, ...days.map((day) => day.sleepMinutes ?? 0));
  $: selectedSamples = selectedHealthDay?.samples ?? [];
  $: heartValues = selectedSamples.map((sample) => sample.bpm).filter((value) => Number.isFinite(value));
  $: heartMinimum = heartValues.length ? Math.floor(Math.min(...heartValues) - 2) : 0;
  $: heartMaximum = heartValues.length ? Math.ceil(Math.max(...heartValues) + 2) : 100;
  $: heartMedian = median(heartValues);
  $: selectedHeart = reducedHeartPoints(selectedSamples);

  function timelineSegments(day: typeof selectedHealthDay, kind: 'sleep' | 'exercise') {
    if (!day) return [];
    return day[kind]
      .map((segment) => ({ ...segment, position: intervalPosition(segment.start, segment.end, day?.date ?? '') }))
      .filter((segment) => segment.position !== null);
  }

  $: sleepSegments = timelineSegments(selectedHealthDay, 'sleep');
  $: exerciseSegments = timelineSegments(selectedHealthDay, 'exercise');
</script>

<section class="health" aria-labelledby="health-heading">
  <div class="health-heading">
    <h2 id="health-heading" class="sr-only">Health history</h2>
    <div class="health-controls">
      <label for="health-origin">Source</label>
      <select id="health-origin" bind:value={selectedOrigin} disabled={origins.length === 0}>
        {#each origins as origin}
          <option value={origin}>{originLabel(origin)}</option>
        {/each}
      </select>
      <DayPicker value={selectedDay} onchange={(date)=>selectedDay=date} disabled={!toDay || loading} />
    </div>
  </div>

  {#if loading && observations.length === 0}
    <p class="health-status">Loading health history…</p>
  {:else}
    {#if loading}<p class="health-status">Refreshing health history…</p>{/if}
    {#if error}<p class="health-error" role="alert">{error}</p>{/if}
    {#if observations.length === 0 || days.length === 0}
      {#if !loading}<div class="health-empty"><strong>No health records</strong></div>{/if}
    {:else}

    <div class="day-overview" aria-label="Thirty day health overview">
      <div class="overview-axis"><span>Daily steps · {days.some(d => d.steps !== null) ? `0–${numberText(dayStepMaximum)}` : 'no valid totals'} / Sleep session time · {days.some(d => d.sleepMinutes !== null) ? `0–${formatMinutes(daySleepMaximum)}` : 'unavailable'}</span><span>{dayText(fromDay)} → {dayText(toDay)}</span></div>
      <div class="day-grid">
        {#each days as day}
          <button
            class:selected={day.date === selectedDay}
            class:no-data={day.steps === null && day.sleepMinutes === null}
            class="day-cell"
            title={`${fullDayText(day.date)} — ${day.stepAmbiguous ? 'step total ambiguous' : day.steps === null ? 'steps unavailable' : `${numberText(day.steps)} steps`}; ${day.sleepMinutes === null ? 'sleep unavailable' : `${formatMinutes(day.sleepMinutes)} recorded sleep session time`}`}
            aria-label={`${fullDayText(day.date)}. ${day.stepAmbiguous ? 'Step total ambiguous' : day.steps === null ? 'Steps unavailable' : `${numberText(day.steps)} steps`}. ${day.sleepMinutes === null ? 'Sleep unavailable' : `${formatMinutes(day.sleepMinutes)} recorded session duration`}.`}
            on:click={() => (selectedDay = day.date)}
          >
            <svg class="day-bars" viewBox="0 0 100 54" preserveAspectRatio="none" aria-hidden="true">
              <rect class:zero={day.steps === 0} class:missing={day.steps === null} class="step-bar" x="5" y={54 - barHeight(day.steps, dayStepMaximum)} width="40" height={barHeight(day.steps, dayStepMaximum)} />
              <rect class:zero={day.sleepMinutes === 0} class:missing={day.sleepMinutes === null} class="sleep-bar" x="55" y={54 - barHeight(day.sleepMinutes, daySleepMaximum)} width="40" height={barHeight(day.sleepMinutes, daySleepMaximum)} />
            </svg>
            <span class="day-label">{day.date.slice(8)}</span>
          </button>
        {/each}
      </div>
      <div class="legend"><span><i class="legend-step"></i>steps</span><span><i class="legend-sleep"></i>sleep</span></div>
    </div>

    {#if selectedHealthDay}
      <div class="selected-header">
        <div>
          <h3>{fullDayText(selectedHealthDay.date)}</h3>
        </div>
        <div class="metric-strip">
          <div><span>Steps</span><strong>{numberText(selectedHealthDay.steps)}</strong>{#if selectedHealthDay.stepAmbiguous}<small>Overlap</small>{/if}</div>
          <div><span>Sleep</span><strong>{selectedHealthDay.sleepMinutes === null ? '—' : formatMinutes(selectedHealthDay.sleepMinutes)}</strong><small>session duration</small></div>
          <div><span>Exercise</span><strong>{selectedHealthDay.exerciseMinutes === null ? '—' : formatMinutes(selectedHealthDay.exerciseMinutes)}</strong></div>
          <div><span>Heart rate</span><strong>{heartValues.length ? `${Math.round(Math.min(...heartValues))}–${Math.round(Math.max(...heartValues))}` : '—'}</strong><small>{heartValues.length ? `${heartValues.length} samples · median ${Math.round(heartMedian ?? 0)} bpm` : 'no samples'}</small></div>
        </div>
      </div>

      <div class="detail-grid">
        <section class="detail-card" aria-labelledby="timeline-title">
          <div class="card-title"><h4 id="timeline-title">Day timeline</h4><span>00:00 → 24:00 Istanbul</span></div>
          <div class="timeline">
            <svg viewBox="-55 0 1060 118" role="group" aria-label="Selected day sleep and exercise intervals">
              {#each [0, 6, 12, 18, 24] as hour}
                <line class="grid-line" x1={(hour / 24) * 1000} x2={(hour / 24) * 1000} y1="8" y2="103"></line>
                <text class="axis-label" x={(hour / 24) * 1000 + (hour === 24 ? -5 : 4)} y="115" text-anchor={hour === 24 ? 'end' : 'start'}>{String(hour).padStart(2, '0')}</text>
              {/each}
              <text class="row-label" x="0" y="10">sleep</text>
              <text class="row-label" x="0" y="56">exercise</text>
              {#each sleepSegments as segment}
                <rect class="interval sleep" tabindex="0" role="button" aria-label={`Sleep session ${clock(segment.start)} to ${clock(segment.end)}, ${formatMinutes(Math.round(((segment.end ?? 0) - (segment.start ?? 0)) / 60000))}; inspect record`} x={(segment.position?.x ?? 0) * 10} y="12" width={(segment.position?.width ?? 0) * 10} height="24" on:click={() => oninspect(segment.observation)} on:keydown={(event) => keydownInspect(event, segment.observation)}></rect>
              {/each}
              {#each exerciseSegments as segment}
                <rect class="interval exercise" tabindex="0" role="button" aria-label={`Exercise session ${clock(segment.start)} to ${clock(segment.end)}, ${formatMinutes(Math.round(((segment.end ?? 0) - (segment.start ?? 0)) / 60000))}; inspect record`} x={(segment.position?.x ?? 0) * 10} y="58" width={(segment.position?.width ?? 0) * 10} height="24" on:click={() => oninspect(segment.observation)} on:keydown={(event) => keydownInspect(event, segment.observation)}></rect>
              {/each}
            </svg>
          </div>
          {#if sleepSegments.length === 0 && exerciseSegments.length === 0}<p class="card-empty">No recorded sleep or exercise interval for this day.</p>{/if}
        </section>

        <section class="detail-card" aria-labelledby="heart-title">
          <div class="card-title"><h4 id="heart-title">Heart rate samples</h4><span>{selectedHeart.binned ? '5-minute min/max bins' : 'individual samples'}</span></div>
          {#if selectedHeart.points.length > 0}
            <div class="heart-chart">
              <svg viewBox="-55 0 1060 125" role="group" aria-label={`Heart rate samples for ${fullDayText(selectedHealthDay.date)}`}>
                <line class="chart-axis" x1="0" y1="100" x2="1000" y2="100"></line>
                <line class="chart-axis" x1="0" y1="8" x2="0" y2="100"></line>
                <text class="chart-label" x="-52" y="16">{heartMaximum} bpm</text>
                <text class="chart-label" x="-52" y="92">{heartMinimum} bpm</text>
                {#each selectedHeart.points as point, index}
                  <circle
                    class="heart-point"
                    cx={(pointX(point.time, selectedHealthDay.date) / 100) * 1000}
                    cy={pointY(point.bpm, heartMinimum, heartMaximum)}
                    r={selectedHeart.binned ? 3 : 2.25}
                    tabindex="0"
                    role="button"
                    aria-label={`${new Date(point.time).toLocaleTimeString('en-GB', {timeZone:'Europe/Istanbul'})}, ${Math.round(point.bpm)} bpm${point.label ? `, ${point.label}` : ''}; inspect sample`}
                    on:click={() => oninspect(point.observation)}
                    on:keydown={(event) => keydownInspect(event, point.observation)}
                  ><title>{new Date(point.time).toLocaleTimeString('en-GB', {timeZone:'Europe/Istanbul'})} · {point.bpm} bpm</title></circle>
                {/each}
                {#each [0, 6, 12, 18, 24] as hour}
                  <text class="chart-label" x={(hour / 24) * 1000 + (hour === 24 ? -4 : 4)} y="117" text-anchor={hour === 24 ? 'end' : 'start'}>{String(hour).padStart(2, '0')}</text>
                {/each}
              </svg>
            </div>
            <div class="heart-stats"><span>min <strong>{Math.round(Math.min(...heartValues))}</strong></span><span>median <strong>{Math.round(heartMedian ?? 0)}</strong></span><span>max <strong>{Math.round(Math.max(...heartValues))}</strong></span><span>{heartValues.length.toLocaleString()} samples</span></div>
          {:else}
            <p class="card-empty">No heart-rate samples for this day.</p>
          {/if}
        </section>
      </div>

      {#if selectedHealthDay.stepRecords.length > 0}
        <section class="record-card" aria-labelledby="step-record-title">
          <div class="card-title"><h4 id="step-record-title">Step records</h4><span>{selectedHealthDay.stepRecords.length} retained record{selectedHealthDay.stepRecords.length === 1 ? '' : 's'}</span></div>
          <div class="record-list">
            {#each selectedHealthDay.stepRecords as record, index}
              <button class="record-button" title="Inspect original step record" on:click={() => oninspect(record)}><span>{stepRecordText(record)}</span><small>record {index + 1}</small></button>
            {/each}
          </div>
        </section>
      {/if}
    {/if}

    {/if}
  {/if}
</section>

<style>
  .health { border-top: 1px solid var(--fg); margin-top: 10px; padding-top: 10px; }
  .sr-only { position:absolute; width:1px; height:1px; overflow:hidden; clip-path:inset(50%); }
  .health-heading, .selected-header, .card-title, .overview-axis { align-items: flex-end; display: flex; gap: 12px; justify-content: space-between; }
  h2, h3, h4, p { margin-top: 0; }
  h2 { font-size: 1.35rem; letter-spacing: -.05em; margin-bottom: 0; }
  h3 { font-size: 1.05rem; letter-spacing: -.04em; margin-bottom: 0; }
  h4 { font-size: .78rem; letter-spacing: .04em; margin: 0; text-transform: uppercase; }
  .health-controls { align-items: center; display: flex; flex-wrap: wrap; gap: 6px; }
  .health-controls label { color: var(--muted); font-size: .68rem; }
  select { background: var(--panel); border: 1px solid var(--fg); border-radius: 0; color: var(--fg); font: inherit; font-size: .72rem; padding: 6px 7px; }
  .health-status, .health-error { border-left: 3px solid var(--fg); font-size: .76rem; margin: 12px 0 0; padding: 8px 10px; }
  .health-error { background: var(--soft); }
  .health-empty { align-items: baseline; background: var(--panel); border: 1px solid var(--fg); display: flex; flex-wrap: wrap; gap: 10px; margin-top: 12px; padding: 14px; }
  .day-overview, .detail-card, .record-card { background: var(--panel); border: 1px solid var(--fg); margin-top: 12px; padding: 11px; }
  .overview-axis { color: var(--muted); font-size: .67rem; margin-bottom: 8px; }
  .day-grid { display: grid; gap: 3px; grid-template-columns: repeat(30, minmax(22px, 1fr)); }
  .day-cell { background: var(--soft); border: 1px solid var(--line); border-radius: 0; color: var(--fg); cursor: pointer; min-width: 0; padding: 4px 2px 3px; }
  .day-cell:hover, .day-cell:focus-visible, .day-cell.selected { background: var(--panel); border-color: var(--fg); outline: 2px solid var(--fg); outline-offset: 1px; }
  .day-cell.no-data { background: repeating-linear-gradient(135deg, var(--soft), var(--soft) 4px, var(--line) 4px, var(--line) 5px); }
  .day-bars { display:block; height:54px; width:100%; }
  .step-bar { fill:var(--fg); }
  .sleep-bar { fill:var(--muted); }
  .day-bars .missing { fill:none; stroke:var(--muted); stroke-width:1; stroke-dasharray:4 4; }
  .day-bars .zero { fill:var(--panel); stroke:var(--fg); stroke-width:2; }
  .day-label { display: block; font-size: .58rem; line-height: 1.2; margin-top: 3px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .legend { color: var(--muted); display: flex; flex-wrap: wrap; font-size: .65rem; gap: 12px; margin-top: 8px; }
  .legend span { align-items: center; display: inline-flex; gap: 4px; }
  .legend i { display: inline-block; height: 8px; width: 8px; }
  .legend-step { background: var(--fg); }
  .legend-sleep { background: var(--muted); }
  .selected-header { align-items: flex-start; margin-top: 14px; }
  .metric-strip { display: grid; gap: 6px; grid-template-columns: repeat(4, minmax(100px, 1fr)); width: min(780px, 100%); }
  .metric-strip > div { border-left: 1px solid var(--line); display: grid; gap: 2px; padding-left: 8px; }
  .metric-strip span, .metric-strip small, .card-title > span { color: var(--muted); font-size: .64rem; }
  .metric-strip strong { font-size: 1.05rem; letter-spacing: -.05em; }
  .metric-strip small { line-height: 1.25; }
  .detail-grid { display: grid; gap: 12px; grid-template-columns: minmax(0, 1fr); margin-top: 12px; }
  .card-title { align-items: baseline; border-bottom: 1px solid var(--line); margin-bottom: 8px; padding-bottom: 7px; }
  .timeline, .heart-chart { overflow: hidden; width: 100%; }
  svg { display: block; height: auto; width: 100%; }
  .grid-line, .chart-axis { stroke: var(--line); stroke-width: 1; }
  .axis-label, .chart-label, .row-label { fill: var(--muted); font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12px; }
  .row-label { fill: var(--fg); font-size: 11px; }
  .interval { cursor: pointer; stroke: var(--fg); stroke-width: 1; }
  .interval.sleep { fill: var(--fg); }
  .interval.exercise { fill: var(--muted); }
  .interval:focus, .interval:hover { fill: var(--panel); stroke-width: 2; }
  .heart-point { cursor: pointer; fill: var(--panel); stroke: var(--fg); stroke-width: 1.25; }
  .heart-point:hover, .heart-point:focus { fill: var(--fg); r: 4; }
  .heart-stats { display: flex; flex-wrap: wrap; font-size: .68rem; gap: 12px; margin-top: 3px; }
  .heart-stats span { color: var(--muted); }
  .heart-stats strong { color: var(--fg); }
  .card-empty { color: var(--muted); font-size: .74rem; margin: 24px 0; }
  .record-list { display: flex; flex-wrap: wrap; gap: 5px; }
  .record-button { align-items: flex-start; background: var(--panel); border: 1px solid var(--fg); color: var(--fg); cursor: pointer; display: flex; flex-direction: column; gap: 2px; padding: 6px 8px; text-align: left; }
  .record-button:hover, .record-button:focus-visible { background: var(--fg); color: var(--panel); outline: 2px solid var(--fg); outline-offset: 1px; }
  .record-button small { color: var(--muted); font-size: .6rem; }
  .record-button:hover small, .record-button:focus-visible small { color: var(--muted); }
  @media (max-width: 900px) {
    .axis-label, .chart-label, .row-label { font-size:20px; }
    .health-heading, .selected-header { align-items: flex-start; flex-direction: column; }
    .health-controls { width: 100%; }
    .metric-strip { grid-template-columns: repeat(2, minmax(100px, 1fr)); width: 100%; }
    .detail-grid { grid-template-columns: 1fr; }
  }
  @media (max-width: 620px) {
    .day-grid { gap: 2px; grid-template-columns: repeat(15, minmax(20px, 1fr)); }
    .day-cell:nth-child(n + 16) { margin-top: 2px; }
    .overview-axis { align-items: flex-start; flex-direction: column; }
    .metric-strip { grid-template-columns: repeat(2, minmax(0, 1fr)); }
    .metric-strip > div { padding-left: 5px; }
  }
</style>
