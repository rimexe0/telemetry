import { describe, expect, it } from 'vitest';
import { describeChange, formatMinutes, namedInsight, observedTop, usageName } from './activity';

describe('activity helpers', () => {
  it('formats minutes compactly', () => {
    expect(formatMinutes(12.4)).toBe('12 min');
    expect(formatMinutes(60)).toBe('1 h');
    expect(formatMinutes(125)).toBe('2 h 5 min');
  });
  it('describes label changes for review', () => {
    expect(describeChange({ action: 'set', kind: 'site', match: 'youtube.com', label: 'distraction' })).toBe('Label site youtube.com as “distraction”');
    expect(describeChange({ action: 'remove', kind: 'app', match: 'osu!' })).toBe('Remove the label from app osu!');
  });
  it('names phone apps by the phone\'s name and keeps the package otherwise', () => {
    expect(usageName({ name: 'com.whatsapp', app_name: 'WhatsApp', minutes: 5, sessions: 1 })).toBe('WhatsApp');
    expect(usageName({ name: 'com.unknown', minutes: 5, sessions: 1 })).toBe('com.unknown');
    const observed = { minutes: 60, distraction_minutes: 0, devices: [{ device: 'phone' as const, active_minutes: 30, no_data_minutes: 0, labels: [],
      apps: [{ name: 'com.twitter.android', app_name: 'X', minutes: 20, sessions: 2 }, { name: 'com.unknown', minutes: 10, sessions: 1 }] }] };
    expect(observedTop(observed)).toBe('X 20 min, com.unknown 10 min');
  });
  it('puts the phone\'s names into insights that mention packages', () => {
    const insight = { key: 'screen_phone', text: 'Phone: 1h of screen time', detail: 'Top app: com.twitter.android, 40m.', names: ['com.twitter.android'] };
    expect(namedInsight(insight, { 'com.twitter.android': 'X' }).detail).toBe('Top app: X, 40m.');
    expect(namedInsight(insight, null).detail).toBe('Top app: com.twitter.android, 40m.');
  });
});
