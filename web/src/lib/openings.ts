import type { Opening, OpeningRule } from './api';

const clock = (at: Date) => at.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: false });

function countdown(ms: number): string {
  const seconds = Math.max(0, Math.ceil(ms / 1000));
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`;
}

export type OpeningPhase = 'waiting' | 'confirming' | 'open' | 'ended';

/** Where an opening is at an instant, in the words the panel shows. */
export function openingStatus(opening: Opening, now: Date): { phase: OpeningPhase; text: string } {
  const starts = new Date(opening.starts_at);
  const until = new Date(opening.until);
  const needed = opening.confirmations_needed ?? 0;
  const given = opening.confirmations ?? 0;
  if (opening.ended_at || now >= until) return { phase: 'ended', text: 'Ended' };
  if (given < needed) {
    if (now < starts) return { phase: 'waiting', text: `Confirm in ${countdown(starts.getTime() - now.getTime())}` };
    return { phase: 'confirming', text: `Confirmed ${given} of ${needed}` };
  }
  if (now < starts) return { phase: 'waiting', text: `Opens in ${countdown(starts.getTime() - now.getTime())}, closes ${clock(until)}` };
  return { phase: 'open', text: `Open until ${clock(until)}` };
}

/** What the owner is asked before an opening is made. */
export function confirmText(rule: OpeningRule, usedToday: number, minutes: number, now: Date): string {
  const wait = rule.wait_minutes ?? 0;
  const confirmations = rule.confirmations ?? 0;
  const starts = new Date(now.getTime() + wait * 60_000);
  const until = new Date(starts.getTime() + minutes * 60_000);
  const count = rule.per_day ? ` This is opening ${usedToday + 1} of ${rule.per_day} today.` : '';
  if (confirmations > 0) {
    const when = wait > 0 ? `In ${wait} minutes, at ${clock(starts)},` : 'Then';
    return `${when} you confirm it ${confirmations} times, and it opens for ${minutes} minutes after the last.${count} Are you sure?`;
  }
  const when = wait > 0 ? `It opens at ${clock(starts)}, after ${wait} minutes,` : 'It opens now';
  return `${when} and closes at ${clock(until)}.${count} Are you sure?`;
}

/** The longest opening the rule allows, in minutes. */
export function longestOpening(rule: OpeningRule): number {
  return rule.max_minutes && rule.max_minutes < 240 ? rule.max_minutes : 240;
}

const prompts = [
  'Are you sure?',
  'Is this what you planned for now?',
  'Do you want this more than the next step of your task?',
  'Will this really take only the time you asked for?',
  'Are you sure, again?',
  'Is the reason above still true?',
  'Does this really need to happen before distraction time?',
  'Do you want this more than a small first step of your task?',
  'Still sure?',
  'Last chance to go back to your task. Sure?'
];

/** The question on confirmation page `page` (0-based). */
export function confirmPrompt(page: number): string {
  return prompts[page % prompts.length];
}

// Spots for the Yes button, as percentages of the page; neighbours are far
// apart so it is never where the last one was.
const spots: [number, number][] = [
  [8, 8], [78, 82], [14, 70], [70, 10], [44, 46], [84, 30], [24, 34], [60, 66], [6, 48], [88, 60]
];

/** Where the Yes button sits on a page, as top and left percentages. */
export function yesSpot(openingID: number, page: number): { top: number; left: number } {
  const [top, left] = spots[(page + openingID * 3) % spots.length];
  return { top, left };
}
