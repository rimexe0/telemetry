import { describe, expect, it } from 'vitest';
import { dayTime, describeChange, visibleChanges } from './replan';
import type { PlanChange } from './api';

const change = (overrides: Partial<PlanChange>): PlanChange => ({ kind: 'moved', title: 'Write', fixed: false, ...overrides });

describe('replan review helpers', () => {
  it('builds Istanbul wall-clock times for the plan day', () => {
    expect(dayTime('2026-09-25', '09:15')).toBe('2026-09-25T09:15:00+03:00');
  });

  it('describes before and after for moved and parked blocks', () => {
    expect(describeChange(change({ old_start: '2026-09-25T06:00:00Z', old_duration_minutes: 60, new_start: '2026-09-25T08:00:00Z', new_duration_minutes: 60 })))
      .toBe('09:00 · 60 min → 11:00 · 60 min');
    expect(describeChange(change({ kind: 'parked', old_start: '2026-09-25T06:00:00Z', old_duration_minutes: 90 }))).toBe('09:00 · 90 min → not scheduled');
  });

  it('hides unchanged blocks unless asked', () => {
    const changes = [change({ kind: 'unchanged' }), change({ kind: 'parked' })];
    expect(visibleChanges(changes, false).map(c => c.kind)).toEqual(['parked']);
    expect(visibleChanges(changes, true)).toHaveLength(2);
  });
});
