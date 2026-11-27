// The panel's four places and their addresses. The address is the hash, so a
// refresh or a shared link comes back to the same place:
//   #today
//   #habits
//   #activity/<section>[/<day>]
//   #settings[/<page>]
// Every address the panel used before the four places (#now, #rules, #day/…,
// #plan/…, #health, #collection, #devices, #sign-in, #habits, #activity) still
// opens the place its contents moved to.

export type Place = 'today' | 'habits' | 'activity' | 'settings';
export type ActivitySection = 'timeline' | 'apps' | 'health' | 'plan' | 'places';
export type SettingsPage = 'budget' | 'times' | 'distractions' | 'lock' | 'devices' | 'sources' | 'sign-in';

export type Route =
  | { place: 'today' }
  | { place: 'habits' }
  | { place: 'activity'; section: ActivitySection; day?: string }
  | { place: 'settings'; page?: SettingsPage };

export const places: { place: Place; label: string }[] = [
  { place: 'today', label: 'Today' },
  { place: 'habits', label: 'Habits' },
  { place: 'activity', label: 'Activity' },
  { place: 'settings', label: 'Settings' }
];

export const activitySections: { section: ActivitySection; label: string }[] = [
  { section: 'timeline', label: 'Timeline' },
  { section: 'apps', label: 'Apps' },
  { section: 'health', label: 'Health' },
  { section: 'plan', label: 'Plan' },
  { section: 'places', label: 'Places' }
];

export const settingsPages: Record<SettingsPage, string> = {
  budget: 'Budget and costs',
  times: 'Distraction time, bedtime',
  distractions: 'What counts as a distraction',
  lock: 'Settings lock',
  devices: 'Devices',
  sources: 'Data sources',
  'sign-in': 'Sign-in'
};

const sectionNames = activitySections.map(item => item.section) as string[];
const pageNames = Object.keys(settingsPages);
const dayPattern = /^\d{4}-\d{2}-\d{2}$/;

/** A day from an address: a real calendar day, not after today except for a plan. */
function readDay(value: string | undefined, today: string, future: boolean): string | undefined {
  if (!value || !dayPattern.test(value)) return undefined;
  const parsed = new Date(`${value}T12:00:00Z`);
  if (Number.isNaN(parsed.getTime()) || parsed.toISOString().slice(0, 10) !== value) return undefined;
  if (!future && value > today) return undefined;
  return value;
}

function activity(section: ActivitySection, day: string | undefined, today: string): Route {
  const kept = section === 'health' ? undefined : readDay(day, today, section === 'plan');
  return kept ? { place: 'activity', section, day: kept } : { place: 'activity', section };
}

/** The place an address opens, old addresses included. Anything unknown opens Today. */
export function parseHash(hash: string, today: string): Route {
  const [name = '', second, third] = hash.replace(/^#/, '').split('/');
  switch (name) {
    case 'today':
    case 'now':
      return { place: 'today' };
    case 'habits':
      return { place: 'habits' };
    case 'activity':
      // #activity alone was the old Activity tab: where the time went, by app.
      if (second && sectionNames.includes(second)) return activity(second as ActivitySection, third, today);
      return activity('apps', undefined, today);
    case 'day':
      return activity('timeline', second, today);
    case 'plan':
      return activity('plan', second, today);
    case 'health':
      return activity('health', undefined, today);
    case 'settings':
      return second && pageNames.includes(second) ? { place: 'settings', page: second as SettingsPage } : { place: 'settings' };
    case 'rules':
      return { place: 'settings' };
    case 'collection':
      return { place: 'settings', page: 'sources' };
    case 'devices':
      return { place: 'settings', page: 'devices' };
    case 'sign-in':
      return { place: 'settings', page: 'sign-in' };
    default:
      return { place: 'today' };
  }
}

/** The address of a place. Today's day is left out, so the address stays short. */
export function routeHash(route: Route, today: string): string {
  if (route.place === 'activity') {
    const day = route.section !== 'health' && route.day && route.day !== today ? `/${route.day}` : '';
    return `#activity/${route.section}${day}`;
  }
  if (route.place === 'settings') return route.page ? `#settings/${route.page}` : '#settings';
  return `#${route.place}`;
}

/**
 * What the page loads for a place, named as the old tabs were: the loading
 * code is the old tabs' own. Places that load their own parts return 'self'.
 */
export type LoadKey = 'now' | 'day' | 'plan' | 'rules' | 'health' | 'collection' | 'devices' | 'self';

export function loadKey(route: Route): LoadKey {
  switch (route.place) {
    case 'today':
      return 'now';
    case 'habits':
      return 'self';
    case 'activity':
      return route.section === 'timeline' ? 'day' : route.section === 'health' ? 'health' : route.section === 'plan' ? 'plan' : 'self';
    case 'settings':
      if (route.page === 'sources') return 'collection';
      if (route.page === 'devices') return 'devices';
      if (route.page === 'budget' || route.page === 'times' || route.page === 'distractions' || route.page === 'lock') return 'rules';
      return 'self';
  }
}

/** Whether two routes are the same place, section and page (the day aside). */
export function samePlace(a: Route, b: Route): boolean {
  if (a.place !== b.place) return false;
  if (a.place === 'activity' && b.place === 'activity') return a.section === b.section;
  if (a.place === 'settings' && b.place === 'settings') return a.page === b.page;
  return true;
}

/** Whether the panel is open inside the Telemetry phone app's WebView. */
export function inPhoneApp(userAgent: string | undefined): boolean {
  return !!userAgent && userAgent.includes('TelemetryApp/');
}

/**
 * Whether the phone app draws the four places itself (Today and Habits are
 * native there), so the panel leaves its own bar out. Older app versions
 * without it still get the panel's bar.
 */
export function appDrawsNav(userAgent: string | undefined): boolean {
  return inPhoneApp(userAgent) && userAgent!.includes('TelemetryAppNav/');
}

/** The phone app's own settings screens, which the app opens when these links are followed. */
export const phoneLinks = {
  permissions: 'telemetry-app://settings/permissions',
  notifications: 'telemetry-app://settings/notifications'
};
