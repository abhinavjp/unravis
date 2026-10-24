<script lang="ts">
  import type { Analysis, Rollup } from '../lib/types';
  import { kindLabel } from '../lib/presentation';
  import { lensRead, rankOperations, sparkSeries, type Lens } from '../lib/pulse';

  // D1: operations (controller + action + request type) ranked by the chosen lens, each with a sparkline and the one number that matters.
  let { analysis, lens, compare, openKey, onOpen, limit = 12 }: {
    analysis: Analysis; lens: Lens; compare: boolean; openKey: string; onOpen: (row: Rollup) => void; limit?: number;
  } = $props();
  let showAll = $state(false);
  const ranked = $derived(rankOperations(analysis.rollups ?? [], lens));
  const rows = $derived(showAll ? ranked : ranked.slice(0, limit));
  const keyOf = (row: Rollup) => row.controller + '\u0000' + row.action + '\u0000' + (row.kind ?? '');
  const toneOf = (value: string) => value === '—' || value === '0%' || value === '0.0%' ? '' : lens === 'client' ? 'warn' : lens === 'server' ? 'bad' : '';
  const WIDTH = 96, HEIGHT = 28;
  function points(values: Array<number | null>): string {
    const real = values.filter((value): value is number => value != null);
    if (real.length < 2) return '';
    const low = Math.min(...real), high = Math.max(...real), span = high - low || 1;
    return values.map((value, index) => value == null ? '' : (index / (values.length - 1) * WIDTH).toFixed(1) + ',' + (HEIGHT - 3 - (value - low) / span * (HEIGHT - 6)).toFixed(1)).filter(Boolean).join(' ');
  }
</script>

{#if ranked.length}
  <ol class="where-list" aria-label="Operations, worst first">
    {#each rows as row, index (keyOf(row))}
      {@const read = lensRead(row, lens)}
      {@const line = points(sparkSeries(analysis, row, lens))}
      <li>
        <button type="button" class="where-row" aria-current={openKey === keyOf(row)} onclick={() => onOpen(row)} aria-label={row.controller + '.' + row.action + ', ' + kindLabel(row.kind ?? 'unknown') + ', ' + read.value + ' ' + read.label + '. Open evidence'}>
          <span class="where-rank">{index + 1}</span>
          <span class="where-name"><strong>{row.controller}.{row.action}</strong><small>{kindLabel(row.kind ?? 'unknown')}</small></span>
          {#if line}<svg class="spark" viewBox={`0 0 ${WIDTH} ${HEIGHT}`} preserveAspectRatio="none" aria-hidden="true"><polyline points={line}></polyline></svg>{:else}<span class="spark-none" title="No chart series for this operation" aria-hidden="true"></span>{/if}
          <span class="where-value" data-tone={toneOf(read.value)}><strong>{read.value}</strong><small>{read.label}{#if compare && read.delta !== '—'}<span class="where-delta"> · {read.delta} vs earlier</span>{/if}</small></span>
        </button>
      </li>
    {/each}
  </ol>
  {#if ranked.length > limit}
    <div class="where-more"><span>Showing {rows.length.toLocaleString()} of {ranked.length.toLocaleString()} operations</span><button type="button" class="quiet-button" onclick={() => showAll = !showAll}>{showAll ? 'Show fewer' : 'Show all operations'}</button></div>
  {/if}
{:else}
  <div class="empty-state"><strong>No operations in this period</strong><p>Nothing matched the selected sources, request type and filters. Widen the range or clear a filter.</p></div>
{/if}
