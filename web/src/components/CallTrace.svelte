<script lang="ts">
 import { onMount, tick, untrack } from 'svelte';
 import { ApiError } from '../lib/api';
 import { app } from '../lib/appState.svelte';
 import {
  buildAxis, callBox, callNumbers, clampMargin, DEFAULT_MARGIN_MIN, eventBox, eventText, fetchCallTrace, MARGIN_CHOICES, packRows,
  partSegments, payloadPart, realClockOffered, startPct, tickMarks, trayKey, type CallTraceResult, type TimeMode, type TraceLane
 } from '../lib/callTrace';
 import { clearNotice, removeFromTray, tray } from '../lib/callTraceTray.svelte';
 import { CALL_TRACE_TEXT as T, callName, callsAre, hiddenMore, LANE_STATE_TEXT, marginOption, statusIcon, statusText } from '../lib/copy/callTraceCopy';
 import { formatDuration } from '../lib/format';
 import { formatTime } from '../lib/time';
 let { zone, onclose }: { zone: string; onclose: () => void } = $props();
 let margin = $state(DEFAULT_MARGIN_MIN), mode = $state<TimeMode>('alignedToStart');
 let result = $state.raw<CallTraceResult | null>(null), loading = $state(false), error = $state('');
 let readout = $state<{ trace: number; text: string } | null>(null);
 let controller: AbortController | undefined, sequence = 0, timer: ReturnType<typeof setTimeout> | undefined;
 const caseId = $derived(app.activeCaseId);
 // The request depends on which calls are chosen, the open case and the margin. A change to anything else (a label, the anchor) does not refetch.
 const requestKey = $derived(tray.calls.map(trayKey).join('|') + '#' + caseId + '#' + margin);
 const axis = $derived(result ? buildAxis(mode, result) : null);
 const ticks = $derived(axis ? tickMarks(axis, mode, zone) : []);
 const realOk = $derived(result ? realClockOffered(result) : false);
 const stamp = (value: string | number) => formatTime(value, { zone, precision: 'ms' });
 const ms = (value: string) => Date.parse(value);

 async function load() {
  clearTimeout(timer);
  controller?.abort();
  const mine = new AbortController();
  controller = mine;
  const run = ++sequence;
  const calls = tray.calls.map(call => ({ sourceId: call.sourceId, id: call.id }));
  if (!calls.length) { loading = false; return; }
  clearNotice(); // an old refusal does not sit beside a new answer
  loading = true; error = '';
  try {
   const out = await fetchCallTrace(calls, margin, margin, caseId, mine.signal);
   if (run !== sequence) return;
   result = out;
   if (mode === 'realClock' && !realClockOffered(out)) mode = 'alignedToStart';
  } catch (cause) {
   if (run !== sequence || (cause instanceof DOMException && cause.name === 'AbortError')) return;
   error = cause instanceof ApiError ? cause.message : cause instanceof Error && cause.message === 'unreadable' ? T.unreadable : T.failed;
  } finally { if (run === sequence) loading = false; }
 }
 // A newer request replaces the older one. The short delay lets someone add a second and third call without a request for each click.
 $effect(() => {
  void requestKey;
  untrack(() => {
   sequence++; controller?.abort(); clearTimeout(timer);
   result = null; error = ''; readout = null; loading = true;
   timer = setTimeout(() => void load(), 200);
  });
  return () => clearTimeout(timer);
 });
 onMount(() => () => { sequence++; controller?.abort(); clearTimeout(timer); });

 // Removing a chip takes its button away. The last one ends the trace; otherwise focus moves to the heading.
 async function removeCall(call: { sourceId: string; id: string }) {
  removeFromTray(call);
  if (!tray.calls.length) { onclose(); return; }
  await tick();
  document.getElementById('call-trace-heading')?.focus();
 }
 const laneBoxes = (lane: TraceLane, trace: CallTraceResult['traces'][number]) => (axis ? lane.events.map(event => eventBox(axis, mode, trace.call, event)) : []);
 const sharedNote = $derived(mode === 'realClock' ? T.modeRealHint : T.modeAlignedHint);
 const callTitle = (index: number, trace: CallTraceResult['traces'][number]) => `${callName(index)}: ${trace.call.controller}.${trace.call.action}`;
 const totalText = (trace: CallTraceResult['traces'][number]) => trace.call.durationMs == null ? 'duration unavailable' : formatDuration(trace.call.durationMs);
