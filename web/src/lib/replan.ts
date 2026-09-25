import type { PlanChange } from './api';
import { clockLabel } from './plan';

// Plan times are entered as Istanbul wall-clock times on the plan's day.
export const dayTime = (day: string, hhmm: string) => `${day}T${hhmm}:00+03:00`;

const span = (start?: string, minutes?: number) => (start && minutes ? `${clockLabel(start)} · ${minutes} min` : '');

export const kindLabel: Record<PlanChange['kind'], string> = {
  unchanged: 'Unchanged',
  moved: 'Moved',
  resized: 'Resized',
  parked: 'Parked',
  added: 'Added',
  fixed_conflict: 'Fixed conflict'
};

export function describeChange(change: PlanChange): string {
  const before = span(change.old_start, change.old_duration_minutes);
  const after = span(change.new_start, change.new_duration_minutes);
  switch (change.kind) {
    case 'moved':
    case 'resized':
      return `${before} → ${after}`;
    case 'parked':
      return `${before} → not scheduled`;
    case 'added':
      return after;
    case 'fixed_conflict':
      return after || before;
    default:
      return before;
  }
}

// Unchanged blocks are hidden by default so the review shows only what changes.
export const visibleChanges = (changes: PlanChange[], showUnchanged: boolean) =>
  changes.filter(change => showUnchanged || change.kind !== 'unchanged');
