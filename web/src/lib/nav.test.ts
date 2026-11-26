import { describe, expect, it } from 'vitest';
import { inPhoneApp, loadKey, parseHash, routeHash, samePlace, type Route } from './nav';

const today = '2026-11-26';

describe('parseHash', () => {
  it('opens the four places', () => {
    expect(parseHash('#today', today)).toEqual({ place: 'today' });
    expect(parseHash('#habits', today)).toEqual({ place: 'habits' });
    expect(parseHash('#activity/timeline', today)).toEqual({ place: 'activity', section: 'timeline' });
    expect(parseHash('#activity/apps/2026-11-20', today)).toEqual({ place: 'activity', section: 'apps', day: '2026-11-20' });
    expect(parseHash('#settings', today)).toEqual({ place: 'settings' });
    expect(parseHash('#settings/lock', today)).toEqual({ place: 'settings', page: 'lock' });
  });

  it('opens Today for an empty or unknown address', () => {
    expect(parseHash('', today)).toEqual({ place: 'today' });
    expect(parseHash('#', today)).toEqual({ place: 'today' });
    expect(parseHash('#nothing-here', today)).toEqual({ place: 'today' });
  });

  it('sends every old tab to where its contents went', () => {
    const old: [string, Route][] = [
      ['#now', { place: 'today' }],
      ['#habits', { place: 'habits' }],
      ['#activity', { place: 'activity', section: 'apps' }],
      ['#day', { place: 'activity', section: 'timeline' }],
      ['#day/2026-11-19', { place: 'activity', section: 'timeline', day: '2026-11-19' }],
      ['#plan', { place: 'activity', section: 'plan' }],
      ['#plan/2026-11-19', { place: 'activity', section: 'plan', day: '2026-11-19' }],
      ['#rules', { place: 'settings' }],
      ['#health', { place: 'activity', section: 'health' }],
      ['#collection', { place: 'settings', page: 'sources' }],
      ['#devices', { place: 'settings', page: 'devices' }],
      ['#sign-in', { place: 'settings', page: 'sign-in' }]
    ];
    for (const [hash, route] of old) expect(parseHash(hash, today), hash).toEqual(route);
  });

  it('keeps only real days, and no day after today except for a plan', () => {
    expect(parseHash('#day/2026-02-30', today)).toEqual({ place: 'activity', section: 'timeline' });
    expect(parseHash('#day/yesterday', today)).toEqual({ place: 'activity', section: 'timeline' });
    expect(parseHash('#day/2026-11-27', today)).toEqual({ place: 'activity', section: 'timeline' });
    expect(parseHash('#plan/2026-11-27', today)).toEqual({ place: 'activity', section: 'plan', day: '2026-11-27' });
    expect(parseHash('#activity/health/2026-11-20', today)).toEqual({ place: 'activity', section: 'health' });
  });

  it('opens the Settings list for an unknown settings page', () => {
    expect(parseHash('#settings/nope', today)).toEqual({ place: 'settings' });
  });
});

describe('routeHash', () => {
  it('writes each place, leaving today out', () => {
    expect(routeHash({ place: 'today' }, today)).toBe('#today');
    expect(routeHash({ place: 'habits' }, today)).toBe('#habits');
    expect(routeHash({ place: 'activity', section: 'timeline', day: today }, today)).toBe('#activity/timeline');
    expect(routeHash({ place: 'activity', section: 'plan', day: '2026-11-19' }, today)).toBe('#activity/plan/2026-11-19');
    expect(routeHash({ place: 'activity', section: 'health', day: '2026-11-19' }, today)).toBe('#activity/health');
    expect(routeHash({ place: 'settings' }, today)).toBe('#settings');
    expect(routeHash({ place: 'settings', page: 'sources' }, today)).toBe('#settings/sources');
  });

  it('round-trips through parseHash', () => {
    const routes: Route[] = [
      { place: 'today' }, { place: 'habits' }, { place: 'settings' }, { place: 'settings', page: 'times' },
      { place: 'activity', section: 'apps', day: '2026-11-01' }, { place: 'activity', section: 'health' }
    ];
    for (const route of routes) expect(parseHash(routeHash(route, today), today)).toEqual(route);
  });
});

describe('loadKey', () => {
  it('loads what the old tab for that content loaded', () => {
    expect(loadKey({ place: 'today' })).toBe('now');
    expect(loadKey({ place: 'habits' })).toBe('self');
    expect(loadKey({ place: 'activity', section: 'timeline' })).toBe('day');
    expect(loadKey({ place: 'activity', section: 'apps' })).toBe('self');
    expect(loadKey({ place: 'activity', section: 'health' })).toBe('health');
    expect(loadKey({ place: 'activity', section: 'plan' })).toBe('plan');
    expect(loadKey({ place: 'settings' })).toBe('self');
    expect(loadKey({ place: 'settings', page: 'budget' })).toBe('rules');
    expect(loadKey({ place: 'settings', page: 'distractions' })).toBe('rules');
    expect(loadKey({ place: 'settings', page: 'sources' })).toBe('collection');
    expect(loadKey({ place: 'settings', page: 'devices' })).toBe('devices');
    expect(loadKey({ place: 'settings', page: 'sign-in' })).toBe('self');
  });
});

describe('samePlace', () => {
  it('compares place, section and page but not the day', () => {
    expect(samePlace({ place: 'activity', section: 'apps', day: '2026-11-01' }, { place: 'activity', section: 'apps' })).toBe(true);
    expect(samePlace({ place: 'activity', section: 'apps' }, { place: 'activity', section: 'plan' })).toBe(false);
    expect(samePlace({ place: 'settings' }, { place: 'settings', page: 'lock' })).toBe(false);
  });
});

describe('inPhoneApp', () => {
  it('is true only inside the phone app', () => {
    expect(inPhoneApp('Mozilla/5.0 (Linux; Android 16) AppleWebKit/537.36 Chrome/140 Mobile Safari/537.36 TelemetryApp/1.4')).toBe(true);
    expect(inPhoneApp('Mozilla/5.0 (Macintosh) AppleWebKit/537.36 Chrome/140 Safari/537.36')).toBe(false);
    expect(inPhoneApp(undefined)).toBe(false);
  });
});
