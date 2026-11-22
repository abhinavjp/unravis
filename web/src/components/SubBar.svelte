<script lang="ts">
  import { untrack } from 'svelte';
  import { app, FILTERS_CHANGED_HINT, cancelAnalysis, clearChips, onScopeChange, panels, removeChip, results, runAnalysis, scope } from '../lib/appState.svelte';
  import { filterChips, filtersInUseText, panelGroupCounts } from '../lib/depth';
  import { firstActiveGroup, type RailGroup } from '../lib/filterRail';
  import FilterChips from './FilterChips.svelte';
  import QueryControls from './QueryControls.svelte';
  import { intervalNote } from '../lib/intervalNote.svelte';
  import { RUN_TEXT, WAIT_TEXT } from '../lib/copy/waitCopy';
  import { createSlowRead } from '../lib/slowRead.svelte';

  // The scope row under the top bar: period and request type, More filters, the run hint and button, and the active filter chips.
  const runHint = $derived(scope.runHint);
  const moreCount = $derived(scope.moreCount);
  const loading = $derived(results.loading);
  // A large window can take a minute: after a few seconds the hint says so and Cancel run stays beside it.
  const waiting = createSlowRead(() => results.loading);
  // Only the short "Filters changed" line (or nothing) sits in the space reserved under the button. Any other sentence (why the run cannot start, that the read is slow)
  // is read in full: it wraps beside the button and is never cut off (SWEEP-F2-S3).
  const hintText = $derived(loading && waiting.slow ? WAIT_TEXT.stillReading : runHint);
  const reservedHint = $derived(hintText === '' || hintText === FILTERS_CHANGED_HINT);
  const groupNames = $derived((results.analysis?.apis ?? []).map(row => row.controller + '.' + row.action));
  // While the panel is open its own header shows the chips, so the outer row hides (no chip ever appears twice).
  let moreOpen = $state(false);
  let summaryEl = $state<HTMLElement>();
  // The selected group of the filter rail lives here, so it survives the panel remounting (Clear all, a reset view).
  // Opening the panel selects the first group that has an active filter, else When.
  let panelGroup = $state<RailGroup>('when');
  $effect(() => {
    if (moreOpen) untrack(() => { panelGroup = firstActiveGroup(panelGroupCounts(filterChips(scope.query))); });
  });
</script>

<section class="scope" aria-label="Scope" inert={panels.modalOpen}>
  <div class="scope-main">
    {#key scope.controlsResetKey}<QueryControls bind:query={scope.query} bind:targetMs={scope.targetMs} bind:dateInputsValid={scope.dateInputsValid} part="bar" {groupNames} now={app.info?.now ?? ''} {onScopeChange} />{/key}
    <details class="more-filters" bind:open={moreOpen}>
      <summary bind:this={summaryEl}>More filters{#if moreCount > 0}<span class="count-badge" aria-label={moreCount + ' active'}>{moreCount}</span>{/if}</summary>
      <div class="more-body">
        <div class="panel-head"><p class="panel-count">{filtersInUseText(moreCount)}</p>{#if results.displayedRunIsStale && !loading}<p class="panel-stale">Changes here apply when you press Run analysis.</p>{/if}<FilterChips chips={scope.filterChipList} onRemove={removeChip} onClear={clearChips} onEmpty={() => summaryEl?.focus()} /></div>
        {#key scope.controlsResetKey}<QueryControls bind:query={scope.query} bind:targetMs={scope.targetMs} bind:dateInputsValid={scope.dateInputsValid} bind:activeGroup={panelGroup} part="panel" {groupNames} now={app.info?.now ?? ''} {onScopeChange} />{/key}
      </div>
    </details>
    <div class="run-actions"><span class="run-hint" class:stale={results.displayedRunIsStale && !loading} class:reserved={reservedHint} id="run-hint" role="status">{hintText}</span>{#if loading}<button class="quiet-button" title={RUN_TEXT.cancelRunTitle} onclick={cancelAnalysis}>{RUN_TEXT.cancelRun}</button>{/if}<button class="primary-button" aria-describedby="run-hint" disabled={loading || !scope.query.sourceIds.length || !scope.dateInputsValid} onclick={() => runAnalysis()}>{loading ? RUN_TEXT.running : RUN_TEXT.run}</button></div>
  </div>
  {#if intervalNote.text}<p class="interval-note" role="status">{intervalNote.text}</p>{/if}
  {#if !moreOpen}<FilterChips chips={scope.filterChipList} onRemove={removeChip} onClear={clearChips} onEmpty={() => summaryEl?.focus()} />{/if}
</section>

<style>
  /* The short "Filters changed" line under the Run button sits in space the bar always has, so it appearing or going never moves the bar or the page below it
     (ANALYSIS-04). It takes no width, so it cannot push the chips onto a second row. Any longer sentence (why the run cannot start) is not in that space: it
     stays in the flow beside the button and wraps, so it is never cut off (SWEEP-F2-S3). On a phone the hint stays in the flow, as before. */
  @media (min-width: 721px) {
    .scope-main { align-items: flex-start; }
    .scope-main .run-actions { position: relative; align-self: flex-start; padding-bottom: 20px; margin-bottom: -14px; }
    .scope-main .run-hint.reserved { position: absolute; right: 0; bottom: 0; margin: 0; max-width: min(520px, 60vw); line-height: 16px; white-space: nowrap; }
    .scope-main .run-hint.reserved.stale { padding: 0 0 0 8px; font-size: 12px; background: transparent; }
    .scope-main .run-hint:not(.reserved) { max-width: min(680px, 60vw); line-height: 16px; text-align: right; overflow-wrap: anywhere; }
    .scope-main .run-actions:has(.run-hint:not(.reserved)) { padding-bottom: 0; margin-bottom: 0; align-items: flex-end; }
  }
</style>
