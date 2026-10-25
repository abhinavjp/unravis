<script lang="ts">
  import { breakdownDims } from '../lib/breakdown';
  import { metricLabels, qualityByKindText, unknownKindTip } from '../lib/presentation';
  import type { Lens } from '../lib/pulse';
  import type { Analysis, BreakdownDim } from '../lib/types';
  import { app, drillFromOverview, drillInto, drillQuality, evidence, navigation, openComparison, openFromTable, openOperation, results, scope } from '../lib/appState.svelte';
  import BreakdownPanel from './BreakdownPanel.svelte';
  import ComparisonTable from './ComparisonTable.svelte';
  import Hint from './Hint.svelte';
  import HelpNote from './HelpNote.svelte';
  import IncompletePanel from './IncompletePanel.svelte';
  import MultiTimeline from './MultiTimeline.svelte';
  import OverviewStrip from './OverviewStrip.svelte';
  import ProvenanceNote from './ProvenanceNote.svelte';
  import RequestVolume from './RequestVolume.svelte';
  import WhereList from './WhereList.svelte';

  // D1, "Where is it?": operations ranked by the chosen figure, the optional split, and the disclosures under the list.
  let { analysis }: { analysis: Analysis } = $props();

  const depth = $derived(navigation.depth);
  const lens = $derived(navigation.lens);
  const compare = $derived(navigation.compare);
  const splitDim = $derived(navigation.splitDim);
  const openKey = $derived(evidence.openKey);
  const selectedKey = $derived(evidence.selectedKey);
  const sourceLabels = $derived(app.sourceLabels);
  const zone = $derived(scope.zone);
  const breakdownQuery = $derived(results.breakdownQuery);
  const timelineKeys = $derived(results.timelineKeys);
  const calculationNotes = $derived(results.calculationNotes);
  const lensChoices: Array<{ id: Lens; label: string }> = [{ id: 'requests', label: 'Requests' }, { id: 'completion', label: 'Completion' }, { id: 'p95', label: 'P95' }, { id: 'client', label: '4xx' }, { id: 'server', label: '5xx' }];
  function pickSplit(dim: BreakdownDim | '', event: MouseEvent) {
    navigation.splitDim = splitDim === dim ? '' : dim;
    const menu = (event.currentTarget as HTMLElement).closest('details');
    if (menu) { menu.open = false; menu.querySelector('summary')?.focus(); }
  }
</script>

