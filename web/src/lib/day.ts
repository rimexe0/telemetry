import type { Observation, SourceSummary } from './api';
import { dayStart, formatMinutes, healthOrigin, unionMinutes } from './health';
export const clock = (time: number) => new Date(time).toLocaleTimeString('en-GB', { timeZone:'Europe/Istanbul', hour:'2-digit', minute:'2-digit' });
export type DaySegment = {start:number; end:number; label:string; detail:string; evidence:Observation[]; kind:string};
type Lane = {name:string; note:string; segments:DaySegment[]};
const obj=(v:unknown):Record<string,unknown> => v !== null && typeof v==='object' && !Array.isArray(v) ? v as Record<string,unknown> : {};
const text=(v:unknown,fallback='Unknown')=> typeof v==='string' && v ? v : fallback;
const basename=(v:unknown)=>text(v,'Unknown project').split('/').filter(Boolean).at(-1) ?? 'Unknown project';
const dayLength=86_400_000;

// Exact overlap/touch union, retaining input references. Never fill silent gaps.
export function mergeSegments(rows:DaySegment[]):DaySegment[] {
 const out:DaySegment[]=[];
 for(const r of [...rows].sort((a,b)=>a.start-b.start || a.end-b.end)) {
  const last=out.at(-1);
  if(last && last.label===r.label && last.kind===r.kind && last.detail===r.detail && r.start<=last.end) {
   last.end=Math.max(last.end,r.end);last.evidence.push(...r.evidence);
  } else out.push({...r,evidence:[...r.evidence]});
 }
 return out;
}

// Partition foreground/input overlaps before adding durations. Conflicts are
// displayed, not arbitrarily assigned to an app or input state.
export function partitionSegments(rows:DaySegment[]):DaySegment[] {
 const events=new Map<number,{starts:Array<[number,DaySegment]>;ends:number[]}>();
 const eventAt=(time:number)=>{let event=events.get(time);if(!event){event={starts:[],ends:[]};events.set(time,event);}return event;};
 rows.forEach((row,index)=>{
  if(row.end<=row.start)return;
  eventAt(row.start).starts.push([index,row]);
  eventAt(row.end).ends.push(index);
 });
 const points=[...events.keys()].sort((a,b)=>a-b);
 const out:DaySegment[]=[];
 const active=new Map<number,DaySegment>();
 for(let i=0;i<points.length-1;i++) {
  const start=points[i],end=points[i+1];
  const event=events.get(start)!;
  for(const index of event.ends)active.delete(index);
  for(const [index,row] of event.starts)active.set(index,row);
  if(!active.size)continue;
  const overlapping=[...active.entries()].sort(([a],[b])=>a-b).map(([,row])=>row);
  const labels=[...new Set(overlapping.map(r=>r.label))];
  const conflicting=labels.length>1;
  out.push({start,end,label:conflicting?'Conflicting records':labels[0],kind:conflicting?'conflict':overlapping[0].kind,detail:conflicting?`Overlapping source values: ${labels.join(', ')}. Duration is not assigned to an app or input state.`:overlapping[0].detail,evidence:overlapping.flatMap(r=>r.evidence)});
 }
 return mergeSegments(out);
}
const packageName=(p:string)=>({'org.mozilla.firefox':'Firefox','tw.nekomimi.nekogram':'Nekogram','com.ichi2.anki':'Anki','com.android.settings':'Settings','com.google.android.apps.nexuslauncher':'Launcher','com.openai.chatgpt':'ChatGPT','com.google.android.apps.tasks':'Google Tasks','com.opennutritracker.ont.opennutritracker':'OpenNutriTracker','com.sec.android.app.shealth':'Samsung Health','com.android.chrome':'Chrome','com.google.android.calendar':'Calendar','com.google.android.apps.wellbeing':'Digital Wellbeing'}[p] ?? p);

