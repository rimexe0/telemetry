import type { ActivityLabel, Device, LockResponse, PolicyConfig, SignInSettings, SourceSummary } from './api';
import { budgetInForce, formatBudgetMinutes } from './budget';
import { formatWindows } from './lock';
import { clock } from './today';

// The short current value on the right of each Settings row. An empty
// string means unknown, and the row shows nothing rather than a guess.

export function budgetValue(config: PolicyConfig | undefined, now = new Date()): string {
  if (!config) return '';
  const budget = budgetInForce(config, now);
  if (!budget?.enabled) return 'Off';
  return `${formatBudgetMinutes(budget.base_minutes)} a day`;
}

export function timesValue(config: PolicyConfig | undefined, lock: LockResponse | undefined): string {
  const parts: string[] = [];
  const times = formatWindows(config?.distraction_times);
  if (times) parts.push(times);
  if (lock) parts.push(lock.bedtime?.enabled ? `bed ${lock.bedtime.start ?? '?'}` : 'no bedtime');
  return parts.join(' · ');
}

// How many apps and sites count as distractions: those in the blocked
// groups and those labelled "distraction", each counted once.
export function distractionsValue(config: PolicyConfig | undefined, labels: ActivityLabel[] | undefined): string {
  if (!config || !labels) return '';
  const apps = new Set<string>();
  const sites = new Set<string>();
  const site = (value: string) => value.trim().toLowerCase().replace(/^www\./, '');
  for (const group of config.distractions ?? []) {
    for (const app of [...(group.android_packages ?? []), ...(group.mac_apps ?? [])]) if (app.trim()) apps.add(app.trim().toLowerCase());
    for (const domain of group.domains ?? []) if (site(domain)) sites.add(site(domain));
  }
  for (const label of labels) {
    if (label.label !== 'distraction' || !label.match.trim()) continue;
    if (label.kind === 'site') sites.add(site(label.match));
    else apps.add(label.match.trim().toLowerCase());
  }
  const total = apps.size + sites.size;
  if (total === 0) return 'None';
  if (sites.size === 0) return `${total} ${total === 1 ? 'app' : 'apps'}`;
  if (apps.size === 0) return `${total} ${total === 1 ? 'site' : 'sites'}`;
  return `${total} apps and sites`;
}

export function lockValue(lock: LockResponse | undefined): string {
  if (!lock) return '';
  if (lock.state.unlocked_until) return `Open until ${clock(lock.state.unlocked_until)}`;
  if (lock.state.locked) return lock.state.until ? `Locked until ${clock(lock.state.until)}` : 'Locked';
  return lock.lock.enabled ? 'On' : 'Off';
}

export function devicesValue(devices: Device[] | undefined): string {
  if (!devices) return '';
  const connected = devices.filter(device => !device.revoked_at).length;
  return `${connected} connected`;
}

export function sourcesValue(sources: SourceSummary[] | undefined): string {
  if (!sources) return '';
  return `${sources.length} ${sources.length === 1 ? 'source' : 'sources'}`;
}

export function signInValue(settings: SignInSettings | undefined): string {
  if (!settings) return '';
  const on = [settings.panel_token.enabled ? 'token' : '', settings.clerk.enabled ? 'Clerk' : ''].filter(Boolean);
  return on.length ? on.join(', ') : 'None on';
}
