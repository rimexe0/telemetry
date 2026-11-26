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
  // Fixture numbers: no wait, so the confirmation pages show at once.
  let openingRule: Record<string, number> = { wait_minutes: 0, max_minutes: 30, per_day: 2, confirmations: 10, confirm_seconds: 2 };
  const openings: Record<string, any>[] = [];
  let morningRule: Record<string, any> = { enabled: false, from_wake: true, until: '11:00', lock_all: true };
  let morningMade: string | undefined;
  let lockRule: Record<string, any> = { enabled: false };
  let bedtime: Record<string, any> = { enabled: false };
  let fixturePin = '';
  const lockState: Record<string, any> = { locked: false, pin_set: false, pin_open: true, tries_left: 3 };
  // A 1x1 placeholder; the real server draws the code.
  const fixtureQR = 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAAAAAA6fptVAAAACklEQVR4nGNgAAAAAgABSK+kcQAAAABJRU5ErkJggg==';
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
      if (path === '/policy/config' && method === 'PUT') {
        policyVersion++;
        Object.assign(policyConfig, body.config);
        return [200, { policy: { id: policyVersion, config: policyConfig } }];
      }
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
      if (path === '/openings' && method === 'GET') return [200, { rule: openingRule, used_today: openings.length, openings, distractions: policyConfig.distractions.length ? policyConfig.distractions : [{ id: 'fixture-feed', label: 'Fixture feed' }, { id: 'fixture-video', label: 'Fixture video' }] }];
      if (path === '/openings' && method === 'POST') {
        if (openingRule.max_minutes && body.minutes > openingRule.max_minutes) return [409, { error: { detail: `opening refused: an opening lasts at most ${openingRule.max_minutes} minutes` } }];
        if (openingRule.per_day && openings.length >= openingRule.per_day) return [409, { error: { detail: `opening refused: all ${openingRule.per_day} openings for today are used` } }];
        const starts = Date.now() + (openingRule.wait_minutes ?? 0) * 60_000;
        const needed = openingRule.confirmations ?? 0, seconds = openingRule.confirm_seconds ?? 0;
        openings.push({ id: openings.length + 1, purpose: body.purpose, sites: [], apps: [], everything: body.everything, starts_at: new Date(starts).toISOString(),
          until: new Date(starts + (needed ? 3_600_000 : body.minutes * 60_000)).toISOString(), created_at: created,
          confirmations_needed: needed, confirmations: 0, confirm_seconds: seconds, length_minutes: body.minutes, next_confirm_at: needed ? new Date(starts + seconds * 1000).toISOString() : undefined });
        return [201, { opening: openings[openings.length - 1] }];
      }
      const endMatch = path.match(/^\/openings\/(\d+)\/end$/);
      if (endMatch && method === 'POST') {
        const opening = openings.find(row => row.id === Number(endMatch[1]));
        if (!opening) return [404, { error: { detail: 'no such opening' } }];
        opening.ended_at = new Date().toISOString();
        return [200, { opening }];
      }
      const confirmMatch = path.match(/^\/openings\/(\d+)\/confirm$/);
      if (confirmMatch && method === 'POST') {
        const opening = openings.find(row => row.id === Number(confirmMatch[1]));
        if (!opening) return [404, { error: { detail: 'no such opening' } }];
        if (Date.now() < Date.parse(opening.next_confirm_at)) return [409, { error: { detail: 'opening refused: wait' } }];
        opening.confirmations++;
        opening.next_confirm_at = new Date(Date.now() + opening.confirm_seconds * 1000).toISOString();
        if (opening.confirmations >= opening.confirmations_needed) {
          opening.starts_at = new Date().toISOString();
          opening.until = new Date(Date.now() + opening.length_minutes * 60_000).toISOString();
          delete opening.next_confirm_at;
        }
        return [200, { opening }];
      }
      if (path === '/openings/rule' && method === 'PUT') {
        openingRule = body;
        return [200, { opening_rule: openingRule, morning: morningRule }];
      }
      if (path === '/morning' && method === 'GET') return [200, { rule: morningRule, holding: false, scan_address: 'http://127.0.0.1/morning?code=', code_made: morningMade }];
      if (path === '/morning' && method === 'PUT') { morningRule = body; return [200, { opening_rule: openingRule, morning: morningRule }]; }
      if (path === '/morning/code' && method === 'POST') {
        morningMade = new Date().toISOString();
        return [201, { address: 'http://127.0.0.1/morning?code=fixture', image: fixtureQR }];
      }
      if (path === '/lock' && method === 'GET') return [200, { lock: lockRule, bedtime, state: lockState }];
      if (path === '/lock' && method === 'PUT') {
        if (body.lock) { if (body.lock.enabled && !lockState.pin_set) return [400, { error: { detail: 'set the PIN in the panel before turning the settings lock on' } }]; lockRule = body.lock; lockState.locked = body.lock.enabled; }
        if (body.bedtime) bedtime = body.bedtime;
        return [200, { lock: lockRule, bedtime, state: lockState }];
      }
      if (path === '/lock/pin' && method === 'POST') { if (fixturePin && body.current !== fixturePin) return [423, { error: { detail: 'blocking settings are locked: wrong PIN' } }]; fixturePin = body.pin; lockState.pin_set = true; return [200, { pin_set: true }]; }
      if (path === '/lock/unlock' && method === 'POST') {
        if (body.pin !== fixturePin) { lockState.tries_left = Math.max(0, lockState.tries_left - 1); return [423, { error: { detail: `blocking settings are locked: wrong PIN, ${lockState.tries_left} tries left this hour` } }]; }
        const until = new Date(Date.now() + (lockRule.unlock_minutes ?? 15) * 60_000).toISOString();
        lockState.locked = false; lockState.unlocked_until = until;
        return [200, { unlocked_until: until }];
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
      return fixtureBudget.handle(method, path, body);
    }
  };
}

