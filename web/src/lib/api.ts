export type SourceSummary = {
  source_instance_key: string;
  source_kind: string;
  device_id: string;
  first_collected_at: string;
  last_collected_at?: string;
  observation_count: number;
};

export type Observation = {
  source_instance_key: string;
  source_kind: string;
  device_id: string;
  source_record_key: string;
  kind: string;
  occurred_at: string;
  ended_at?: string;
  collected_at: string;
  revision_digest: string;
  original_payload: unknown;
  projection: unknown;
};

export type CollectionDevice = {
  device_id: string;
  last_received_at?: string;
  resource_history: Observation[];
};

export type CollectionHealth = {
  checked_at: string;
  sources: SourceSummary[];
  sources_truncated: boolean;
  devices: CollectionDevice[];
};

export type AuthConfig = {
  publishable_key?: string;
  pairing_enabled?: boolean;
  /** Whether the panel token signs in; absent on older servers. */
  panel_token_enabled?: boolean;
};

export type Device = {
  device_id: string;
  device_name: string;
  created_at: string;
  revoked_at?: string;
};

export type PairingDetails = {
  pairing_id: string;
  device_id: string;
  device_name: string;
  status: string;
  expires_at: string;
};

export class APIError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

let clerkTokenProvider: (() => Promise<string | null>) | undefined;

export function setClerkTokenProvider(provider?: () => Promise<string | null>): void {
  clerkTokenProvider = provider;
}

// Errors come as {error: {code, detail}}, or, for the door and habit calls of
// the budget contract, as {error, message}.
function errorDetail(payload: unknown): string | undefined {
  if (!payload || typeof payload !== 'object') return undefined;
  const { error, message } = payload as { error?: unknown; message?: unknown };
  if (typeof message === 'string' && message) return message;
  if (error && typeof error === 'object') {
    const detail = (error as { detail?: unknown }).detail;
    if (typeof detail === 'string') return detail;
  }
  return typeof error === 'string' && error ? error : undefined;
}

// An in-memory stale-while-revalidate cache for GET answers, so switching
// between places and sections shows the last answer at once (owner
// 2026-11-26: "why when i switch to a different page and switch back it
// needs to get the data again"). Each read of a cached answer returns it and
// asks the server again in the background; an answer that differs replaces
// it and tells the listeners (onFresh), so views redraw with it. A change
// (any other method) drops the cached answers of the same resource, so a view
// that loads after saving never shows what was there before. Sign-in, the
// settings lock and pairing are never cached.
type CacheEntry = { body: string; at: number };

const cache = new Map<string, CacheEntry>();
const revalidating = new Map<string, Promise<void>>();
const freshListeners = new Set<(path: string) => void>();
/** An answer this new is returned without asking again. */
const SETTLED_MS = 2_000;
const MAX_ENTRIES = 200;
const MAX_BODY = 2 << 20;
const uncached = ['/v1/panel/session', '/v1/auth/config', '/v1/lock', '/v1/pairing', '/v1/settings/sign-in'];
/** Resources whose changes move another's answers too. */
const related: Record<string, string[]> = {
  habits: ['budget', 'reasons', 'device'],
  reasons: ['habits', 'budget', 'device'],
  door: ['budget', 'openings', 'policy', 'device'],
  openings: ['budget', 'policy', 'device'],
  policy: ['budget', 'openings', 'device', 'plans'],
  'context-rules': ['policy', 'plans'],
  plans: ['execution', 'alignment', 'policy'],
  execution: ['alignment', 'plans'],
  activities: ['plans', 'activity-labels', 'activity'],
  'activity-labels': ['activities', 'activity', 'policy'],
  'activity-label-proposals': ['activity-labels', 'activities', 'activity', 'policy'],
  morning: ['policy', 'budget']
};

/** The first part of a path after /v1/: the resource it is about. */
function resource(path: string): string {
  return path.replace(/^\/v1\//, '').split(/[/?]/)[0] ?? '';
}

const cacheable = (path: string) => path.startsWith('/v1/') && !uncached.some(prefix => path === prefix || path.startsWith(`${prefix}/`) || path.startsWith(`${prefix}?`));

/** Called with the path whenever a cached answer changed in the background. Returns the way to stop. */
export function onFresh(listener: (path: string) => void): () => void {
  freshListeners.add(listener);
  return () => { freshListeners.delete(listener); };
}

/** Forgets every cached answer (sign-out, tests). */
export function clearCache(): void {
  cache.clear();
  revalidating.clear();
}

/** Forgets the cached answers about a resource and those it moves. */
export function invalidate(path: string): void {
  const changed = resource(path);
  const touched = new Set([changed, ...(related[changed] ?? [])]);
  for (const key of [...cache.keys()]) if (touched.has(resource(key))) cache.delete(key);
}

function remember(path: string, body: string): void {
  if (body.length > MAX_BODY) return;
  cache.delete(path);
  cache.set(path, { body, at: Date.now() });
  while (cache.size > MAX_ENTRIES) cache.delete(cache.keys().next().value as string);
}

async function send(path: string, init?: RequestInit): Promise<{ response: Response; text: string }> {
  const headers = new Headers(init?.headers);
  headers.set('Accept', 'application/json');
  if (clerkTokenProvider) {
    const token = await clerkTokenProvider();
    if (token) headers.set('Authorization', `Bearer ${token}`);
  }
  const response = await fetch(path, {
    ...init,
    credentials: 'same-origin',
    headers
  });
  const text = await response.text().catch(() => '');
  return { response, text };
}

function parse(text: string): unknown {
  try {
    return text ? JSON.parse(text) : undefined;
  } catch {
    return undefined;
  }
}

/** Asks again for a cached answer, once at a time, and tells the listeners when it changed. */
function revalidate(path: string): Promise<void> {
  const running = revalidating.get(path);
  if (running) return running;
  const work = (async () => {
    try {
      const { response, text } = await send(path);
      if (!response.ok) {
        // Signed out: nothing cached may be shown any more.
        if (response.status === 401 || response.status === 403) clearCache();
        return;
      }
      const before = cache.get(path);
      remember(path, text);
      if (before && before.body !== text) for (const listener of [...freshListeners]) listener(path);
    } catch {
      // Offline: the cached answer stays.
    } finally {
      revalidating.delete(path);
    }
  })();
  revalidating.set(path, work);
  return work;
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const method = (init?.method ?? 'GET').toUpperCase();
  const reading = method === 'GET' && cacheable(path);
  if (reading) {
    const entry = cache.get(path);
    if (entry) {
      if (Date.now() - entry.at >= SETTLED_MS) void revalidate(path);
      // Parsed again each time, so a view that changes what it got never changes the cache.
      return parse(entry.body) as T;
    }
  }
  const { response, text } = await send(path, init);
  const payload = parse(text);
  if (!response.ok) {
    throw new APIError(response.status, errorDetail(payload) ?? `Request failed (${response.status})`);
  }
  if (reading) remember(path, text);
  else if (method !== 'GET' && path.startsWith('/v1/panel/session')) clearCache();
  else if (method !== 'GET') invalidate(path);
  return payload as T;
}

export async function getAuthConfig(): Promise<AuthConfig> {
  return request<AuthConfig>('/v1/auth/config');
}

export async function panelSession(): Promise<boolean> {
  const result = await request<{ authenticated: boolean }>('/v1/panel/session');
  return result.authenticated;
}

export async function startPanelSession(token: string): Promise<void> {
  await request('/v1/panel/session', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token })
  });
}

