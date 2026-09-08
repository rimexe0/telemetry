export const DAY = 86_400_000;
export const MIN_WINDOW = 60_000;
export type TimeWindow = { start:number; end:number };
export function moveWindow(window:TimeWindow, delta:number):TimeWindow {
 const span=window.end-window.start;
 const start=Math.max(0,Math.min(DAY-span,window.start+delta));
 return {start,end:start+span};
}
export function resizeWindow(window:TimeWindow, edge:'start'|'end', value:number):TimeWindow {
 return edge==='start' ? {start:Math.max(0,Math.min(window.end-MIN_WINDOW,value)),end:window.end} : {start:window.start,end:Math.min(DAY,Math.max(window.start+MIN_WINDOW,value))};
}
export function tooltipPosition(x:number,y:number,width:number,height:number,viewportWidth:number,viewportHeight:number) {
 const left=x+18+width<=viewportWidth-8 ? x+18 : x-width-18;
 const top=y+18+height<=viewportHeight-8 ? y+18 : y-height-18;
 return {left:Math.max(8,Math.min(viewportWidth-width-8,left)),top:Math.max(8,Math.min(viewportHeight-height-8,top))};
}
