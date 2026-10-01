import type { LabelChange, ObservedActivity } from './api';

export function formatMinutes(minutes: number): string {
  const total = Math.round(minutes);
  if (total < 60) return `${total} min`;
  const hours = Math.floor(total / 60);
  const rest = total % 60;
  return rest ? `${hours} h ${rest} min` : `${hours} h`;
}

export function describeChange(change: LabelChange): string {
  const target = `${change.kind === 'site' ? 'site' : 'app'} ${change.match}`;
  return change.action === 'remove' ? `Remove the label from ${target}` : `Label ${target} as “${change.label}”`;
}

export function deviceName(device: string): string {
  return device === 'mac' ? 'Mac' : device === 'phone' ? 'Phone' : device;
}

// One line of what was recorded in a stretch of time: per-device active time,
// then distractions and missing data when there are any.
export function observedLine(observed: ObservedActivity): string {
  const parts = observed.devices.filter(device => device.active_minutes >= 1).map(device => `${deviceName(device.device)} ${formatMinutes(device.active_minutes)}`);
  if (parts.length === 0) parts.push('nothing recorded');
  if (observed.distraction_minutes >= 1) parts.push(`distractions ${formatMinutes(observed.distraction_minutes)}`);
  const missing = Math.max(0, ...observed.devices.map(device => device.no_data_minutes));
  if (missing >= 5) parts.push(`no data ${formatMinutes(missing)}`);
  return parts.join(' · ');
}

// The most used apps and sites in a stretch, sites named in place of their browser.
export function observedTop(observed: ObservedActivity, limit = 4): string {
  const rows = observed.devices.flatMap(device => [...(device.sites ?? []), ...(device.apps ?? []).filter(app => !(device.sites ?? []).some(site => site.app === app.name))]);
  return rows.filter(row => row.minutes >= 1).sort((a, b) => b.minutes - a.minutes).slice(0, limit).map(row => `${row.name} ${formatMinutes(row.minutes)}`).join(', ');
}
