<script lang="ts">
 import { onMount, untrack } from 'svelte';
 import { api } from '../lib/api';
 import type { Page, RequestRecord, SourceResult } from '../lib/types';
 import { formatTime, zoneAbbreviation } from '../lib/time';
 import { formatDuration, countNoun } from '../lib/format';
 let { anchor, sourceIds, sourceLabels, zone }: { anchor: RequestRecord; sourceIds: string[]; sourceLabels: Map<string,string>; zone: string } = $props();
 let before = $state(30), after = $state(30), unit = $state(1);
 let rows = $state<RequestRecord[]>([]), page = $state<Page | null>(null), loading = $state(false), error = $state('');
 let appliedStart = $state(0), appliedEnd = $state(0), appliedSignature = $state('');
 let controller: AbortController | undefined, sequence = 0;
 let coverageErrors = $state<SourceResult[]>([]), everTruncated = $state(false);
 const draftSignature = $derived(`${before}|${after}|${unit}`);
 const ordered = $derived(rows.filter(row => row.sourceId !== anchor.sourceId || row.id !== anchor.id).sort((a,b) => Date.parse(a.initiatedAt ?? '') - Date.parse(b.initiatedAt ?? '')));
 const stamp = (value: number) => formatTime(value,{zone,precision:'ms'});
 const validEnd = (row: RequestRecord) => row.initiatedAt && row.completedAt && Date.parse(row.completedAt) >= Date.parse(row.initiatedAt) ? Date.parse(row.completedAt) : null;
 function bar(row: RequestRecord) {
  const start = Date.parse(row.initiatedAt ?? '');
  const end = validEnd(row);
  const width = appliedEnd-appliedStart;
  const left = Math.max(0,Math.min(100,(start-appliedStart)/width*100));
  const right = Math.max(left,Math.min(100,((end ?? start)-appliedStart)/width*100));
  return `left:${left}%;width:${Math.max(.15,right-left)}%`;
 }
 const description = (row: RequestRecord) => `${row.controller}.${row.action} · ${sourceLabels.get(row.sourceId) ?? row.sourceId} · ${row.kind} · ${row.outcome} · ${row.initiatedAt ? stamp(Date.parse(row.initiatedAt)) : 'Unknown start'} to ${validEnd(row) != null ? stamp(validEnd(row)!) : 'unknown completion'} · ${row.durationMs == null ? 'duration unavailable' : formatDuration(row.durationMs)}`;
 async function load(cursor = '') {
  let start=appliedStart,end=appliedEnd;
  if (!cursor) {
   if (!Number.isFinite(before)||!Number.isFinite(after)||before<0||after<0) {error='Enter non-negative Before and After values.';return;}
   const at=Date.parse(anchor.initiatedAt ?? '');
   start=at-before*unit*1000;end=(validEnd(anchor) ?? at)+after*unit*1000;
   if (!Number.isFinite(start)||!Number.isFinite(end)||end<=start||end-start>86400000) {error='Choose a window longer than zero and no more than 24 hours, including the selected request.';return;}
  }
  controller?.abort();controller=new AbortController();const run=++sequence;loading=true;error='';
  if (!cursor) {rows=[];page=null;coverageErrors=[];everTruncated=false;appliedStart=start;appliedEnd=end;appliedSignature=draftSignature;}
  try {
   const result=await api.activity({sourceIds,start:new Date(start).toISOString(),end:new Date(end).toISOString(),limit:100,cursor},controller.signal);
   if (run!==sequence)return;
   rows=cursor?[...rows,...result.rows].filter((row,i,list)=>list.findIndex(item=>item.id===row.id&&item.sourceId===row.sourceId)===i).slice(0,500):result.rows;
   page=result;
   coverageErrors=[...coverageErrors,...result.sources.filter(item=>!item.ok)].filter((item,index,list)=>list.findIndex(other=>other.sourceId===item.sourceId)===index);
   everTruncated ||= result.truncated;
  } catch(cause) {if(run===sequence&&!(cause instanceof DOMException&&cause.name==='AbortError'))error=cause instanceof Error?cause.message:'Could not read nearby activity.';}
  finally {if(run===sequence)loading=false;}
 }
 $effect(()=>{ const current=anchor; untrack(()=>{sequence++;controller?.abort();rows=[];page=null;coverageErrors=[];everTruncated=false;appliedStart=0;appliedEnd=0;appliedSignature='';loading=false;if(current.initiatedAt)void load();}); });
 onMount(()=>()=>{sequence++;controller?.abort();});