export async function endPanelSession(): Promise<void> {
  await request('/v1/panel/session', { method: 'DELETE' });
}

/** How the owner signs in, from Settings → Sign-in. Nothing here is secret. */
export type SignInSettings = {
  managed_by: 'environment' | 'settings';
  public_url: string;
  can_change: boolean;
  blocked?: string;
  panel_token: { enabled: boolean; configured: boolean };
  clerk: { enabled: boolean; publishable_key?: string; issuer?: string; linked: boolean; owner_user_id?: string; development: boolean };
  changes: Array<{ changed_at: string; change: string }>;
};

const jsonBody = (value: unknown): RequestInit => ({ headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(value) });

export async function getSignInSettings(): Promise<SignInSettings> {
  return request<SignInSettings>('/v1/settings/sign-in');
}

export async function setSignInMethods(panelTokenEnabled: boolean, clerkEnabled: boolean): Promise<SignInSettings> {
  return request<SignInSettings>('/v1/settings/sign-in', { method: 'PUT', ...jsonBody({ panel_token_enabled: panelTokenEnabled, clerk_enabled: clerkEnabled }) });
}

export async function setClerkKey(publishableKey: string): Promise<SignInSettings> {
  return request<SignInSettings>('/v1/settings/sign-in/clerk', { method: 'PUT', ...jsonBody({ publishable_key: publishableKey }) });
}

export async function linkClerk(sessionToken: string): Promise<SignInSettings> {
  return request<SignInSettings>('/v1/settings/sign-in/clerk/link', { method: 'POST', ...jsonBody({ session_token: sessionToken }) });
}

export async function unlinkClerk(): Promise<SignInSettings> {
  return request<SignInSettings>('/v1/settings/sign-in/clerk/unlink', { method: 'POST', ...jsonBody({}) });
}

export async function listDevices(): Promise<Device[]> {
  const result = await request<{ devices: Device[] | null }>('/v1/devices');
  return result.devices ?? [];
}

export async function revokeDevice(deviceId: string): Promise<void> {
  // The server requires an explicit empty JSON object for these actions.
  await request(`/v1/devices/${encodeURIComponent(deviceId)}/revoke`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' });
}

export async function pairingDetails(pairingId: string): Promise<PairingDetails> {
  return request<PairingDetails>(`/v1/pairing/sessions/${encodeURIComponent(pairingId)}/details`);
}

export async function approvePairing(pairingId: string): Promise<void> {
  await request(`/v1/pairing/sessions/${encodeURIComponent(pairingId)}/approve`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' });
}

export async function listSources(): Promise<SourceSummary[]> {
  const result = await request<{ sources: SourceSummary[] }>('/v1/sources?limit=100');
  return result.sources;
}

export async function collectionHealth(signal?: AbortSignal): Promise<CollectionHealth> {
  return request<CollectionHealth>('/v1/collection/health?limit=100&samples=24', { signal });
}

export async function listObservations(input: {
  sourceInstanceKey?: string;
  from: Date;
  to: Date;
  signal?: AbortSignal;
}): Promise<Observation[]> {
  const query = new URLSearchParams({
    from: input.from.toISOString(),
    to: input.to.toISOString(),
    limit: '1000'
  });
  if (input.sourceInstanceKey) {
    query.set('source_instance_key', input.sourceInstanceKey);
  }
  const result = await request<{ observations: Observation[] | null }>(`/v1/observations?${query}`, { signal: input.signal });
  return result.observations ?? [];
}

// The current API has a cap, but no cursor. Split saturated windows without
// advancing past them; yield each complete slice while later slices load.
export async function* iterateObservationHistory(sourceInstanceKey: string, from: Date, to: Date, signal?: AbortSignal): AsyncGenerator<Observation[]> {
  let requests = 0;
  let count = 0;
  async function* read(lo: number, hi: number): AsyncGenerator<Observation[]> {
    if (++requests > 128) throw new Error('Observation history exceeds the query budget. No partial totals are shown.');
    const rows = await listObservations({sourceInstanceKey, from: new Date(lo), to: new Date(hi), signal});
    if (rows.length >= 1000) {
      if (hi - lo <= 1) throw new Error('Too many records share a timestamp. A paginated API is needed; no partial totals are shown.');
      const mid = Math.floor((lo + hi) / 2);
      yield* read(lo, mid);
      yield* read(mid, hi);
      return;
    }
    count += rows.length;
    if (count > 25_000) throw new Error('Observation history exceeds the browser record budget. No partial totals are shown.');
    if (rows.length) yield rows;
  }
  yield* read(from.getTime(), to.getTime());
}

export async function loadObservationHistory(sourceInstanceKey: string, from: Date, to: Date, signal?: AbortSignal): Promise<Observation[]> {
  const rows: Observation[] = [];
  for await (const slice of iterateObservationHistory(sourceInstanceKey, from, to, signal)) rows.push(...slice);
  return rows;
}

export const loadHealthHistory = loadObservationHistory;

export type Activity = {
  id: number;
  title: string;
  notes: string;
  next_action: string;
  resume_marker: string;
  status: 'active' | 'waiting' | 'blocked' | 'done' | 'completed' | 'abandoned' | 'unknown';
  activity_state: 'growth' | 'maintenance' | 'parked';
  priority: number;
  deadline?: string;
  effort_estimate_minutes?: number;
  details?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
};

export type ActivityInput = Partial<Omit<Activity, 'id' | 'created_at' | 'updated_at'>> & { title: string };

export type PlanBlock = {
  id: number;
  activity_id?: number;
  source_block_id?: number;
  title: string;
  start_time: string;
  duration_minutes: number;
  fixed: boolean;
  focus?: boolean;
  state: 'planned' | 'active' | 'done' | 'skipped' | 'switched' | 'blocked' | 'unknown';
  state_note: string;
  explanation_code?: string;
  explanation_note?: string;
  explanation_provenance?: Record<string, unknown>;
  last_changed_at?: string;
  position: number;
};

export type PlanBlockSpec = {
  activity_id?: number;
  title?: string;
  start_time: string;
  duration_minutes: number;
  fixed?: boolean;
  focus?: boolean;
};

export type Plan = {
  plan_date: string;
  version_id: number;
  created_at: string;
  note: string;
  blocks: PlanBlock[];
};

export type PlanInput = { note?: string; blocks: PlanBlockSpec[] };

export type PlanVersionSummary = {
  version_id: number;
  plan_date: string;
  created_at: string;
  note: string;
  block_count: number;
};

export type PlanBlockEvent = {
  id: number;
  plan_block_id: number;
  state: PlanBlock['state'];
  action: 'planned' | 'state' | 'explanation' | 'return';
  explanation_code?: string;
  note?: string;
  provenance: Record<string, unknown>;
  created_at: string;
};

export type PlanningSettings = {
  daily_plan_gate_enabled: boolean;
  enforcement_active: boolean;
  updated_at?: string;
};

export async function listActivities(): Promise<Activity[]> {
  const result = await request<{ activities: Activity[] }>('/v1/activities?limit=200');
  return result.activities ?? [];
}

export async function createActivity(input: ActivityInput): Promise<Activity> {
  return request<Activity>('/v1/activities', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) });
}

