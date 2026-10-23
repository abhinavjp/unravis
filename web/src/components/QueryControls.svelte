<script lang="ts">
  import type { Query } from '../lib/types';
  import { bucketForWindows, fromLocalParts, quickRange, quickRanges, toLocalDatePart, toLocalTimePart, zones, type QuickRangeId } from '../lib/time';
  import { metricLabels } from '../lib/presentation';
  import DateTimeField from './DateTimeField.svelte';
  let { query = $bindable(), zone = $bindable(), targetMs = $bindable(), dateInputsValid = $bindable(), now = '' }: { query: Query; zone: string; targetMs: number | null; dateInputsValid: boolean; now?: string } = $props();
  let timeInputError = $state('');
  let invalidWindows = $state<string[]>([]);
  $effect(() => { dateInputsValid = invalidWindows.length === 0; });
  const browserZone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'local time';
  const localValue = (value: string) => {
    const date = new Date(value);
    return `${toLocalDatePart(date)}T${toLocalTimePart(date)}`;
  };
  const setWindow = (field: 'start' | 'end' | 'baselineStart' | 'baselineEnd', datePart: string, timePart: string) => {
    if (!datePart || !timePart) {
      invalidWindows = invalidWindows.includes(field) ? invalidWindows : [...invalidWindows, field];
      timeInputError = 'Enter both a date and time for every range. Choose another time if the clocks change made that local value invalid.';
      return;
    }
    try {
      query[field] = fromLocalParts(datePart, timePart);
      query.bucketSeconds = bucketForWindows(query.start, query.end, query.baselineStart, query.baselineEnd);
      invalidWindows = invalidWindows.filter(item => item !== field);
      timeInputError = invalidWindows.length ? 'Complete every date and time with a valid local value before running the comparison.' : '';
    } catch (error) {
      invalidWindows = invalidWindows.includes(field) ? invalidWindows : [...invalidWindows, field];
      timeInputError = error instanceof Error ? error.message : 'Choose a valid local date and time.';
    }
  };
  // A quick range ends now (the server's clock) and compares with the equal-length period right before it.
  const applyQuickRange = (id: QuickRangeId) => {
    const base = new Date(now);
    Object.assign(query, quickRange(id, Number.isFinite(base.getTime()) ? base : new Date()));
    invalidWindows = []; timeInputError = '';
  };
</script>

<section class="query-controls" aria-label="Comparison settings">
  <div class="quick-ranges" role="group" aria-label="Quick ranges">
    <span>Quick range</span>
    {#each quickRanges as preset (preset.id)}<button type="button" class="secondary-button" title={'Investigate the ' + preset.label.toLowerCase() + ' and compare with the period right before it.'} onclick={() => applyQuickRange(preset.id)}>{preset.label}</button>{/each}
  </div>
  <div class="query-ranges">
    <section class="range-group" aria-labelledby="current-range-label">
      <div class="range-title"><h2 id="current-range-label">Investigate</h2><p>Requests in the period you want to inspect.</p></div>
      <DateTimeField id="current-start" label="From" value={localValue(query.start)} onChange={(date, time) => setWindow('start', date, time)} />
      <DateTimeField id="current-end" label="To" value={localValue(query.end)} onChange={(date, time) => setWindow('end', date, time)} />
    </section>
    <section class="range-group reference-range" aria-labelledby="reference-range-label">
      <div class="range-title"><h2 id="reference-range-label">Reference</h2><p>An earlier period to compare with.</p></div>
      <DateTimeField id="reference-start" label="From" value={localValue(query.baselineStart)} onChange={(date, time) => setWindow('baselineStart', date, time)} />
      <DateTimeField id="reference-end" label="To" value={localValue(query.baselineEnd)} onChange={(date, time) => setWindow('baselineEnd', date, time)} />
    </section>
  </div>
  <div class="query-footer">
    <p class="browser-time-note">Times use your browser time zone: <strong>{browserZone}</strong>.</p>
    {#if timeInputError}<p class="inline-error" role="alert">{timeInputError}</p>{/if}
    <details class="exact-times"><summary>Exact UTC times</summary><dl><dt>Current from</dt><dd>{query.start}</dd><dt>Current to</dt><dd>{query.end}</dd><dt>Reference from</dt><dd>{query.baselineStart}</dd><dt>Reference to</dt><dd>{query.baselineEnd}</dd></dl></details>
    <details class="advanced-filters">
      <summary>Filters and chart settings</summary>
      <div class="advanced-content">
        <div class="field-grid">
          <div class="control-group"><label for="kind">Request type</label><select id="kind" bind:value={query.kind}><option value="synchronous">Synchronous</option><option value="asynchronous">Asynchronous</option><option value="hangfire">Hangfire</option><option value="http">HTTP (synchronous, asynchronous and polling type unknown)</option><option value="http_unknown">HTTP (polling type unknown)</option><option value="all">All types</option></select></div>
          <div class="control-group"><label for="outcome">Status</label><select id="outcome" bind:value={query.outcome}><option value="all">All statuses</option><option value="success">Success</option><option value="failure">Failure</option><option value="client_error">Client errors (4xx)</option><option value="server_error">Server errors (5xx, incl. failed Hangfire jobs)</option></select></div>
          <div class="control-group"><label for="metric">Time measured</label><select id="metric" bind:value={query.metric}><option value="total">{metricLabels.total}</option><option value="api">{metricLabels.api}</option></select></div>
          <div class="control-group"><label for="bucket">Chart interval (seconds)</label><input id="bucket" type="number" min="10" max="86400" step="1" bind:value={query.bucketSeconds} /><p class="field-note">10–86,400 seconds; no more than 120 points.</p></div>
        </div>
        <div class="control-group"><label for="latency-target">Optional response-time target (ms)</label><input id="latency-target" type="number" min="0.01" step="any" bind:value={targetMs} placeholder="No target" /><p class="field-note">Adds a chart line and compares it with P95: the estimated time within which about 95 of 100 requests with usable timings finish.</p></div>
        <div class="field-grid"><div class="control-group"><label for="controller">Controller name</label><input id="controller" bind:value={query.controller} placeholder="Any controller" /></div><div class="control-group"><label for="action">Action name</label><input id="action" bind:value={query.action} placeholder="Any action" /></div></div>
        <div class="control-group"><label for="display-zone">Chart labels time zone</label><select id="display-zone" bind:value={zone}>{#each zones as item}<option value={item}>{item}</option>{/each}</select><p class="field-note">Changes chart labels only; it does not change either selected period.</p></div>
      </div>
    </details>
  </div>
</section>
