import type { CollectionDevice, Observation, SourceSummary } from './api';

// Display tolerance for the existing five-minute collector reports. This never
// changes a device's collection policy or claims missing source events are zero.
export const REPORT_DELAY_MS = 15 * 60_000;
export type ReportState = 'recent' | 'delayed' | 'missing' | 'clock' | 'unreadable';
type Payload = Record<string, unknown>;

function object(value: unknown): Payload | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Payload : null;
}
function number(value: unknown, minimum = 0, maximum = Infinity): number | null {
  return typeof value === 'number' && Number.isFinite(value) && value >= minimum && value <= maximum ? value : null;
}
function text(value: unknown): string | null {
  return typeof value === 'string' && value.length > 0 ? value.slice(0, 200) : null;
}
function bool(value: unknown): boolean | null { return typeof value === 'boolean' ? value : null; }

export function resourceSample(observation: Observation) {
  const raw = object(observation.original_payload);
  const p = raw ?? {};
  const states = object(p.source_states) ?? {};
  return {
    observation, at: Date.parse(observation.collected_at), readable: raw !== null,
    pending: number(p.pending_revisions), delivery: text(p.delivery_state),
    oldestPendingAt: text(p.oldest_pending_collected_at), lastUploadAt: text(p.last_successful_delivery_at),
    battery: number(p.battery_level_percent, 0, 100), temperature: number(p.battery_temperature_c, -100, 200),
    charging: bool(p.charging), cpu: number(p.process_cpu_percent, 0, 100), rss: number(p.process_rss_bytes),
    tx: number(p.uid_tx_bytes), rx: number(p.uid_rx_bytes),
    attemptedBytes: number(p.attempted_upload_body_bytes), meteredBytes: number(p.metered_body_bytes_today),
    failedBatches: number(p.failed_upload_batches), metered: bool(p.network_metered),
    connected: bool(p.network_available), foreground: bool(p.foreground_active),
    cycleSeconds: number(p.cycle_interval_seconds, 1), healthMinutes: number(p.health_interval_minutes, 1),
    constrainedReason: text(p.constrained_reason), lastLocationAt: text(p.last_location_fix_at),
    states: Object.entries(states).slice(0, 64).flatMap(([name, value]) =>
      typeof value === 'string' ? [{ name, value: value.slice(0, 200) }] : [])
  };
}
export type ResourceSample = ReturnType<typeof resourceSample>;

export function collectionDevice(device: CollectionDevice, now: number) {
  const samples = device.resource_history.filter(row => row.device_id === device.device_id &&
    row.source_kind === 'telemetry_collector' && row.kind === 'telemetry.collector_status')
    .map(resourceSample).sort((a, b) => (a.at || 0) - (b.at || 0));
  const latest = samples.at(-1);
  let reportState: ReportState = 'missing';
  if (latest) {
    reportState = !Number.isFinite(latest.at) || latest.at > now + 120_000 ? 'clock'
      : !latest.readable ? 'unreadable' : now - latest.at > REPORT_DELAY_MS ? 'delayed' : 'recent';
  }
  return { device, samples, latest, reportState };
}

export const reportLabels: Record<ReportState, string> = {
  recent: 'Recent report', delayed: 'Report delayed', missing: 'Reports unavailable',
  clock: 'Check device clock', unreadable: 'Unreadable report'
};

