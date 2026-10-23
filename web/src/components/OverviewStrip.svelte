<script lang="ts">
  import type { Snippet } from 'svelte';
  import type { OverviewRow, Quantile } from '../lib/types';
  import { kindLabel, unknownKindTip } from '../lib/presentation';
  import { changeText, msChangeText, overviewDrill, overviewStateNote, pointsText, rateText, type OverviewDrill, type OverviewTile } from '../lib/overview';
  let { overview, outcomeFilter = 'all', onDrill, quality }: {
    overview: OverviewRow[]; outcomeFilter?: string; onDrill: (drill: OverviewDrill) => void; quality: Snippet;
  } = $props();
  const latency = (q: Quantile | null | undefined) => q?.valueMs == null ? 'N/A' : '≈ ' + q.valueMs.toFixed(1) + ' ms';
  const sharesNote = $derived(outcomeFilter === 'all' ? '' : 'Error shares need the outcome filter set to All.');
  const drill = (row: OverviewRow, tile: OverviewTile) => onDrill(overviewDrill(row.kind, tile));
  const percentiles = (row: OverviewRow) => [
    { id: 'P50', quantile: row.p50, delta: row.delta.p50Ms }, { id: 'P90', quantile: row.p90, delta: row.delta.p90Ms },
    { id: 'P95', quantile: row.p95, delta: row.delta.p95Ms }, { id: 'P99', quantile: row.p99, delta: row.delta.p99Ms }
  ];
</script>

{#snippet countTile(row: OverviewRow, tile: OverviewTile, label: string, count: number, detail: string, tip: string)}
  <button type="button" class="overview-tile overview-drill" disabled={count === 0} title={count === 0 ? 'Nothing to list for this count.' : tip + ' Opens the matching requests.'} onclick={() => drill(row, tile)}>
    <strong>{count.toLocaleString()}</strong><span>{label}</span><small>{detail}</small>
  </button>
{/snippet}

<section class="overview-strip" aria-label="Overview by request type">
  {#each overview as row (row.kind)}
    <div class="overview-kind" role="group" aria-label={kindLabel(row.kind) + ' overview'}>
      <header>
        <h4 title={row.kind === 'unknown' ? unknownKindTip : undefined}>{kindLabel(row.kind)}</h4>
        <span class="state-pill" title={overviewStateNote(row)}>{row.state === 'compared' ? 'Compared' : row.state.replaceAll('_', ' ')}</span>
        <span class="overview-rate" title="Events per minute over the current window, counting every stored event.">{row.perMinute.toLocaleString(undefined, { maximumFractionDigits: 1 })} / min</span>
      </header>
      <div class="overview-tiles">
        {@render countTile(row, 'events', 'Events', row.events, 'vs reference ' + changeText(row.delta.traffic), 'Every stored event of this request type in the current window, including incomplete and pending ones.')}
        {@render countTile(row, 'completed', 'Completed', row.completed, 'Completion ' + rateText(row.completionRate) + ' · ' + pointsText(row.delta.completionRate), 'Events with a valid, complete timing. Incomplete and pending events are never counted as fast or successful.')}
        {@render countTile(row, 'clientErrors', '4xx', row.clientErrors, rateText(row.clientErrorRate) + ' · ' + pointsText(row.delta.clientErrorRate), 'Client errors. The share is of events with a known result. ' + sharesNote)}
        {@render countTile(row, 'serverErrors', '5xx', row.serverErrors, rateText(row.serverErrorRate) + ' · ' + pointsText(row.delta.serverErrorRate), 'Server errors, including failed Hangfire jobs. The share is of events with a known result. ' + sharesNote)}
        {#each percentiles(row) as item (item.id)}
          <div class="overview-tile" title="Histogram estimate over valid timings of this request type only. Needs at least 20 samples{item.id === 'P99' ? ' (100 for P99)' : ''}; the change needs 20 in the reference too.">
            <strong>{latency(item.quantile)}</strong><span>{item.id}</span><small>{msChangeText(item.delta)}</small>
          </div>
        {/each}
      </div>
    </div>
  {/each}
  {@render quality()}
</section>
