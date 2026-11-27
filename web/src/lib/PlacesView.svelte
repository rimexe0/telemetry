<script lang="ts">
  import 'leaflet/dist/leaflet.css';
  import { onDestroy, onMount, tick } from 'svelte';
  import type * as Leaflet from 'leaflet';
  import { whenFresh } from './fresh';
  import { getPlacesDay } from './api';
  import { clock, dayList, habitVisits, pathLines, stayLabel, usablePoints, type ListItem, type PlacesDay } from './places';

  export let date = '';

  // OpenStreetMap's own tiles, with no key. The page's address is the only
  // thing they are told (the origin, never the path), as their usage policy
  // asks; nothing about the owner goes with them.
  const tiles = 'https://tile.openstreetmap.org/{z}/{x}/{y}.png';
  const attribution = '© <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a>';

  let day: PlacesDay | undefined;
  let items: ListItem[] = [];
  let loading = false;
  let error = '';
  let loadedFor: string | undefined;
  let element: HTMLDivElement | undefined;
  let L: typeof Leaflet | undefined;
  let map: Leaflet.Map | undefined;
  let drawn: Leaflet.LayerGroup | undefined;
  let mounted = false;

  $: empty = !!day && !(day.positions ?? []).length && !(day.stays ?? []).length;
  $: if (mounted && loadedFor !== date) void load();
  whenFresh(load);

  onMount(async () => {
    mounted = true;
    L = await import('leaflet');
    await draw();
  });
  onDestroy(() => {
    map?.remove();
    map = undefined;
  });

  async function load() {
    loadedFor = date;
    loading = true;
    error = '';
    try {
      const loaded = await getPlacesDay(date || undefined);
      if (loadedFor !== date) return;
      day = loaded;
      items = dayList(loaded);
      await draw();
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Unable to load places.';
    } finally {
      loading = false;
    }
  }

  function color(name: string, fallback: string): string {
    return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || fallback;
  }

  /** Text for a label: names come from the map and the owner, never markup. */
  function text(value: string): HTMLElement {
    const span = document.createElement('span');
    span.textContent = value;
    return span;
  }

  async function draw() {
    await tick();
    if (!L || !day || !element || empty) {
      if (empty && map) { map.remove(); map = undefined; }
      return;
    }
    if (!map) {
      map = L.map(element, { zoomSnap: 0.5 });
      map.attributionControl.setPrefix(false);
      L.tileLayer(tiles, { maxZoom: 19, attribution, referrerPolicy: 'strict-origin' }).addTo(map);
      drawn = L.layerGroup().addTo(map);
    }
    drawn!.clearLayers();
    const fg = color('--fg', '#181918'), bg = color('--bg', '#f5f5f2'), muted = color('--muted', '#63645f'), done = color('--done', '#2f8f5b');
    const timezone = day.timezone;
    const points = usablePoints(day.positions);
    const visits = habitVisits(points, day.rules);
    const bounds = L.latLngBounds([]);

    for (const place of day.rules ?? []) {
      const reached = visits.some(visit => visit.place.habit_id === place.habit_id);
      L.circle([place.lat, place.lon], { radius: place.radius_m, color: reached ? done : muted, weight: 2, fillOpacity: 0.12, interactive: false })
        .bindTooltip(text(place.name), { permanent: true, direction: 'top', className: 'places-label', offset: [0, -4] })
        .addTo(drawn!);
    }
    const lines = pathLines(points).filter(line => line.length > 1).map(line => line.map(point => [point.lat, point.lon] as [number, number]));
    if (lines.length) L.polyline(lines, { color: fg, weight: 3, opacity: 0.75, interactive: false }).addTo(drawn!);
    const dots = L.canvas({ padding: 0.5 });
    for (const point of points) {
      bounds.extend([point.lat, point.lon]);
      L.circleMarker([point.lat, point.lon], { renderer: dots, radius: 3, stroke: false, fillColor: fg, fillOpacity: 0.55 })
        .bindTooltip(clock(point.at, timezone), { direction: 'top', className: 'places-label', offset: [0, -2] })
        .addTo(drawn!);
    }
    const homes = (day.stays ?? []).filter(stay => stay.home);
    if (homes.length) {
      L.circleMarker([homes[0].lat, homes[0].lon], { radius: 7, color: fg, weight: 2, fillColor: fg, fillOpacity: 1 })
        .bindTooltip(text('home'), { permanent: true, direction: 'right', className: 'places-label', offset: [6, 0] })
        .addTo(drawn!);
    }
    for (const stay of day.stays ?? []) {
      bounds.extend([stay.lat, stay.lon]);
      if (stay.home) continue;
      L.circleMarker([stay.lat, stay.lon], { radius: 6, color: fg, weight: 2, fillColor: bg, fillOpacity: 1 })
        .bindTooltip(text(stayLabel(stay, visits, timezone)), { permanent: true, direction: 'right', className: 'places-label', offset: [6, 0] })
        .addTo(drawn!);
    }
    map.invalidateSize();
    if (bounds.isValid()) map.fitBounds(bounds, { padding: [28, 28], maxZoom: 17 });
  }
</script>

<section class="places" aria-labelledby="places-heading">
  <h2 id="places-heading" class="sr-only">Places</h2>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  {#if loading && !day}<p class="muted">Loading…</p>{/if}
  {#if empty}<p class="muted">No positions this day.</p>{/if}
  <div class="map" class:hidden={!day || empty} bind:this={element}></div>
  {#if day && !empty}
    <ul class="stays">
      {#each items as item (`${item.kind}-${item.from}`)}
        <li class:gap={item.kind === 'gap'}>{item.text}</li>
      {/each}
    </ul>
    <p class="muted small">From the phone's GPS. Positions worse than 100 m are left out. Inside a building GPS is weak, so short steps may not show.</p>
  {/if}
</section>

<style>
  .map { border: 1px solid var(--fg); height: min(62vh, 520px); min-height: 260px; width: 100%; }
  .map.hidden { display: none; }
  ul { list-style: none; margin: 10px 0 4px; padding: 0; }
  li { border-bottom: 1px solid var(--line); font-size: .85rem; overflow-wrap: anywhere; padding: 4px 0; }
  li.gap { color: var(--muted); }
  .muted { color: var(--muted); }
  .small { font-size: .78rem; }
  .sr-only { height: 1px; margin: -1px; overflow: hidden; position: absolute; width: 1px; }
  /* Leaflet's own look, squared and in the panel's type. */
  .map :global(.leaflet-container), .map:global(.leaflet-container) { background: var(--soft); font: inherit; }
  .map :global(.leaflet-bar) { border: 1px solid var(--fg); border-radius: 0; box-shadow: none; }
  .map :global(.leaflet-bar a) { background: var(--bg); border-bottom-color: var(--fg); border-radius: 0 !important; color: var(--fg); }
  .map :global(.leaflet-control-attribution) { background: var(--bg); color: var(--muted); font-size: .68rem; }
  .map :global(.leaflet-control-attribution a) { color: var(--fg); }
  .map :global(.places-label) { background: var(--bg); border: 1px solid var(--fg); border-radius: 0; box-shadow: none; color: var(--fg); font: inherit; font-size: .72rem; padding: 1px 4px; }
  .map :global(.places-label::before) { display: none; }
</style>
