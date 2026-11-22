<script lang="ts">
  import { untrack } from 'svelte';
  import FieldHelp from './FieldHelp.svelte';
  import { parseTime24, stepTime24, timeForQuery, TIME24_HINT, TIME24_PLACEHOLDER } from '../lib/time24';
  // `help` is an optional help key; the "i" button sits under the inputs, outside the legend so the group name stays "From" or "To".
  // The time box is a 24-hour text box (BL-495, BL-181), not the browser's own time box, so it never shows AM or PM. `onChange` gets the
  // date text and a time in the form `HH:MM:SS.mmm`; text that is not a 24-hour time is handed over as typed (or empty), which the caller treats as
  // an invalid value, as it did for the native box. `invalid` and `errorId` let the caller mark the box and point to its message.
  let {
    id,
    label,
    value,
    onChange,
    help,
    helpLabel = '',
    zone = '',
    invalid = false,
    errorId = ''
  }: { id: string; label: string; value: string; onChange: (datePart: string, timePart: string) => void; help?: string; helpLabel?: string; zone?: string; invalid?: boolean; errorId?: string } = $props();

  const committed = $derived(value.slice(11, 23));
  // What the box shows. It follows the query whenever the query changes (a quick range, the Times in zone); a half-typed value stays until committed.
  let text = $state('');
  let bad = $state(false);
  // A value that already reads the same as the query (typed "8:45" while the query holds 08:45:00.000) is left alone, so typing is never rewritten under the caret.
  $effect(() => { const now = committed; untrack(() => { if (parseTime24(text) !== now) { text = now; bad = false; } }); });
  let dateBox: HTMLInputElement | undefined = $state();
  // The date the person chose, which may not be in the query yet while the time is refused; it is sent with the next valid time instead of being lost.
  const dateNow = () => dateBox?.value ?? value.slice(0, 10);
  const hintId = $derived(`${id}-time-hint`);

  const commit = (typed: string) => {
    const parsed = parseTime24(typed);
    if (parsed === null) {
      bad = true;
      text = typed;
      onChange(dateNow(), timeForQuery(typed));
      return;
    }
    bad = false;
    text = parsed;
    onChange(dateNow(), parsed);
  };
  // Like the native box, a complete 24-hour time takes effect as it is typed; a half-typed or empty value waits until the box is left or Enter is pressed
  // (the change event), so the message does not flash on every key. A paste is committed and tidied at once.
  const onInput = (event: Event & { currentTarget: HTMLInputElement }) => {
    const typed = event.currentTarget.value;
    if ((event as unknown as InputEvent).inputType === 'insertFromPaste') return commit(typed);
    const parsed = parseTime24(typed);
    if (parsed === null) return;
    bad = false;
    onChange(dateNow(), parsed);
  };
  const onKey = (event: KeyboardEvent & { currentTarget: HTMLInputElement }) => {
    if (event.key !== 'ArrowUp' && event.key !== 'ArrowDown') return;
    if (event.altKey || event.ctrlKey || event.metaKey) return;
    const box = event.currentTarget;
    event.preventDefault();
    if (box.value.trim() === '') { box.value = '00:00:00.000'; box.setSelectionRange(0, 2); commit(box.value); return; }
    const stepped = stepTime24(box.value, box.selectionStart ?? 0, event.key === 'ArrowUp' ? 1 : -1);
    if (!stepped) return;
    box.value = stepped.text;
    box.setSelectionRange(stepped.select[0], stepped.select[1]);
    commit(stepped.text);
    queueMicrotask(() => box.setSelectionRange(stepped.select[0], stepped.select[1]));
  };
</script>

<fieldset class="date-time-field">
  <legend>{label}</legend>
  <div class="date-time-controls">
    <div class="date-time-control"><label for={`${id}-date`}>Date</label><input id={`${id}-date`} type="date" bind:this={dateBox} value={value.slice(0, 10)} onchange={(event) => onChange(event.currentTarget.value, timeForQuery(text))} /></div>
    <div class="date-time-control">
      <label for={`${id}-time`}>Time{#if zone}{' '}({zone}){/if}</label>
      <input
        id={`${id}-time`}
        class="time24"
        type="text"
        autocomplete="off"
        autocapitalize="off"
        spellcheck="false"
        placeholder={TIME24_PLACEHOLDER}
        bind:value={text}
        aria-invalid={bad || invalid ? 'true' : undefined}
        aria-describedby={`${hintId}${(bad || invalid) && errorId ? ` ${errorId}` : ''}`}
        onchange={(event) => commit(event.currentTarget.value)}
        oninput={onInput}
        onkeydown={onKey}
      />
      <span id={hintId} class="visually-hidden">24-hour time, hours 00 to 23. {TIME24_HINT}</span>
    </div>
  </div>
  {#if help}<div class="label-line date-time-help"><FieldHelp id={help} label={helpLabel} /></div>{/if}
</fieldset>