export async function updateActivity(id: number, input: ActivityInput): Promise<Activity> {
  return request<Activity>(`/v1/activities/${encodeURIComponent(id)}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) });
}

export async function listPlanVersions(planDate: string): Promise<PlanVersionSummary[]> {
  const result = await request<{ versions: PlanVersionSummary[] | null }>(`/v1/plans/${encodeURIComponent(planDate)}/versions`);
  return result.versions ?? [];
}

export async function getPlan(planDate: string): Promise<Plan | null> {
  const result = await request<{ plan: Plan | null }>(`/v1/plans/${encodeURIComponent(planDate)}`);
  return result.plan;
}

export async function savePlan(planDate: string, input: PlanInput): Promise<Plan> {
  return request<Plan>(`/v1/plans/${encodeURIComponent(planDate)}`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) });
}

export async function setPlanBlockState(planDate: string, blockId: number, state: PlanBlock['state'], stateNote = '', explanationCode = '', action: 'state' | 'explanation' | 'return' = 'state'): Promise<PlanBlock> {
  return request<PlanBlock>(`/v1/plans/${encodeURIComponent(planDate)}/blocks/${encodeURIComponent(blockId)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ state, state_note: stateNote, explanation_code: explanationCode, action })
  });
}

export async function listPlanBlockEvents(planDate: string, blockId: number): Promise<PlanBlockEvent[]> {
  const result = await request<{ events: PlanBlockEvent[] | null }>(`/v1/plans/${encodeURIComponent(planDate)}/blocks/${encodeURIComponent(blockId)}/events`);
  return result.events ?? [];
}

export async function getPlanningSettings(): Promise<PlanningSettings> {
  return request<PlanningSettings>('/v1/planning/settings');
}

export async function updatePlanningSettings(dailyPlanGateEnabled: boolean): Promise<PlanningSettings> {
  return request<PlanningSettings>('/v1/planning/settings', {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ daily_plan_gate_enabled: dailyPlanGateEnabled })
  });
}

export type AlignmentEvidence = {
  source_instance_key: string;
  source_kind: string;
  source_record_key: string;
  related_activity_id?: number;
  related_activity_title?: string;
  kind: string;
  occurred_at: string;
  ended_at?: string;
  collected_at: string;
  summary: string;
  relationship: 'matched' | 'unmatched' | 'ambiguous' | 'context_only' | 'interleave';
  origin: 'observed_device_activity' | 'observed_agent_activity';
  basis?: 'context_rule' | 'agent_context' | 'terms';
  context?: ActivityContext;
  context_matches?: ContextMatch[];
};

export type ActivityContext = {
  app?: string;
  title?: string;
  url?: string;
  domain?: string;
  paths?: string[];
  branch?: string;
  workspace_ids?: string[];
  agent_session?: string;
};

export type ContextSignal = 'path_prefix' | 'domain' | 'url_prefix' | 'workspace_id' | 'app';

export type ContextMatch = { rule_id: number; activity_id: number; signal: ContextSignal; pattern: string; value: string };

export type ActivityContextRule = {
  id: number;
  activity_id: number;
  signal: ContextSignal;
  pattern: string;
  note?: string;
  created_at: string;
  disabled_at?: string;
};

export async function listContextRules(): Promise<ActivityContextRule[]> {
  const result = await request<{ rules: ActivityContextRule[] | null }>('/v1/context-rules');
  return result.rules ?? [];
}

