<script lang="ts">
  import { MIN_SAMPLE } from '../lib/thresholds';
  import { tick } from 'svelte';
  import { drillHeading } from '../lib/overview';
  import { kindLabel, stateLabel, unknownKindTip } from '../lib/presentation';
  import type { Analysis, Comparison } from '../lib/types';
  import { app, chooseGroup, clearDrill, closeInspector, drillFromIncomplete, drillInto, evidence, exportCSV, loadRequests, navigation, scope, showRecord } from '../lib/appState.svelte';
  import ActivityContext from './ActivityContext.svelte';
  import BreakdownPanel from './BreakdownPanel.svelte';
  import Charts from './Charts.svelte';
  import FailurePanel from './FailurePanel.svelte';
  import Hint from './Hint.svelte';
  import IncompletePanel from './IncompletePanel.svelte';
  import Inspector from './Inspector.svelte';
  import PhaseBar from './PhaseBar.svelte';
  import PlainLine from './PlainLine.svelte';
  import RequestTable from './RequestTable.svelte';
  import TrafficSlowness from './TrafficSlowness.svelte';

  // D2, the evidence inspector for the opened operation: its tabs, then the request history and nearby activity.
  let { analysis }: { analysis: Analysis } = $props();

  const depth = $derived(navigation.depth);
  const zone = $derived(scope.zone);
  const targetMs = $derived(scope.targetMs);
  const sourceLabels = $derived(app.sourceLabels);
  const selected = $derived(evidence.selected);
  const selectedKey = $derived(evidence.selectedKey);
  const operation = $derived(evidence.operation);
  const operationGroups = $derived(evidence.operationGroups);
  const evidenceName = $derived(evidence.evidenceName);
  const comparisonWide = $derived(evidence.comparisonWide);
  const selectedPlain = $derived(evidence.selectedPlain);
  const inspectorTabs = $derived(evidence.inspectorTabs);
  const activeTab = $derived(evidence.activeTab);
  const targetAssessment = $derived(evidence.targetAssessment);
  const scopedQuery = $derived(evidence.scopedQuery);
  const failureCohort = $derived(evidence.failureCohort);
  const slowScope = $derived(evidence.slowScope);
  const queryWide = $derived(evidence.queryWide);
  const drillOverview = $derived(evidence.drillOverview);
  const requestState = $derived(evidence.requestState);
  const drillActive = $derived(evidence.drillActive);
  const drillChips = $derived(evidence.drillChips);
  const requestError = $derived(evidence.requestError);
  const requestPage = $derived(evidence.requestPage);
  const requestRows = $derived(evidence.requestRows);
  const requestLoading = $derived(evidence.requestLoading);
  const requestPageNumber = $derived(evidence.requestPageNumber);
  const recordLoadingId = $derived(evidence.recordLoadingId);
  const contextAnchor = $derived(evidence.contextAnchor);
  const groupLabel = (row: Comparison) => (sourceLabels.get(row.sourceId) ?? 'Source unavailable') + ' · ' + row.outcome;
</script>