export function deriveDay(observations:Observation[],sources:SourceSummary[],date:string) {
 const lo=dayStart(date),hi=lo+dayLength;
 const lanes:Lane[]=[],chapters:DaySegment[]=[],facts:string[]=[],warnings:string[]=[];
 const appTimes=new Map<string,number>();
 if(!Number.isFinite(lo))return {lanes,chapters,apps:[],facts,warnings:['Choose a valid day.']};
 const unique=[...new Map(observations.map(o=>[`${o.source_instance_key}\0${o.source_record_key}`,o])).values()];
 const groups=new Map<string,Observation[]>();
 for(const o of unique) {const group=groups.get(o.source_instance_key)??[];group.push(o);groups.set(o.source_instance_key,group);}
 function segment(o:Observation,label:string,kind:string,detail:string,point=false):DaySegment|null {
  const start=Date.parse(o.occurred_at),end=point?start:Date.parse(o.ended_at??'');
  if(!Number.isFinite(start)||!Number.isFinite(end)||end<start||start>=hi||end<lo||(!point&&end===lo))return null;
  return {start:Math.max(lo,start),end:Math.min(hi,end),label,kind,detail,evidence:[o]};
 }
 function coverage(rows:DaySegment[]) {
  if(!rows.length)return 'No records in this day; activity is unknown.';
  return `Records ${clock(Math.min(...rows.map(r=>r.start)))}–${clock(Math.max(...rows.map(r=>r.end)))}. Gaps are unknown.`;
 }
 const health=unique.filter(o=>o.source_kind==='health_connect');
 const preferredHealth=health.find(o=>healthOrigin(o)==='com.sec.android.app.shealth') ?? health[0];
 const origin=preferredHealth?healthOrigin(preferredHealth):'';
 const selectedHealth=health.filter(o=>o.source_instance_key===preferredHealth?.source_instance_key&&healthOrigin(o)===origin);
 if(new Set(health.map(o=>`${o.source_instance_key}:${healthOrigin(o)}`)).size>1)warnings.push('Sleep and exercise use one health source and origin to avoid counting mirrored records twice.');
 const sleep:DaySegment[]=[],exercise:DaySegment[]=[];
 for(const o of selectedHealth) {
  const p=obj(o.original_payload);
  if(p.record_type==='sleep_session') {
   const s=segment(o,'Sleep','sleep','');if(s)sleep.push(s);
  }
  if(p.record_type==='exercise_session') {
   // AndroidX ExerciseSessionRecord.EXERCISE_TYPE_WALKING = 79.
   const label=p.exercise_type===79?'Walking':p.exercise_type===8?'Cycling':`Exercise ${p.exercise_type ?? '—'}`;
   const s=segment(o,label,'exercise','');if(s)exercise.push(s);
  }
 }
 lanes.push({name:'Sleep',note:coverage(sleep),segments:mergeSegments(sleep)});
 chapters.push(...mergeSegments(sleep));
 for(const s of mergeSegments(sleep)) {
  const actualEnd=Math.max(...s.evidence.map(o=>Date.parse(o.ended_at??'')));
  if(actualEnd>=lo && actualEnd<hi)chapters.push({start:actualEnd,end:actualEnd,label:'Recorded sleep ends',kind:'inference',detail:'Possible wake time. A sleep-session end does not confirm when you got up.',evidence:s.evidence});
 }
 let windowCount=0;
 for(const [key,rows] of groups) {
  if(key.includes('aw-watcher-window')) {
   windowCount++;
   const parsed=rows.map(o=>segment(o,text(obj(obj(o.original_payload).data).app,'Unknown app'),'foreground',text(obj(obj(o.original_payload).data).title,''))).filter((s):s is DaySegment=>s!==null);
   const intervals=partitionSegments(parsed);
   lanes.push({name:`Computer · ${rows[0].device_id}`,note:coverage(intervals),segments:intervals});
   for(const s of intervals)if(s.kind!=='conflict')appTimes.set(s.label,(appTimes.get(s.label)??0)+(s.end-s.start)/60_000);
   // Narrative blocks group nearby window observations, but keep precise gaps
   // in the lane and explicitly label spans rather than continuous usage.
   const blocks:DaySegment[]=[];
   for(const s of intervals) {
    const last=blocks.at(-1);
    if(last && s.start-last.end<=120_000) {last.end=s.end;last.evidence.push(...s.evidence);}
    else blocks.push({...s,label:'Computer window activity',kind:'computer',evidence:[...s.evidence]});
   }
   for(const b of blocks) {
    const names=[...new Set(b.evidence.map(o=>text(obj(obj(o.original_payload).data).app,'Unknown app')))];
    b.detail=`Window observations include ${names.slice(0,6).join(', ')}${names.length>6?` and ${names.length-6} other apps`:''}. This is a span containing events; short gaps and idle periods are not counted as attention.`;
   }
   chapters.push(...blocks);
   if(intervals.length)facts.push(`Computer window history begins at ${clock(intervals[0].start)} for ${rows[0].device_id}; earlier computer use is unknown.`);
  }
  if(key.includes('aw-watcher-afk')) {
   const parsed=rows.map(o=>{
    const state=obj(obj(o.original_payload).data).status;
    if(state!=='afk'&&state!=='not-afk')return null;
    return segment(o,state==='afk'?'Idle':'Active',state==='afk'?'idle':'input','');
   }).filter((s):s is DaySegment=>s!==null);
   const intervals=partitionSegments(parsed);
   lanes.push({name:`Input · ${rows[0].device_id}`,note:coverage(intervals),segments:intervals});
   const active=unionMinutes(intervals.filter(s=>s.kind==='input'));
   if(active!==null)facts.push(`${formatMinutes(active)} of input-active state is recorded during the covered portion of the day.`);
   chapters.push(...intervals.filter(s=>s.kind==='idle' && s.end-s.start>=180_000));
   for(let i=1;i<intervals.length;i++)if(intervals[i].kind==='input'&&intervals[i-1].kind==='idle')chapters.push({...intervals[i],end:intervals[i].start,label:'Computer input resumes',detail:'Keyboard/mouse activity is reported again. This does not establish a return home.'});
  }
  if(key.includes('aw-watcher-web')) {
   const parsed=rows.map(o=>{
    const url=obj(obj(o.original_payload).data).url;
    let domain='Browser tab';
    try { domain=new URL(text(url)).hostname || 'Local browser page'; } catch { /* Non-web tab URL. */ }
    return segment(o,domain,'browser',text(obj(obj(o.original_payload).data).title,''));
   }).filter((s):s is DaySegment=>s!==null);
   const intervals=partitionSegments(parsed);
   lanes.push({name:'Browser',note:coverage(intervals),segments:intervals});
  }
  if(rows[0].source_kind==='android_usage') {
   const events:DaySegment[]=[];
   const pending=new Map<string,Observation>();
   for(const o of [...rows].sort((a,b)=>Date.parse(a.occurred_at)-Date.parse(b.occurred_at))) {
    const p=obj(o.original_payload),identity=`${text(p.package_name)}\0${text(p.class_name,'')}`;
    if(p.event_type==='activity_resumed') {
     const previous=pending.get(identity);
     if(previous) { const point=segment(previous,packageName(text(p.package_name)),'phone','Opened',true); if(point)events.push(point); }
     pending.set(identity,o);
    } else if(p.event_type==='activity_paused') {
     const begin=pending.get(identity); if(!begin)continue;
     pending.delete(identity);
     const s=segment({...begin,ended_at:o.occurred_at},packageName(text(p.package_name)),'phone',text(p.class_name,''));
     if(s){s.evidence=[begin,o];events.push(s);}
    }
   }
   for(const o of pending.values()) { const p=obj(o.original_payload),s=segment(o,packageName(text(p.package_name)),'phone','Opened',true);if(s)events.push(s); }
   events.sort((a,b)=>a.start-b.start);
   lanes.push({name:`Phone · ${rows[0].device_id}`,note:coverage(events),segments:events});
   const blocks:DaySegment[]=[];
   for(const s of events){const last=blocks.at(-1);if(last&&s.start-last.end<=300_000){last.end=s.end;last.evidence.push(...s.evidence);}else blocks.push({...s,label:'Phone app events',evidence:[...s.evidence]});}
   for(const b of blocks)b.detail=`${[...new Set(b.evidence.map(o=>packageName(text(obj(o.original_payload).package_name))))].slice(0,5).join(', ')}. Event span, not continuous phone-use time.`;
   chapters.push(...blocks);
  }
  if(key.includes('aw-watcher-codex')) {
   const segments=rows.map(o=>{
    const d=obj(obj(o.original_payload).data);
    return segment(o,basename(d.project_path??d.cwd),'agent',text(d.chat_title,''));
   }).filter((s):s is DaySegment=>s!==null);
   lanes.push({name:'Coding agents',note:coverage(segments),segments:mergeSegments(segments)});
  }
 }
 if(!windowCount)warnings.push('No computer window history was returned for this day.');
 lanes.push({name:'Exercise',note:coverage(exercise),segments:mergeSegments(exercise)});
 chapters.push(...mergeSegments(exercise));
 warnings.push('No place or meal interpretation is implemented. These records cannot establish a food trip, destination, or return home.');
 if(!sources.some(s=>/gps|location/.test(s.source_kind)))warnings.push('No GPS/location source has delivered records. A computer idle interval is not evidence that you went outside.');
 warnings.push('Only retained records are shown. Unrecorded activity, sleep stages and your intentions remain unknown.');
 const apps=[...appTimes].map(([name,minutes])=>({name,minutes})).sort((a,b)=>b.minutes-a.minutes);
 const order=['Sleep','Computer','Input','Phone','Exercise','Browser','Coding'];
 lanes.sort((a,b)=>order.findIndex(prefix=>a.name.startsWith(prefix))-order.findIndex(prefix=>b.name.startsWith(prefix)));
 return {lanes,chapters:chapters.sort((a,b)=>a.start-b.start || a.end-b.end),apps,facts,warnings};
}

export type EventRow = { lane: string; segment: DaySegment };

// Flattens timeline lanes into one list, newest first, so events can be
// scanned without the chart. Lanes and search filters are applied upstream.
export function eventRows(lanes: Lane[], order: 'newest' | 'oldest' = 'newest'): EventRow[] {
  const rows = lanes.flatMap(lane => lane.segments.map(segment => ({ lane: lane.name.split(' · ')[0], segment })));
  rows.sort((a, b) => order === 'newest' ? b.segment.start - a.segment.start : a.segment.start - b.segment.start);
  return rows;
}
