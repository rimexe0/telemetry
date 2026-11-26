import type { BudgetState, CollectionIncident, FollowUp, Tracking } from './api';
import { formatBudgetMinutes, formatRate } from './budget';
import { dayKey, dayStart } from './health';

// The words on Today. They state facts and the way back, never scold, and
// say "unknown" where nothing was recorded (executive-function principles 6
// and 14).

/** A moment as 14:05, in the owner's time zone. */
export function clock(at: string | number | null | undefined, timeZone = 'Europe/Istanbul'): string {
  if (at === null || at === undefined || at === '') return '';
  const time = new Date(at);
  if (Number.isNaN(time.getTime())) return '';
  return time.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit', hour12: false, timeZone });
}

/**
 * The open follow-ups Today shows: the due ones, then the ones still to come
 * today, each soonest first. Ones due on a later day wait until that day.
 */
export function todaysFollowUps(items: FollowUp[], now: number): { due: FollowUp[]; later: FollowUp[] } {
  const endOfDay = dayStart(dayKey(now)) + 86_400_000;
  const open = items
    .filter(item => item.state === 'open' || !item.state)
    .map(item => ({ item, at: Date.parse(item.due_at) }))
    .filter(entry => Number.isFinite(entry.at))
    .sort((a, b) => a.at - b.at || a.item.id - b.item.id);
  return {
    due: open.filter(entry => entry.at <= now).map(entry => entry.item),
    later: open.filter(entry => entry.at > now && entry.at < endOfDay).map(entry => entry.item)
  };
}

export type TrackingView = { line: string; since: string; notes: string[] };

/** What the server reads at the Mac right now, in a line and a few notes. */
export function trackingView(tracking: Tracking | null | undefined): TrackingView | null {
  if (!tracking) return null;
  const since = clock(tracking.since);
  let line: string;
  switch (tracking.state) {
    case 'task':
      line = tracking.task ? `On ${tracking.task}` : 'On an open task';
      break;
    case 'distraction':
      line = tracking.context ? `On a distraction: ${tracking.context}` : 'On a distraction';
      break;
    case 'other':
      line = tracking.context ? `On ${tracking.context}` : 'On something outside the open tasks';
      break;
    case 'away':
      line = 'Nothing recent from the Mac';
      break;
    default:
      line = 'Unknown';
  }
  if (tracking.excused) line += ' (a one-off you mentioned)';
  const notes: string[] = [];
  const focus = tracking.focus;
  if (focus) {
    const stand = focus.on === 'on' ? 'on it' : focus.on === 'away' ? 'away from the Mac' : focus.on === 'off' ? `away from it${focus.off_since ? ` since ${clock(focus.off_since)}` : ''}` : 'how it stands is unknown';
    notes.push(`Focus: ${focus.task}, ${stand}.${focus.next_action ? ` Next: ${focus.next_action}` : ''}`);
  }
  for (const turn of tracking.your_turn ?? []) notes.push(`Your turn on ${turn.task}: its agent stopped at ${clock(turn.since)}.`);
  if (tracking.agent_running) notes.push('An agent is at work.');
  return { line, since: tracking.state === 'away' ? '' : since, notes };
}

/** One collection problem in a line, as the phone words it. */
export function incidentText(incident: CollectionIncident): string {
  const detail = incident.detail ?? {};
  const name = String(detail.device_name ?? '').replace(/\.local$/, '') || incident.device;
  const time = clock(incident.started_at);
  const kind = incident.kind;
  if (kind === 'stopped_while_on') return `Telemetry is off on ${name} since ${time}`;
  if (kind === 'silent') return `${name}: no data since ${time}`;
  if (kind === 'titles_unreadable') return `${name} can't read window titles`;
  if (kind === 'window_sampling_stopped') return `${name} stopped recording windows at ${time}`;
  if (kind.startsWith('module_failing:')) return `${name}: ${kind.slice('module_failing:'.length)} collection failing since ${time}`;
  const message = typeof detail.message === 'string' ? detail.message : '';
  return message ? `${name}: ${message}` : `${name}: collection problem since ${time}`;
}

/**
 * The muted line under the time left: the rate now, the next distraction
 * time with its rate, and what habits earned. "3× now · 1× from 19:00 · +30 min earned".
 */
export function budgetLine(state: BudgetState, distractionRate?: number): string {
  const parts: string[] = [];
  const rate = state.rate;
  parts.push(rate.grayscale_off > 0 ? `${formatRate(rate.total)} now (${formatRate(rate.grayscale_off)} with grayscale kept off)` : `${formatRate(rate.total)} now`);
  if (rate.kind === 'bedtime') parts.push('bedtime');
  else if (state.in_distraction_time) parts.push('distraction time');
  else if (state.next_distraction_time) {
    const at = clock(state.next_distraction_time);
    parts.push(distractionRate !== undefined && distractionRate > 0 ? `${formatRate(distractionRate)} from ${at}` : `distraction time from ${at}`);
  }
  if (state.earned > 0) parts.push(`+${formatBudgetMinutes(state.earned)} earned`);
  return parts.join(' · ');
}

/** How much of the day's budget is left, 0 to 1, for the bar. */
export function leftShare(state: Pick<BudgetState, 'left' | 'budget'>): number {
  if (!Number.isFinite(state.left) || !Number.isFinite(state.budget) || state.budget <= 0) return 0;
  return Math.min(1, Math.max(0, state.left / state.budget));
}

/** "+30": what a habit is worth today, in whole budget minutes. */
export function habitWorth(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '';
  const rounded = Math.round(value);
  return rounded >= 1 ? `+${rounded}` : '+<1';
}
