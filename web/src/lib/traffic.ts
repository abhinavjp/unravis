import type {Comparison,LoadWindow,Query,Rollup} from './types';
export function trafficData(query:Pick<Query,'start'|'end'|'bucketSeconds'>,rows:LoadWindow[],threshold:number,metric:'p95'|'median'|'max'){
 const start=Date.parse(query.start),end=Date.parse(query.end),width=query.bucketSeconds*1000;
 const observed=new Map(rows.map(row=>[Date.parse(row.at),row]));
 const windows:{at:number;start:number;end:number;count:number;timings:number;latency:number|null;max:number|null;slow:boolean;partial:boolean}[]=[];
 if(width>0)for(let at=Math.floor(start/width)*width;at<end&&windows.length<120;at+=width){const row=observed.get(at),timing=row?.timing;const latency=metric==='max'?timing?.maxMs:metric==='median'?timing?.p50?.valueMs:timing?.p95?.valueMs;windows.push({at,start:Math.max(start,at),end:Math.min(end,at+width),count:row?.count??0,timings:timing?.count??0,latency:latency!=null&&Number.isFinite(latency)?latency:null,max:timing?.maxMs??null,slow:timing?.maxMs!=null&&timing.maxMs>threshold,partial:at<start||at+width>end});}
 const points=windows.filter(row=>row.count>0&&row.timings>0&&row.latency!=null);
 const grouped=new Map<number,{lower:number;upper:number;measured:number;slow:number;percent:number}>();
 for(const row of windows){if(row.partial||row.count<=0||!row.timings)continue;const lower=2**Math.floor(Math.log2(row.count));let b=grouped.get(lower);if(!b){b={lower,upper:lower*2,measured:0,slow:0,percent:0};grouped.set(lower,b);}b.measured++;if(row.slow)b.slow++;b.percent=b.slow/b.measured*100;}
 return {windows,points,buckets:[...grouped.values()].sort((a,b)=>a.lower-b.lower),unmeasured:windows.filter(row=>row.count>0&&!row.timings).length};
}

/** Error rate as a percent; "—" when the API has no known-outcome requests. */
export const errorRateText=(rate:number|null|undefined)=>rate==null||!Number.isFinite(rate)?'—':(rate*100).toFixed(1)+'%';
/** The rollup for a cohort's API; rollups are per controller and action across sources and kinds. */
export const rollupFor=(rollups:Rollup[]|undefined,row:Pick<Comparison,'controller'|'action'>)=>rollups?.find(r=>r.controller===row.controller&&r.action===row.action);
export const LOAD_BADGE_MIN_ABS_RHO=0.6,LOAD_BADGE_MIN_POINTS=8;
/**
 * Badge text for a series' load correlation, or null when it is too weak or too thin.
 * points defaults to the minimum because the backend only reports rho with at least that many paired buckets.
 */
export const loadBadge=(rho:number|null|undefined,points=LOAD_BADGE_MIN_POINTS)=>{
 if(rho==null||!Number.isFinite(rho)||points<LOAD_BADGE_MIN_POINTS||Math.abs(rho)<LOAD_BADGE_MIN_ABS_RHO)return null;
 return (rho>0?'slows under load':'faster under load')+' (ρ='+rho.toFixed(1)+')';
};
