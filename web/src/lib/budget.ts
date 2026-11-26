import type { BudgetDay, BudgetRate, BudgetSettings, BudgetState, Door, PolicyConfig } from './api';

// The words and numbers of the distraction budget and the door. They state
// facts and never scold (executive-function principles 6 and 14).

/** Budget minutes as "1 h 40 min", "10 min" or "under 1 min". */
export function formatBudgetMinutes(minutes: number | null | undefined): string {
  if (minutes === null || minutes === undefined || !Number.isFinite(minutes)) return 'unknown';
  const sign = minutes < 0 ? '−' : '';
  const size = Math.abs(minutes);
  if (size > 0 && size < 0.5) return `${sign}under 1 min`;
  const whole = Math.round(size);
  if (whole < 60) return `${sign}${whole} min`;
  const hours = Math.floor(whole / 60);
  const rest = whole % 60;
  return `${sign}${hours} h${rest ? ` ${rest} min` : ''}`;
}

/** A rate as "3×" or "1.5×". */
export function formatRate(rate: number | null | undefined): string {
  if (rate === null || rate === undefined || !Number.isFinite(rate)) return '?×';
  return `${Number(rate.toFixed(2))}×`;
}

const kindLabels: Record<string, string> = { distraction: 'distraction time', day: 'daytime', bedtime: 'bedtime' };

/** What the rate is called by the time it applies to. */
export function rateKindLabel(kind: string): string {
  return kindLabels[kind] ?? kind;
}

/** "3× now, daytime", or with the grayscale surcharge, "13× now: 10× bedtime + 3× grayscale kept off". */
export function rateText(rate: BudgetRate): string {
  if (rate.grayscale_off > 0) {
    return `${formatRate(rate.total)} now: ${formatRate(rate.base)} ${rateKindLabel(rate.kind)} + ${formatRate(rate.grayscale_off)} grayscale kept off`;
  }
  return `${formatRate(rate.total)} now, ${rateKindLabel(rate.kind)}`;
}

/**
 * What a door costs, as the door screen says it: "3× now. 10 min = 30 min of
 * budget. Left today: 1 h 40 min." Once today is used up, the cost is in
 * tomorrow's budget.
 */
export function doorCostLines(state: BudgetState): string[] {
  const rate = state.rate.total;
  const use = state.settings.window_use_minutes;
  const multiplier = state.settings.borrow_multiplier;
  const lines: string[] = [];
  if (state.left > 0) {
    lines.push(`${formatRate(rate)} now. ${use} min = ${formatBudgetMinutes(use * rate)} of budget. Left today: ${formatBudgetMinutes(state.left)}.`);
    if (use * rate > state.left) lines.push(`Past that, borrowing: 1 min = ${formatBudgetMinutes(rate * multiplier)} of tomorrow's budget.`);
  } else {
    lines.push(`${formatRate(rate)} now. Borrowing: ${use} min = ${formatBudgetMinutes(use * rate * multiplier)} of tomorrow's budget.`);
    lines.push(`Tomorrow can still lend ${formatBudgetMinutes(state.borrowable)}.`);
  }
  if (state.rate.grayscale_off > 0) lines.push(`The rate includes ${formatRate(state.rate.grayscale_off)} while grayscale is kept off.`);
  return lines;
}

const clock = (at: Date, timeZone: string) => at.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit', hour12: false, timeZone });

function countdown(ms: number): string {
  const seconds = Math.max(0, Math.ceil(ms / 1000));
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`;
}

/** "Bedtime in 25 min" during the wind-down, "It's bedtime (until 08:00)." during bedtime, else nothing. */
export function bedtimeLine(state: BudgetState, now: Date, bedtimeEnd = ''): string {
  if (state.rate.kind === 'bedtime') return bedtimeEnd ? `It's bedtime (until ${bedtimeEnd}).` : "It's bedtime.";
  if (state.wind_down && state.bedtime_at) {
    const minutes = Math.max(0, Math.ceil((Date.parse(state.bedtime_at) - now.getTime()) / 60_000));
    return `Bedtime in ${minutes} min`;
  }
  return '';
}

export type DoorPhase = 'waiting' | 'open' | 'grace' | 'ended';

/** Where a door is at an instant, in the words the panel shows. */
export function doorStatus(door: Door, now: Date, timeZone = 'Europe/Istanbul'): { phase: DoorPhase; text: string } {
  if (door.state === 'cancelled') return { phase: 'ended', text: 'Cancelled' };
  const ends = door.ends_at ? new Date(door.ends_at) : null;
  if (door.state === 'ended' || (ends && now >= ends && door.state !== 'waiting')) return { phase: 'ended', text: 'Ended' };
  if (door.state === 'waiting') {
    const wait = Date.parse(door.opens_at) - now.getTime();
    return { phase: 'waiting', text: wait > 0 ? `Opens in ${countdown(wait)}` : 'Opening…' };
  }
  if (door.state === 'grace') {
    const left = ends ? Math.max(1, Math.ceil((ends.getTime() - now.getTime()) / 60_000)) : 1;
    return { phase: 'grace', text: `${left} min to close or save` };
  }
  const until = ends ? `, until ${clock(ends, timeZone)}` : '';
  if (door.use_minutes !== null && door.use_minutes !== undefined) {
    return { phase: 'open', text: `${Math.floor(door.used_minutes)} of ${door.use_minutes} min used${until}` };
  }
  return { phase: 'open', text: `Open${door.borrowing ? ', borrowing' : ''}${until}` };
}

