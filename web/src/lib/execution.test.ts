import { describe, expect, it, vi } from 'vitest';
import { deriveNowView, performNowAction, type ExecutionAPI, type NowSnapshot } from './execution';
import type { Activity, ExecutionEvent, ExecutionSession, Plan, PlanBlock } from './api';

const now = new Date('2026-09-25T07:30:00Z'); // 10:30 Istanbul

const activity = (overrides: Partial<Activity> = {}): Activity => ({
  id: 7, title: 'Write report', notes: '', next_action: 'Draft the summary paragraph', resume_marker: '',
  status: 'active', activity_state: 'growth', priority: 0, created_at: '', updated_at: '', ...overrides
});

const block = (overrides: Partial<PlanBlock> = {}): PlanBlock => ({
  id: 11, activity_id: 7, title: 'Write report', start_time: '2026-09-25T10:00:00+03:00', duration_minutes: 60,
  fixed: false, state: 'planned', state_note: '', position: 0, ...overrides
});

const plan = (blocks: PlanBlock[]): Plan => ({ plan_date: '2026-09-25', version_id: 1, created_at: '', note: '', blocks });

let eventId = 0;
const event = (event_type: string, overrides: Partial<ExecutionEvent> = {}): ExecutionEvent => ({
  id: ++eventId, session_id: 3, event_type, occurred_at: '2026-09-25T07:00:00Z', created_at: '', ...overrides
});

const session = (overrides: Partial<ExecutionSession> = {}): ExecutionSession => ({
  id: 3, activity_id: 7, plan_block_id: 11, role: 'primary', started_at: '2026-09-25T07:00:00Z', state: 'active',
  next_action_snapshot: 'snapshot action', created_at: '', updated_at: '', events: [event('started')], ...overrides
});

const snapshot = (overrides: Partial<NowSnapshot> = {}): NowSnapshot => ({
  day: '2026-09-25', plan: plan([block()]), activities: [activity()], primary: null, attended: null, planGateEnabled: true, ...overrides
});

const api = () => {
  const calls: unknown[][] = [];
  const record = (name: string) => vi.fn(async (...args: unknown[]) => { calls.push([name, ...args]); return {} as never; });
  const client: ExecutionAPI = {
    startExecutionSession: record('start'),
    recordExecutionEvent: record('event'),
    returnToPrimary: record('return'),
    setPlanBlockState: record('block')
  };
  return { client, calls };
};

