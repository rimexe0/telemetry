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

async function request<T>(path: string, init?: RequestInit): Promise<T> {
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
  const payload = (await response.json().catch(() => undefined)) as
    | { error?: { detail?: string } }
    | T
    | undefined;
  if (!response.ok) {
    const detail = payload && typeof payload === 'object' && 'error' in payload ? payload.error?.detail : undefined;
    throw new APIError(response.status, detail ?? `Request failed (${response.status})`);
  }
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

export async function listDevices(): Promise<Device[]> {
  const result = await request<{ devices: Device[] | null }>('/v1/devices');
  return result.devices ?? [];
}

export async function revokeDevice(deviceId: string): Promise<void> {
  await request(`/v1/devices/${encodeURIComponent(deviceId)}/revoke`, { method: 'POST' });
}

export async function pairingDetails(pairingId: string): Promise<PairingDetails> {
  return request<PairingDetails>(`/v1/pairing/sessions/${encodeURIComponent(pairingId)}/details`);
}

export async function approvePairing(pairingId: string): Promise<void> {
  await request(`/v1/pairing/sessions/${encodeURIComponent(pairingId)}/approve`, { method: 'POST' });
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
  title: string;
  start_time: string;
  duration_minutes: number;
  fixed: boolean;
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
  relationship: 'matched' | 'unmatched' | 'ambiguous' | 'context_only';
  origin: 'observed_device_activity' | 'observed_agent_activity';
};

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
