<script lang="ts">
  import { DRILL_TEXT } from '../lib/drill/drillCopy';
  import {
    WINDOW_PRESETS,
    WINDOW_MIN,
    WINDOW_MAX,
    type CallWindow,
    backToCall,
    callInside,
    canStep,
    clampMinutes,
    coversOverview,
    presetOf,
    stepWindow,
    windowBounds
  } from '../lib/drill/callWindow';
  import { formatTime } from '../lib/time';

  type Shown = { api: boolean; iis: boolean; db: boolean };
  type SourceKey = keyof Shown;

  let {
    window: win,
    callStartMs,
    callEndMs,
    zone,
    shown,
    onCommit,
    onShown
  }: {
    window: CallWindow;
    callStartMs: number;
    callEndMs: number;
    zone: string;
    shown: Shown;
    onCommit: (w: CallWindow) => void;
    onShown: (s: Shown) => void;
  } = $props();

  const SOURCES: { key: SourceKey; label: string }[] = [
    { key: 'api', label: DRILL_TEXT.laneApi },
    { key: 'iis', label: DRILL_TEXT.laneIis },
    { key: 'db', label: DRILL_TEXT.laneDb }
  ];

  let customOpen = $state(false);
  let beforeValue = $state<number | null>(null);
  let afterValue = $state<number | null>(null);
  let rangeError = $state(false);

  const bounds = $derived(windowBounds(callStartMs, win));
  const clock = (ms: number): string => formatTime(ms, { zone, date: false, suffix: false, precision: 'minute' });
  const line = $derived(
    coversOverview(callStartMs, win)
      ? DRILL_TEXT.windowWhole
      : win.shiftMs !== 0
        ? DRILL_TEXT.windowLineMoved(clock(bounds.start), clock(bounds.end))
        : DRILL_TEXT.windowLine(clock(bounds.start), clock(bounds.end), win.before, win.after)
  );
  const inside = $derived(callInside(callStartMs, callEndMs, win));
  const onCount = $derived(SOURCES.filter((s) => shown[s.key]).length);

  function toggleCustom(): void {
    customOpen = !customOpen;
    if (customOpen) {
      beforeValue = win.before;
      afterValue = win.after;
      rangeError = false;
    }
  }

  function apply(): void {
    const before = clampMinutes(beforeValue);
    const after = clampMinutes(afterValue);
    if (before === null || after === null) {
      rangeError = true;
      return;
    }
    rangeError = false;
    onCommit({ before, after, shiftMs: win.shiftMs });
  }

  function setSource(key: SourceKey, on: boolean): void {
    onShown({ ...shown, [key]: on });
  }
</script>

<div class="scall-window" data-testid="scall-window" role="group" aria-label={DRILL_TEXT.windowLabel}>
  <div class="scall-window-row">
    {#each WINDOW_PRESETS as m (m)}
      <button
        type="button"
        class="chip"
        data-testid="scall-preset-{m}"
        aria-pressed={presetOf(win) === m}
        onclick={() => onCommit({ before: m, after: m, shiftMs: 0 })}
      >{DRILL_TEXT.windowPreset(m)}</button>
    {/each}
    <button
      type="button"
      class="chip"
      data-testid="scall-custom"
      aria-expanded={customOpen}
      onclick={toggleCustom}
    >{DRILL_TEXT.windowCustom}</button>
    <button
      type="button"
      class="secondary-button"
      data-testid="scall-earlier"
      disabled={!canStep(callStartMs, win, -1)}
      onclick={() => onCommit(stepWindow(callStartMs, win, -1))}
    >{DRILL_TEXT.earlier}</button>
    <button
      type="button"
      class="secondary-button"
      data-testid="scall-later"
      disabled={!canStep(callStartMs, win, 1)}
      onclick={() => onCommit(stepWindow(callStartMs, win, 1))}
    >{DRILL_TEXT.later}</button>
    {#if win.shiftMs !== 0}
      <button
        type="button"
        class="secondary-button"
        data-testid="scall-tocall"
        onclick={() => onCommit(backToCall(win))}
      >{DRILL_TEXT.backToCall}</button>
    {/if}
  </div>

  {#if customOpen}
    <div class="scall-custom-row">
      <label>
        <span>{DRILL_TEXT.windowBefore}</span>
        <input type="number" min={WINDOW_MIN} max={WINDOW_MAX} step="1" data-testid="scall-before" bind:value={beforeValue} />
      </label>
      <label>
        <span>{DRILL_TEXT.windowAfter}</span>
        <input type="number" min={WINDOW_MIN} max={WINDOW_MAX} step="1" data-testid="scall-after" bind:value={afterValue} />
      </label>
      <button type="button" class="secondary-button" data-testid="scall-apply" onclick={apply}>{DRILL_TEXT.windowApply}</button>
    </div>
    {#if rangeError}
      <p class="scall-range" role="alert" data-testid="scall-range">{DRILL_TEXT.windowRange}</p>
    {/if}
  {/if}

  <p class="scall-window-line" data-testid="scall-window-line" aria-live="polite">{line}</p>
  {#if !inside}
    <p class="scall-outside" data-testid="scall-outside">{DRILL_TEXT.legendOutside}</p>
  {/if}

  <fieldset class="scall-sources">
    <legend>{DRILL_TEXT.sourcesLabel}</legend>
    {#each SOURCES as s (s.key)}
      <label>
        <input
          type="checkbox"
          data-testid="scall-src-{s.key}"
          checked={shown[s.key]}
          disabled={shown[s.key] && onCount <= 1}
          onchange={(e) => setSource(s.key, e.currentTarget.checked)}
        />
        {s.label}
      </label>
    {/each}
  </fieldset>
</div>

<style>
  .scall-window { display: flex; flex-direction: column; gap: 8px; min-width: 0; }
  .scall-window-row, .scall-custom-row { display: flex; flex-wrap: wrap; align-items: flex-end; gap: 6px 8px; }
  .scall-custom-row label { display: flex; flex-direction: column; gap: 2px; font-size: 12px; color: var(--text-2); }
  .scall-custom-row input { width: 6rem; }
  .scall-window-row .secondary-button, .scall-custom-row .secondary-button { min-height: 28px; padding: 3px 10px; font-size: 13px; border-radius: var(--radius-sm); }
  .scall-window-line, .scall-outside, .scall-range { margin: 0; font-size: 13px; }
  .scall-window-line { color: var(--text-2); }
  .scall-outside, .scall-range { color: var(--text); }
  .scall-sources { display: flex; flex-wrap: wrap; gap: 4px 14px; margin: 0; padding: 0; border: 0; font-size: 13px; }
  .scall-sources legend { padding: 0; margin-bottom: 2px; font-size: 12px; font-weight: 500; color: var(--text-2); }
  .scall-sources label { display: inline-flex; align-items: center; gap: 6px; }
</style>
