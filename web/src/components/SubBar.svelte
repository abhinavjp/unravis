<script lang="ts">
  import { untrack } from 'svelte';
  import { app, cancelAnalysis, clearChips, onScopeChange, panels, removeChip, results, runAnalysis, scope } from '../lib/appState.svelte';
  import { filterChips, filtersInUseText, panelGroupCounts } from '../lib/depth';
  import { firstActiveGroup, type RailGroup } from '../lib/filterRail';
  import FilterChips from './FilterChips.svelte';
  import QueryControls from './QueryControls.svelte';
  import { intervalNote } from '../lib/intervalNote.svelte';

  // The scope row under the top bar: period and request type, More filters, the run hint and button, and the active filter chips.
  const runHint = $derived(scope.runHint);
  const moreCount = $derived(scope.moreCount);
  const loading = $derived(results.loading);
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
    <div class="run-actions"><span class="run-hint" id="run-hint" role="status">{runHint}</span>{#if loading}<button class="quiet-button" onclick={cancelAnalysis}>Cancel run</button>{/if}<button class="primary-button" aria-describedby="run-hint" disabled={loading || !scope.query.sourceIds.length || !scope.dateInputsValid} onclick={() => runAnalysis()}>{loading ? 'Comparing…' : 'Run analysis'}</button></div>
  </div>
  {#if intervalNote.text}<p class="interval-note" role="status">{intervalNote.text}</p>{/if}
  {#if !moreOpen}<FilterChips chips={scope.filterChipList} onRemove={removeChip} onClear={clearChips} onEmpty={() => summaryEl?.focus()} />{/if}
</section>
