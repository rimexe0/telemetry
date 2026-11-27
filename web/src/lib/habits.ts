import type { Habit, HabitDayStatus, HabitRule } from './api';

// Habits and their heatmap. Days are budget-day keys, YYYY-MM-DD.

/** Weekdays in the order the panel shows them, numbered as Go does (0 is Sunday). */
export const weekdays = [
  { day: 1, short: 'Mon', long: 'Monday' }, { day: 2, short: 'Tue', long: 'Tuesday' }, { day: 3, short: 'Wed', long: 'Wednesday' },
  { day: 4, short: 'Thu', long: 'Thursday' }, { day: 5, short: 'Fri', long: 'Friday' }, { day: 6, short: 'Sat', long: 'Saturday' },
  { day: 0, short: 'Sun', long: 'Sunday' }
];

/** The weekdays to store: every day, all seven or none, is stored as none. */
export function normalizeWeekdays(selected: number[]): number[] {
  const unique = [...new Set(selected.filter(day => Number.isInteger(day) && day >= 0 && day <= 6))];
  if (unique.length === 0 || unique.length === 7) return [];
  return weekdays.map(item => item.day).filter(day => unique.includes(day));
}

/** "Every day", or "Mon, Wed, Fri". */
export function weekdaysText(days: number[] | null | undefined): string {
  const normal = normalizeWeekdays(days ?? []);
  if (!normal.length) return 'Every day';
  return normal.map(day => weekdays.find(item => item.day === day)?.short).join(', ');
}

/** "Go to the pier · within 100 m for 1 min · from waking until 11:00", or "" without a rule. */
export function ruleText(rule: HabitRule | null | undefined): string {
  if (!rule || rule.kind !== 'place' || !rule.place) return '';
  const minutes = rule.minutes ?? 1;
  const from = rule.window.from === 'wake' ? 'from waking' : `from ${rule.window.from}`;
  return `Go to ${rule.place.name} · within ${rule.radius_m ?? 100} m for ${minutes} min · ${from} until ${rule.window.until}`;
}

export const isArchived = (habit: Habit) => !!habit.archived || !!habit.archived_at;

/** Active habits in their order, then archived ones. */
export function sortHabits(habits: Habit[]): { active: Habit[]; archived: Habit[] } {
  const ordered = [...habits].sort((a, b) => a.position - b.position || a.id - b.id);
  return { active: ordered.filter(habit => !isArchived(habit)), archived: ordered.filter(isArchived) };
}

/**
 * Moves one active habit up (-1) or down (+1). Returns the position changes
 * that give the new order, numbering every active habit from 0, or none when
 * it is already at that end.
 */
export function moveHabit(active: Habit[], id: number, delta: -1 | 1): { id: number; position: number }[] {
  const from = active.findIndex(habit => habit.id === id);
  const to = from + delta;
  if (from < 0 || to < 0 || to >= active.length) return [];
  const order = [...active];
  [order[from], order[to]] = [order[to], order[from]];
  return order.map((habit, position) => ({ id: habit.id, position })).filter(({ id, position }) => active.find(habit => habit.id === id)?.position !== position);
}

const addDays = (day: string, delta: number) => {
  const at = new Date(`${day}T12:00:00Z`);
  at.setUTCDate(at.getUTCDate() + delta);
  return at.toISOString().slice(0, 10);
};
const weekdayOf = (day: string) => new Date(`${day}T12:00:00Z`).getUTCDay();
const monthNames = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const monthLabel = (day: string) => monthNames[Number(day.slice(5, 7)) - 1];

/** A day as "Tue 3 Nov 2026". */
export function longDay(day: string): string {
  return new Date(`${day}T12:00:00Z`).toLocaleDateString('en-GB', { weekday: 'short', day: 'numeric', month: 'short', year: 'numeric', timeZone: 'UTC' });
}

export type HeatmapLayout = {
  /** Columns of seven days, Monday first; null where the range does not reach. */
  weeks: (string | null)[][];
  /** Month names over the first column that holds a day of that month. */
  months: { label: string; column: number }[];
};

