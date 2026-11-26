import { describe, expect, it } from 'vitest';
import type { BudgetSettings, BudgetState, Door } from './api';
import { bedtimeLine, budgetAsksLess, budgetHistory, budgetInForce, doorCostLines, doorStatus, formatBudgetMinutes, formatRate, habitsDoneText, rateText } from './budget';

// Example numbers, not the owner's records.
const settings: BudgetSettings = {
  enabled: true, base_minutes: 120, max_minutes: 300, floor_minutes: 30, habits_for_max: 6,
  rate_distraction: 1, rate_day: 3, rate_bedtime: 10, rate_grayscale_off: 3, borrow_multiplier: 2,
  window_minutes: 60, window_use_minutes: 10, grace_minutes: 1, wait_minutes: 10
};

const state: BudgetState = {
  enabled: true, day: '2026-11-26', budget: 150, spent: 50, left: 100, earned: 30, penalty: 0, debt: 0,
  debt_tomorrow: 0, borrowable: 90, rate: { total: 3, base: 3, grayscale_off: 0, kind: 'day' },
  in_distraction_time: false, next_distraction_time: '2026-11-26T16:00:00Z', bedtime_at: '2026-11-26T21:00:00Z',
  wind_down: false, grayscale_declined: false, door: null, door_available: true, door_unavailable_reason: '',
  next_door_at: null, habits: [], reasons: [],
  settings: { wait_minutes: 10, window_minutes: 60, window_use_minutes: 10, grace_minutes: 1, borrow_multiplier: 2 }
};

describe('budget minutes and rates', () => {
  it('writes minutes as hours and minutes', () => {
    expect(formatBudgetMinutes(100)).toBe('1 h 40 min');
    expect(formatBudgetMinutes(120)).toBe('2 h');
    expect(formatBudgetMinutes(42.5)).toBe('43 min');
    expect(formatBudgetMinutes(59.6)).toBe('1 h');
    expect(formatBudgetMinutes(0)).toBe('0 min');
    expect(formatBudgetMinutes(0.3)).toBe('under 1 min');
    expect(formatBudgetMinutes(-15)).toBe('−15 min');
    expect(formatBudgetMinutes(undefined)).toBe('unknown');
    expect(formatBudgetMinutes(Number.NaN)).toBe('unknown');
  });

  it('writes rates with their parts', () => {
    expect(formatRate(3)).toBe('3×');
    expect(formatRate(1.5)).toBe('1.5×');
    expect(formatRate(1 / 3)).toBe('0.33×');
    expect(rateText(state.rate)).toBe('3× now, daytime');
    expect(rateText({ total: 13, base: 10, grayscale_off: 3, kind: 'bedtime' })).toBe('13× now: 10× bedtime + 3× grayscale kept off');
    expect(rateText({ total: 1, base: 1, grayscale_off: 0, kind: 'distraction' })).toBe('1× now, distraction time');
  });
});

describe('the door screen', () => {
  it('states the cost against what is left today', () => {
    expect(doorCostLines(state)).toEqual(['3× now. 10 min = 30 min of budget. Left today: 1 h 40 min.']);
    expect(doorCostLines({ ...state, left: 20 })).toEqual([
      '3× now. 10 min = 30 min of budget. Left today: 20 min.',
      "Past that, borrowing: 1 min = 6 min of tomorrow's budget."
    ]);
  });

  it('states the cost in tomorrow\'s budget once today is used up', () => {
    expect(doorCostLines({ ...state, left: 0, rate: { total: 1, base: 1, grayscale_off: 0, kind: 'distraction' } })).toEqual([
      "1× now. Borrowing: 10 min = 20 min of tomorrow's budget.",
      'Tomorrow can still lend 1 h 30 min.'
    ]);
    expect(doorCostLines({ ...state, rate: { total: 13, base: 10, grayscale_off: 3, kind: 'bedtime' } }))
      .toContain('The rate includes 3× while grayscale is kept off.');
  });

  it('says when bedtime comes, without judging', () => {
    const now = new Date('2026-11-26T20:35:00Z');
    expect(bedtimeLine({ ...state, wind_down: true }, now)).toBe('Bedtime in 25 min');
    expect(bedtimeLine({ ...state, rate: { total: 10, base: 10, grayscale_off: 0, kind: 'bedtime' } }, now, '08:00')).toBe("It's bedtime (until 08:00).");
    expect(bedtimeLine(state, now)).toBe('');
  });
});

