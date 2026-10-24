<script lang="ts">
  import { app, cancelAnalysis, clearChips, onScopeChange, panels, removeChip, results, runAnalysis, scope } from '../lib/appState.svelte';
  import FilterChips from './FilterChips.svelte';
  import QueryControls from './QueryControls.svelte';

  // The scope row under the top bar: period and request type, More filters, the run hint and button, and the active filter chips.
  const runHint = $derived(scope.runHint);
  const moreCount = $derived(scope.moreCount);
  const loading = $derived(results.loading);
  const groupNames = $derived((results.analysis?.apis ?? []).map(row => row.controller + '.' + row.action));
</script>

<section class="scope" aria-label="Scope" inert={panels.modalOpen}>
  <div class="scope-main">
    {#key scope.controlsResetKey}<QueryControls bind:query={scope.query} bind:zone={scope.zone} bind:targetMs={scope.targetMs} bind:dateInputsValid={scope.dateInputsValid} part="bar" {groupNames} now={app.info?.now ?? ''} {onScopeChange} />{/key}
    <details class="more-filters">
      <summary>More filters{#if moreCount > 0}<span class="count-badge" aria-label={moreCount + ' active'}>{moreCount}</span>{/if}</summary>
      <div class="more-body">
        {#key scope.controlsResetKey}<QueryControls bind:query={scope.query} bind:zone={scope.zone} bind:targetMs={scope.targetMs} bind:dateInputsValid={scope.dateInputsValid} part="panel" {groupNames} now={app.info?.now ?? ''} {onScopeChange} />{/key}
      </div>
    </details>
    <div class="run-actions"><span class="run-hint" id="run-hint" role="status">{runHint}</span>{#if loading}<button class="quiet-button" onclick={cancelAnalysis}>Cancel run</button>{/if}<button class="primary-button" aria-describedby="run-hint" disabled={loading || !scope.query.sourceIds.length || !scope.dateInputsValid} onclick={() => runAnalysis()}>{loading ? 'Comparing…' : 'Run analysis'}</button></div>
  </div>
  <FilterChips chips={scope.filterChipList} onRemove={removeChip} onClear={clearChips} />
</section>