// Invented habits, budget and door so the Habits view and the door screen
// can be clicked through. Numbers are the contract's examples, not the
// owner's; the arithmetic is simplified and is not evidence of the server.
const fixtureBudget = (() => {
  const dayOf = (at: number) => new Date(at + 3 * 3_600_000).toISOString().slice(0, 10);
  const today = dayOf(Date.now());
  const addDays = (day: string, delta: number) => { const at = new Date(`${day}T12:00:00Z`); at.setUTCDate(at.getUTCDate() + delta); return at.toISOString().slice(0, 10); };
  const habits: Record<string, any>[] = [
    { id: 1, name: 'Fixture walk', position: 0, weekdays: [], created_day: addDays(today, -300) },
    { id: 2, name: 'Fixture reading', position: 1, weekdays: [1, 3, 5], created_day: addDays(today, -120) },
    { id: 3, name: 'Fixture stretch', position: 2, weekdays: [], created_day: addDays(today, -40) },
    { id: 4, name: 'Fixture old habit', position: 3, weekdays: [], archived: true, created_day: addDays(today, -365), archived_day: addDays(today, -200) }
  ];
  const ticks = new Set<number>([1]);
  let reasons = ['Fixture reason: a line the owner writes about what they want.'];
  const followUps: Record<string, any>[] = [
    { id: 1, title: 'Fixture follow-up that is due', due_at: new Date(Date.now() - 20 * 60_000).toISOString(), state: 'open' },
    { id: 2, title: 'Fixture follow-up later today', due_at: new Date(Math.min(Date.now() + 90 * 60_000, Date.parse(`${today}T20:59:00Z`))).toISOString(), state: 'open' }
  ];
  let door: Record<string, any> | null = null;
  const settings = { enabled: true, base_minutes: 120, max_minutes: 300, floor_minutes: 30, habits_for_max: 6, rate_distraction: 1, rate_day: 3, rate_bedtime: 10,
    rate_grayscale_off: 3, borrow_multiplier: 2, window_minutes: 60, window_use_minutes: 10, grace_minutes: 1, wait_minutes: 10 };
  const scheduled = (habit: Record<string, any>, day: string) => !habit.weekdays?.length || habit.weekdays.includes(new Date(`${day}T12:00:00Z`).getUTCDay());
  const active = () => habits.filter(habit => !habit.archived).sort((a, b) => a.position - b.position);
  // A steady invented pattern: most days done, some not, a few without a record.
  const pattern = (id: number, back: number) => ((back * 7 + id * 3) % 10) < 7;
  function heatmap(days: number) {
    const combined: Record<string, number | null> = {};
    const rows = habits.map(habit => {
      const cells: Record<string, string> = {};
      let current = 0, longest = 0, run = 0, counting = true;
      for (let back = 0; back < days; back++) {
        const day = addDays(today, -back);
        let status = day < habit.created_day || (habit.archived_day && day > habit.archived_day) ? 'none' : !scheduled(habit, day) ? 'unscheduled'
          : back === 0 ? (ticks.has(habit.id) ? 'done' : 'missed') : pattern(habit.id, back) ? 'done' : 'missed';
        if (back > 0 && back % 53 === 0) continue; // no record that day
        cells[day] = status;
        if (status === 'done') { run++; longest = Math.max(longest, run); if (counting) current = run; }
        else if (status === 'missed' && back > 0) { run = 0; counting = false; }
      }
      return { id: habit.id, name: habit.name, created_day: habit.created_day, archived_day: habit.archived_day ?? null, days: cells, current_run: current, longest_run: longest };
    });
    for (let back = 0; back < days; back++) {
      const day = addDays(today, -back);
      const states = rows.map(row => row.days[day]).filter(state => state === 'done' || state === 'missed');
      if (states.length) combined[day] = states.filter(state => state === 'done').length / states.length;
    }
    return { habits: rows, combined };
  }
  function doorNow() {
    if (!door || door.state === 'cancelled') return door;
    const now = Date.now();
    if (now >= Date.parse(door.opens_at)) door.state = now >= Date.parse(door.ends_at) ? 'ended' : now >= Date.parse(door.ends_at) - 60_000 ? 'grace' : 'open';
    if (door.state === 'open' || door.state === 'grace') door.used_minutes = Math.min(10, (now - Date.parse(door.opens_at)) / 60_000);
    return door;
  }
  function state() {
    const todays = active().filter(habit => scheduled(habit, today));
    const perHabit = (settings.max_minutes - settings.base_minutes) / Math.max(todays.length, settings.habits_for_max);
    const earned = todays.filter(habit => ticks.has(habit.id)).length * perHabit;
    const budget = Math.max(settings.floor_minutes, Math.min(settings.base_minutes + earned, settings.max_minutes) - 30);
    const spent = 47.5;
    const live = doorNow();
    const open = live && ['waiting', 'open', 'grace'].includes(live.state);
    return {
      enabled: true, day: today, budget, spent, left: budget - spent, earned, penalty: 30, debt: 0, debt_tomorrow: 0, borrowable: 90,
      rate: { total: 3, base: 3, grayscale_off: 0, kind: 'day' }, in_distraction_time: false,
      next_distraction_time: new Date(`${today}T16:00:00Z`).toISOString(), bedtime_at: new Date(`${today}T21:00:00Z`).toISOString(),
      wind_down: false, grayscale_declined: false, door: live, door_available: !open, door_unavailable_reason: open ? 'a door is already open' : '', next_door_at: null,
      habits: todays.map(habit => ({ id: habit.id, name: habit.name, done: ticks.has(habit.id), value: perHabit })), reasons,
      settings: { wait_minutes: settings.wait_minutes, window_minutes: settings.window_minutes, window_use_minutes: settings.window_use_minutes, grace_minutes: settings.grace_minutes, borrow_multiplier: settings.borrow_multiplier }
    };
  }
  return {
    handle(method: string, path: string, body: any): [number, unknown] | undefined {
      if (path === '/budget' && method === 'GET') {
        const days = Array.from({ length: 30 }, (_, index) => addDays(today, -(index + 1))).filter((_, index) => index % 11 !== 4)
          .map((day, index) => ({ day, budget: 150 + (index % 4) * 30, spent: 40 + (index * 17) % 120, debt_passed_on: index % 9 === 0 ? 20 : 0, habits_done: 2 + (index % 2), habits_missed: index % 3 === 0 ? 1 : 0 }));
        return [200, { budget: state(), days }];
      }
      if (path === '/door' && method === 'POST') {
        const live = doorNow();
        if (live && ['waiting', 'open', 'grace'].includes(live.state)) return [409, { error: 'door_unavailable', message: 'A door is already open.' }];
        // Fixture: the wait is 20 seconds so the countdown can be followed.
        const opens = Date.now() + 20_000;
        door = { id: (door?.id ?? 0) + 1, state: 'waiting', requested_at: new Date().toISOString(), opens_at: new Date(opens).toISOString(),
          window_end: new Date(opens + 3_600_000).toISOString(), ends_at: new Date(opens + 11 * 60_000).toISOString(), used_minutes: 0, use_minutes: 10, borrowing: false };
        return [201, { door }];
      }
      const cancel = path.match(/^\/door\/(\d+)\/cancel$/);
      if (cancel && method === 'POST') {
        if (!door || door.id !== Number(cancel[1]) || doorNow()?.state !== 'waiting') return [409, { error: 'door_not_waiting', message: 'Only a waiting door can be cancelled.' }];
        door.state = 'cancelled';
        return [200, { door }];
      }
      if (path === '/door/back' && method === 'POST') return [200, { recorded: true }];
      if (path === '/habits' && method === 'GET') return [200, { habits }];
      if (path === '/habits' && method === 'POST') {
        const habit = { id: habits.length + 1, name: body.name, position: active().length, weekdays: body.weekdays ?? [], created_day: today };
        habits.push(habit);
        return [201, { habit }];
      }
      if (path === '/habits/heatmap' && method === 'GET') return [200, heatmap(365)];
      const patch = path.match(/^\/habits\/(\d+)$/);
      if (patch && method === 'PATCH') {
        const habit = habits.find(row => row.id === Number(patch[1]));
        if (!habit) return [404, { error: { detail: 'no such habit' } }];
        Object.assign(habit, body);
        if (body.archived === true) habit.archived_day = today;
        if (body.archived === false) delete habit.archived_day;
        return [200, { habit }];
      }
      const tick = path.match(/^\/habits\/(\d+)\/(tick|untick)$/);
      if (tick && method === 'POST') {
        if (tick[2] === 'tick') ticks.add(Number(tick[1])); else ticks.delete(Number(tick[1]));
        return [200, { budget: state() }];
      }
      if (path === '/reasons' && method === 'GET') return [200, { reasons }];
      if (path === '/reasons' && method === 'PUT') { reasons = body.reasons; return [200, { reasons }]; }
      // Today's follow-ups, what is on the Mac and one open collection problem.
      if (path === '/device/now' && method === 'GET') {
        const ago = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString();
        return [200, {
          generated_at: new Date().toISOString(), budget: state(),
          tracking: { state: 'task', task_id: 3, task: 'Fixture task', since: ago(25), context: 'Fixture editor', agent_running: true,
            your_turn: [{ task_id: 4, task: 'Fixture agent task', since: ago(6) }] },
          follow_ups: followUps.filter(item => item.state === 'open'),
          collection_incidents: [{ id: 1, device: 'fixture-mac', kind: 'silent', started_at: ago(50), detected_at: ago(40), detail: { device_name: 'Fixture Mac' } }]
        }];
      }
      const followUpDone = path.match(/^\/device\/follow-ups\/(\d+)\/done$/);
      if (followUpDone && method === 'POST') {
        const item = followUps.find(row => row.id === Number(followUpDone[1]));
        if (!item) return [404, { error: { detail: 'follow-up does not exist' } }];
        item.state = 'done';
        return [200, { follow_up: item }];
      }
      return undefined;
    }
  };
})();

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
        // Fixture previews never wait a day.
        if ((request.url ?? '').includes('preview=1')) {
          send(200, { waits_until: {} });
          return;
        }
        if (path.startsWith('/execution') || path.startsWith('/plans') || path.startsWith('/activities') || path.startsWith('/activity') || path.startsWith('/policy') || path.startsWith('/context-rules') || path.startsWith('/openings') || path.startsWith('/morning') || path.startsWith('/lock') || path === '/planning/settings' ||
          path.startsWith('/budget') || path.startsWith('/door') || path.startsWith('/habits') || path === '/reasons' || path.startsWith('/device/')) {
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