describe('door status', () => {
  const door: Door = {
    id: 7, state: 'waiting', requested_at: '2026-11-26T10:00:00Z', opens_at: '2026-11-26T10:10:00Z',
    window_end: '2026-11-26T11:10:00Z', ends_at: '2026-11-26T10:21:00Z', used_minutes: 0, use_minutes: 10, borrowing: false
  };

  it('counts down the wait', () => {
    expect(doorStatus(door, new Date('2026-11-26T10:00:30Z'))).toEqual({ phase: 'waiting', text: 'Opens in 9:30' });
    expect(doorStatus(door, new Date('2026-11-26T10:10:05Z'))).toEqual({ phase: 'waiting', text: 'Opening…' });
  });

  it('shows use against the window, then the grace', () => {
    expect(doorStatus({ ...door, state: 'open', used_minutes: 6.7 }, new Date('2026-11-26T10:17:00Z')))
      .toEqual({ phase: 'open', text: '6 of 10 min used, until 13:21' });
    expect(doorStatus({ ...door, state: 'grace', used_minutes: 10 }, new Date('2026-11-26T10:20:10Z')))
      .toEqual({ phase: 'grace', text: '1 min to close or save' });
    expect(doorStatus({ ...door, state: 'open', use_minutes: null, borrowing: true }, new Date('2026-11-26T10:17:00Z')))
      .toEqual({ phase: 'open', text: 'Open, borrowing, until 13:21' });
  });

  it('ends at the sliding end or when cancelled', () => {
    expect(doorStatus({ ...door, state: 'open' }, new Date('2026-11-26T10:21:00Z')).phase).toBe('ended');
    expect(doorStatus({ ...door, state: 'cancelled' }, new Date('2026-11-26T10:01:00Z'))).toEqual({ phase: 'ended', text: 'Cancelled' });
  });
});

describe('budget settings', () => {
  it('treats each looser number as asking less', () => {
    expect(budgetAsksLess(settings, settings)).toBe(false);
    for (const change of [
      { base_minutes: 150 }, { max_minutes: 360 }, { floor_minutes: 40 }, { habits_for_max: 5 },
      { rate_distraction: 0.5 }, { rate_day: 2 }, { rate_bedtime: 5 }, { rate_grayscale_off: 0 }, { borrow_multiplier: 1 },
      { wait_minutes: 5 }, { window_use_minutes: 15 }, { grace_minutes: 2 }, { window_minutes: 30 }, { enabled: false }
    ]) expect(budgetAsksLess(settings, { ...settings, ...change }), JSON.stringify(change)).toBe(true);
  });

  it('applies asking more, and anything while it is off, at once', () => {
    expect(budgetAsksLess(settings, { ...settings, base_minutes: 90, rate_day: 4, wait_minutes: 15, window_minutes: 90 })).toBe(false);
    expect(budgetAsksLess({ ...settings, enabled: false }, { ...settings, base_minutes: 600 })).toBe(false);
    expect(budgetAsksLess(undefined, settings)).toBe(false);
  });

  it('uses the waiting settings once their time has come', () => {
    const next = { ...settings, base_minutes: 150 };
    const config = { distractions: [], essentials: [], budget: settings, budget_next: next, budget_next_from: '2026-11-27T05:00:00Z' };
    expect(budgetInForce(config, new Date('2026-11-27T04:59:00Z'))).toBe(settings);
    expect(budgetInForce(config, new Date('2026-11-27T05:00:00Z'))).toBe(next);
    expect(budgetInForce({ ...config, budget_next_from: '2026-11-27' }, new Date('2026-11-26T22:00:00Z'))).toBe(next);
  });
});

describe('budget history', () => {
  it('lists closed days newest first and keeps days without a record', () => {
    const row = { day: '2026-11-24', budget: 150, spent: 40, debt_passed_on: 0, habits_done: 3, habits_missed: 1 };
    const history = budgetHistory([row], '2026-11-26', 3);
    expect(history.map(item => item.day)).toEqual(['2026-11-25', '2026-11-24', '2026-11-23']);
    expect(history[0].record).toBeUndefined();
    expect(history[1].record).toBe(row);
    expect(habitsDoneText(row)).toBe('3 of 4');
    expect(habitsDoneText({ ...row, habits_done: 0, habits_missed: 0 })).toBe('none scheduled');
  });
});