<div class="view" class:off={depth === 0 || depth === 3} class:under={depth === 2}>
  <div class="view-head">
    <div><h2 id="where-title" tabindex="-1">Where is it?</h2></div>
    <div class="view-tools">
      <button type="button" class="chip" onclick={openComparison}>All operations</button>
      <button type="button" class="chip" aria-pressed={compare} onclick={() => navigation.compare = !navigation.compare}>Compare to earlier</button><Hint termId="reference-period" />
      <details class="menu">
        <summary>{splitDim ? 'Split by ' + (breakdownDims.find(item => item.dim === splitDim)?.label ?? '') : 'Split by…'}</summary>
        <div class="menu-list">
          {#each breakdownDims as item (item.dim)}<button type="button" aria-pressed={splitDim === item.dim} onclick={(event) => pickSplit(item.dim, event)}>{item.label}</button>{/each}
          {#if splitDim}<button type="button" onclick={(event) => pickSplit('', event)}>No split</button>{/if}
        </div>
      </details>
    </div>
  </div>
  <HelpNote id="where" />
  <div class="lens-chips" role="group" aria-label="Rank by">
    {#each lensChoices as item (item.id)}<button type="button" class="chip" aria-pressed={lens === item.id} title={item.id === 'p95' ? metricLabels[analysis.query.metric] : undefined} onclick={() => navigation.lens = item.id}>{item.label}</button>{/each}<Hint termId="p95" />
  </div>
  <WhereList {analysis} {lens} {compare} {openKey} onOpen={(row) => openOperation(row)} />
  <ProvenanceNote provenance={analysis.provenance} {sourceLabels} />
  {#if splitDim && breakdownQuery}<BreakdownPanel query={breakdownQuery} {sourceLabels} lockedDim={splitDim} panelId="split-breakdown" onDrill={drillInto} />{/if}
  <div class="disclosures">
    <details class="disclosure">
      <summary>Quality of the data<small>timing, results and pending work</small></summary>
      <div class="disclosure-body">
        <OverviewStrip overview={analysis.overview ?? []} outcomeFilter={analysis.query.outcome} kindFilter={analysis.query.kind} onDrill={drillFromOverview}>
          {#snippet quality()}{#if analysis}
        <div class="quality-strip">
          <div><strong>{analysis.apis.length.toLocaleString()}</strong><span>API groups</span></div>
          <div><button type="button" class="quality-count" disabled={!analysis.quality.incomplete} title="List the requests behind this count" onclick={() => drillQuality('incomplete')}><strong>{analysis.quality.incomplete.toLocaleString()}</strong><span>Missing timing</span></button><Hint termId="missing-timing" /></div>
          <div><button type="button" class="quality-count" disabled={!analysis.quality.invalid} title="List the requests behind this count" onclick={() => drillQuality('invalid')}><strong>{analysis.quality.invalid.toLocaleString()}</strong><span>Unusable timing</span></button><Hint termId="unusable-timing" /></div>
          <div><button type="button" class="quality-count" disabled={!analysis.quality.unknownOutcome} title="List the requests behind this count" onclick={() => drillQuality('unclassified')}><strong>{analysis.quality.unknownOutcome.toLocaleString()}</strong><span>Unclassified result</span></button></div>
          {#if analysis.quality.pendingAsync}<div><button type="button" class="quality-count" title="List the requests behind this count" onclick={() => drillQuality('pending')}><strong>{analysis.quality.pendingAsync.toLocaleString()}</strong><span>pending async</span></button><Hint termId="pending-async" /></div>{/if}
          {#if qualityByKindText(analysis.quality)}<div class="quality-filter-note" title={unknownKindTip}><span>{qualityByKindText(analysis.quality)}</span></div>{/if}
          {#if analysis.query.outcome === 'client_error' || analysis.query.outcome === 'server_error'}<div class="quality-filter-note"><span>Counts above cover only the selected status class.</span></div>{/if}
        <p>Response times are estimates. A comparison shows change, not a health verdict.</p>
        <details class="quality-definitions"><summary>What these counts mean</summary><p><strong>Missing timing:</strong> no complete duration was recorded. <strong>Unusable timing:</strong> a duration was present but could not be used. <strong>Unclassified result:</strong> the request could not be identified as a success or failure. <strong>Pending async:</strong> an asynchronous request that only answered 202 Accepted; its work is not finished, so it is counted as missing timing and never as a response-time sample.</p></details>
        </div>
          {/if}{/snippet}
        </OverviewStrip>
        <IncompletePanel rollups={analysis.rollups ?? []} kindFilter={analysis.query.kind} onDrill={drillFromOverview} />
      </div>
    </details>
    <details class="disclosure">
      <summary>Request volume<small>every interval in the period</small></summary>
      <div class="disclosure-body"><RequestVolume {analysis} {zone} /></div>
    </details>
    <details class="disclosure">
      <summary>All {analysis.apis.length.toLocaleString()} API groups<small>one source, action, type and outcome each</small></summary>
      <div class="disclosure-body">
        <section class="data-section" id="api-group-results">
          <div class="section-heading"><div><h3>Explore {analysis.apis.length.toLocaleString()} API groups</h3><p>Largest slowdowns first. Each group keeps one source, API action, request type, and outcome together. P95 is the time within which about 95 of 100 requests finish.</p></div></div>
          <ComparisonTable rows={analysis.apis} rollups={analysis.rollups ?? []} timeline={analysis.timeline} kindFilter={analysis.query.kind} {selectedKey} {timelineKeys} onToggleTimeline={(key) => results.timelineKeys = timelineKeys.includes(key) ? timelineKeys.filter(item => item !== key) : timelineKeys.length < 8 ? [...timelineKeys, key] : timelineKeys} sources={sourceLabels} outcomeFilter={analysis.query.outcome} onDrill={drillFromOverview} onSelect={openFromTable} />
        </section>
        <MultiTimeline {analysis} keys={timelineKeys} sources={sourceLabels} {zone} onRemove={(key) => results.timelineKeys = timelineKeys.filter(item => item !== key)} />
      </div>
    </details>
    <details class="disclosure">
      <summary>About these results<small>calculation notes and sample sizes</small></summary>
      <div class="disclosure-body"><div class="calculation-notes">{#each calculationNotes as note}<p>{note}</p>{/each}</div></div>
    </details>
  </div>
</div>
