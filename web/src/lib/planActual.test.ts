import { describe, expect, it } from 'vitest';
import type { Activity, Observation, PlanBlock, SourceSummary } from './api';
import { assessPlanBlock } from './planActual';

const now = Date.parse('2026-09-20T10:00:00Z');

const activity: Activity = {
  id: 1,
  title: 'Telemetry implementation',
  notes: 'Keep the change focused',
  next_action: 'Run the planning tests',
  resume_marker: 'Continue with the plan panel',
  status: 'active',
  activity_state: 'growth',
  priority: 5,
  created_at: '2026-09-19T10:00:00Z',
  updated_at: '2026-09-19T10:00:00Z'
};

const block: PlanBlock = {
  id: 10,
  activity_id: 1,
  title: 'Telemetry implementation',
  start_time: '2026-09-20T12:00:00+03:00',
  duration_minutes: 60,
  fixed: false,
  state: 'planned',
  state_note: '',
  position: 0
};

const source = (last_collected_at = '2026-09-20T09:55:00Z'): SourceSummary => ({
  source_instance_key: 'activitywatch:mac:aw-watcher-window',
  source_kind: 'activitywatch',
  device_id: 'mac',
  first_collected_at: '2026-09-19T10:00:00Z',
  last_collected_at,
  observation_count: 20
});

const observation = (overrides: Partial<Observation>): Observation => ({
  source_instance_key: 'activitywatch:mac:aw-watcher-window',
  source_kind: 'activitywatch',
  device_id: 'mac',
  source_record_key: 'window-1',
  kind: 'activitywatch.event',
  occurred_at: '2026-09-20T09:15:00Z',
  ended_at: '2026-09-20T09:45:00Z',
  collected_at: '2026-09-20T09:46:00Z',
  revision_digest: 'digest',
  original_payload: { data: { app: 'Code', title: 'Telemetry implementation' } },
  projection: {},
  ...overrides
});

describe('plan-versus-actual assessment', () => {
  it('marks matching device context as consistent without claiming completion', () => {
    const result = assessPlanBlock(block, activity, [observation({})], [source()], now);

    expect(result.status).toBe('consistent');
    expect(result.inference).toContain('does not prove');
    expect(result.evidence[0].relationship).toBe('matched');
    expect(result.evidence[0].origin).toBe('observed_device_activity');
  });

  it('exposes unmatched activity as possible drift while preserving the alternative explanation', () => {
    const result = assessPlanBlock(block, activity, [observation({
      original_payload: { data: { app: 'YouTube', title: 'Music' } }
    })], [source()], now);

    expect(result.status).toBe('possible_drift');
    expect(result.reason).toContain('did not match');
    expect(result.inference).toContain('intentional switching');
    expect(result.evidence[0].relationship).toBe('unmatched');
  });

  it('returns unknown for stale sources instead of inventing drift', () => {
    const result = assessPlanBlock(block, activity, [observation({
      original_payload: { data: { app: 'YouTube', title: 'Music' } }
    })], [source('2026-09-20T08:00:00Z')], now);

    expect(result.status).toBe('unknown');
    expect(result.source_freshness).toBe('stale');
    expect(result.inference).toContain('missing or stale');
  });

  it('returns unknown for a fresh source with no overlapping records', () => {
    const result = assessPlanBlock(block, activity, [], [source()], now);

    expect(result.status).toBe('unknown');
    expect(result.reason).toContain('No activity record');
    expect(result.evidence).toHaveLength(0);
  });

  it('keeps agent runtime separate from human activity', () => {
    const result = assessPlanBlock(block, activity, [observation({
      source_instance_key: 'activitywatch:mac:aw-watcher-codex',
      original_payload: { data: { project_path: '/Users/ri/code/personal/Telemetry', chat_title: 'Telemetry implementation' } }
    })], [source()], now);

    expect(result.status).toBe('unknown');
    expect(result.evidence[0].origin).toBe('observed_agent_activity');
    expect(result.reason).toContain('agent activity');
  });
});
