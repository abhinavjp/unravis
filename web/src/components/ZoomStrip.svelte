<script lang="ts">
  import { brushEdgeText, rangeReadout, sameWindow, type StripModel, type ZoomWindow } from '../lib/zoom';
  import { brushCentre, brushDrag, brushFloor, brushHit, brushKey, type BrushMode } from '../lib/zoomBrush';
  import { zoomCopy } from '../lib/copy/zoomCopy';
  import HelpNote from './HelpNote.svelte';

  // The zoom strip above the time charts. It only draws the model it is given (lib/zoom.ts stripModel) and reports what was pressed.
  // It is always visible. Top row: Back to ..., the trail, Back, Zoom in, Zoom out, Reset. Under it, the zoomed window in words and, once
  // zoomed, "Investigate this window". Then the navigator: the whole period as a strip with the zoomed part marked. Drag the marked part to
  // move it, drag its edges to resize it, click the strip to centre on a point. Earlier and Later sit on its two ends.
  let { model, zone = 'UTC', announcement = '', investigate, backLabel = null, onBack, onEarlier, onLater, onZoomIn, onZoomOut, onReset, onJump, onBrush, onInvestigate, onWindowBack }: {
    model: StripModel; zone?: string; announcement?: string;
    investigate: { enabled: boolean; reason: string }; backLabel?: string | null;
    onBack: () => void; onEarlier: () => void; onLater: () => void; onZoomIn: () => void; onZoomOut: () => void; onReset: () => void; onJump: (index: number) => void;
    onBrush: (win: ZoomWindow) => void; onInvestigate: () => void; onWindowBack: () => void;
  } = $props();

  const uid = $props.id();
  const reasonId = 'investigate-reason-' + uid;
  const KEY_COMMIT_MS = 300;
  const DRAG_PX = 4;

  const bars = $derived(model.overview.bars);
  const step = $derived(bars.length ? 100 / bars.length : 0);
  const brush = $derived(model.brush);
  const root = $derived<ZoomWindow>({ start: brush.rootStart, end: brush.rootEnd });
  const view = $derived<ZoomWindow>({ start: brush.viewStart, end: brush.viewEnd });
  const rootWidth = $derived(Math.max(brush.rootEnd - brush.rootStart, 1));

  // While a drag or a run of key presses is under way the navigator shows this window; only one read happens, when it is committed.
  let preview = $state.raw<ZoomWindow | null>(null);
  const shown = $derived(preview ?? view);
  const left = $derived(((shown.start - brush.rootStart) / rootWidth) * 100);
  const right = $derived(((shown.end - brush.rootStart) / rootWidth) * 100);
  const previewText = $derived(preview ? rangeReadout(preview, zone) : '');
  // The handles' spoken values follow what is shown (the preview while a drag or key run is under way), like aria-valuenow does.
  const floor = $derived(brushFloor(root));
  const startText = $derived(brushEdgeText(shown.start, shown, zone));
  const endText = $derived(brushEdgeText(shown.end, shown, zone));
  let spoken = $state('');

  let track: HTMLElement | undefined;
  interface Drag { mode: BrushMode; start: ZoomWindow; fromMs: number; x0: number; moved: boolean; id: number; rect: DOMRect }
  let drag: Drag | null = null;
  let timer: ReturnType<typeof setTimeout> | undefined;

  $effect(() => () => clearTimeout(timer));

  const msAt = (clientX: number, rect: DOMRect) => brush.rootStart + Math.min(Math.max((clientX - rect.left) / rect.width, 0), 1) * rootWidth;
  const halfHit = () => (typeof matchMedia === 'function' && matchMedia('(pointer: coarse)').matches ? 12 : 6);

  /** The one place that reports a window to the page. An unchanged window reports nothing. */
  function emit(win: ZoomWindow) { if (!sameWindow(win, view)) onBrush(win); }
  /** Drop the preview and any drag, and report nothing. */
  function revert() { drag = null; clearTimeout(timer); timer = undefined; preview = null; }
  function flush() {
    clearTimeout(timer); timer = undefined;
    const win = preview; preview = null;
    if (win) emit(win);
  }

  function down(event: PointerEvent) {
    if (event.button !== 0 || !track) return;
    flush();
    const rect = track.getBoundingClientRect();
    if (rect.width <= 0) return;
    const at = (event.clientX - rect.left) / rect.width;
    let mode = brushHit(at, { left: (view.start - brush.rootStart) / rootWidth, right: (view.end - brush.rootStart) / rootWidth }, halfHit() / rect.width);
    // Nothing is left to move when the whole period is on screen, so a press in the middle starts a new range.
    if (mode === 'move' && view.start <= root.start && view.end >= root.end) mode = 'new';
    // The track does not move during a drag, so its box is read once here and not on every pointermove.
    drag = { mode, start: view, fromMs: msAt(event.clientX, rect), x0: event.clientX, moved: false, id: event.pointerId, rect };
    track.setPointerCapture?.(event.pointerId);
  }
  function move(event: PointerEvent) {
    if (!drag || event.pointerId !== drag.id || !track) return;
    if (!drag.moved && Math.abs(event.clientX - drag.x0) < DRAG_PX) return;
    drag.moved = true;
    preview = brushDrag(drag.mode, root, drag.start, drag.fromMs, msAt(event.clientX, drag.rect), brush.stepMs);
  }
  function up(event: PointerEvent) {
    const current = drag;
    if (!current || event.pointerId !== current.id) return;
    drag = null;
    track?.releasePointerCapture?.(event.pointerId);
    let next: ZoomWindow | null = null;
    if (current.moved) next = preview;
    else if (current.mode === 'new') next = brushCentre(root, current.start, current.fromMs, brush.stepMs);
    preview = null;
    if (next) emit(next);
  }
  function cancel(event: PointerEvent) { if (drag && event.pointerId === drag.id) revert(); }

  function keydown(handle: 'start' | 'end', event: KeyboardEvent) {
    if (drag || event.altKey || event.ctrlKey || event.metaKey) return;
    const next = brushKey(handle, event.key, root, shown, brush.stepMs);
    if (!next) return;
    event.preventDefault();
    preview = next;
    clearTimeout(timer);
    timer = setTimeout(flush, KEY_COMMIT_MS);
  }
  /** Escape in the middle of a drag, or before a key run has committed, puts the window back and does nothing else. */
  function escape(event: KeyboardEvent) {
    if (event.key !== 'Escape' || (!drag && !preview)) return;
    revert();
    event.preventDefault();
    event.stopPropagation();
  }

  function investigateNow() {
    if (investigate.enabled) { onInvestigate(); return; }
    spoken = '';
    queueMicrotask(() => { spoken = investigate.reason; });
  }
