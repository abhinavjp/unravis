<script lang="ts">
  import { onMount } from 'svelte';
  import { ApiError, api, readSessionToken } from '../lib/api';
  import { app, scope } from '../lib/appState.svelte';
  import { DRAWER_RESULTS_TEXT, clearDrawerResults, coverageKey, foundAtText, readCoverage, saveCoverage } from '../lib/drawerResults.svelte';
  import { COVERAGE_TERM, COVERAGE_TEXT, coverageRange, coverageRefusalText, coverageRowsForDisplay, tenantLine } from '../lib/provenance';
  import { TENANT_TEXT, tenantPlain } from '../lib/copy/screenCopy';
  import { CENSUS_TEXT } from '../lib/copy/sourcesCopy';
  import type { CoverageReport } from '../lib/types';
  import Hint from './Hint.svelte';
  import ProvenanceNote from './ProvenanceNote.svelte';

  // The coverage census for one source. It reads every record in the period, so it never runs by itself: only the button starts it.
  // A refusal (a live source without --allow-broad-scans) shows the fixed sentence from lib/provenance.ts. A refusal is never kept.
  // The last report for each source and period is kept in memory (lib/drawerResults.svelte.ts). Reopening shows it with its age and a Run again button, and never runs by itself.
  let { sourceId, sourceLabel, start, end, autofocus = false }: { sourceId: string; sourceLabel: string; start: string; end: string; autofocus?: boolean } = $props();
  type State = { kind: 'idle' } | { kind: 'running' } | { kind: 'done'; report: CoverageReport } | { kind: 'refused'; text: string } | { kind: 'error'; text: string };
  let phase = $state<State>({ kind: 'idle' });
  let controller: AbortController | null = null;
  // savedAt is set only while the report on screen came from memory. A fresh run clears it.
  let savedAt = $state<number | null>(null);
  let now = $state(Date.now());
  const range = $derived(coverageRange(start, end));
  const rows = $derived(phase.kind === 'done' ? coverageRowsForDisplay(phase.report) : []);
  const labels = $derived(new Map([[sourceId, sourceLabel]]));
  // The census may run on a source that is not ticked for the search: it only reads. The note says so (D-A-27).
  const unticked = $derived(!scope.query.sourceIds.includes(sourceId));

  async function run() {
    if (!range || phase.kind === 'running') return;
    controller?.abort();
    controller = new AbortController();
    const mine = controller;
    const key = coverageKey(sourceId, range.start, range.end);
    savedAt = null;
    phase = { kind: 'running' };
    try {
      const result = await api.coverage(sourceId, range.start, range.end, mine.signal);
      if (mine.signal.aborted) return;
      if ('refused' in result) { phase = { kind: 'refused', text: coverageRefusalText(result) }; return; }
      saveCoverage({ key, sourceId, report: result });
      phase = { kind: 'done', report: result };
    } catch (cause) {
      if (mine.signal.aborted) return;
      if (cause instanceof ApiError && cause.status === 401) clearDrawerResults();
      phase = { kind: 'error', text: cause instanceof Error && cause.message ? cause.message : COVERAGE_TEXT.failed };
    }
  }
  let runButton = $state<HTMLButtonElement>();
  onMount(() => {
    if (autofocus) runButton?.focus();
    // A forgotten session means nothing from before may be shown.
    if (!readSessionToken()) { clearDrawerResults(); return; }
    if (!range) return;
    const saved = readCoverage(coverageKey(sourceId, range.start, range.end), app.sources.map((source) => source.id));
    if (!saved) return;
    phase = { kind: 'done', report: saved.report };
    savedAt = saved.savedAt;
    now = Date.now();
  });
  $effect(() => () => controller?.abort());
  // Keeps "N min ago" honest while a saved report stays on screen.
  $effect(() => {
    if (savedAt === null) return;
    const timer = setInterval(() => { now = Date.now(); }, 30_000);
    return () => clearInterval(timer);
  });
</script>

