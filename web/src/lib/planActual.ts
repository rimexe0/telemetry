import type { Activity, Observation, PlanBlock, SourceSummary } from './api';

export type PlanAssessmentStatus = 'consistent' | 'possible_drift' | 'unknown';
export type EvidenceRelationship = 'matched' | 'unmatched' | 'ambiguous' | 'context_only';

export type PlanEvidence = {
  source_instance_key: string;
  source_kind: string;
  source_record_key: string;
  kind: string;
  occurred_at: string;
  ended_at?: string;
  collected_at: string;
  summary: string;
  relationship: EvidenceRelationship;
  origin: 'observed_device_activity' | 'observed_agent_activity';
  related_activity_id?: number;
  related_activity_title?: string;
};

export type PlanAssessment = {
  status: PlanAssessmentStatus;
  source_freshness: 'fresh' | 'stale' | 'missing';
  freshness_note: string;
  reason: string;
  inference: string;
  evidence: PlanEvidence[];
};

const freshnessWindowMs = 15 * 60_000;
const stopWords = new Set(['the', 'and', 'for', 'with', 'from', 'this', 'that', 'open', 'page', 'work', 'task', 'plan', 'session']);

const objectValue = (value: unknown): Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {};

const stringValue = (value: unknown): string => typeof value === 'string' ? value : '';

const normalise = (value: string): string => value.toLocaleLowerCase().replace(/[^\p{L}\p{N}]+/gu, ' ').trim();

const termsFrom = (value: string): string[] => [...new Set(normalise(value).split(/\s+/).filter(term => term.length >= 3 && !stopWords.has(term)))];

function payloadText(observation: Observation): { text: string; summary: string } {
  const payload = objectValue(observation.original_payload);
  if (observation.source_kind === 'android_usage') {
    const packageName = stringValue(payload.package_name) || 'Unknown package';
    const className = stringValue(payload.class_name);
    const eventType = stringValue(payload.event_type).replaceAll('_', ' ');
    return {
      text: normalise([packageName, className, eventType].join(' ')),
      summary: `${packageName}${className ? ` · ${className}` : ''}${eventType ? ` · ${eventType}` : ''}`
    };
  }
  const data = objectValue(payload.data);
  const values = [
    stringValue(data.app),
    stringValue(data.title),
    stringValue(data.url),
    stringValue(data.project_path),
    stringValue(data.cwd),
    stringValue(data.chat_title),
    stringValue(data.model),
    stringValue(data.status)
  ].filter(Boolean);
  return {
    text: normalise(values.join(' ')),
    summary: values.slice(0, 3).join(' · ') || observation.kind || observation.source_kind
  };
}

function evidenceOrigin(observation: Observation): PlanEvidence['origin'] {
  return observation.source_kind === 'activitywatch' && observation.source_instance_key.includes('aw-watcher-codex')
    ? 'observed_agent_activity'
    : 'observed_device_activity';
}

function isHumanActivity(observation: Observation): boolean {
  if (observation.source_kind === 'android_usage') return true;
  if (observation.source_kind !== 'activitywatch') return false;
  return !observation.source_instance_key.includes('aw-watcher-codex');
}

function relatedActivityForEvidence(text: string, activities: Activity[], currentActivityId: number | undefined): Activity | undefined {
  const evidenceTerms = new Set(text.split(/\s+/).filter(Boolean));
  const matches = activities.filter(candidate => {
    if (candidate.id === currentActivityId || ['done', 'completed', 'abandoned'].includes(candidate.status)) return false;
    const titleTerms = termsFrom(candidate.title);
    return titleTerms.length > 0 && titleTerms.every(term => evidenceTerms.has(term));
  });
  return matches.length === 1 ? matches[0] : undefined;
}

function overlaps(block: PlanBlock, observation: Observation): boolean {
  const start = Date.parse(block.start_time);
  const end = start + block.duration_minutes * 60_000;
  const observedStart = Date.parse(observation.occurred_at);
  const observedEnd = Date.parse(observation.ended_at ?? observation.occurred_at);
  return Number.isFinite(start) && Number.isFinite(observedStart) && observedStart < end && observedEnd >= start;
}

