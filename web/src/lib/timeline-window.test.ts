import {test,expect} from 'vitest';
import {DAY,MIN_WINDOW,moveWindow,resizeWindow,tooltipPosition} from './timeline-window';
test('full-day brush can shrink from either edge',()=>{
 expect(resizeWindow({start:0,end:DAY},'start',18*3600000)).toEqual({start:18*3600000,end:DAY});
 expect(resizeWindow({start:0,end:DAY},'end',6*3600000)).toEqual({start:0,end:6*3600000});
});
test('moving selection preserves width and has no dead zones at day edges',()=>{
 expect(moveWindow({start:0,end:3600000},DAY)).toEqual({start:DAY-3600000,end:DAY});
 expect(moveWindow({start:DAY-3600000,end:DAY},-DAY)).toEqual({start:0,end:3600000});
 expect(resizeWindow({start:0,end:3600000},'end',0).end).toBe(MIN_WINDOW);
});
test('tooltip flips before edges and never contains the cursor',()=>{
 for(const [x,y] of [[100,100],[1200,740],[4,4],[1275,799]]) {
  const p=tooltipPosition(x,y,260,120,1280,800);
  expect(p.left).toBeGreaterThanOrEqual(8);expect(p.top).toBeGreaterThanOrEqual(8);
  expect(p.left+260).toBeLessThanOrEqual(1272);expect(p.top+120).toBeLessThanOrEqual(792);
  expect(x>=p.left&&x<=p.left+260&&y>=p.top&&y<=p.top+120).toBe(false);
 }
});
