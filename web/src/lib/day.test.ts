import {test,expect} from 'vitest';
import {deriveDay,partitionSegments,type DaySegment} from './day';
import type {Observation} from './api';
let id=0;
function aw(bucket:string,start:string,end:string,data:unknown):Observation {
 return {source_kind:'activitywatch',source_instance_key:`aw-watcher-${bucket}`,source_record_key:String(++id),device_id:'mac',revision_digest:'1',occurred_at:start,ended_at:end,collected_at:end,kind:'aw',projection:{},original_payload:{data}};
}
const at=(time:string)=>`2026-09-07T${time}:00+03:00`;
test('overlapping foreground records are partitioned; conflicts do not inflate app totals',()=>{
 const a=aw('window',at('10:00'),at('11:00'),{app:'A'}), b=aw('window',at('10:30'),at('11:30'),{app:'B'});
 const result=deriveDay([a,b],[],'2026-09-07');
 expect(result.apps).toEqual([{name:'A',minutes:30},{name:'B',minutes:30}]);
 expect(result.lanes.find(l=>l.name.startsWith('Computer'))?.segments.map(s=>s.label)).toEqual(['A','Conflicting records','B']);
});
test('duplicate input snapshots count their union, not the sum, and gaps remain',()=>{
 const rows=[aw('afk',at('10:00'),at('10:10'),{status:'not-afk'}),aw('afk',at('10:00'),at('10:15'),{status:'not-afk'}),aw('afk',at('11:00'),at('11:05'),{status:'not-afk'})];
 const d=deriveDay(rows,[],'2026-09-07');
 expect(d.lanes.find(l=>l.name.startsWith('Input'))?.segments).toHaveLength(2);
 expect(d.facts[0]).toContain('0h 20m');
});
test('sleep end is labelled possible wake and not a confirmed wake or outing',()=>{
 const a=aw('health',at('01:48'),at('07:26'),{}); a.source_kind='health_connect';a.original_payload={record_type:'sleep_session',metadata:{data_origin:'com.sec.android.app.shealth'}};
 const d=deriveDay([a],[],'2026-09-07');
 expect(d.chapters.find(s=>s.label==='Recorded sleep ends')?.detail).toContain('Possible wake');
 expect(d.warnings.some(w=>w.includes('food trip'))).toBe(true);
});
test('phone resume events remain points and agent runtime is not included in window totals',()=>{
 const a=aw('phone',at('11:00'),at('11:01'),{});a.source_kind='android_usage';a.original_payload={event_type:'activity_resumed',package_name:'org.mozilla.firefox'};
 const b=aw('codex',at('09:00'),at('10:00'),{project_path:'/project/telemetry'});
 const d=deriveDay([a,b],[],'2026-09-07');
 expect(d.apps).toEqual([]);
 const phone=d.lanes.find(l=>l.name.startsWith('Phone'))!.segments[0];expect(phone.end).toBe(phone.start);
});
test('overlap partition leaves silent time empty',()=>{
 const row=(start:number,end:number):DaySegment=>({start,end,label:'A',kind:'foreground',detail:'',evidence:[]});
 expect(partitionSegments([row(0,10),row(20,30)])).toHaveLength(2);
});

test('phone intervals require matching package and activity pause; unmatched resume stays a point',()=>{
 const phone=(time:string,event:string,activity:string)=>{const o=aw('phone',at(time),at(time),{});o.source_kind='android_usage';o.original_payload={event_type:event,package_name:'org.mozilla.firefox',class_name:activity};return o;};
 const d=deriveDay([phone('10:00','activity_resumed','A'),phone('10:03','activity_paused','B'),phone('10:05','activity_paused','A'),phone('11:00','activity_resumed','A')],[],'2026-09-07');
 const rows=d.lanes.find(l=>l.name.startsWith('Phone'))!.segments;
 expect(rows.map(r=>(r.end-r.start)/60000)).toEqual([5,0]);
 expect(rows[0].evidence).toHaveLength(2);
});
test('different window titles remain inspectable even within the same app',()=>{
 const d=deriveDay([aw('window',at('10:00'),at('10:10'),{app:'Browser',title:'First'}),aw('window',at('10:10'),at('10:20'),{app:'Browser',title:'Second'})],[],'2026-09-07');
 expect(d.lanes.find(l=>l.name.startsWith('Computer'))!.segments.map(r=>r.detail)).toEqual(['First','Second']);
 expect(d.apps).toEqual([{name:'Browser',minutes:20}]);
});
