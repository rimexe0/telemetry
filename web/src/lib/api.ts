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
// advancing past them; never silently describe a capped result as complete.
export async function loadObservationHistory(sourceInstanceKey: string, from: Date, to: Date, signal?: AbortSignal): Promise<Observation[]> {
  let requests = 0;
  let count = 0;
  async function read(lo: number, hi: number): Promise<Observation[]> {
    if (++requests > 128) throw new Error('Observation history exceeds the query budget. No partial totals are shown.');
    const rows = await listObservations({sourceInstanceKey, from: new Date(lo), to: new Date(hi), signal});
    if (rows.length >= 1000) {
      if (hi - lo <= 1) throw new Error('Too many records share a timestamp. A paginated API is needed; no partial totals are shown.');
      const mid = Math.floor((lo + hi) / 2);
      return [...await read(lo, mid), ...await read(mid, hi)];
    }
    count += rows.length;
    if (count > 25_000) throw new Error('Observation history exceeds the browser record budget. No partial totals are shown.');
    return rows;
  }
  return read(from.getTime(), to.getTime());
}

export const loadHealthHistory = loadObservationHistory;

export type Activity = {
  id: number;
  title: string;
  notes: string;
  next_action: string;
  status: 'active' | 'waiting' | 'blocked' | 'done' | 'abandoned';
  priority: number;
  deadline?: string;
  effort_estimate_minutes?: number;
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
  state: 'planned' | 'active' | 'done' | 'skipped' | 'switched';
  state_note: string;
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

export async function getPlan(planDate: string): Promise<Plan | null> {
  const result = await request<{ plan: Plan | null }>(`/v1/plans/${encodeURIComponent(planDate)}`);
  return result.plan;
}

export async function savePlan(planDate: string, input: PlanInput): Promise<Plan> {
  return request<Plan>(`/v1/plans/${encodeURIComponent(planDate)}`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) });
}

export async function setPlanBlockState(planDate: string, blockId: number, state: PlanBlock['state'], stateNote = ''): Promise<PlanBlock> {
  return request<PlanBlock>(`/v1/plans/${encodeURIComponent(planDate)}/blocks/${encodeURIComponent(blockId)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ state, state_note: stateNote })
  });
}
