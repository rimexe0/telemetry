import type { Device, LockResponse, PolicyConfig, SignInSettings, SourceSummary } from './api';
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

export function distractionsValue(config: PolicyConfig | undefined): string {
  if (!config) return '';
  const groups = config.distractions?.length ?? 0;
  return `${groups} ${groups === 1 ? 'group' : 'groups'}`;
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
