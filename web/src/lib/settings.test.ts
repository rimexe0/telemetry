import { describe, expect, it } from 'vitest';
import type { ActivityLabel, PolicyConfig } from './api';
import { distractionsValue } from './settings';

const label = (id: number, kind: 'app' | 'site', match: string, value = 'distraction'): ActivityLabel => ({ id, kind, match, label: value, created_at: '2026-11-26T10:00:00Z' });

describe('what counts as a distraction', () => {
  it('counts the blocked groups and the distraction labels, each once', () => {
    const config: PolicyConfig = { essentials: [], distractions: [
      { id: 'social', label: 'Social', android_packages: ['com.twitter.android', 'com.reddit.frontpage'], domains: ['www.reddit.com', 'x.com'] },
      { id: 'video', label: 'Video', mac_apps: ['IINA'], domains: ['youtube.com'] }
    ] };
    const labels = [label(1, 'app', 'com.twitter.android'), label(2, 'site', 'reddit.com'), label(3, 'app', 'com.whatsapp'), label(4, 'site', 'news.ycombinator.com'), label(5, 'app', 'com.ichi2.anki', 'study')];
    expect(distractionsValue(config, labels)).toBe('8 apps and sites');
  });
  it('counts labels alone when no group is set, in plain words', () => {
    const empty: PolicyConfig = { distractions: [], essentials: [] };
    expect(distractionsValue(empty, [label(1, 'app', 'com.whatsapp'), label(2, 'app', 'com.twitter.android')])).toBe('2 apps');
    expect(distractionsValue(empty, [label(1, 'site', 'youtube.com')])).toBe('1 site');
    expect(distractionsValue(empty, [])).toBe('None');
  });
  it('shows nothing until both are known', () => {
    expect(distractionsValue(undefined, [])).toBe('');
    expect(distractionsValue({ distractions: [], essentials: [] }, undefined)).toBe('');
  });
});
