<script lang="ts">
  import { api } from '../lib/api';
  import { COVERAGE_TERM, COVERAGE_TEXT, coverageRange, coverageRefusalText, coverageRowsForDisplay, tenantLine } from '../lib/provenance';
  import { TENANT_TEXT, tenantPlain } from '../lib/copy/screenCopy';
  import type { CoverageReport } from '../lib/types';
  import Hint from './Hint.svelte';
  import ProvenanceNote from './ProvenanceNote.svelte';

  // The coverage census for one source. It reads every record in the period, so it never runs by itself: only the button starts it.
  // A refusal (a live source without --allow-broad-scans) shows the fixed sentence from lib/provenance.ts.
  let { sourceId, sourceLabel, start, end }: { sourceId: string; sourceLabel: string; start: string; end: string } = $props();
  type State = { kind: 'idle' } | { kind: 'running' } | { kind: 'done'; report: CoverageReport } | { kind: 'refused'; text: string } | { kind: 'error'; text: string };
  let state = $state<State>({ kind: 'idle' });
  let controller: AbortController | null = null;
  const range = $derived(coverageRange(start, end));
  const rows = $derived(state.kind === 'done' ? coverageRowsForDisplay(state.report) : []);
  const labels = $derived(new Map([[sourceId, sourceLabel]]));

  async function run() {
    if (!range || state.kind === 'running') return;
    controller?.abort();
    controller = new AbortController();
    const mine = controller;
    state = { kind: 'running' };
    try {
      const result = await api.coverage(sourceId, range.start, range.end, mine.signal);
      if (mine.signal.aborted) return;
      state = 'refused' in result ? { kind: 'refused', text: coverageRefusalText(result) } : { kind: 'done', report: result };
    } catch (cause) {
      if (mine.signal.aborted) return;
      state = { kind: 'error', text: cause instanceof Error && cause.message ? cause.message : COVERAGE_TEXT.failed };
    }
  }
  $effect(() => () => controller?.abort());
</script>

<div class="coverage-panel">
  <div class="coverage-head">
    <strong>{COVERAGE_TEXT.heading}</strong><Hint termId={COVERAGE_TERM} />
  </div>
  <p class="coverage-intro">{COVERAGE_TEXT.intro}</p>
  {#if range?.clamped}<p class="coverage-intro">{COVERAGE_TEXT.clamped}</p>{/if}
  {#if !range}<p class="coverage-intro">{COVERAGE_TEXT.noPeriod}</p>{/if}
  <button type="button" class="secondary-button coverage-run" disabled={!range || state.kind === 'running'} onclick={run}>{state.kind === 'running' ? COVERAGE_TEXT.running : COVERAGE_TEXT.button}</button>
  <div aria-live="polite">
    {#if state.kind === 'refused'}<div class="notice coverage-refused" role="status">{state.text}</div>{/if}
    {#if state.kind === 'error'}<div class="inline-error" role="alert">{state.text}</div>{/if}
    {#if state.kind === 'done'}
      <p class="coverage-tenant" title={tenantLine(state.report)}>{tenantPlain(state.report.tenantAttribution)}</p>
      <p class="coverage-tenant">{TENANT_TEXT.account}</p>
      {#if state.report.rows.length === 0 && state.report.total.docs === 0}
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
      <ProvenanceNote provenance={state.report.provenance} sourceLabels={labels} />
    {/if}
  </div>
</div>

<style>
  .coverage-panel { display: grid; gap: 6px; margin: 4px 2px 8px; padding: 8px 10px; background: var(--surface); border: 1px solid var(--line-soft); border-radius: var(--radius); min-width: 0; }
  .coverage-head { display: flex; align-items: center; gap: 4px; font-size: 13px; }
  .coverage-intro, .coverage-tenant { margin: 0; color: var(--text-2); font-size: 12px; line-height: 1.5; }
  .coverage-tenant { color: var(--text); }
  .coverage-run { justify-self: start; }
  .coverage-scroll { overflow-x: auto; max-height: 240px; overflow-y: auto; border: 1px solid var(--line-soft); border-radius: var(--radius); }
  .coverage-table { width: 100%; border-collapse: collapse; font-size: 11px; white-space: nowrap; }
  .coverage-table th, .coverage-table td { padding: 4px 8px; text-align: left; border-bottom: 1px solid var(--line-soft); font-weight: 400; }
  .coverage-table thead th { position: sticky; top: 0; background: var(--surface-2); color: var(--text-2); font-weight: 600; }
  .coverage-table tbody th { font-weight: 600; }
  .coverage-table .num { text-align: right; font-variant-numeric: tabular-nums; }
  .coverage-table tr.total td, .coverage-table tr.total th { font-weight: 700; background: var(--surface-2); }
</style>