</script>

<svelte:window onkeydowncapture={escape} />

<section class="zoom-strip" aria-label={zoomCopy.heading}>
  <div class="zoom-row">
    <div class="zoom-trail">
      {#if backLabel}<button type="button" class="quiet-button window-back" onclick={onWindowBack}>{backLabel}</button>{/if}
      <nav aria-label={zoomCopy.breadcrumbLabel}>
        <ol class="crumbs">
          {#each model.crumbs as crumb (crumb.index)}
            <li>{#if crumb.current}<span class="crumb-current" aria-current="step">{crumb.label}</span>{:else}<button type="button" class="crumb-link" onclick={() => onJump(crumb.index)}>{crumb.label}</button>{/if}</li>
          {/each}
        </ol>
      </nav>
    </div>
    <div class="zoom-buttons">
      <button type="button" class="quiet-button" disabled={!model.canBack} onclick={onBack}>{zoomCopy.back}</button>
      <button type="button" class="quiet-button" disabled={!model.canIn} onclick={onZoomIn}>{zoomCopy.zoomIn}</button>
      <button type="button" class="quiet-button" disabled={!model.canOut} onclick={onZoomOut}>{zoomCopy.zoomOut}</button>
      <button type="button" class="quiet-button" disabled={!model.canReset} onclick={onReset}>{zoomCopy.reset}</button>
    </div>
  </div>
  {#if model.canBack}<div class="zoom-help"><HelpNote id="drill-back" /></div>{/if}

  <div class="zoom-stage">
    <p class="zoom-line"><strong class="level">{model.levelName}</strong> {#if preview}<span class="readout">{previewText}</span>{:else}{model.rangeLine} {model.intervalLine}{/if}</p>
    {#if !model.isRoot}
      <div class="investigate-chip">
        <button type="button" class="secondary-button investigate" title={zoomCopy.investigateTitle} aria-disabled={!investigate.enabled} aria-describedby={investigate.enabled ? undefined : reasonId} onclick={investigateNow}>{zoomCopy.investigate}</button>
        {#if !investigate.enabled}<span class="chip-reason" id={reasonId}>{investigate.reason}</span>{/if}
      </div>
    {/if}
  </div>
  {#if !model.isRoot}<div class="zoom-help"><HelpNote id="drill-investigate" /></div>{/if}
  {#if model.message}<p class="zoom-message" data-tone={model.messageTone}>{model.message}</p>{/if}
  {#if model.notice}<p class="zoom-message" data-tone="info">{model.notice}</p>{/if}

  <div class="navigator">
    <HelpNote id="drill-navigator" />
    <div class="nav-row">
      <button type="button" class="quiet-button nav-earlier" disabled={!model.canEarlier} onclick={onEarlier}>{zoomCopy.earlier}</button>
      <div class="nav-track" role="group" aria-label={model.overview.alt} bind:this={track} onpointerdown={down} onpointermove={move} onpointerup={up} onpointercancel={cancel}>
        <svg class="overview" viewBox="0 0 100 14" preserveAspectRatio="none" aria-hidden="true" focusable="false">
          {#each bars as value, index (index)}<rect class="bar" x={index * step} y={14 - value * 12} width={Math.max(step - 0.3, 0.2)} height={value * 12} />{/each}
        </svg>
        <div class="nav-body" style:left={left + '%'} style:width={Math.max(right - left, 0) + '%'}></div>
        <div class="nav-handle" role="slider" tabindex="0" aria-label={zoomCopy.brushStart} aria-orientation="horizontal" aria-valuemin={brush.rootStart} aria-valuemax={Math.max(shown.end - floor, brush.rootStart)} aria-valuenow={shown.start} aria-valuetext={startText} style:left={left + '%'} onkeydown={(event) => keydown('start', event)} onblur={flush}></div>
        <div class="nav-handle" role="slider" tabindex="0" aria-label={zoomCopy.brushEnd} aria-orientation="horizontal" aria-valuemin={Math.min(shown.start + floor, brush.rootEnd)} aria-valuemax={brush.rootEnd} aria-valuenow={shown.end} aria-valuetext={endText} style:left={right + '%'} onkeydown={(event) => keydown('end', event)} onblur={flush}></div>
      </div>
      <button type="button" class="quiet-button nav-later" disabled={!model.canLater} onclick={onLater}>{zoomCopy.later}</button>
    </div>
    {#if !model.overview.hasData}<p class="zoom-note overview-empty">{zoomCopy.overviewEmpty}</p>{/if}
  </div>

  <p class="zoom-note hint">{model.hint}</p>
  {#if model.asOfLine}<p class="zoom-note">{model.asOfLine}</p>{/if}
  {#if model.scopeLine}<p class="zoom-note">{model.scopeLine}</p>{/if}
  {#if model.referenceLine}<p class="zoom-note">{model.referenceLine}</p>{/if}
  <p class="zoom-note">{zoomCopy.howTo}</p>
  <p class="sr-only" role="status" aria-live="polite">{model.rangeLine} {model.message} {model.notice} {announcement} {previewText} {spoken}</p>
</section>

<style>
  .zoom-strip { display: grid; gap: var(--space-2); margin: 0 0 var(--space-3); padding: var(--space-2) 0 var(--space-3); border-top: var(--hairline-width) solid var(--hairline); border-bottom: var(--hairline-width) solid var(--hairline); font-size: var(--fs-3); }
  .zoom-row { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: var(--space-2) var(--space-4); }
  .zoom-trail { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-1) var(--space-3); min-width: 0; }
  .crumbs { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-1); margin: 0; padding: 0; list-style: none; }
  .crumbs li + li::before { content: '›'; margin-right: var(--space-1); color: var(--text-2); }
  .crumb-link { padding: 0; border: 0; background: none; color: var(--accent); cursor: pointer; font: inherit; text-decoration: underline; }
  .crumb-current { font-weight: 600; color: var(--text); }
  .zoom-buttons { display: flex; flex-wrap: wrap; gap: var(--space-1); }
  .zoom-buttons button:disabled, .nav-row button:disabled { opacity: .45; cursor: not-allowed; }
  .zoom-stage { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: var(--space-2) var(--space-4); }
  .zoom-line, .zoom-note, .zoom-message { margin: 0; }
  .zoom-line { min-width: 0; overflow-wrap: anywhere; }
  .readout { font-family: var(--mono); font-variant-numeric: tabular-nums; color: var(--text); }
  .investigate-chip { display: flex; flex-wrap: wrap; align-items: center; justify-content: flex-end; gap: var(--space-2); margin-inline-start: auto; }
  .investigate[aria-disabled='true'] { opacity: .6; cursor: not-allowed; }
  .chip-reason { color: var(--text-2); font-size: var(--fs-2); }
  .navigator { display: grid; gap: var(--space-2); }
  .nav-row { display: grid; grid-template-columns: auto minmax(0, 1fr) auto; grid-template-areas: 'earlier track later'; align-items: center; gap: var(--space-2); }
  .nav-earlier { grid-area: earlier; }
  .nav-later { grid-area: later; }
  .nav-row .quiet-button { min-height: 24px; }
  /* The track has side margins so the handles, which straddle its ends, never reach past the strip. */
  .nav-track { position: relative; grid-area: track; height: 34px; margin: 0 12px; touch-action: pan-y; user-select: none; cursor: pointer; }
  .overview { position: absolute; inset: 0; width: 100%; height: 100%; display: block; background: var(--surface-2); border-radius: var(--radius-sm); }
  .overview .bar { fill: var(--text-2); opacity: .55; }
  .nav-body { position: absolute; top: 0; bottom: 0; min-width: 2px; box-sizing: border-box; background: color-mix(in srgb, var(--accent) 20%, transparent); border: var(--hairline-width) solid var(--accent); cursor: grab; }
  .nav-body:active { cursor: grabbing; }
  .nav-handle { position: absolute; top: 0; bottom: 0; width: 12px; margin-left: -6px; cursor: ew-resize; touch-action: pan-y; }
  /* The grip is a small outlined bar on the edge: no solid accent fill outside primary buttons. */
  .nav-handle::before { content: ''; position: absolute; top: 4px; bottom: 4px; left: 50%; width: 4px; margin-left: -2px; box-sizing: border-box; background: var(--surface); border: var(--hairline-width) solid var(--accent); border-radius: var(--radius-sm); }
  .nav-handle:focus-visible { outline: 2px solid var(--focus); outline-offset: 0; }
  .zoom-note { color: var(--text-2); font-size: var(--fs-2); }
  .hint { color: var(--text-2); }
  .zoom-message[data-tone='warn'] { color: var(--text); font-weight: 600; }
  .zoom-message[data-tone='error'] { color: var(--bad); font-weight: 600; }
  .sr-only { position: absolute; width: 1px; height: 1px; margin: -1px; padding: 0; overflow: hidden; clip-path: inset(50%); white-space: nowrap; border: 0; }
  /* Touch: the handles get a 24px hit area (the grip stays the same size). */
  @media (pointer: coarse) {
    .nav-track { margin: 0 var(--space-3); }
    .nav-handle { width: 24px; margin-left: -12px; }
    .nav-row .quiet-button { min-height: 32px; }
  }
  @media (max-width: 520px) {
    .nav-row { grid-template-columns: 1fr 1fr; grid-template-areas: 'track track' 'earlier later'; }
    .nav-later { justify-self: end; }
    .nav-earlier { justify-self: start; }
    .investigate-chip { margin-inline-start: 0; justify-content: flex-start; }
  }
</style>