export async function createContextRule(input: Pick<ActivityContextRule, 'activity_id' | 'signal' | 'pattern' | 'note'>): Promise<ActivityContextRule> {
  return request<ActivityContextRule>('/v1/context-rules', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) });
}

export async function disableContextRule(id: number): Promise<ActivityContextRule> {
  return request<ActivityContextRule>(`/v1/context-rules/${encodeURIComponent(id)}/disable`, { method: 'POST' });
}

export type AlignmentView = {
  evaluated_at: string;
  plan_date: string;
  plan_version_id?: number;
  plan_block_id?: number;
  activity_id?: number;
  activity_title?: string;
  next_action?: string;
  resume_marker?: string;
  plan_block_state?: PlanBlock['state'];
  execution_session_id?: number;
  execution_state?: 'active' | 'waiting_external' | 'interrupted' | 'completed' | 'abandoned' | 'unknown';
  attended_execution_session_id?: number;
  attended_execution_state?: 'active' | 'waiting_external' | 'interrupted' | 'completed' | 'abandoned';
  alignment_state: 'aligned' | 'possible_drift' | 'unknown';
  reason_code: string;
  reason: string;
  source_freshness: 'fresh' | 'stale' | 'missing';
  coverage: 'available' | 'missing' | 'bounded';
  freshness_note: string;
  evidence: AlignmentEvidence[];
};

export type ExecutionRole = 'primary' | 'waiting_interleave';
export type ExecutionSessionState = 'active' | 'waiting_external' | 'interrupted' | 'completed' | 'abandoned';

export type ExecutionEvent = {
  id: number;
  session_id: number;
  event_type: string;
  occurred_at: string;
  note?: string;
  metadata?: Record<string, unknown>;
  provenance?: Record<string, unknown>;
  created_at: string;
};

export type ExecutionSession = {
  id: number;
  activity_id: number;
  plan_block_id?: number;
  role: ExecutionRole;
  primary_session_id?: number;
  started_at: string;
  ended_at?: string;
  state: ExecutionSessionState;
  next_action_snapshot: string;
  resume_marker?: string;
  created_at: string;
  updated_at: string;
  events?: ExecutionEvent[];
};

export type ExecutionSessionInput = {
  activity_id: number;
  plan_block_id?: number;
  role?: ExecutionRole;
  primary_session_id?: number;
  started_at?: string;
};

export type ExecutionEventInput = {
  event_type: string;
  occurred_at?: string;
  note?: string;
  resume_marker?: string;
  metadata?: Record<string, unknown>;
};

export type ReturnToPrimaryInput = {
  occurred_at?: string;
  primary_resume_marker?: string;
  interleave_resume_marker?: string;
};

export type ExecutionReturn = {
  primary_session: ExecutionSession;
  interleave_session: ExecutionSession;
};

export type ExecutionMetricSource = {
  metric: string;
  session_id: number;
  plan_block_id?: number;
  event_ids: number[];
  value: number;
  unit: 'count' | 'seconds';
};

export type ExecutionSummary = {
  from: string;
  to: string;
  coverage: 'complete' | 'bounded';
  sessions: number;
  completed: number;
  abandoned: number;
  terminal_session_count: number;
  abandonment_rate: number | null;
  initiation_latencies_seconds: number[];
  interruption_count: number;
  intentional_switch_count: number;
  waiting_external_count: number;
  return_opportunity_count: number;
  return_count: number;
  return_rate: number | null;
  return_latencies_seconds: number[];
  stuck_unknown_next_action_count: number;
  stuck_emotional_overwhelm_count: number;
  overwhelmed_count: number;
  recovery_count: number;
  recovery_durations_seconds: number[];
  recovery_same_task_return_count: number;
  recovery_same_task_return_rate: number | null;
  planned_seconds: number;
  session_elapsed_seconds: number;
  session_elapsed_count: number;
  session_elapsed_with_nonactive_events_count: number;
  duration_estimation_errors: Array<{
    session_id: number;
    plan_block_id?: number;
    planned_seconds: number;
    session_elapsed_seconds: number;
    error_seconds: number;
    has_nonactive_events: boolean;
  }>;
  possible_unplanned_switch_count: number;
  possible_unplanned_switches: AlignmentView[];
  metric_sources: ExecutionMetricSource[];
  metric_sources_truncated: boolean;
};

export async function getCurrentAlignment(at?: Date): Promise<AlignmentView> {
  const path = at ? `/v1/alignment/current?at=${encodeURIComponent(at.toISOString())}` : '/v1/alignment/current';
  const result = await request<{ alignment: AlignmentView }>(path);
  return result.alignment;
}

export async function getAlignmentHistory(from: Date, to: Date, limit = 50): Promise<AlignmentView[]> {
  const query = new URLSearchParams({ from: from.toISOString(), to: to.toISOString(), limit: String(limit) });
  const result = await request<{ alignments: AlignmentView[] | null }>(`/v1/alignment/history?${query}`);
  return result.alignments ?? [];
}

export async function getExecutionSummary(from: Date, to: Date): Promise<ExecutionSummary> {
  const query = new URLSearchParams({ from: from.toISOString(), to: to.toISOString() });
  const result = await request<{ summary: ExecutionSummary }>(`/v1/execution/summary?${query}`);
  return result.summary;
}

export async function startExecutionSession(input: ExecutionSessionInput): Promise<ExecutionSession> {
  return request<ExecutionSession>('/v1/execution/sessions', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input)
  });
}

export async function getCurrentPrimaryExecutionSession(): Promise<ExecutionSession | null> {
  const result = await request<{ session: ExecutionSession | null }>('/v1/execution/primary');
  return result.session;
}

export async function getCurrentAttendedExecutionSession(): Promise<ExecutionSession | null> {
  const result = await request<{ session: ExecutionSession | null }>('/v1/execution/attended');
  return result.session;
}

export async function listExecutionInterleaves(primarySessionId: number, limit = 20): Promise<ExecutionSession[]> {
  const query = new URLSearchParams({ limit: String(limit) });
  const result = await request<{ sessions: ExecutionSession[] | null }>(
    `/v1/execution/sessions/${encodeURIComponent(primarySessionId)}/interleaves?${query}`
  );
  return result.sessions ?? [];
}

