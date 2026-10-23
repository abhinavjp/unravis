<script lang="ts">
  import type { Rollup } from '../lib/types';
  import { kindLabel, unknownKindTip } from '../lib/presentation';
  import { changeText, msChangeText, pointsText, sortOperations, type OperationSort } from '../lib/overview';
  let { rollups }: { rollups: Rollup[] } = $props();
  let sort = $state<OperationSort>('events');
  let direction = $state(-1);
  let page = $state(0);
  const pageSize = 10;
  let ordered = $derived(sortOperations(rollups, sort, direction));
  let pageCount = $derived(Math.max(1, Math.ceil(ordered.length / pageSize)));
  let currentPage = $derived(Math.min(page, pageCount - 1));
  let visible = $derived(ordered.slice(currentPage * pageSize, (currentPage + 1) * pageSize));
  const sortBy = (field: OperationSort) => {
    if (sort === field) direction *= -1;
    else { sort = field; direction = -1; }
    page = 0;
  };
  const arrow = (field: OperationSort) => sort === field ? direction < 0 ? ' ↓' : ' ↑' : '';
  const ariaSort = (field: OperationSort) => sort === field ? direction < 0 ? 'descending' : 'ascending' : 'none';
  const referenceNote = 'Changes are hidden (shown as —) unless the reference period has at least 20 valid timings for this API; the P95 change also needs 20 in the current period.';
</script>

<div class="group-toolbar">
  <label class="mobile-group-sort">Sort by<select value={sort + ':' + direction} onchange={(event) => { const [field, order] = event.currentTarget.value.split(':'); sort = field as OperationSort; direction = Number(order); page = 0; }}>
    <option value="events:-1">Most events</option><option value="trafficDelta:-1">Largest traffic increase</option><option value="trafficDelta:1">Largest traffic drop</option>
    <option value="p95DeltaMs:-1">Largest P95 increase</option><option value="clientErrorDelta:-1">Largest 4xx increase</option><option value="serverErrorDelta:-1">Largest 5xx increase</option><option value="completionDelta:1">Largest completion drop</option>
  </select></label>
  <span>One row per API action and request type, with sources merged. {referenceNote}</span>
</div>
<div class="table-scroll group-table-scroll">
  <table class="comparison-table operation-table">
    <thead><tr>
      <th>Operation</th>
      <th>Request type</th>
      <th aria-sort={ariaSort('events')}><button class="sort-button" title="Every stored event in the current period, including incomplete and pending ones." onclick={() => sortBy('events')}>Events{arrow('events')}</button></th>
      <th aria-sort={ariaSort('trafficDelta')}><button class="sort-button" title="Change in events per minute against the reference period. +25% means a quarter more traffic." onclick={() => sortBy('trafficDelta')}>Δ traffic{arrow('trafficDelta')}</button></th>
      <th aria-sort={ariaSort('p95DeltaMs')}><button class="sort-button" title="Change in the estimated P95 response time against the reference period, in milliseconds." onclick={() => sortBy('p95DeltaMs')}>Δ P95{arrow('p95DeltaMs')}</button></th>
      <th aria-sort={ariaSort('clientErrorDelta')}><button class="sort-button" title="Change in the 4xx share of events with a known result, in percentage points." onclick={() => sortBy('clientErrorDelta')}>Δ 4xx{arrow('clientErrorDelta')}</button></th>
      <th aria-sort={ariaSort('serverErrorDelta')}><button class="sort-button" title="Change in the 5xx share (including failed Hangfire jobs) of events with a known result, in percentage points." onclick={() => sortBy('serverErrorDelta')}>Δ 5xx{arrow('serverErrorDelta')}</button></th>
      <th aria-sort={ariaSort('completionDelta')}><button class="sort-button" title="Change in the share of events that completed with a valid timing, in percentage points." onclick={() => sortBy('completionDelta')}>Δ completion{arrow('completionDelta')}</button></th>
    </tr></thead>
    <tbody>
      {#each visible as row (row.controller + '|' + row.action + '|' + (row.kind ?? ''))}
        <tr>
          <td data-label="Operation">{row.controller}.{row.action}</td>
          <td data-label="Request type" title={row.kind === 'unknown' ? unknownKindTip : undefined}>{kindLabel(row.kind) || '—'}</td>
          <td class="numeric" data-label="Events" title={'Reference period: ' + (row.referenceEvents ?? 0).toLocaleString() + ' events.'}>{(row.events ?? 0).toLocaleString()}</td>
          <td class="numeric" data-label="Δ traffic" title={referenceNote}>{changeText(row.trafficDelta)}</td>
          <td class="numeric" data-label="Δ P95" title={referenceNote}>{msChangeText(row.p95DeltaMs)}</td>
          <td class="numeric" data-label="Δ 4xx" title={referenceNote}>{pointsText(row.clientErrorDelta)}</td>
          <td class="numeric" data-label="Δ 5xx" title={referenceNote}>{pointsText(row.serverErrorDelta)}</td>
          <td class="numeric" data-label="Δ completion" title={referenceNote}>{pointsText(row.completionDelta)}</td>
        </tr>
      {:else}<tr><td colspan="8" class="table-empty">No operations are available for this query.</td></tr>{/each}
    </tbody>
  </table>
</div>
<div class="pagination-row group-pagination">
  <span aria-live="polite">{ordered.length ? currentPage * pageSize + 1 : 0}–{Math.min((currentPage + 1) * pageSize, ordered.length)} of {ordered.length} operations · Page {currentPage + 1} of {pageCount}</span>
  <div class="page-actions"><button class="secondary-button" disabled={currentPage === 0} onclick={() => page = currentPage - 1}>Previous operations</button><button class="secondary-button" disabled={currentPage + 1 >= pageCount} onclick={() => page = currentPage + 1}>Next operations</button></div>
</div>
