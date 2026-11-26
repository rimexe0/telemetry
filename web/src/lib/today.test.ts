import { describe, expect, it } from 'vitest';
import type { BudgetState, FollowUp } from './api';
import { budgetLine, habitWorth, incidentText, leftShare, todaysFollowUps, trackingView } from './today';

// 2026-11-26 12:00 in Istanbul (UTC+3) is 09:00 UTC.
const now = Date.parse('2026-11-26T09:00:00Z');
const follow = (id: number, due_at: string, state = 'open'): FollowUp => ({ id, title: `Follow-up ${id}`, due_at, state });

describe('todaysFollowUps', () => {
  it('splits due ones from the rest of today, soonest first, and leaves later days out', () => {
    const { due, later } = todaysFollowUps([
      follow(1, '2026-11-26T08:30:00Z'),
      follow(2, '2026-11-25T18:00:00Z'),
      follow(3, '2026-11-26T15:00:00Z'),
      follow(4, '2026-11-26T22:30:00Z'), // 01:30 on the 27th in Istanbul
      follow(5, '2026-11-26T08:00:00Z', 'done'),
      follow(6, 'not a time')
    ], now);
    expect(due.map(item => item.id)).toEqual([2, 1]);
    expect(later.map(item => item.id)).toEqual([3]);
  });

  it('is empty when nothing is open', () => {
    expect(todaysFollowUps([], now)).toEqual({ due: [], later: [] });
  });
});

describe('trackingView', () => {
  const base = { agent_running: false, your_turn: [] };

  it('says what is on the Mac and since when', () => {
    expect(trackingView({ ...base, state: 'task', task: 'Write the report', since: '2026-11-26T08:15:00Z' })).toEqual({ line: 'On Write the report', since: '11:15', notes: [] });
    expect(trackingView({ ...base, state: 'distraction', context: 'youtube.com' })?.line).toBe('On a distraction: youtube.com');
    expect(trackingView({ ...base, state: 'other', context: 'Finder' })?.line).toBe('On Finder');
  });

  it('says nothing recent came from the Mac instead of calling it rest', () => {
    expect(trackingView({ ...base, state: 'away', since: '2026-11-26T08:15:00Z' })).toEqual({ line: 'Nothing recent from the Mac', since: '', notes: [] });
  });

  it('notes an agent at work, a turn waiting and a focus session', () => {
    const view = trackingView({
      ...base, state: 'other', agent_running: true,
      your_turn: [{ task_id: 3, task: 'Panel redesign', since: '2026-11-26T08:40:00Z' }],
      focus: { task_id: 3, task: 'Panel redesign', since: '2026-11-26T08:00:00Z', on: 'off', off_since: '2026-11-26T08:50:00Z', next_action: 'Run the tests' }
    });
    expect(view?.notes).toEqual([
      'Focus: Panel redesign, away from it since 11:50. Next: Run the tests',
      'Your turn on Panel redesign: its agent stopped at 11:40.',
      'An agent is at work.'
    ]);
  });

  it('is nothing without a reading', () => {
    expect(trackingView(null)).toBeNull();
  });
});

describe('incidentText', () => {
  const incident = (kind: string, detail: Record<string, unknown> = {}) => ({ id: 1, device: 'mac-1', kind, started_at: '2026-11-26T07:05:00Z', detected_at: '2026-11-26T07:10:00Z', detail });
  it('words each kind as the phone does', () => {
    expect(incidentText(incident('silent', { device_name: 'Studio.local' }))).toBe('Studio: no data since 10:05');
    expect(incidentText(incident('stopped_while_on'))).toBe('Telemetry is off on mac-1 since 10:05');
    expect(incidentText(incident('titles_unreadable'))).toBe("mac-1 can't read window titles");
    expect(incidentText(incident('module_failing:browser'))).toBe('mac-1: browser collection failing since 10:05');
    expect(incidentText(incident('disk_low', { message: 'disk 92% full' }))).toBe('mac-1: disk 92% full');
    expect(incidentText(incident('something_new'))).toBe('mac-1: collection problem since 10:05');
  });
});

describe('budget card', () => {
  const state = (over: Partial<BudgetState> = {}): BudgetState => ({
    enabled: true, day: '2026-11-26', budget: 150, spent: 45, left: 105, earned: 30, penalty: 0, debt: 0, debt_tomorrow: 0, borrowable: 90,
    rate: { total: 3, base: 3, grayscale_off: 0, kind: 'day' }, in_distraction_time: false, next_distraction_time: '2026-11-26T16:00:00Z',
    bedtime_at: '2026-11-26T21:00:00Z', wind_down: false, grayscale_declined: false, door: null, door_available: true, door_unavailable_reason: '',
    habits: [], reasons: [], settings: { wait_minutes: 10, window_minutes: 60, window_use_minutes: 10, grace_minutes: 1, borrow_multiplier: 2 }, ...over
  });

  it('gives the rate now, the next distraction time and what was earned', () => {
    expect(budgetLine(state(), 1)).toBe('3× now · 1× from 19:00 · +30 min earned');
    expect(budgetLine(state())).toBe('3× now · distraction time from 19:00 · +30 min earned');
    expect(budgetLine(state({ earned: 0, in_distraction_time: true, rate: { total: 1, base: 1, grayscale_off: 0, kind: 'distraction' } }), 1)).toBe('1× now · distraction time');
    expect(budgetLine(state({ rate: { total: 13, base: 10, grayscale_off: 3, kind: 'bedtime' } }), 1)).toBe('13× now (3× with grayscale kept off) · bedtime · +30 min earned');
  });

  it('fills the bar with the share left, never past the ends', () => {
    expect(leftShare({ left: 105, budget: 150 })).toBeCloseTo(0.7);
    expect(leftShare({ left: -20, budget: 150 })).toBe(0);
    expect(leftShare({ left: 10, budget: 0 })).toBe(0);
  });

  it('shows what a habit is worth in whole minutes', () => {
    expect(habitWorth(30)).toBe('+30');
    expect(habitWorth(29.6)).toBe('+30');
    expect(habitWorth(0.3)).toBe('+<1');
    expect(habitWorth(0)).toBe('');
  });
});
