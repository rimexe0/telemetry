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

// Invented in-memory plan and execution state so the NOW view can be clicked
// through without a database. It follows the server's transition rules loosely
// and is not evidence of server behaviour.
function fixtureExecution() {
  const day = new Date(Date.now() + 3 * 3_600_000).toISOString().slice(0, 10);
  const hour = new Date(Date.now() + 3 * 3_600_000).getUTCHours();
  const created = new Date().toISOString();
  const activities = [
    { id: 1, title: 'Fixture task', notes: '', next_action: 'Open the fixture file and write the first line', resume_marker: '', status: 'active', activity_state: 'growth', priority: 0, created_at: created, updated_at: created }
  ];
  const blocks = [
    { id: 1, activity_id: 1, title: 'Fixture task', start_time: `${day}T${String(hour).padStart(2, '0')}:00:00+03:00`, duration_minutes: 60, fixed: false, state: 'planned', state_note: '', position: 0 }
  ];
  let session: Record<string, any> | null = null;
  const proposals: Record<string, any>[] = [];
  const rules: Record<string, any>[] = [];
  let eventId = 0;
  let policyVersion = 2;
  const policyConfig: Record<string, any> = { timezone: 'Europe/Istanbul', plan_gate: { enabled: false }, essentials: [], distractions: [], emergency_minutes: 10 };
  const policyProposals: Record<string, any>[] = [{ id: 1, status: 'pending', content_hash: 'fixture-policy-1', created_at: created, reason: 'Fixture: owner named x, reddit and youtube',
    changes: [{ action: 'block', android_package: 'com.twitter.android' }, { action: 'block', android_package: 'com.reddit.frontpage' }, { action: 'block', android_package: 'com.google.android.youtube' }] }];
  // Invented labels and usage for the Activity view.
  const labels: Record<string, any>[] = [{ id: 1, kind: 'app', match: 'com.twitter.android', label: 'distraction', created_at: created }];
  const labelProposals: Record<string, any>[] = [{ id: 1, status: 'pending', content_hash: 'fixture-label-1', created_at: created, reason: 'Fixture: owner said these are doomscrolling',
    changes: [{ action: 'set', kind: 'site', match: 'youtube.com', label: 'distraction' }, { action: 'set', kind: 'app', match: 'T3 Code', label: 'work' }] }];
  const terminal = new Set(['completed', 'abandoned']);
  const nextState: Record<string, string> = {
    waiting_external: 'waiting_external', switched_out: 'interrupted', paused: 'interrupted', interrupted: 'interrupted',
    offline_recovery_started: 'interrupted', offline_recovery_ended: 'interrupted', resumed: 'active', completed: 'completed', abandoned: 'abandoned'
  };
  return {
    handle(method: string, path: string, body: any): [number, unknown] | undefined {
      if (path === '/context-rules' && method === 'GET') return [200, { rules: rules.filter(rule => !rule.disabled_at) }];
      if (path === '/context-rules' && method === 'POST') {
        if (!['path_prefix', 'domain', 'url_prefix', 'workspace_id'].includes(body.signal)) return [400, { error: { detail: 'invalid signal' } }];
        rules.push({ id: rules.length + 1, activity_id: body.activity_id, signal: body.signal, pattern: body.pattern, created_at: new Date().toISOString() });
        return [201, rules[rules.length - 1]];
      }
      const disable = path.match(/^\/context-rules\/(\d+)\/disable$/);
      if (disable && method === 'POST') {
        const rule = rules.find(row => row.id === Number(disable[1]));
        if (!rule) return [404, { error: { detail: 'not found' } }];
        rule.disabled_at ??= new Date().toISOString();
        return [200, rule];
      }
      if (path === '/activity/summary') {
        const tagged = (name: string) => labels.find(label => label.match === name)?.label ?? 'unlabeled';
        const apps = (rows: [string, number][]) => rows.map(([name, minutes]) => ({ name, minutes, sessions: 3, label: tagged(name) }));
        return [200, { from: created, to: created, timezone: 'Europe/Istanbul', no_data: [], devices: [
          { device: 'mac', active_minutes: 312, afk_unknown_minutes: 20, no_data_minutes: 480, apps: apps([['Helium', 128], ['osu!', 77], ['T3 Code', 64], ['OBS Studio', 8]]),
            sites: [{ name: 'youtube.com', app: 'Helium', minutes: 46, sessions: 9, label: tagged('youtube.com') }, { name: 'chatgpt.com', app: 'Helium', minutes: 22, sessions: 4, label: 'unlabeled' }],
            labels: labels.length > 1 ? [{ name: 'distraction', minutes: 123, sessions: 9 }, { name: 'work', minutes: 64, sessions: 3 }, { name: 'unlabeled', minutes: 125, sessions: 20 }] : [{ name: 'unlabeled', minutes: 312, sessions: 30 }] },
          { device: 'phone', active_minutes: 276, no_data_minutes: 28, apps: apps([['com.twitter.android', 60], ['com.t3tools.t3code', 37], ['com.openai.chatgpt', 35]]),
            labels: [{ name: 'distraction', minutes: 60, sessions: 7 }, { name: 'unlabeled', minutes: 216, sessions: 90 }] }
        ],
        plan_exists: true,
        insights: [
          { key: 'distractions', text: '3h 3m on distractions today', detail: 'Mostly osu!: 1h 17m in 3 visits. That is 7 episodes of a 24-minute show.' },
          { key: 'clean_run', text: 'Longest run without a distraction: 1h 4m', detail: 'Counted in active time on the Mac and phone.' },
          { key: 'agents', text: 'Agents worked 2h 10m for you', detail: 'You sent 14 messages across 3 threads.' }
        ],
        plan_review: {
          blocks: [
            { id: 1, title: 'Telemetry', task_id: 1, start: '2026-10-01T06:00:00Z', end: '2026-10-01T07:30:00Z', state: 'planned', closes_distractions: true, status: 'past', observed: {
              minutes: 90, distraction_minutes: 12, coding: [{ device: 'mac', title: 'Fix the collector', messages_sent: 4, agent_minutes: 31 }], devices: [
                { device: 'mac', active_minutes: 71, no_data_minutes: 0, apps: apps([['T3 Code', 52], ['Helium', 19]]), sites: [{ name: 'youtube.com', app: 'Helium', minutes: 12, sessions: 2, label: 'distraction' }], labels: [] },
                { device: 'phone', active_minutes: 4, no_data_minutes: 20, apps: apps([['com.whatsapp', 4]]), labels: [] }] } },
            { id: 2, title: 'Shower', start: '2026-10-01T15:00:00Z', end: '2026-10-01T15:30:00Z', state: 'planned', closes_distractions: false, status: 'upcoming' }
          ],
          outside_blocks: { minutes: 600, distraction_minutes: 171, devices: [
            { device: 'mac', active_minutes: 241, no_data_minutes: 480, apps: apps([['osu!', 77], ['Helium', 109]]), sites: [{ name: 'youtube.com', app: 'Helium', minutes: 34, sessions: 7, label: 'distraction' }], labels: [] },
            { device: 'phone', active_minutes: 272, no_data_minutes: 8, apps: apps([['com.twitter.android', 60]]), labels: [] }] }
        } }];
      }
      if (path === '/activity-labels') return [200, { labels: labels.filter(label => !label.removed_at), proposals: labelProposals }];
      const labelDecision = path.match(/^\/activity-label-proposals\/(\d+)\/(accept|reject)$/);
      if (labelDecision && method === 'POST') {
        const proposal = labelProposals.find(row => row.id === Number(labelDecision[1]));
        if (!proposal || proposal.status !== 'pending') return [409, { error: { detail: 'already decided' } }];
        if (body.content_hash !== proposal.content_hash) return [409, { error: { detail: 'content_hash does not match this proposal' } }];
        proposal.status = labelDecision[2] === 'accept' ? 'accepted' : 'rejected';
        if (proposal.status === 'accepted') for (const change of proposal.changes) labels.push({ id: labels.length + 1, ...change, created_at: created });
        return [200, proposal];
      }
      const labelRemove = path.match(/^\/activity-labels\/(\d+)$/);
      if (labelRemove && method === 'DELETE') {
        const label = labels.find(row => row.id === Number(labelRemove[1]));
        if (label) label.removed_at = created;
        return [200, { removed: Number(labelRemove[1]) }];
      }
      // Invented blocklist state for the Blocked apps editor.
      if (path === '/policy/config' && method === 'GET') return [200, { policy: { id: policyVersion, config: policyConfig } }];
      if (path === '/policy/suggestions') return [200, { android_apps: [{ package: 'com.google.android.youtube', events: 120, last_seen: created }] }];
      if (path === '/policy/proposals') return [200, { proposals: policyProposals }];
      const policyDecision = path.match(/^\/policy\/proposals\/(\d+)\/(accept|reject)$/);
      if (policyDecision && method === 'POST') {
        const proposal = policyProposals.find(row => row.id === Number(policyDecision[1]));
        if (!proposal || proposal.status !== 'pending') return [409, { error: { detail: 'already decided' } }];
        if (body.content_hash !== proposal.content_hash) return [409, { error: { detail: 'content_hash does not match this proposal' } }];
        proposal.status = policyDecision[2] === 'accept' ? 'accepted' : 'rejected';
        if (proposal.status === 'accepted') {
          policyVersion++;
          for (const change of proposal.changes) policyConfig.distractions.push({ id: change.android_package, label: change.android_package, android_packages: [change.android_package] });
          proposal.result_policy_version_id = policyVersion;
        }
        return [200, proposal];
      }
      if (path === '/planning/settings') return [200, { daily_plan_gate_enabled: true, enforcement_active: false }];
      if (path.startsWith('/activities')) return [200, { activities }];
      if (path === `/plans/${day}/replan-proposals` && method === 'GET') return [200, { proposals: [...proposals].reverse() }];
      if (path === `/plans/${day}/replan-proposals` && method === 'POST') {
        // Canned diff for display only; the real diff is computed by the Go core.
        const block = blocks[0];
        const moved = new Date(Date.parse(body.disruption.end ?? body.disruption.start)).toISOString();
        proposals.push({ id: proposals.length + 1, plan_date: day, base_plan_version_id: 1, origin: 'disruption_helper', reason: body.reason, content_hash: `fixture-${proposals.length + 1}`, status: 'pending', created_at: created,
          proposed_plan: { note: body.reason, blocks: [] }, warnings: session && !session.ended_at && !session.resume_marker ? [{ code: 'missing_resume_marker', detail: `"${block.title}" is moved but its open execution session has no resume marker; add one before accepting to keep return context` }] : [],
          changes: [{ kind: 'moved', base_block_id: block.id, title: block.title, fixed: false, old_start: block.start_time, old_duration_minutes: 60, new_start: moved, new_duration_minutes: 60 }] });
        return [201, proposals[proposals.length - 1]];
      }
      const decision = path.match(/^\/plans\/[^/]+\/replan-proposals\/(\d+)\/(accept|reject)$/);
      if (decision && method === 'POST') {
        const proposal = proposals.find(row => row.id === Number(decision[1]));
        if (!proposal) return [404, { error: { detail: 'not found' } }];
        if (decision[2] === 'accept' && body.content_hash !== proposal.content_hash) return [409, { error: { detail: 'content_hash does not match this proposal' } }];
        proposal.status = decision[2] === 'accept' ? 'accepted' : 'rejected';
        return [200, proposal];
      }
      if (path === `/plans/${day}` && method === 'GET') return [200, { plan: { plan_date: day, version_id: 1, created_at: created, note: 'fixture', blocks } }];
      if (path.startsWith('/plans/') && path.endsWith('/versions')) return [200, { versions: [] }];
      if (path.startsWith('/plans/') && method === 'GET') return [200, { plan: null }];
      const blockMatch = path.match(/^\/plans\/[^/]+\/blocks\/(\d+)$/);
      if (blockMatch && method === 'PATCH') {
        const block = blocks.find(row => row.id === Number(blockMatch[1]));
        if (!block) return [404, { error: { detail: 'not found' } }];
        block.state = body.state;
        return [200, block];
      }
      if (path === '/execution/primary' || path === '/execution/attended') return [200, { session: session && !session.ended_at ? session : null }];
      if (path === '/execution/sessions' && method === 'POST') {
        if (session && !session.ended_at) return [409, { error: { detail: 'a primary execution is already open' } }];
        const at = new Date().toISOString();
        session = { id: (session?.id ?? 0) + 1, activity_id: body.activity_id, plan_block_id: body.plan_block_id, role: 'primary', started_at: at, state: 'active', next_action_snapshot: activities[0].next_action, created_at: at, updated_at: at, events: [{ id: ++eventId, session_id: 0, event_type: 'started', occurred_at: at, created_at: at }] };
        return [201, session];
      }
      const eventMatch = path.match(/^\/execution\/sessions\/(\d+)\/events$/);
      if (eventMatch && method === 'POST') {
        if (!session || session.id !== Number(eventMatch[1])) return [404, { error: { detail: 'execution session does not exist' } }];
        if (terminal.has(session.state)) return [400, { error: { detail: `terminal session cannot receive ${body.event_type}` } }];
        const at = new Date().toISOString();
        session.events.push({ id: ++eventId, session_id: session.id, event_type: body.event_type, occurred_at: at, note: body.note, created_at: at });
        session.state = nextState[body.event_type] ?? session.state;
        if (body.resume_marker !== undefined) session.resume_marker = body.resume_marker;
        if (terminal.has(session.state)) session.ended_at = at;
        return [200, session];
      }
      return undefined;
    }
  };
}

function fixtureAPI(): Plugin {
  const execution = fixtureExecution();
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
        const path = (request.url ?? '/').split('?')[0];
        if (path.startsWith('/execution') || path.startsWith('/plans') || path.startsWith('/activities') || path.startsWith('/activity') || path.startsWith('/policy') || path.startsWith('/context-rules') || path === '/planning/settings') {
          let raw = '';
          request.on('data', chunk => raw += chunk);
          request.on('end', () => {
            const result = execution.handle(request.method ?? 'GET', path, raw ? JSON.parse(raw) : undefined);
            if (result) send(result[0], result[1]);
            else send(404, { error: { detail: 'not in fixture' } });
          });
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
