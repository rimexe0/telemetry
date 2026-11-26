<script lang="ts">
  import { places, type Place } from './nav';

  // The four places: a bottom bar on a phone, a sidebar at desktop width.
  export let current: Place = 'today';
  /** Where each place opens, so Activity comes back to the last section. */
  export let hrefs: Record<Place, string> = { today: '#today', habits: '#habits', activity: '#activity/timeline', settings: '#settings' };
</script>

<nav class="place-nav" aria-label="Places">
  <span class="brand" aria-hidden="true">Telemetry</span>
  <ul>
    {#each places as item (item.place)}
      <li><a href={hrefs[item.place]} class:active={current === item.place} aria-current={current === item.place ? 'page' : undefined}>{item.label}</a></li>
    {/each}
  </ul>
</nav>

<style>
  .place-nav { background: var(--bg); z-index: 15; }
  ul { list-style: none; margin: 0; padding: 0; }
  a { color: var(--fg); display: flex; font-size: .85rem; text-decoration: none; }

  /* Phone: a bar along the bottom, the place in use filled. */
  @media (max-width: 699px) {
    .place-nav { border-top: 2px solid var(--fg); bottom: 0; left: 0; padding: 0 var(--safe-right, 0px) var(--safe-bottom, 0px) var(--safe-left, 0px); position: fixed; right: 0; }
    .brand { display: none; }
    ul { display: grid; grid-template-columns: repeat(4, 1fr); }
    a { align-items: center; justify-content: center; min-height: 52px; padding: 0 4px; }
    a.active { background: var(--fg); color: var(--bg); font-weight: 700; }
    a:focus-visible { outline: 2px solid var(--fg); outline-offset: -4px; }
    a.active:focus-visible { outline-color: var(--bg); }
  }

  /* Desktop: a sidebar, the place in use outlined. */
  @media (min-width: 700px) {
    .place-nav { align-self: start; padding-top: var(--safe-top, 0px); position: sticky; top: 0; }
    .brand { border-bottom: 2px solid var(--fg); display: block; font-size: 1.15rem; font-weight: 700; letter-spacing: -.04em; margin-bottom: 14px; padding: 18px 0 12px; }
    ul { display: grid; gap: 6px; }
    a { align-items: center; border: 1px solid transparent; min-height: 40px; padding: 0 12px; }
    a:hover { border-color: var(--line); }
    a.active { border-color: var(--fg); font-weight: 700; }
    a:focus-visible { outline: 2px solid var(--fg); outline-offset: 2px; }
  }
</style>
