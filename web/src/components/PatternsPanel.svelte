<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../lib/api';
  import { formatLocal, quickRange, quickRanges, type QuickRangeId } from '../lib/time';
  import { activeRangeId } from '../lib/depth';
  import { app, closeSidePanel, onScopeChange, scope, showRecord } from '../lib/appState.svelte';
  import { citeInLedger } from '../lib/citeInLedger';
  import { formatElapsed } from '../lib/provenance';
  import {
    DEFAULT_EPISODE_GAP_SECONDS, DEFAULT_STALL_SECONDS, PATTERNS_TEXT, PATTERN_RANGES, auditStallLine, citeLabel, coverageLine, emptyEpisodesText, endGapsText,
    episodeCiteLabel, episodeEvidenceId, findingRows, isEvidenceId, nBadge, operationText, qualityLine, secondsLabel, stallsOf, thresholdsChanged,
    truncationNotices, verdictText, volleysOf, windowInputSeconds
  } from '../lib/patterns';
  import type { PatternReport, Query, RequestRecord } from '../lib/types';
  import Hint from './Hint.svelte';
  import ProvenanceNote from './ProvenanceNote.svelte';
  import Drawer from './Drawer.svelte';

  // The Patterns drawer. It never runs by itself: the button starts one search over the window already on screen.
  // Summary first (verdict, findings, counts), then the episode table, which opens one episode at a time.
  // The range chips change the page's own range. A slow request or a slow audit save opens its record (and closes this drawer).
  let { query, zone, sourceLabels, onClose }: { query: Query; zone: string; sourceLabels: Map<string, string>; onClose: () => void } = $props();
  type State = { kind: 'idle' } | { kind: 'running' } | { kind: 'done'; report: PatternReport; key: string } | { kind: 'error'; text: string };
  let phase = $state<State>({ kind: 'idle' });
  let stallSec = $state(String(DEFAULT_STALL_SECONDS));
  let gapSec = $state(String(DEFAULT_EPISODE_GAP_SECONDS));
  let thresholdError = $state('');
  let openEpisodes = $state<number[]>([]);
  let citeNote = $state('');
  let runButton = $state<HTMLButtonElement>();
  let controller: AbortController | null = null;

  const sources = $derived(query.sourceIds.map((id) => sourceLabels.get(id) ?? id).join(', '));
  const timeOptions: Intl.DateTimeFormatOptions = { year: 'numeric', month: 'short', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false };
  const at = (value: string) => formatLocal(value, zone, timeOptions);
  const clock = (value: string) => formatLocal(value, zone, { hour: '2-digit', minute: '2-digit', hour12: false });
  const labelOf = (id: string) => sourceLabels.get(id) ?? id;
  const activeRange = $derived(activeRangeId(query, quickRanges));
  const changed = $derived(thresholdsChanged(stallSec, gapSec));
  // What the search depends on. When it moves after a run, the numbers on screen describe an older question.
  const askKey = $derived([query.sourceIds.join(','), query.start, query.end, stallSec.trim(), gapSec.trim()].join('|'));

  onMount(() => runButton?.focus());
  $effect(() => () => controller?.abort());

  function pickRange(id: QuickRangeId) {
    if (activeRange === id) return;
    const base = new Date(app.info?.now ?? '');
    Object.assign(scope.query, quickRange(id, Number.isFinite(base.getTime()) ? base : new Date()));
    scope.dateInputsValid = true;
    onScopeChange();
  }
  function reset() { stallSec = String(DEFAULT_STALL_SECONDS); gapSec = String(DEFAULT_EPISODE_GAP_SECONDS); thresholdError = ''; }

  async function run() {
    if (phase.kind === 'running') return;
    const built = windowInputSeconds(query, stallSec, gapSec);
    if ('error' in built) { thresholdError = built.error; return; }
    thresholdError = '';
    controller?.abort();
    controller = new AbortController();
    const mine = controller;
    const key = askKey;
    phase = { kind: 'running' };
    openEpisodes = [];
    citeNote = '';
    try {
      const report = await api.patterns(built.input, mine.signal);
      if (mine.signal.aborted) return;
      phase = { kind: 'done', report, key };
    } catch (cause) {
      if (mine.signal.aborted) return;
      phase = { kind: 'error', text: cause instanceof Error && cause.message ? cause.message : PATTERNS_TEXT.failed };
    }
  }
  function toggle(index: number) {
    openEpisodes = openEpisodes.includes(index) ? openEpisodes.filter((i) => i !== index) : [...openEpisodes, index];
  }
  // The record view needs only the source and the id of the request. The drawer closes first so the record is not hidden behind it.
  function openRecord(sourceId: string, id: string) {
    void closeSidePanel();
    void showRecord({ sourceId, id } as RequestRecord);
  }
  async function cite(evidenceId: string, label: string) {
    const result = await citeInLedger(evidenceId, label);
    citeNote = result === 'added' ? PATTERNS_TEXT.citeAdded : result === 'copied' ? PATTERNS_TEXT.citeCopied : PATTERNS_TEXT.citeFailed;
  }