<div class="coverage-panel">
  <div class="coverage-head">
    <strong>{sourceLabel}</strong><Hint termId={COVERAGE_TERM} />
  </div>
  <p class="coverage-intro">{COVERAGE_TEXT.intro}</p>
  {#if range?.clamped}<p class="coverage-intro">{COVERAGE_TEXT.clamped}</p>{/if}
  {#if !range}<p class="coverage-intro">{COVERAGE_TEXT.noPeriod}</p>{/if}
  {#if unticked}<p class="coverage-intro" data-unticked>{CENSUS_TEXT.notTicked}</p>{/if}
  <button type="button" class="secondary-button coverage-run" bind:this={runButton} disabled={!range || phase.kind === 'running'} onclick={run}>{phase.kind === 'running' ? COVERAGE_TEXT.running : COVERAGE_TEXT.button}</button>
  <div aria-live="polite">
    {#if phase.kind === 'refused'}<div class="notice coverage-refused" role="status">{phase.text}</div>{/if}
    {#if phase.kind === 'error'}<div class="inline-error" role="alert">{phase.text}</div>{/if}
    {#if phase.kind === 'done'}
      {#if savedAt !== null}
        <div class="notice coverage-saved" role="status">
          <span>{foundAtText(savedAt, now, scope.zone)}. {DRAWER_RESULTS_TEXT.savedNote}</span>
          <button type="button" class="secondary-button" disabled={!range} onclick={run}>{DRAWER_RESULTS_TEXT.runAgain}</button>
        </div>
      {/if}
      <p class="coverage-tenant" title={tenantLine(phase.report)}>{tenantPlain(phase.report.tenantAttribution)}</p>
      <p class="coverage-tenant">{TENANT_TEXT.account}</p>
      {#if phase.report.rows.length === 0 && phase.report.total.docs === 0}
        <p class="coverage-intro">{COVERAGE_TEXT.empty}</p>
      {:else}
        <div class="coverage-scroll">
          <table class="coverage-table" aria-label={COVERAGE_TEXT.tableLabel}>
            <thead><tr>{#each COVERAGE_TEXT.columns as column, index (column)}<th scope="col" class:num={index >= 2}>{column}</th>{/each}</tr></thead>
            <tbody>
              {#each rows as row (row.key)}
                <tr class:total={row.total}><th scope="row">{row.kind}</th><td>{row.day}</td><td class="num">{row.docs}</td>{#each row.fields as field, index (index)}<td class="num">{field}</td>{/each}</tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
      <ProvenanceNote provenance={phase.report.provenance} sourceLabels={labels} />
    {/if}
  </div>
</div>

<style>
  .coverage-panel { display: grid; gap: 10px; margin: 14px 0 0; min-width: 0; }
  .coverage-head { display: flex; align-items: center; gap: 4px; font-size: 14px; overflow-wrap: anywhere; }
  .coverage-intro, .coverage-tenant { margin: 0; color: var(--text-2); font-size: 13px; line-height: 1.5; }
  .coverage-tenant { color: var(--text); }
  .coverage-run { justify-self: start; }
  .coverage-saved { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 8px; }
  .coverage-saved button { margin-left: 0; }
  .coverage-scroll { overflow-x: auto; border: 0; border-top: 1px solid var(--line-soft); border-bottom: 1px solid var(--line-soft); }
  .coverage-table { width: 100%; border-collapse: collapse; font-size: 12px; white-space: nowrap; }
  .coverage-table th, .coverage-table td { padding: 4px 8px; text-align: left; border-bottom: 1px solid var(--line-soft); font-weight: 400; }
  .coverage-table thead th { background: var(--surface-2); color: var(--text-2); font-weight: 500; }
  .coverage-table tbody th { font-weight: 600; }
  .coverage-table .num { text-align: right; font-variant-numeric: tabular-nums; }
  .coverage-table tr.total td, .coverage-table tr.total th { font-weight: 600; background: var(--surface-2); }
</style>
