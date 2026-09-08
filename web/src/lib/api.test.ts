import { afterEach, expect, test, vi } from 'vitest';
import { APIError, listObservations, startPanelSession } from './api';

afterEach(() => vi.unstubAllGlobals());

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

test('an unsplittable saturated health window fails instead of returning partial totals',async()=>{
  const {loadHealthHistory}=await import('./api');
  vi.stubGlobal('fetch',vi.fn().mockResolvedValue(new Response(JSON.stringify({observations:Array(1000).fill({})}),{status:200})));
  await expect(loadHealthHistory('health',new Date(0),new Date(1))).rejects.toThrow('paginated API');
});
