<script lang="ts">
  import { untrack } from 'svelte';
  import type { Query } from '../../lib/types';
  import { bucketForWindows, fromLocalParts, toLocalDatePart, toLocalTimePart } from '../../lib/time';
  import { intervalReplacedNote } from '../../lib/autorun';
  import { intervalNote } from '../../lib/intervalNote.svelte';
  import { isManualBucket } from '../../lib/zoom';
  import DateTimeField from '../DateTimeField.svelte';
  import Hint from '../Hint.svelte';
  import FilterGroup from './FilterGroup.svelte';
  // The two date windows of the More filters panel. Only the panel renders this component, so it always owns the validity state:
  // dateInputsValid follows invalidWindows, and a valid value set from outside (a quick range in the bar) clears the stale errors.
  let { query = $bindable(), dateInputsValid = $bindable() }: { query: Query; dateInputsValid: boolean } = $props();
  let timeInputError = $state('');
  let invalidWindows = $state<string[]>([]);
  $effect(() => { dateInputsValid = invalidWindows.length === 0; });
  $effect(() => { if (dateInputsValid) untrack(() => { if (invalidWindows.length) { invalidWindows = []; timeInputError = ''; } }); });
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
      const previous = query.bucketSeconds, typed = isManualBucket(query);
      query[field] = fromLocalParts(datePart, timePart);
      query.bucketSeconds = bucketForWindows(query.start, query.end, query.baselineStart, query.baselineEnd);
      intervalNote.set(typed ? intervalReplacedNote(previous, query) : '');
      invalidWindows = invalidWindows.filter(item => item !== field);
      timeInputError = invalidWindows.length ? 'Complete every date and time with a valid local value before running the comparison.' : '';
    } catch (error) {
      invalidWindows = invalidWindows.includes(field) ? invalidWindows : [...invalidWindows, field];
      timeInputError = error instanceof Error ? error.message : 'Choose a valid local date and time.';
    }
  };
</script>

<FilterGroup id="when" title="When">
  {#snippet lede()}Times use your browser time zone: <strong>{browserZone}</strong>.{/snippet}
  <div class="when-windows">
    <section class="when-window" aria-labelledby="current-range-label">
      <h3 id="current-range-label">Investigate</h3>
      <p class="when-sub">Requests in the period you want to inspect.</p>
      <div class="window-bounds">
        <DateTimeField id="current-start" label="From" value={localValue(query.start)} onChange={(date, time) => setWindow('start', date, time)} />
        <DateTimeField id="current-end" label="To" value={localValue(query.end)} onChange={(date, time) => setWindow('end', date, time)} />
      </div>
      {#if !invalidWindows.some(field => field === 'start' || field === 'end')}<p class="utc-echo">UTC <time datetime={query.start}>{query.start}</time> – <time datetime={query.end}>{query.end}</time></p>{/if}
    </section>
    <section class="when-window" aria-labelledby="reference-range-label">
      <h3 id="reference-range-label">Reference<Hint termId="reference-period" /></h3>
      <p class="when-sub">An earlier period to compare with.</p>
      <div class="window-bounds">
        <DateTimeField id="reference-start" label="From" value={localValue(query.baselineStart)} onChange={(date, time) => setWindow('baselineStart', date, time)} />
        <DateTimeField id="reference-end" label="To" value={localValue(query.baselineEnd)} onChange={(date, time) => setWindow('baselineEnd', date, time)} />
      </div>
      {#if !invalidWindows.some(field => field === 'baselineStart' || field === 'baselineEnd')}<p class="utc-echo">UTC <time datetime={query.baselineStart}>{query.baselineStart}</time> – <time datetime={query.baselineEnd}>{query.baselineEnd}</time></p>{/if}
    </section>
  </div>
  {#if timeInputError}<p class="inline-error" role="alert">{timeInputError}</p>{/if}
</FilterGroup>

<style>
  .when-windows { display: grid; grid-template-columns: minmax(0, 1fr); gap: 12px var(--space-8); min-width: 0; }
  .when-window { display: grid; gap: 6px; align-content: start; min-width: 0; }
  .when-window h3 { display: flex; align-items: center; margin: 0; font-size: 12px; font-weight: 600; }
  .when-sub { margin: 0; color: var(--text-2); font-size: 12px; line-height: 1.45; }
  .window-bounds { display: grid; grid-template-columns: minmax(0, 1fr); gap: 8px; min-width: 0; }
  .utc-echo { margin: 0; color: var(--text-2); font-size: 12px; font-variant-numeric: tabular-nums; overflow-wrap: anywhere; }
  @media (min-width: 721px) {
    .when-windows { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  }
  @media (min-width: 1100px) {
    .window-bounds { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  }
</style>
