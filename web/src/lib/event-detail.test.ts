import { expect, test } from 'vitest';
import type { Observation } from './api';
import { describeEvent } from './event-detail';

let id = 0;

function health(type: string, start: string, end: string, extra: Record<string, unknown> = {}, origin = 'com.sec.android.app.shealth'): Observation {
  return {
    source_kind: 'health_connect',
    source_instance_key: 'health:phone',
    device_id: 'phone',
    source_record_key: `${type}-${++id}`,
    kind: type,
    occurred_at: start,
    ended_at: end,
    collected_at: end,
    revision_digest: 'digest',
    projection: {},
    original_payload: { record_type: type, metadata: { data_origin: origin }, start_time: start, end_time: end, ...extra }
  };
}

function activity(data: Record<string, unknown>, start = '2026-09-07T10:00:00+03:00', end = '2026-09-07T10:25:00+03:00'): Observation {
  return {
    source_kind: 'activitywatch',
    source_instance_key: 'aw-watcher-window',
    device_id: 'mac',
    source_record_key: `aw-${++id}`,
    kind: 'activitywatch',
    occurred_at: start,
    ended_at: end,
    collected_at: end,
    revision_digest: 'digest',
    projection: {},
    original_payload: { timestamp: start, duration: 1500, data }
  };
}

test('exercise detail names walking, uses its interval, and reconciles steps and heart rate', () => {
  const exercise = health('exercise_session', '2026-09-07T10:00:00+03:00', '2026-09-07T11:00:00+03:00', { exercise_type: 79, title: null, notes: null, laps: [], segments: [], unread_fields: ['exercise_route'] });
  const stepsA = health('steps', '2026-09-07T10:10:00+03:00', '2026-09-07T10:20:00+03:00', { count: 100 });
  const stepsB = health('steps', '2026-09-07T10:20:00+03:00', '2026-09-07T10:30:00+03:00', { count: 200 });
  const heartA = health('heart_rate', '2026-09-07T10:00:00+03:00', '2026-09-07T11:00:00+03:00', { samples: [
    { time: '2026-09-07T10:15:00+03:00', beats_per_minute: 100 },
    { time: '2026-09-07T10:20:00+03:00', beats_per_minute: 110 },
    { time: '2026-09-07T10:40:00+03:00', beats_per_minute: 80 }
  ] });
  const heartB = health('heart_rate', '2026-09-07T10:00:00+03:00', '2026-09-07T11:00:00+03:00', { samples: [
    { time: '2026-09-07T10:15:00+03:00', beats_per_minute: 100 },
    { time: '2026-09-07T10:20:00+03:00', beats_per_minute: 120 }
  ] });

  const view = describeEvent(exercise, [stepsA, stepsB, heartA, heartB]);

  expect(view.title).toBe('Walking');
  expect(view.fields.find(field => field.label === 'Duration')?.value).toBe('1h');
  expect(view.fields.find(field => field.label === 'Steps')?.value).toBe('300');
  expect(view.fields.find(field => field.label === 'Route')?.value).toBe('—');
  expect(view.fields.find(field => field.label === 'Heart-rate samples')?.value).toBe('2');
  expect(view.fields.find(field => field.label === 'Heart-rate range')?.value).toBe('80–100 bpm');
  expect(view.fields.find(field => field.label === 'Heart-rate median')?.value).toBe('90 bpm');
});

test('overlapping step records withhold the exercise step number', () => {
  const exercise = health('exercise_session', '2026-09-07T10:00:00+03:00', '2026-09-07T11:00:00+03:00', { exercise_type: 79 });
  const first = health('steps', '2026-09-07T10:10:00+03:00', '2026-09-07T10:30:00+03:00', { count: 100 });
  const second = health('steps', '2026-09-07T10:20:00+03:00', '2026-09-07T10:40:00+03:00', { count: 200 });
  expect(describeEvent(exercise, [first, second]).fields.find(field => field.label === 'Steps')?.value).toBe('—');
});

