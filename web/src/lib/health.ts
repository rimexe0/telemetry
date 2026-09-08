import type { Observation } from './api';

// health-view/v1: fixed UTC+03:00 display, never browser-dependent day boundaries.
const DAY = 86_400_000;
export const dayKey = (time: string | number) => new Date(new Date(time).getTime() + 3 * 3_600_000).toISOString().slice(0, 10);
export const dayStart = (date: string) => Date.parse(`${date}T00:00:00+03:00`);
export const formatMinutes = (minutes: number | null) => minutes === null ? 'Unknown' : `${Math.floor(Math.round(minutes) / 60)}h ${Math.round(minutes) % 60}m`;
export const originLabel = (origin: string) => origin === 'com.sec.android.app.shealth' ? 'Samsung Health' : origin === 'android' ? 'Android' : origin;
export type Interval = { start: number; end: number; observation: Observation };
export type HealthDay = {
  date: string; steps: number | null; stepAmbiguous: boolean;
  sleepMinutes: number | null; exerciseMinutes: number | null;
  samples: { time: number; bpm: number; observation: Observation }[];
  sleep: Interval[]; exercise: Interval[]; stepRecords: Observation[];
};
type ObjectValue = Record<string, unknown>;
const object = (value: unknown): ObjectValue => value !== null && typeof value === 'object' && !Array.isArray(value) ? value as ObjectValue : {};
export const healthOrigin = (o: Observation) => String(object(object(o.original_payload).metadata).data_origin ?? 'Unknown origin');
export const isHealth = (o: Observation) => o.source_kind === 'health_connect';

export function unionMinutes(intervals: {start: number; end: number}[]): number | null {
  if (!intervals.length) return null;
  let total = 0, end = -Infinity;
  for (const x of [...intervals].sort((a, b) => a.start - b.start)) {
    total += Math.max(0, x.end - Math.max(x.start, end));
    end = Math.max(end, x.end);
  }
  return total / 60_000;
}

export function deriveHealth(observations: Observation[], origin: string, fromDay: string, toDay: string) {
  const health = observations.filter(isHealth);
  const origins = [...new Set(health.map(healthOrigin))].sort();
  const days: HealthDay[] = [];
  const start = dayStart(fromDay), end = dayStart(toDay) + DAY;
  if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start || end - start > 366 * DAY) return { days, origins, warnings: ['Choose a valid range of at most 366 days.'] };
  for (let t = start; t < end; t += DAY) days.push({date: dayKey(t), steps: null, stepAmbiguous: false, sleepMinutes: null, exerciseMinutes: null, samples: [], sleep: [], exercise: [], stepRecords: []});
  const dayMap = new Map(days.map(d => [d.date, d]));
  const warnings = new Set<string>();
  const samples = new Map<number, { time: number; bpm: number; observation: Observation } | null>();
  let repeated = 0, conflicts = 0, invalid = 0;
  const records = new Map<string, Observation>();
  // Effective records come from the API. Repeated transport copies are ignored.
  for (const o of health) if (healthOrigin(o) === origin) records.set(`${o.source_instance_key}\0${o.source_record_key}`, o);
  for (const o of records.values()) {
    const p = object(o.original_payload), type = p.record_type;
    const a = Date.parse(o.occurred_at), b = Date.parse(o.ended_at ?? '');
    if (type === 'heart_rate') {
      if (!Array.isArray(p.samples)) { invalid++; continue; }
      for (const value of p.samples) {
        const s = object(value), time = typeof s.time === 'string' ? Date.parse(s.time) : NaN, bpm = s.beats_per_minute;
        if (!Number.isFinite(time) || typeof bpm !== 'number' || !Number.isFinite(bpm) || bpm <= 0) { invalid++; continue; }
        if (time < start || time >= end) continue;
        if (!samples.has(time)) samples.set(time, {time, bpm, observation: o});
        else {
          const prior = samples.get(time);
          if (prior && prior.bpm === bpm) repeated++;
          else if (prior) { samples.set(time, null); conflicts++; }
        }
      }
      continue;
    }
    if (!['steps', 'sleep_session', 'exercise_session'].includes(String(type))) continue;
    if (!Number.isFinite(a) || !Number.isFinite(b) || b <= a) { invalid++; continue; }
    for (const d of days) {
      const lo = dayStart(d.date), hi = lo + DAY;
      if (a >= hi || b <= lo) continue;
      if (type === 'steps') {
        d.stepRecords.push(o);
        if (a < lo || b > hi || typeof p.count !== 'number' || !Number.isFinite(p.count) || p.count < 0) d.stepAmbiguous = true;
      } else {
        const interval = {start: Math.max(a, lo), end: Math.min(b, hi), observation: o};
        (type === 'sleep_session' ? d.sleep : d.exercise).push(interval);
      }
    }
  }
  for (const sample of samples.values()) if (sample) dayMap.get(dayKey(sample.time))?.samples.push(sample);
  for (const d of days) {
    const sorted = [...d.stepRecords].sort((a,b) => Date.parse(a.occurred_at) - Date.parse(b.occurred_at));
    let lastEnd = -Infinity;
    for (const o of sorted) {
      if (Date.parse(o.occurred_at) < lastEnd) d.stepAmbiguous = true;
      lastEnd = Math.max(lastEnd, Date.parse(o.ended_at ?? ''));
    }
    if (d.stepRecords.length && !d.stepAmbiguous) d.steps = d.stepRecords.reduce((n,o) => n + Number(object(o.original_payload).count), 0);
    d.sleepMinutes = unionMinutes(d.sleep);
    d.exerciseMinutes = unionMinutes(d.exercise);
    d.samples.sort((a,b) => a.time - b.time);
    d.sleep.sort((a,b) => a.start - b.start);
    d.exercise.sort((a,b) => a.start - b.start);
  }
  if (days.some(d => d.stepAmbiguous)) warnings.add('Some step intervals overlap, cross midnight, or lack a valid count. Their daily totals are withheld; inspect the source records.');
  if (repeated) warnings.add(`${repeated.toLocaleString()} identical heart-rate readings at repeated timestamps were counted once.`);
  if (conflicts) warnings.add(`${conflicts.toLocaleString()} heart-rate timestamps have conflicting values and are excluded from charts and summaries.`);
  if (invalid) warnings.add(`${invalid.toLocaleString()} malformed measurements or intervals were excluded.`);
  return {days, origins, warnings: [...warnings]};
}
