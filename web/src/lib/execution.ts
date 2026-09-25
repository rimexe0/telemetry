import type { Activity, ExecutionEventInput, ExecutionReturn, ExecutionSession, ExecutionSessionInput, Plan, PlanBlock } from './api';
import { blockEnd, sortBlocks } from './plan';

// Everything the NOW surface shows is derived from these server reads. The
// browser keeps no task model of its own; after every action it reloads them.
export type NowSnapshot = {
  day: string;
  plan: Plan | null;
  activities: Activity[];
  primary: ExecutionSession | null;
  attended: ExecutionSession | null;
  planGateEnabled: boolean;
};

export type NowTask = {
  title: string;
  nextAction: string;
  resumeMarker: string;
  block?: PlanBlock;
};

export type NowView =
  | { kind: 'plan_your_day'; day: string; planGateEnabled: boolean }
  | { kind: 'ready'; block: PlanBlock; activityId: number; task: NowTask; upcoming: boolean }
  | { kind: 'choose'; startable: PlanBlock[]; unlinked: PlanBlock[] }
  | { kind: 'active'; primary: ExecutionSession; task: NowTask }
  | { kind: 'waiting'; primary: ExecutionSession; task: NowTask; waitingNote: string; ready: boolean; interleave?: ExecutionSession; interleaveTask?: NowTask }
  | { kind: 'interrupted'; primary: ExecutionSession; task: NowTask }
  | { kind: 'recovery'; primary: ExecutionSession; task: NowTask; startedAt: string };

const openBlockStates: PlanBlock['state'][] = ['planned', 'active'];

const lastEvent = (session: ExecutionSession) => {
  const events = session.events ?? [];
  return events.length ? events.reduce((latest, event) => (event.id > latest.id ? event : latest)) : undefined;
};

const latestEventOfType = (session: ExecutionSession, types: string[]) =>
  (session.events ?? []).filter(event => types.includes(event.event_type)).sort((a, b) => b.id - a.id)[0];

export function taskFor(session: ExecutionSession, snapshot: Pick<NowSnapshot, 'activities' | 'plan'>): NowTask {
  const activity = snapshot.activities.find(row => row.id === session.activity_id);
  // After an accepted replan the session still names the older block; the
  // current version links back to it through source_block_id.
  const block = session.plan_block_id
    ? snapshot.plan?.blocks.find(row => row.id === session.plan_block_id) ?? snapshot.plan?.blocks.find(row => row.source_block_id === session.plan_block_id)
    : undefined;
  return {
    // A missing activity is shown by id rather than guessed from the plan.
    title: activity?.title ?? block?.title ?? `Activity #${session.activity_id}`,
    nextAction: activity?.next_action || session.next_action_snapshot,
    resumeMarker: session.resume_marker || activity?.resume_marker || '',
    block
  };
}

// The block whose window contains `now`, or else the next one to start. When
// several open blocks overlap `now`, intent is ambiguous and the owner chooses.
export function currentBlock(plan: Plan, now: Date): { block?: PlanBlock; upcoming: boolean; ambiguous: boolean } {
  const time = now.getTime();
  const open = sortBlocks(plan.blocks).filter(block => openBlockStates.includes(block.state));
  const running = open.filter(block => Date.parse(block.start_time) <= time && time < Date.parse(blockEnd(block)));
  if (running.length > 1) return { upcoming: false, ambiguous: true };
  if (running.length === 1) return { block: running[0], upcoming: false, ambiguous: false };
  const next = open.find(block => Date.parse(block.start_time) > time);
  return { block: next, upcoming: Boolean(next), ambiguous: false };
}

