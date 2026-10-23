<script lang="ts">
  import type { RequestRecord } from '../lib/types';
  let { rows, sourceLabels, loading, pageNumber, nextCursor, truncated, payloadId, onFirst, onNext, onPayload, onContext }: {
    rows: RequestRecord[]; loading: boolean; nextCursor: string; truncated: boolean;
    sourceLabels: Map<string, string>;
    pageNumber: number; payloadId: string; onFirst: () => void; onNext: () => void; onPayload: (row: RequestRecord) => void
    onContext: (row: RequestRecord) => void
  } = $props();
  const time = (value: string | null) => value ? new Date(value).toLocaleString() : 'N/A';
  const number = (value: number | null) => value == null || !Number.isFinite(value) ? 'N/A' : value.toFixed(1);
</script>

<div class="table-scroll request-scroll">
  <table class="request-table">
    <thead><tr><th>Initiated</th><th>Source</th><th>Controller / action</th><th>Status</th><th title="Full elapsed request duration">Total ms</th><th title="Time spent in API processing">API ms</th><th title="Captured request and response text">Investigate</th></tr></thead>
    <tbody>
      {#each rows as row (row.sourceId + row.id)}
        <tr>
          <td>{time(row.initiatedAt)}</td><td>{sourceLabels.get(row.sourceId) ?? row.sourceId}</td><td><strong>{row.controller}.{row.action}</strong><small>{row.url}</small></td>
          <td>{row.status || 'N/A'}<small>{row.outcome}</small></td><td>{number(row.durationMs)}</td><td>{number(row.apiTimeMs)}</td>
          <td><button class="secondary-button" disabled={!row.initiatedAt} onclick={() => onContext(row)}>Nearby activity</button> <button class="quiet-button payload-button" aria-label={'View payload for request ' + row.id} disabled={payloadId === row.id} onclick={() => onPayload(row)}>{payloadId === row.id ? 'Loading…' : 'Inspect payload'}</button></td>
        </tr>
      {:else}<tr><td colspan="7" class="table-empty">{loading ? 'Loading request records…' : 'Select an API group to inspect its requests.'}</td></tr>{/each}
    </tbody>
  </table>
</div>
<div class="pagination-row">
  <span>Page {pageNumber} · {rows.length} request{rows.length === 1 ? '' : 's'} shown · at most 100 per page{truncated ? ' · source page truncated' : ''}</span>
  <div class="page-actions">
    {#if pageNumber > 1}<button class="quiet-button" disabled={loading} onclick={onFirst}>First page</button>{/if}
    {#if nextCursor}<button class="secondary-button" disabled={loading} onclick={onNext}>{loading ? 'Loading…' : 'Next page'}</button>{/if}
  </div>
</div>

