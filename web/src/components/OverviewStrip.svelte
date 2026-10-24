<script lang="ts">
  import type { Snippet } from 'svelte';
  import type { OverviewRow, Quantile } from '../lib/types';
  import { kindLabel, unknownKindTip } from '../lib/presentation';
  import { changeText, msChangeText, overviewDrill, overviewStateNote, pointsText, rateText, tileCanOpen, type DrillContext, type OverviewDrill, type OverviewTile } from '../lib/overview';
  let { overview, outcomeFilter = 'all', kindFilter = 'all', onDrill, quality }: {
    overview: OverviewRow[]; outcomeFilter?: string; kindFilter?: string; onDrill: (drill: OverviewDrill) => void; quality: Snippet;
  } = $props();
  const latency = (q: Quantile | null | undefined) => q?.valueMs == null ? 'N/A' : '≈ ' + q.valueMs.toFixed(1) + ' ms';
  const sharesNote = $derived(outcomeFilter === 'all' ? '' : 'Error shares need the outcome filter set to All.');
  // Incomplete and invalid rows of unknown request type: the Synchronous and Asynchronous lists also hold them, the tiles do not.
  const unknownKindRows = $derived.by(() => { const row = overview.find(item => item.kind === 'unknown'); return row ? row.incomplete + row.invalid : 0; });
  const contextFor = (row: OverviewRow): DrillContext => ({ unknownKindRows, unknownResults: row.unknownOutcome, outcomeFilter, queryKind: kindFilter });
  const drillFor = (row: OverviewRow, tile: OverviewTile) => overviewDrill(row.kind, tile, contextFor(row));
  // A count of 0 still opens when the list can hold rows the count leaves out (see tileCanOpen).
  const canOpen = (row: OverviewRow, tile: OverviewTile, count: number) => tileCanOpen(row.kind, tile, count, contextFor(row));
  const drill = (row: OverviewRow, tile: OverviewTile) => onDrill(drillFor(row, tile));
  const drillTip = (row: OverviewRow, tile: OverviewTile, tip: string) => {
    const note = drillFor(row, tile).note;
    return tip + ' Opens the matching requests.' + (note ? ' The list ' + note + '.' : '');
  };
  const percentiles = (row: OverviewRow) => [
    { id: 'P50', quantile: row.p50, delta: row.delta.p50Ms }, { id: 'P90', quantile: row.p90, delta: row.delta.p90Ms },
    { id: 'P95', quantile: row.p95, delta: row.delta.p95Ms }, { id: 'P99', quantile: row.p99, delta: row.delta.p99Ms }
  ];
</script>

{#snippet countTile(row: OverviewRow, tile: OverviewTile, label: string, count: number, detail: string, tip: string)}
  <button type="button" class="overview-tile overview-drill" disabled={!canOpen(row, tile, count)} title={canOpen(row, tile, count) ? drillTip(row, tile, tip) : 'Nothing to list for this count.'} onclick={() => drill(row, tile)}>
    <strong>{count.toLocaleString()}</strong><span>{label}</span><small>{detail}</small>
  </button>
{/snippet}

<section class="overview-strip" aria-label="Overview by request type">
  {#each overview as row (row.kind)}
    <div class="overview-kind" role="group" aria-label={kindLabel(row.kind) + ' overview'}>
      <header>
        <h4 title={row.kind === 'unknown' ? unknownKindTip : undefined}>{kindLabel(row.kind)}</h4>
        <span class="state-pill" title={overviewStateNote(row)}>{row.state === 'compared' ? 'Compared' : row.state.replaceAll('_', ' ')}</span>
        <span class="overview-rate" title="Events per minute over the current window, counting every event of this request type that matches the filters.">{row.perMinute.toLocaleString(undefined, { maximumFractionDigits: 1 })} / min</span>
      </header>
      <div class="overview-tiles">
        {@render countTile(row, 'events', 'Events', row.events, 'vs reference ' + changeText(row.delta.traffic), 'Events of this request type in the current window that match the filters, including incomplete and pending ones. Incomplete rows whose request type is not known yet are counted under HTTP (polling type unknown), not here.')}
        {@render countTile(row, 'completed', 'Completed', row.completed, 'Completed ÷ all events ' + rateText(row.completionRate) + ' · ' + pointsText(row.delta.completionRate), 'Events with a valid, complete timing, shown as a share of all events including unusable ones. The Completion % column elsewhere divides by completed, incomplete and pending instead. Incomplete and pending events are never counted as fast or successful.')}
        {@render countTile(row, 'clientErrors', '4xx', row.clientErrors, rateText(row.clientErrorRate) + ' · ' + pointsText(row.delta.clientErrorRate), 'Client errors. The share is of events with a known result. ' + sharesNote)}
        {@render countTile(row, 'serverErrors', '5xx', row.serverErrors, rateText(row.serverErrorRate) + ' · ' + pointsText(row.delta.serverErrorRate), 'Server errors, including failed Hangfire jobs. The share is of events with a known result. ' + sharesNote)}
        {#each percentiles(row) as item (item.id)}
          <div class="overview-tile" title="Histogram estimate over valid timings of this request type only. With few valid timings it is only a rough figure{item.id === 'P99' ? ' (P99 appears from 100 timings)' : ''}; the change against the reference needs at least 20 valid timings in both periods{item.id === 'P99' ? ' (100 for P99)' : ''}.">
            <strong>{latency(item.quantile)}</strong><span>{item.id}</span><small>{msChangeText(item.delta)}</small>
          </div>
        {/each}
      </div>
    </div>
  {/each}
  {@render quality()}
</section>