</script>

<section class="data-section activity-context" id="nearby-activity" aria-label="Nearby request activity">
 <div class="section-heading"><div><h3>What was happening around this request?</h3><p><strong>{anchor.controller}.{anchor.action}</strong> · {sourceLabels.get(anchor.sourceId) ?? anchor.sourceId}</p></div></div>
 <div class="activity-controls"><label>Before<input type="number" min="0" step="any" bind:value={before} /></label><label>After<input type="number" min="0" step="any" bind:value={after} /></label><label>Unit<select aria-label="Activity time unit" bind:value={unit}><option value={1}>Seconds</option><option value={60}>Minutes</option><option value={3600}>Hours</option></select></label><button class="primary-button" disabled={loading} onclick={()=>load()}>{loading?'Reading activity…':'Apply time window'}</button>{#if loading}<button class="quiet-button" onclick={()=>{sequence++;controller?.abort();loading=false;}}>Cancel</button>{/if}</div>
 <div class="activity-explanation"><p>Before is measured from the selected request’s start; After is measured from its completion. The highlighted row is your selected request. Other bars show recorded API calls and jobs across <strong>{countNoun(sourceIds.length,'source')}</strong>, with all request types and outcomes included.</p><p>Earlier requests are included when a recorded finish time shows they overlapped this window. Earlier records without a finish time cannot establish that they were still running. An outlined marker means completion is unknown or unusable. These logs do not show CPU, database waits, or causal relationships; clocks on different servers may differ.</p>{#if appliedEnd>appliedStart}<p><strong>{stamp(appliedStart)}–{stamp(appliedEnd)} ({zone})</strong></p>{/if}{#if appliedSignature&&draftSignature!==appliedSignature}<p>Time controls changed. Apply the time window to update the displayed activity.</p>{/if}</div>
 {#if error}<div class="inline-error" role="alert">{error}</div>{/if}
 {#if coverageErrors.length}<div class="banner source-failure" role="alert"><strong>Incomplete source coverage</strong>{#each coverageErrors as failed}<span>{sourceLabels.get(failed.sourceId) ?? failed.sourceId}: {failed.error}</span>{/each}<span>A source failed on at least one page. Apply the time window again to retry the entire window; later pages cannot recover records skipped on an earlier page.</span></div>{/if}
 {#if appliedEnd>appliedStart}<div class="activity-scroll"><div class="activity-waterfall"><div class="activity-axis"><span>Recorded activity · {zoneAbbreviation(zone,appliedStart)}</span><div>{#each [0,.25,.5,.75,1] as part}<span style:left={part*100+'%'}>{formatTime(appliedStart+(appliedEnd-appliedStart)*part,{zone,date:false,suffix:false})}</span>{/each}</div></div>
 {#each [anchor,...ordered] as row,index (row.sourceId+':'+row.id)}<div class="activity-row" class:activity-anchor={index===0}><div class="activity-row-label" title={description(row)}><strong>{index===0?'Selected · ':''}{row.controller}.{row.action}</strong><small>{sourceLabels.get(row.sourceId) ?? row.sourceId} · {row.kind} · {row.outcome}</small></div><div class="activity-track"><div class="activity-anchor-line" style:left={Math.max(0,Math.min(100,(Date.parse(anchor.initiatedAt!)-appliedStart)/(appliedEnd-appliedStart)*100))+'%'}></div><button class="activity-bar" class:activity-unknown={validEnd(row)==null} style={bar(row)} aria-label={description(row)} title={description(row)}></button></div></div>{/each}
 </div></div>{/if}
 <div class="pagination-row"><span>{rows.length} activity records loaded. Selected request is pinned separately. {page?.nextCursor||everTruncated||coverageErrors.length?'This is not a complete view of activity yet.':page ? 'No more matching records reported by successful sources.' : loading ? 'Reading activity…' : 'No activity page available yet.'}</span>{#if page?.nextCursor&&rows.length<500}<button class="secondary-button" disabled={loading} onclick={()=>load(page?.nextCursor ?? '')}>Load more activity</button>{/if}</div>
 {#if rows.length>=500}<p class="activity-explanation">Display limit reached (500 records). Narrow the time window to inspect this busy period.</p>{/if}
</section>

