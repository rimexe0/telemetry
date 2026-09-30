import { describe, expect, it } from 'vitest';
import { describeChange, formatMinutes } from './activity';

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
});
