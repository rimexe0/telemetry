import type { Activity, PlanBlock } from './api';

export const activityStatuses = ['active', 'waiting', 'blocked', 'done', 'abandoned'] as const;
export const blockStates = ['planned', 'active', 'done', 'skipped', 'switched'] as const;

export const statusLabel: Record<Activity['status'], string> = {
  active: 'Active',
  waiting: 'Waiting',
  blocked: 'Blocked',
  done: 'Done',
  abandoned: 'Abandoned'
};

export const stateLabel: Record<PlanBlock['state'], string> = {
  planned: 'Planned',
  active: 'In progress',
  done: 'Done',
  skipped: 'Skipped',
  switched: 'Switched'
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
