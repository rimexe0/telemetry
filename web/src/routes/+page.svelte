<script lang="ts">
  import '$lib/controls.css';
  import '$lib/panel.css';
  import { onDestroy, onMount, tick } from 'svelte';
  import { replaceState } from '$app/navigation';
  import HealthHistory from '$lib/HealthHistory.svelte';
  import DayTimeline from '$lib/DayTimeline.svelte';
  import EventDetail from '$lib/EventDetail.svelte';
  import PlanView from '$lib/PlanView.svelte';
  import Openings from '$lib/Openings.svelte';
  import RulesView from '$lib/RulesView.svelte';
  import ActivityView from '$lib/ActivityView.svelte';
  import HabitsView from '$lib/HabitsView.svelte';
  import SignInSettings from '$lib/SignInSettings.svelte';
  import TodayView from '$lib/TodayView.svelte';
  import PlaceNav from '$lib/PlaceNav.svelte';
  import ActivityBar from '$lib/ActivityBar.svelte';
  import SettingsHome from '$lib/SettingsHome.svelte';
  import SourcesView from '$lib/SourcesView.svelte';
  import DevicesView from '$lib/DevicesView.svelte';
  import { performNowAction, type NowAction, type NowSnapshot, type NowView } from '$lib/execution';
  import { dayKey, dayStart } from '$lib/health';
  import { inPhoneApp, loadKey, parseHash, routeHash, samePlace, settingsPages, type ActivitySection, type LoadKey, type Place, type Route } from '$lib/nav';
  import { clearClerk, loadClerk, type ClerkClient } from '$lib/clerk';
  import {
    APIError,
    approvePairing,
    collectionHealth,
    createActivity,
    endPanelSession,
    getAuthConfig,
    getWakeTime,
    getCurrentAlignment,
    getCurrentAttendedExecutionSession,
    getCurrentPrimaryExecutionSession,
    getPlan,
    getPlanningSettings,
    listActivities,
    listPlanVersions,
    listObservations,
    listDevices,
    listSources,
    loadHealthHistory,
    loadObservationHistory,
    iterateObservationHistory,
    listPendingPairings,
    pairingDetails,
    recordExecutionEvent,
    returnToPrimary,
    revokeDevice,
    panelSession,
    savePlan,
    setClerkTokenProvider,
    setPlanBlockState,
    startExecutionSession,
    startPanelSession,
    updatePlanningSettings,
    updateActivity,
    type Activity,
    type ActivityInput,
    type AlignmentView,
    type AuthConfig,
    type CollectionHealth,
    type Device,
    type Observation,
    type PairingDetails,
    type PendingPairing,
    type Plan,
    type PlanBlock,
    type PlanInput,
    type PlanVersionSummary,
    type PlanningSettings,
    type SourceSummary
  } from '$lib/api';
  import { assessPlanBlock, type PlanAssessment } from '$lib/planActual';

  type Range = '24h' | '7d';

  const fixtureMode = import.meta.env.VITE_TELEMETRY_FIXTURE === '1';
  const snapshotMode = import.meta.env.VITE_TELEMETRY_SNAPSHOT === '1';

  // Four places: Today, Habits, Activity and Settings. The place, the
  // Activity section and its day live in the address (#activity/timeline/
  // 2026-11-19), so a refresh comes back to the same place; old addresses
  // (#now, #day/…, #rules…) open where their contents went.
  let route: Route = { place: 'today' };
  // What the place loads, named as the old tabs were; set with the route so
  // a refresh started at once loads the new place.
  let view: LoadKey = 'now';
  let selectedDay = dayKey(Date.now());
  // The Activity section the nav goes back to.
  let lastSection: ActivitySection = 'timeline';
  // Where the Day timeline starts: when you woke that day, else midnight.
  let dayWindowStart: number | undefined;
  // Inside the phone app's WebView: the phone's own settings show, sign-in is the app's.
  let inApp = false;

  let locationReady = false;
  function setRoute(next: Route): void {
    const moved = !samePlace(route, next);
    route = next;
    view = loadKey(next);
    if (next.place === 'activity') {
      lastSection = next.section;
      selectedDay = next.day ?? dayKey(Date.now());
    }
    if (moved && locationReady) void tick().then(() => window.scrollTo({ top: 0 }));
  }
  function readLocation(): void {
    setRoute(parseHash(window.location.hash, dayKey(Date.now())));
  }
  function currentHash(current: Route, day: string): string {
    const today = dayKey(Date.now());
    return routeHash(current.place === 'activity' ? { ...current, day } : current, today);
  }
  function writeLocation(current: Route, day: string): void {
    const hash = currentHash(current, day);
    if (window.location.hash === hash) return;
    try { replaceState(`${window.location.pathname}${window.location.search}${hash}`, {}); } catch {}
  }
  $: if (locationReady) writeLocation(route, selectedDay);
  // A link to another place (the nav, a row in Settings) changes the hash.
  function onHashChange(): void {
    readLocation();
    void refresh();
  }
  function navigate(hash: string): void {
    if (window.location.hash === hash) onHashChange();
    else window.location.hash = hash;
  }
  $: navHrefs = {
    today: '#today',
    habits: '#habits',
    activity: routeHash({ place: 'activity', section: lastSection, day: selectedDay }, dayKey(Date.now())),
    settings: '#settings'
  } as Record<Place, string>;
  function changeActivityDay(day: string): void {
    selectedDay = day;
    void refresh();
  }
  const todayTitle = () => new Date().toLocaleDateString('en-GB', { weekday: 'short', day: 'numeric', month: 'short', timeZone: 'Europe/Istanbul' });
  let dayObservations: Observation[] = [];
  let dayLoading = false;
  let dayError = '';
  let dayStatus = '';
  let dayRequest: AbortController | undefined;
  let activeDayLoad: { day: string; sourceKeys: string[]; promise: Promise<Observation[]> } | undefined;
  let healthObservations: Observation[] = [];
  let healthError = '';
  let healthStatus = '';
  let healthSource = '';
  let healthLoading = false;
  let healthRequest: AbortController | undefined;
  let devices: Device[] = [];
  let devicesLoading = false;
  let devicesError = '';
  let activities: Activity[] = [];
  let activitiesLoading = false;
  let activitiesError = '';
  let plan: Plan | null = null;
  let planLoading = false;
  let planError = '';
  let planVersions: PlanVersionSummary[] = [];
  let planningSettings: PlanningSettings | undefined;
  let planObservations: Observation[] = [];
  let planEvidenceLoading = false;
  let planEvidenceError = '';
  let currentAlignment: AlignmentView | undefined;
  let currentAlignmentError = '';
  let nowSnapshot: NowSnapshot | undefined;
  let nowLoading = false;
  let nowError = '';
  $: planAssessments = Object.fromEntries((plan?.blocks ?? []).map(block => [block.id, assessPlanBlock(block, block.activity_id ? activities.find(activity => activity.id === block.activity_id) : undefined, planObservations, sources, activities)])) as Record<number, PlanAssessment>;
  let collectionStatus: CollectionHealth | undefined;
  let collectionError = '';
  let collectionRequest: AbortController | undefined;
  $: healthSources = sources.filter(source => source.source_kind === 'health_connect');
  let authenticated = false;
  let checkingSession = true;
  let loading = false;
  let loadingView: LoadKey | undefined;
  let refreshQueued = false;
  let refreshQueuedForceDay = false;
  let error = '';
  let token = '';
  let authConfig: AuthConfig | undefined;
  let clerk: ClerkClient | undefined;
  let clerkLoading = false;
  let clerkError = '';
  let clerkUserId = '';
  let clerkAccessBlocked = false;
  let pairingId = '';
  let pairing: PairingDetails | undefined;
  let pairingLoading = false;
  let pairingError = '';
  let pairingIntent = false;
  let pairingApproved = false;
  let pendingPairings: PendingPairing[] = [];
  let pendingApproving = '';
  let pendingError = '';
  let clerkUnsubscribe: (() => void) | undefined;
  let sources: SourceSummary[] = [];
  let observations: Observation[] = [];
  let selectedSource = '';
  let range: Range = '24h';
  let selectedObservation: Observation | undefined;
  let updatedAt: Date | undefined;
  let timer: ReturnType<typeof setInterval> | undefined;
  const historyCacheMaxAge = 5 * 60_000;
  let historyCache: Array<{ day: string; sourceKeys: string[]; from: number; to: number; rows: Observation[]; savedAt: number }> = [];

  function cachedHistory(day: string, sourceKeys: string[], from: number, to: number): Observation[] | undefined {
    const now = Date.now();
    const required = new Set(sourceKeys);
    const cache = historyCache.find(entry => entry.day === day && now - entry.savedAt < historyCacheMaxAge &&
      entry.from <= from && entry.to > from && sourceKeys.every(key => entry.sourceKeys.includes(key)));
    return cache?.rows.filter(row => {
      const occurred = Date.parse(row.occurred_at);
      return required.has(row.source_instance_key) && occurred >= from && occurred < to;
    });
  }

  function saveHistory(day: string, sourceKeys: string[], from: number, to: number, rows: Observation[]): void {
    historyCache = historyCache.filter(entry => !(entry.day === day && entry.from === from && entry.to === to &&
      entry.sourceKeys.length === sourceKeys.length && sourceKeys.every(key => entry.sourceKeys.includes(key))));
    historyCache.push({ day, sourceKeys: [...sourceKeys], from, to, rows, savedAt: Date.now() });
    if (historyCache.length > 2) historyCache.shift();
  }

  async function loadDayHistory(
    available: SourceSummary[],
    from: Date,
    to: Date,
    signal: AbortSignal,
    onProgress: (rows: Observation[]) => void
  ): Promise<Observation[]> {
    const rows: Observation[] = [];
    for (const source of available) {
      for await (const slice of iterateObservationHistory(source.source_instance_key, from, to, signal)) {
        if (signal.aborted) throw new DOMException('The request was aborted.', 'AbortError');
        rows.push(...slice);
        if (rows.length > 25_000) throw new Error('Day view exceeds the browser record budget. The displayed timeline is partial.');
        onProgress(rows);
      }
    }
    return rows;
  }

  function rangeStart(value: Range) {
    const hours = value === '24h' ? 24 : 24 * 7;
    return new Date(Date.now() - hours * 3_600_000);
  }

  function date(value?: string) {
    return value ? new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short', timeZone: 'Europe/Istanbul' }).format(new Date(value)) : 'No collection yet';
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

  function clerkAccessMessage(cause: unknown): string {
    if (cause instanceof APIError && cause.status === 403) {
      return clerkUserId ? `Access is not enabled for ${clerkUserId}.` : 'This Clerk account is not enabled for the panel.';
    }
    return cause instanceof APIError ? cause.message : 'Unable to authenticate with Clerk.';
  }

  function syncClerkSession(): void {
    clerkUserId = clerk?.user?.id ?? '';
    setClerkTokenProvider(clerk?.session ? () => clerk?.session?.getToken() ?? Promise.resolve(null) : undefined);
  }

  const pairingIntentKey = 'telemetry-pairing-intent';

  function rememberPairingIntent(): void {
    if (!pairingId) return;
    try { sessionStorage.setItem(pairingIntentKey, JSON.stringify({ pairingId, expiresAt: Date.now() + 10 * 60_000 })); } catch {}
  }

  function consumePairingIntent(): boolean {
    try {
      const raw = sessionStorage.getItem(pairingIntentKey);
      if (!raw) return false;
      sessionStorage.removeItem(pairingIntentKey);
      const intent = JSON.parse(raw) as { pairingId?: string; expiresAt?: number };
      return intent.pairingId === pairingId && Number(intent.expiresAt) > Date.now();
    } catch { return false; }
  }

  function onClerkChange(): void {
    syncClerkSession();
    if (!clerk?.isSignedIn) {
      authenticated = false;
      selectedObservation = undefined;
      collectionStatus = undefined;
      return;
    }
    if (pairingId && (pairingIntent || consumePairingIntent())) {
      pairingIntent = false;
      void connectPairing();
    } else {
      void establishClerkSession();
    }
  }

  async function refreshDevices(): Promise<void> {
    devicesLoading = true;
    devicesError = '';
    try {
      devices = await listDevices();
    } catch (cause) {
      devicesError = cause instanceof Error ? cause.message : 'Unable to load devices.';
      if (cause instanceof APIError && cause.status === 401) authenticated = false;
    } finally {
      devicesLoading = false;
    }
  }

  async function refreshPlan(): Promise<void> {
    if (!authenticated) return;
    planLoading = true;
    planError = '';
    activitiesLoading = true;
    activitiesError = '';
    try {
      const [dayPlan, rows, versions, settings] = await Promise.all([getPlan(selectedDay), listActivities(), listPlanVersions(selectedDay), getPlanningSettings()]);
      plan = dayPlan;
      activities = rows;
      planVersions = versions;
      planningSettings = settings;
      currentAlignmentError = '';
      if (selectedDay === dayKey(Date.now())) {
        try {
          currentAlignment = await getCurrentAlignment(new Date());
        } catch (cause) {
          currentAlignment = undefined;
          currentAlignmentError = cause instanceof Error ? cause.message : 'Unable to load current execution alignment.';
          if (cause instanceof APIError && cause.status === 401) authenticated = false;
        }
      } else {
        currentAlignment = undefined;
      }
    } catch (cause) {
      planError = cause instanceof Error ? cause.message : 'Unable to load the plan.';
      if (cause instanceof APIError && cause.status === 401) authenticated = false;
    } finally {
      planLoading = false;
      activitiesLoading = false;
    }
    void refreshPlanEvidence();
  }

  // The NOW view always reads today's plan and the server's current execution;
  // it does not depend on telemetry sources, so stale collection cannot hide it.
  async function refreshNow(): Promise<void> {
    if (!authenticated) return;
    nowLoading = true;
    nowError = '';
    const today = dayKey(Date.now());
    try {
      const [dayPlan, rows, primary, attended, settings] = await Promise.all([
        getPlan(today), listActivities(), getCurrentPrimaryExecutionSession(), getCurrentAttendedExecutionSession(), getPlanningSettings()
      ]);
      nowSnapshot = { day: today, plan: dayPlan, activities: rows, primary, attended, planGateEnabled: settings.daily_plan_gate_enabled };
    } catch (cause) {
      nowSnapshot = undefined;
      nowError = cause instanceof Error ? cause.message : 'Unable to load the current task.';
      if (cause instanceof APIError && cause.status === 401) authenticated = false;
    } finally {
      nowLoading = false;
    }
  }

  async function handleNowAction(current: NowView, action: NowAction): Promise<void> {
    if (!nowSnapshot) return;
    try {
      await performNowAction({ startExecutionSession, recordExecutionEvent, returnToPrimary, setPlanBlockState }, current, nowSnapshot.day, action);
    } finally {
      await refreshNow();
    }
  }

  function openTodayPlan(): void {
    selectedDay = dayKey(Date.now());
    navigate('#activity/plan');
  }

  async function handleSavePlan(input: PlanInput): Promise<void> {
    plan = await savePlan(selectedDay, input);
    planVersions = await listPlanVersions(selectedDay);
  }

  async function handleBlockState(blockId: number, state: PlanBlock['state'], note: string, explanationCode = '', action: 'state' | 'explanation' | 'return' = 'state'): Promise<void> {
    const updated = await setPlanBlockState(selectedDay, blockId, state, note, explanationCode, action);
    if (plan) plan = { ...plan, blocks: plan.blocks.map(block => block.id === updated.id ? updated : block) };
  }

  async function handleCreateActivity(input: ActivityInput): Promise<void> {
    const created = await createActivity(input);
    activities = [...activities, created];
  }

  async function handleUpdateActivity(activity: Activity): Promise<void> {
    const updated = await updateActivity(activity.id, activity);
    activities = activities.map(row => row.id === updated.id ? updated : row);
  }

  async function handleUpdatePlanningSettings(enabled: boolean): Promise<void> {
    planningSettings = await updatePlanningSettings(enabled);
  }

  async function refreshPlanEvidence(): Promise<void> {
    planEvidenceLoading = true;
    planEvidenceError = '';
    const requestedDay = selectedDay;
    try {
      if (!plan) {
        planObservations = [];
        return;
      }
      const available = sources.filter(source => source.source_kind === 'activitywatch' || source.source_kind === 'android_usage');
      if (available.length > 16) throw new Error('Plan evidence supports up to 16 activity sources. Source selection is needed for larger installations.');
      const fromMs = dayStart(requestedDay);
      const toMs = Math.min(fromMs + 86_400_000, Date.now());
      if (!Number.isFinite(fromMs) || toMs <= fromMs) {
        planObservations = [];
        return;
      }
      const sourceKeys = available.map(source => source.source_instance_key);
      const cached = cachedHistory(requestedDay, sourceKeys, fromMs, toMs);
      if (cached) {
        if (authenticated && selectedDay === requestedDay) planObservations = cached;
        return;
      }
      const active = activeDayLoad;
      if (active?.day === requestedDay && sourceKeys.every(key => active.sourceKeys.includes(key))) {
        try {
          const history = await active.promise;
          if (authenticated && selectedDay === requestedDay) {
            planObservations = history.filter(row => {
              const occurred = Date.parse(row.occurred_at);
              return sourceKeys.includes(row.source_instance_key) && occurred >= fromMs && occurred < toMs;
            });
          }
          return;
        } catch (cause) {
          if (!(cause instanceof DOMException && cause.name === 'AbortError')) throw cause;
        }
      }
      const rows: Observation[] = [];
      for (const source of available) {
        rows.push(...await loadObservationHistory(source.source_instance_key, new Date(fromMs), new Date(toMs)));
        if (rows.length > 25_000) throw new Error('Plan evidence exceeds the browser record budget. No partial alignment is shown.');
      }
      if (authenticated && selectedDay === requestedDay) {
        planObservations = rows;
        saveHistory(requestedDay, sourceKeys, fromMs, toMs, rows);
      }
    } catch (cause) {
      planEvidenceError = cause instanceof Error ? cause.message : 'Unable to load plan evidence.';
    } finally {
      planEvidenceLoading = false;
    }
  }

  async function refreshPairing(): Promise<void> {
    if (!pairingId || !(clerk?.isSignedIn || authenticated)) return;
    pairingLoading = true;
    pairingError = '';
    try {
      pairing = await pairingDetails(pairingId);
    } catch (cause) {
      pairingError = clerkAccessMessage(cause);
    } finally {
      pairingLoading = false;
    }
  }

  async function connectPairing(): Promise<void> {
    if (!pairingId || !(clerk?.isSignedIn || authenticated)) return;
    pairingLoading = true;
    pairingError = '';
    try {
      if (!pairing) pairing = await pairingDetails(pairingId);
      await approvePairing(pairingId);
      pairingApproved = true;
      if (pairing) pairing = { ...pairing, status: 'approved' };
    } catch (cause) {
      pairingError = clerkAccessMessage(cause);
    } finally {
      pairingLoading = false;
    }
  }

  async function establishClerkSession(): Promise<void> {
    syncClerkSession();
    try {
      authenticated = await panelSession();
      clerkAccessBlocked = !authenticated;
      if (!authenticated) clerkError = `Access is not enabled for ${clerkUserId}.`;
      if (authenticated) await refresh();
    } catch (cause) {
      if (cause instanceof APIError && cause.status === 403) {
        authenticated = false;
        clerkAccessBlocked = true;
        clerkError = clerkAccessMessage(cause);
        return;
      }
      throw cause;
    }
  }

  async function signInWithClerk(): Promise<void> {
    if (!clerk) return;
    clerkLoading = true;
    clerkError = '';
    pairingIntent = Boolean(pairingId);
    if (pairingIntent) rememberPairingIntent();
    try {
      if (clerk.isSignedIn) {
        if (pairingIntent && pairingId) await connectPairing();
        else await establishClerkSession();
        return;
      }
      await clerk.openSignIn({ redirectUrl: window.location.href });
    } catch (cause) {
      clerkError = clerkAccessMessage(cause);
    } finally {
      clerkLoading = false;
    }
  }

  // Devices waiting to be connected can be approved from any signed-in panel.
  async function refreshPendingPairings(): Promise<void> {
    try {
      pendingPairings = await listPendingPairings();
    } catch {
      pendingPairings = [];
    }
  }

  async function approvePending(item: PendingPairing): Promise<void> {
    pendingApproving = item.pairing_id;
    pendingError = '';
    try {
      await approvePairing(item.pairing_id);
      await refreshPendingPairings();
      if (view === 'devices') await refreshDevices();
    } catch (cause) {
      pendingError = cause instanceof Error ? cause.message : 'Could not connect the device.';
    } finally {
      pendingApproving = '';
    }
  }

  async function revoke(device: Device): Promise<void> {
    devicesError = '';
    try {
      await revokeDevice(device.device_id);
      devices = devices.map(item => item.device_id === device.device_id ? { ...item, revoked_at: new Date().toISOString() } : item);
    } catch (cause) {
      devicesError = cause instanceof Error ? cause.message : 'Unable to revoke device.';
    }
  }

  async function refresh(forceDay = false) {
    if (!authenticated) return;
    if (loading) {
      if (view !== loadingView) {
        refreshQueued = true;
        refreshQueuedForceDay ||= forceDay;
      }
      return;
    }
    void refreshPendingPairings();
    const requestedView = view;
    if (requestedView !== 'day') {
      loading = true;
      loadingView = requestedView;
    }
    error = '';
    try {
      const now = new Date();
      if (requestedView === 'now') {
        await refreshNow();
      } else if (requestedView === 'self') {
        // Habits, Apps, Sign-in and the Settings list load their own parts.
      } else if (requestedView === 'collection') {
        const [rows] = await Promise.all([listObservations({
          sourceInstanceKey: selectedSource || undefined,
          from: rangeStart(range),
          to: now
        }), refreshCollection()]);
        if (authenticated) {
          observations = rows;
          // The health response has source summaries from the same snapshot.
          // Older servers can still serve the existing source inspection view.
          sources = collectionStatus?.sources ?? await listSources();
        }
      } else {
        sources = await listSources();
        if (requestedView === 'health') await refreshHealth();
        else if (requestedView === 'devices') await refreshDevices();
        else if (requestedView === 'plan' || requestedView === 'rules') await refreshPlan();
        else await refreshDay(forceDay);
      }
      if (selectedObservation) selectedObservation = (requestedView === 'health' ? healthObservations : requestedView === 'day' ? dayObservations : [...observations, ...(collectionStatus?.devices.flatMap(d => d.resource_history) ?? [])]).find(event => event.source_record_key === selectedObservation?.source_record_key && event.source_instance_key === selectedObservation?.source_instance_key);
      updatedAt = new Date();
    } catch (cause) {
      error = cause instanceof APIError ? cause.message : 'Unable to load the current telemetry view.';
      if (cause instanceof APIError && cause.status === 401) authenticated = false;
    } finally {
      if (requestedView !== 'day') {
        loading = false;
        loadingView = undefined;
      }
      if (refreshQueued) {
        const nextForceDay = refreshQueuedForceDay;
        refreshQueued = false;
        refreshQueuedForceDay = false;
        void refresh(nextForceDay);
      }
    }
  }

  async function refreshCollection() {
    collectionRequest?.abort();
    const request = new AbortController();
    collectionRequest = request;
    collectionError = '';
    try {
      const result = await collectionHealth(request.signal);
      if (!request.signal.aborted && authenticated) collectionStatus = result;
    } catch (cause) {
      if (request.signal.aborted) return;
      collectionError = cause instanceof APIError && cause.status === 404
        ? 'This server does not provide collection-health reports yet.'
        : cause instanceof Error ? cause.message : 'Unable to load collection reports.';
      if (cause instanceof APIError && (cause.status === 401 || cause.status === 403)) {
        collectionStatus = undefined;
        selectedObservation = undefined;
        authenticated = false;
      }
    }
  }

  async function refreshDay(force = false) {
    dayRequest?.abort();
    const request = new AbortController(); dayRequest = request;
    let historyPromise: Promise<Observation[]> | undefined;
    dayLoading = true; dayError = ''; dayStatus = '';
    try {
      const requestedDay = selectedDay;
      const lo = dayStart(requestedDay);
      if (!Number.isFinite(lo)) throw new Error('Choose a valid date.');
      if (lo >= Date.now()) throw new Error('Choose today or an earlier day.');
      // The day starts when you woke, not at midnight: the server finds the
      // end of the last long quiet stretch before 18:00 that day.
      // It starts at the wake time you set instead when that came first, so
      // a missed wake time stays in view.
      const wake = await getWakeTime(new Date(Math.min(lo + 18 * 3_600_000, Date.now())), request.signal).catch(() => null);
      const inDay = (value: string | null | undefined) => {
        const at = value ? Date.parse(value) : NaN;
        return Number.isFinite(at) && at >= lo - 6 * 3_600_000 && at < lo + 18 * 3_600_000 ? at : undefined;
      };
      const wokeAt = inDay(wake?.woke);
      const plannedAt = inDay(wake?.planned);
      dayWindowStart = Math.min(wokeAt ?? Infinity, plannedAt ?? Infinity);
      if (!Number.isFinite(dayWindowStart)) dayWindowStart = lo;
      const start = dayWindowStart;
      const from = new Date(start - 6 * 3_600_000);
      const to = new Date(Math.min(start + 86_400_000, Date.now()));
      // Only sources that sent anything since the window began: old phones
      // and one-off imports would otherwise crowd out the ones in use.
      const kinds = ['activitywatch', 'health_connect', 'android_usage'];
      const candidates = sources
        .filter(s => kinds.includes(s.source_kind) && (!s.last_collected_at || Date.parse(s.last_collected_at) >= from.getTime()))
        .sort((a, b) => Date.parse(b.last_collected_at ?? '') - Date.parse(a.last_collected_at ?? ''));
      const available = candidates.slice(0, 16);
      const skipped = candidates.length - available.length;
      const sourceKeys = available.map(source => source.source_instance_key);
      const cached = force ? undefined : cachedHistory(requestedDay, sourceKeys, from.getTime(), to.getTime());
      if (cached) {
        if (request.signal.aborted || !authenticated || selectedDay !== requestedDay) return;
        dayObservations = cached;
        dayStatus = `Showing ${cached.length.toLocaleString()} recently cached records for ${requestedDay}.`;
        return;
      }
      historyPromise = loadDayHistory(available, from, to, request.signal, rows => {
        if (request.signal.aborted || !authenticated || selectedDay !== requestedDay) return;
        dayObservations = [...rows];
        dayStatus = `Loading ${rows.length.toLocaleString()} records for ${requestedDay}; the timeline is partial until loading finishes.`;
      });
      activeDayLoad = { day: requestedDay, sourceKeys, promise: historyPromise };
      const rows = await historyPromise;
      if (request.signal.aborted || !authenticated || selectedDay !== requestedDay) return;
      dayObservations = rows;
      saveHistory(requestedDay, sourceKeys, from.getTime(), to.getTime(), rows);
      const clock = (at: number) => new Date(at).toLocaleTimeString('en-GB', { timeZone: 'Europe/Istanbul', hour: '2-digit', minute: '2-digit' });
      const began = start === lo ? 'Starts at midnight: no wake time was found.'
        : plannedAt !== undefined && start === plannedAt
          ? `Starts at ${clock(plannedAt)}, the wake time you set${wokeAt !== undefined && wokeAt > plannedAt ? `; you woke at ${clock(wokeAt)}` : ''}.`
          : `Starts when you woke, at ${clock(start)}.`;
      dayStatus = `Loaded ${rows.length.toLocaleString()} records for ${requestedDay} at ${date(new Date().toISOString())}. ${began} UTC+03:00. Gaps mean unknown activity.${skipped > 0 ? ` ${skipped} less active source${skipped === 1 ? ' was' : 's were'} left out.` : ''}`;
    } catch (cause) {
      if (request.signal.aborted) return;
      const detail = cause instanceof Error ? cause.message.replace(/ No partial totals are shown\.$/, '') : 'Unable to load the day.';
      dayError = `The timeline may be partial. ${detail}`;
      if (cause instanceof APIError && cause.status === 401) authenticated = false;
    } finally {
      if (historyPromise && activeDayLoad?.promise === historyPromise) activeDayLoad = undefined;
      if (dayRequest === request) dayLoading = false;
    }
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
      // A device's pairing link: show it as soon as the panel token signs in.
      void refreshPairing();
      await refresh();
    } catch (cause) {
      error = cause instanceof APIError ? cause.message : 'Unable to start a panel session.';
    } finally {
      loading = false;
    }
  }

  async function signOut() {
    await endPanelSession().catch(() => undefined);
    if (clerk?.isSignedIn) await clerk.signOut().catch(() => undefined);
    clearClerk();
    setClerkTokenProvider(undefined);
    healthRequest?.abort();
    collectionRequest?.abort();
    collectionStatus = undefined;
    collectionError = '';
    authenticated = false;
    dayRequest?.abort();
    dayObservations = [];
    sources = [];
    observations = [];
    healthObservations = [];
    devices = [];
    clerkUserId = '';
    clerkAccessBlocked = false;
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
    inApp = inPhoneApp(navigator.userAgent);
    if (inApp) {
      // The app draws edge to edge: the page pads itself for the system bars.
      document.documentElement.dataset.app = 'phone';
      const viewport = document.querySelector('meta[name="viewport"]');
      if (viewport && !viewport.getAttribute('content')?.includes('viewport-fit')) viewport.setAttribute('content', `${viewport.getAttribute('content')}, viewport-fit=cover`);
    }
    readLocation();
    locationReady = true;
    window.addEventListener('hashchange', onHashChange);
    try {
      pairingId = new URLSearchParams(window.location.search).get('pair') ?? '';
      try {
        authConfig = await getAuthConfig();
      } catch (cause) {
        if (!(cause instanceof APIError && cause.status === 404)) {
          clerkError = 'Clerk sign-in is unavailable; use the panel token if configured.';
        }
      }
      if (authConfig?.publishable_key) {
        clerkLoading = true;
        try {
          clerk = await loadClerk(authConfig.publishable_key);
          syncClerkSession();
          clerkUnsubscribe = clerk.addListener?.(() => onClerkChange());
          if (pairingId && clerk.isSignedIn) await refreshPairing();
        } catch (cause) {
          clerkError = cause instanceof Error ? cause.message : 'Unable to load Clerk.';
        } finally {
          clerkLoading = false;
        }
      }
      authenticated = await panelSession();
      if (clerk?.isSignedIn && !authenticated) {
        clerkAccessBlocked = true;
        clerkError = `Access is not enabled for ${clerkUserId}.`;
      }
      if (authenticated) {
        checkingSession = false;
        if (pairingId && !clerk) void refreshPairing();
        void refresh();
      }
    } catch (cause) {
      if (cause instanceof APIError && cause.status === 403 && clerkUserId) {
        clerkAccessBlocked = true;
        clerkError = clerkAccessMessage(cause);
      } else {
        error = cause instanceof APIError && cause.status === 404 ? 'Panel sessions are not configured on this server.' : 'Unable to check the panel session.';
      }
    } finally {
      checkingSession = false;
    }
    timer = setInterval(() => {
      if (document.visibilityState !== 'visible' || snapshotMode) return;
      if (view === 'collection' || view === 'now' || (!dayLoading && updatedAt && Date.now() - updatedAt.getTime() >= 300_000)) void refresh();
    }, 30_000);
  });

  onDestroy(() => {
    if (timer) clearInterval(timer);
    themeQuery?.removeEventListener('change', applyTheme);
    if (typeof window !== 'undefined') window.removeEventListener('hashchange', onHashChange);
    clerkUnsubscribe?.();
    setClerkTokenProvider(undefined);
    healthRequest?.abort();
    dayRequest?.abort();
    collectionRequest?.abort();
  });

  $: themeLabel = themeMode === 'system' ? `System · ${theme}` : themeMode === 'light' ? 'Light' : 'Dark';
  $: settingsPage = route.place === 'settings' ? route.page : undefined;
  $: activitySection = route.place === 'activity' ? route.section : 'timeline';
