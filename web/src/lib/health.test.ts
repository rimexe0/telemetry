import {expect, test} from 'vitest';
import {deriveHealth, dayKey, unionMinutes} from './health';
import type {Observation} from './api';
let id = 0;
function record(type: string, start: string, end: string, extra = {}, origin = 'samsung'): Observation {
 return {source_kind:'health_connect', source_instance_key:'phone',device_id:'phone',source_record_key:String(++id),revision_digest:'1',kind:type,occurred_at:start,ended_at:end,collected_at:end,projection:{},original_payload:{record_type:type,metadata:{data_origin:origin},...extra}};
}
const view = (records:Observation[]) => deriveHealth(records,'samsung','2026-09-01','2026-09-02');
test('sleep crossing midnight is clipped into local days; overlaps count once',()=> {
 const a=record('sleep_session','2026-09-01T23:00:00+03:00','2026-09-02T07:00:00+03:00');
 const b=record('sleep_session','2026-09-02T06:00:00+03:00','2026-09-02T08:00:00+03:00');
 const d=view([a,b]).days;
 expect(d.map(x=>x.sleepMinutes)).toEqual([60,480]);
 expect(d[1].sleep[0].observation).toBe(a);
 expect(unionMinutes([])).toBeNull();
});
test('steps are isolated by origin and missing remains null, recorded zero is zero',()=> {
 const a=record('steps','2026-09-01T00:00:00+03:00','2026-09-01T23:59:59+03:00',{count:0});
 const b=record('steps',a.occurred_at,a.ended_at!,{count:9000},'android');
 expect(view([a,b,a]).days.map(x=>x.steps)).toEqual([0,null]);
});
test('overlapping and cross-midnight steps are withheld rather than summed or spread',()=>{
 const a=record('steps','2026-09-01T01:00:00+03:00','2026-09-01T03:00:00+03:00',{count:100});
 const b=record('steps','2026-09-01T02:00:00+03:00','2026-09-01T04:00:00+03:00',{count:200});
 expect(view([a,b]).days[0]).toMatchObject({steps:null,stepAmbiguous:true});
 const c=record('steps','2026-09-01T23:00:00+03:00','2026-09-02T01:00:00+03:00',{count:200});
 expect(view([c]).days.every(d=>d.stepAmbiguous && d.steps===null)).toBe(true);
});
test('HR uses sample time, removes identical repeats, excludes conflicting timestamps',()=>{
 const a=record('heart_rate','2026-09-01T20:00:00Z','2026-09-01T23:00:00Z',{samples:[{time:'2026-09-01T22:00:00Z',beats_per_minute:70},{time:'2026-09-01T22:01:00Z',beats_per_minute:80}]});
 const b=record('heart_rate',a.occurred_at,a.ended_at!,{samples:[{time:'2026-09-01T22:00:00Z',beats_per_minute:70},{time:'2026-09-01T22:01:00Z',beats_per_minute:81}]});
 const result=view([a,b]);
 expect(result.days[0].samples).toEqual([]);
 expect(result.days[1].samples.map(s=>s.bpm)).toEqual([70]);
 expect(result.warnings).toHaveLength(2);
 expect(dayKey('2026-09-01T22:00:00Z')).toBe('2026-09-02');
});
test('invalid intervals and measurements are visible',()=>{
 const a=record('sleep_session','invalid','invalid');
 expect(view([a]).warnings[0]).toContain('malformed');
});
