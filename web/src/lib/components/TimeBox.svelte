<script lang="ts">
  import { PICKER_TEXT } from '../copy/pickerCopy';
  import { HOURS_24, MINUTES_60, timeFromParts, timeParts } from '../tracing/pickerModel';

  // One time of day as two boxes, the hour (00 to 23) and the minute: 24-hour time, as every screen shows it (owner call, 2026-11-21). It is
  // bound to the `HH:mm` text the screens keep. An empty value shows "--" in both boxes and means no time; picking one part of an empty time
  // fills the other with 00, so one pick is always a whole time. The person cannot pick an impossible time here.
  let { value = '', label, disabled = false, invalid = false, describedby, onchange }: {
    value?: string;
    /** The spoken name of the time, for example "Incident window start time". The two boxes add hour and minute. */
    label: string;
    disabled?: boolean;
    invalid?: boolean;
    describedby?: string;
    /** Called with the new 24-hour `HH:mm` text after the person picks a part. */
    onchange?: (value: string) => void;
  } = $props();

  const parts = $derived(timeParts(value));
  const two = (n: number): string => String(n).padStart(2, '0');

  function pick(change: { hour?: string; minute?: string }) {
    const hour = change.hour ?? (parts ? String(parts.hour) : '');
    const minute = change.minute ?? (parts ? String(parts.minute) : '');
    // Choosing "--" for the hour or the minute clears the whole time (a half time is not a time); picking a part of a cleared time fills the other.
    const cleared = change.hour === '' || change.minute === '';
    onchange?.(cleared ? '' : timeFromParts(hour === '' ? 0 : +hour, minute === '' ? 0 : +minute));
  }
</script>

<span class="tb" role="group" aria-label={label} data-time-box>
  <select class="th" aria-label={`${label} ${PICKER_TEXT.hourWord}`} aria-invalid={invalid ? true : undefined} aria-describedby={describedby} {disabled}
    value={parts ? String(parts.hour) : ''} onchange={(e) => pick({ hour: e.currentTarget.value })}>
    <option value="">{PICKER_TEXT.unset}</option>
    {#each HOURS_24 as h (h)}<option value={String(h)}>{two(h)}</option>{/each}
  </select>
  <span class="colon" aria-hidden="true">:</span>
  <select class="tm" aria-label={`${label} ${PICKER_TEXT.minuteWord}`} aria-invalid={invalid ? true : undefined} aria-describedby={describedby} {disabled}
    value={parts ? String(parts.minute) : ''} onchange={(e) => pick({ minute: e.currentTarget.value })}>
    <option value="">{PICKER_TEXT.unset}</option>
    {#each MINUTES_60 as m (m)}<option value={String(m)}>{two(m)}</option>{/each}
  </select>
</span>

<style>
  .tb { display: inline-flex; align-items: center; gap: 3px; min-width: 0; }
  select { width: auto; min-width: 0; min-height: 34px; padding: 4px 4px; font-size: 13px; font-family: var(--mono); font-variant-numeric: tabular-nums; }
  select.th { width: 54px; }
  select.tm { width: 54px; }
  select[aria-invalid='true'] { border-color: var(--bad); }
  .colon { color: var(--text-2); font-family: var(--mono); }
  select:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  @media (max-width: 1024px), (pointer: coarse) { select { min-height: 40px; } }
</style>
