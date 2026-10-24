<script lang="ts">
  import { untrack } from 'svelte';
  import { CaseApiError, fetchResource, isCaseGone } from '../lib/cases';
  import { errorText } from '../lib/copy/importCopy';
  import { app } from '../lib/appState.svelte';
  import { onPatternsChange, patternsWindowKey, readPatterns } from '../lib/drawerResults.svelte';
  import {
    chipsFor, episodesForRequest, rangeText, resourceCopy, rulesAt, rulesOfEpisode, slotRange, spansKey, stallSpans, stepWindow, viewWindow, bucketFor,
    type Chip, type EpisodeSpan, type ResourceView, type RuleRow, type ViewWindow
  } from '../lib/resource';
  import { zoomView } from '../lib/zoomView.svelte';
  import { spanCopy, spanNotice, type Span } from '../lib/resourceSpan';
  import CoverageDots from './resource/CoverageDots.svelte';
  import HostLines from './resource/HostLines.svelte';
  import RuleChips from './resource/RuleChips.svelte';
  import RuleDrawer from './resource/RuleDrawer.svelte';

  // What each host measured around the stalls, in three steps: a line per host with dots for how much data it has (closed), the lines on one
  // time axis (open), and the rule rows behind a click (drawer). The window follows the zoom on screen when there is one; otherwise the panel
  // keeps its own. Nothing here says why something happened: it shows what was measured, where, and which rules the server fired.
  // The stall periods come from the Patterns report for the same sources and window, when one has been run (lib/drawerResults). The server cannot
  // know them, so they go with each read as ep values; with no report, or no stalls in it, none are sent and the stall rules stay quiet.
  // span is the time the case's files cover, when they said. When the window on screen holds no data, the panel offers to show that period.
  // That choice is the panel's own: it does not move the audit window, which other screens read. While it is on, the panel's whole period is
  // the files' period, so the zoom buttons and Whole period work inside it. The stall periods still come from the Patterns report.
  let { caseId, query, zone, span = null, apiWindows = [], onGone }: {
    caseId: string; query: { start: string; end: string; sourceIds?: string[] }; zone: string; span?: Span | null;
    apiWindows?: ReadonlyArray<{ at: string; p95Ms: number | null }>; onGone?: () => void;
  } = $props();

  let resource = $state.raw<ResourceView | null>(null);
  let busy = $state(true);
  let problem = $state('');
  let attempt = $state(0);
  let openHost = $state<string | null>(null);
  let local = $state.raw<ViewWindow | null>(null);
  let drawer = $state.raw<{ title: string; rows: RuleRow[] } | null>(null);
  let pickNote = $state('');

  let period = $state.raw<ViewWindow | null>(null);
  // The window the panel counts as whole: the one on screen, or the files' period once the person asked for it.
  const base = $derived(period ? { start: new Date(period.fromMs).toISOString(), end: new Date(period.toMs).toISOString() } : query);
  const usesStore = $derived(zoomView.slots.length > 0 && period == null);
  const whole = $derived(viewWindow({ local: null, zoomed: false, slots: [], query: base }));
  const view = $derived(viewWindow({ local: usesStore ? null : local, zoomed: zoomView.zoomed, slots: zoomView.slots, query: base }));
  const zoomedNow = $derived(usesStore ? zoomView.zoomed : local != null);
  // The newest Patterns report for this window. It is read again whenever a report is saved or cleared.
  let reportTick = $state(0);
  $effect(() => onPatternsChange(() => { reportTick += 1; }));
  const stalls = $derived.by(() => {
    void reportTick;
    const saved = readPatterns(patternsWindowKey(query.sourceIds ?? [], query.start, query.end), app.sources.map(source => source.id));
    return stallSpans(saved?.report.episodes);
  });
  // What goes with the read: the stall periods that touch the view, capped at the server limit. The key changes only when the list does.
  const sent = $derived(episodesForRequest(stalls, view));
  const sentKey = $derived(spansKey(sent));
  const aborted = (error: unknown) => error instanceof DOMException && error.name === 'AbortError';

  function failure(error: unknown): string {
    if (isCaseGone(error)) onGone?.();
    return error instanceof CaseApiError ? errorText(error) : errorText({ status: 0, reason: 'unreachable', message: '' });
  }

  // The summary, rules, gaps and markers for the window on screen. A window change waits a moment so a run of zoom clicks makes one read.
  $effect(() => {
    const from = view?.fromMs, to = view?.toMs;
    void attempt;
    void sentKey;
    if (from == null || to == null) return;
    const mine = new AbortController();
    const timer = setTimeout(async () => {
      busy = true; problem = '';
      try {
        const next = await fetchResource(caseId, { fromMs: from, toMs: to, episodes: untrack(() => sent) }, mine.signal);
        if (!mine.signal.aborted) resource = next;
      } catch (error) {
        if (mine.signal.aborted || aborted(error)) return;
        problem = failure(error);
      } finally { if (!mine.signal.aborted) busy = false; }
    }, 150);
    return () => { clearTimeout(timer); mine.abort(); };
  });

  const hosts = $derived(resource?.hosts ?? []);
  const rules = $derived(resource?.rules ?? []);
  const episodes = $derived(resource?.episodes ?? []);
  const markers = $derived(resource?.markers ?? []);
  const gaps = $derived(resource?.gaps ?? []);
  const chips = $derived(chipsFor(rules, null));
  // The notice shows only for an empty answer that was read for the window on screen; a window with data never shows it.
  const notice = $derived(spanNotice({ resource, settled: !busy && problem === '', span, view, zone }));
  // The panel is hidden when the case has no hosts. While the first read is going, or after it failed, it says so instead. An empty answer holds
  // no hosts when the files did not name one, so the span notice (and the way back from the period it opened) keeps the panel on screen.
  const shown = $derived(hosts.length > 0 || notice != null || period != null || (resource == null && (busy || problem !== '')));
  const open = $derived(hosts.find(item => item.host === openHost) ?? null);
  function showPeriod() {
    if (!notice) return;
    pickNote = '';
    local = null;
    period = notice.window;
  }
  function backToScreen() { pickNote = ''; local = null; period = null; }

  function brush(fromMs: number, toMs: number) {
    pickNote = '';
    if (usesStore) {
      const range = slotRange(zoomView.slots, fromMs, toMs);
      if (range) zoomView.zoomIndexRange(range[0], range[1]);
    } else local = { fromMs: Math.round(Math.min(fromMs, toMs)), toMs: Math.round(Math.max(fromMs, toMs)) };
  }
  function step(action: 'in' | 'out' | 'earlier' | 'later') {
    pickNote = '';
    if (usesStore) {
      if (action === 'in') zoomView.zoomIn(); else if (action === 'out') zoomView.zoomOut(); else if (action === 'earlier') zoomView.earlier(); else zoomView.later();
    } else if (view && whole) local = stepWindow(view, whole, action);
  }
  function resetZoom() {
    pickNote = '';
    if (usesStore) zoomView.reset(); else local = null;
  }

  function showRows(title: string, rows: RuleRow[]) { drawer = { title, rows }; }
  function openChip(chip: Chip) { showRows(chip.text, rules.filter(rule => rule.id === chip.id && rule.host === chip.host && rule.wording === chip.wording)); }
  function openEpisode(span: EpisodeSpan) { showRows(resourceCopy.drawerHeading, rulesOfEpisode(rules, span, null)); }
  function pick(t: number) {
    const rows = rulesAt(rules, t, view ? bucketFor(view.toMs - view.fromMs) : 0, openHost);
    if (rows.length > 0) { pickNote = ''; showRows(resourceCopy.drawerHeading, rows); }
    else pickNote = resourceCopy.drawerNoRows + ' ' + rangeText(t, t, zone);
  }
  function toggle(name: string) { openHost = openHost === name ? null : name; pickNote = ''; }
  function closeDrawer() { drawer = null; }
