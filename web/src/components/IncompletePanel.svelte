<script lang="ts">
  import { formatCount } from '../lib/format';
  import type { Rollup } from '../lib/types';
  import { rateText, type OverviewDrill } from '../lib/overview';
  import { kindLabel } from '../lib/presentation';
  import Hint from './Hint.svelte';
  import { DEFAULT_STALE_MINUTES, MAX_STALE_MINUTES, parseStaleMinutes, rankByCompletion, staleButtonText, staleDrill } from '../lib/incomplete';
  let { rollups, kindFilter = 'all', onDrill }: { rollups: Rollup[]; kindFilter?: string; onDrill: (drill: OverviewDrill) => void } = $props();
  // The threshold is text while it is being typed; an unusable value disables the stale lists instead of guessing one.
  let minutesText = $state(String(DEFAULT_STALE_MINUTES));
  let minutes = $derived(parseStaleMinutes(minutesText));
  let ranked = $derived(rankByCompletion(rollups));
  const shown = 8;
  let worst = $derived(ranked.filter(item => item.unfinished > 0).slice(0, shown));
  let totals = $derived(rollups.reduce((sum, row) => ({ incomplete: sum.incomplete + (row.incomplete ?? 0), pending: sum.pending + (row.pending ?? 0), invalid: sum.invalid + (row.invalid ?? 0) }), { incomplete: 0, pending: 0, invalid: 0 }));
  const open = (operation: Rollup | null, completion: 'incomplete' | 'pending') => { if (minutes != null) onDrill(staleDrill(operation, completion, minutes, kindFilter)); };
  let incompleteButton = $derived(staleButtonText('incomplete', totals, minutes));
  let pendingButton = $derived(staleButtonText('pending', totals, minutes));
  const rowIncompleteTip = 'Opens the unfinished requests of this operation, oldest first. The list includes pending asynchronous ones and only holds requests open at least the stale threshold; this count is every age and leaves pending out.';
  const rowPendingTip = 'Opens the pending asynchronous requests of this operation, oldest first, only those open at least the stale threshold; this count is every age.';
</script>

{#if rollups.length}
<section class="data-section incomplete-panel" id="incomplete-work" aria-label="Incomplete requests">
  <div class="section-heading"><div><h3>Incomplete requests<Hint termId="incomplete-request" /></h3><p>Completion % is completed / (completed + incomplete + pending). Unusable rows are counted apart and never in that share. A dash means there was nothing to measure, not 0%.</p></div></div>
  <div class="group-toolbar">
    <label>Stale after (minutes)<input type="text" inputmode="decimal" bind:value={minutesText} aria-invalid={minutes == null} aria-describedby="stale-threshold-note" /></label>
    <button type="button" class="secondary-button" disabled={minutes == null || !(totals.incomplete + totals.pending)} title={incompleteButton.tip} onclick={() => open(null, 'incomplete')}>{incompleteButton.text}</button>
    <button type="button" class="secondary-button" disabled={minutes == null || !totals.pending} title={pendingButton.tip} onclick={() => open(null, 'pending')}>{pendingButton.text}</button>
    <span id="stale-threshold-note" class:stale-error={minutes == null}>{minutes == null ? 'Enter a number of minutes above 0 and up to ' + formatCount(MAX_STALE_MINUTES) + '.' : 'The ' + DEFAULT_STALE_MINUTES + ' minute default is a starting point taken from the code, not measured from live data; change it to suit your system.'}</span>
  </div>
  <div class="table-scroll group-table-scroll">
    <table class="comparison-table incomplete-table">
      <thead><tr>
        <th>Operation</th><th>Request type</th>
        <th title="Completed / (completed + incomplete + pending). Lowest first.">Completion %<Hint termId="completion-percent" /></th>
        <th title="Started but never completed, not counting accepted asynchronous jobs.">Incomplete</th>
        <th title="Asynchronous requests that were accepted (202) and have no final result yet.">Pending async<Hint termId="pending-async" /></th>
        <th title="A duration was present but could not be used. Not part of the completion share.">Unusable<Hint termId="unusable-timing" /></th>
      </tr></thead>
      <tbody>
        {#each worst as item (item.rollup.controller + '|' + item.rollup.action + '|' + (item.rollup.kind ?? ''))}
          <tr>
            <td data-label="Operation">{item.rollup.controller}.{item.rollup.action}</td>
            <td data-label="Request type">{kindLabel(item.rollup.kind ?? '') || '—'}</td>
            <td class="numeric" data-label="Completion %">{rateText(item.rate)}</td>
            <td class="numeric" data-label="Incomplete">{#if item.rollup.incomplete}<button type="button" class="api-select" disabled={minutes == null} title={rowIncompleteTip} onclick={() => open(item.rollup, 'incomplete')}>{formatCount(item.rollup.incomplete)}</button>{:else}0{/if}</td>
            <td class="numeric" data-label="Pending async">{#if item.rollup.pending}<button type="button" class="api-select" disabled={minutes == null} title={rowPendingTip} onclick={() => open(item.rollup, 'pending')}>{formatCount(item.rollup.pending)}</button>{:else}0{/if}</td>
            <td class="numeric" data-label="Unusable">{formatCount(item.rollup.invalid ?? 0)}</td>
          </tr>
        {:else}<tr><td colspan="6" class="table-empty">{ranked.length ? 'No operation has incomplete or pending requests in this window.' : 'No operation has anything to measure a completion share from.'}</td></tr>{/each}
      </tbody>
    </table>
  </div>
  {#if totals.invalid}<p class="incomplete-note">{formatCount(totals.invalid)} unusable {totals.invalid === 1 ? 'row is' : 'rows are'} left out of every share above.</p>{/if}
</section>
{/if}

<style>
  .incomplete-panel { margin-bottom: 14px; }
  .stale-error { color: var(--bad); }
  .incomplete-note { margin: 0; padding: 8px 12px; color: var(--text-2); font-size: 12px; }
</style>