test('sleep stages map official Health Connect values, clip to the session, and merge duplicates', () => {
  const sleep = health('sleep_session', '2026-09-07T00:00:00+03:00', '2026-09-07T04:00:00+03:00', { stages: [
    { start_time: '2026-09-07T00:00:00+03:00', end_time: '2026-09-07T01:00:00+03:00', stage: 4 },
    { start_time: '2026-09-07T00:00:00+03:00', end_time: '2026-09-07T01:00:00+03:00', stage: 4 },
    { start_time: '2026-09-07T01:00:00+03:00', end_time: '2026-09-07T02:00:00+03:00', stage: 5 },
    { start_time: '2026-09-07T02:00:00+03:00', end_time: '2026-09-07T03:00:00+03:00', stage: 6 },
    { start_time: '2026-09-07T03:00:00+03:00', end_time: '2026-09-07T03:30:00+03:00', stage: 1 },
    { start_time: '2026-09-07T03:30:00+03:00', end_time: '2026-09-07T05:00:00+03:00', stage: 5 }
  ] });

  const view = describeEvent(sleep);
  expect(view.title).toBe('Sleep');
  expect(view.stages.map(stage => stage.label)).toEqual(['Light', 'Deep', 'REM', 'Awake', 'Deep']);
  expect(view.stages.at(-1)).toMatchObject({ end: Date.parse('2026-09-07T04:00:00+03:00') });
  expect(view.fields.find(field => field.label === 'Light')?.value).toBe('1h');
  expect(view.fields.find(field => field.label === 'Deep')?.value).toBe('1h 30m');
  expect(view.fields.find(field => field.label === 'REM')?.value).toBe('1h');
  expect(view.fields.find(field => field.label === 'Awake')?.value).toBe('30m');
  expect(view.fields.find(field => field.label === 'Staged time')?.value).toBe('4h / 4h');
});

test('activitywatch and Android usage details expose the actual event context', () => {
  const codex = activity({ app: 'Codex', cwd: '/Users/rime/code/personal/Telemetry', model: 'gpt-5.6-luna', chat_title: 'Health timeline', status: 'working' });
  const phone: Observation = {
    source_kind: 'android_usage', source_instance_key: 'android:usage', device_id: 'phone', source_record_key: 'usage-1', kind: 'usage',
    occurred_at: '2026-09-07T12:00:00+03:00', collected_at: '2026-09-07T12:00:01+03:00', revision_digest: 'digest', projection: {},
    original_payload: { event_type: 'activity_resumed', package_name: 'com.opennutritracker.ont.opennutritracker', class_name: 'MainActivity', event_time: '2026-09-07T12:00:00+03:00' }
  };
  expect(describeEvent(codex).title).toBe('Health timeline');
  expect(describeEvent(codex).fields).toEqual(expect.arrayContaining([
    { label: 'Project', value: 'Telemetry' }, { label: 'Model', value: 'gpt-5.6-luna' }, { label: 'Status', value: 'working' }
  ]));
  expect(describeEvent(phone).title).toBe('OpenNutriTracker');
  expect(describeEvent(phone).fields).toEqual(expect.arrayContaining([
    { label: 'Package', value: 'com.opennutritracker.ont.opennutritracker' }, { label: 'Activity', value: 'MainActivity' }
  ]));
});

test('Android resumed events use the matching pause as their actual interval', () => {
  const resumed: Observation = {
    source_kind: 'android_usage', source_instance_key: 'android:usage', device_id: 'phone', source_record_key: 'usage-resume', kind: 'usage',
    occurred_at: '2026-09-07T12:00:00+03:00', collected_at: '2026-09-07T12:00:01+03:00', revision_digest: 'digest', projection: {},
    original_payload: { event_type: 'activity_resumed', package_name: 'org.mozilla.firefox', class_name: 'BrowserActivity', event_time: '2026-09-07T12:00:00+03:00' }
  };
  const paused: Observation = {
    ...resumed,
    source_record_key: 'usage-pause',
    occurred_at: '2026-09-07T12:18:00+03:00',
    original_payload: { event_type: 'activity_paused', package_name: 'org.mozilla.firefox', class_name: 'BrowserActivity', event_time: '2026-09-07T12:18:00+03:00' }
  };
  const view = describeEvent(resumed, [paused]);
  expect(view.fields.find(field => field.label === 'Started')?.value).toContain('12:00');
  expect(view.fields.find(field => field.label === 'Ended')?.value).toContain('12:18');
  expect(view.fields.find(field => field.label === 'Duration')?.value).toBe('18m');
});

test('phone pairing stops at a repeated resume instead of borrowing a later pause', () => {
 const row=(minute:string,event_type:string):Observation=>({ ...activity({}),source_kind:'android_usage',source_instance_key:'phone',occurred_at:`2026-09-07T12:${minute}:00+03:00`,original_payload:{event_type,package_name:'app',class_name:'main'} });
 const first=row('00','activity_resumed'),second=row('05','activity_resumed'),pause=row('10','activity_paused');
 expect(describeEvent(first,[first,second,pause]).fields.some(f=>f.label==='Duration')).toBe(false);
 expect(describeEvent(second,[first,second,pause]).fields.find(f=>f.label==='Duration')?.value).toBe('5m');
});
test('cycling and ActivityWatch idle state have readable names',()=>{
 expect(describeEvent(health('exercise_session','2026-09-07T10:00:00Z','2026-09-07T11:00:00Z',{exercise_type:8})).title).toBe('Cycling');
 expect(describeEvent(activity({status:'afk'})).title).toBe('Idle');
});
