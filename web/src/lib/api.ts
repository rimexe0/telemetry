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

export class APIError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    credentials: 'same-origin',
    headers: { Accept: 'application/json', ...(init?.headers ?? {}) },
    ...init
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

export async function listSources(): Promise<SourceSummary[]> {
  const result = await request<{ sources: SourceSummary[] }>('/v1/sources?limit=100');
  return result.sources;
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