export async function returnToPrimary(sessionId: number, input: ReturnToPrimaryInput = {}): Promise<ExecutionReturn> {
  return request<ExecutionReturn>(`/v1/execution/sessions/${encodeURIComponent(sessionId)}/return-to-primary`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input)
  });
}

export async function recordExecutionEvent(sessionId: number, input: ExecutionEventInput): Promise<ExecutionSession> {
  return request<ExecutionSession>(`/v1/execution/sessions/${encodeURIComponent(sessionId)}/events`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input)
  });
}

export type ProposedBlock = {
  source_block_id?: number;
  activity_id?: number;
  title: string;
  start_time: string;
  duration_minutes: number;
  fixed: boolean;
  state: PlanBlock['state'];
};

export type PlanChange = {
  kind: 'unchanged' | 'moved' | 'resized' | 'parked' | 'added' | 'fixed_conflict';
  base_block_id?: number;
  activity_id?: number;
  title: string;
  fixed: boolean;
  state?: PlanBlock['state'];
  old_start?: string;
  old_duration_minutes?: number;
  new_start?: string;
  new_duration_minutes?: number;
  detail?: string;
  related_block_ids?: number[];
};

export type ProposalWarning = { code: string; base_block_id?: number; session_id?: number; detail: string };

export type PlanChangeProposal = {
  id: number;
  plan_date: string;
  base_plan_version_id: number;
  origin: 'disruption_helper' | 'explicit';
  reason: string;
  proposed_plan: { note: string; blocks: ProposedBlock[] };
  changes: PlanChange[];
  warnings: ProposalWarning[];
  content_hash: string;
  status: 'pending' | 'accepted' | 'rejected' | 'stale';
  created_at: string;
  decided_at?: string;
  decision_note?: string;
  result_plan_version_id?: number;
};

export type DisruptionRequest = {
  base_plan_version_id: number;
  reason: string;
  disruption: { start: string; end?: string; available_until: string };
};

const proposalPath = (planDate: string, suffix = '') => `/v1/plans/${encodeURIComponent(planDate)}/replan-proposals${suffix}`;

export async function createReplanProposal(planDate: string, input: DisruptionRequest): Promise<PlanChangeProposal> {
  return request<PlanChangeProposal>(proposalPath(planDate), { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) });
}

export async function listReplanProposals(planDate: string, limit = 10): Promise<PlanChangeProposal[]> {
  const result = await request<{ proposals: PlanChangeProposal[] | null }>(proposalPath(planDate, `?limit=${limit}`));
  return result.proposals ?? [];
}

// Acceptance echoes the reviewed content hash; the server rejects any other content.
export async function acceptReplanProposal(planDate: string, proposal: Pick<PlanChangeProposal, 'id' | 'content_hash'>): Promise<PlanChangeProposal> {
  return request<PlanChangeProposal>(proposalPath(planDate, `/${encodeURIComponent(proposal.id)}/accept`), {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ content_hash: proposal.content_hash })
  });
}

export async function rejectReplanProposal(planDate: string, proposalId: number, note = ''): Promise<PlanChangeProposal> {
  return request<PlanChangeProposal>(proposalPath(planDate, `/${encodeURIComponent(proposalId)}/reject`), {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ note })
  });
}

export type PendingPairing = { pairing_id: string; device_id: string; device_name?: string; status: string; expires_at: string; returning?: boolean; continues?: string };

export async function listPendingPairings(): Promise<PendingPairing[]> {
  const result = await request<{ sessions: PendingPairing[] | null }>('/v1/pairing/sessions');
  return result.sessions ?? [];
}

export type TargetGroup = { id: string; label: string; android_packages?: string[]; mac_apps?: string[]; domains?: string[] };
export type PolicyConfig = {
  timezone?: string;
  distractions: TargetGroup[];
  essentials: TargetGroup[];
  plan_gate?: { enabled: boolean; ends_at?: string };
  emergency_minutes?: number;
  closed_by_default?: boolean;
  distraction_times?: Window[];
  /** The distraction budget. A looser one waits in budget_next until budget_next_from. */
  budget?: BudgetSettings;
  budget_next?: BudgetSettings;
  budget_next_from?: string;
};
export type PolicyVersion = { id: number; config: PolicyConfig; note?: string; created_at: string };
export type AppUsageSuggestion = { package: string; events: number; last_seen: string };

export async function getPolicy(): Promise<PolicyVersion | null> {
  const result = await request<{ policy: PolicyVersion | null }>('/v1/policy/config');
  return result.policy;
}

export async function savePolicy(config: PolicyConfig, note = ''): Promise<PolicyVersion> {
  const result = await request<{ policy: PolicyVersion }>('/v1/policy/config', {
    method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ config, note })
  });
  return result.policy;
}

export async function getAppSuggestions(): Promise<AppUsageSuggestion[]> {
  const result = await request<{ android_apps: AppUsageSuggestion[] | null }>('/v1/policy/suggestions');
  return result.android_apps ?? [];
}

export type ActivityUsage = { name: string; app?: string; label?: string; minutes: number; sessions: number };
export type DeviceActivity = {
  device: 'mac' | 'phone';
  active_minutes: number;
  afk_unknown_minutes?: number;
  no_data_minutes: number;
  last_record?: string;
  apps: ActivityUsage[] | null;
  sites?: ActivityUsage[] | null;
  labels: ActivityUsage[] | null;
};
export type ObservedActivity = { minutes: number; devices: DeviceActivity[]; distraction_minutes: number; coding?: { device: string; title: string; project?: string; messages_sent: number; agent_minutes: number }[] };
export type BlockReview = { id: number; title: string; task_id?: number; start: string; end: string; state: string; closes_distractions: boolean; status: 'upcoming' | 'in_progress' | 'past'; observed?: ObservedActivity };
export type Insight = { key: string; text: string; detail?: string };
export type ActivitySummary = {
  from: string; to: string; timezone: string; devices: DeviceActivity[]; no_data: { device: string; start: string; end: string }[] | null;
  plan_exists?: boolean; plan_review?: { blocks: BlockReview[]; outside_blocks: ObservedActivity }; insights?: Insight[];
};

