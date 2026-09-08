import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig, type Plugin } from 'vite';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

// Explicit local-only browser verification against a retained owner snapshot.
// No snapshot paths or records are imported into the production bundle.
function snapshotAPI(): Plugin {
  return {
    name: 'telemetry-local-snapshot',
    configureServer(server) {
      const directory = process.env.TELEMETRY_PANEL_SNAPSHOT_DIR;
      if (!directory) return;
      const sources = JSON.parse(readFileSync(join(directory, 'telemetry-sources.json'), 'utf8')).sources;
      const observations = JSON.parse(readFileSync(join(directory, 'telemetry-health-observations.json'), 'utf8')).observations;
      let authenticated = true;
      server.middlewares.use('/v1', (request, response) => {
        response.setHeader('Cache-Control', 'no-store');
        response.setHeader('Content-Type', 'application/json');
        const send = (status: number, value: unknown) => { response.statusCode = status; response.end(JSON.stringify(value)); };
        if (!['127.0.0.1', '::1', '::ffff:127.0.0.1'].includes(request.socket.remoteAddress ?? '')) {
          send(403, {error: {detail: 'Snapshot preview is loopback-only.'}}); return;
        }
        const url = new URL(request.url ?? '/', 'http://localhost');
        if (url.pathname === '/panel/session') {
          if (request.method === 'DELETE') authenticated = false;
          else if (request.method !== 'GET') { send(405, {error: {detail: 'Restart the snapshot preview to reopen it.'}}); return; }
          send(200, {authenticated}); return;
        }
        if (!authenticated) { send(401, {error: {detail: 'Snapshot session ended.'}}); return; }
        if (request.method !== 'GET') { send(405, {}); return; }
        if (url.pathname === '/sources') { send(200, {sources: sources.filter((s: {source_kind: string}) => s.source_kind === 'health_connect')}); return; }
        if (url.pathname === '/observations') {
          const from = Date.parse(url.searchParams.get('from') ?? ''), to = Date.parse(url.searchParams.get('to') ?? '');
          const source = url.searchParams.get('source_instance_key');
          send(200, {observations: observations.filter((o: {source_instance_key: string; occurred_at: string}) => (!source || o.source_instance_key === source) && Date.parse(o.occurred_at) >= from && Date.parse(o.occurred_at) < to).slice(0, 1000)}); return;
        }
        send(404, {});
      });
    }
  };
}

function fixtureAPI(): Plugin {
  return {
    name: 'telemetry-fixture-api',
    configureServer(server) {
      server.middlewares.use('/v1', (request, response, next) => {
        if (process.env.TELEMETRY_PANEL_FIXTURE !== '1') {
          next();
          return;
        }
        const send = (status: number, value?: unknown) => {
          response.statusCode = status;
          response.setHeader('Content-Type', 'application/json');
          response.end(value === undefined ? '' : JSON.stringify(value));
        };
        const now = Date.now();
        const timestamp = (minutesAgo: number) => new Date(now - minutesAgo * 60_000).toISOString();
        if (request.url === '/panel/session' && request.method === 'GET') {
          send(200, { authenticated: true });
          return;
        }
        if (request.url === '/panel/session' && request.method === 'POST') {
          send(200, { authenticated: true, expires_at: timestamp(-720) });
          return;
        }
        if (request.url === '/panel/session' && request.method === 'DELETE') {
          send(200, { authenticated: false });
          return;
        }
        if (request.url?.startsWith('/sources')) {
          send(200, {
            sources: [
              {
                source_instance_key: 'fixture:mac:activitywatch',
                source_kind: 'fixture.activitywatch',
                device_id: 'fixture-mac',
                first_collected_at: timestamp(60 * 24 * 5),
                last_collected_at: timestamp(4),
                observation_count: 124
              },
              {
                source_instance_key: 'fixture:phone:health',
                source_kind: 'fixture.health',
                device_id: 'fixture-phone',
                first_collected_at: timestamp(60 * 24 * 2),
                last_collected_at: timestamp(47),
                observation_count: 38
              }
            ]
          });
          return;
        }
        if (request.url?.startsWith('/observations')) {
          const observations = Array.from({ length: 34 }, (_, index) => {
            const isHealth = index % 6 === 0;
            return {
              source_instance_key: isHealth ? 'fixture:phone:health' : 'fixture:mac:activitywatch',
              source_kind: isHealth ? 'fixture.health' : 'fixture.activitywatch',
              device_id: isHealth ? 'fixture-phone' : 'fixture-mac',
              source_record_key: `fixture-${index + 1}`,
              kind: isHealth ? 'fixture.health.reading' : 'fixture.activity.event',
              occurred_at: timestamp(60 * (index + 1) * 2),
              collected_at: timestamp(60 * (index + 1) * 2 - 2),
              revision_digest: `fixture-revision-${index + 1}`,
              original_payload: { fixture: true, sequence: index + 1 },
              projection: { fixture: true, category: isHealth ? 'health' : 'activity' }
            };
          });
          send(200, { observations });
          return;
        }
        next();
      });
    }
  };
}

export default defineConfig({
  plugins: [snapshotAPI(), fixtureAPI(), sveltekit()]
});
