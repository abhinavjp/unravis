<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { api } from '../lib/api';
  import { breakdownDims, breakdownNotes, breakdownPickTitle, breakdownRowFilter, completeTitle, percentileText, pooledKindsNote, withoutDimension } from '../lib/breakdown';
  import type { Breakdown, BreakdownDim, BreakdownFilter, BreakdownRow, Query } from '../lib/types';
  // lockedDim fixes the dimension and hides the tabs (the failure panel's status table); panelId keeps a second panel's id unique.
  let { query, sourceLabels, onDrill, lockedDim, panelId = 'breakdown', heading = 'Break these requests down' }: {
    query: Query; sourceLabels: Map<string, string>; onDrill: (filter: BreakdownFilter, row: BreakdownRow, dim: BreakdownDim) => void;
    lockedDim?: BreakdownDim; panelId?: string; heading?: string;
  } = $props();
  let picked = $state<BreakdownDim>('userId');
  const dim = $derived<BreakdownDim>(lockedDim ?? picked);
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
  const pooled = $derived(result?.pooledKinds === true);
  const notes = $derived(breakdownNotes(dim, pooled));
  const timings = (row: BreakdownRow) => pooled ? 'Percentiles are not shown for pooled request types' : row.stats.count + ' valid timings';
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

<section class="data-section breakdown-panel" id={panelId} aria-label="Request breakdown">
  <div class="section-heading"><div><h3>{heading}</h3><p>See which {label.toLowerCase()} values the requests in the current period share. Choose a row to list matching requests. Counts follow the request type and result filters above; the current period only.</p></div></div>
  {#if !lockedDim}<div class="breakdown-tabs" role="tablist" aria-label="Breakdown by">
    {#each breakdownDims as item (item.dim)}
      <button role="tab" aria-selected={dim === item.dim} class:active={dim === item.dim} onclick={() => picked = item.dim}>{item.label}</button>
    {/each}
  </div>{/if}
  {#if dim === 'userIp' && !ipOpen}
    <div class="breakdown-collapsed"><p>User IP addresses are personal data and are hidden until you ask for them.</p><button class="secondary-button" onclick={() => ipOpen = true}>Show user IP breakdown</button></div>
  {:else}
    {#if error}<div class="inline-error" role="alert">{error}</div>{/if}
    {#if failing.length}<div class="source-failure compact-failure" role="alert">{#each failing as failed}<span>{sourceLabels.get(failed.sourceId) ?? failed.sourceId}: {failed.error || 'Source unavailable'}</span>{/each}<span>Counts exclude sources that failed.</span></div>{/if}
    <ul class="breakdown-notes" aria-label="About these numbers">{#each notes as note (note)}<li class:breakdown-note-pooled={note === pooledKindsNote}>{note}</li>{/each}</ul>
    <div class="table-scroll breakdown-scroll" aria-busy={loading}>
      <table class="breakdown-table">
        <thead><tr><th>{label}</th><th title="Requests in this group">Requests</th><th title="Median total time, from valid completed requests only">P50</th><th title="About 95 in 100 of these requests finished within this time">P95</th><th title="Responses with a 4xx status">4xx</th><th title="Responses with a 5xx status, and failed jobs">5xx</th><th title={completeTitle}>Complete</th><th title="Requests with no complete duration recorded">Missing timing</th><th title="Asynchronous requests that only answered 202 Accepted">Pending</th></tr></thead>
        <tbody>
          {#if result}
            {#each result.rows as row (row.value + '|' + row.missing)}
              {@const filter = breakdownRowFilter(row)}
              <tr>
                <td title={filter ? undefined : breakdownPickTitle(dim, row)}><button class="breakdown-pick" class:breakdown-missing={row.missing} disabled={!filter} title={breakdownPickTitle(dim, row)} onclick={() => filter && onDrill(filter, row, dim)}>{row.value}</button></td>
                <td class="numeric">{whole(row.count)}</td><td title={timings(row)}>{percentileText(row.stats.p50, pooled)}</td><td title={timings(row)}>{percentileText(row.stats.p95, pooled)}</td>
                <td>{whole(row.count4xx)}</td><td>{whole(row.count5xx)}</td><td>{whole(row.complete)}</td><td>{whole(row.incomplete)}</td><td>{whole(row.pending)}</td>
              </tr>
            {:else}<tr><td colspan="9" class="table-empty">{loading ? 'Reading the breakdown…' : 'No requests match the current filters.'}</td></tr>{/each}
            {#if result.other}
              <tr class="breakdown-other">
                <td title={breakdownPickTitle(dim, null)}>Other{result.other.values ? ` (${whole(result.other.values)} values)` : ''}<small>Not selectable: narrow the period or filters to see these.</small></td>
                <td class="numeric">{whole(result.other.count)}</td><td>{percentileText(result.other.stats.p50, pooled)}</td><td>{percentileText(result.other.stats.p95, pooled)}</td>
                <td>{whole(result.other.count4xx)}</td><td>{whole(result.other.count5xx)}</td><td>{whole(result.other.complete)}</td><td>{whole(result.other.incomplete)}</td><td>{whole(result.other.pending)}</td>
              </tr>
            {/if}
          {:else}<tr><td colspan="9" class="table-empty">{loading ? 'Reading the breakdown…' : 'The breakdown is not available.'}</td></tr>{/if}
        </tbody>
      </table>
    </div>
    <div class="pagination-row"><span>{#if result}{whole(result.total)} request{result.total === 1 ? '' : 's'} · {whole(result.distinct)} distinct value{result.distinct === 1 ? '' : 's'}{loading ? ' · updating…' : ''}{:else}{loading ? 'Reading…' : ''}{/if}</span><span>“(not yet written)” means the audit record has no value for this field yet.</span></div>
  {/if}
</section>
