<script lang="ts">
  import { PICKER_TEXT } from '../copy/pickerCopy';
  import { HOURS_12, MINUTES_60, timeFromParts, timeParts } from '../tracing/pickerModel';

  // One time of day as three boxes: the hour (1 to 12), the minute and AM or PM. It is bound to the 24-hour `HH:mm` text the screens keep, so
  // nothing outside this box knows about 12-hour time. An empty value shows "--" in all three boxes and means no time; picking any part of an empty
  // time fills the rest with 12, 00 and AM, so one pick is always a whole time. The person cannot type an impossible time here (the model still
  // reads typed 24-hour text).
  let { value = '', label, disabled = false, invalid = false, describedby, onchange }: {
    value?: string;
    /** The spoken name of the time, for example "Incident window start time". The three boxes add hour, minute and AM or PM. */
    label: string;
    disabled?: boolean;
    invalid?: boolean;
    describedby?: string;
    /** Called with the new 24-hour `HH:mm` text after the person picks a part. */
    onchange?: (value: string) => void;
  } = $props();

  const parts = $derived(timeParts(value));
  const two = (n: number): string => String(n).padStart(2, '0');

  function pick(change: { hour?: string; minute?: string; period?: string }) {
    const hour = change.hour ?? (parts ? String(parts.hour) : '');
    const minute = change.minute ?? (parts ? String(parts.minute) : '');
    const period = change.period ?? (parts?.pm ? 'PM' : 'AM');
    // Choosing "--" for the hour, the minute or the period clears the whole time (a half time is not a time); picking any part of a cleared time fills the rest.
    const cleared = change.hour === '' || change.minute === '' || change.period === '';
    const next = cleared ? '' : timeFromParts(hour === '' ? 12 : +hour, minute === '' ? 0 : +minute, period === 'PM');
    onchange?.(next);
  }
</script>

<span class="tb" role="group" aria-label={label} data-time-box>
  <select class="th" aria-label={`${label} ${PICKER_TEXT.hourWord}`} aria-invalid={invalid ? true : undefined} aria-describedby={describedby} {disabled}
    value={parts ? String(parts.hour) : ''} onchange={(e) => pick({ hour: e.currentTarget.value })}>
    <option value="">{PICKER_TEXT.unset}</option>
    {#each HOURS_12 as h (h)}<option value={String(h)}>{h}</option>{/each}
  </select>
  <span class="colon" aria-hidden="true">:</span>
  <select class="tm" aria-label={`${label} ${PICKER_TEXT.minuteWord}`} aria-invalid={invalid ? true : undefined} aria-describedby={describedby} {disabled}
    value={parts ? String(parts.minute) : ''} onchange={(e) => pick({ minute: e.currentTarget.value })}>
    <option value="">{PICKER_TEXT.unset}</option>
    {#each MINUTES_60 as m (m)}<option value={String(m)}>{two(m)}</option>{/each}
  </select>
  <select class="tp" aria-label={`${label} ${PICKER_TEXT.periodWord}`} aria-invalid={invalid ? true : undefined} aria-describedby={describedby} {disabled}
    value={parts ? (parts.pm ? 'PM' : 'AM') : ''} onchange={(e) => pick({ period: e.currentTarget.value })}>
    <option value="">{PICKER_TEXT.unset}</option>
    <option value="AM">{PICKER_TEXT.am}</option>
    <option value="PM">{PICKER_TEXT.pm}</option>
  </select>
</span>

<style>
  .tb { display: inline-flex; align-items: center; gap: 3px; min-width: 0; }
  select { width: auto; min-width: 0; min-height: 34px; padding: 4px 4px; font-size: 13px; font-family: var(--mono); font-variant-numeric: tabular-nums; }
  select.th { width: 54px; }
  select.tm { width: 54px; }
  select.tp { width: 58px; }
  select[aria-invalid='true'] { border-color: var(--bad); }
  .colon { color: var(--text-2); font-family: var(--mono); }
  select:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  @media (max-width: 1024px), (pointer: coarse) { select { min-height: 40px; } }
</style>
