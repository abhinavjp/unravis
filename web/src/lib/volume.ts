import type { Query, VolumeBucket } from './types';
export function volumeData(query: Pick<Query,'start'|'end'|'bucketSeconds'>, rows: VolumeBucket[], from=0, to=119, top=8, focus='') {
 const start=Date.parse(query.start),end=Date.parse(query.end),width=query.bucketSeconds*1000;
 const slots:{start:number;end:number;at:number}[]=[];
 if(Number.isFinite(start)&&Number.isFinite(end)&&width>0)for(let at=Math.floor(start/width)*width;at<end&&slots.length<120;at+=width)slots.push({at,start:Math.max(start,at),end:Math.min(end,at+width)});
 const first=Math.max(0,Math.min(slots.length-1,from)),last=Math.max(first,Math.min(slots.length-1,to)),selected=slots.slice(first,last+1);
 const index=new Map(selected.map((slot,i)=>[slot.at,i]));
 const grouped=new Map<string,{key:string;controller:string;action:string;total:number;counts:number[]}>();
 for(const row of rows){const key=JSON.stringify([row.controller,row.action]),i=index.get(Date.parse(row.at));if(i==null||(focus&&key!==focus))continue;let api=grouped.get(key);if(!api){api={key,controller:row.controller,action:row.action,total:0,counts:selected.map(()=>0)};grouped.set(key,api);}api.counts[i]+=row.count;api.total+=row.count;}
 const apis=[...grouped.values()].sort((a,b)=>b.total-a.total||a.key.localeCompare(b.key));
 const series=apis.slice(0,top).map(api=>({key:api.key,name:(api.controller||'(unnamed controller)')+'.'+(api.action||'(unnamed action)'),counts:api.counts,total:api.total}));
 if(apis.length>top){const other=apis.slice(top),counts=selected.map((_,i)=>other.reduce((sum,api)=>sum+api.counts[i],0));series.push({key:'other',name:'Other APIs ('+other.length+')',counts,total:counts.reduce((a,b)=>a+b,0)});}
 return {slots:selected,allSlots:slots,first,last,apis,series,total:apis.reduce((sum,api)=>sum+api.total,0)};
}
