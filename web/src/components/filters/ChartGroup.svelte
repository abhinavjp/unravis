<script lang="ts">
  import type { Query } from '../../lib/types';
  import { metricLabels } from '../../lib/presentation';
  import { intervalFitNote } from '../../lib/autorun';
  import { chartIntervalProblem } from '../../lib/filterRail';
  import { intervalNote } from '../../lib/intervalNote.svelte';
  import FilterGroup from './FilterGroup.svelte';
  import Hint from '../Hint.svelte';
  import HelpNote from '../HelpNote.svelte';
  import FieldHelp from '../FieldHelp.svelte';
  // The Chart group of the More filters panel: what the charts measure, how finely they slice time, and an optional response-time target.
  // The panel never starts a run, so nothing here calls onScopeChange.
  // hasError lets the panel rail show a problem dot while this group is on a hidden pane; it is true when the interval would stop the run.
  let { query = $bindable(), targetMs = $bindable(), count = 0, hasError = $bindable(false) }: { query: Query; targetMs: number | null; count?: number; hasError?: boolean } = $props();
  const intervalFitText = $derived(intervalFitNote(query));
  $effect(() => { hasError = chartIntervalProblem(query); });
</script>

<FilterGroup id="chart" title="Chart" {count}>
  {#snippet help()}<HelpNote id="chart" />{/snippet}
  <div class="field-grid">
    <div class="control-group"><span class="label-line"><label for="metric">Time measured</label><Hint termId="response-time" /><FieldHelp id="metric" /></span><select id="metric" bind:value={query.metric}><option value="total">{metricLabels.total}</option><option value="api">{metricLabels.api}</option></select></div>
    <div class="control-group">
      <span class="label-line"><label for="bucket">Chart interval (seconds)</label><Hint termId="time-slice" /><FieldHelp id="bucket" /></span>
      <input id="bucket" type="number" min="10" max="86400" step="1" bind:value={query.bucketSeconds} oninput={() => intervalNote.clear()} />
      <p class="field-note">10–86,400 seconds; no more than 120 points.</p>
      {#if intervalFitText}<p class="field-note interval-note" role="status">{intervalFitText}</p>{/if}
    </div>
    <div class="control-group wide">
      <span class="label-line"><label for="latency-target">Optional response-time target (ms)</label><Hint termId="p95" /><FieldHelp id="latency-target" /></span>
      <input id="latency-target" type="number" min="0.01" step="any" bind:value={targetMs} placeholder="No target" />
      <p class="field-note">Adds a chart line compared with P95.</p>
    </div>
  </div>
</FilterGroup>

<style>
  .field-grid > .wide { grid-column: 1 / -1; }
</style>