export async function getActivitySummary(date?: string): Promise<ActivitySummary> {
  return request<ActivitySummary>(`/v1/activity/summary${date ? `?date=${encodeURIComponent(date)}` : ''}`);
}

export type LabelChange = { action: 'set' | 'remove'; kind: 'app' | 'site'; match: string; label?: string };
export type ActivityLabel = { id: number; kind: 'app' | 'site'; match: string; label: string; created_at: string };
export type LabelProposal = { id: number; changes: LabelChange[]; reason: string; content_hash: string; status: 'pending' | 'accepted' | 'rejected'; created_at: string; decided_at?: string };

export async function getActivityLabels(): Promise<{ labels: ActivityLabel[]; proposals: LabelProposal[] }> {
  const result = await request<{ labels: ActivityLabel[] | null; proposals: LabelProposal[] | null }>('/v1/activity-labels');
  return { labels: result.labels ?? [], proposals: result.proposals ?? [] };
}

// Both decisions echo the reviewed content hash; the server applies nothing else.
export async function decideLabelProposal(proposal: Pick<LabelProposal, 'id' | 'content_hash'>, accept: boolean): Promise<LabelProposal> {
  return request<LabelProposal>(`/v1/activity-label-proposals/${encodeURIComponent(proposal.id)}/${accept ? 'accept' : 'reject'}`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ content_hash: proposal.content_hash })
  });
}

