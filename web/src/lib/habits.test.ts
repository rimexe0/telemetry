import { describe, expect, it } from 'vitest';
import { cellState, cellTitle, combinedLevel, combinedTitle, heatmapLayout, moveHabit, normalizeWeekdays, runsText, sortHabits, weekdaysText, weekStrip } from './habits';

describe('heatmap layout', () => {
  it('puts days in Monday-first week columns', () => {
    // 2026-11-17 is a Tuesday, 2026-11-26 a Thursday.
    const { weeks, months } = heatmapLayout('2026-11-26', 10);
    expect(weeks).toHaveLength(2);
    expect(weeks[0]).toEqual([null, '2026-11-17', '2026-11-18', '2026-11-19', '2026-11-20', '2026-11-21', '2026-11-22']);
    expect(weeks[1]).toEqual(['2026-11-23', '2026-11-24', '2026-11-25', '2026-11-26', null, null, null]);
    expect(months).toEqual([{ label: 'Nov', column: 0 }]);
  });

  it('lays out a year with one label per month', () => {
    const { weeks, months } = heatmapLayout('2026-11-26', 365);
    expect(weeks).toHaveLength(53);
    expect(weeks.every(week => week.length === 7)).toBe(true);
    expect(weeks.flat().filter(Boolean)).toHaveLength(365);
    expect(weeks.flat().filter(Boolean)[0]).toBe('2025-11-27');
    // The few days of November 2025 give way to December.
    expect(months[0]).toEqual({ label: 'Dec', column: 1 });
    expect(months.map(month => month.label)).toEqual(['Dec', 'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov']);
    for (let i = 1; i < months.length; i++) expect(months[i].column - months[i - 1].column).toBeGreaterThanOrEqual(3);
  });

  it('crosses a year and a leap day', () => {
    const { weeks } = heatmapLayout('2028-03-01', 3);
    expect(weeks.flat().filter(Boolean)).toEqual(['2028-02-28', '2028-02-29', '2028-03-01']);
  });
});

describe('heatmap cells', () => {
  it('shows today as open until it is done, and no record as unknown', () => {
    expect(cellState('missed', '2026-11-26', '2026-11-26')).toBe('open');
    expect(cellState(undefined, '2026-11-26', '2026-11-26')).toBe('open');
    expect(cellState('done', '2026-11-26', '2026-11-26')).toBe('done');
    expect(cellState('unscheduled', '2026-11-26', '2026-11-26')).toBe('unscheduled');
    expect(cellState('missed', '2026-11-25', '2026-11-26')).toBe('missed');
    expect(cellState('none', '2026-11-01', '2026-11-26')).toBe('none');
    expect(cellState(undefined, '2026-11-25', '2026-11-26')).toBe('unknown');
  });

  it('labels every cell in words', () => {
    expect(cellTitle('Walk', '2026-11-03', 'done')).toBe('Walk, Tue, 3 Nov 2026: done');
    expect(cellTitle('Walk', '2026-11-03', 'missed')).toBe('Walk, Tue, 3 Nov 2026: not done');
    expect(cellTitle('Walk', '2026-11-03', 'unknown')).toBe('Walk, Tue, 3 Nov 2026: no record');
    expect(combinedTitle('2026-11-03', 2 / 3)).toBe('All habits, Tue, 3 Nov 2026: 67% done');
    expect(combinedTitle('2026-11-03', null)).toBe('All habits, Tue, 3 Nov 2026: no record');
  });

  it('shades the combined row by the share done', () => {
    expect([undefined, 0, 0.2, 0.5, 0.9, 1].map(combinedLevel)).toEqual([-1, 0, 1, 2, 3, 4]);
  });
});

describe('runs', () => {
  it('states runs as facts and never says one ended', () => {
    expect(runsText(0, 0)).toBe('No runs yet');
    expect(runsText(1, 1)).toBe('1 day in a row, the longest so far');
    expect(runsText(4, 12)).toBe('4 days in a row · longest 12 days');
    expect(runsText(0, 12)).toBe('Longest run 12 days');
    for (const text of [runsText(0, 5), runsText(2, 5)]) expect(text).not.toMatch(/broke|lost|reset|fail/i);
  });
});

