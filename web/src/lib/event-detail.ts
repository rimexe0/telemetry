import type { Observation } from './api';
import { originLabel } from './health';

export type EventField = { label: string; value: string };
export type EventStage = { start: number; end: number; label: string };
export type EventDescription = {
  title: string;
  subtitle: string;
  fields: EventField[];
  stages: EventStage[];
};

type RecordValue = Record<string, unknown>;

const object = (value: unknown): RecordValue =>
  value !== null && typeof value === 'object' && !Array.isArray(value) ? value as RecordValue : {};

const nonEmptyString = (value: unknown): string | undefined =>
  typeof value === 'string' && value.trim() ? value.trim() : undefined;

const displayString = (value: unknown, fallback = '—') => nonEmptyString(value) ?? fallback;

function parseTime(value: unknown): number | undefined {
  if (typeof value === 'number' && Number.isFinite(value)) return value < 10_000_000_000 ? value * 1000 : value;
  if (typeof value !== 'string' || !value.trim()) return undefined;
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) ? parsed : undefined;
}

function payloadFor(observation: Observation): RecordValue {
  return object(observation.original_payload);
}

function dataFor(observation: Observation): RecordValue {
  return object(payloadFor(observation).data);
}

function intervalFor(observation: Observation): { start?: number; end?: number } {
  const payload = payloadFor(observation);
  const start = parseTime(payload.start_time) ?? parseTime(observation.occurred_at);
  const end = parseTime(payload.end_time) ?? parseTime(observation.ended_at);
  if (start !== undefined && end === undefined) {
    const duration = typeof payload.duration === 'number' && Number.isFinite(payload.duration)
      ? payload.duration * 1000
      : undefined;
    return { start, end: duration !== undefined ? start + duration : undefined };
  }
  return { start, end };
}

function formatDateTime(time: number | undefined): string {
  if (time === undefined || !Number.isFinite(time)) return '—';
  return new Intl.DateTimeFormat('en-GB', {
    day: '2-digit', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit',
    timeZone: 'Europe/Istanbul'
  }).format(new Date(time));
}