/** Whether a door still needs the panel's attention: waiting or open. */
export function doorLive(door: Door | null | undefined, now: Date): door is Door {
  return !!door && doorStatus(door, now).phase !== 'ended';
}

/** The budget settings in force now: a waiting looser one once its time has come. */
export function budgetInForce(config: PolicyConfig, now: Date, timeZone = config.timezone || 'Europe/Istanbul'): BudgetSettings | undefined {
  const next = config.budget_next;
  const from = config.budget_next_from;
  if (next && from && started(from, now, timeZone)) return next;
  return config.budget;
}

function started(from: string, now: Date, timeZone: string): boolean {
  if (from.includes('T')) {
    const at = Date.parse(from);
    return Number.isFinite(at) && at <= now.getTime();
  }
  const today = now.toLocaleDateString('en-CA', { timeZone });
  return from <= today;
}

/** The budget settings an owner starts from when none are set: off, every number empty. */
export const emptyBudget: BudgetSettings = {
  enabled: false, base_minutes: 0, max_minutes: 0, floor_minutes: 0, habits_for_max: 0,
  rate_distraction: 0, rate_day: 0, rate_bedtime: 0, rate_grayscale_off: 0, borrow_multiplier: 0,
  window_minutes: 0, window_use_minutes: 0, grace_minutes: 0, wait_minutes: 0
};

/**
 * Whether a change asks less than the settings in force, so it waits for the
 * next day and opens a loophole case. The list is the contract's.
 */
export function budgetAsksLess(current: BudgetSettings | undefined, next: BudgetSettings): boolean {
  if (!current?.enabled) return false;
  if (!next.enabled) return true;
  const higher = (['base_minutes', 'max_minutes', 'floor_minutes', 'window_use_minutes', 'grace_minutes'] as const)
    .some(key => next[key] > current[key]);
  const lower = (['habits_for_max', 'rate_distraction', 'rate_day', 'rate_bedtime', 'rate_grayscale_off', 'borrow_multiplier', 'wait_minutes', 'window_minutes'] as const)
    .some(key => next[key] < current[key]);
  return higher || lower;
}

/** The settings in one line, as the rules card shows them. */
export function budgetSummary(settings: BudgetSettings | undefined): string {
  if (!settings?.enabled) return 'Off.';
  const s = settings;
  return [
    `On: ${formatBudgetMinutes(s.base_minutes)} a day, up to ${formatBudgetMinutes(s.max_minutes)} with ${s.habits_for_max} habits, never under ${formatBudgetMinutes(s.floor_minutes)}.`,
    `${formatRate(s.rate_distraction)} in distraction times, ${formatRate(s.rate_day)} in the daytime, ${formatRate(s.rate_bedtime)} in bedtime, +${formatRate(s.rate_grayscale_off)} with grayscale kept off.`,
    `Borrowing costs tomorrow ${formatRate(s.borrow_multiplier)}.`,
    `A door waits ${s.wait_minutes} min, then gives ${s.window_use_minutes} min of use${s.grace_minutes ? ` and ${s.grace_minutes} min to close` : ''} in ${s.window_minutes} min.`
  ].join(' ');
}

export type BudgetHistoryRow = { day: string; record?: BudgetDay };

const addDays = (day: string, delta: number) => {
  const at = new Date(`${day}T12:00:00Z`);
  at.setUTCDate(at.getUTCDate() + delta);
  return at.toISOString().slice(0, 10);
};

/**
 * The closed days before today, newest first, `count` of them. A day without
 * a snapshot stays in the list without a record: missing data is missing.
 */
export function budgetHistory(rows: BudgetDay[], today: string, count = 30): BudgetHistoryRow[] {
  const byDay = new Map(rows.map(row => [row.day, row]));
  const days: BudgetHistoryRow[] = [];
  for (let back = 1; back <= count; back++) {
    const day = addDays(today, -back);
    days.push({ day, record: byDay.get(day) });
  }
  return days;
}

/** "3 of 5" habits done on a closed day, or "none scheduled". */
export function habitsDoneText(record: BudgetDay): string {
  const scheduled = record.habits_done + record.habits_missed;
  return scheduled ? `${record.habits_done} of ${scheduled}` : 'none scheduled';
}
