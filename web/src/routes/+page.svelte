<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import TimeSeries from '$lib/TimeSeries.svelte';
  import HealthHistory from '$lib/HealthHistory.svelte';
  import DayTimeline from '$lib/DayTimeline.svelte';
  import EventDetail from '$lib/EventDetail.svelte';
  import { dayKey, dayStart } from '$lib/health';
  import {
    APIError,
    endPanelSession,
    listObservations,
    listSources,
    loadHealthHistory,
    loadObservationHistory,
    panelSession,
    startPanelSession,
    type Observation,
    type SourceSummary
  } from '$lib/api';

  type Range = '24h' | '7d';

  const fixtureMode = import.meta.env.VITE_TELEMETRY_FIXTURE === '1';
  const snapshotMode = import.meta.env.VITE_TELEMETRY_SNAPSHOT === '1';
  let view: 'day' | 'health' | 'collection' = 'day';
  let selectedDay = dayKey(Date.now());
  let dayObservations: Observation[] = [];
  let dayLoading = false;
  let dayError = '';
  let dayStatus = '';
  let dayRequest: AbortController | undefined;
  let healthObservations: Observation[] = [];
  let healthError = '';
  let healthStatus = '';
  let healthSource = '';
  let healthLoading = false;
  let healthRequest: AbortController | undefined;
  $: healthSources = sources.filter(source => source.source_kind === 'health_connect');
  let authenticated = false;
  let checkingSession = true;
  let loading = false;
  let error = '';
  let token = '';
  let sources: SourceSummary[] = [];
  let observations: Observation[] = [];
  let selectedSource = '';
  let range: Range = '24h';
  let selectedObservation: Observation | undefined;
  let updatedAt: Date | undefined;
  let timer: ReturnType<typeof setInterval> | undefined;

  function rangeStart(value: Range) {
    const hours = value === '24h' ? 24 : 24 * 7;
    return new Date(Date.now() - hours * 3_600_000);
  }

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

  function sourceLabel(source:SourceSummary) {
    const key=source.source_instance_key;
    if(key.includes('aw-watcher-window'))return 'Computer windows';
    if(key.includes('aw-watcher-afk'))return 'Keyboard / mouse';
    if(key.includes('aw-watcher-web'))return 'Browser tabs';
    if(key.includes('aw-watcher-codex'))return 'Codex';
    return ({health_connect:'Health Connect',android_usage:'Phone apps'} as Record<string,string>)[source.source_kind] ?? source.source_kind;
  }
  function fingerprint(value: string) {
    return value.length > 18 ? `${value.slice(0, 10)}…${value.slice(-6)}` : value;
  }

  type ThemeMode = 'system' | 'light' | 'dark';
  let themeMode: ThemeMode = 'system';
  let theme = 'dark';
  let themeQuery: MediaQueryList | undefined;

  function applyTheme() {
    const prefersDark = themeQuery?.matches ?? window.matchMedia('(prefers-color-scheme: dark)').matches;
    theme = themeMode === 'system' ? (prefersDark ? 'dark' : 'light') : themeMode;
    document.documentElement.dataset.theme = theme;
  }

  function cycleTheme() {
    themeMode = themeMode === 'system' ? 'light' : themeMode === 'light' ? 'dark' : 'system';
    applyTheme();
    try { localStorage.setItem('telemetry-theme-mode', themeMode); } catch {}
  }

  async function refresh() {
    if (!authenticated || loading) return;
    loading = true;
    error = '';
    try {
      const now = new Date();
      sources = await listSources();
      if (view === 'collection') {
        observations = await listObservations({
          sourceInstanceKey: selectedSource || undefined,
          from: rangeStart(range),
          to: now
        });
      } else if (view === 'health') {
        await refreshHealth();
      } else {
        await refreshDay();
      }
      if (selectedObservation) selectedObservation = (view === 'health' ? healthObservations : view === 'day' ? dayObservations : observations).find(event => event.source_record_key === selectedObservation?.source_record_key && event.source_instance_key === selectedObservation?.source_instance_key);
      updatedAt = now;
    } catch (cause) {
      error = cause instanceof APIError ? cause.message : 'Unable to load the current telemetry view.';
      if (cause instanceof APIError && cause.status === 401) authenticated = false;
    } finally {
      loading = false;
    }
  }

  async function refreshDay() {
    dayRequest?.abort();
    const request = new AbortController(); dayRequest = request;
    dayLoading = true; dayError = ''; dayStatus = '';
    try {
      const lo = dayStart(selectedDay);
      if (!Number.isFinite(lo)) throw new Error('Choose a valid date.');
      const from = new Date(lo - 86_400_000);
      const to = new Date(Math.min(lo + 86_400_000, Date.now()));
      if (to.getTime() <= lo) throw new Error('Choose today or an earlier day.');
      const rows: Observation[] = [];
      const available = sources.filter(s => ['activitywatch', 'health_connect', 'android_usage'].includes(s.source_kind));
      if (available.length > 16) throw new Error('This view supports up to 16 sources. Source selection is needed for larger installations.');
      for (const source of available) {
        rows.push(...await loadObservationHistory(source.source_instance_key, from, to, request.signal));
        if (rows.length > 25_000) throw new Error('Day view exceeds the browser record budget. No partial timeline is shown.');
      }
      if (request.signal.aborted || !authenticated) return;
      dayObservations = rows;
      dayStatus = `Day ${selectedDay}. Read at ${date(new Date().toISOString())}. UTC+03:00. Gaps mean unknown activity.`;
    } catch (cause) {
      if (request.signal.aborted) return;
      dayError = cause instanceof Error ? cause.message : 'Unable to load the day.';
      if (cause instanceof APIError && cause.status === 401) authenticated = false;
    } finally { if (dayRequest === request) dayLoading = false; }
  }

  async function refreshHealth() {
    healthRequest?.abort();
    const request = new AbortController();
    healthRequest = request;
    healthLoading = true;
    healthError = '';
    const available = sources.filter(s => s.source_kind === 'health_connect');
    if (!available.some(s => s.source_instance_key === healthSource)) healthSource = available[0]?.source_instance_key ?? '';
    healthStatus = '';
    try {
      if (!healthSource) { healthObservations = []; return; }
      const to = new Date();
      const from = new Date(to.getTime() - 35 * 86_400_000);
      const rows = await loadHealthHistory(healthSource, from, to, request.signal);
      if (request.signal.aborted || !authenticated) return;
      healthObservations = rows;
      healthStatus = `Loaded ${date(to.toISOString())}. Includes records starting since ${date(from.toISOString())}. Older intervals and uncollected data may be absent.`;
    } catch (cause) {
      if (request.signal.aborted) return;
      healthError = cause instanceof Error ? cause.message : 'Unable to load health history.';
      if (cause instanceof APIError && cause.status === 401) authenticated = false;
    } finally {
      if (healthRequest === request) healthLoading = false;
    }
  }

  async function signIn() {
    error = '';
    loading = true;
    try {
      await startPanelSession(token);
      token = '';
      authenticated = true;
      loading = false;
      await refresh();
    } catch (cause) {
      error = cause instanceof APIError ? cause.message : 'Unable to start a panel session.';
    } finally {
      loading = false;
    }
  }

  async function signOut() {
    await endPanelSession();
    healthRequest?.abort();
    authenticated = false;
    dayRequest?.abort();
    dayObservations = [];
    sources = [];
    observations = [];
    healthObservations = [];
    selectedObservation = undefined;
  }

  async function selectSource(sourceInstanceKey: string) {
    selectedSource = selectedSource === sourceInstanceKey ? '' : sourceInstanceKey;
    await refresh();
  }

  async function selectRange(nextRange: Range) {
    range = nextRange;
    await refresh();
  }

  onMount(async () => {
    try {
      const saved = localStorage.getItem('telemetry-theme-mode');
      themeMode = saved === 'light' || saved === 'dark' || saved === 'system' ? saved : 'system';
      localStorage.removeItem('telemetry-theme');
    } catch { themeMode = 'system'; }
    themeQuery = window.matchMedia('(prefers-color-scheme: dark)');
    themeQuery.addEventListener('change', applyTheme);
    applyTheme();
    try {
      authenticated = await panelSession();
      if (authenticated) await refresh();
    } catch (cause) {
      error = cause instanceof APIError && cause.status === 404 ? 'Panel sessions are not configured on this server.' : 'Unable to check the panel session.';
    } finally {
      checkingSession = false;
    }
    timer = setInterval(() => {
      if (document.visibilityState !== 'visible' || snapshotMode) return;
      if (view === 'collection' || (updatedAt && Date.now() - updatedAt.getTime() >= 300_000)) void refresh();
    }, 30_000);
  });

  onDestroy(() => {
    if (timer) clearInterval(timer);
    themeQuery?.removeEventListener('change', applyTheme);
    healthRequest?.abort();
    dayRequest?.abort();
  });
