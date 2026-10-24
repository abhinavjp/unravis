<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../lib/api';
  import { formatLocal } from '../lib/time';
  import { formatElapsed } from '../lib/provenance';
  import {
    DEFAULT_EPISODE_GAP_SECONDS, DEFAULT_STALL_MS, PATTERNS_TEXT, auditStallLine, coverageLine, endGapsText, nBadge, operationText,
    qualityLine, secondsLabel, stallsOf, verdictText, volleysOf, windowInput
  } from '../lib/patterns';
  import type { PatternReport, Query } from '../lib/types';
  import Hint from './Hint.svelte';
  import ProvenanceNote from './ProvenanceNote.svelte';

  // The Patterns drawer. It never runs by itself: the button starts one search over the window already on screen.
  // Summary first (verdict, findings, counts), then the episode table, which opens one episode at a time.
  let { query, zone, sourceLabels, onClose }: { query: Query; zone: string; sourceLabels: Map<string, string>; onClose: () => void } = $props();
  type State = { kind: 'idle' } | { kind: 'running' } | { kind: 'done'; report: PatternReport } | { kind: 'error'; text: string };
  let phase = $state<State>({ kind: 'idle' });
  let stallRaw = $state('');
  let gapRaw = $state('');
  let thresholdError = $state('');
  let openEpisodes = $state<number[]>([]);
  let drawer = $state<HTMLElement>();
  let runButton = $state<HTMLButtonElement>();
  let controller: AbortController | null = null;

  const sources = $derived(query.sourceIds.map((id) => sourceLabels.get(id) ?? id).join(', '));
  const timeOptions: Intl.DateTimeFormatOptions = { year: 'numeric', month: 'short', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false };
  const at = (value: string) => formatLocal(value, zone, timeOptions);
  const labelOf = (id: string) => sourceLabels.get(id) ?? id;

  onMount(() => runButton?.focus());
  $effect(() => () => controller?.abort());

  async function run() {
    if (phase.kind === 'running') return;
    const built = windowInput(query, stallRaw, gapRaw);
    if ('error' in built) { thresholdError = built.error; return; }
    thresholdError = '';
    controller?.abort();
    controller = new AbortController();
    const mine = controller;
    phase = { kind: 'running' };
    openEpisodes = [];
    try {
      const report = await api.patterns(built.input, mine.signal);
      if (mine.signal.aborted) return;
      phase = { kind: 'done', report };
    } catch (cause) {
      if (mine.signal.aborted) return;
      phase = { kind: 'error', text: cause instanceof Error && cause.message ? cause.message : PATTERNS_TEXT.failed };
    }
  }
  function toggle(index: number) {
    openEpisodes = openEpisodes.includes(index) ? openEpisodes.filter((i) => i !== index) : [...openEpisodes, index];
  }
  function onKey(event: KeyboardEvent) {
    if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); onClose(); return; }
    if (event.key !== 'Tab' || !drawer) return;
    const focusable = [...drawer.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), summary, [href], [tabindex]:not([tabindex="-1"])')];
    if (!focusable.length) return;
    const first = focusable[0], last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  }
</script>

