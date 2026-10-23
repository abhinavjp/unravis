<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { api } from '../lib/api';
  import { breakdownDims, breakdownRowFilter, withoutDimension } from '../lib/breakdown';
  import type { Breakdown, BreakdownDim, BreakdownRow, Quantile, Query, RecordFilter } from '../lib/types';
  let { query, sourceLabels, onDrill }: {
    query: Query; sourceLabels: Map<string, string>; onDrill: (filter: RecordFilter, row: BreakdownRow, dim: BreakdownDim) => void;
  } = $props();
  let dim = $state<BreakdownDim>('statusCode');
  let ipOpen = $state(false);
  let result = $state<Breakdown | null>(null);
  let loading = $state(false);
  let error = $state('');
  let controller: AbortController | undefined;
  let sequence = 0;
  const visible = $derived(dim !== 'userIp' || ipOpen);
  const requestKey = $derived(JSON.stringify([withoutDimension(query, dim), dim, visible]));
  const label = $derived(breakdownDims.find(item => item.dim === dim)?.label ?? '');
  const failing = $derived((result?.sources ?? []).filter(source => !source.ok));
  const ms = (q: Quantile | null | undefined) => q?.valueMs == null ? 'N/A' : '≈ ' + q.valueMs.toFixed(1) + ' ms';
  const whole = (value: number) => value.toLocaleString();

  async function load(key: string) {
    controller?.abort();
    controller = new AbortController();
    const run = ++sequence;
    loading = true; error = '';
    try {
      const body = JSON.parse(key) as [Query, BreakdownDim];
      const out = await api.breakdown(body[0], body[1], 20, controller.signal);
      if (run !== sequence) return;
      result = out;
    } catch (cause) {
      if (run === sequence && !(cause instanceof DOMException && cause.name === 'AbortError')) {
        error = cause instanceof Error ? cause.message : 'The breakdown could not be read.';
        result = null;
      }
    } finally { if (run === sequence) loading = false; }
  }
  $effect(() => {
    const key = requestKey;
    const show = visible;
    untrack(() => {
      if (!show) { sequence++; controller?.abort(); loading = false; return; }
      result = null;
      void load(key);
    });
  });
  onMount(() => () => { sequence++; controller?.abort(); });
</script>

<section class="data-section breakdown-panel" id="breakdown" aria-label="Request breakdown">
  <div class="section-heading"><div><h3>Break these requests down</h3><p>See which {label.toLowerCase()} values the requests in the current period share. Choose a row to list exactly those requests. Counts follow the request type and result filters above; the current period only.</p></div></div>
  <div class="breakdown-tabs" role="tablist" aria-label="Breakdown by">
    {#each breakdownDims as item (item.dim)}
      <button role="tab" aria-selected={dim === item.dim} class:active={dim === item.dim} onclick={() => dim = item.dim}>{item.label}</button>
    {/each}
  </div>
  {#if dim === 'userIp' && !ipOpen}
    <div class="breakdown-collapsed"><p>User IP addresses are personal data and are hidden until you ask for them.</p><button class="secondary-button" onclick={() => ipOpen = true}>Show user IP breakdown</button></div>
  {:else}
    {#if error}<div class="inline-error" role="alert">{error}</div>{/if}
    {#if failing.length}<div class="source-failure compact-failure" role="alert">{#each failing as failed}<span>{sourceLabels.get(failed.sourceId) ?? failed.sourceId}: {failed.error || 'Source unavailable'}</span>{/each}<span>Counts exclude sources that failed.</span></div>{/if}
    <div class="table-scroll breakdown-scroll" aria-busy={loading}>
      <table class="breakdown-table">
        <thead><tr><th>{label}</th><th title="Requests in this group">Requests</th><th title="Median total time, from valid completed requests only">P50</th><th title="About 95 in 100 of these requests finished within this time">P95</th><th title="Responses with a 4xx status">4xx</th><th title="Responses with a 5xx status, and failed jobs">5xx</th><th title="No complete duration was recorded">Missing timing</th><th title="Asynchronous requests that only answered 202 Accepted">Pending</th></tr></thead>
        <tbody>
          {#if result}
            {#each result.rows as row (row.value + '|' + row.missing)}
              {@const filter = breakdownRowFilter(row)}
              <tr>
                <td><button class="breakdown-pick" class:breakdown-missing={row.missing} disabled={!filter} title="Show these requests" onclick={() => filter && onDrill(filter, row, dim)}>{row.value}</button></td>
                <td class="numeric">{whole(row.count)}</td><td title={row.stats.count + ' valid timings'}>{ms(row.stats.p50)}</td><td title={row.stats.count + ' valid timings'}>{ms(row.stats.p95)}</td>
                <td>{whole(row.count4xx)}</td><td>{whole(row.count5xx)}</td><td>{whole(row.incomplete)}</td><td>{whole(row.pending)}</td>
              </tr>
            {:else}<tr><td colspan="8" class="table-empty">{loading ? 'Reading the breakdown…' : 'No requests match the current filters.'}</td></tr>{/each}
            {#if result.other}
              <tr class="breakdown-other">
                <td>Other{result.other.values ? ` (${whole(result.other.values)} values)` : ''}<small>Not selectable: narrow the period or filters to see these.</small></td>
                <td class="numeric">{whole(result.other.count)}</td><td>{ms(result.other.stats.p50)}</td><td>{ms(result.other.stats.p95)}</td>
                <td>{whole(result.other.count4xx)}</td><td>{whole(result.other.count5xx)}</td><td>{whole(result.other.incomplete)}</td><td>{whole(result.other.pending)}</td>
              </tr>
            {/if}
          {:else}<tr><td colspan="8" class="table-empty">{loading ? 'Reading the breakdown…' : 'The breakdown is not available.'}</td></tr>{/if}
        </tbody>
      </table>
    </div>
    <div class="pagination-row"><span>{#if result}{whole(result.total)} request{result.total === 1 ? '' : 's'} · {whole(result.distinct)} distinct value{result.distinct === 1 ? '' : 's'}{loading ? ' · updating…' : ''}{:else}{loading ? 'Reading…' : ''}{/if}</span><span>“(not yet written)” means the audit record has no value for this field yet.</span></div>
  {/if}
</section>
