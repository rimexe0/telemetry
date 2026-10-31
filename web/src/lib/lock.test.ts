import { describe, expect, it } from 'vitest';
import { formatWindows, parseWindows } from './lock';

describe('lock times', () => {
  it('reads and writes windows', () => {
    const windows = parseWindows('09:00-18:00, 22:00 – 02:00');
    expect(windows).toEqual([{ start: '09:00', end: '18:00' }, { start: '22:00', end: '02:00' }]);
    expect(formatWindows(windows!)).toBe('09:00-18:00, 22:00-02:00');
    expect(parseWindows('')).toEqual([]);
    expect(parseWindows('9-18')).toBeNull();
    expect(parseWindows('10:00-10:00')).toBeNull();
  });
});

import { bedtimeHours, saveWarning } from './lock';

describe('rule save warnings', () => {
  it('measures bedtime across midnight', () => {
    expect(bedtimeHours('12:00', '08:00')).toBe(20);
    expect(bedtimeHours('00:00', '08:00')).toBe(8);
    expect(bedtimeHours('23:30', '07:00')).toBe(7.5);
  });

  it('warns about long bedtimes and changes that wait a day', () => {
    expect(saveWarning(undefined, { enabled: true, start: '12:00', end: '08:00' })).toBe('Bedtime would last 20 hours, from 12:00 to 08:00.');
    expect(saveWarning('2026-11-01')).toBe('This asks less than now, so it only starts on 1 Nov; today stays as it is.');
    expect(saveWarning(undefined, { enabled: true, start: '00:00', end: '08:00' })).toBe('');
  });
});