/** The GitHub-style grid for `count` days ending on `end`, weeks as columns. */
export function heatmapLayout(end: string, count: number): HeatmapLayout {
  const start = addDays(end, -(Math.max(1, count) - 1));
  const lead = (weekdayOf(start) + 6) % 7;
  const weeks: (string | null)[][] = [];
  let week: (string | null)[] = Array(lead).fill(null);
  for (let day = start; day <= end; day = addDays(day, 1)) {
    week.push(day);
    if (week.length === 7) { weeks.push(week); week = []; }
  }
  if (week.length) weeks.push([...week, ...Array(7 - week.length).fill(null)]);
  const months: HeatmapLayout['months'] = [];
  let last = '';
  weeks.forEach((column, index) => {
    const first = column.find((day): day is string => day !== null);
    const month = first?.slice(0, 7) ?? '';
    if (first && month !== last) {
      // A label needs about three columns; a part month at the very start
      // gives way to the next one.
      if (months.length && index - months[months.length - 1].column < 3) months.pop();
      months.push({ label: monthLabel(first), column: index });
      last = month;
    }
  });
  return { weeks, months };
}

export type CellState = HabitDayStatus | 'open' | 'unknown';

/**
 * How one habit's day is drawn. Today is still open until the day closes, so
 * a habit not yet done shows as open, not missed. A day the server has no
 * record for is unknown, never missed.
 */
export function cellState(status: HabitDayStatus | undefined, day: string, openDay: string): CellState {
  if (day === openDay && status !== 'done' && status !== 'unscheduled' && status !== 'none') return 'open';
  return status ?? 'unknown';
}

const stateWords: Record<CellState, string> = {
  done: 'done', missed: 'not done', unscheduled: 'not scheduled', none: 'before the habit existed', open: 'open today', unknown: 'no record'
};

/** The cell's title and label: "Walk, Tue 3 Nov 2026: done". */
export function cellTitle(name: string, day: string, state: CellState): string {
  return `${name}, ${longDay(day)}: ${stateWords[state]}`;
}

/** The shade of a combined cell, 0 to 4, or -1 without a record. */
export function combinedLevel(share: number | null | undefined): number {
  if (share === null || share === undefined || !Number.isFinite(share)) return -1;
  if (share <= 0) return 0;
  if (share >= 1) return 4;
  return Math.min(3, Math.ceil(share * 3));
}

export function combinedTitle(day: string, share: number | null | undefined): string {
  if (share === null || share === undefined || !Number.isFinite(share)) return `All habits, ${longDay(day)}: no record`;
  return `All habits, ${longDay(day)}: ${Math.round(share * 100)}% done`;
}

const days = (count: number) => `${count} ${count === 1 ? 'day' : 'days'}`;

/**
 * The runs of days in a row, as facts. Days a habit is not scheduled do not
 * end a run (the server counts them). Without a current run only the longest
 * is shown; nothing ever says a run ended.
 */
export function runsText(current: number, longest: number): string {
  const best = Math.max(current, longest);
  if (best <= 0) return 'No runs yet';
  if (current > 0) return current >= best ? `${days(current)} in a row, the longest so far` : `${days(current)} in a row · longest ${days(best)}`;
  return `Longest run ${days(best)}`;
}

export type WeekCell = {
  day: string;
  state: CellState;
  /** The weekday's letter, M T W T F S S. */
  letter: string;
  today: boolean;
  title: string;
};

const weekdayLetters = ['S', 'M', 'T', 'W', 'T', 'F', 'S'];

/**
 * The last `count` days of one habit, oldest first, ending on today's budget
 * day, for the small strip under each habit on Today. Today follows the
 * budget's own tick, which is newer than the heatmap: done once ticked, open
 * until then, unless the habit is not scheduled or did not exist yet. Earlier
 * days are what the server recorded; a day without a record stays unknown,
 * never missed.
 */
export function weekStrip(name: string, days: Record<string, HabitDayStatus> | null | undefined, today: string, todayDone: boolean, count = 7): WeekCell[] {
  const cells: WeekCell[] = [];
  for (let back = Math.max(1, count) - 1; back >= 0; back--) {
    const day = addDays(today, -back);
    const recorded = days?.[day];
    let state: CellState;
    if (back === 0) state = todayDone ? 'done' : recorded === 'unscheduled' || recorded === 'none' ? recorded : 'open';
    else state = recorded ?? 'unknown';
    cells.push({ day, state, letter: weekdayLetters[weekdayOf(day)], today: back === 0, title: cellTitle(name, day, state) });
  }
  return cells;
}
