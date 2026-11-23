<script lang="ts">
  import { untrack } from 'svelte';
  import type { Query } from '../../lib/types';
  import { bucketForWindows, zoneAbbreviation } from '../../lib/time';
  import { fromZonedParts, toZonedInput } from '../../lib/zonedInput';
  import { TIME24_HINT } from '../../lib/time24';
  import { TIME_ZONE_OPTIONS, timeZone } from '../../lib/timeZone.svelte';
  import { intervalReplacedNote } from '../../lib/autorun';
  import { intervalNote } from '../../lib/intervalNote.svelte';
  import { isManualBucket } from '../../lib/zoom';
  import DateTimeField from '../DateTimeField.svelte';
  import Hint from '../Hint.svelte';
  import HelpNote from '../HelpNote.svelte';
  import FieldHelp from '../FieldHelp.svelte';
  import FilterGroup from './FilterGroup.svelte';
  // The two date windows of the More filters panel. Only the panel renders this component, so it always owns the validity state:
  // dateInputsValid follows invalidWindows, and a valid value set from outside (a quick range in the bar) clears the stale errors.
  let { query = $bindable(), dateInputsValid = $bindable() }: { query: Query; dateInputsValid: boolean } = $props();
  let timeInputError = $state('');
  let invalidWindows = $state<string[]>([]);
  $effect(() => { dateInputsValid = invalidWindows.length === 0; });
  $effect(() => { if (dateInputsValid) untrack(() => { if (invalidWindows.length) { invalidWindows = []; timeInputError = ''; } }); });
  // The boxes show and take the wall clock of the zone the header chose ("Times in"), so what is typed here is read in the same zone as every time on screen (ANALYSIS-01).
  // The query keeps UTC; only the boxes convert. Each box names its zone at its own date, because the name changes across a clock change ("GMT" in winter, "GMT+1" in summer).
  const zone = $derived(timeZone.zone);
  const zoneLabel = $derived(timeZone.choice === 'local' ? timeZone.browserZone : TIME_ZONE_OPTIONS.find(option => option.id === timeZone.choice)?.label ?? zone);
  // The time box hands over HH:MM:SS.mmm when the text is a 24-hour time; anything else came from text that is not one.
  const TIME24 = /^\d{2}:\d{2}:\d{2}\.\d{3}$/;
  const localValue = (value: string) => toZonedInput(value, zone);
  const zoneAt = (value: string) => zoneAbbreviation(zone, value);
  const setWindow = (field: 'start' | 'end' | 'baselineStart' | 'baselineEnd', datePart: string, timePart: string) => {
    if (!datePart || !timePart) {
      invalidWindows = invalidWindows.includes(field) ? invalidWindows : [...invalidWindows, field];
      timeInputError = 'Enter both a date and time for every range. Choose another time if the clocks change made that local value invalid.';
      return;
    }
    if (!TIME24.test(timePart)) {
      invalidWindows = invalidWindows.includes(field) ? invalidWindows : [...invalidWindows, field];
      timeInputError = TIME24_HINT;
      return;
    }
    try {
      const previous = query.bucketSeconds, typed = isManualBucket(query);
      query[field] = fromZonedParts(datePart, timePart, zone, query[field]);
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
  {#snippet lede()}Times are in <strong>{zoneLabel}</strong>, as set by Times in at the top of the page.{/snippet}
  {#snippet help()}<HelpNote id="when" />{/snippet}
  <div class="when-windows">
    <section class="when-window" aria-labelledby="current-range-label">
      <div class="label-line"><h3 id="current-range-label">Investigate</h3><FieldHelp id="current-range" /></div>
      <p class="when-sub">Requests in the period you want to inspect.</p>
      <div class="window-bounds">
        <DateTimeField id="current-start" label="From" help="current-start" helpLabel="Investigate from" zone={zoneAt(query.start)} value={localValue(query.start)} invalid={invalidWindows.includes('start')} errorId="when-time-error" onChange={(date, time) => setWindow('start', date, time)} />
        <DateTimeField id="current-end" label="To" help="current-end" helpLabel="Investigate to" zone={zoneAt(query.end)} value={localValue(query.end)} invalid={invalidWindows.includes('end')} errorId="when-time-error" onChange={(date, time) => setWindow('end', date, time)} />
      </div>
      {#if !invalidWindows.some(field => field === 'start' || field === 'end')}<p class="utc-echo">UTC <time datetime={query.start}>{query.start}</time> – <time datetime={query.end}>{query.end}</time></p>{/if}
    </section>
    <section class="when-window" aria-labelledby="reference-range-label">
      <div class="label-line"><h3 id="reference-range-label">Reference<Hint termId="reference-period" /></h3><FieldHelp id="reference-range" /></div>
      <p class="when-sub">An earlier period to compare with.</p>
      <div class="window-bounds">
        <DateTimeField id="reference-start" label="From" help="reference-start" helpLabel="Reference from" zone={zoneAt(query.baselineStart)} value={localValue(query.baselineStart)} invalid={invalidWindows.includes('baselineStart')} errorId="when-time-error" onChange={(date, time) => setWindow('baselineStart', date, time)} />
        <DateTimeField id="reference-end" label="To" help="reference-end" helpLabel="Reference to" zone={zoneAt(query.baselineEnd)} value={localValue(query.baselineEnd)} invalid={invalidWindows.includes('baselineEnd')} errorId="when-time-error" onChange={(date, time) => setWindow('baselineEnd', date, time)} />
      </div>
      {#if !invalidWindows.some(field => field === 'baselineStart' || field === 'baselineEnd')}<p class="utc-echo">UTC <time datetime={query.baselineStart}>{query.baselineStart}</time> – <time datetime={query.baselineEnd}>{query.baselineEnd}</time></p>{/if}
    </section>
  </div>
  {#if timeInputError}<p class="inline-error" role="alert" id="when-time-error">{timeInputError}</p>{/if}
</FilterGroup>

<style>
  .when-windows { display: grid; grid-template-columns: minmax(0, 1fr); gap: 12px var(--space-8); min-width: 0; }
  .when-window { display: grid; gap: 6px; align-content: start; min-width: 0; }
  .when-window h3 { display: flex; align-items: center; margin: 0; font-size: 12px; font-weight: 600; }
  /* The "?" hint sits inside the heading and the "i" button follows the heading, so the gap has to come from the heading: the hint's 24px box
     reaches 4px past the heading, and the "i" button on a touch or narrow screen reaches 4px back into it (BL-474). */
  .when-window h3 { margin-inline-end: var(--space-1); }
  @media (pointer: coarse), (max-width: 720px) { .when-window h3 { margin-inline-end: var(--space-2); } }
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
