import type { LabelChange } from './api';

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
