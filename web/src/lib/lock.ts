import type { Window } from './api';

const clock = /^([01][0-9]|2[0-3]):[0-5][0-9]$/;

/** Reads "09:00-18:00, 22:00-02:00" into windows; null when it does not read. */
export function parseWindows(text: string): Window[] | null {
  const windows: Window[] = [];
  for (const part of text.split(',').map(item => item.trim()).filter(Boolean)) {
    const [start, end] = part.split(/\s*[-–]\s*/);
    if (!clock.test(start ?? '') || !clock.test(end ?? '') || start === end) return null;
    windows.push({ start, end });
  }
  return windows;
}

export function formatWindows(windows: Window[] | undefined): string {
  return (windows ?? []).map(window => `${window.start}-${window.end}`).join(', ');
}

/** Hours a bedtime lasts, from start to end, past midnight included. */
export function bedtimeHours(start: string, end: string): number {
  const minutes = (clock: string) => Number(clock.slice(0, 2)) * 60 + Number(clock.slice(3, 5));
  if (!/^\d\d:\d\d$/.test(start) || !/^\d\d:\d\d$/.test(end)) return 0;
  return ((minutes(end) - minutes(start) + 1440) % 1440) / 60;
}

/** "1 Nov" from a YYYY-MM-DD date. */
export function shortDate(day: string): string {
  return new Date(`${day}T12:00:00Z`).toLocaleDateString('en-GB', { day: 'numeric', month: 'short', timeZone: 'UTC' });
}

/**
 * What the owner is asked before a rule change is saved, or nothing when it
 * applies at once and needs no warning.
 */
export function saveWarning(waitsUntil: string | undefined, bedtime?: { enabled?: boolean; start?: string; end?: string }): string {
  const parts: string[] = [];
  if (bedtime?.enabled && bedtime.start && bedtime.end) {
    const hours = bedtimeHours(bedtime.start, bedtime.end);
    if (hours > 12) parts.push(`Bedtime would last ${hours} hours, from ${bedtime.start} to ${bedtime.end}.`);
  }
  if (waitsUntil) parts.push(`This asks less than now, so it only starts on ${shortDate(waitsUntil)}; today stays as it is.`);
  return parts.join(' ');
}