function sourceFreshness(sources: SourceSummary[], now: number): { state: PlanAssessment['source_freshness']; note: string } {
  const relevant = sources.filter(source => source.source_kind === 'activitywatch' || source.source_kind === 'android_usage');
  if (!relevant.length) return { state: 'missing', note: 'No ActivityWatch or Android activity source is available.' };
  const details = relevant.map(source => {
    const at = source.last_collected_at ? Date.parse(source.last_collected_at) : Number.NaN;
    if (!Number.isFinite(at)) return `${source.source_kind} has no recent receipt`;
    const age = Math.max(0, Math.round((now - at) / 60_000));
    return `${source.source_kind} ${age < 1 ? 'now' : `${age}m ago`}`;
  });
  const fresh = relevant.some(source => {
    const at = source.last_collected_at ? Date.parse(source.last_collected_at) : Number.NaN;
    return Number.isFinite(at) && now - at <= freshnessWindowMs;
  });
  return { state: fresh ? 'fresh' : 'stale', note: details.join(', ') };
}

export function assessPlanBlock(
  block: PlanBlock,
  activity: Activity | undefined,
  observations: Observation[],
  sources: SourceSummary[],
  activities: Activity[] = [],
  now = Date.now()
): PlanAssessment {
  const freshness = sourceFreshness(sources, now);
  const relevant = observations.filter(observation => overlaps(block, observation));
  const terms = termsFrom([
    block.title,
    activity?.title ?? '',
    activity?.next_action ?? '',
    activity?.resume_marker ?? '',
    activity?.notes ?? ''
  ].join(' '));
  const evidence = relevant.slice(0, 24).map(observation => {
    const content = payloadText(observation);
    const matched = terms.length > 0 && terms.some(term => content.text.includes(term));
    const relatedActivity = isHumanActivity(observation) && !matched
      ? relatedActivityForEvidence(content.text, activities, activity?.id ?? block.activity_id)
      : undefined;
    const relationship: EvidenceRelationship = !isHumanActivity(observation)
      ? 'context_only'
      : matched ? 'matched' : relatedActivity ? 'unmatched' : 'ambiguous';
    return {
      source_instance_key: observation.source_instance_key,
      source_kind: observation.source_kind,
      source_record_key: observation.source_record_key,
      kind: observation.kind,
      occurred_at: observation.occurred_at,
      ...(observation.ended_at ? { ended_at: observation.ended_at } : {}),
      collected_at: observation.collected_at,
      summary: content.summary,
      relationship,
      origin: evidenceOrigin(observation),
      ...(relatedActivity ? { related_activity_id: relatedActivity.id, related_activity_title: relatedActivity.title } : {})
    } satisfies PlanEvidence;
  });

  if (freshness.state !== 'fresh') {
    return {
      status: 'unknown',
      source_freshness: freshness.state,
      freshness_note: freshness.note,
      reason: freshness.state === 'missing' ? 'Activity evidence is unavailable.' : 'The available activity source is stale; this block is unknown.',
      inference: 'No completion or distraction claim is made from missing or stale telemetry.',
      evidence
    };
  }

  const humanEvidence = evidence.filter(item => item.origin === 'observed_device_activity');
  const matched = humanEvidence.filter(item => item.relationship === 'matched');
  const unrelated = humanEvidence.filter(item => item.relationship === 'unmatched');
  if (matched.length) {
    return {
      status: 'consistent',
      source_freshness: freshness.state,
      freshness_note: freshness.note,
      reason: 'Observed device activity contains terms related to this planned activity.',
      inference: 'This is consistent with the plan. It does not prove that the task was completed.',
      evidence
    };
  }
  if (unrelated.length) {
    return {
      status: 'possible_drift',
      source_freshness: freshness.state,
      freshness_note: freshness.note,
      reason: 'Observed device activity matches another configured activity.',
      inference: 'This may be intentional switching, useful interleaving, waiting, or possible drift. Ask what changed.',
      evidence
    };
  }
  if (humanEvidence.length) {
    return {
      status: 'unknown',
      source_freshness: freshness.state,
      freshness_note: freshness.note,
      reason: 'Observed device activity is not linked to this plan or another configured activity.',
      inference: 'No drift claim is made from unmatched app or window text alone.',
      evidence
    };
  }
  if (evidence.length) {
    return {
      status: 'unknown',
      source_freshness: freshness.state,
      freshness_note: freshness.note,
      reason: 'Only agent activity was observed; it does not establish human attention.',
      inference: 'Agent runtime remains separate from task completion or owner attention.',
      evidence
    };
  }
  return {
    status: 'unknown',
    source_freshness: freshness.state,
    freshness_note: freshness.note,
    reason: 'No activity record overlaps this planned block.',
    inference: 'The missing interval is unknown, not a failed task.',
    evidence
  };
}
