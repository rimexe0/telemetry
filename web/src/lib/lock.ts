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