</script>

<section class="call-trace" id="call-trace" aria-labelledby="call-trace-heading" aria-busy={loading}>
 <div class="ct-head">
  <div><h4 id="call-trace-heading" tabindex="-1">{T.heading}</h4><p class="ct-note">{T.intro}</p></div>
  <button type="button" class="secondary-button" onclick={onclose}>{T.close}</button>
 </div>
 <ul class="ct-chips" aria-label={T.trayHeading}>
  {#each tray.calls as call, index (trayKey(call))}<li><span><b>{callName(index)}</b> {call.label}</span><button type="button" class="quiet-button" aria-label={`${T.trayRemove} ${callName(index)}: ${call.label}`} onclick={() => void removeCall(call)}>{T.trayRemove}</button></li>{/each}
 </ul>
 <div class="ct-controls">
  <div class="ct-mode" role="group" aria-label={T.modeLabel}>
   <button type="button" class="secondary-button" aria-pressed={mode === 'alignedToStart'} onclick={() => { mode = 'alignedToStart'; }}>{T.modeAligned}</button>
   <button type="button" class="secondary-button" aria-pressed={mode === 'realClock'} disabled={!realOk} aria-describedby="ct-mode-note" onclick={() => { mode = 'realClock'; }}>{T.modeReal}</button>
  </div>
  <label>{T.marginLabel}<select bind:value={margin} onchange={() => { margin = clampMargin(Number(margin)); }}>{#each MARGIN_CHOICES as choice}<option value={choice}>{marginOption(choice)}</option>{/each}</select></label>
 </div>
 <p class="ct-note" id="ct-mode-note">{#if result && !realOk}{T.realUnavailable}{:else}{sharedNote}{/if}</p>
 <p class="ct-note">{T.overlapNote} {T.splitNote}</p>

 {#if error}<div class="inline-error" role="alert">{error} <button type="button" class="secondary-button" onclick={() => void load()}>{T.retry}</button></div>{/if}
 {#if loading && !result}<div class="ct-loading" role="status"><div class="loading-pulse"></div><p>{T.loading}</p></div>{/if}
 {#if !loading && !result && !error && !tray.calls.length}<p class="ct-note">{T.empty}</p>{/if}

 {#if result && axis}
  {#if result.truncated}<div class="banner" role="status"><strong>{T.truncated}</strong></div>{/if}
  {#each result.traces as trace, ti (trace.call.sourceId + ':' + trace.call.id)}
   {@const segments = partSegments(trace.call.parts)}
   {@const payload = payloadPart(trace.call.parts)}
   {@const box = callBox(axis, mode, trace.call)}
   <article class="ct-trace" aria-label={callTitle(ti, trace)}>
    <h5>{callTitle(ti, trace)} <small>{trace.call.outcome} &middot; {totalText(trace)} &middot; {stamp(ms(trace.call.start))}</small></h5>
    {#if trace.call.clockAnomaly}<p class="ct-note ct-warn">{T.clockAnomaly}</p>{:else if !trace.call.end}<p class="ct-note ct-warn">{T.noFinish}</p>{/if}
    <div class="ct-scroll" role="region" aria-label={`${callTitle(ti, trace)}. ${T.chartLabel}`}>
     <div class="ct-chart">
      <div class="ct-ticks" aria-hidden="true">{#each ticks as tick}<span style:left={tick.pct + '%'}>{tick.label}</span>{/each}</div>
      <div class="ct-lane-head"><strong>{T.callBar}</strong></div>
      <div class="ct-track">
       <div class="ct-guide" style:left={startPct(axis, mode, trace.call) + '%'}></div>
       {#if box}<span class="ct-call" role="img" aria-label={`${callTitle(ti, trace)}, ${totalText(trace)}, ${stamp(ms(trace.call.start))}`} style:left={box.left + '%'} style:width={box.width + '%'}></span>{/if}
      </div>
       <div class="ct-lane-head"><strong>{T.partsHeading}</strong></div>
       <div class="ct-parts">
        {#if segments.length}
         <div class="ct-bar" role="img" aria-label={segments.map(s => `${s.label} ${formatDuration(s.ms)}`).join(', ')}>{#each segments as seg (seg.key)}<span class={'ct-seg ct-part-' + seg.key} style:width={seg.widthPct + '%'}></span>{/each}</div>
        {:else}<p class="ct-note">{T.noEvents}</p>{/if}
       </div>
      {#each trace.lanes as lane (lane.key)}
       {@const boxes = laneBoxes(lane, trace)}
       {@const rows = packRows(boxes)}
       {@const depth = Math.max(0, ...rows) + 1}
       <div class="ct-lane-head"><strong>{lane.label}</strong>{#if lane.state !== 'events'}<span class="ct-state">{LANE_STATE_TEXT[lane.state] ?? lane.state}</span>{/if}</div>
       {#if lane.state === 'events'}
        <div class="ct-track ct-events" style:height={depth * 22 + 6 + 'px'}>
         <div class="ct-guide" style:left={startPct(axis, mode, trace.call) + '%'}></div>
         {#each lane.events as event, ei (event.id + ':' + ei)}
          {#if boxes[ei]}<button type="button" class={'ct-ev ct-lane-' + lane.key} class:ct-sev={!!event.severity} style:left={boxes[ei]!.left + '%'} style:width={boxes[ei]!.width + '%'} style:top={rows[ei] * 22 + 3 + 'px'} aria-label={`${lane.label}: ${eventText(event, zone)}`} title={eventText(event, zone)} onmouseenter={() => { readout = { trace: ti, text: `${lane.label}: ${eventText(event, zone)}` }; }} onfocus={() => { readout = { trace: ti, text: `${lane.label}: ${eventText(event, zone)}` }; }} onmouseleave={() => { readout = null; }} onblur={() => { readout = null; }}></button>{/if}
         {/each}
        </div>
        {#if lane.hidden > 0}<p class="ct-note">{hiddenMore(lane.hidden)}</p>{/if}
       {/if}
      {/each}
     </div>
    </div>
    {#if segments.length}
     <div class="ct-key">
      <ul class="ct-legend">{#each segments as seg (seg.key)}<li><span class={'ct-swatch ct-part-' + seg.key} aria-hidden="true"></span>{seg.label}: <b>{formatDuration(seg.ms)}</b></li>{/each}{#if payload}<li><span class="ct-diamond" aria-hidden="true">&#9670;</span>{T.payloadMarker}: <b>{formatDuration(payload.ms)}</b></li>{/if}</ul>
      {#if payload}<p class="ct-note">{T.payloadNote}</p>{/if}
     </div>
    {/if}
    <p class="ct-readout" aria-hidden="true">{readout && readout.trace === ti ? readout.text : ' '}</p>
    <h6>{T.checksHeading}</h6>
    {#if trace.checks.length}
     <ul class="ct-checks">{#each trace.checks as check (check.id)}<li class={'ct-check ct-status-' + check.status}><span class="ct-badge"><span aria-hidden="true">{statusIcon(check.status)}</span> {statusText(check.status)}</span> <strong>{check.title}</strong><p>{check.detail}</p>{#if check.needs}<p class="ct-needs">{T.needs}: {check.needs}</p>{/if}</li>{/each}</ul>
    {:else}<p class="ct-note">{T.checksNone}</p>{/if}
   </article>
  {/each}

  {#if result.traces.length > 1}
   <section class="ct-trace" aria-label={T.compareHeading}>
    <h5>{T.compareHeading}</h5>
    {#if result.compare.length}
     <ul class="ct-checks">{#each result.compare as check (check.id)}<li class={'ct-check ct-status-' + check.status}><span class="ct-badge"><span aria-hidden="true">{statusIcon(check.status)}</span> {statusText(check.status)}</span> <strong>{check.title}</strong> <small>{callsAre(callNumbers(check, result.traces))}</small><p>{check.detail}</p>{#if check.needs}<p class="ct-needs">{T.needs}: {check.needs}</p>{/if}</li>{/each}</ul>
    {:else}<p class="ct-note">{T.checksNone}</p>{/if}
   </section>
  {/if}

  <details class="ct-list">
   <summary>{T.listSummary}</summary>
   {#each result.traces as trace, ti (trace.call.sourceId + ':' + trace.call.id)}
    <h6>{callTitle(ti, trace)}</h6>
    <table>
     <caption>{T.listParts}</caption>
     <thead><tr><th scope="col">{T.listWhat}</th><th scope="col">{T.listLength}</th></tr></thead>
     <tbody>{#each trace.call.parts as part (part.key)}<tr><th scope="row">{part.label}</th><td>{formatDuration(part.ms)}</td></tr>{/each}</tbody>
    </table>
    {#each trace.lanes as lane (lane.key)}
     <table>
      <caption>{lane.label}{lane.state !== 'events' ? ': ' + (LANE_STATE_TEXT[lane.state] ?? lane.state) : ''}</caption>
      {#if lane.events.length}
       <thead><tr><th scope="col">{T.listWhat}</th><th scope="col">{T.listWhen}</th><th scope="col">{T.listLength}</th></tr></thead>
       <tbody>{#each lane.events as event, ei (event.id + ':' + ei)}<tr><th scope="row">{event.label}</th><td>{stamp(ms(event.start))}</td><td>{event.durationMs == null ? '' : formatDuration(event.durationMs)}</td></tr>{/each}</tbody>
      {/if}
     </table>
     {#if lane.hidden > 0}<p class="ct-note">{hiddenMore(lane.hidden)}</p>{/if}
    {/each}
   {/each}
  </details>
 {/if}
</section>

<style>
 .call-trace { display: grid; gap: 10px; margin: 0 14px 14px; padding: 12px; min-width: 0; background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius-sm); }
 .ct-head { display: flex; flex-wrap: wrap; align-items: start; justify-content: space-between; gap: 8px; }
 h4, h5, h6 { margin: 0; }
 h4 { font-size: 15px; }
 h5 { font-size: 14px; overflow-wrap: anywhere; }
 h5 small { margin-left: 6px; color: var(--text-2); font-size: 12px; font-weight: 400; }
 h6 { font-size: 13px; color: var(--text-2); }
 .ct-note { margin: 0; color: var(--text-2); font-size: 13px; line-height: 1.5; overflow-wrap: anywhere; }
 .ct-warn { color: var(--warn); }
 .ct-chips { display: flex; flex-wrap: wrap; gap: 6px; margin: 0; padding: 0; list-style: none; }
 .ct-chips li { display: inline-flex; align-items: center; gap: 4px; max-width: 100%; padding: 2px 4px 2px 10px; border: 1px solid var(--line); border-radius: 999px; background: var(--surface-2); font-size: 12px; overflow-wrap: anywhere; }
 .ct-controls { display: flex; flex-wrap: wrap; align-items: end; gap: 10px 16px; }
 .ct-controls label { display: grid; gap: 4px; color: var(--text-2); font-size: 12px; }
 .ct-mode { display: inline-flex; gap: 6px; }
 .ct-mode button[aria-pressed="true"] { border-color: var(--accent); background: var(--surface-2); }
 .ct-loading { display: flex; align-items: center; gap: 10px; }
 .ct-loading p { margin: 0; color: var(--text-2); font-size: 13px; }
 .ct-trace { display: grid; gap: 8px; padding-top: 10px; border-top: 1px solid var(--line-soft); min-width: 0; }
 .ct-scroll { overflow-x: auto; max-width: 100%; }
 .ct-chart { min-width: 560px; padding: 0 8px; }
 .ct-ticks { position: relative; height: 18px; margin: 0 24px; color: var(--text-2); font-size: 11px; font-family: var(--mono); }
 .ct-ticks span { position: absolute; transform: translateX(-50%); white-space: nowrap; }
 .ct-lane-head { display: flex; flex-wrap: wrap; align-items: baseline; gap: 4px 10px; margin-top: 8px; font-size: 12px; position: sticky; left: 0; }
 .ct-state { color: var(--text-2); }
 .ct-track { position: relative; height: 22px; margin: 4px 24px 0; border-top: 1px solid var(--line-soft); border-bottom: 1px solid var(--line-soft); }
 .ct-guide { position: absolute; top: 0; bottom: 0; border-left: 1px dashed var(--accent); opacity: .6; pointer-events: none; }
 .ct-call { position: absolute; top: 4px; height: 14px; border-radius: 3px; background: var(--accent); }
 .ct-parts { margin: 4px 24px 0; display: grid; gap: 6px; }
 .ct-key { display: grid; gap: 6px; min-width: 0; }
 .ct-bar { display: flex; height: 16px; overflow: hidden; border-radius: 3px; border: 1px solid var(--line); }
 .ct-seg { display: block; height: 100%; }
 .ct-part-audit { background: var(--series-6); }
 .ct-part-api { background: var(--series-1); }
 .ct-part-other { background: var(--series-8); background-image: repeating-linear-gradient(45deg, transparent 0 4px, color-mix(in srgb, var(--surface) 55%, transparent) 4px 7px); }
 .ct-legend { display: flex; flex-wrap: wrap; gap: 4px 16px; margin: 0; padding: 0; list-style: none; font-size: 12px; }
 .ct-swatch { display: inline-block; width: 10px; height: 10px; margin-right: 5px; border-radius: 2px; vertical-align: -1px; }
 .ct-diamond { margin-right: 5px; color: var(--warn); }
 .ct-ev { position: absolute; height: 16px; min-width: 4px; padding: 0; border: 1px solid var(--surface); border-radius: 3px; opacity: .85; cursor: help; }
 .ct-ev:hover { opacity: 1; }
 .ct-ev:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; opacity: 1; z-index: 2; }
 .ct-sev { border-color: var(--bad); border-width: 2px; }
 .ct-lane-sql { background: var(--series-1); }
 .ct-lane-mongo { background: var(--series-2); }
 .ct-lane-redis { background: var(--series-4); }
 .ct-lane-iis { background: var(--series-3); }
 .ct-lane-other { background: var(--series-8); }
 .ct-readout { min-height: 18px; margin: 0; color: var(--text-2); font-size: 12px; font-family: var(--mono); overflow-wrap: anywhere; }
 .ct-checks { display: grid; gap: 8px; margin: 0; padding: 0; list-style: none; }
 .ct-check { padding: 8px 10px; border: 1px solid var(--line); border-left-width: 4px; border-radius: var(--radius-sm); font-size: 13px; overflow-wrap: anywhere; }
 .ct-check p { margin: 4px 0 0; color: var(--text-2); }
 .ct-check small { color: var(--text-2); }
 .ct-badge { display: inline-block; margin-right: 6px; padding: 0 7px; border: 1px solid currentColor; border-radius: 999px; font-size: 12px; font-weight: 600; white-space: nowrap; }
 .ct-status-consistent { border-left-color: var(--accent); }
 .ct-status-consistent .ct-badge { color: var(--accent); }
 .ct-status-contradicted { border-left-color: var(--bad); }
 .ct-status-contradicted .ct-badge { color: var(--bad); }
 .ct-status-not-enough-data { border-left-color: var(--warn); }
 .ct-status-not-enough-data .ct-badge { color: var(--warn); }
 .ct-status-not-testable { border-left-color: var(--pending); }
 .ct-status-not-testable .ct-badge { color: var(--text-2); }
 .ct-list summary { cursor: pointer; font-size: 13px; }
 .ct-list table { width: 100%; margin: 8px 0; border-collapse: collapse; font-size: 12px; }
 .ct-list caption { text-align: left; font-weight: 600; padding-bottom: 4px; }
 .ct-list th, .ct-list td { padding: 3px 6px; text-align: left; border-bottom: 1px solid var(--line-soft); font-weight: 400; overflow-wrap: anywhere; }
 .ct-list thead th { color: var(--text-2); }
 @media (max-width: 640px) { .call-trace { margin: 0 8px 12px; padding: 10px; } .ct-chart { min-width: 520px; } }
 @media (prefers-reduced-motion: reduce) { * { transition: none !important; animation: none !important; } }
</style>
