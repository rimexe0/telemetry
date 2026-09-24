import { afterEach, expect, test, vi } from 'vitest';
import { APIError, getCurrentAlignment, getCurrentAttendedExecutionSession, getCurrentPrimaryExecutionSession, getExecutionSummary, iterateObservationHistory, listExecutionInterleaves, listObservations, returnToPrimary, setClerkTokenProvider, startExecutionSession, startPanelSession } from './api';
import type { Observation } from './api';

afterEach(() => {
  setClerkTokenProvider(undefined);
  vi.unstubAllGlobals();
});

test('observation queries remain bounded, time-scoped and same-origin', async () => {
  const fetch = vi.fn().mockResolvedValue(
    new Response(JSON.stringify({ observations: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } })
  );
  vi.stubGlobal('fetch', fetch);

  await listObservations({
    sourceInstanceKey: 'activitywatch:mac:codex',
    from: new Date('2026-09-07T00:00:00Z'),
    to: new Date('2026-09-08T00:00:00Z')
  });

  expect(fetch).toHaveBeenCalledWith(
    '/v1/observations?from=2026-09-07T00%3A00%3A00.000Z&to=2026-09-08T00%3A00%3A00.000Z&limit=1000&source_instance_key=activitywatch%3Amac%3Acodex',
    expect.objectContaining({ credentials: 'same-origin' })
  );
});

test('panel login sends the owner token only to the same-origin session endpoint', async () => {
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ authenticated: true }), { status: 200 }));
  vi.stubGlobal('fetch', fetch);

  await startPanelSession('panel-token');

  expect(fetch).toHaveBeenCalledWith(
    '/v1/panel/session',
    expect.objectContaining({ method: 'POST', body: '{"token":"panel-token"}', credentials: 'same-origin' })
  );
});

test('Clerk session tokens are attached in memory to owner API requests', async () => {
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ authenticated: true }), { status: 200 }));
  vi.stubGlobal('fetch', fetch);
  setClerkTokenProvider(async () => 'clerk-session-token');

  await startPanelSession('legacy-token');

  const request = fetch.mock.calls[0][1] as RequestInit;
  expect(new Headers(request.headers).get('Authorization')).toBe('Bearer clerk-session-token');
  expect(request.body).toBe('{"token":"legacy-token"}');
});

test('API errors preserve the server message without returning a token', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: { detail: 'valid panel credentials are required' } }), { status: 401 }))
  );

  await expect(startPanelSession('wrong')).rejects.toEqual(new APIError(401, 'valid panel credentials are required'));
});

test('saturated health windows are split into adjacent half-open ranges', async () => {
  const {loadHealthHistory} = await import('./api');
  const fetch = vi.fn().mockImplementation((path:string) => {
    const url = new URL(path,'http://localhost');
    const from=Date.parse(url.searchParams.get('from')!); const to=Date.parse(url.searchParams.get('to')!);
    const observations = to-from > 2 ? Array(1000).fill({}) : [{source_record_key:String(from)}];
    return Promise.resolve(new Response(JSON.stringify({observations}),{status:200}));
  });
  vi.stubGlobal('fetch',fetch);
  expect(await loadHealthHistory('health',new Date(0),new Date(4))).toEqual([{source_record_key:'0'},{source_record_key:'2'}]);
  expect(fetch).toHaveBeenCalledTimes(3);
});

test('observation history yields each complete slice before fetching the next slice', async () => {
  const response = (observations: unknown[]) => new Response(JSON.stringify({ observations }), { status: 200 });
  const fetch = vi.fn()
    .mockResolvedValueOnce(response(Array(1000).fill({ source_record_key: 'capped' })))
    .mockResolvedValueOnce(response([{ source_record_key: 'left' }]))
    .mockResolvedValueOnce(response([{ source_record_key: 'right' }]));
  vi.stubGlobal('fetch', fetch);

  const history = iterateObservationHistory('aw-window', new Date(0), new Date(4));
  const first = await history.next();
  expect(first.value?.map((row: Observation) => row.source_record_key)).toEqual(['left']);
  expect(fetch).toHaveBeenCalledTimes(2);

  const second = await history.next();
  expect(second.value?.map((row: Observation) => row.source_record_key)).toEqual(['right']);
  expect(fetch).toHaveBeenCalledTimes(3);
  expect((await history.next()).done).toBe(true);
});

test('an unsplittable saturated health window fails instead of returning partial totals',async()=>{
  const {loadHealthHistory}=await import('./api');
  vi.stubGlobal('fetch',vi.fn().mockResolvedValue(new Response(JSON.stringify({observations:Array(1000).fill({})}),{status:200})));
  await expect(loadHealthHistory('health',new Date(0),new Date(1))).rejects.toThrow('paginated API');
});

