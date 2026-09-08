<script lang="ts">
  import type { Observation, SourceSummary } from './api';
  import DayPicker from './DayPicker.svelte';
  import { tick } from 'svelte';
  import { moveWindow, resizeWindow, tooltipPosition } from './timeline-window';
  import { clock, deriveDay } from './day';
  import { formatDuration, describeEvent } from './event-detail';

  export let observations: Observation[] = [];
  export let sources: SourceSummary[] = [];
  export let date = '';
  export let loading = false;
  export let error = '';
  export let status = '';
  export let oninspect: (observation: Observation) => void = () => undefined;
  export let ondatechange: () => void = () => undefined;

  type DaySegment = { start: number; end: number; label: string; detail: string; evidence: Observation[]; kind: string };
  type DayLane = { name: string; note: string; segments: DaySegment[] };
  type DayModel = { lanes: DayLane[]; chapters: DaySegment[]; apps: { name: string; minutes: number }[]; facts: string[]; warnings: string[] };


  const hour = 3_600_000;
  const dayLength = 24 * hour;
  const plotLeft = 142;
  const plotRight = 18;
  const plotWidth = 1_030;
  const rowHeight = 34;
  const rowGap = 6;
  const timelineTop = 38;
  const timelineBottom = 18;

  let zoomHours = 24;
  let centerHour = 12;
  let previousDate = '';
  let dragging = false;
  let dragX = 0;
  let activeSegment: DaySegment | null = null;
  let tooltip: HTMLDivElement;
  let tooltipX=0, tooltipY=0;
  let search='';
  let hiddenLanes:string[]=[];
  let density:'compact'|'records'='compact';
  let brushMode:'start'|'end'|'move'|null=null;
  let brushOrigin=0;
  let brushWindow={start:0,end:dayLength};
  $: filteredLanes=model.lanes.filter(lane=>!hiddenLanes.includes(lane.name)).map(lane=>{
    const matching=lane.segments.filter(s=>!search || `${s.label} ${s.detail}`.toLowerCase().includes(search.toLowerCase()));
    const canCompact=!/^(Sleep|Exercise)/.test(lane.name);
    return {...lane,segments:density==='compact' && canCompact ? compactSegments(matching) : matching};
  });
  $: summary=activeSegment?.evidence[0] ? describeEvent(activeSegment.evidence[0],observations) : null;
  $: tooltipFields=summary?.fields.filter(f=>['Light','Deep','REM','Heart-rate range'].includes(f.label)).slice(0,3) ?? [];
  $: sleepTotal=laneDuration(model.lanes,'Sleep');
  $: exerciseTotal=laneDuration(model.lanes,'Exercise');
  $: computerTotal=laneDuration(model.lanes,'Computer');
  $: phoneCount=laneCount(model.lanes,'Phone');
  $: browserTotal=laneDuration(model.lanes,'Browser');


  $: displayedDateTitle = dateTitle(date);
  $: model = deriveDay(observations, sources, date) as DayModel;
  $: dayStart = Date.parse(`${date}T00:00:00+03:00`);
  $: dayEnd = dayStart + dayLength;
  $: if (date !== previousDate) {
    previousDate = date;
    activeSegment = null;
  }
  $: visibleSpan = Math.min(dayLength, Math.max(60_000, zoomHours * hour));
  $: visibleStart = clamp(dayStart + centerHour * hour - visibleSpan / 2, dayStart, dayEnd - visibleSpan);
  $: visibleEnd = visibleStart + visibleSpan;
  $: visibleHours = hourTicks(dayStart, visibleStart, visibleEnd, zoomHours);
  $: laneHeight = Math.max(1, filteredLanes.length) * (rowHeight + rowGap);
  $: svgHeight = timelineTop + laneHeight + timelineBottom;

  function clamp(value: number, lo: number, hi: number) {
    if (!Number.isFinite(value)) return lo;
    return Math.min(Math.max(value, lo), Math.max(lo, hi));
  }

  function xAt(time: number, lo: number, hi: number) {
    if (!Number.isFinite(lo) || !Number.isFinite(hi) || hi <= lo) return plotLeft;
    return plotLeft + Math.max(0, Math.min(1, (time - lo) / (hi - lo))) * plotWidth;
  }

  function segmentVisible(segment: DaySegment, lo: number, hi: number) {
    return segment.end >= lo && segment.start < hi;
  }

  function segmentX(segment: DaySegment, lo: number, hi: number) {
    return Math.max(plotLeft, xAt(segment.start, lo, hi));
  }

  function segmentWidth(segment: DaySegment, lo: number, hi: number) {
    const left = Math.max(plotLeft, xAt(segment.start, lo, hi));
    const right = Math.min(plotLeft + plotWidth, xAt(segment.end, lo, hi));
    return Math.max(2, right - left);
  }

  function hourTicks(dayLo: number, lo: number, hi: number, zoom: number) {
    if (!Number.isFinite(dayLo)) return [] as number[];
    const step = zoom <= 1/60 ? 15_000 : zoom <= 5/60 ? 60_000 : zoom <= 0.25 ? 300_000 : zoom <= 1 ? 15 * 60_000 : zoom <= 3 ? 30 * 60_000 : zoom <= 6 ? hour : zoom <= 12 ? 2 * hour : 4 * hour;
    const first = dayLo + Math.ceil((lo - dayLo) / step) * step;
    const ticks: number[] = [];
    for (let value = first; value <= hi + 1; value += step) ticks.push(value);
    return ticks;
  }

  function tickLabel(time: number) {
    if(time === dayEnd) return '24:00';
    return new Date(time).toLocaleTimeString('en-GB', { timeZone: 'Europe/Istanbul', hour: '2-digit', minute: '2-digit', ...(zoomHours <= 5/60 ? {second:'2-digit' as const} : {}) });
  }

  function duration(value: number) { return value === 0 ? '—' : formatDuration(value); }

  function laneDuration(lanes: DayLane[], prefix: string): number {
    const intervals=lanes
      .filter(lane=>lane.name.startsWith(prefix))
      .flatMap(lane=>lane.segments)
      .filter(segment=>segment.kind!=='conflict' && segment.end>segment.start)
      .sort((a,b)=>a.start-b.start || a.end-b.end);
    let total=0;
    let end=-Infinity;
    for(const segment of intervals){
      total+=Math.max(0,segment.end-Math.max(end,segment.start));
      end=Math.max(end,segment.end);
    }
    return total;
  }

  function laneCount(lanes: DayLane[], prefix: string): number {
    return lanes.filter(lane=>lane.name.startsWith(prefix)).reduce((count,lane)=>count+lane.segments.length,0);
  }

  function compactSegments(segments: DaySegment[]): DaySegment[] {
    const compacted: DaySegment[]=[];
    for (const segment of [...segments].sort((a,b)=>a.start-b.start || a.end-b.end)) {
      const last=compacted.at(-1);
      if (last && last.label===segment.label && last.kind===segment.kind && segment.start-last.end<=300_000) {
        last.end=Math.max(last.end,segment.end);
        last.evidence.push(...segment.evidence);
        last.detail=`${last.evidence.length} records`;
      } else {
        compacted.push({...segment,evidence:[...segment.evidence]});
      }
    }
    return compacted;
  }

  function segmentTitle(segment: DaySegment) {
    return `${clock(segment.start)}–${clock(segment.end)} · ${segment.label}`;
  }

  function inspect(segment: DaySegment) {
    hidePopover();
    const evidence = segment.evidence?.[0];
    if (evidence) oninspect(evidence);
  }

  function keydown(event: KeyboardEvent, segment: DaySegment) {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      inspect(segment);
    }
    if (event.key === 'Escape') hidePopover();
  }

  async function showPopover(segment: DaySegment, event: MouseEvent | FocusEvent) {
    if(dragging || !segment.evidence[0])return;
    activeSegment=segment;
    const bounds=(event.currentTarget as Element | null)?.getBoundingClientRect();
    await tick();
    const x=event instanceof MouseEvent ? event.clientX : (bounds?.right ?? 0);
    const y=event instanceof MouseEvent ? event.clientY : (bounds?.bottom ?? 0);
    placeTooltip(x,y);
  }
  function placeTooltip(x:number,y:number) {
    const box=tooltip?.getBoundingClientRect();
    const position=tooltipPosition(x,y,box?.width ?? 260,box?.height ?? 110,window.innerWidth,window.innerHeight);
    tooltipX=position.left;tooltipY=position.top;
  }
  function hidePopover() { activeSegment=null; }
  function applyWindow(next:{start:number;end:number}) {
    zoomHours=(next.end-next.start)/hour;
    centerHour=(next.start+next.end)/2/hour;
    hidePopover();
  }
  function brushTime(event:PointerEvent) {
    const box=(event.currentTarget as SVGSVGElement).getBoundingClientRect();
    return clamp((event.clientX-box.left)/box.width,0,1)*dayLength;
  }
  function startBrush(event:PointerEvent) {
    const time=brushTime(event);
    const edge=(event.target as Element).getAttribute('data-edge');
    brushMode=edge==='start'||edge==='end'?edge:'move';
    brushWindow={start:visibleStart-dayStart,end:visibleEnd-dayStart};
    if(brushMode==='move'&&(visibleSpan===dayLength||time<brushWindow.start||time>brushWindow.end)) {
      const span=Math.min(visibleSpan,6*hour);
      brushWindow=moveWindow({start:0,end:span},time-span/2);
      applyWindow(brushWindow);
    }
    brushOrigin=time;
    (event.currentTarget as SVGSVGElement).setPointerCapture(event.pointerId);
    hidePopover();
  }
  function moveBrush(event:PointerEvent) {
    if(!brushMode)return;
    const time=brushTime(event);
    applyWindow(brushMode==='move'?moveWindow(brushWindow,time-brushOrigin):resizeWindow(brushWindow,brushMode,time));
  }
  function endBrush(event:PointerEvent) {
    brushMode=null;
    const svg=event.currentTarget as SVGSVGElement;
    if(svg.hasPointerCapture(event.pointerId))svg.releasePointerCapture(event.pointerId);
  }
  function brushKey(event:KeyboardEvent,edge:'start'|'end') {
    const current={start:visibleStart-dayStart,end:visibleEnd-dayStart};
    const delta=event.key==='ArrowLeft'?-visibleSpan/10:event.key==='ArrowRight'?visibleSpan/10:0;
    if(delta){event.preventDefault();applyWindow(resizeWindow(current,edge,current[edge]+delta));}
  }
  async function changeDate(next:string) {date=next;hidePopover();await tick();ondatechange();}
  function toggleLane(name:string) {hiddenLanes=hiddenLanes.includes(name)?hiddenLanes.filter(n=>n!==name):[...hiddenLanes,name];hidePopover();}

  function handleWindowKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') hidePopover();
  }

  function setZoomWindow(nextValue: number | string, anchorTime?: number) {
    hidePopover();
    const next = clamp(Number(nextValue), 1/60, 24);
    if (!Number.isFinite(next) || !Number.isFinite(dayStart)) return;
    const oldSpan = visibleSpan;
    const anchor = anchorTime ?? visibleStart + oldSpan / 2;
    const ratio = oldSpan > 0 ? (anchor - visibleStart) / oldSpan : 0.5;
    zoomHours = next;
    const nextSpan = next * hour;
    const nextStart = clamp(anchor - ratio * nextSpan, dayStart, dayEnd - nextSpan);
    centerHour = (nextStart + nextSpan / 2 - dayStart) / hour;
  }

  function pan(direction: number) {
    if (!Number.isFinite(dayStart)) return;
    const nextStart = clamp(visibleStart + direction * visibleSpan * 0.3, dayStart, dayEnd - visibleSpan);
    centerHour = (nextStart + visibleSpan / 2 - dayStart) / hour;
  }

  function handleWheel(event: WheelEvent) {
    if (!Number.isFinite(dayStart) || event.deltaY === 0) return;
    const svg = event.currentTarget as SVGSVGElement;
    const bounds = svg.getBoundingClientRect();
    const viewWidth = plotLeft + plotWidth + plotRight;
    const viewX = ((event.clientX - bounds.left) / Math.max(1, bounds.width)) * viewWidth;
    const ratio = clamp((viewX - plotLeft) / plotWidth, 0, 1);
    const anchor = visibleStart + ratio * visibleSpan;
    setZoomWindow(zoomHours * (event.deltaY < 0 ? 0.75 : 1.25), anchor);
  }

  function handlePointerDown(event: PointerEvent) {
    const target = event.target as Element | null;
    if (target?.closest('.day-segment')) return;
    hidePopover();
    dragging = true;
    dragX = event.clientX;
    (event.currentTarget as SVGSVGElement).setPointerCapture(event.pointerId);
  }

  function handlePointerMove(event: PointerEvent) {
    if (!dragging) return;
    const svg = event.currentTarget as SVGSVGElement;
    const bounds = svg.getBoundingClientRect();
    const delta = (event.clientX - dragX) / Math.max(1, bounds.width) * visibleSpan;
    dragX = event.clientX;
    const nextStart = clamp(visibleStart - delta, dayStart, dayEnd - visibleSpan);
    centerHour = (nextStart + visibleSpan / 2 - dayStart) / hour;
  }

  function handlePointerUp(event: PointerEvent) {
    dragging = false;
    const svg = event.currentTarget as SVGSVGElement;
    if (svg.hasPointerCapture(event.pointerId)) svg.releasePointerCapture(event.pointerId);
  }

  function dateTitle(value: string) {
    if (!value) return '';
    const parsed = new Date(`${value}T12:00:00+03:00`);
    return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleDateString('en-GB', {
      weekday: 'short',
      day: 'numeric',
      month: 'short',
      year: 'numeric',
      timeZone: 'Europe/Istanbul'
    });
  }