</script>

<svelte:head>
  <title>Telemetry panel</title>
  <meta name="description" content="Sleep, movement and heart-rate history with inspectable source records." />
</svelte:head>
<svelte:window on:keydown={(event) => { if (event.key === 'Escape') selectedObservation = undefined; }} />

<main>
  <header class="masthead">
    <div>
      <h1>Telemetry</h1>
    </div>
    <div class="header-actions"><button class="quiet-button" aria-label="Change theme" on:click={cycleTheme}>{themeMode === 'system' ? `System · ${theme}` : `Theme · ${themeMode}`}</button>
    {#if authenticated}<button class="quiet-button" on:click={signOut}>Sign out</button>{/if}</div>
  </header>

  {#if fixtureMode}
    <aside class="fixture" aria-label="Fixture preview notice">Fixture data</aside>
  {/if}
  {#if snapshotMode}<aside class="fixture">Local snapshot</aside>{/if}

  {#if checkingSession}
    <section class="state"><p>Loading…</p></section>
  {:else if !authenticated}
    <section class="login state" aria-labelledby="panel-login-heading">
      <h2 id="panel-login-heading">Sign in</h2>
      <form on:submit|preventDefault={signIn}>
        <label for="panel-token">Panel token</label>
        <input id="panel-token" bind:value={token} type="password" autocomplete="current-password" required />
        <button type="submit" disabled={loading}>{loading ? 'Opening…' : 'Open panel'}</button>
      </form>
      {#if error}<p class="error" role="alert">{error}</p>{/if}
    </section>
  {:else}
    <nav class="view-tabs" aria-label="Panel views">
      <button class:active={view === 'day'} aria-pressed={view === 'day'} disabled={loading} on:click={() => { view = 'day'; void refresh(); }}>Day</button>
      <button class:active={view === 'health'} aria-pressed={view === 'health'} disabled={loading} on:click={() => { view = 'health'; void refresh(); }}>Health</button>
      <button class:active={view === 'collection'} aria-pressed={view === 'collection'} disabled={loading} on:click={() => { view = 'collection'; void refresh(); }}>Sources</button>
    </nav>
    {#if view === 'day'}
      {#if error}<p class="error" role="alert">{error}</p>{/if}
      <DayTimeline observations={dayObservations} {sources} bind:date={selectedDay} loading={dayLoading || loading} error={dayError || error} status={dayStatus} ondatechange={refresh} oninspect={(o) => selectedObservation = o} />
    {:else if view === 'health'}
      <div class="health-toolbar">
        {#if healthSources.length > 1}
          <label>Device <select bind:value={healthSource} on:change={refreshHealth}>{#each healthSources as source}<option value={source.source_instance_key}>{source.device_id}</option>{/each}</select></label>
        {/if}
        <span>UTC+03:00</span>
        <button class="quiet-button" disabled={loading || healthLoading} on:click={refresh}>Refresh health</button>
      </div>
      {#if error}<p class="error" role="alert">{error}</p>{/if}
      <HealthHistory observations={healthObservations} loading={healthLoading} error={healthError} oninspect={(o) => selectedObservation = o} />
    {:else}
    <section class="controls" aria-label="History controls">
      <div class="control-group">
        <span>Window</span>
        <button class:active={range === '24h'} on:click={() => selectRange('24h')}>24 hours</button>
        <button class:active={range === '7d'} on:click={() => selectRange('7d')}>7 days</button>
      </div>
      <p class="updated">{loading ? 'Refreshing…' : updatedAt ? `Last refreshed ${date(updatedAt.toISOString())}` : 'No data loaded'}</p>
    </section>

    {#if error}<p class="error" role="alert">{error}</p>{/if}

    <section aria-labelledby="sources-heading">
      <div class="section-title">
        <div>
          <h2 id="sources-heading">Sources</h2>
        </div>
        <span class="count">{sources.length} connected</span>
      </div>
      <div class="source-grid">
        {#each sources as source (source.source_instance_key)}
          <button class:chosen={selectedSource === source.source_instance_key} class="source-card" on:click={() => selectSource(source.source_instance_key)}>
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
    </section>

    <section class="timeline-section" aria-labelledby="timeline-heading">
      <div class="section-title">
        <div>
          <h2 id="timeline-heading">Observation times</h2>
        </div>
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
        <div>
          <h2 id="records-heading">Recent records</h2>
        </div>
        {#if selectedSource}<button class="quiet-button" on:click={() => selectSource(selectedSource)}>Clear source filter</button>{/if}
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
                <td><button class="inspect" on:click={() => (selectedObservation = observation)}>Inspect</button></td>
              </tr>
            {:else}
              <tr><td class="empty" colspan="5">No records</td></tr>
            {/each}
          </tbody>
        </table>
      </div>
    </section>
    {/if}
  {/if}
</main>

{#if selectedObservation}
  <div class="drawer-backdrop" role="presentation" on:click={() => (selectedObservation = undefined)}></div>
  <aside class="drawer" aria-label="Observation detail">
    <button class="quiet-button drawer-close" on:click={() => (selectedObservation = undefined)}>Close</button>
    <EventDetail observation={selectedObservation} observations={view === 'health' ? healthObservations : view === 'day' ? dayObservations : observations} />
  </aside>
{/if}

<style>
  :global(:root) { color-scheme: dark; --bg:#111213; --panel:#191a1b; --fg:#eeeeeb; --muted:#aaa9a4; --line:#454647; --soft:#242628; }
  :global(:root[data-theme="light"]) { color-scheme:light; --bg:#f5f5f2; --panel:#fff; --fg:#181918; --muted:#63645f; --line:#c4c5bf; --soft:#e9eae5; }
  @media (prefers-color-scheme: light) {
    :global(:root:not([data-theme])) { color-scheme:light; --bg:#f5f5f2; --panel:#fff; --fg:#181918; --muted:#63645f; --line:#c4c5bf; --soft:#e9eae5; }
  }
  .header-actions { display:flex; gap:6px; }
  :global(*) { box-sizing: border-box; }
  :global(body) { margin: 0; background: var(--bg); color: var(--fg); font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; }
  :global(button), :global(input) { font: inherit; }
  main { width: min(1440px, calc(100% - 24px)); margin: 0 auto; padding: 14px 0 24px; }
  .view-tabs, .health-toolbar { display:flex; align-items:center; gap:8px; margin:12px 0; flex-wrap:wrap; font-size:12px; }
  .health-toolbar span { flex:1; color:var(--muted); }
  .view-tabs button { font-size:12px; padding:7px 10px; }
  .masthead { display: flex; align-items: flex-start; justify-content: space-between; gap: 20px; border-bottom: 2px solid var(--fg); padding-bottom: 16px; }
  h1, h2, p { margin-top: 0; }
  h1 { margin: 0; font-size: 23px; letter-spacing: -.04em; line-height: 1.1; }
  h2 { margin-bottom: 0; font-size: clamp(1.2rem, 2.5vw, 1.65rem); letter-spacing: -.045em; }
  .fixture, .state, .controls, .timeline-frame, .source-card, .table-wrap { border: 1px solid var(--fg); background: var(--panel); }
  .fixture { margin-top: 16px; padding: 9px 12px; font-size: .78rem; font-weight: 700; }
  .state { width: min(550px, 100%); margin: 36px auto; padding: 20px; }
  .login p { color: var(--muted); line-height: 1.6; }
  form { display: grid; gap: 8px; margin-top: 16px; }
  label { font-size: .8rem; font-weight: 700; }
  input { border: 1px solid var(--fg); border-radius: 0; padding: 9px 10px; background: var(--panel); color: var(--fg); }
  button { border: 1px solid var(--fg); border-radius: 0; background: var(--fg); color: var(--panel); cursor: pointer; padding: 8px 11px; }
  button:hover, button:focus-visible, .active, .chosen { background: var(--panel); color: var(--fg); outline: 2px solid var(--fg); outline-offset: 2px; }
  button:disabled { cursor: wait; opacity: .55; }
  .quiet-button { background: var(--panel); color: var(--fg); white-space: nowrap; }
  .controls { display: flex; justify-content: space-between; gap: 12px; align-items: center; margin: 16px 0 26px; padding: 9px 10px; }
  .control-group { display: flex; align-items: center; gap: 6px; }
  .control-group span, .updated, .count, .caption { color: var(--muted); font-size: .8rem; }
  .updated { margin: 0; text-align: right; }
  .section-title { display: flex; align-items: flex-end; justify-content: space-between; gap: 12px; margin-bottom: 9px; }
  .source-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); gap: 8px; }
  .source-card { min-height: 150px; color: var(--fg); display: flex; flex-direction: column; align-items: flex-start; padding: 13px; text-align: left; }
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
  .table-wrap { overflow: auto; max-height:480px; }
  table { border-collapse: collapse; min-width: 760px; width: 100%; }
  th, td { border-bottom: 1px solid var(--line); padding: 9px 11px; text-align: left; font-size: .78rem; }
  th { background: var(--soft); font-size: .72rem; letter-spacing: .05em; text-transform: uppercase; }
  tr:last-child td { border-bottom: 0; }
  code { font-size: .75rem; }
  .inspect { background: var(--panel); color: var(--fg); padding: 6px 9px; font-size: .75rem; }
  .empty { color: var(--muted); padding: 24px; }
  .error { border-left: 4px solid var(--fg); margin: 12px 0; padding: 9px; background: var(--soft); }
  .drawer-backdrop { background: rgb(0 0 0 / .34); inset: 0; position: fixed; z-index:19; }
  .drawer { background: var(--panel); border-left: 3px solid var(--fg); bottom: 0; max-width: min(580px, 100vw); overflow: auto; padding: 20px; position: fixed; right: 0; top: 0; width: 100%; z-index: 20; }
  .drawer-close { float: right; }
  .sr-only { height: 1px; margin: -1px; overflow: hidden; position: absolute; width: 1px; }
  @media (max-width: 620px) {
    main { width: min(100% - 20px, 1280px); padding-top: 16px; }
    .controls, .section-title { align-items: flex-start; flex-direction: column; }
    .updated { text-align: left; }
    .control-group { flex-wrap: wrap; }
  }
</style>
