<script lang="ts">
  import { untrack } from 'svelte';
  import { CaseApiError, fetchSeries, isCaseGone } from '../../lib/cases';
  import { errorText } from '../../lib/copy/importCopy';
  import {
    PROC_METRICS, apiStrip, bucketFor, bucketText, buildStrips, hostSelectors, orderStrips, processSelectors, processStrip, readSeries, resourceCopy, stripsSummary,
    type EpisodeSpan, type Marker, type ProcMetricId, type ResourceHost, type SeriesRead, type ViewWindow
  } from '../../lib/resource';
  import StripChart from './StripChart.svelte';

  // The lines of one open host: reads /series for the view window (bucket = window ÷ 600), draws the strips and says plainly when there is nothing,
  // when the read is still going, when some of it was not read in time and when it failed.
  let { caseId, host, view, episodes, markers, zone, apiWindows, onBrush, onPick, onGone }: {
    caseId: string; host: ResourceHost; view: ViewWindow; episodes: EpisodeSpan[]; markers: Marker[]; zone: string;
    apiWindows: ReadonlyArray<{ at: string; p95Ms: number | null }>;
    onBrush: (fromMs: number, toMs: number) => void; onPick: (t: number) => void; onGone?: () => void;
  } = $props();

  let procMetric = $state<ProcMetricId>('proc.cpu');
  let hostRead = $state.raw<SeriesRead | null>(null);
  let procRead = $state.raw<SeriesRead | null>(null);
  let hostBusy = $state(false);
  let procBusy = $state(false);
  let problem = $state('');
  let attempt = $state(0);

  const aborted = (error: unknown) => error instanceof DOMException && error.name === 'AbortError';
  function failure(error: unknown): string {
    if (isCaseGone(error)) onGone?.();
    return error instanceof CaseApiError ? errorText(error) : errorText({ status: 0, reason: 'unreachable', message: '' });
  }
  const fetcher = (request: Parameters<typeof fetchSeries>[1], signal?: AbortSignal) => fetchSeries(caseId, request, signal);

  // The panel hands over a fresh host object after each summary read. These keep a read from repeating when nothing it depends on changed.
  const hostName = $derived(host.host);
  const processKey = $derived(JSON.stringify(host.processes));
  const fromMs = $derived(view.fromMs);
  const toMs = $derived(view.toMs);

  // The host lines. A window change waits a moment, so a run of zoom clicks makes one read.
  $effect(() => {
    const from = fromMs, to = toMs, name = hostName;
    void attempt;
    const mine = new AbortController();
    const timer = setTimeout(async () => {
      hostBusy = true; problem = '';
      try {
        const read = await readSeries(fetcher, { fromMs: from, toMs: to }, hostSelectors(name), mine.signal);
        if (!mine.signal.aborted) hostRead = read;
      } catch (error) {
        if (mine.signal.aborted || aborted(error)) return;
        hostRead = null; problem = failure(error);
      } finally { if (!mine.signal.aborted) hostBusy = false; }
    }, 150);
    return () => { clearTimeout(timer); mine.abort(); };
  });

  // The process lines, for the measure picked. They come from selectors the server named for this host.
  $effect(() => {
    const from = fromMs, to = toMs, metric = procMetric;
    void processKey;
    const refs = untrack(() => processSelectors(host.processes, metric));
    void attempt;
    if (refs.length === 0) { procRead = null; procBusy = false; return; }
    const mine = new AbortController();
    const timer = setTimeout(async () => {
      procBusy = true;
      try {
        const read = await readSeries(fetcher, { fromMs: from, toMs: to }, refs.map(ref => ref.sel), mine.signal);
        if (!mine.signal.aborted) procRead = read;
      } catch (error) {
        if (mine.signal.aborted || aborted(error)) return;
        procRead = null; problem = failure(error);
      } finally { if (!mine.signal.aborted) procBusy = false; }
    }, 150);
    return () => { clearTimeout(timer); mine.abort(); };
  });

  const bucketMs = $derived(hostRead?.bucketMs ?? bucketFor(view.toMs - view.fromMs));
  const hostStrips = $derived(hostRead ? buildStrips(host.host, hostRead.items, hostRead.bucketMs) : []);
  const hostCpu = $derived(hostStrips.find(strip => strip.id === 'host-cpu')?.lines[0] ?? null);
  const procStrip = $derived(procRead ? processStrip(procMetric, processSelectors(host.processes, procMetric), procRead.items, procRead.bucketMs, hostCpu) : null);
  const strips = $derived(orderStrips(apiStrip(apiWindows, view, bucketMs), hostStrips, procStrip));
  const hostMarkers = $derived(markers.filter(marker => marker.host === '' || marker.host === host.host));
  // The panel above says so itself when its own read was partial, so only this chart's reads count here.
  const partial = $derived((hostRead?.partial ?? false) || (procRead?.partial ?? false));
  const busy = $derived(hostBusy || procBusy);
  const hasData = $derived(strips.some(strip => strip.id !== 'api-p95'));
  const label = $derived(stripsSummary(host.host, strips, episodes, hostMarkers, view, zone));
</script>

<div class="lines">
  {#if host.processes.length > 0}
    <div class="measure" role="group" aria-label={resourceCopy.processMetricLabel}>
      <span class="measure-label">{resourceCopy.processesHeading}</span>
      {#each PROC_METRICS as option (option.id)}
        <button type="button" class="pill" aria-pressed={procMetric === option.id} onclick={() => { procMetric = option.id; }}>{option.label}</button>
      {/each}
    </div>
  {:else}
    <p class="note">{resourceCopy.noProcesses}</p>
  {/if}

  <div aria-live="polite" class="status">
    {#if busy}<p class="note" role="status">{resourceCopy.loadingLines}</p>{/if}
    {#if partial}<p class="note" role="status">{resourceCopy.partial}</p>{/if}
    {#if problem}
      <div class="inline-error" role="alert">{problem} <button type="button" class="secondary-button" onclick={() => { attempt += 1; }}>{resourceCopy.retry}</button></div>
    {/if}
  </div>

  {#if strips.length > 0}
    <StripChart {strips} {view} {episodes} markers={hostMarkers} {zone} {label} {onBrush} {onPick} />
    <p class="note">{resourceCopy.brushHint} {resourceCopy.windowNote} {bucketText(bucketMs)}.</p>
  {/if}
  {#if !busy && !problem && hostRead && !hasData}<p class="empty">{resourceCopy.noData}</p>{/if}
</div>

<style>
  .lines { display: grid; gap: var(--space-2); min-width: 0; }
  .note { margin: 0; color: var(--text-2); font-size: var(--fs-2); line-height: 1.5; }
  .empty { margin: 0; padding: 8px 0; border: 0; border-top: 1px solid var(--line); border-radius: 0; background: transparent; font-size: var(--fs-3); }
  .status { display: grid; gap: var(--space-1); }
  .measure { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; }
  .measure-label { margin-right: 4px; color: var(--text-2); font-size: var(--fs-2); font-weight: 500; }
  .pill { padding: 3px 10px; color: var(--text-2); font-size: var(--fs-2); background: transparent; border: 1px solid var(--line); border-radius: var(--radius-sm); }
  .pill:hover { border-color: var(--text-2); }
  .pill[aria-pressed='true'] { color: var(--text); background: var(--surface-2); border-color: var(--accent); }
</style>
