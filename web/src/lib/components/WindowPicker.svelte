<script lang="ts">
  import { tick, untrack } from 'svelte';
  import { PICKER_TEXT, dayLabel, dialogLabel, partLabel, triggerLabel } from '../copy/pickerCopy';
  import TimeBox from './TimeBox.svelte';
  import {
    DAY_MS, daysInMonth, dayName, dayRole, draftFrom, draftWithDays, formatStamp, isDateText, monthGrid, parseStamp, parseTime, pickDay,
    rangeSpanText, rangeText, readDraft, readParts, shiftMonth, splitStamp, syncParts, type DraftText, type QuickRange
  } from '../tracing/pickerModel';

  // A time window in UTC as a start and an end, bound to `YYYY-MM-DDTHH:mm` strings (the shape the screens already check).
  //  - popover: a button that shows the window; it opens quick ranges, a calendar and four time boxes. Apply keeps the draft; Cancel, Escape and a click outside drop it.
  //  - inline: the date and the two times as text boxes beside each other, with a switch for a window that ends on another day.
  // Typing is free in both. An empty box clears its value; text that is not a real date or time keeps the previous good value, marks the box, and goes back on leaving it.
  let {
    start = $bindable(''),
    end = $bindable(''),
    label,
    variant = 'popover',
    hint,
    disabled = false,
    readOnly = false,
    invalidStart = false,
    invalidEnd = false,
    describedby,
    quickRanges,
    onquick,
    onchange,
    now = () => Date.now()
  }: {
    start?: string;
    end?: string;
    /** The name of the window, for example "Incident window (UTC)". It names the button, the popover and every box. */
    label: string;
    variant?: 'popover' | 'inline';
    /** One line under the picker, tied to it with aria-describedby. */
    hint?: string;
    disabled?: boolean;
    readOnly?: boolean;
    invalidStart?: boolean;
    invalidEnd?: boolean;
    describedby?: string;
    /** The quick ranges to list in the popover; read each time it opens. */
    quickRanges?: () => QuickRange[];
    /** Called after a quick range was applied to this picker, so the screen can set a second window from it. */
    onquick?: (quick: QuickRange) => void;
    /** Called after the person changes the window (not when the value is set from outside). */
    onchange?: () => void;
    /** The moment "today" is marked from; the demo data has its own clock. */
    now?: () => number;
  } = $props();

  const uid = $props.id();
  const off = $derived(disabled || readOnly);
  const hintId = $derived(hint ? `${uid}-hint` : undefined);
  const describedAll = $derived([describedby, hintId].filter(Boolean).join(' ') || undefined);

  /* ------------------------------------------------------------------ what the window says, in words */
  const summary = $derived(rangeText({ start, end }) || (start && end ? `${start.replace('T', ' ')} → ${end.replace('T', ' ')}` : ''));
  const span = $derived(rangeSpanText({ start, end }));

  /* ------------------------------------------------------------------ popover */
  let open = $state(false);
  let draft = $state<DraftText>({ startDate: '', startTime: '', endDate: '', endTime: '' });
  let picking = $state(false);
  let tried = $state(false);
  let month = $state({ year: 1970, month: 0 });
  let focusDate = $state('');
  let quicks = $state<QuickRange[]>([]);
  let shift = $state(0);
  let wrap = $state<HTMLDivElement>();
  let trigger = $state<HTMLButtonElement>();
  let dialog = $state<HTMLDivElement>();

  const reading = $derived(readDraft(draft));
  const grid = $derived(monthGrid(month.year, month.month));
  const today = $derived(formatStamp(now()).slice(0, 10));
  const dayState = $derived({ startDay: draft.startDate.trim(), endDay: picking ? '' : draft.endDate.trim(), picking });
  const tabDay = $derived.by(() => {
    if (grid.days.some((c) => c.date === focusDate)) return focusDate;
    if (grid.days.some((c) => c.date === dayState.startDay)) return dayState.startDay;
    return grid.days[0].date;
  });
  const badDate = (text: string) => text.trim() !== '' && !isDateText(text.trim());
  const badTime = (text: string) => text.trim() !== '' && parseTime(text) === null;
  const note = $derived.by((): { text: string; bad: boolean } => {
    if (badDate(draft.startDate) || badDate(draft.endDate)) return { text: PICKER_TEXT.badDate, bad: true };
    if (badTime(draft.startTime) || badTime(draft.endTime)) return { text: PICKER_TEXT.badTime, bad: true };
    if (reading.state === 'order') return { text: PICKER_TEXT.orderProblem, bad: true };
    if (reading.state === 'incomplete') return { text: PICKER_TEXT.incomplete, bad: tried };
    const range = reading.range;
    return { text: range ? `${rangeText(range)} · ${rangeSpanText(range)}` : '', bad: false };
  });

  async function openPopover() {
    draft = draftFrom({ start, end });
    picking = false;
    tried = false;
    quicks = quickRanges?.() ?? [];
    const anchor = splitStamp(start).date || splitStamp(end).date || formatStamp(now()).slice(0, 10);
    month = { year: +anchor.slice(0, 4), month: +anchor.slice(5, 7) - 1 };
    focusDate = '';
    shift = 0;
    open = true;
    await tick();
    place();
    dialog?.focus();
  }

  /** Keeps the popover on screen on a wide window; on a narrow one it sits in the page and needs no shifting. */
  function place() {
    if (!dialog || window.innerWidth <= 760) return;
    const box = dialog.getBoundingClientRect();
    let by = 0;
    if (box.right > window.innerWidth - 8) by = window.innerWidth - 8 - box.right;
    if (box.left + by < 8) by = 8 - box.left;
    shift = by;
  }

  function closePopover(refocus: boolean) {
    open = false;
    if (refocus) void tick().then(() => trigger?.focus());
  }

  function toggle() {
    if (open) closePopover(false);
    else void openPopover();
  }

  function commitDraft() {
    const range = reading.range;
    if (reading.state !== 'ok' || !range) {
      tried = true;
      return;
    }
    start = range.start;
    end = range.end;
    closePopover(true);
    onchange?.();
  }

  function applyQuick(quick: QuickRange) {
    if (!quick.range) return;
    start = quick.range.start;
    end = quick.range.end;
    closePopover(true);
    onquick?.(quick);
    onchange?.();
  }

  function outside(event: PointerEvent) {
    if (open && wrap && event.target instanceof Node && !wrap.contains(event.target)) closePopover(false);
  }

  function onDialogKey(event: KeyboardEvent) {
    // Enter in a text box applies the draft; it must never reach the form around the picker and start a read. Enter in one of the time
    // selects is left to the select itself (it confirms the highlighted choice); a select does not submit a form, so nothing is done for it.
    if (event.key === 'Enter' && event.target instanceof HTMLInputElement) {
      event.preventDefault();
      event.stopPropagation();
      commitDraft();
      return;
    }
    if (event.key !== 'Escape') return;
    event.preventDefault();
    event.stopPropagation();
    closePopover(true);
  }

  // Tab can move focus out of the non-modal popover, so Escape is also heard on the window and refocuses the trigger.
  function onWindowKey(event: KeyboardEvent) {
    if (open && event.key === 'Escape') closePopover(true);
  }

  function clickDay(date: string) {
    const next = pickDay(dayState, date);
    const days = draftWithDays(draft, next);
    draft = { ...days, startTime: days.startTime.trim() || '00:00', endTime: days.endTime.trim() || '23:59' };
    picking = next.picking;
    focusDate = date;
    tried = false;
  }

  function stepMonth(delta: number) {
    month = shiftMonth(month.year, month.month, delta);
  }

  const shiftDate = (date: string, days: number): string => formatStamp(parseStamp(date + 'T00:00') + days * DAY_MS).slice(0, 10);

  async function onDayKey(event: KeyboardEvent, date: string) {
    const steps: Record<string, number> = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -7, ArrowDown: 7 };
    let target = '';
    if (event.key in steps) target = shiftDate(date, steps[event.key]);
    else if (event.key === 'PageUp' || event.key === 'PageDown') {
      const moved = shiftMonth(+date.slice(0, 4), +date.slice(5, 7) - 1, event.key === 'PageUp' ? -1 : 1);
      const day = Math.min(+date.slice(8, 10), daysInMonth(moved.year, moved.month));
      target = `${String(moved.year).padStart(4, '0')}-${String(moved.month + 1).padStart(2, '0')}-${String(day).padStart(2, '0')}`;
    }
    if (!target) return;
    event.preventDefault();
    focusDate = target;
    const year = +target.slice(0, 4);
    const m = +target.slice(5, 7) - 1;
    if (year !== month.year || m !== month.month) month = { year, month: m };
    await tick();
    dialog?.querySelector<HTMLElement>(`[data-date="${target}"]`)?.focus();
  }

  function draftTyped(date?: string) {
    picking = false;
    tried = false;
    const text = date?.trim() ?? '';
    if (isDateText(text)) month = { year: +text.slice(0, 4), month: +text.slice(5, 7) - 1 };
  }

  /* ------------------------------------------------------------------ inline */
  const first = untrack(() => ({ s: splitStamp(start), e: splitStamp(end) }));
  let sd = $state(first.s.date);
  let st = $state(first.s.time);
  let ed = $state(first.e.date);
  let et = $state(first.e.time);
  let otherDay = $state(first.s.date !== '' && first.e.date !== '' && first.s.date !== first.e.date);
  let badSd = $state(false);
  let badSt = $state(false);
  let badEd = $state(false);
  let badEt = $state(false);

  // A value set from outside (a quick pick, "move the comparison period") replaces the text; text the person is still fixing is left alone.
  $effect(() => {
    const s0 = start;
    const e0 = end;
    if (variant !== 'inline') return;
    untrack(() => {
      const s1 = syncParts(sd, st, s0);
      if (s1.changed) {
        sd = s1.date;
        st = s1.time;
        badSd = s1.badDate;
        badSt = s1.badTime;
      }
      const e1 = syncParts(otherDay ? ed : sd, et, e0);
      if (e1.changed) {
        if (otherDay) ed = e1.date;
        et = e1.time;
        badEd = otherDay && e1.badDate;
        badEt = e1.badTime;
      }
      const a = splitStamp(s0).date;
      const b = splitStamp(e0).date;
      if (a && b && a !== b) otherDay = true;
      if (!otherDay && b) ed = b;
    });
  });

  function readInline() {
    const endDate = otherDay ? ed : isDateText(sd.trim()) ? sd.trim() : splitStamp(end).date;
    const s = readParts(sd, st, start);
    const e = readParts(endDate, et, end);
    badSd = s.badDate;
    badSt = s.badTime;
    badEd = otherDay && e.badDate;
    badEt = e.badTime;
    if (!otherDay) ed = endDate;
    if (s.value !== start) start = s.value;
    if (e.value !== end) end = e.value;
    onchange?.();
  }
  function toggleOtherDay() {
    if (!otherDay) ed = splitStamp(end).date || sd;
    readInline();
  }
  const settle = (which: 'sd' | 'st' | 'ed' | 'et') => {
    if (which === 'sd' && badSd) { sd = splitStamp(start).date; badSd = false; }
    if (which === 'st' && badSt) { st = splitStamp(start).time; badSt = false; }
    if (which === 'ed' && badEd) { ed = splitStamp(end).date; badEd = false; }
    if (which === 'et' && badEt) { et = splitStamp(end).time; badEt = false; }
  };
  const inlineProblem = $derived(badSd || badEd ? PICKER_TEXT.badDate : badSt || badEt ? PICKER_TEXT.badTime : '');
  const problemId = $derived(inlineProblem ? `${uid}-bad` : undefined);
  const inlineDescribed = $derived([describedAll, problemId].filter(Boolean).join(' ') || undefined);