const labels: Record<string, string> = {
  delivered: 'Delivered', queue_empty: 'Queue empty', offline: 'Offline', pairing_required: 'Pairing required',
  retry_backoff: 'Waiting to retry', catching_up: 'Catching up', metered_daily_limit: 'Daily upload limit reached',
  permission_required: 'Permission required', background_permission_required: 'Background permission required',
  permission_missing: 'Permission missing', disabled: 'Disabled', not_started: 'Not started',
  provider_unavailable: 'Provider unavailable', empty_initial: 'Initial inbox empty',
  partial: 'More records to check', cursor_write_error: 'Progress could not be saved',
  query_timeout: 'Read timed out',
  stopped: 'Stopped', unavailable: 'Unavailable', unchanged: 'No new records', ok: 'Collected',
  records_observed: 'Records available', empty: 'No records returned', initial_empty: 'Initial window empty',
  empty_initial_window: 'Initial window empty', listening: 'Listening', fix_received: 'Fix received',
  stale_service_check: 'Service check delayed', waiting_for_fix: 'Waiting for a fix',
  thermal: 'Thermal limit', battery: 'Low battery', battery_saver: 'Battery Saver'
};
export function stateLabel(value: string | null): string {
  return value === null ? 'Unknown' : labels[value] ?? value.replaceAll('_', ' ');
}
export function stateNeedsAttention(value: string): boolean {
  return /permission|error|failed|stale|unavailable|pairing|retry|offline|limit|timeout|delivery_/i.test(value);
}
export function sourceName(source: SourceSummary): string {
  const key = source.source_instance_key;
  if (key.includes('aw-watcher-window')) return 'Computer windows';
  if (key.includes('aw-watcher-afk')) return 'Keyboard / mouse';
  if (key.includes('aw-watcher-web')) return 'Browser tabs';
  if (key.includes('aw-watcher-codex')) return 'Codex';
  return ({ health_connect: 'Health Connect', android_usage: 'Phone apps', android_device: 'Device state',
    android_location: 'Location', android_notifications: 'Notifications', android_sms: 'SMS inbox',
    telemetry_collector: 'Collector reports', t3code: 'T3 Code', git: 'Local Git',
    github_repository: 'GitHub', opennutritracker: 'Nutrition', koreader: 'Reading'
  } as Record<string, string>)[source.source_kind] ?? source.source_kind;
}
export function stateName(name: string): string {
  const availability = name.endsWith('_availability');
  const key = availability ? name.slice(0, -13) : name;
  const label = ({ usage: 'Phone apps', device: 'Device state', steps: 'Steps', heart_rate: 'Heart rate',
    sleep_session: 'Sleep', exercise_session: 'Exercise', location: 'Location', notifications: 'Notifications',
    sms: 'SMS inbox'
  } as Record<string, string>)[key] ?? key.replaceAll('_', ' ');
  return availability ? `${label} availability` : label;
}
export function bytes(value: number | null): string {
  if (value === null) return 'Unknown';
  if (value < 1024) return `${value.toLocaleString()} B`;
  if (value < 1024 ** 2) return `${(value / 1024).toFixed(1)} KiB`;
  if (value < 1024 ** 3) return `${(value / 1024 ** 2).toFixed(1)} MiB`;
  return `${(value / 1024 ** 3).toFixed(2)} GiB`;
}
export function when(value?: string | null): string {
  const at = value ? Date.parse(value) : NaN;
  return Number.isFinite(at) ? new Intl.DateTimeFormat(undefined, {
    month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', timeZoneName: 'short'
  }).format(new Date(at)) : 'Unknown';
}

// Points are actual observations, with time-spaced positions. No line or fill
// interpolates a queue or temperature through periods without reports.
export function samplePoints(samples: ResourceSample[], metric: 'temperature' | 'pending') {
  const values = samples.filter(s => Number.isFinite(s.at) && s[metric] !== null);
  if (values.length === 0) return { points: [], minimum: null, maximum: null };
  const minimum = Math.min(...values.map(s => s[metric]!));
  const maximum = Math.max(...values.map(s => s[metric]!));
  const start = values[0].at, span = Math.max(1, values.at(-1)!.at - start);
  return { minimum, maximum, points: values.map(s => ({
    x: values.length === 1 ? 150 : 5 + (s.at - start) / span * 290,
    y: maximum === minimum ? 30 : 55 - (s[metric]! - minimum) / (maximum - minimum) * 50,
    at: s.observation.collected_at, value: s[metric]!
  })) };
}