test('collection health stays bounded, same-origin and cancellable', async () => {
  const { collectionHealth } = await import('./api');
  const result = { checked_at: '2026-09-12T10:00:00Z', sources: [], sources_truncated: false, devices: [] };
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify(result), { status: 200 }));
  vi.stubGlobal('fetch', fetch);
  const controller = new AbortController();
  expect(await collectionHealth(controller.signal)).toEqual(result);
  expect(fetch).toHaveBeenCalledWith('/v1/collection/health?limit=100&samples=24',
    expect.objectContaining({ credentials: 'same-origin', signal: controller.signal }));
});

test('current alignment stays owner-scoped and carries an explicit evaluation time', async () => {
  const alignment = { alignment_state: 'unknown', reason_code: 'missing_evidence', evidence: [] };
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ alignment }), { status: 200 }));
  vi.stubGlobal('fetch', fetch);

  await getCurrentAlignment(new Date('2026-09-23T10:15:00Z'));

  expect(fetch).toHaveBeenCalledWith(
    '/v1/alignment/current?at=2026-09-23T10%3A15%3A00.000Z',
    expect.objectContaining({ credentials: 'same-origin' })
  );
});

test('execution summary stays owner-scoped and requests only the selected bounded window', async () => {
  const summary = { possible_unplanned_switch_count: 1, possible_unplanned_switches: [], metric_sources: [], metric_sources_truncated: false };
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ summary }), { status: 200 }));
  vi.stubGlobal('fetch', fetch);

  expect(await getExecutionSummary(new Date('2026-09-23T00:00:00Z'), new Date('2026-09-23T21:00:00Z'))).toEqual(summary);
  expect(fetch).toHaveBeenCalledWith(
    '/v1/execution/summary?from=2026-09-23T00%3A00%3A00.000Z&to=2026-09-23T21%3A00%3A00.000Z',
    expect.objectContaining({ credentials: 'same-origin' })
  );
});

test('primary and attended execution reads remain distinct and interleaves bounded', async () => {
  const primary = { id: 7, role: 'primary', state: 'waiting_external' };
  const attended = { id: 8, role: 'waiting_interleave', primary_session_id: 7, state: 'active' };
  const fetch = vi.fn()
    .mockResolvedValueOnce(new Response(JSON.stringify({ session: primary }), { status: 200 }))
    .mockResolvedValueOnce(new Response(JSON.stringify({ session: attended }), { status: 200 }))
    .mockResolvedValueOnce(new Response(JSON.stringify({ sessions: [attended] }), { status: 200 }));
  vi.stubGlobal('fetch', fetch);

  expect(await getCurrentPrimaryExecutionSession()).toEqual(primary);
  expect(await getCurrentAttendedExecutionSession()).toEqual(attended);
  expect(await listExecutionInterleaves(7, 9)).toEqual([attended]);
  expect(fetch.mock.calls.map(([url]) => url)).toEqual([
    '/v1/execution/primary',
    '/v1/execution/attended',
    '/v1/execution/sessions/7/interleaves?limit=9'
  ]);
  for (const [, options] of fetch.mock.calls) expect(options.credentials).toBe('same-origin');
});

test('execution start and return send explicit interleave linkage and both resume markers', async () => {
  const started = { id: 8, role: 'waiting_interleave', primary_session_id: 7, state: 'active' };
  const returned = { primary_session: { id: 7 }, interleave_session: { id: 8 } };
  const fetch = vi.fn()
    .mockResolvedValueOnce(new Response(JSON.stringify(started), { status: 201 }))
    .mockResolvedValueOnce(new Response(JSON.stringify(returned), { status: 200 }));
  vi.stubGlobal('fetch', fetch);

  await startExecutionSession({ activity_id: 4, role: 'waiting_interleave', primary_session_id: 7 });
  await returnToPrimary(8, { primary_resume_marker: 'Continue the outline', interleave_resume_marker: 'Resume at page four' });

  expect(fetch.mock.calls[0][0]).toBe('/v1/execution/sessions');
  expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({ activity_id: 4, role: 'waiting_interleave', primary_session_id: 7 });
  expect(fetch.mock.calls[1][0]).toBe('/v1/execution/sessions/8/return-to-primary');
  expect(JSON.parse(fetch.mock.calls[1][1].body)).toEqual({ primary_resume_marker: 'Continue the outline', interleave_resume_marker: 'Resume at page four' });
});
