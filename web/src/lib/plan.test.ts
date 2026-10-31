import { describe, expect, it } from 'vitest';
import { clockLabel, fixedOverlaps, planTotals, sortBlocks, stateLabel, toBlockLocalTime } from './plan';
import type { PlanBlock } from './api';

const block = (overrides: Partial<PlanBlock>): PlanBlock => ({
  id: 1, title: 'Practice', start_time: '2026-09-15T06:00:00+03:00', duration_minutes: 50,
  fixed: false, state: 'planned', state_note: '', position: 0, ...overrides
});

describe('planning helpers', () => {
  it('sorts blocks by start time and keeps position as a tiebreak', () => {
    const sorted = sortBlocks([
      block({ id: 2, start_time: '2026-09-15T10:00:00+03:00', position: 1 }),
      block({ id: 1, start_time: '2026-09-15T06:00:00+03:00', position: 1 }),
      block({ id: 3, start_time: '2026-09-15T10:00:00+03:00', position: 0 })
    ]);
    expect(sorted.map(b => b.id)).toEqual([1, 3, 2]);
  });

  it('formats block times in Istanbul local time', () => {
    expect(clockLabel('2026-09-15T06:30:00+03:00')).toBe('06:30');
    expect(clockLabel('2026-09-15T20:30:00Z')).toBe('23:30');
    expect(toBlockLocalTime('2026-09-15T20:30:00Z').getUTCHours()).toBe(23);
  });

  it('totals done, remaining and set-aside minutes', () => {
    const totals = planTotals([
      block({ state: 'done', duration_minutes: 50 }),
      block({ state: 'active', duration_minutes: 20, id: 2 }),
      block({ state: 'planned', duration_minutes: 30, id: 3 }),
      block({ state: 'skipped', duration_minutes: 15, id: 4 }),
      block({ state: 'switched', duration_minutes: 10, id: 5 })
    ]);
    expect(totals).toEqual({ plannedMinutes: 50, doneMinutes: 50, otherMinutes: 25, doneBlocks: 1, totalBlocks: 5 });
  });

  it('flags overlapping fixed blocks only', () => {
    const overlaps = fixedOverlaps([
      block({ id: 1, fixed: true, start_time: '2026-09-15T09:00:00+03:00', duration_minutes: 60 }),
      block({ id: 2, fixed: true, start_time: '2026-09-15T09:30:00+03:00', duration_minutes: 30 }),
      block({ id: 3, fixed: false, start_time: '2026-09-15T09:45:00+03:00', duration_minutes: 30 })
    ]);
    expect(overlaps).toHaveLength(1);
    expect(overlaps[0][0].id).toBe(1);
    expect(overlaps[0][1].id).toBe(2);
  });

  it('labels every state', () => {
    expect(stateLabel.done).toBe('Completed');
    expect(stateLabel.switched).toBe('Switched');
  });
});

import { blockNow, dayLabel, primaryAction, shiftDay } from './plan';

describe('plan page helpers', () => {
  it('moves between days across months', () => {
    expect(shiftDay('2026-10-31', 1)).toBe('2026-11-01');
    expect(shiftDay('2026-11-01', -1)).toBe('2026-10-31');
    expect(dayLabel('2026-10-31')).toBe('Sat 31 Oct');
  });

  it('offers one main action by state', () => {
    expect(primaryAction('planned')).toEqual({ state: 'active', label: 'Start' });
    expect(primaryAction('active')).toEqual({ state: 'done', label: 'Done' });
    expect(primaryAction('done')).toBeNull();
    expect(primaryAction('switched')).toEqual({ state: 'active', label: 'Return to it', ret: true });
  });

  it('knows the block under way', () => {
    const block = { id: 1, title: 'x', start_time: '2026-10-31T09:00:00+03:00', duration_minutes: 60, fixed: false, state: 'planned' as const, state_note: '', position: 0 };
    expect(blockNow(block, Date.parse('2026-10-31T09:30:00+03:00'))).toBe(true);
    expect(blockNow(block, Date.parse('2026-10-31T10:00:00+03:00'))).toBe(false);
  });
});