export function deriveNowView(snapshot: NowSnapshot, now: Date): NowView {
  const { primary } = snapshot;
  // Live execution outranks the plan: an unfinished primary is shown even when
  // today's plan is missing, so work is never hidden behind the planning prompt.
  if (primary && !primary.ended_at) {
    const task = taskFor(primary, snapshot);
    const last = lastEvent(primary);
    if (primary.state === 'interrupted' && last?.event_type === 'offline_recovery_started') {
      return { kind: 'recovery', primary, task, startedAt: last.occurred_at };
    }
    if (primary.state === 'interrupted') return { kind: 'interrupted', primary, task };
    if (primary.state === 'waiting_external') {
      const waiting = latestEventOfType(primary, ['waiting_external']);
      const interleave = snapshot.attended && snapshot.attended.role === 'waiting_interleave' && snapshot.attended.primary_session_id === primary.id && !snapshot.attended.ended_at
        ? snapshot.attended
        : undefined;
      return {
        kind: 'waiting',
        primary,
        task,
        waitingNote: waiting?.note ?? '',
        ready: last?.event_type === 'external_ready',
        interleave,
        interleaveTask: interleave ? taskFor(interleave, snapshot) : undefined
      };
    }
    return { kind: 'active', primary, task };
  }
  if (!snapshot.plan) return { kind: 'plan_your_day', day: snapshot.day, planGateEnabled: snapshot.planGateEnabled };
  const choice = currentBlock(snapshot.plan, now);
  if (choice.block?.activity_id && !choice.ambiguous) {
    const activity = snapshot.activities.find(row => row.id === choice.block?.activity_id);
    return {
      kind: 'ready',
      block: choice.block,
      activityId: choice.block.activity_id,
      upcoming: choice.upcoming,
      task: {
        title: activity?.title ?? choice.block.title,
        nextAction: activity?.next_action ?? '',
        resumeMarker: activity?.resume_marker ?? '',
        block: choice.block
      }
    };
  }
  const open = sortBlocks(snapshot.plan.blocks).filter(block => openBlockStates.includes(block.state));
  return {
    kind: 'choose',
    startable: open.filter(block => block.activity_id),
    unlinked: open.filter(block => !block.activity_id)
  };
}

export type NowAction =
  | { type: 'start'; block: PlanBlock; activityId: number }
  | { type: 'waiting'; note: string }
  | { type: 'stuck_unknown' }
  | { type: 'stuck_overwhelm' }
  | { type: 'switch'; resumeMarker: string }
  | { type: 'done' }
  | { type: 'recovery_start'; overwhelmed: boolean }
  | { type: 'recovery_end' }
  | { type: 'resume' };

export type ExecutionAPI = {
  startExecutionSession: (input: ExecutionSessionInput) => Promise<ExecutionSession>;
  recordExecutionEvent: (sessionId: number, input: ExecutionEventInput) => Promise<ExecutionSession>;
  returnToPrimary: (sessionId: number) => Promise<ExecutionReturn>;
  setPlanBlockState: (planDate: string, blockId: number, state: PlanBlock['state'], note?: string) => Promise<PlanBlock>;
};

// Sends an owner action to the authoritative API. Nothing is applied locally:
// the caller reloads the snapshot afterwards, whether the call failed or not,
// so a rejected transition leaves the previously accepted state on screen.
export async function performNowAction(api: ExecutionAPI, view: NowView, day: string, action: NowAction): Promise<void> {
  const primary = 'primary' in view ? view.primary : undefined;
  const event = (type: string, extra: Omit<ExecutionEventInput, 'event_type'> = {}) => {
    if (!primary) throw new Error('No execution session is current.');
    return api.recordExecutionEvent(primary.id, { event_type: type, ...extra });
  };
  switch (action.type) {
    case 'start':
      await api.startExecutionSession({ activity_id: action.activityId, plan_block_id: action.block.id });
      if (action.block.state === 'planned') await api.setPlanBlockState(day, action.block.id, 'active');
      return;
    case 'waiting':
      await event('waiting_external', action.note.trim() ? { note: action.note.trim() } : {});
      return;
    case 'stuck_unknown':
      await event('stuck_unknown_next_action');
      return;
    case 'stuck_overwhelm':
      await event('stuck_emotional_overwhelm');
      return;
    case 'switch':
      // Interrupted sessions already retain their marker; only live work is switched out.
      if (view.kind === 'active' || (view.kind === 'waiting' && !view.interleave)) {
        await event('switched_out', { resume_marker: action.resumeMarker.trim() });
      }
      return;
    case 'done': {
      const block = view.kind === 'active' ? view.task.block : undefined;
      await event('completed');
      if (block && openBlockStates.includes(block.state)) await api.setPlanBlockState(day, block.id, 'done');
      return;
    }
    case 'recovery_start':
      if (action.overwhelmed) await event('overwhelmed');
      await event('offline_recovery_started');
      return;
    case 'recovery_end':
      await event('offline_recovery_ended');
      await event('resumed');
      return;
    case 'resume':
      if (view.kind === 'waiting' && view.interleave) {
        await api.returnToPrimary(view.interleave.id);
        return;
      }
      await event('resumed');
      return;
  }
}

export const executionStateLabel: Record<NowView['kind'], string> = {
  plan_your_day: 'No plan',
  ready: 'Not started',
  choose: 'No current block',
  active: 'Working',
  waiting: 'Waiting',
  interrupted: 'Paused',
  recovery: 'Offline recovery'
};