describe('NOW view derivation', () => {
  it('asks for a plan when none exists', () => {
    expect(deriveNowView(snapshot({ plan: null }), now)).toEqual({ kind: 'plan_your_day', day: '2026-09-25', planGateEnabled: true });
  });

  it('shows the current block and its next action before execution starts', () => {
    const view = deriveNowView(snapshot(), now);
    expect(view.kind).toBe('ready');
    if (view.kind !== 'ready') return;
    expect(view.task.title).toBe('Write report');
    expect(view.task.nextAction).toBe('Draft the summary paragraph');
    expect(view.upcoming).toBe(false);
  });

  it('offers the next block when nothing is scheduled right now', () => {
    const view = deriveNowView(snapshot({ plan: plan([block({ start_time: '2026-09-25T13:00:00+03:00' })]) }), now);
    expect(view.kind).toBe('ready');
    if (view.kind === 'ready') expect(view.upcoming).toBe(true);
  });

  it('asks the owner to choose instead of guessing between overlapping blocks', () => {
    const view = deriveNowView(snapshot({ plan: plan([block(), block({ id: 12, activity_id: 8 })]) }), now);
    expect(view.kind).toBe('choose');
    if (view.kind === 'choose') expect(view.startable.map(b => b.id)).toEqual([11, 12]);
  });

  it('does not invent a task when the current block has no linked activity', () => {
    const view = deriveNowView(snapshot({ plan: plan([block({ activity_id: undefined, title: 'Errands' })]) }), now);
    expect(view.kind).toBe('choose');
    if (view.kind === 'choose') {
      expect(view.startable).toEqual([]);
      expect(view.unlinked.map(b => b.title)).toEqual(['Errands']);
    }
  });

  it('shows the active primary with its next action', () => {
    const view = deriveNowView(snapshot({ primary: session(), attended: session() }), now);
    expect(view.kind).toBe('active');
    if (view.kind === 'active') {
      expect(view.task.title).toBe('Write report');
      expect(view.task.nextAction).toBe('Draft the summary paragraph');
      expect(view.task.block?.id).toBe(11);
    }
  });

  it('follows a replanned block back to the session through source_block_id', () => {
    const replanned = plan([block({ id: 21, source_block_id: 11, start_time: '2026-09-25T11:00:00+03:00' })]);
    const view = deriveNowView(snapshot({ plan: replanned, primary: session() }), now);
    expect(view.kind === 'active' && view.task.block?.id).toBe(21);
  });

  it('keeps live execution visible even without a plan', () => {
    expect(deriveNowView(snapshot({ plan: null, primary: session() }), now).kind).toBe('active');
  });

  it('keeps the primary task current while waiting externally', () => {
    const primary = session({ state: 'waiting_external', events: [event('started'), event('waiting_external', { note: 'Codex running tests' })] });
    const view = deriveNowView(snapshot({ primary, attended: null }), now);
    expect(view.kind).toBe('waiting');
    if (view.kind !== 'waiting') return;
    expect(view.task.title).toBe('Write report');
    expect(view.waitingNote).toBe('Codex running tests');
    expect(view.ready).toBe(false);
  });

  it('shows a waiting interleave separately from the primary', () => {
    const primary = session({ state: 'waiting_external', events: [event('started'), event('waiting_external')] });
    const interleave = session({ id: 4, activity_id: 8, plan_block_id: undefined, role: 'waiting_interleave', primary_session_id: 3 });
    const view = deriveNowView(snapshot({ primary, attended: interleave, activities: [activity(), activity({ id: 8, title: 'Inbox' })] }), now);
    expect(view.kind).toBe('waiting');
    if (view.kind !== 'waiting') return;
    expect(view.task.title).toBe('Write report');
    expect(view.interleaveTask?.title).toBe('Inbox');
  });

  it('marks the primary ready once the external work finishes', () => {
    const primary = session({ state: 'waiting_external', events: [event('started'), event('waiting_external'), event('external_ready')] });
    const view = deriveNowView(snapshot({ primary }), now);
    expect(view.kind === 'waiting' && view.ready).toBe(true);
  });

  it('puts the resume marker first for interrupted work', () => {
    const primary = session({ state: 'interrupted', resume_marker: 'Halfway through section 2', events: [event('started'), event('switched_out')] });
    const view = deriveNowView(snapshot({ primary }), now);
    expect(view.kind).toBe('interrupted');
    if (view.kind === 'interrupted') expect(view.task.resumeMarker).toBe('Halfway through section 2');
  });

  it('recognises offline recovery from the latest event', () => {
    const primary = session({ state: 'interrupted', events: [event('started'), event('offline_recovery_started', { occurred_at: '2026-09-25T07:20:00Z' })] });
    const view = deriveNowView(snapshot({ primary }), now);
    expect(view).toMatchObject({ kind: 'recovery', startedAt: '2026-09-25T07:20:00Z' });
  });

  it('treats an ended recovery as paused work waiting to be resumed', () => {
    const primary = session({ state: 'interrupted', events: [event('started'), event('offline_recovery_started'), event('offline_recovery_ended')] });
    expect(deriveNowView(snapshot({ primary }), now).kind).toBe('interrupted');
  });

  it('does not render a finished session as active', () => {
    const primary = session({ state: 'completed', ended_at: '2026-09-25T07:25:00Z' });
    const view = deriveNowView(snapshot({ primary, plan: plan([block({ state: 'done' })]) }), now);
    expect(view.kind).toBe('choose');
  });

  it('falls back to the session snapshot when the activity is unknown', () => {
    const view = deriveNowView(snapshot({ primary: session({ activity_id: 99 }), activities: [], plan: null }), now);
    expect(view.kind === 'active' && view.task).toMatchObject({ title: 'Activity #99', nextAction: 'snapshot action' });
  });
});