export function formatDuration(milliseconds: number | undefined): string {
  if (milliseconds === undefined || !Number.isFinite(milliseconds) || milliseconds < 0) return '—';
  const seconds = Math.round(milliseconds / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  const remainingSeconds = seconds % 60;
  if (minutes < 60) return `${minutes}m${remainingSeconds ? ` ${remainingSeconds}s` : ''}`;
  const hours = Math.floor(minutes / 60);
  const remainingMinutes = minutes % 60;
  return `${hours}h${remainingMinutes ? ` ${remainingMinutes}m` : ''}`;
}

function intervalFields(interval: { start?: number; end?: number }): EventField[] {
  const fields: EventField[] = [];
  if (interval.start !== undefined) fields.push({ label: 'Started', value: formatDateTime(interval.start) });
  if (interval.end !== undefined) fields.push({ label: 'Ended', value: formatDateTime(interval.end) });
  if (interval.start !== undefined && interval.end !== undefined) {
    fields.push({ label: 'Duration', value: formatDuration(interval.end - interval.start) });
  }
  return fields;
}

function originOf(observation: Observation): string {
  return displayString(object(payloadFor(observation).metadata).data_origin, '');
}

function recordType(observation: Observation): string {
  return displayString(payloadFor(observation).record_type, observation.kind || observation.source_kind);
}

function prettyPackage(packageName: string): string {
  return {
    'org.mozilla.firefox': 'Firefox',
    'tw.nekomimi.nekogram': 'Nekogram',
    'com.ichi2.anki': 'Anki',
    'com.android.settings': 'Settings',
    'com.google.android.apps.nexuslauncher': 'Launcher',
    'com.openai.chatgpt': 'ChatGPT',
    'com.google.android.apps.tasks': 'Google Tasks',
    'com.opennutritracker.ont.opennutritracker': 'OpenNutriTracker'
  }[packageName] ?? packageName;
}

function projectName(value: unknown): string | undefined {
  const text = nonEmptyString(value);
  if (!text) return undefined;
  const parts = text.split('/').filter(Boolean);
  return parts.at(-1) ?? text;
}

function healthStageLabel(value: unknown): string {
  // AndroidX Health Connect SleepSessionRecord constants:
  // unknown 0, awake 1, sleeping 2, out of bed 3, light 4, deep 5, REM 6, awake in bed 7.
  if (typeof value === 'number') {
    return ({ 0: 'Unknown', 1: 'Awake', 2: 'Sleeping', 3: 'Awake', 4: 'Light', 5: 'Deep', 6: 'REM', 7: 'Awake' } as Record<number, string>)[value] ?? `Stage ${value}`;
  }
  const text = nonEmptyString(value)?.toLowerCase();
  if (!text) return 'Unknown';
  if (text.includes('rem')) return 'REM';
  if (text.includes('deep')) return 'Deep';
  if (text.includes('light')) return 'Light';
  if (text.includes('awake') || text.includes('out_of_bed') || text.includes('out of bed')) return 'Awake';
  if (text.includes('sleep')) return 'Sleeping';
  return 'Unknown';
}

function sleepStages(observation: Observation, interval: { start?: number; end?: number }): EventStage[] {
  const values = payloadFor(observation).stages;
  if (!Array.isArray(values) || interval.start === undefined || interval.end === undefined) return [];
  const seen = new Set<string>();
  const candidates: EventStage[] = [];
  for (const value of values) {
    const stage = object(value);
    const start = parseTime(stage.start_time) ?? parseTime(stage.startTime);
    const end = parseTime(stage.end_time) ?? parseTime(stage.endTime);
    if (start === undefined || end === undefined || end <= start) continue;
    const clippedStart = Math.max(start, interval.start);
    const clippedEnd = Math.min(end, interval.end);
    if (clippedEnd <= clippedStart) continue;
    const label = healthStageLabel(stage.stage ?? stage.stage_type ?? stage.type);
    const key = `${clippedStart}:${clippedEnd}:${label}`;
    if (seen.has(key)) continue;
    seen.add(key);
    candidates.push({ start: clippedStart, end: clippedEnd, label });
  }

  const merged: EventStage[] = [];
  for (const stage of candidates.sort((left, right) => left.start - right.start || left.end - right.end)) {
    const previous = merged.at(-1);
    if (previous && previous.label === stage.label && stage.start <= previous.end) {
      previous.end = Math.max(previous.end, stage.end);
    } else {
      merged.push({ ...stage });
    }
  }
  return merged;
}

function median(values: number[]): number {
  const sorted = [...values].sort((left, right) => left - right);
  const middle = Math.floor(sorted.length / 2);
  return sorted.length % 2 ? sorted[middle] : (sorted[middle - 1] + sorted[middle]) / 2;
}

type HeartSample = { time: number; bpm: number };

function heartSamplesFor(
  target: Observation,
  observations: Observation[],
  interval?: { start?: number; end?: number }
): { samples: HeartSample[]; conflicts: number } {
  const targetOrigin = originOf(target);
  const targetInterval = interval ?? intervalFor(target);
  const byTime = new Map<number, Set<number>>();
  for (const observation of observations) {
    if (observation.source_kind !== 'health_connect' || observation.device_id !== target.device_id) continue;
    if (originOf(observation) !== targetOrigin || recordType(observation) !== 'heart_rate') continue;
    const samples = payloadFor(observation).samples;
    if (!Array.isArray(samples)) continue;
    for (const value of samples) {
      const item = object(value);
      const time = parseTime(item.time);
      const bpm = item.beats_per_minute;
      if (time === undefined || typeof bpm !== 'number' || !Number.isFinite(bpm) || bpm <= 0) continue;
      if (targetInterval.start !== undefined && time < targetInterval.start) continue;
      if (targetInterval.end !== undefined && time >= targetInterval.end) continue;
      const values = byTime.get(time) ?? new Set<number>();
      values.add(bpm);
      byTime.set(time, values);
    }
  }
  const samples: HeartSample[] = [];
  let conflicts = 0;
  for (const [time, values] of byTime) {
    if (values.size !== 1) { conflicts++; continue; }
    samples.push({ time, bpm: [...values][0] });
  }
  return { samples: samples.sort((left, right) => left.time - right.time), conflicts };
}

function stepCountInsideExercise(target: Observation, observations: Observation[], interval: { start?: number; end?: number }): number | undefined {
  if (interval.start === undefined || interval.end === undefined) return undefined;
  const targetOrigin = originOf(target);
  const records: { start: number; end: number; count: number }[] = [];
  for (const observation of observations) {
    if (observation.source_kind !== 'health_connect' || observation.device_id !== target.device_id) continue;
    if (originOf(observation) !== targetOrigin || recordType(observation) !== 'steps') continue;
    const sourceInterval = intervalFor(observation);
    const count = payloadFor(observation).count;
    if (sourceInterval.start === undefined || sourceInterval.end === undefined || sourceInterval.end <= sourceInterval.start) continue;
    if (sourceInterval.start < interval.start || sourceInterval.end > interval.end) continue;
    if (typeof count !== 'number' || !Number.isFinite(count) || count < 0) continue;
    records.push({ start: sourceInterval.start, end: sourceInterval.end, count });
  }
  records.sort((left, right) => left.start - right.start || left.end - right.end);
  for (let index = 1; index < records.length; index += 1) {
    if (records[index].start < records[index - 1].end) return undefined;
  }
  return records.length ? records.reduce((total, record) => total + record.count, 0) : undefined;
}

function addHeartFields(fields: EventField[], target: Observation, observations: Observation[], interval?: { start?: number; end?: number }) {
  const result = heartSamplesFor(target, observations, interval);
  if (!result.samples.length) return;
  const values = result.samples.map(sample => sample.bpm);
  fields.push({ label: 'Heart-rate samples', value: values.length.toLocaleString() });
  fields.push({ label: 'Heart-rate range', value: `${Math.min(...values)}–${Math.max(...values)} bpm` });
  fields.push({ label: 'Heart-rate median', value: `${median(values).toLocaleString()} bpm` });
}

function describeHealthEvent(observation: Observation, observations: Observation[]): EventDescription {
  const payload = payloadFor(observation);
  const type = recordType(observation);
  const interval = intervalFor(observation);
  const fields = intervalFields(interval);
  const origin = originLabel(originOf(observation));

  if (type === 'sleep_session') {
    const stages = sleepStages(observation, interval);
    const totals = new Map<string, number>();
    for (const stage of stages) totals.set(stage.label, (totals.get(stage.label) ?? 0) + stage.end - stage.start);
    const stageOrder = ['Light', 'Deep', 'REM', 'Awake', 'Sleeping', 'Unknown'];
    const staged = stages.reduce((total, stage) => total + stage.end - stage.start, 0);
    for (const label of stageOrder) {
      const total = totals.get(label);
      if (total !== undefined) fields.push({ label, value: formatDuration(total) });
    }
    if (stages.length) {
      fields.push({ label: 'Staged time', value: `${formatDuration(staged)} / ${formatDuration(interval.start !== undefined && interval.end !== undefined ? interval.end - interval.start : undefined)}` });
    }
    addHeartFields(fields, observation, observations, interval);
    return {
      title: 'Sleep',
      subtitle: `${stages.length ? `${stages.length} stages · ` : ''}${displayString(origin, 'Health Connect')}`,
      fields,
      stages
    };
  }

  if (type === 'exercise_session') {
    const exerciseType = payload.exercise_type;
    const title = exerciseType === 79 ? 'Walking' : exerciseType === 8 ? 'Cycling' : nonEmptyString(payload.title) ?? (typeof exerciseType === 'number' ? `Exercise ${exerciseType}` : 'Exercise');
    const notes = nonEmptyString(payload.notes);
    const steps = stepCountInsideExercise(observation, observations, interval);
    fields.unshift({ label: 'Type', value: typeof exerciseType === 'number' ? `${title} (${exerciseType})` : title });
    fields.push({ label: 'Steps', value: steps === undefined ? '—' : steps.toLocaleString() });
    fields.push({ label: 'Route', value: '—' });
    if(notes) fields.push({ label: 'Notes', value: notes });
    if(Array.isArray(payload.laps) && payload.laps.length) fields.push({ label: 'Laps', value: payload.laps.length.toLocaleString() });
    addHeartFields(fields, observation, observations, interval);
    return { title, subtitle: `${displayString(origin, 'Health Connect')} · ${formatDuration(interval.start !== undefined && interval.end !== undefined ? interval.end - interval.start : undefined)}`, fields, stages: [] };
  }

  if (type === 'heart_rate') {
    const result = heartSamplesFor(observation, observations, interval);
    const values = result.samples.map(sample => sample.bpm);
    const fieldList = values.length ? [
      { label: 'Heart-rate samples', value: values.length.toLocaleString() },
      { label: 'Heart-rate range', value: `${Math.min(...values)}–${Math.max(...values)} bpm` },
      { label: 'Heart-rate median', value: `${median(values).toLocaleString()} bpm` }
    ] : [];
    fields.push(...fieldList);
    return { title: 'Heart rate', subtitle: `${displayString(origin, 'Health Connect')}${result.conflicts ? ` · ${result.conflicts} conflicting timestamps omitted` : ''}`, fields, stages: [] };
  }

  if (type === 'steps') {
    const count = payload.count;
    fields.unshift({ label: 'Count', value: typeof count === 'number' && Number.isFinite(count) ? count.toLocaleString() : '—' });
    return { title: 'Steps', subtitle: displayString(origin, 'Health Connect'), fields, stages: [] };
  }

  return { title: type, subtitle: displayString(origin, 'Health Connect'), fields, stages: [] };
}

function describeActivityWatch(observation: Observation): EventDescription {
  const data = dataFor(observation);
  const interval = intervalFor(observation);
  const fields = intervalFields(interval);
  const app = nonEmptyString(data.app);
  const title = nonEmptyString(data.title);
  const url = nonEmptyString(data.url);
  const project = projectName(data.project_path) ?? projectName(data.cwd);
  if(data.status === 'afk' || data.status === 'not-afk') return {title:data.status === 'afk' ? 'Idle' : 'Active',subtitle:'ActivityWatch',fields,stages:[]};
  const isCodex = Boolean(project || data.model || data.chat_title);
  const eventTitle = isCodex ? (nonEmptyString(data.chat_title) ?? project ?? app ?? 'Codex') : (app ?? title ?? url ?? 'ActivityWatch');
  const subtitleParts = [isCodex ? (app ?? 'Codex') : undefined, !isCodex && title && title !== eventTitle ? title : undefined, nonEmptyString(data.model), nonEmptyString(data.status)].filter(Boolean);
  if (title && title !== eventTitle) fields.push({ label: 'Title', value: title });
  if (url) fields.push({ label: 'URL', value: url });
  if (app && app !== eventTitle) fields.push({ label: 'Application', value: app });
  if (project) fields.push({ label: 'Project', value: project });
  if (nonEmptyString(data.chat_title) && nonEmptyString(data.chat_title) !== eventTitle) fields.push({ label: 'Chat', value: nonEmptyString(data.chat_title)! });
  if (nonEmptyString(data.model)) fields.push({ label: 'Model', value: nonEmptyString(data.model)! });
  if (nonEmptyString(data.status)) fields.push({ label: 'Status', value: nonEmptyString(data.status)! });
  return { title: eventTitle, subtitle: subtitleParts.join(' · '), fields, stages: [] };
}

function describeAndroidUsage(observation: Observation, observations: Observation[]): EventDescription {
  const payload = payloadFor(observation);
  const packageName = nonEmptyString(payload.package_name) ?? 'Unknown package';
  const className = nonEmptyString(payload.class_name);
  const eventTime = parseTime(payload.event_time) ?? parseTime(observation.occurred_at);
  const eventType = nonEmptyString(payload.event_type)?.replaceAll('_', ' ') ?? 'event';
  const fields: EventField[] = [];
  if (payload.event_type === 'activity_resumed' && eventTime !== undefined) {
    const following = observations
      .filter(candidate => candidate.source_kind === 'android_usage')
      .filter(candidate => candidate.source_instance_key === observation.source_instance_key && candidate.device_id === observation.device_id)
      .filter(candidate => {
        const candidatePayload = payloadFor(candidate);
        return nonEmptyString(candidatePayload.package_name) === packageName &&
          nonEmptyString(candidatePayload.class_name) === className;
      })
      .map(candidate => ({ observation: candidate, payload: payloadFor(candidate), time: parseTime(payloadFor(candidate).event_time) ?? parseTime(candidate.occurred_at) }))
      .filter((candidate): candidate is { observation: Observation; payload: RecordValue; time: number } => candidate.time !== undefined && candidate.time > eventTime)
      .sort((left, right) => left.time - right.time);
    const pause = following.find(candidate => ['activity_paused', 'activity_resumed'].includes(String(candidate.payload.event_type)));
    if (pause?.payload.event_type === 'activity_paused') fields.push(...intervalFields({ start: eventTime, end: pause.time }));
  }
  if (!fields.length) fields.push({ label: 'Time', value: formatDateTime(eventTime) });
  fields.push({ label: 'Package', value: packageName });
  if (className) fields.push({ label: 'Activity', value: className });
  return { title: prettyPackage(packageName), subtitle: eventType, fields, stages: [] };
}

function describeGeneric(observation: Observation): EventDescription {
  const interval = intervalFor(observation);
  return {
    title: observation.kind || observation.source_kind,
    subtitle: observation.source_kind,
    fields: intervalFields(interval),
    stages: []
  };
}

export function describeEvent(observation: Observation, observations: Observation[] = []): EventDescription {
  if (observation.source_kind === 'health_connect') return describeHealthEvent(observation, [observation, ...observations]);
  if (observation.source_kind === 'activitywatch') return describeActivityWatch(observation);
  if (observation.source_kind === 'android_usage') return describeAndroidUsage(observation, [observation, ...observations]);
  return describeGeneric(observation);
}
