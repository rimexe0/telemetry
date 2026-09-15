import { expect, test } from 'vitest';
import type { CollectionDevice, Observation } from './api';
import { bytes, collectionDevice, resourceSample, samplePoints, stateLabel } from './collection';

const now = Date.parse('2026-09-12T10:00:00Z');
function report(at: string, payload: Record<string, unknown> = {}, device = 'phone'): Observation {
  return {
    source_instance_key: `android:${device}:collector`, source_kind: 'telemetry_collector',
    device_id: device, source_record_key: at, revision_digest: 'revision-1',
    kind: 'telemetry.collector_status', occurred_at: at, collected_at: at,
    original_payload: payload, projection: payload
  };
}
const device = (rows: Observation[]): CollectionDevice => ({ device_id: 'phone', resource_history: rows });

test('a historical delivered report with an empty queue is delayed, not current health', () => {
  const result = collectionDevice(device([report('2026-09-12T09:00:00Z', {
    pending_revisions: 0, delivery_state: 'delivered'
  })]), now);
  expect(result.reportState).toBe('delayed');
});

test('another device and a non-resource observation cannot become the latest report', () => {
  const mine = report('2026-09-12T09:55:00Z');
  const other = report('2026-09-12T09:59:00Z', {}, 'other');
  const health = { ...report('2026-09-12T09:59:01Z'), source_kind: 'health_connect' };
  expect(collectionDevice(device([other, mine, health]), now).latest?.observation).toBe(mine);
});

test('missing and unsupported counters remain unknown while a recorded zero stays zero', () => {
  const result = resourceSample(report('2026-09-12T09:55:00Z', {
    pending_revisions: 0, uid_tx_bytes: -1, process_cpu_percent: '0.3', charging: null,
    battery_level_percent: 200, battery_temperature_c: 31.5
  }));
  expect(result).toMatchObject({ pending: 0, tx: null, cpu: null, charging: null, battery: null, temperature: 31.5 });
  expect(bytes(null)).toBe('Unknown');
  expect(bytes(0)).toBe('0 B');
});

test('missing, unreadable and future-dated reports do not get a recent-report label', () => {
  expect(collectionDevice(device([]), now).reportState).toBe('missing');
  const invalid = { ...report('2026-09-12T09:59:00Z'), original_payload: null };
  expect(collectionDevice(device([invalid]), now).reportState).toBe('unreadable');
  expect(collectionDevice(device([report('2026-09-13T00:00:00Z')]), now).reportState).toBe('clock');
});

test('an unchanged health check is distinct from an empty source and missing permission', () => {
  expect(stateLabel('unchanged')).toBe('No new records');
  expect(stateLabel('records_observed')).toBe('Records available');
  expect(stateLabel('permission_required')).toBe('Permission required');
});

test('resource reports use chronological order and graph points retain actual time gaps', () => {
  const rows = [report('2026-09-12T09:59:00Z', { pending_revisions: 0 }),
    report('2026-09-12T09:00:00Z', { pending_revisions: 20 }),
    report('2026-09-12T09:05:00Z', { pending_revisions: 10 })];
  const result = collectionDevice(device(rows), now);
  expect(result.latest?.pending).toBe(0);
  const graph = samplePoints(result.samples, 'pending');
  expect(graph.points.map(p => p.value)).toEqual([20, 10, 0]);
  expect(graph.points[1].x).toBeLessThan(35);
  expect(graph.points[2].x).toBe(295);
});