<button type="button" class="drawer-scrim" aria-label="Close patterns" onclick={onClose}></button>
<div class="connections-drawer glossary-drawer patterns-drawer" bind:this={drawer} onkeydown={onKey} role="dialog" aria-modal="true" aria-labelledby="patterns-title" tabindex="-1">
  <div class="drawer-header">
    <div><h2 id="patterns-title">{PATTERNS_TEXT.title}</h2></div>
    <button type="button" class="icon-button drawer-close" aria-label="Close patterns" onclick={onClose}>×</button>
  </div>

  <section class="patterns-window" aria-label="Window">
    <dl>
      <div><dt>Sources</dt><dd>{sources || 'None selected'}</dd></div>
      <div><dt>From</dt><dd>{at(query.start)}</dd></div>
      <div><dt>To</dt><dd>{at(query.end)}</dd></div>
      <div><dt>Time zone</dt><dd>{zone}</dd></div>
    </dl>
    <details class="patterns-thresholds">
      <summary>{PATTERNS_TEXT.thresholds}</summary>
      <div class="threshold-fields">
        <label>{PATTERNS_TEXT.stallLabel}<input type="text" inputmode="numeric" bind:value={stallRaw} placeholder={String(DEFAULT_STALL_MS)} autocomplete="off" /></label>
        <label>{PATTERNS_TEXT.gapLabel}<input type="text" inputmode="numeric" bind:value={gapRaw} placeholder={String(DEFAULT_EPISODE_GAP_SECONDS)} autocomplete="off" /></label>
      </div>
      <p class="patterns-note">{PATTERNS_TEXT.thresholdNote} Defaults: {DEFAULT_STALL_MS} ms and {DEFAULT_EPISODE_GAP_SECONDS} s.</p>
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
      <section class="patterns-summary" aria-label="Summary">
        <p class="patterns-verdict">{verdictText(report)}<Hint termId="episode" /></p>
        {#if report.findings.length}
          <ul class="finding-list">
            {#each report.findings as finding (finding.rule + finding.evidenceId)}
              <li class="finding">
                <div class="finding-meta">
                  <span class="evidence-badge" class:strong={finding.label === 'Strong correlation'}>{finding.label}</span>
                  <span class="finding-n">{nBadge(finding.n)}</span>
                  {#if finding.lowN}<span class="low-n">{PATTERNS_TEXT.lowN}</span>{/if}
                </div>
                <p>{finding.headline}</p>
              </li>
            {/each}
          </ul>
        {/if}
        <p class="patterns-quality">{qualityLine(report.quality)}<Hint termId="incomplete-request" /></p>
        {#if coverage}<p class="patterns-quality">{coverage}<Hint termId="coverage" /></p>{/if}
        {#if report.truncated}<div class="notice" role="status">{PATTERNS_TEXT.truncated}</div>{/if}
        {#each report.warnings as warning (warning)}<div class="notice" role="status">{warning}</div>{/each}
        <ProvenanceNote provenance={report.provenance} {sourceLabels} />
      </section>

      <section class="patterns-detail" aria-label={PATTERNS_TEXT.episodes}>
        <h3>{PATTERNS_TEXT.episodes}</h3>
        {#if report.episodes.length === 0}
          <p class="patterns-note">{PATTERNS_TEXT.episodesNone}</p>
        {:else}
          <div class="table-scroll">
            <table class="patterns-table">
              <thead><tr><th scope="col">Start</th><th scope="col">End</th><th scope="col" class="num">Requests</th><th scope="col" class="num">Users</th><th scope="col" class="num">Longest</th><th scope="col">Operations</th></tr></thead>
              <tbody>
                {#each report.episodes as episode (episode.index)}
                  {@const isOpen = openEpisodes.includes(episode.index)}
                  <tr>
                    <th scope="row"><button type="button" class="episode-toggle" aria-expanded={isOpen} aria-controls={'episode-' + episode.index} onclick={() => toggle(episode.index)}>{at(episode.start)}</button></th>
                    <td>{at(episode.end)}</td>
                    <td class="num">{episode.requests}</td>
                    <td class="num">{episode.users}</td>
                    <td class="num">{formatElapsed(episode.maxMs)}</td>
                    <td class="ops">{episode.operations.join(', ')}</td>
                  </tr>
                  {#if isOpen}
                    {@const volleys = volleysOf(report, episode)}
                    {@const stalls = stallsOf(report, episode)}
                    <tr class="episode-detail" id={'episode-' + episode.index}>
                      <td colspan="6">
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
                                  <td class="ops">{operationText(stall)}{#if query.sourceIds.length > 1} <span class="source-tag">{labelOf(stall.sourceId)}</span>{/if}</td>
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
                <span class="ops">{operationText(stall)}{#if query.sourceIds.length > 1} <span class="source-tag">{labelOf(stall.sourceId)}</span>{/if}</span>
                <span class="audit-when">{at(stall.initiatedAt)}</span>
                <span class="audit-line">{auditStallLine(stall)}</span>
              </li>
            {/each}
          </ul>
        </section>
      {/if}
    {/if}
  </div>
</div>

<style>
  .patterns-drawer { overflow-x: hidden; gap: 14px; }
  .patterns-window { display: grid; gap: 10px; margin-top: 13px; min-width: 0; }
  .patterns-window dl { display: grid; gap: 4px; margin: 0; font-size: 13px; }
  .patterns-window dl div { display: flex; gap: 10px; min-width: 0; }
  .patterns-window dt { flex: 0 0 74px; color: var(--text-2); }
  .patterns-window dd { margin: 0; min-width: 0; overflow-wrap: anywhere; }
  .patterns-thresholds summary { width: fit-content; color: var(--text-2); font-size: 13px; }
  .threshold-fields { display: grid; grid-template-columns: repeat(auto-fit, minmax(170px, 1fr)); gap: 10px; margin-top: 8px; }
  .threshold-fields label { display: grid; gap: 4px; color: var(--text-2); font-size: 12px; }
  .threshold-fields input { min-width: 0; min-height: 36px; padding: 6px 10px; color: var(--text); background: var(--bg); border: 1px solid var(--control-line); border-radius: var(--radius); font-size: 14px; }
  .patterns-run { justify-self: start; }
  .patterns-note { margin: 0; color: var(--text-2); font-size: 12px; line-height: 1.5; }
  .patterns-summary { display: grid; gap: 10px; min-width: 0; }
  .patterns-verdict { margin: 0; font-size: 16px; font-weight: 600; }
  .finding-list { display: grid; gap: 8px; margin: 0; padding: 0; list-style: none; }
  .finding { padding: 9px 12px; background: var(--surface-2); border: 1px solid var(--line-soft); border-radius: var(--radius); }
  .finding p { margin: 6px 0 0; font-size: 13px; overflow-wrap: anywhere; }
  .finding-meta { display: flex; flex-wrap: wrap; align-items: center; gap: 6px 10px; font-size: 12px; }
  .evidence-badge { padding: 2px 8px; color: var(--text); border: 1px solid var(--line); border-radius: 4px; font-weight: 650; }
  .evidence-badge.strong { border-color: var(--accent); }
  .finding-n { color: var(--text-2); font-variant-numeric: tabular-nums; }
  .low-n { padding: 1px 6px; color: var(--warn); border: 1px solid var(--warn); border-radius: 4px; }
  .patterns-quality { margin: 0; color: var(--text-2); font-size: 12px; line-height: 1.5; }
  .patterns-detail { margin-top: 16px; min-width: 0; }
  .patterns-detail h3 { margin: 0 0 8px; font-size: 14px; font-weight: 600; }
  .patterns-detail h4 { margin: 10px 0 6px; color: var(--text-2); font-size: 12px; font-weight: 650; }
  .table-scroll { overflow-x: auto; border: 1px solid var(--line-soft); border-radius: var(--radius); }
  .patterns-table { width: 100%; border-collapse: collapse; font-size: 12px; }
  .patterns-table th, .patterns-table td { padding: 5px 8px; text-align: left; vertical-align: top; border-bottom: 1px solid var(--line-soft); font-weight: 400; }
  .patterns-table thead th { background: var(--surface-2); color: var(--text-2); font-weight: 600; white-space: nowrap; }
  .patterns-table .num { text-align: right; font-variant-numeric: tabular-nums; white-space: nowrap; }
  .patterns-table .ops { overflow-wrap: anywhere; }
  .patterns-table.inner { margin-bottom: 4px; }
  .episode-detail > td { background: var(--surface-2); }
  .episode-toggle { padding: 0; color: var(--text); background: none; border: 0; font: inherit; font-weight: 600; text-align: left; text-decoration: underline; text-underline-offset: 3px; cursor: pointer; }
  .episode-toggle:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; border-radius: var(--radius); }
  .audit-list { display: grid; gap: 8px; margin: 0; padding: 0; list-style: none; }
  .audit-list li { display: grid; gap: 2px; padding: 8px 12px; background: var(--surface-2); border: 1px solid var(--line-soft); border-radius: var(--radius); font-size: 13px; }
  .audit-when { color: var(--text-2); font-size: 12px; }
  .source-tag { color: var(--text-2); font-size: 11px; }
</style>
