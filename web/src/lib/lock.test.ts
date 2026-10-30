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