</script>

<svelte:window onpointerdown={outside} onkeydown={onWindowKey} />

{#if variant === 'popover'}
  <div class="pkwrap" bind:this={wrap}>
    <button type="button" class="pkbtn" bind:this={trigger} aria-haspopup="dialog" aria-expanded={open} aria-controls={open ? `${uid}-dialog` : undefined}
      aria-label={triggerLabel(label, summary)} aria-describedby={describedAll} data-invalid={invalidStart || invalidEnd ? 'true' : undefined} disabled={off} onclick={toggle}>
      <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true"><rect x="1.5" y="3" width="13" height="11.5" rx="1" fill="none" stroke="currentColor" /><path d="M1.5 6.5h13M5 1.5v3M11 1.5v3" stroke="currentColor" fill="none" /></svg>
      <span class="pk-text">
        <b class="pk-main">{summary || PICKER_TEXT.choose}</b>
        <span class="pk-sub">{PICKER_TEXT.zone}{span ? ` · ${span}` : ''}</span>
      </span>
    </button>
    {#if open}
      <div class="pk" class:noquick={quicks.length === 0} id={`${uid}-dialog`} role="dialog" aria-label={dialogLabel(label)} tabindex="-1" bind:this={dialog} onkeydown={onDialogKey} style:--pk-shift={`${shift}px`}>
        {#if quicks.length > 0}
          <div class="pkc" role="group" aria-labelledby={`${uid}-quick`}>
            <p class="pk-title" id={`${uid}-quick`}>{PICKER_TEXT.quickHeading}</p>
            {#each quicks as quick (quick.id)}
              <button type="button" class="quiet-button qr" disabled={!quick.range} onclick={() => applyQuick(quick)}>{quick.label}</button>
            {/each}
          </div>
        {/if}
        <div class="pkc">
          <div class="cal-head">
            <button type="button" class="quiet-button" aria-label={PICKER_TEXT.previousMonth} onclick={() => stepMonth(-1)}>‹</button>
            <b aria-live="polite">{grid.title}</b>
            <button type="button" class="quiet-button" aria-label={PICKER_TEXT.nextMonth} onclick={() => stepMonth(1)}>›</button>
          </div>
          <div class="cal dow" aria-hidden="true">{#each PICKER_TEXT.weekdays as letter, i (i)}<span>{letter}</span>{/each}</div>
          <div class="cal">
            {#each { length: grid.lead } as _, i (i)}<span></span>{/each}
            {#each grid.days as cell (cell.date)}
              {@const role = dayRole(dayState, cell.date)}
              <button type="button" class="day" class:end={role === 'start' || role === 'end'} class:in={role === 'between'} class:today={cell.date === today}
                data-date={cell.date} tabindex={cell.date === tabDay ? 0 : -1} aria-pressed={role === 'start' || role === 'end'} aria-label={dayLabel(dayName(cell.date), role)}
                onclick={() => clickDay(cell.date)} onkeydown={(event) => void onDayKey(event, cell.date)}>{cell.day}</button>
            {/each}
          </div>
          <p class="hint">{picking ? PICKER_TEXT.hintSecond : PICKER_TEXT.hintFirst}</p>
        </div>
        <div class="pkc" role="group" aria-labelledby={`${uid}-times`}>
          <p class="pk-title" id={`${uid}-times`}>{PICKER_TEXT.timesHeading}</p>
          <div class="trow">
            <span class="rl">{PICKER_TEXT.start}</span>
            <span class="pair">
              <input class="d" type="text" inputmode="text" autocomplete="off" spellcheck="false" maxlength="10" placeholder={PICKER_TEXT.datePlaceholder} aria-label={partLabel(label, 'start date')}
                aria-invalid={badDate(draft.startDate) ? true : undefined} value={draft.startDate} oninput={(e) => { draft.startDate = e.currentTarget.value; draftTyped(draft.startDate); }} />
              <TimeBox label={partLabel(label, 'start time')} invalid={badTime(draft.startTime)} value={draft.startTime} onchange={(next) => { draft.startTime = next; draftTyped(); }} />
            </span>
          </div>
          <div class="trow">
            <span class="rl">{PICKER_TEXT.end}</span>
            <span class="pair">
              <input class="d" type="text" inputmode="text" autocomplete="off" spellcheck="false" maxlength="10" placeholder={PICKER_TEXT.datePlaceholder} aria-label={partLabel(label, 'end date')}
                aria-invalid={badDate(draft.endDate) || reading.state === 'order' ? true : undefined} value={draft.endDate} oninput={(e) => { draft.endDate = e.currentTarget.value; draftTyped(draft.endDate); }} />
              <TimeBox label={partLabel(label, 'end time')} invalid={badTime(draft.endTime) || reading.state === 'order'} value={draft.endTime} onchange={(next) => { draft.endTime = next; draftTyped(); }} />
            </span>
          </div>
          <p class="pk-note" class:err={note.bad} aria-live="polite" data-testid="picker-note">{note.text}</p>
          <div class="row">
            <button type="button" class="secondary-button" onclick={() => closePopover(true)}>{PICKER_TEXT.cancel}</button>
            <button type="button" class="primary-button" onclick={commitDraft}>{PICKER_TEXT.apply}</button>
          </div>
        </div>
      </div>
    {/if}
  </div>
  {#if hint}<span class="hint" id={hintId}>{hint}</span>{/if}
{:else}
  <div class="dtr">
    <input class="d" type="text" inputmode="text" autocomplete="off" spellcheck="false" maxlength="10" placeholder={PICKER_TEXT.datePlaceholder} aria-label={partLabel(label, 'start date')}
      aria-invalid={invalidStart || badSd ? true : undefined} aria-describedby={inlineDescribed} disabled={off} value={sd} oninput={(e) => { sd = e.currentTarget.value; readInline(); }} onblur={() => settle('sd')} />
    <TimeBox label={partLabel(label, 'start time')} invalid={invalidStart || badSt} describedby={inlineDescribed} disabled={off} value={st} onchange={(next) => { st = next; readInline(); }} />
    <span class="sep">{PICKER_TEXT.to}</span>
    {#if otherDay}
      <input class="d" type="text" inputmode="text" autocomplete="off" spellcheck="false" maxlength="10" placeholder={PICKER_TEXT.datePlaceholder} aria-label={partLabel(label, 'end date')}
        aria-invalid={invalidEnd || badEd ? true : undefined} aria-describedby={inlineDescribed} disabled={off} value={ed} oninput={(e) => { ed = e.currentTarget.value; readInline(); }} onblur={() => settle('ed')} />
    {/if}
    <TimeBox label={partLabel(label, 'end time')} invalid={invalidEnd || badEt} describedby={inlineDescribed} disabled={off} value={et} onchange={(next) => { et = next; readInline(); }} />
    <span class="tz">{PICKER_TEXT.zone}</span>
  </div>
  <label class="other"><input type="checkbox" bind:checked={otherDay} disabled={off} onchange={toggleOtherDay} /> {PICKER_TEXT.otherDay}</label>
  {#if inlineProblem}<span class="err" id={problemId} role="alert">{inlineProblem}</span>{/if}
  {#if hint}<span class="hint" id={hintId}>{hint}</span>{/if}
{/if}

<style>
  /* Popover */
  .pkwrap { position: relative; display: inline-block; max-width: 100%; }
  .pkbtn { display: flex; align-items: center; gap: 10px; max-width: 100%; padding: 8px 12px; border: 1px solid var(--control-line); border-radius: var(--radius-sm); background: var(--bg); color: var(--text); text-align: left; line-height: 1.3; }
  .pkbtn:hover:not(:disabled) { border-color: var(--text-2); }
  .pkbtn[aria-expanded='true'] { border-color: var(--accent); }
  .pkbtn[data-invalid='true'] { border-color: var(--bad); }
  .pkbtn:disabled { color: var(--text-2); }
  .pkbtn:focus-visible, .day:focus-visible, .qr:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .pk-text { display: grid; min-width: 0; }
  .pk-main { font-weight: 600; font-variant-numeric: tabular-nums; overflow-wrap: anywhere; }
  .pk-sub { color: var(--text-2); font-size: 12px; }
  .pk { position: absolute; z-index: 40; top: calc(100% + 6px); left: var(--pk-shift, 0); width: min(700px, 92vw); display: grid; grid-template-columns: 150px 250px minmax(0, 1fr); background: var(--surface); border: 1px solid var(--control-line); border-radius: var(--radius-sm); box-shadow: 0 12px 32px var(--scrim); color: var(--text); }
  .pk.noquick { grid-template-columns: 250px minmax(0, 1fr); width: min(560px, 92vw); }
  .pk:focus { outline: none; }
  .pk:focus-visible { outline: 2px solid var(--focus); outline-offset: -2px; }
  .pkc { display: grid; gap: var(--space-2); align-content: start; min-width: 0; padding: var(--space-3); }
  .pkc + .pkc { border-left: 1px solid var(--line); }
  .pk-title { margin: 0; color: var(--text-2); font-size: 12px; font-weight: 600; }
  .qr { text-align: left; }
  .cal-head { display: flex; align-items: center; justify-content: space-between; gap: var(--space-2); }
  .cal { display: grid; grid-template-columns: repeat(7, 1fr); gap: 2px; }
  .cal.dow { color: var(--text-2); font-size: 11px; text-align: center; }
  .day { aspect-ratio: 1; padding: 0; border: 0; border-radius: var(--radius-sm); background: transparent; color: var(--text); font-size: 13px; font-variant-numeric: tabular-nums; }
  .day:hover { background: var(--surface-2); }
  .day.in { background: color-mix(in srgb, var(--accent) 18%, transparent); }
  .day.end { background: var(--accent); color: var(--accent-ink); font-weight: 600; }
  .day.today { box-shadow: inset 0 0 0 1px var(--text-2); }
  .hint { margin: 0; color: var(--text-2); font-size: 12px; }
  .trow { display: grid; gap: 2px; }
  .rl { color: var(--text-2); font-size: 12px; }
  .pair { display: flex; flex-wrap: wrap; gap: 6px; }
  .pk-note { margin: 0; min-height: 2.6em; color: var(--text-2); font-size: 12px; font-variant-numeric: tabular-nums; }
  .row { display: flex; flex-wrap: wrap; gap: var(--space-2); }
  .pk input[type='text'], .dtr input[type='text'] { width: auto; min-width: 0; min-height: 34px; padding: 4px 8px; font-size: 13px; font-family: var(--mono); font-variant-numeric: tabular-nums; }
  .pk input.d, .dtr input.d { width: 118px; }

  input[aria-invalid='true'] { border-color: var(--bad); }
  .err { color: var(--bad); }

  /* Inline */
  .dtr { display: flex; flex-wrap: wrap; gap: 6px 8px; align-items: center; }
  .sep, .tz { color: var(--text-2); font-size: 12px; }
  .tz { font-family: var(--mono); }
  .other { display: flex; align-items: center; gap: 6px; margin-top: 4px; color: var(--text-2); font-size: 12px; font-weight: 500; }
  span.err { display: block; margin-top: 4px; font-size: 12px; }
  span.hint { display: block; margin-top: 4px; }

  @media (max-width: 1024px), (pointer: coarse) {
    .pk input[type='text'], .dtr input[type='text'] { min-height: 40px; }
    .day, .qr { min-height: 40px; }
  }
  /* A phone: the popover sits in the page under its button and the three columns stack. */
  @media (max-width: 760px) {
    .pkwrap { display: block; width: 100%; }
    .pkbtn { width: 100%; box-sizing: border-box; }
    .pk, .pk.noquick { position: static; width: auto; margin-top: 6px; grid-template-columns: minmax(0, 1fr); box-shadow: none; }
    .pkc + .pkc { border-left: 0; border-top: 1px solid var(--line); }
    .day { aspect-ratio: auto; min-height: 40px; }
  }
</style>
