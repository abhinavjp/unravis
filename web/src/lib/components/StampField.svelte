<script lang="ts">
  import { untrack } from 'svelte';
import { PICKER_TEXT } from '../copy/pickerCopy';
  import { readParts, splitStamp, syncParts } from '../tracing/pickerModel';

  // One moment in UTC as two text boxes, a date and a time, bound to a `YYYY-MM-DDTHH:mm` string (the shape the screens already check).
  // An empty box clears the value; text that is not a real date or time keeps the previous good value, marks the box and goes back on leaving it.
  let {
    value = $bindable(''),
    label,
    disabled = false,
    readOnly = false,
    invalid = false,
    describedby,
    onchange
  }: {
    value?: string;
    /** The spoken name of the pair, for example "Release time". The boxes are named "Release time date" and "Release time time". */
    label: string;
    disabled?: boolean;
    readOnly?: boolean;
    invalid?: boolean;
    describedby?: string;
    /** Called after the person edits a box (not when the value is set from outside). */
    onchange?: () => void;
  } = $props();

  const uid = $props.id();
  const first = untrack(() => splitStamp(value));
  let date = $state(first.date);
  let time = $state(first.time);
  let badDate = $state(false);
  let badTime = $state(false);
  const off = $derived(disabled || readOnly);
  const problemId = $derived(badDate || badTime ? `${uid}-bad` : undefined);
  const describedAll = $derived([describedby, problemId].filter(Boolean).join(' ') || undefined);

  // A value set from outside (a quick pick, a reset) replaces the text; text the person is still fixing is left alone.
  $effect(() => {
    const outside = value;
    untrack(() => {
      const next = syncParts(date, time, outside);
      if (next.changed) {
        date = next.date;
        time = next.time;
        badDate = next.badDate;
        badTime = next.badTime;
      }
    });
  });

  function read() {
    const reading = readParts(date, time, value);
    badDate = reading.badDate;
    badTime = reading.badTime;
    if (reading.value !== value) value = reading.value;
    onchange?.();
  }
  function settleDate() {
    if (!badDate) return;
    date = splitStamp(value).date;
    badDate = false;
  }
  function settleTime() {
    if (!badTime) return;
    time = splitStamp(value).time;
    badTime = false;
  }
</script>

<span class="stamp">
  <input class="d" type="text" inputmode="text" autocomplete="off" spellcheck="false" maxlength="10" placeholder={PICKER_TEXT.datePlaceholder}
    aria-label={`${label} date`} aria-invalid={invalid || badDate ? true : undefined} aria-describedby={describedAll} disabled={off}
    value={date} oninput={(e) => { date = e.currentTarget.value; read(); }} onblur={settleDate} />
  <input class="t" type="text" inputmode="text" autocomplete="off" spellcheck="false" maxlength="5" placeholder={PICKER_TEXT.timePlaceholder}
    aria-label={`${label} time`} aria-invalid={invalid || badTime ? true : undefined} aria-describedby={describedAll} disabled={off}
    value={time} oninput={(e) => { time = e.currentTarget.value; read(); }} onblur={settleTime} />
  <span class="tz">{PICKER_TEXT.zone}</span>
  {#if badDate || badTime}<span class="err" id={problemId} role="alert">{badDate ? PICKER_TEXT.badDate : PICKER_TEXT.badTime}</span>{/if}
</span>

<style>
  .stamp { display: inline-flex; flex-wrap: wrap; align-items: center; gap: 6px 8px; min-width: 0; max-width: 100%; }
  input { width: auto; min-width: 0; min-height: 34px; padding: 4px 8px; font-size: 13px; font-variant-numeric: tabular-nums; font-family: var(--mono); }
  input.d { width: 118px; }
  input.t { width: 68px; }
  input[aria-invalid='true'] { border-color: var(--bad); }
  .tz { color: var(--text-2); font-size: 12px; font-family: var(--mono); }
  .err { flex: 1 1 100%; color: var(--bad); font-size: 12px; }
  @media (max-width: 1024px), (pointer: coarse) { input { min-height: 40px; } }
</style>
