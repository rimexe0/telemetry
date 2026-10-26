import type { Opening, OpeningRule } from './api';

const clock = (at: Date) => at.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: false });

function countdown(ms: number): string {
  const seconds = Math.max(0, Math.ceil(ms / 1000));
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`;
}

/** Where an opening is at an instant, in the words the panel shows. */
export function openingStatus(opening: Opening, now: Date): { phase: 'waiting' | 'open' | 'ended'; text: string } {
  const starts = new Date(opening.starts_at);
  const until = new Date(opening.until);
  if (opening.ended_at || now >= until) return { phase: 'ended', text: 'Ended' };
  if (now < starts) return { phase: 'waiting', text: `Opens in ${countdown(starts.getTime() - now.getTime())}, closes ${clock(until)}` };
  return { phase: 'open', text: `Open until ${clock(until)}` };
}

/** What the owner is asked before an opening is made. */
export function confirmText(rule: OpeningRule, usedToday: number, minutes: number, now: Date): string {
  const wait = rule.wait_minutes ?? 0;
  const starts = new Date(now.getTime() + wait * 60_000);
  const until = new Date(starts.getTime() + minutes * 60_000);
  const when = wait > 0 ? `It opens at ${clock(starts)}, after ${wait} minutes,` : 'It opens now';
  const count = rule.per_day ? ` This is opening ${usedToday + 1} of ${rule.per_day} today.` : '';
  return `${when} and closes at ${clock(until)}.${count} Are you sure?`;
}

/** The longest opening the rule allows, in minutes. */
export function longestOpening(rule: OpeningRule): number {
  return rule.max_minutes && rule.max_minutes < 240 ? rule.max_minutes : 240;
}