<Inspector off={depth !== 2} title={evidenceName} subtitle={operation ? kindLabel(operation.kind ?? 'unknown') + ' · ' + operation.requests.toLocaleString() + ' requests' : ''} tabs={inspectorTabs} active={activeTab} onTab={(id) => evidence.inspectorTab = id} onClose={closeInspector}>
  {#if comparisonWide}<p class="comparison-wide-note">Comparison-wide view: Failures, Slowest and Breakdown cover every operation in this comparison. The request list below follows what you opened.</p>{/if}
  {#if selectedPlain}<PlainLine result={selectedPlain} hints={['p50', 'p95']} />{/if}
  {#if activeTab === 'timeline' && selected}
    {#if operationGroups.length > 1}
      <div class="group-pick"><label for="group-select">Group</label>
        <select id="group-select" value={selectedKey} onchange={(event) => chooseGroup(event.currentTarget.value)}>{#each operationGroups as group (group.key)}<option value={group.key}>{groupLabel(group)}</option>{/each}</select>
      </div>
    {/if}
    <Charts {analysis} {selected} {zone} {targetMs} />
  {:else if activeTab === 'phases' && selected}
    {#if operationGroups.length > 1}
      <div class="group-pick"><label for="group-select-phases">Group</label>
        <select id="group-select-phases" value={selectedKey} onchange={(event) => chooseGroup(event.currentTarget.value)}>{#each operationGroups as group (group.key)}<option value={group.key}>{groupLabel(group)}</option>{/each}</select>
      </div>
    {/if}
    <section class="phase-group" aria-label="Where the time goes">
      <p>{sourceLabels.get(selected.sourceId) ?? 'Source unavailable'} · <span title={selected.kind === 'unknown' ? unknownKindTip : undefined}>{kindLabel(selected.kind)}</span> · {selected.outcome}</p>
      <PhaseBar phases={selected.current.phases ?? null} delta={selected.phaseDeltaMs ?? null} />
      <div class="selected-api-stats"><span><strong>{selected.current.count.toLocaleString()}</strong> current timings</span><span><strong>{selected.reference.count.toLocaleString()}</strong> reference timings</span><span><strong>{selected.p95Ratio == null ? 'Unavailable' : '≈ ' + selected.p95Ratio.toFixed(2) + '×'}</strong> P95 vs reference</span>{#if targetMs != null}<span>Target {targetMs} ms: {targetAssessment}</span>{/if}</div>
      {#if selected.current.count < MIN_SAMPLE || selected.reference.count < MIN_SAMPLE}<p class="sample-note">{selected.current.count === 0 ? 'No usable timings in the current period.' : selected.reference.count === 0 ? 'No usable timings in the reference period.' : 'Fewer than ' + MIN_SAMPLE + ' usable timings in at least one period.'} A slowdown comparison needs at least {MIN_SAMPLE} usable timings in each period.<Hint termId="low-sample" /></p>{/if}
    </section>
  {:else if activeTab === 'failures' && scopedQuery}
    <FailurePanel {analysis} query={scopedQuery} {sourceLabels} {zone} onDrill={drillInto} cohortKeys={failureCohort} />
  {:else if activeTab === 'incomplete' && operation}
    <IncompletePanel rollups={[operation]} kindFilter={analysis.query.kind} onDrill={drillFromIncomplete} />
  {:else if activeTab === 'slowest'}
    <TrafficSlowness {analysis} {zone} {sourceLabels} cohort={selected} scope={slowScope} onContext={async (row) => { evidence.contextAnchor = row; await tick(); document.getElementById('nearby-activity')?.scrollIntoView({ block: 'start' }); }} />
  {:else if activeTab === 'breakdown' && scopedQuery}
    <BreakdownPanel query={scopedQuery} {sourceLabels} onDrill={drillInto} />
  {/if}
  <section class="data-section request-section" id="request-history">
    <div class="section-heading"><div><h3>Request history</h3><p>{queryWide ? (drillOverview ? drillHeading(drillOverview) : stateLabel(requestState)) + ' · ' + (operation ? 'every group of this operation' : 'every API group in this comparison') + '.' : selected ? selected.controller + '.' + selected.action + ' · ' + kindLabel(selected.kind) + ' · ' + selected.outcome + '.' : drillActive ? 'Requests matching ' + drillChips.map(chip => chip.text).join(' · ') + '.' : 'Choose an operation to list its requests.'} Total ms is full request time; API ms is API processing time. Inspect opens every stored field and the captured request and response text.</p></div>
      {#if selected || queryWide}<button class="secondary-button" onclick={exportCSV}>Export CSV</button>{/if}
      {#if drillActive}<button class="quiet-button" onclick={clearDrill}>{operation ? 'Back to all of this operation' : 'Clear breakdown filter'}</button>{/if}
    </div>
    {#if requestError}<div class="inline-error" role="alert">{requestError}</div>{/if}
    {#if requestPage?.sources.some(source => !source.ok)}<div class="source-failure compact-failure" role="alert">{#each requestPage.sources.filter(source => !source.ok) as failed}<span>{sourceLabels.get(failed.sourceId) ?? failed.sourceId}: {failed.error}</span>{/each}</div>{/if}
    <RequestTable rows={requestRows} {sourceLabels} loading={requestLoading} pageNumber={requestPageNumber} nextCursor={requestPage?.nextCursor ?? ''} truncated={requestPage?.truncated ?? false} recordId={recordLoadingId} onFirst={() => loadRequests('', 1)} onNext={() => loadRequests(requestPage?.nextCursor ?? '', requestPageNumber + 1)} onInspect={showRecord} onContext={async (row) => { evidence.contextAnchor = row; await tick(); document.getElementById('nearby-activity')?.scrollIntoView({ block: 'start' }); }} />
  </section>
  {#if contextAnchor}<ActivityContext anchor={contextAnchor} sourceIds={analysis.query.sourceIds} {sourceLabels} {zone} />{/if}
</Inspector>

<style>
  /* A note in the evidence frame is a hatch dot and text between two hairlines: no box and no side stripe. */
  .comparison-wide-note { position: relative; margin: 0; padding: var(--space-2) 0 var(--space-2) var(--space-5); color: var(--text); font-size: var(--fs-3); line-height: 1.5; border-top: 1px solid var(--hairline); border-bottom: 1px solid var(--hairline); }
  .comparison-wide-note::before { content: ""; position: absolute; left: 0; top: calc(var(--space-2) + 0.45em); width: 9px; height: 9px; border-radius: 50%; background: var(--hatch); box-shadow: 0 0 0 1px var(--pending); }
</style>