</script>

<svelte:head>
  <title>Telemetry panel</title>
  <meta name="description" content="Sleep, movement and heart-rate history with inspectable source records." />
</svelte:head>
<svelte:window on:keydown={(event) => { if (event.key === 'Escape') selectedObservation = undefined; }} />

<div class="shell" class:with-nav={authenticated && !checkingSession}>
  {#if authenticated && !checkingSession}
    <PlaceNav current={route.place} hrefs={navHrefs} />
  {/if}

  <main>
    {#if fixtureMode}
      <aside class="fixture" aria-label="Fixture preview notice">Fixture data</aside>
    {/if}
    {#if snapshotMode}<aside class="fixture">Local snapshot</aside>{/if}

    {#if checkingSession}
      <section class="state"><p>Loading…</p></section>
    {:else if !authenticated}
      <header class="masthead">
        <h1>Telemetry</h1>
        <div class="header-actions"><button class="quiet-button" aria-label="Change theme" on:click={cycleTheme}>{themeMode === 'system' ? `System · ${theme}` : `Theme · ${themeMode}`}</button>
        {#if clerk?.isSignedIn && !inApp}<button class="quiet-button" on:click={signOut}>Sign out</button>{/if}</div>
      </header>
      <section class="login state" aria-labelledby="panel-login-heading">
        <h2 id="panel-login-heading">Sign in</h2>
        {#if pairingId && clerk?.isSignedIn}
          <div class="pairing pairing-login" aria-labelledby="pairing-login-heading">
            <div class="section-title"><h3 id="pairing-login-heading">Connect device</h3><span class="count">{pairing?.status ?? 'pending'}</span></div>
            {#if pairingLoading}<p class="muted">Checking pairing…</p>
            {:else if pairingApproved}<p>{pairing?.device_name ?? 'Device'} connected.</p>
            {:else if pairing}<div class="pairing-row"><strong>{pairing.device_name}</strong><span>{pairing.device_id}</span><button on:click={connectPairing} disabled={pairing.status !== 'pending'}>Connect</button></div>
            {:else}<p class="error" role="alert">{pairingError || 'Pairing session unavailable.'}</p>{/if}
            {#if pairingError && pairing}<p class="error" role="alert">{pairingError}</p>{/if}
          </div>
        {:else if authConfig?.publishable_key && clerk && !authConfig.panel_token_enabled}
          <button type="button" on:click={signInWithClerk} disabled={clerkLoading}>{clerkLoading ? 'Loading Clerk…' : 'Sign in with Clerk'}</button>
          {#if clerkAccessBlocked && clerkUserId}<p class="access-note">Signed in as <code>{clerkUserId}</code></p>{/if}
          {#if clerkError}<p class="error" role="alert">{clerkError}</p>{/if}
        {:else}
          {#if authConfig?.publishable_key && clerk}
            <button type="button" on:click={signInWithClerk} disabled={clerkLoading}>{clerkLoading ? 'Loading Clerk…' : 'Sign in with Clerk'}</button>
            {#if clerkAccessBlocked && clerkUserId}<p class="access-note">Signed in as <code>{clerkUserId}</code></p>{/if}
            {#if clerkError}<p class="error" role="alert">{clerkError}</p>{/if}
            <p class="access-note">or with the panel token</p>
          {/if}
          <form on:submit|preventDefault={signIn}>
            <label for="panel-token">Panel token</label>
            <input id="panel-token" bind:value={token} type="password" autocomplete="current-password" required />
            <button type="submit" disabled={loading}>{loading ? 'Opening…' : 'Open panel'}</button>
          </form>
          {#if error}<p class="error" role="alert">{error}</p>{/if}
        {/if}
      </section>
    {:else}
      {#each pendingPairings as item (item.pairing_id)}
        <section class="pairing" aria-label="Device waiting to connect">
          <div class="pairing-row"><strong>{item.device_name || 'A device'} wants to connect{item.returning ? ' again' : ''}</strong><span>{item.device_id}{item.returning ? ' · was connected before; connecting replaces its old key. Only connect it if you just set it up again.' : ''}{item.continues ? ` · the same phone as ${item.continues}, set up again; it keeps that phone's history` : ''}</span>
            <button on:click={() => approvePending(item)} disabled={pendingApproving === item.pairing_id}>{pendingApproving === item.pairing_id ? 'Connecting…' : 'Connect'}</button></div>
          {#if pendingError}<p class="error" role="alert">{pendingError}</p>{/if}
        </section>
      {/each}
      {#if pairingId}
        <section class="pairing" aria-labelledby="pairing-heading">
          <div class="section-title"><h2 id="pairing-heading">Connect device</h2><span class="count">{pairing?.status ?? 'pending'}</span></div>
          {#if pairingLoading}<p class="muted">Checking pairing…</p>
          {:else if pairingApproved}<p>{pairing?.device_name ?? 'Device'} connected.</p>
          {:else if pairing}
            <div class="pairing-row"><strong>{pairing.device_name}</strong><span>{pairing.device_id}</span><button on:click={connectPairing} disabled={pairingLoading || pairing.status !== 'pending'}>Connect</button></div>
          {:else if clerk?.isSignedIn || !clerk}<p class="error" role="alert">{pairingError || 'Pairing session unavailable.'}</p>
          {:else}<button on:click={signInWithClerk} disabled={clerkLoading}>{clerkLoading ? 'Loading Clerk…' : 'Sign in to connect'}</button>{/if}
          {#if pairingError && pairing}<p class="error" role="alert">{pairingError}</p>{/if}
        </section>
      {/if}

      {#if route.place === 'today'}
        <header class="page-head"><h1>Today</h1><span class="muted small">{todayTitle()}</span></header>
        <div class="narrow">
          <TodayView snapshot={nowSnapshot} loading={nowLoading} error={nowError || error} onaction={handleNowAction} onopenplan={openTodayPlan} onrefresh={() => refresh()} />
        </div>
      {:else if route.place === 'habits'}
        <header class="page-head"><h1>Habits</h1></header>
        <div class="narrow wide-ok"><HabitsView /></div>
      {:else if route.place === 'activity'}
        <header class="page-head"><h1>Activity</h1></header>
        <ActivityBar section={activitySection} day={selectedDay} today={dayKey(Date.now())} onday={changeActivityDay} />
        {#if activitySection === 'timeline'}
          {#if error}<p class="error" role="alert">{error}</p>{/if}
          <DayTimeline picker={false} observations={dayObservations} {sources} start={dayWindowStart} bind:date={selectedDay} loading={dayLoading} error={dayError || error} status={dayStatus} ondatechange={refresh} onrefresh={() => refresh(true)} oninspect={(o) => selectedObservation = o} />
        {:else if activitySection === 'apps'}
          <ActivityView picker={false} date={selectedDay} />
        {:else if activitySection === 'health'}
          <div class="health-toolbar">
            {#if healthSources.length > 1}
              <label>Device <select bind:value={healthSource} on:change={refreshHealth}>{#each healthSources as source}<option value={source.source_instance_key}>{source.device_id}</option>{/each}</select></label>
            {/if}
            <span>UTC+03:00</span>
            <button class="quiet-button" disabled={loading || healthLoading} on:click={() => refresh()}>Refresh health</button>
          </div>
          {#if error}<p class="error" role="alert">{error}</p>{/if}
          <HealthHistory observations={healthObservations} loading={healthLoading} error={healthError} oninspect={(o) => selectedObservation = o} />
        {:else}
          <PlanView picker={false} day={selectedDay} today={dayKey(Date.now())} {activities} {plan} {activitiesLoading} {planLoading} activitiesError={activitiesError} planError={planError}
            {planVersions} assessments={planAssessments} {planEvidenceLoading} {planEvidenceError}
            {currentAlignment} currentAlignmentError={currentAlignmentError}
            onrefresh={refreshPlan} onchangeday={changeActivityDay} onsave={handleSavePlan} onsetstate={handleBlockState}
            oncreateactivity={handleCreateActivity} onupdateactivity={handleUpdateActivity} />
        {/if}
      {:else if !settingsPage}
        <header class="page-head"><h1>Settings</h1></header>
        <SettingsHome {inApp} {themeLabel} oncycletheme={cycleTheme} onsignout={signOut} />
      {:else}
        <a class="back" href="#settings">‹ Settings</a>
        <header class="page-head"><h1>{settingsPages[settingsPage]}</h1></header>
        <div class="settings-page">
          {#if settingsPage === 'budget' || settingsPage === 'distractions' || settingsPage === 'lock'}
            <RulesView part={settingsPage} {activities} {planningSettings} onupdatesettings={handleUpdatePlanningSettings} onleave={() => navigate('#settings')} />
          {:else if settingsPage === 'times'}
            <div class="card"><Openings /></div>
            <RulesView part="times" {activities} {planningSettings} onupdatesettings={handleUpdatePlanningSettings} onleave={() => navigate('#settings')} />
          {:else if settingsPage === 'devices'}
            <DevicesView {devices} loading={devicesLoading} error={devicesError} onrefresh={refreshDevices} onrevoke={revoke} />
          {:else if settingsPage === 'sources'}
            <SourcesView {collectionStatus} {collectionError} {error} {loading} {sources} {observations} {selectedSource} {range} {updatedAt}
              onsource={selectSource} onrange={selectRange} oninspect={(observation) => selectedObservation = observation} onrefresh={refresh} />
          {:else if settingsPage === 'sign-in'}
            {#if inApp}
              <p class="muted small">Inside the phone app, signing in is the app's own.</p>
            {:else}
              <SignInSettings />
            {/if}
          {/if}
        </div>
      {/if}
    {/if}
  </main>
</div>

{#if authenticated && selectedObservation}
  <div class="drawer-backdrop" role="presentation" on:click={() => (selectedObservation = undefined)}></div>
  <aside class="drawer" aria-label="Observation detail">
    <button class="quiet-button drawer-close" on:click={() => (selectedObservation = undefined)}>Close</button>
    <EventDetail observation={selectedObservation} observations={view === 'health' ? healthObservations : view === 'day' ? dayObservations : observations} />
  </aside>
{/if}

<style>
  :global(:root) { color-scheme: dark; --bg:#111213; --panel:#191a1b; --fg:#eeeeeb; --muted:#aaa9a4; --line:#454647; --soft:#242628; --done:#2f8f5b; }
  :global(:root[data-theme="light"]) { color-scheme:light; --bg:#f5f5f2; --panel:#fff; --fg:#181918; --muted:#63645f; --line:#c4c5bf; --soft:#e9eae5; --done:#2f8f5b; }
  @media (prefers-color-scheme: light) {
    :global(:root:not([data-theme])) { color-scheme:light; --bg:#f5f5f2; --panel:#fff; --fg:#181918; --muted:#63645f; --line:#c4c5bf; --soft:#e9eae5; --done:#2f8f5b; }
  }
  /* Inside the phone app the page reaches under the system bars. */
  :global(:root[data-app="phone"]) { --safe-top: env(safe-area-inset-top, 0px); --safe-right: env(safe-area-inset-right, 0px); --safe-bottom: env(safe-area-inset-bottom, 0px); --safe-left: env(safe-area-inset-left, 0px); }
  :global(*) { box-sizing: border-box; }
  :global(body) { margin: 0; background: var(--bg); color: var(--fg); font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; overflow-x: hidden; }
  :global(button), :global(input) { font: inherit; }

  .shell { margin: 0 auto; padding: 0 calc(12px + var(--safe-right, 0px)) 0 calc(12px + var(--safe-left, 0px)); width: min(1440px, 100%); }
  main { min-width: 0; padding: calc(16px + var(--safe-top, 0px)) 0 24px; }
  .shell.with-nav main { padding-bottom: calc(76px + var(--safe-bottom, 0px)); }
  @media (min-width: 700px) {
    .shell { padding: 0 24px; }
    .shell.with-nav { column-gap: 32px; display: grid; grid-template-columns: 180px minmax(0, 1fr); }
    .shell.with-nav main { padding: calc(22px + var(--safe-top, 0px)) 0 32px; }
  }
  .narrow { max-width: 720px; }
  .back { color: var(--muted); display: inline-flex; align-items: center; font-size: .85rem; min-height: 44px; margin: -10px 0 0; text-decoration: none; }
  .back:hover, .back:focus-visible { color: var(--fg); text-decoration: underline; }
  .settings-page { max-width: 980px; }
  .settings-page :global(.openings) { border-top: 0; margin-top: 0; padding-top: 0; }

  .header-actions { display:flex; gap:6px; }
  .health-toolbar { display:flex; align-items:center; gap:8px; margin:0 0 12px; flex-wrap:wrap; font-size:12px; }
  .health-toolbar span { flex:1; color:var(--muted); }
  .masthead { display: flex; align-items: flex-start; justify-content: space-between; gap: 20px; border-bottom: 2px solid var(--fg); padding-bottom: 16px; flex-wrap: wrap; }
  h1, h2, p { margin-top: 0; }
  .masthead h1 { margin: 0; font-size: 23px; letter-spacing: -.04em; line-height: 1.1; }
  .fixture, .state { border: 1px solid var(--fg); background: var(--panel); }
  .fixture { margin: 0 0 12px; padding: 9px 12px; font-size: .78rem; font-weight: 700; }
  .state { width: min(550px, 100%); margin: 36px auto; padding: 20px; }
  .login p { color: var(--muted); line-height: 1.6; }
  .access-note, .muted { color: var(--muted); font-size: .8rem; }
  .pairing { border: 1px solid var(--fg); background: var(--panel); margin: 0 0 14px; padding: 12px; }
  .pairing h2, .pairing h3 { margin: 0; font-size: 1rem; }
  .pairing-login { border: 0; margin: 18px 0 0; padding: 0; }
  .pairing-row { align-items: center; display: flex; flex-wrap: wrap; gap: 12px; justify-content: space-between; }
  .pairing-row span { color: var(--muted); font-size: .78rem; overflow-wrap: anywhere; }
  .section-title { display: flex; align-items: flex-end; justify-content: space-between; gap: 12px; margin-bottom: 9px; }
  .count { color: var(--muted); font-size: .8rem; }
  form { display: grid; gap: 8px; margin-top: 16px; }
  label { font-size: .8rem; font-weight: 700; }
  button { border: 1px solid var(--fg); border-radius: 0; background: var(--fg); color: var(--panel); cursor: pointer; padding: 8px 11px; }
  button:hover, button:focus-visible { background: var(--panel); color: var(--fg); outline: 2px solid var(--fg); outline-offset: 2px; }
  button:disabled { cursor: wait; opacity: .55; }
  .quiet-button { background: var(--panel); color: var(--fg); white-space: nowrap; }
  code { font-size: .75rem; }
  .error { border-left: 4px solid var(--fg); margin: 12px 0; padding: 9px; background: var(--soft); }
  .drawer-backdrop { background: rgb(0 0 0 / .34); inset: 0; position: fixed; z-index:19; }
  .drawer { background: var(--panel); border-left: 3px solid var(--fg); bottom: 0; max-width: min(580px, 100vw); overflow: auto; padding: calc(20px + var(--safe-top, 0px)) 20px calc(20px + var(--safe-bottom, 0px)); position: fixed; right: 0; top: 0; width: 100%; z-index: 20; }
  .drawer-close { float: right; }
</style>