</script>

<Drawer class="glossary-drawer patterns-drawer" title={PATTERNS_TEXT.title} titleId="patterns-title" intro={PATTERNS_TEXT.intro} closeLabel="Close patterns" focusable={'button:not(:disabled), input:not(:disabled), summary, [href], [tabindex]:not([tabindex="-1"])'} {onClose}>

  <section class="patterns-window" aria-label="Window">
    <div class="range-row" role="group" aria-label={PATTERNS_TEXT.rangeLabel}>
      <span class="range-label">{PATTERNS_TEXT.rangeLabel}</span>
      {#each PATTERN_RANGES as preset (preset.id)}
        <button type="button" class="chip" class:on={activeRange === preset.id} aria-pressed={activeRange === preset.id} onclick={() => pickRange(preset.id)}>{preset.label}</button>
      {/each}
    </div>
    <p class="patterns-note">{PATTERNS_TEXT.rangeNote}</p>
    <dl>
      <div><dt>Sources</dt><dd>{sources || 'None selected'}</dd></div>
      <div><dt>From</dt><dd>{at(query.start)}</dd></div>
      <div><dt>To</dt><dd>{at(query.end)}</dd></div>
      <div><dt>Time zone</dt><dd>{zone}</dd></div>
    </dl>
    <details class="patterns-thresholds">
      <summary>{PATTERNS_TEXT.thresholds}{#if changed} ({PATTERNS_TEXT.thresholdsChanged}){/if}</summary>
      <div class="threshold-fields">
        <label>{PATTERNS_TEXT.stallLabel}<input type="text" inputmode="decimal" bind:value={stallSec} autocomplete="off" /></label>
        <label>{PATTERNS_TEXT.gapLabel}<input type="text" inputmode="numeric" bind:value={gapSec} autocomplete="off" /></label>
      </div>
      <p class="patterns-note">{PATTERNS_TEXT.thresholdNote}</p>
      {#if changed}<button type="button" class="reset-link" onclick={reset}>{PATTERNS_TEXT.reset}</button>{/if}
    </details>
    {#if thresholdError}<div class="inline-error" role="alert">{thresholdError}</div>{/if}
    <button type="button" class="primary-button patterns-run" bind:this={runButton} disabled={phase.kind === 'running' || query.sourceIds.length === 0} onclick={run}>{phase.kind === 'running' ? PATTERNS_TEXT.running : PATTERNS_TEXT.run}</button>
    <p class="patterns-note">{PATTERNS_TEXT.runNote}</p>
  </section>

  <div aria-live="polite">
    {#if phase.kind === 'error'}<div class="inline-error" role="alert">{phase.text}</div>{/if}
    {#if phase.kind === 'done'}
      {@const report = phase.report}
      {@const coverage = coverageLine(report.coverage)}
      {@const rows = findingRows(report)}
      {#if phase.key !== askKey}<div class="notice" role="status">{PATTERNS_TEXT.stale}</div>{/if}
      <section class="patterns-summary" aria-label="Summary">
        <p class="patterns-verdict">{verdictText(report)}<Hint termId="episode" /></p>
        {#if citeNote}<p class="cite-note" role="status">{citeNote}</p>{/if}
        {#if rows.length}
          <ul class="finding-list">
            {#each rows as finding (finding.key)}
              <li class="finding">
                <div class="finding-meta">
                  <span class="evidence-badge" class:strong={finding.label === 'Strong correlation'}>{finding.label}</span>
                  <span class="finding-n">{nBadge(finding.n)}</span>
                  {#if finding.lowN}<span class="low-n">{PATTERNS_TEXT.lowN}</span>{/if}
                </div>
                <p>{finding.headline}</p>
                {#if finding.evidenceId}
                  <p class="finding-ref" title={PATTERNS_TEXT.referenceNote}><span class="finding-ref-label">{PATTERNS_TEXT.reference}</span> <code>{finding.evidenceId}</code>
                    {#if isEvidenceId(finding.evidenceId)}<button type="button" class="cite-button" aria-label={PATTERNS_TEXT.cite + ': ' + citeLabel(finding.headline)} onclick={() => cite(finding.evidenceId, finding.headline)}>{PATTERNS_TEXT.cite}</button>{/if}
                  </p>
                {/if}
              </li>
            {/each}
          </ul>
        {/if}
        <p class="patterns-quality">{qualityLine(report.quality)}<Hint termId="incomplete-request" /></p>
        {#if coverage}<p class="patterns-quality">{coverage}<Hint termId="coverage" /></p>{/if}
        {#each truncationNotices(report) as notice (notice)}<div class="notice" role="status">{notice}</div>{/each}
        {#each report.warnings as warning (warning)}<div class="notice" role="status">{warning}</div>{/each}
        <ProvenanceNote provenance={report.provenance} {sourceLabels} />
      </section>

      <section class="patterns-detail" aria-label={PATTERNS_TEXT.episodes}>
        <h3>{PATTERNS_TEXT.episodes}</h3>
        {#if report.episodes.length === 0}
          <div class="empty-state patterns-empty"><strong>{PATTERNS_TEXT.emptyTitle}</strong><p>{emptyEpisodesText(report.params.stallMs)}</p></div>
        {:else}
          <div class="table-scroll">
            <table class="patterns-table">
              <thead><tr><th scope="col">Start</th><th scope="col">End</th><th scope="col" class="num">Requests</th><th scope="col" class="num">Users</th><th scope="col" class="num">Longest</th><th scope="col">Operations</th><th scope="col">Ledger</th></tr></thead>
              <tbody>
                {#each report.episodes as episode (episode.index)}
                  {@const isOpen = openEpisodes.includes(episode.index)}
                  {@const episodeId = episodeEvidenceId(episode.start)}
                  <tr>
                    <th scope="row"><button type="button" class="episode-toggle" aria-expanded={isOpen} aria-controls={'episode-' + episode.index} onclick={() => toggle(episode.index)}>{at(episode.start)}</button></th>
                    <td>{at(episode.end)}</td>
                    <td class="num">{episode.requests}</td>
                    <td class="num">{episode.users}</td>
                    <td class="num">{formatElapsed(episode.maxMs)}</td>
                    <td class="ops">{episode.operations.join(', ')}</td>
                    <td>{#if episodeId}<button type="button" class="cite-button" aria-label={PATTERNS_TEXT.cite + ': episode at ' + at(episode.start)} onclick={() => cite(episodeId, episodeCiteLabel(clock(episode.start)))}>{PATTERNS_TEXT.cite}</button>{/if}</td>
                  </tr>
                  {#if isOpen}
                    {@const volleys = volleysOf(report, episode)}
                    {@const stalls = stallsOf(report, episode)}
                    <tr class="episode-detail" id={'episode-' + episode.index}>
                      <td colspan="7">
                        <h4>{PATTERNS_TEXT.volleys}</h4>
                        {#if volleys.length === 0}<p class="patterns-note">{PATTERNS_TEXT.none}</p>{:else}
                          <table class="patterns-table inner">
                            <thead><tr><th scope="col">User id</th><th scope="col" class="num">Requests</th><th scope="col" class="num">Start spread</th><th scope="col" class="num">End stagger</th><th scope="col">End gaps</th></tr></thead>
                            <tbody>
                              {#each volleys as volley (volley.userId + volley.firstStart)}
                                <tr><td>{volley.userId}</td><td class="num">{volley.requests}</td><td class="num">{volley.startSpreadMs} ms</td><td class="num">{volley.endStaggerMs} ms</td><td>{endGapsText(volley)}</td></tr>
                              {/each}
                            </tbody>
                          </table>
                        {/if}
                        <h4>{PATTERNS_TEXT.stalls}</h4>
                        {#if stalls.length === 0}<p class="patterns-note">{PATTERNS_TEXT.none}</p>{:else}
                          <table class="patterns-table inner">
                            <thead><tr><th scope="col">Operation</th><th scope="col">Started</th><th scope="col" class="num">Observed</th></tr></thead>
                            <tbody>
                              {#each stalls as stall (stall.sourceId + stall.id)}
                                <tr>
                                  <td class="ops"><button type="button" class="record-link" title={PATTERNS_TEXT.openRecord} aria-label={PATTERNS_TEXT.openRecord + ': ' + operationText(stall) + ', started ' + at(stall.initiatedAt)} onclick={() => openRecord(stall.sourceId, stall.id)}>{operationText(stall)}</button>{#if query.sourceIds.length > 1} <span class="source-tag">{labelOf(stall.sourceId)}</span>{/if}</td>
                                  <td>{at(stall.initiatedAt)}</td>
                                  <td class="num">{secondsLabel(stall.observedMs)}</td>
                                </tr>
                              {/each}
                            </tbody>
                          </table>
                        {/if}
                      </td>
                    </tr>
                  {/if}
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
      </section>

      {#if report.auditStalls.length}
        <section class="patterns-detail" aria-label={PATTERNS_TEXT.auditStalls}>
          <h3>{PATTERNS_TEXT.auditStalls}</h3>
          <ul class="audit-list">
            {#each report.auditStalls as stall (stall.sourceId + stall.id)}
              <li>
                <span class="ops"><button type="button" class="record-link" title={PATTERNS_TEXT.openRecord} aria-label={PATTERNS_TEXT.openRecord + ': ' + operationText(stall) + ', started ' + at(stall.initiatedAt)} onclick={() => openRecord(stall.sourceId, stall.id)}>{operationText(stall)}</button>{#if query.sourceIds.length > 1} <span class="source-tag">{labelOf(stall.sourceId)}</span>{/if}</span>
                <span class="audit-when">{at(stall.initiatedAt)}</span>
                <span class="audit-line">{auditStallLine(stall)}</span>
              </li>
            {/each}
          </ul>
        </section>
      {/if}
    {/if}
  </div>
</Drawer>

<style>
  :global(.connections-drawer.patterns-drawer) { overflow-x: hidden; gap: var(--space-3); }
  .patterns-window { display: grid; gap: 10px; margin-top: 13px; min-width: 0; }
  .range-row { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; }
  .range-label { margin-right: var(--space-1); color: var(--text-2); font-size: var(--fs-2); }
  .patterns-window dl { display: grid; gap: 4px; margin: 0; font-size: var(--fs-3); }
  .patterns-window dl div { display: flex; gap: 10px; min-width: 0; }
  .patterns-window dt { flex: 0 0 74px; color: var(--text-2); }
  .patterns-window dd { margin: 0; min-width: 0; overflow-wrap: anywhere; }
  .patterns-thresholds summary { width: fit-content; color: var(--text-2); font-size: var(--fs-3); }
  .threshold-fields { display: grid; grid-template-columns: repeat(auto-fit, minmax(170px, 1fr)); gap: 10px; margin-top: 8px; }
  .threshold-fields label { display: grid; gap: 4px; color: var(--text-2); font-size: var(--fs-2); }
  .threshold-fields input { min-width: 0; min-height: 36px; padding: 6px 10px; color: var(--text); background: var(--bg); border: 1px solid var(--control-line); border-radius: var(--radius); font-size: var(--fs-4); font-variant-numeric: tabular-nums; }
  .patterns-thresholds .patterns-note { margin-top: var(--space-2); }
  .reset-link { margin-top: var(--space-1); padding: 3px 0; color: var(--text-2); background: none; border: 0; font-size: var(--fs-2); text-decoration: underline; text-underline-offset: 3px; cursor: pointer; }
  .reset-link:hover { color: var(--text); }
  .reset-link:focus-visible, .cite-button:focus-visible, .record-link:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; border-radius: var(--radius); }
  .patterns-run { justify-self: start; }
  .patterns-note { margin: 0; color: var(--text-2); font-size: var(--fs-2); line-height: 1.5; }
  .patterns-summary { display: grid; gap: 10px; min-width: 0; }
  .patterns-verdict { margin: 0; font-size: var(--fs-5); font-weight: 600; }
  .cite-note { margin: 0; color: var(--text-2); font-size: var(--fs-2); }
  .finding-list { display: grid; gap: 8px; margin: 0; padding: 0; list-style: none; }
  .finding { padding: 9px 12px; background: var(--surface-2); border: 1px solid var(--line-soft); border-radius: var(--radius); }
  .finding p { margin: 6px 0 0; font-size: var(--fs-3); overflow-wrap: anywhere; }
  .finding-meta { display: flex; flex-wrap: wrap; align-items: center; gap: 6px 10px; font-size: var(--fs-2); }
  .evidence-badge { padding: 2px 8px; color: var(--text); border: 1px solid var(--line); border-radius: 4px; font-weight: 650; }
  .evidence-badge.strong { border-color: var(--accent); }
  .finding-n { color: var(--text-2); font-variant-numeric: tabular-nums; }
  .finding p.finding-ref { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 8px; margin-top: 4px; color: var(--text-2); font-size: var(--fs-1); }
  .finding-ref code { font-family: var(--mono); user-select: all; overflow-wrap: anywhere; }
  .cite-button { padding: 3px 0; color: var(--text-2); background: none; border: 0; font-size: var(--fs-2); text-decoration: underline; text-underline-offset: 3px; cursor: pointer; white-space: nowrap; }
  .cite-button:hover, .record-link:hover { color: var(--text); }
  .record-link { padding: 0; color: var(--text); background: none; border: 0; font: inherit; text-align: left; text-decoration: underline; text-underline-offset: 3px; cursor: pointer; overflow-wrap: anywhere; }
  .low-n { padding: 1px 6px; color: var(--warn); border: 1px solid var(--warn); border-radius: 4px; }
  .patterns-quality { margin: 0; color: var(--text-2); font-size: var(--fs-2); line-height: 1.5; }
  .patterns-detail { margin-top: var(--space-4); min-width: 0; }
  .patterns-detail h3 { margin: 0 0 8px; font-size: var(--fs-4); font-weight: 600; }
  .patterns-detail h4 { margin: 10px 0 6px; color: var(--text-2); font-size: var(--fs-2); font-weight: 650; }
  .patterns-empty { padding: var(--space-4); }
  .table-scroll { overflow-x: auto; border: 1px solid var(--line-soft); border-radius: var(--radius); }
  .patterns-table { width: 100%; border-collapse: collapse; font-size: var(--fs-2); }
  .patterns-table th, .patterns-table td { padding: 5px 8px; text-align: left; vertical-align: top; border-bottom: 1px solid var(--line-soft); font-weight: 400; }
  .patterns-table thead th { background: var(--surface-2); color: var(--text-2); font-weight: 600; white-space: nowrap; }
  .patterns-table .num { text-align: right; font-variant-numeric: tabular-nums; white-space: nowrap; }
  .patterns-table .ops { overflow-wrap: anywhere; }
  .patterns-table.inner { margin-bottom: 4px; }
  .episode-detail > td { background: var(--surface-2); }
  .episode-toggle { padding: 0; color: var(--text); background: none; border: 0; font: inherit; font-weight: 600; text-align: left; text-decoration: underline; text-underline-offset: 3px; cursor: pointer; }
  .episode-toggle:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; border-radius: var(--radius); }
  .audit-list { display: grid; gap: 8px; margin: 0; padding: 0; list-style: none; }
  .audit-list li { display: grid; justify-items: start; gap: 2px; padding: 8px 12px; background: var(--surface-2); border: 1px solid var(--line-soft); border-radius: var(--radius); font-size: var(--fs-3); }
  .audit-when { color: var(--text-2); font-size: var(--fs-2); }
  .source-tag { color: var(--text-2); font-size: var(--fs-1); }
</style>