</script>

<svelte:window on:keydown={handleWindowKeydown} />

<section class="day-view" aria-label="Day" data-status={status ? 'ready' : undefined}>
  <div class="day-header">
    <DayPicker value={date} onchange={changeDate} disabled={loading} />
    <div class="day-controls" aria-label="Timeline controls">
      <button class:chosen={density==='compact'} aria-pressed={density==='compact'} on:click={()=>density='compact'}>Compact</button>
      <button class:chosen={density==='records'} aria-pressed={density==='records'} on:click={()=>density='records'}>Records</button>
      {#each [24,6,1,0.25] as preset}<button class:chosen={Math.abs(zoomHours-preset)<0.00001} aria-pressed={Math.abs(zoomHours-preset)<0.00001} on:click={()=>setZoomWindow(preset)}>{preset===24?'Full day':preset===0.25?'15m':`${preset}h`}</button>{/each}
      <button type="button" class="icon-button" aria-label="Zoom out" on:click={() => setZoomWindow(zoomHours * 1.5)}>−</button>
      <button type="button" class="icon-button" aria-label="Zoom in" on:click={() => setZoomWindow(zoomHours / 1.5)}>+</button>
      <button type="button" class="icon-button" aria-label="Pan earlier" on:click={() => pan(-1)}>←</button>
      <button type="button" class="icon-button" aria-label="Pan later" on:click={() => pan(1)}>→</button>
      <button type="button" class="refresh-day" disabled={loading} on:click={ondatechange}>↻</button>
    </div>
  </div>

  <div class="lane-controls">
    <input class="search" type="search" aria-label="Filter apps and titles" placeholder="App or title" bind:value={search} on:input={hidePopover} />
    {#each model.lanes as lane}<button aria-pressed={!hiddenLanes.includes(lane.name)} class:muted={hiddenLanes.includes(lane.name)} on:click={()=>toggleLane(lane.name)}>{lane.name.split(' · ')[0]}</button>{/each}
  </div>
  <div class="day-stats" aria-label="Recorded day totals">
    <div><span>Sleep</span><strong>{duration(sleepTotal)}</strong></div>
    <div><span>Exercise</span><strong>{duration(exerciseTotal)}</strong></div>
    <div><span>Windows</span><strong>{duration(computerTotal)}</strong></div>
    <div><span>Phone</span><strong>{phoneCount.toLocaleString()} events</strong></div>
    <div><span>Browser</span><strong>{duration(browserTotal)}</strong></div>
  </div>
  {#if loading}<div class="inline-state" role="status">Loading</div>{/if}
  {#if error}<div class="inline-state error" role="alert">{error}</div>{/if}

  {#if !loading && !error}
    <section class="timeline-card" aria-labelledby="timeline-title">
      <div class="card-head">
        <h3 id="timeline-title">Timeline</h3>
        <span class="range-label">{tickLabel(visibleStart)}–{tickLabel(visibleEnd)}</span>
      </div>

      {#if filteredLanes.length}
        <div class="chart-wrap">
          <div class="timeline-scroll">
            <svg
              class="day-svg"
              viewBox={`0 0 ${plotLeft + plotWidth + plotRight} ${svgHeight}`}
              role="group"
              aria-label={`${displayedDateTitle} timeline`}
              on:wheel|preventDefault={handleWheel}
              on:pointerdown={handlePointerDown}
              on:pointermove={handlePointerMove}
              on:pointerup={handlePointerUp}
              on:pointercancel={handlePointerUp}
            >
              <defs>
                <pattern id="day-unknown-pattern" width="7" height="7" patternUnits="userSpaceOnUse">
                  <path class="pattern-line" d="M-1 1L1 -1M0 7L7 0M6 8L8 6" />
                </pattern>
                <pattern id="day-agent-pattern" width="8" height="8" patternUnits="userSpaceOnUse">
                  <path class="pattern-line" d="M0 8L8 0" />
                </pattern>
              </defs>

              <rect class="plot-background" x={plotLeft} y={timelineTop - 8} width={plotWidth} height={laneHeight} />
              {#each visibleHours as tick}
                <line class="hour-line" x1={xAt(tick, visibleStart, visibleEnd)} x2={xAt(tick, visibleStart, visibleEnd)} y1="24" y2={timelineTop + laneHeight - 3} />
                <text class="hour-label" x={xAt(tick, visibleStart, visibleEnd) + (tick === visibleEnd ? -2 : 2)} text-anchor={tick === visibleEnd ? 'end' : 'start'} y="15">{tickLabel(tick)}</text>
              {/each}

              {#each filteredLanes as lane, laneIndex}
                {@const rowY = timelineTop + laneIndex * (rowHeight + rowGap)}
                <text class="lane-name" x="0" y={rowY + 20}>{lane.name.split(' · ')[0]}</text>
                <rect class="lane-row" x={plotLeft} y={rowY} width={plotWidth} height={rowHeight} />
                {#each lane.segments.filter((segment) => segmentVisible(segment, visibleStart, visibleEnd)) as segment}
                  {@const left = segmentX(segment, visibleStart, visibleEnd)}
                  {@const width = segmentWidth(segment, visibleStart, visibleEnd)}
                  <g
                    class={`day-segment ${segment.kind}`}
                    tabindex="0"
                    role="button"
                    aria-label={segmentTitle(segment)}
                    on:click={() => inspect(segment)}
                    on:keydown={(event) => keydown(event, segment)}
                    on:mouseenter={(event) => showPopover(segment,event)}
                    on:mousemove={(event)=>{if(activeSegment)placeTooltip(event.clientX,event.clientY);}}
                    on:mouseleave={hidePopover}
                    on:focus={(event) => showPopover(segment,event)}
                    on:blur={hidePopover}
                  >

                    <rect x={left} y={rowY + 5} width={width} height={rowHeight - 10} rx="1" />
                    {#if width > Math.max(34, segment.label.length * 6 + 12)}
                      <text class="segment-label" x={left + 5} y={rowY + 21}>{segment.label}</text>
                    {/if}
                  </g>
                {/each}
              {/each}
            </svg>
          </div>

          <div class="overview">
            <svg class="day-overview" viewBox="0 0 1000 42" preserveAspectRatio="none" role="group" aria-label="Day overview" on:pointerdown={startBrush} on:pointermove={moveBrush} on:pointerup={endBrush} on:pointercancel={endBrush}>
              <rect class="overview-bg" x="0" y="0" width="1000" height="42" />
              {#each filteredLanes as lane, index}{#each lane.segments as segment}
                <rect class="overview-event" x={(segment.start-dayStart)/dayLength*1000} y={4+index*4} width={Math.max(1,(segment.end-segment.start)/dayLength*1000)} height="3" />
              {/each}{/each}
              <rect class="brush-selection" x={(visibleStart-dayStart)/dayLength*1000} y="1" width={visibleSpan/dayLength*1000} height="40" />
              {#each ['start','end'] as edge}
                {@const value=edge==='start'?visibleStart:visibleEnd}
                <rect class="brush-handle" data-edge={edge} x={clamp((value-dayStart)/dayLength*1000-(edge==='start'?10:0),0,990)} y="0" width="10" height="42" tabindex="0" role="slider" aria-label={edge==='start'?'Range start':'Range end'} aria-valuemin="0" aria-valuemax="24" aria-valuenow={(value-dayStart)/hour} aria-valuetext={tickLabel(value)} on:keydown={(event)=>brushKey(event,edge as 'start'|'end')} />
              {/each}
            </svg>
            <div class="overview-times"><span>00:00</span><span>06:00</span><span>12:00</span><span>18:00</span><span>24:00</span></div>
          </div>

        </div>
      {:else}
        <div class="empty">No events</div>
      {/if}
    </section>


  {/if}
</section>
{#if activeSegment && summary}
  <div class="cursor-tooltip" bind:this={tooltip} role="tooltip" style:left={`${tooltipX}px`} style:top={`${tooltipY}px`}>
    <strong>{activeSegment.label}</strong>
    <span>{clock(activeSegment.start)}–{clock(activeSegment.end)} · {duration(activeSegment.end-activeSegment.start)}{activeSegment.evidence.length > 1 ? ` · ${activeSegment.evidence.length} records` : ''}</span>
    {#if activeSegment.detail && !['phone','conflict'].includes(activeSegment.kind)}<p>{activeSegment.detail}</p>{/if}
    {#if tooltipFields.length}<span>{tooltipFields.map(f=>`${f.label} ${f.value}`).join(' · ')}</span>{/if}
  </div>
{/if}

<style>
 .day-view {display:grid;gap:10px;min-width:0;}
 .day-header,.day-controls,.lane-controls {display:flex;align-items:center;gap:6px;flex-wrap:wrap;}
 .day-header {justify-content:space-between;}
 button,input {font:inherit;font-size:12px;border:1px solid var(--line);color:var(--fg);background:var(--panel);padding:6px 9px;}
 button {cursor:pointer;} button:hover,button:focus-visible {outline:1px solid var(--fg);}
 button.chosen {background:var(--fg);color:var(--bg);} button:disabled {opacity:.4;cursor:default;}
 .lane-controls {padding-bottom:4px;} .lane-controls .muted {opacity:.4;}
 .search {width:170px;} .inline-state {font-size:12px;color:var(--muted);}
 .day-stats {display:grid;grid-template-columns:repeat(5,minmax(0,1fr));gap:1px;border:1px solid var(--line);background:var(--line);}
 .day-stats div {display:grid;gap:3px;padding:7px 9px;background:var(--panel);min-width:0;}
 .day-stats span {font-size:10px;color:var(--muted);text-transform:uppercase;letter-spacing:.04em;}
 .day-stats strong {font-size:13px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;}
 .timeline-card {border:1px solid var(--line);background:var(--panel);min-width:0;}
 .card-head {display:flex;justify-content:space-between;padding:8px 12px;font-size:12px;}
 h3 {font-size:13px;margin:0;} .range-label {color:var(--muted);}
 .timeline-scroll {overflow-x:auto;border-top:1px solid var(--line);padding:8px;}
 .day-svg {display:block;width:100%;min-width:680px;height:auto;touch-action:none;cursor:grab;}
 .day-svg:active {cursor:grabbing;} .plot-background,.lane-row {fill:var(--soft);opacity:.35;}
 .lane-row {stroke:var(--line);} .hour-line {stroke:var(--line);stroke-dasharray:2 3;}
 .hour-label,.lane-name {fill:var(--muted);font-size:12px;} .lane-name {fill:var(--fg);font-weight:600;}
 .pattern-line {stroke:var(--muted);}
 .day-segment {cursor:pointer;outline:none;}
 .day-segment rect {fill:var(--muted);stroke:var(--line);stroke-width:.5;}
 .day-segment.sleep rect {fill:var(--fg);}
 .day-segment.idle rect,.day-segment.agent rect {fill:var(--panel);stroke:var(--muted);stroke-dasharray:3 2;}
 .day-segment.conflict rect {fill:url(#day-agent-pattern);}
 .day-segment:hover rect,.day-segment:focus rect {stroke:var(--fg);stroke-width:2;}
 .segment-label {fill:var(--bg);font-size:10px;pointer-events:none;}
 .day-segment.idle .segment-label,.day-segment.agent .segment-label {fill:var(--fg);}
 .overview {border-top:1px solid var(--line);padding:8px 12px 6px;}
 .day-overview {display:block;width:100%;height:42px;touch-action:none;cursor:ew-resize;}
 .overview-bg {fill:var(--bg);} .overview-event {fill:var(--muted);opacity:.45;pointer-events:none;}
 .brush-selection {fill:var(--fg);fill-opacity:.09;stroke:var(--fg);stroke-width:1;cursor:grab;}
 .brush-handle {fill:var(--fg);cursor:ew-resize;} .brush-handle:focus {stroke:var(--muted);stroke-width:3;}
 .overview-times {display:flex;justify-content:space-between;color:var(--muted);font-size:10px;margin-top:4px;}
 .cursor-tooltip {position:fixed;z-index:50;pointer-events:none;width:min(260px,calc(100vw - 16px));max-height:130px;overflow:hidden;padding:9px 11px;border:1px solid var(--muted);background:var(--panel);color:var(--fg);box-shadow:0 3px 12px #0005;font-size:12px;}
 .cursor-tooltip strong,.cursor-tooltip span {display:block;} .cursor-tooltip span {font-size:11px;color:var(--muted);margin-top:4px;}
 .cursor-tooltip p {margin:5px 0 0;display:-webkit-box;-webkit-line-clamp:2;line-clamp:2;-webkit-box-orient:vertical;overflow:hidden;overflow-wrap:anywhere;}
 .empty {padding:12px;color:var(--muted);font-size:12px;}
 @media(max-width:720px) { .hour-label,.lane-name {font-size:20px;} .segment-label {font-size:17px;} }
 @media(max-width:720px) { .day-stats {grid-template-columns:repeat(3,minmax(0,1fr));} }
</style>