describe('habit list', () => {
  const habits = [
    { id: 3, name: 'Read', position: 2 }, { id: 1, name: 'Walk', position: 0 },
    { id: 2, name: 'Stretch', position: 1, archived: true }, { id: 4, name: 'Cook', position: 3 }
  ];

  it('orders active habits and keeps archived ones apart', () => {
    const { active, archived } = sortHabits(habits);
    expect(active.map(habit => habit.name)).toEqual(['Walk', 'Read', 'Cook']);
    expect(archived.map(habit => habit.name)).toEqual(['Stretch']);
  });

  it('moves a habit by renumbering the active ones', () => {
    const { active } = sortHabits(habits);
    expect(moveHabit(active, 4, -1)).toEqual([{ id: 4, position: 1 }]);
    expect(moveHabit(active, 1, 1)).toEqual([{ id: 3, position: 0 }, { id: 1, position: 1 }, { id: 4, position: 2 }]);
    expect(moveHabit(active, 1, -1)).toEqual([]);
    expect(moveHabit(active, 4, 1)).toEqual([]);
  });

  it('stores every day as no weekdays', () => {
    expect(normalizeWeekdays([0, 1, 2, 3, 4, 5, 6])).toEqual([]);
    expect(normalizeWeekdays([])).toEqual([]);
    expect(normalizeWeekdays([0, 5, 1, 1])).toEqual([1, 5, 0]);
    expect(weekdaysText(null)).toBe('Every day');
    expect(weekdaysText([0, 6])).toBe('Sat, Sun');
  });
});

describe('week strip', () => {
  // 2026-11-26 is a Thursday.
  const days = {
    '2026-11-20': 'none', '2026-11-21': 'done', '2026-11-22': 'missed', '2026-11-23': 'unscheduled',
    '2026-11-25': 'done', '2026-11-26': 'missed'
  } as const;

  it('shows the last seven days, oldest first, ending today', () => {
    const cells = weekStrip('Walk', days, '2026-11-26', false);
    expect(cells.map(cell => cell.day)).toEqual(['2026-11-20', '2026-11-21', '2026-11-22', '2026-11-23', '2026-11-24', '2026-11-25', '2026-11-26']);
    expect(cells.map(cell => cell.letter).join('')).toBe('FSSMTWT');
    expect(cells.map(cell => cell.today)).toEqual([false, false, false, false, false, false, true]);
  });

  it('keeps recorded days and leaves a day without a record unknown, never missed', () => {
    const cells = weekStrip('Walk', days, '2026-11-26', false);
    expect(cells.map(cell => cell.state)).toEqual(['none', 'done', 'missed', 'unscheduled', 'unknown', 'done', 'open']);
    expect(cells[4].title).toMatch(/^Walk, Tue,? 24 Nov 2026: no record$/);
  });

  it('shows today as open until ticked, whatever the heatmap said, and done once ticked', () => {
    expect(weekStrip('Walk', days, '2026-11-26', false).at(-1)?.state).toBe('open');
    expect(weekStrip('Walk', days, '2026-11-26', true).at(-1)?.state).toBe('done');
    expect(weekStrip('Walk', { '2026-11-26': 'done' }, '2026-11-26', false).at(-1)?.state).toBe('open');
  });

  it('keeps today unscheduled or before the habit when the server says so', () => {
    expect(weekStrip('Walk', { '2026-11-26': 'unscheduled' }, '2026-11-26', false).at(-1)?.state).toBe('unscheduled');
    expect(weekStrip('Walk', { '2026-11-26': 'none' }, '2026-11-26', false).at(-1)?.state).toBe('none');
  });

  it('works without any heatmap', () => {
    const cells = weekStrip('Walk', null, '2026-11-26', true);
    expect(cells.map(cell => cell.state)).toEqual(['unknown', 'unknown', 'unknown', 'unknown', 'unknown', 'unknown', 'done']);
  });

  it('crosses a month boundary', () => {
    expect(weekStrip('Walk', {}, '2026-12-02', false).map(cell => cell.day)[0]).toBe('2026-11-26');
  });
});
