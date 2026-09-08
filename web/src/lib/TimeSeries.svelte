<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import type UPlot from 'uplot';
  import 'uplot/dist/uPlot.min.css';
  import type { Observation } from './api';

  export let observations: Observation[] = [];

  let host: HTMLDivElement;
  let chart: UPlot | undefined;
  let mounted = false;
  let resizeObserver: ResizeObserver | undefined;
  let renderRevision = 0;
  let themeObserver: MutationObserver | undefined;

  function series(events: Observation[]): [number[], number[]] {
    const counts = new Map<number, number>();
    for (const event of events) {
      const time = Date.parse(event.occurred_at);
      if (Number.isNaN(time)) continue;
      const bucket = Math.floor(time / 3_600_000) * 3_600_000;
      counts.set(bucket, (counts.get(bucket) ?? 0) + 1);
    }
    const buckets = [...counts.keys()].sort((left, right) => left - right);
    return [buckets.map((bucket) => bucket / 1000), buckets.map((bucket) => counts.get(bucket) ?? 0)];
  }

  async function render() {
    const revision = ++renderRevision;
    if (!mounted || !host) return;
    const data = series(observations);
    chart?.destroy();
    chart = undefined;
    if (data[0].length === 0) return;

    const { default: uPlot } = await import('uplot');
    if (!mounted || !host || revision !== renderRevision) return;
    const width = Math.max(280, Math.floor(host.getBoundingClientRect().width));
    const style = getComputedStyle(document.documentElement);
    const fg = style.getPropertyValue('--fg').trim(), line = style.getPropertyValue('--line').trim(), bg = style.getPropertyValue('--panel').trim();
    chart = new uPlot(
      {
        width,
        height: 230,
        cursor: { drag: { setScale: false } },
        legend: { show: false },
        scales: { x: { time: true } },
        axes: [
          { stroke: fg, grid: { stroke: line, width: 1 } },
          { stroke: fg, grid: { stroke: line, width: 1 } }
        ],
        series: [{}, { label: 'Observations', stroke: fg, width: 2, points: { show: true, size: 5, fill: bg, stroke: fg } }]
      },
      data,
      host
    );
  }

  $: if (mounted && observations) void render();

  onMount(() => {
    mounted = true;
    resizeObserver = new ResizeObserver(() => void render());
    resizeObserver.observe(host);
    themeObserver = new MutationObserver(() => void render());
    themeObserver.observe(document.documentElement, {attributes:true,attributeFilter:['data-theme']});
    void render();
  });

  onDestroy(() => {
    mounted = false;
    renderRevision += 1;
    resizeObserver?.disconnect();
    themeObserver?.disconnect();
    chart?.destroy();
  });
</script>

<div class="chart" bind:this={host} aria-label="Observation count by hour"></div>

<style>
  .chart {
    min-height: 230px;
    min-width: 0;
    overflow: hidden;
    width: 100%;
  }

  :global(.uplot) {
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
    max-width: 100%;
  }
</style>