</script>

{#if shown}
  <section class="tools" aria-labelledby="resource-heading" aria-busy={busy}>
    <h3 id="resource-heading">{resourceCopy.heading}</h3>
    <p class="note">{resourceCopy.intro}</p>

    <div aria-live="polite" class="status">
      {#if busy && hosts.length === 0 && !problem}<p class="note" role="status">{resourceCopy.loading}</p>{/if}
      {#if problem}
        <div class="inline-error" role="alert">{problem} <button type="button" class="secondary-button" onclick={() => { attempt += 1; }}>{resourceCopy.retry}</button></div>
      {/if}
      {#if resource?.partial}<p class="note" role="status">{resourceCopy.partial}</p>{/if}
      {#if notice}
        <p class="span-note" role="status">
          <span>{notice.text}</span>
          <button type="button" class="secondary-button" aria-label={spanCopy.buttonLabel} onclick={showPeriod}>{spanCopy.button}</button>
        </p>
      {/if}
      {#if period}
        <p class="span-note" role="status">
          <span>{spanCopy.shown}</span>
          <button type="button" class="secondary-button" onclick={backToScreen}>{spanCopy.back}</button>
        </p>
      {/if}
    </div>

    {#if hosts.length > 0}
      <div class="chips-block">
        <h4>{resourceCopy.chipsHeading}</h4>
        <RuleChips {chips} showHost={hosts.length > 1} onOpen={openChip} />
      </div>

      <ul class="hosts">
        {#each hosts as item, hi (item.host)}
          <li class="host" class:open={openHost === item.host}>
            <div class="host-row">
              <div class="host-text">
                <h4>{item.host}</h4>
                {#if item.summary.length === 0}
                  <p class="line">{resourceCopy.noSummary}</p>
                {:else}
                  {#each item.summary as sentence, i (i)}<p class="line">{sentence}</p>{/each}
                {/if}
                <CoverageDots coverage={item.coverage} />
              </div>
              <button type="button" class="secondary-button toggle" aria-expanded={openHost === item.host} aria-controls={'resource-lines-' + hi}
                aria-label={(openHost === item.host ? resourceCopy.closeLabel : resourceCopy.openLabel) + ' ' + item.host} onclick={() => toggle(item.host)}>
                {openHost === item.host ? resourceCopy.close : resourceCopy.open}
              </button>
            </div>
            {#if open && open.host === item.host && view}
              <div class="expanded" id={'resource-lines-' + hi}>
                <div class="zoom" role="group" aria-label={resourceCopy.windowLabel}>
                  <span class="range">{resourceCopy.windowLabel}: {rangeText(view.fromMs, view.toMs, zone)}</span>
                  <button type="button" class="secondary-button" onclick={() => step('in')}>{resourceCopy.zoomIn}</button>
                  <button type="button" class="secondary-button" disabled={!zoomedNow} onclick={() => step('out')}>{resourceCopy.zoomOut}</button>
                  <button type="button" class="secondary-button" disabled={!zoomedNow} onclick={() => step('earlier')}>{resourceCopy.earlier}</button>
                  <button type="button" class="secondary-button" disabled={!zoomedNow} onclick={() => step('later')}>{resourceCopy.later}</button>
                  <button type="button" class="secondary-button" disabled={!zoomedNow} onclick={resetZoom}>{resourceCopy.resetZoom}</button>
                </div>
                <HostLines {caseId} host={open} {view} {episodes} {markers} {zone} {apiWindows} onBrush={brush} onPick={pick} {onGone} />
                <p class="note" role="status">{pickNote}</p>
                <div class="lists">
                  <h4>{resourceCopy.episodesHeading}</h4>
                  {#if episodes.length === 0}
                    <p class="note">{resourceCopy.noEpisodes}</p>
                  {:else}
                    <ul class="plain">
                      {#each episodes as span, i (i)}
                        <li><button type="button" class="link" onclick={() => openEpisode(span)}>{rangeText(span.start, span.end, zone)}</button> <span class="note">{resourceCopy.episodeOpen}</span></li>
                      {/each}
                    </ul>
                  {/if}
                  <h4>{resourceCopy.markersHeading}</h4>
                  {#if markers.length === 0}
                    <p class="note">{resourceCopy.noMarkers}</p>
                  {:else}
                    <ul class="plain">{#each markers as marker, i (i)}<li>{rangeText(marker.t, marker.t, zone)}: {marker.text}</li>{/each}</ul>
                  {/if}
                </div>
              </div>
            {/if}
          </li>
        {/each}
      </ul>

      {#if gaps.length > 0}
        <div class="lists">
          <h4>{resourceCopy.gapsHeading}</h4>
          <ul class="plain">{#each gaps as gap, i (i)}<li>{gap}</li>{/each}</ul>
        </div>
      {/if}
    {/if}
  </section>
{/if}

{#if drawer}<RuleDrawer title={drawer.title} rows={drawer.rows} {zone} onClose={closeDrawer} />{/if}

<style>
  .tools { display: grid; gap: var(--space-2); min-width: 0; padding-top: var(--space-4); border-top: 1px solid var(--hairline); }
  h3 { margin: 0; font-size: var(--fs-4); font-weight: 600; }
  h4 { margin: var(--space-2) 0 0; color: var(--text-2); font-size: var(--fs-2); font-weight: 650; }
  .note { margin: 0; color: var(--text-2); font-size: var(--fs-2); line-height: 1.5; }
  .status { display: grid; gap: var(--space-1); }
  .span-note { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); margin: 0; color: var(--text-2); font-size: var(--fs-2); line-height: 1.5; }
  .chips-block { display: grid; gap: var(--space-1); }
  .hosts { display: grid; gap: var(--space-3); margin: 0; padding: 0; list-style: none; }
  .host { display: grid; gap: var(--space-2); min-width: 0; padding: var(--space-3); border: 1px solid var(--line); border-radius: var(--radius); background: var(--surface); }
  .host-row { display: flex; align-items: flex-start; justify-content: space-between; gap: var(--space-3); }
  .host-text { display: grid; gap: 4px; min-width: 0; }
  .host-text h4 { margin: 0; color: var(--text); font-size: var(--fs-3); }
  .line { margin: 0; font-size: var(--fs-3); line-height: 1.45; overflow-wrap: anywhere; }
  .toggle { flex: 0 0 auto; }
  .expanded { display: grid; gap: var(--space-2); min-width: 0; padding-top: var(--space-2); border-top: 1px solid var(--hairline); }
  .zoom { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; }
  .range { flex: 1 1 100%; color: var(--text-2); font-size: var(--fs-2); font-variant-numeric: tabular-nums; }
  .lists { display: grid; gap: 4px; }
  .plain { display: grid; gap: 4px; margin: 0; padding: 0 0 0 18px; font-size: var(--fs-2); line-height: 1.5; }
  .link { padding: 0; color: var(--accent); font-size: var(--fs-2); text-decoration: underline; background: transparent; border: 0; }
  @media (max-width: 480px) {
    .host-row { flex-direction: column; }
    .toggle { width: 100%; }
  }
</style>
