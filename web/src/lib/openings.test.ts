import { describe, expect, it } from 'vitest';
import { confirmPrompt, confirmText, longestOpening, openingStatus, yesSpot } from './openings';

const opening = { id: 1, purpose: 'a call', sites: [], apps: [], everything: true, starts_at: '2026-10-26T12:10:00Z', until: '2026-10-26T12:40:00Z' };

describe('openings', () => {
  it('counts down the wait, then shows when it closes', () => {
    expect(openingStatus(opening, new Date('2026-10-26T12:00:30Z'))).toMatchObject({ phase: 'waiting', text: expect.stringMatching(/^Opens in 9:30, closes /) });
    expect(openingStatus(opening, new Date('2026-10-26T12:10:00Z'))).toMatchObject({ phase: 'open' });
    expect(openingStatus(opening, new Date('2026-10-26T12:40:00Z')).phase).toBe('ended');
    expect(openingStatus({ ...opening, ended_at: '2026-10-26T12:05:00Z' }, new Date('2026-10-26T12:06:00Z')).phase).toBe('ended');
  });

  it('asks for confirmations after the wait', () => {
    const asking = { ...opening, confirmations_needed: 10, confirmations: 3 };
    expect(openingStatus(asking, new Date('2026-10-26T12:00:00Z'))).toMatchObject({ phase: 'waiting', text: 'Confirm in 10:00' });
    expect(openingStatus(asking, new Date('2026-10-26T12:11:00Z'))).toMatchObject({ phase: 'confirming', text: 'Confirmed 3 of 10' });
    expect(openingStatus({ ...asking, confirmations: 10 }, new Date('2026-10-26T12:11:00Z')).phase).toBe('open');
  });

  it('asks with the wait, the end and the day count', () => {
    const text = confirmText({ wait_minutes: 10, max_minutes: 30, per_day: 2 }, 1, 30, new Date('2026-10-26T12:00:00Z'));
    expect(text).toMatch(/^It opens at .+, after 10 minutes, and closes at .+\. This is opening 2 of 2 today\. Are you sure\?$/);
    expect(confirmText({}, 0, 15, new Date())).toMatch(/^It opens now and closes at .+\. Are you sure\?$/);
    expect(confirmText({ wait_minutes: 10, confirmations: 10 }, 0, 30, new Date())).toMatch(/you confirm it 10 times, and it opens for 30 minutes after the last\. Are you sure\?$/);
  });

  it('moves Yes on every page and words each page differently', () => {
    for (let page = 1; page < 10; page++) {
      expect(yesSpot(4, page)).not.toEqual(yesSpot(4, page - 1));
      expect(confirmPrompt(page)).not.toBe(confirmPrompt(page - 1));
    }
  });

  it('caps the length at the rule', () => {
    expect(longestOpening({ max_minutes: 30 })).toBe(30);
    expect(longestOpening({})).toBe(240);
  });
});
