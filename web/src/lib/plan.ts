import type { Activity, PlanBlock } from './api';

export const activityStatuses = ['active', 'waiting', 'blocked', 'completed', 'abandoned', 'unknown'] as const;
export const activityStates = ['growth', 'maintenance', 'parked'] as const;
export const blockStates = ['planned', 'active', 'done', 'skipped', 'switched', 'blocked', 'unknown'] as const;

export const statusLabel: Record<Activity['status'], string> = {
  active: 'Active',
  waiting: 'Waiting',
  blocked: 'Blocked',
  done: 'Completed',
  completed: 'Completed',
  abandoned: 'Abandoned',
  unknown: 'Unknown'
};

export const activityStateLabel: Record<Activity['activity_state'], string> = {
  growth: 'Growth',
  maintenance: 'Maintenance',
  parked: 'Parked'
};

export const stateLabel: Record<PlanBlock['state'], string> = {
  planned: 'Planned',
  active: 'In progress',
  done: 'Completed',
  skipped: 'Skipped',
  switched: 'Switched',
  blocked: 'Blocked',
  unknown: 'Unknown'
};

// Istanbul is the owner's local day; the panel keeps one day-key convention.
export const toBlockLocalTime = (value: string | number) => new Date(new Date(value).getTime() + 3 * 3_600_000);

export const clockLabel = (value: string) => toBlockLocalTime(value).toISOString().slice(11, 16);

// Timestamps arrive with mixed offsets (drafts send +03:00, the API returns
// UTC), so ordering and overlap checks must compare parsed times.
export const sortBlocks = (blocks: PlanBlock[]) =>
  [...blocks].sort((a, b) => Date.parse(a.start_time) - Date.parse(b.start_time) || a.position - b.position || a.id - b.id);

export const blockEnd = (block: PlanBlock) =>
  new Date(new Date(block.start_time).getTime() + block.duration_minutes * 60_000).toISOString();

export type PlanTotals = { plannedMinutes: number; doneMinutes: number; otherMinutes: number; doneBlocks: number; totalBlocks: number };

export const planTotals = (blocks: PlanBlock[]): PlanTotals => {
  const totals: PlanTotals = { plannedMinutes: 0, doneMinutes: 0, otherMinutes: 0, doneBlocks: 0, totalBlocks: blocks.length };
  for (const block of blocks) {
    if (block.state === 'done') {
      totals.doneMinutes += block.duration_minutes;
      totals.doneBlocks += 1;
    } else if (block.state === 'planned' || block.state === 'active') {
      totals.plannedMinutes += block.duration_minutes;
    } else {
      totals.otherMinutes += block.duration_minutes;
    }
  }
  return totals;
};

// Overlapping blocks are allowed on purpose (approved interleaving is a real
// pattern), but two *fixed* commitments overlapping is a planning mistake the
// editor should catch before saving.
export const fixedOverlaps = (blocks: PlanBlock[]): [PlanBlock, PlanBlock][] => {
  const fixed = sortBlocks(blocks).filter(block => block.fixed).map(block => ({ block, end: Date.parse(blockEnd(block)) }));
  const overlaps: [PlanBlock, PlanBlock][] = [];
  for (let i = 0; i < fixed.length; i++) {
    for (let j = i + 1; j < fixed.length; j++) {
      if (Date.parse(fixed[j].block.start_time) >= fixed[i].end) break;
      overlaps.push([fixed[i].block, fixed[j].block]);
    }
  }
  return overlaps;
};

export const draftKey = (block: Omit<PlanBlock, 'id' | 'state' | 'state_note' | 'position'>) =>
  `${block.activity_id ?? ''}|${block.title}|${block.start_time}|${block.duration_minutes}|${block.fixed ? 1 : 0}`;

/** The day `offset` days from `day`, both as YYYY-MM-DD. */
export const shiftDay = (day: string, offset: number) => {
  const date = new Date(`${day}T12:00:00Z`);
  date.setUTCDate(date.getUTCDate() + offset);
  return date.toISOString().slice(0, 10);
};

/** "Sat 31 Oct", the way the plan header names a day. */
export const dayLabel = (day: string) =>
  new Date(`${day}T12:00:00Z`).toLocaleDateString('en-GB', { weekday: 'short', day: 'numeric', month: 'short', timeZone: 'UTC' });

/**
 * The one action a block shows by its title; the rest sit under "More".
 * Starting comes first for a block not yet begun, finishing for one under way.
 */
export const primaryAction = (state: PlanBlock['state']): { state: PlanBlock['state']; label: string; ret?: boolean } | null => {
  switch (state) {
    case 'planned': return { state: 'active', label: 'Start' };
    case 'active': return { state: 'done', label: 'Done' };
    case 'done': return null;
    default: return { state: 'active', label: 'Return to it', ret: true };
  }
};

/** Whether a block is under way at `now`. */
export const blockNow = (block: PlanBlock, now: number) =>
  Date.parse(block.start_time) <= now && now < Date.parse(blockEnd(block));