export async function removeActivityLabel(id: number): Promise<void> {
  await request(`/v1/activity-labels/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

export type BlocklistChange = { action: 'block' | 'always_allow' | 'unlist'; android_package: string };
export type PolicyProposal = { id: number; changes: BlocklistChange[]; reason: string; content_hash: string; status: 'pending' | 'accepted' | 'rejected'; created_at: string; result_policy_version_id?: number };

export async function listPolicyProposals(): Promise<PolicyProposal[]> {
  const result = await request<{ proposals: PolicyProposal[] | null }>('/v1/policy/proposals');
  return result.proposals ?? [];
}

// Both decisions echo the reviewed content hash; the server applies nothing else.
export async function decidePolicyProposal(proposal: Pick<PolicyProposal, 'id' | 'content_hash'>, accept: boolean): Promise<PolicyProposal> {
  return request<PolicyProposal>(`/v1/policy/proposals/${encodeURIComponent(proposal.id)}/${accept ? 'accept' : 'reject'}`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ content_hash: proposal.content_hash })
  });
}

export type OpeningRule = { wait_minutes?: number; max_minutes?: number; per_day?: number; confirmations?: number; confirm_seconds?: number };
export type Opening = {
  id: number; purpose: string; sites: string[]; apps: string[]; everything: boolean; starts_at: string; until: string; ended_at?: string;
  confirmations_needed?: number; confirmations?: number; next_confirm_at?: string; length_minutes?: number;
};
export type OpeningsState = {
  rule: OpeningRule; next?: OpeningRule; next_from?: string; used_today: number;
  openings: Opening[]; distractions: TargetGroup[];
};

export async function getOpenings(): Promise<OpeningsState> {
  return request<OpeningsState>('/v1/openings');
}

export async function createOpening(input: { purpose: string; group_ids: string[]; everything: boolean; minutes: number }): Promise<Opening> {
  const result = await request<{ opening: Opening }>('/v1/openings', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input)
  });
  return result.opening;
}

export async function confirmOpening(id: number): Promise<Opening> {
  const result = await request<{ opening: Opening }>(`/v1/openings/${id}/confirm`, { method: 'POST' });
  return result.opening;
}

export async function endOpening(id: number): Promise<Opening> {
  const result = await request<{ opening: Opening }>(`/v1/openings/${id}/end`, { method: 'POST' });
  return result.opening;
}

export type MorningRule = { enabled: boolean; from_wake?: boolean; from?: string; until: string; lock_all?: boolean };
export type RulesSaved = { opening_rule: OpeningRule; morning: MorningRule; opening_rule_next_from?: string; morning_next_from?: string };

export async function saveOpeningRule(rule: OpeningRule): Promise<RulesSaved> {
  return request<RulesSaved>('/v1/openings/rule', {
    method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(rule)
  });
}

export type MorningState = {
  rule: MorningRule; next?: MorningRule; next_from?: string; code_made?: string;
  holding: boolean; scanned_at?: string; scan_address: string;
};

export async function getMorning(): Promise<MorningState> {
  return request<MorningState>('/v1/morning');
}

export async function saveMorningRule(rule: MorningRule): Promise<RulesSaved> {
  return request<RulesSaved>('/v1/morning', {
    method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(rule)
  });
}

export async function newMorningCode(): Promise<{ address: string; image: string }> {
  return request<{ address: string; image: string }>('/v1/morning/code', { method: 'POST' });
}

export type Window = { start: string; end: string };
export type SettingsLock = { enabled: boolean; windows?: Window[]; pin_windows?: Window[]; unlock_minutes?: number };
export type Bedtime = { enabled: boolean; start?: string; end?: string; wind_down_minutes?: number };
export type ExtensionRule = { enabled: boolean; quiet_seconds?: number };
export type LockState = { locked: boolean; until?: string; unlocked_until?: string; pin_set: boolean; pin_open: boolean; tries_left: number };
export type LockResponse = {
  lock: SettingsLock; lock_next?: SettingsLock; lock_next_from?: string;
  bedtime: Bedtime; bedtime_next?: Bedtime; bedtime_next_from?: string; state: LockState;
  extension: ExtensionRule; extension_next?: ExtensionRule; extension_next_from?: string;
};

export async function getLock(): Promise<LockResponse> {
  return request<LockResponse>('/v1/lock');
}

export async function saveLock(input: { lock?: SettingsLock; bedtime?: Bedtime; extension?: ExtensionRule }): Promise<LockResponse> {
  return request<LockResponse>('/v1/lock', {
    method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input)
  });
}

export async function setPin(current: string, pin: string): Promise<void> {
  await request('/v1/lock/pin', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ current, pin }) });
}

export async function unlockWithPin(pin: string): Promise<string> {
  const result = await request<{ unlocked_until: string }>('/v1/lock/unlock', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ pin })
  });
  return result.unlocked_until;
}

/** Which changed rules would only start on a later day, without saving. */
export type RulePreview = { waits_until: Record<string, string> };

export async function previewLock(input: { lock?: SettingsLock; bedtime?: Bedtime; extension?: ExtensionRule }): Promise<RulePreview> {
  return request<RulePreview>('/v1/lock?preview=1', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) });
}

export async function previewOpeningRule(rule: OpeningRule): Promise<RulePreview> {
  return request<RulePreview>('/v1/openings/rule?preview=1', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(rule) });
}

export async function previewMorningRule(rule: MorningRule): Promise<RulePreview> {
  return request<RulePreview>('/v1/morning?preview=1', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(rule) });
}

// Setup codes: made by the owner, used once by a new device to connect
// without a second approval step.
export type SetupCode = {
  invite_id: string;
  code: string;
  status: string;
  expires_at: string;
  server_url: string;
  relay_url?: string;
  join_url: string;
  setup_link: string;
  commands: Record<string, string>;
  qr: string;
};
export type SetupInvite = { invite_id: string; status: 'open' | 'used' | 'cancelled' | 'expired'; created_at: string; expires_at: string; used_at?: string; used_by_device?: string; used_by_name?: string };
export type PairingSettings = { server_url: string; relay_url: string; relay_key: string; relay_available: boolean };

const jsonPost = (body: unknown = {}): RequestInit => ({ method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });

export async function createSetupCode(): Promise<SetupCode> {
  return request<SetupCode>('/v1/pairing/invites', jsonPost());
}

export async function listSetupInvites(): Promise<SetupInvite[]> {
  const result = await request<{ invites: SetupInvite[] | null }>('/v1/pairing/invites');
  return result.invites ?? [];
}

export async function cancelSetupInvite(inviteId: string): Promise<void> {
  await request(`/v1/pairing/invites/${encodeURIComponent(inviteId)}/cancel`, jsonPost());
}

export async function getPairingSettings(): Promise<PairingSettings> {
  return request<PairingSettings>('/v1/pairing/settings');
}

export async function savePairingSettings(relayURL: string): Promise<PairingSettings> {
  return request<PairingSettings>('/v1/pairing/settings', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ relay_url: relayURL }) });
}

// When the owner last woke before `at`: the end of the last long quiet
// stretch across all devices. Null when the server finds none.
/** When the owner woke before at, and the wake time they set (bedtime's end) that day once it has passed. */
export async function getWakeTime(at: Date, signal?: AbortSignal): Promise<{ woke: string | null; planned: string | null }> {
  const result = await request<{ wake_at: string | null; planned_wake?: string | null }>(`/v1/activity/wake?at=${encodeURIComponent(at.toISOString())}`, { signal });
  return { woke: result.wake_at ?? null, planned: result.planned_wake ?? null };
}

// Distraction budget, the door and habits: docs/budget-and-habits.md in the
// notes. Minutes are budget minutes and may be fractional; times are RFC 3339.

export type BudgetSettings = {
  enabled: boolean;
  base_minutes: number;
  max_minutes: number;
  floor_minutes: number;
  habits_for_max: number;
  rate_distraction: number;
  rate_day: number;
  rate_bedtime: number;
  rate_grayscale_off: number;
  borrow_multiplier: number;
  window_minutes: number;
  window_use_minutes: number;
  grace_minutes: number;
  wait_minutes: number;
};

export type BudgetRate = { total: number; base: number; grayscale_off: number; kind: string };

export type DoorState = 'waiting' | 'open' | 'grace' | 'ended' | 'cancelled';

export type Door = {
  id: number;
  state: DoorState;
  requested_at: string;
  opens_at: string;
  window_end?: string | null;
  /** The sliding end. */
  ends_at?: string | null;
  used_minutes: number;
  /** Null inside a distraction time, where a door has no window limit. */
  use_minutes: number | null;
  borrowing: boolean;
};

export type BudgetHabit = { id: number; name: string; done: boolean; value: number };

export type BudgetState = {
  enabled: boolean;
  day: string;
  budget: number;
  spent: number;
  left: number;
  earned: number;
  penalty: number;
  debt: number;
  debt_tomorrow: number;
  borrowable: number;
  rate: BudgetRate;
  in_distraction_time: boolean;
  next_distraction_time?: string | null;
  bedtime_at?: string | null;
  wind_down: boolean;
  grayscale_declined: boolean;
  door: Door | null;
  door_available: boolean;
  door_unavailable_reason: string;
  next_door_at?: string | null;
  habits: BudgetHabit[] | null;
  reasons: string[] | null;
  settings: Pick<BudgetSettings, 'wait_minutes' | 'window_minutes' | 'window_use_minutes' | 'grace_minutes' | 'borrow_multiplier'>;
};

/** One closed budget day, as the server wrote it at day close. */
export type BudgetDay = {
  day: string;
  budget: number;
  spent: number;
  /** Budget minutes borrowed from the next day, already multiplied. */
  debt_passed_on: number;
  habits_done: number;
  habits_missed: number;
  settings?: Partial<BudgetSettings>;
};

const finite = (value: unknown): number => (typeof value === 'number' && Number.isFinite(value) ? value : 0);

/** Reads one day snapshot, accepting the names the contract leaves open. */
export function readBudgetDay(raw: Record<string, unknown>): BudgetDay {
  return {
    day: String(raw.day ?? raw.budget_day ?? ''),
    budget: finite(raw.budget),
    spent: finite(raw.spent),
    debt_passed_on: finite(raw.debt_passed_on ?? raw.debt_tomorrow ?? raw.debt_passed ?? raw.debt),
    habits_done: finite(raw.habits_done ?? raw.done),
    habits_missed: finite(raw.habits_missed ?? raw.missed),
    settings: raw.settings && typeof raw.settings === 'object' ? (raw.settings as Partial<BudgetSettings>) : undefined
  };
}

/** Today's state and the day snapshots. */
export async function getBudget(days = 30): Promise<{ today: BudgetState | null; days: BudgetDay[] }> {
  const result = await request<Record<string, unknown>>(`/v1/budget?days=${encodeURIComponent(days)}`);
  const today = (result.budget ?? result.today ?? null) as BudgetState | null;
  const rows = (result.days ?? result.snapshots ?? null) as Record<string, unknown>[] | null;
  return { today, days: (rows ?? []).map(readBudgetDay).filter(row => row.day) };
}

const unwrap = <T>(result: unknown, key: string): T =>
  (result && typeof result === 'object' && key in result ? (result as Record<string, unknown>)[key] : result) as T;

/** Makes a door; 409 with the reason when none is available. */
export async function createDoor(): Promise<Door> {
  return unwrap<Door>(await request('/v1/door', jsonPost()), 'door');
}

/** Cancels a door while it is waiting, at no cost. */
export async function cancelDoor(id: number): Promise<Door> {
  return unwrap<Door>(await request(`/v1/door/${encodeURIComponent(id)}/cancel`, jsonPost()), 'door');
}

/** Records that the door screen was left with Back, which counts as returning. */
export async function doorBack(): Promise<void> {
  await request('/v1/door/back', jsonPost());
}

/** Weekdays as Go numbers them, 0 for Sunday; empty or missing means every day. */
export type Habit = {
  id: number;
  name: string;
  position: number;
  weekdays?: number[] | null;
  archived?: boolean;
  archived_at?: string | null;
  created_at?: string;
};

export type HabitChange = Partial<Pick<Habit, 'name' | 'position' | 'weekdays' | 'archived'>>;

export async function listHabits(): Promise<Habit[]> {
  const result = await request<{ habits: Habit[] | null }>('/v1/habits');
  return result.habits ?? [];
}

export async function createHabit(input: { name: string; weekdays?: number[] }): Promise<Habit> {
  return unwrap<Habit>(await request('/v1/habits', jsonPost(input)), 'habit');
}

export async function updateHabit(id: number, change: HabitChange): Promise<Habit> {
  return unwrap<Habit>(await request(`/v1/habits/${encodeURIComponent(id)}`, { method: 'PATCH', ...jsonBody(change) }), 'habit');
}

/** Ticks or unticks a habit for today's budget day; 409 once bedtime has started. */
export async function setHabitDone(id: number, done: boolean, day?: string): Promise<void> {
  await request(`/v1/habits/${encodeURIComponent(id)}/${done ? 'tick' : 'untick'}`, jsonPost(day ? { day } : {}));
}

export type HabitDayStatus = 'done' | 'missed' | 'unscheduled' | 'none';

export type HeatmapHabit = {
  id: number;
  name: string;
  created_day: string;
  archived_day?: string | null;
  days: Record<string, HabitDayStatus> | null;
  current_run: number;
  longest_run: number;
};

/** combined holds the share of scheduled habits done each day, 0 to 1. */
export type Heatmap = { habits: HeatmapHabit[]; combined: Record<string, number | null> };

export async function getHabitHeatmap(days = 365): Promise<Heatmap> {
  const result = await request<{ habits?: HeatmapHabit[] | null; combined?: Record<string, number | null> | null }>(`/v1/habits/heatmap?days=${encodeURIComponent(days)}`);
  return { habits: result.habits ?? [], combined: result.combined ?? {} };
}

export async function getReasons(): Promise<string[]> {
  const result = await request<{ reasons: string[] | null }>('/v1/reasons');
  return result.reasons ?? [];
}

export async function saveReasons(reasons: string[]): Promise<string[]> {
  const result = await request<{ reasons?: string[] | null }>('/v1/reasons', { method: 'PUT', ...jsonBody({ reasons }) });
  return result.reasons ?? reasons;
}

// What a device is told now (GET /v1/device/now, which the owner's panel
// session may read too): the parts Today shows. The rest of the answer is for
// devices following the policy and is left out here.

/** Something to come back to. A place follow-up comes here only once its place was reached. */
export type FollowUp = {
  id: number;
  title: string;
  /** When it is due; for a place follow-up, when the place was reached if that was later. */
  due_at: string;
  state: string;
  triggered_at?: string | null;
  place?: { label?: string } | null;
};

export type TurnRef = { task_id: number; task: string; since: string };

export type FocusNow = { task_id: number; task: string; next_action?: string; since: string; on: 'on' | 'off' | 'unknown' | 'away' | string; context?: string; off_since?: string | null };

/** What the server reads at the Mac right now. */
export type Tracking = {
  state: 'away' | 'distraction' | 'task' | 'other' | string;
  task_id?: number;
  task?: string;
  since?: string | null;
  context?: string;
  excused?: boolean;
  agent_running: boolean;
  your_turn: TurnRef[] | null;
  focus?: FocusNow | null;
};

export type CollectionIncident = {
  id: number;
  device: string;
  kind: string;
  started_at: string;
  detected_at: string;
  ended_at?: string | null;
  detail?: Record<string, unknown> | null;
};

export type DeviceNow = {
  generated_at: string;
  tracking: Tracking | null;
  follow_ups: FollowUp[];
  collection_incidents: CollectionIncident[];
};

export async function getDeviceNow(): Promise<DeviceNow> {
  const result = await request<Partial<DeviceNow> & Record<string, unknown>>('/v1/device/now');
  return {
    generated_at: String(result.generated_at ?? ''),
    tracking: (result.tracking ?? null) as Tracking | null,
    follow_ups: (result.follow_ups ?? []) as FollowUp[],
    collection_incidents: (result.collection_incidents ?? []) as CollectionIncident[]
  };
}

/** Marks one follow-up done, as the phone's Done button did. */
export async function followUpDone(id: number): Promise<void> {
  await request(`/v1/device/follow-ups/${encodeURIComponent(id)}/done`, jsonPost());
}
