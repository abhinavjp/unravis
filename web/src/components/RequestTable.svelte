<script lang="ts">
  import type { RequestRecord } from '../lib/types';
  import { stateLabel } from '../lib/presentation';
  let { rows, sourceLabels, loading, pageNumber, nextCursor, truncated, recordId, onFirst, onNext, onInspect, onContext }: {
    rows: RequestRecord[]; loading: boolean; nextCursor: string; truncated: boolean;
    sourceLabels: Map<string, string>;
    pageNumber: number; recordId: string; onFirst: () => void; onNext: () => void; onInspect: (row: RequestRecord) => void
    onContext: (row: RequestRecord) => void
  } = $props();
  const time = (value: string | null) => value ? new Date(value).toLocaleString() : 'N/A';
  const number = (value: number | null) => value == null || !Number.isFinite(value) ? 'N/A' : value.toFixed(1);
</script>

<div class="table-scroll request-scroll">
  <table class="request-table">
    <thead><tr><th>Initiated</th><th>Source</th><th>Controller / action</th><th>Status</th><th title="The record's _id in ApiAuditLog; Inspect opens it">Record id</th><th title="Full elapsed request duration">Total ms</th><th title="Time spent in API processing">API ms</th><th title="Time spent writing the audit row">Audit create ms</th><th title="UserId stored on the audit record">User</th><th title="PayrollUIVersion stored on the audit record">UI version</th><th title="All stored fields, derived values and captured payloads">Investigate</th></tr></thead>
    <tbody>
      {#each rows as row (row.sourceId + row.id)}
        <tr>
          <td>{time(row.initiatedAt)}<small class="drawer-only">{sourceLabels.get(row.sourceId) ?? row.sourceId}</small></td><td>{sourceLabels.get(row.sourceId) ?? row.sourceId}</td><td><strong>{row.controller}.{row.action}</strong><small>{row.url}</small></td>
          <td>{row.status || 'N/A'}<small>{row.outcome}{#if row.state} · <span class={'state-badge state-' + row.state}>{stateLabel(row.state)}</span>{/if}</small></td><td><code class="record-id" title={row.id}>{row.id}</code></td><td>{number(row.durationMs)}</td><td>{number(row.apiTimeMs)}</td>
          <td>{number(row.auditCreateTimeMs)}</td><td>{row.userId ?? 'N/A'}</td><td>{row.uiVersion || 'N/A'}</td>
          <td><button class="secondary-button" disabled={!row.initiatedAt} onclick={() => onContext(row)}>Nearby activity</button> <button class="quiet-button payload-button" aria-label={'Inspect record ' + row.id} disabled={recordId === row.id} onclick={() => onInspect(row)}>{recordId === row.id ? 'Loading…' : 'Inspect'}</button></td>
        </tr>
      {:else}<tr><td colspan="11" class="table-empty">{loading ? 'Loading request records…' : 'Select an API group or a quality count to inspect its requests.'}</td></tr>{/each}
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

