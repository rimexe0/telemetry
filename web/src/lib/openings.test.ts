import { describe, expect, it } from 'vitest';
import { confirmText, longestOpening, openingStatus } from './openings';

const opening = { id: 1, purpose: 'a call', sites: [], apps: [], everything: true, starts_at: '2026-10-26T12:10:00Z', until: '2026-10-26T12:40:00Z' };

describe('openings', () => {
  it('counts down the wait, then shows when it closes', () => {
    expect(openingStatus(opening, new Date('2026-10-26T12:00:30Z'))).toMatchObject({ phase: 'waiting', text: expect.stringMatching(/^Opens in 9:30, closes /) });
    expect(openingStatus(opening, new Date('2026-10-26T12:10:00Z'))).toMatchObject({ phase: 'open' });
    expect(openingStatus(opening, new Date('2026-10-26T12:40:00Z')).phase).toBe('ended');
    expect(openingStatus({ ...opening, ended_at: '2026-10-26T12:05:00Z' }, new Date('2026-10-26T12:06:00Z')).phase).toBe('ended');
  });

  it('asks with the wait, the end and the day count', () => {
    const text = confirmText({ wait_minutes: 10, max_minutes: 30, per_day: 2 }, 1, 30, new Date('2026-10-26T12:00:00Z'));
    expect(text).toMatch(/^It opens at .+, after 10 minutes, and closes at .+\. This is opening 2 of 2 today\. Are you sure\?$/);
    expect(confirmText({}, 0, 15, new Date())).toMatch(/^It opens now and closes at .+\. Are you sure\?$/);
  });

  it('caps the length at the rule', () => {
    expect(longestOpening({ max_minutes: 30 })).toBe(30);
    expect(longestOpening({})).toBe(240);
  });
});