describe('NOW actions', () => {
  const active = () => deriveNowView(snapshot({ primary: session() }), now);

  it('starts a session for the block and marks the block active', async () => {
    const { client, calls } = api();
    const view = deriveNowView(snapshot(), now);
    if (view.kind !== 'ready') throw new Error('expected ready');
    await performNowAction(client, view, '2026-09-25', { type: 'start', block: view.block, activityId: view.activityId });
    expect(calls).toEqual([
      ['start', { activity_id: 7, plan_block_id: 11 }],
      ['block', '2026-09-25', 11, 'active']
    ]);
  });

  it('records the two stuck choices as distinct events without ending the session', async () => {
    const { client, calls } = api();
    await performNowAction(client, active(), '2026-09-25', { type: 'stuck_unknown' });
    await performNowAction(client, active(), '2026-09-25', { type: 'stuck_overwhelm' });
    expect(calls).toEqual([
      ['event', 3, { event_type: 'stuck_unknown_next_action' }],
      ['event', 3, { event_type: 'stuck_emotional_overwhelm' }]
    ]);
  });

  it('keeps the resume marker when switching out', async () => {
    const { client, calls } = api();
    await performNowAction(client, active(), '2026-09-25', { type: 'switch', resumeMarker: ' Continue at test 4 ' });
    expect(calls).toEqual([['event', 3, { event_type: 'switched_out', resume_marker: 'Continue at test 4' }]]);
  });

  it('completes the session and its plan block', async () => {
    const { client, calls } = api();
    await performNowAction(client, active(), '2026-09-25', { type: 'done' });
    expect(calls).toEqual([
      ['event', 3, { event_type: 'completed' }],
      ['block', '2026-09-25', 11, 'done']
    ]);
  });

  it('records overwhelm before starting offline recovery', async () => {
    const { client, calls } = api();
    await performNowAction(client, active(), '2026-09-25', { type: 'recovery_start', overwhelmed: true });
    expect(calls.map(call => (call[2] as { event_type: string }).event_type)).toEqual(['overwhelmed', 'offline_recovery_started']);
  });

  it('ends recovery and resumes the same task', async () => {
    const { client, calls } = api();
    const primary = session({ state: 'interrupted', events: [event('started'), event('offline_recovery_started')] });
    await performNowAction(client, deriveNowView(snapshot({ primary }), now), '2026-09-25', { type: 'recovery_end' });
    expect(calls.map(call => (call[2] as { event_type: string }).event_type)).toEqual(['offline_recovery_ended', 'resumed']);
  });

  it('returns from an interleave through the return-to-primary endpoint', async () => {
    const { client, calls } = api();
    const primary = session({ state: 'waiting_external', events: [event('started'), event('waiting_external')] });
    const interleave = session({ id: 4, role: 'waiting_interleave', primary_session_id: 3 });
    await performNowAction(client, deriveNowView(snapshot({ primary, attended: interleave }), now), '2026-09-25', { type: 'resume' });
    expect(calls).toEqual([['return', 4]]);
  });

  it('propagates a rejected transition without recording anything else', async () => {
    const { client, calls } = api();
    client.recordExecutionEvent = vi.fn().mockRejectedValue(new Error('terminal session cannot receive completed'));
    await expect(performNowAction(client, active(), '2026-09-25', { type: 'done' })).rejects.toThrow('terminal session');
    expect(calls).toEqual([]);
  });
});
