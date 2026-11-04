<script lang="ts">
  // The Investigation Timeline chart (WP-17b): one stacked row per lane (API audit, SQL, IIS) on one shared,
  // server-floored UTC axis. Bars are exactly the buckets the server returned; nothing here sums, estimates
  // or recomputes the axis. WP-17c adds hover/focus text; WP-61 reads the bucket selector's onSelect.
  import type { TracingLane, TracingLaneKey, TracingResponse } from '../lib/tracing/model';
  import {
    LANE_ASK, LANE_NAME, LANE_ORDER, MIXED_NOTE, PANEL_TITLE,
    anyLaneCutShort, axisCaption, barHeightPct, barMax, barsFor, hasAnyData, isLaneVisible, laneCutShort, laneDrawsBars, laneNote, laneNoteTone, laneStatusLine,
    outsideSlots, showsProvenance, slotGrid, slotOutside, slotStartMs, truncatedNote, type Bar
  } from '../lib/tracing/lanes';
  import { formatTime } from '../lib/time';
  import { needsYear } from '../lib/chartTime';
  import { formatDuration } from '../lib/format';
  import { TOOLTIP_TEXT, barTipLines, formatCount, isTouchPointer, laneHeadHint } from '../lib/tracing/tooltip';
  import { WAIT_TEXT } from '../lib/copy/waitCopy';
  import {
    CANVAS_TEXT, MIN_ZOOM_SLOTS, PERCENTILES, PERCENTILE_LABEL, absoluteSlot, clampRange, edgeSlot, fullRange, hasLatency,
    isZoomed, latencySegments, rangeFromDrag, stepSlot, axisTicks, zoomFocus, zoomIn, zoomOut, zoomedText,
    type Percentile, type ZoomRange
  } from '../lib/tracing/canvas';

  let { response, zone, onSelect, visible = {} }: {
    response: TracingResponse;
    zone: string;
    onSelect?: (slotIndex: number) => void;
    // A row is hidden only when its flag is exactly false (WP-23); WP-11 passes the toolbar's choices.
    visible?: Partial<Record<TracingLaneKey, boolean>>;
  } = $props();

  const SWATCH_STYLE: Record<TracingLaneKey, string> = {
    audit: 'background: var(--lane-audit)',
    sql: 'background: var(--lane-sql)',
    iis: 'background: var(--lane-iis)'
  };

  const grid = $derived(slotGrid(response.window));
  // The year is printed when the window is not in the current year or spans two years; otherwise the compact form stays.
  const showYear = $derived(grid ? needsYear(grid.firstMs, grid.firstMs + grid.n * grid.widthMs - 1, zone) : false);
  const at = (value: string | number): string => formatTime(value, { zone, precision: 'minute', year: showYear });
  const orderedLanes = $derived(
    LANE_ORDER.filter((key) => isLaneVisible(visible, key))
      .map((key) => response.lanes.find((lane) => lane.key === key))
      .filter((lane): lane is TracingLane => !!lane)
  );
  const anyHidden = $derived(Object.values(visible).some((flag) => flag === false));

  // Each lane's bars, built once per response: drawing, tooltips and the zoom focus all read this map.
  const laneBars = $derived.by(() => {
    const map = new Map<TracingLaneKey, { bars: Bar[]; byIndex: Map<number, Bar> }>();
    if (grid) {
      for (const lane of response.lanes) {
        const bars = barsFor(grid, lane.buckets);
        map.set(lane.key, { bars, byIndex: new Map(bars.map((bar) => [bar.index, bar])) });
      }
    }
    return map;
  });
  function drawsBars(lane: TracingLane): boolean {
    return !!grid && laneDrawsBars(lane, laneBars.get(lane.key)?.bars.length ?? 0);
  }
  const hasData = $derived(hasAnyData(grid, response.lanes));

  // Zoom (WP-23): every slot index in this file is ABSOLUTE into the full grid; `range` only picks which ones are drawn.
  // The zoom belongs to one response, so a new response starts unzoomed without an effect.
  let zoomState = $state.raw<{ response: TracingResponse; range: ZoomRange } | null>(null);
  const zoom = $derived(zoomState && zoomState.response === response ? zoomState.range : null);
  const range = $derived<ZoomRange>(grid ? (zoom ? clampRange(zoom, grid.n) : fullRange(grid.n)) : { from: 0, to: 0 });
  const shown = $derived(range.to - range.from);
  const zoomed = $derived(grid ? isZoomed(range, grid.n) : false);
  const slotIndexes = $derived(Array.from({ length: shown }, (_, k) => range.from + k));
  const widthPct = $derived(shown > 0 ? 100 / shown : 0);
  function leftPct(index: number): number { return (index - range.from) * widthPct; }
  function inRange(index: number): boolean { return index >= range.from && index < range.to; }

  // Latency line (WP-23c): the audit lane's per-bucket P50 or P95, drawn as plain SVG over the bars. Null buckets leave a gap.
  let percentile = $state<Percentile>('p95');
  const auditLane = $derived(response.lanes.find((lane) => lane.key === 'audit'));
  const latencyAvailable = $derived(!!auditLane && hasLatency(auditLane.buckets, percentile));
  const latency = $derived(grid && auditLane ? latencySegments(auditLane.buckets, grid, range, percentile) : { segments: [] as string[], max: 0 });

  // Axis: the grey "outside your window" dashes and the time labels, thinned to the measured plot width so they never overlap.
  const outside = $derived(grid ? outsideSlots(grid, range.from, range.to) : []);
  let plotW = $state(0);
  const tickLabelChars = $derived(grid && shown > 0 ? at(slotStartMs(grid, range.from)).length : 0);
  const ticks = $derived(axisTicks(shown, plotW, tickLabelChars, grid && shown > 0 ? { startMs: slotStartMs(grid, range.from), widthMs: grid.widthMs, zone } : undefined));
  const caption = $derived(grid ? axisCaption(zone, response.window.bucketSeconds, outside.length > 0) : '');

  // The selected bucket; one that scrolls out of the zoomed range is dropped, not left pointing at a hidden bar.
  let selectedSlot = $state<number | null>(null);
  const current = $derived(selectedSlot != null && inRange(selectedSlot) ? selectedSlot : null);

  function clampIndex(index: number): number {
    return Math.min(range.to - 1, Math.max(range.from, index));
  }
  function isOutside(slot: number): boolean { return !!grid && slotOutside(grid, slot); }

  function applyZoom(next: ZoomRange | null) {
    zoomState = next ? { response, range: next } : null;
    if (selectedSlot != null && next && (selectedSlot < next.from || selectedSlot >= next.to)) selectedSlot = null;
  }
  function doZoomIn() {
    if (!grid) return;
    const drawn = orderedLanes.flatMap((lane) => (drawsBars(lane) ? laneBars.get(lane.key)?.bars ?? [] : []));
    applyZoom(zoomIn(range, grid.n, zoomFocus(range, current, drawn)));
  }
  function doZoomOut() {
    if (!grid) return;
    const next = zoomOut(range, grid.n);
    applyZoom(isZoomed(next, grid.n) ? next : null);
  }
  function resetZoom() { applyZoom(null); }

  function selectAt(index: number) {
    selectedSlot = clampIndex(index);
    onSelect?.(selectedSlot);
  }

  function onOverlayKey(event: KeyboardEvent) {
    if (!grid) return;
    // Browser shortcuts (page zoom with Ctrl or Cmd and + - 0, Alt navigation) are not ours.
    if (event.ctrlKey || event.metaKey || event.altKey) return;
    if (event.key === 'Escape' && dragFrom != null) { event.preventDefault(); event.stopPropagation(); cancelDrag(); return; }
    if (event.key === 'ArrowLeft') { event.preventDefault(); selectedSlot = stepSlot(range, current ?? range.from, -1, isOutside); }
    else if (event.key === 'ArrowRight') { event.preventDefault(); selectedSlot = stepSlot(range, current ?? range.from, 1, isOutside); }
    else if (event.key === 'Home') { event.preventDefault(); selectedSlot = edgeSlot(range, 'first', isOutside); }
    else if (event.key === 'End') { event.preventDefault(); selectedSlot = edgeSlot(range, 'last', isOutside); }
    else if (event.key === 'Enter') { event.preventDefault(); if (current != null) onSelect?.(current); }
    else if (event.key === 'Escape' && tipSlot != null) { event.preventDefault(); event.stopPropagation(); hideTip(); return; }
    else if (event.key === '+' || event.key === '=') { event.preventDefault(); doZoomIn(); return; }
    else if (event.key === '-' || event.key === '_') { event.preventDefault(); doZoomOut(); return; }
    else if (event.key === '0') { event.preventDefault(); resetZoom(); return; }
    else return;
    if (event.key !== 'Enter') showKeyboardTip();
  }

  // Hover and keyboard text (WP-17c): one shared tooltip box. A pointer shows the lane under it; the keyboard
  // shows every lane that draws bars for the selected bucket. All lines come from tooltip.ts as plain text.
  let tipSlot = $state<number | null>(null);
  let tipLane = $state<TracingLaneKey | null>(null);
  let tipFromKeyboard = $state(false);
  // A tooltip left on screen by a tap (finger or pen): the same bar tapped again opens the analysis. A mouse click never sets this.
  let tipFromTouch = $state(false);
  let tipStyle = $state('');
  let overlayEl: HTMLDivElement | undefined;
  let tipEl: HTMLDivElement | undefined;

  function hideTip() { tipSlot = null; tipLane = null; tipFromKeyboard = false; tipFromTouch = false; }

  function linesFor(lane: TracingLane, slot: number): string[] {
    if (!grid) return [];
    // A lane that draws nothing says why instead of claiming "no events in this bucket".
    if (!drawsBars(lane)) {
      const status = laneStatusLine(lane);
      return status ? [status] : [];
    }
    const bar = laneBars.get(lane.key)?.byIndex.get(slot);
    const start = slotStartMs(grid, slot);
    return barTipLines({
      key: lane.key,
      startText: at(start),
      endText: at(start + grid.widthMs),
      zoneName: zone,
      count: bar?.count ?? 0,
      errors: bar?.errors ?? 0,
      origin: lane.origin,
      asOfText: showsProvenance(lane) ? at(lane.asOf) : '',
      note: lane.state === 'incomplete' || laneCutShort(lane) ? laneNote(lane) : ''
    });
  }

  const tipGroups = $derived.by(() => {
    if (tipSlot == null || !grid) return [];
    const slot = tipSlot;
    const lanes = tipLane
      ? orderedLanes.filter((lane) => lane.key === tipLane)
      : orderedLanes.filter((lane) => drawsBars(lane));
    return lanes.map((lane) => ({ key: lane.key, name: LANE_NAME[lane.key], lines: linesFor(lane, slot) }));
  });
  // The tap hint is only worth saying when the tapped bar has events to open.
  const tipOpenable = $derived(
    tipFromTouch && tipSlot != null && orderedLanes.some((lane) => drawsBars(lane) && (laneBars.get(lane.key)?.byIndex.get(tipSlot as number)?.count ?? 0) > 0)
  );
  // Screen readers do not re-read a changed tooltip description, so keyboard moves are also spoken through one polite live region.
  const liveText = $derived(tipFromKeyboard ? tipGroups.map((group) => group.name + '. ' + group.lines.join('. ')).join(' ') : '');

  function showKeyboardTip() {
    if (!grid || current == null) return;
    tipLane = null;
    tipSlot = current;
    tipFromKeyboard = true;
    tipStyle = `left:${Math.min(80, leftPct(current))}%;top:4px`;
  }

  // Drag across the chart to zoom into those bars (WP-23). The overlay captures the pointer on press, so the drag survives
  // leaving it; clicks and drags share the overlay's own handlers (no per-bucket click closures).
  let dragFrom = $state<number | null>(null);
  let dragTo = $state<number | null>(null);
  let suppressClick = false;
  // Whether the press that precedes the next click came from a finger or a pen.
  let downTouch = false;
  const coarsePointer = (): boolean => typeof matchMedia === 'function' && matchMedia('(pointer: coarse)').matches;

  function slotAt(event: { clientX: number }): number {
    if (!overlayEl) return -1;
    const rect = overlayEl.getBoundingClientRect();
    if (rect.width <= 0) return -1;
    return absoluteSlot(range, (event.clientX - rect.left) / rect.width);
  }

  function onOverlayDown(event: PointerEvent) {
    if (!grid || event.button !== 0 || !overlayEl) return;
    suppressClick = false;
    downTouch = isTouchPointer(event.pointerType, coarsePointer());
    const slot = slotAt(event);
    if (slot < 0) return;
    dragFrom = slot;
    dragTo = slot;
    // A tap keeps the tooltip it is about to confirm; a drag clears it on the first move.
    if (!downTouch) hideTip();
    try { overlayEl.setPointerCapture(event.pointerId); } catch { /* a synthetic pointer cannot be captured */ }
  }

  function endDrag(commit: boolean) {
    flushMove();
    if (dragFrom != null && dragTo != null && commit && grid && dragFrom !== dragTo) {
      applyZoom(rangeFromDrag(grid.n, dragFrom, dragTo));
      suppressClick = true;
    }
    dragFrom = null;
    dragTo = null;
  }

  // Escape during a drag drops the selection: nothing zooms, and the click that follows the release is swallowed.
  function cancelDrag() {
    if (dragFrom == null) return;
    cancelMove();
    dragFrom = null;
    dragTo = null;
    suppressClick = true;
  }
  function onWindowKey(event: KeyboardEvent) {
    if (event.key === 'Escape' && dragFrom != null) { event.preventDefault(); cancelDrag(); }
  }

  function onOverlayClick(event: MouseEvent) {
    const touch = downTouch;
    downTouch = false;
    if (suppressClick) { suppressClick = false; return; }
    if (!grid) return;
    const column = (event.target as Element | null)?.closest?.('.column') as HTMLElement | null;
    const fromColumn = column ? Number(column.dataset.slot) : Number.NaN;
    const slot = Number.isInteger(fromColumn) ? fromColumn : slotAt(event);
    if (slot < 0) return;
    // Finger or pen: the first tap on a bar shows its counts and keeps them; tapping the same bar again opens the analysis.
    if (touch && !(tipFromTouch && tipSlot === slot)) { showTouchTip(slot, event); return; }
    selectAt(slot);
  }

  function showTouchTip(slot: number, event: { clientX: number; clientY: number }) {
    selectedSlot = clampIndex(slot);
    tipSlot = slot;
    tipLane = laneAtY(event.clientY);
    tipFromKeyboard = false;
    tipFromTouch = true;
    placeTip(event.clientX, event.clientY);
  }

  function laneAtY(y: number): TracingLaneKey | null {
    let hovered: TracingLaneKey | null = null;
    overlayEl?.parentElement?.querySelectorAll<HTMLElement>('.panel[data-lane]').forEach((panel) => {
      const box = panel.getBoundingClientRect();
      if (y >= box.top && y <= box.bottom) hovered = panel.dataset.lane as TracingLaneKey;
    });
    return hovered;
  }

  function placeTip(clientX: number, clientY: number) {
    if (!overlayEl) return;
    const rect = overlayEl.getBoundingClientRect();
    const tipWidth = tipEl?.offsetWidth || 220;
    const x = Math.min(clientX - rect.left, rect.width - tipWidth);
    tipStyle = `left:${Math.max(0, x)}px;top:${Math.max(0, clientY - rect.top + 14)}px`;
  }

  // Pointer moves are coalesced to one pass per frame: the last position wins, so layout is read once per frame, not once per event.
  let frame = 0;
  let pendingMove: { x: number; y: number } | null = null;

  function onOverlayMove(event: PointerEvent) {
    if (!grid || !overlayEl) return;
    pendingMove = { x: event.clientX, y: event.clientY };
    if (!frame) frame = requestAnimationFrame(() => { frame = 0; processMove(); });
  }
  function flushMove() {
    if (frame) { cancelAnimationFrame(frame); frame = 0; }
    processMove();
  }
  function cancelMove() {
    if (frame) cancelAnimationFrame(frame);
    frame = 0;
    pendingMove = null;
  }
  $effect(() => cancelMove);

  function processMove() {
    const point = pendingMove;
    pendingMove = null;
    if (!point || !grid || !overlayEl) return;
    const rect = overlayEl.getBoundingClientRect();
    if (rect.width <= 0) return;
    const slot = absoluteSlot(range, (point.x - rect.left) / rect.width);
    if (dragFrom != null) {
      if (slot >= 0) dragTo = slot;
      hideTip();
      return;
    }
    if (slot < 0) { hideTip(); return; }
    tipSlot = slot;
    tipLane = laneAtY(point.y);
    tipFromKeyboard = false;
    tipFromTouch = false;
    placeTip(point.x, point.y);
  }

  // A finger lifting leaves the chart; the tooltip it just opened stays until another tap, a drag, Escape or leaving the chart by focus.
  function onOverlayLeave(event: PointerEvent) {
    if (tipFromTouch && event.pointerType !== 'mouse') return;
    cancelMove();
    hideTip();
  }
</script>

<svelte:window onkeydown={onWindowKey} />

<div class="tracing-chart">
  {#if response.mixedConsistency}
    <div role="note" class="notice">
      <p class="mixed-title"><strong>{MIXED_NOTE.title}</strong></p>
      <p>{MIXED_NOTE.body}</p>
      <p class="muted">{MIXED_NOTE.fixed}</p>
    </div>
  {/if}

  {#if anyLaneCutShort(response.lanes)}
    <p role="status" class="notice cut-short" data-testid="lanes-cut-short">{WAIT_TEXT.lanesCutShort}</p>
  {/if}

  {#if grid && hasData}
    <div class="canvas-bar" role="toolbar" aria-label={CANVAS_TEXT.ariaLabel}>
      <button type="button" class="secondary-button" onclick={doZoomIn} disabled={shown <= Math.min(MIN_ZOOM_SLOTS, grid.n)}>{CANVAS_TEXT.zoomIn}</button>
      <button type="button" class="secondary-button" onclick={doZoomOut} disabled={!zoomed}>{CANVAS_TEXT.zoomOut}</button>
      <button type="button" class="secondary-button" onclick={resetZoom} disabled={!zoomed}>{CANVAS_TEXT.resetZoom}</button>
      <span class="canvas-group" role="group" aria-label={CANVAS_TEXT.latencyLabel}>
        {#each PERCENTILES as option (option)}
          <button type="button" class="secondary-button" aria-pressed={percentile === option} onclick={() => { percentile = option; }}>{PERCENTILE_LABEL[option]}</button>
        {/each}
      </span>
      {#if auditLane && !latencyAvailable}<small class="muted">{CANVAS_TEXT.noLatency}</small>{/if}
      <span class="zoom-note" aria-live="polite">{zoomed ? zoomedText(shown, grid.n) : ''}</span>
      <small class="muted">{CANVAS_TEXT.dragHint}</small>
    </div>
  {/if}

  {#if anyHidden && orderedLanes.length === 0}
    <p role="status" class="lane-note">{CANVAS_TEXT.allHidden}</p>
  {/if}

  {#if !grid && response.lanes.length > 0}
    <p role="status" class="lane-note">{CANVAS_TEXT.noAxis}</p>
  {/if}

  <div class="lanes-grid">
    {#each orderedLanes as lane, laneIndex (lane.key)}
      {@const allBars = laneBars.get(lane.key)?.bars ?? []}
      {@const bars = allBars.filter((bar) => inRange(bar.index))}
      {@const max = barMax(bars)}
      {@const drawBars = drawsBars(lane)}
      <!-- Not a tab stop: it has no action. The name and the hint stay available to screen readers, and the hint shows on hover. -->
      <div class="lane-label" role="group" aria-label={LANE_NAME[lane.key]} aria-describedby="tc-hint-{lane.key}" data-truncated={lane.truncated ? 'true' : undefined} style="--row:{laneIndex + 1}">
        <span class="lane-title"><span class="swatch" style={SWATCH_STYLE[lane.key]} aria-hidden="true"></span><strong>{LANE_NAME[lane.key]}</strong></span>
        <small>{LANE_ASK[lane.key]}</small>
        {#if showsProvenance(lane)}
          <small class="origin">{lane.origin === 'live' ? TOOLTIP_TEXT.readJustNow : TOOLTIP_TEXT.fromSavedLogs} · {at(lane.asOf)}</small>
        {/if}
        <span id="tc-hint-{lane.key}" role="tooltip" class="hint">{laneHeadHint(lane)}</span>
      </div>
      <div class="axis axis-left" style="--row:{laneIndex + 1}">
        {#if drawBars}
          <div class="scale">
            <span class="tick tick-top">{formatCount(max)}</span>
            <span class="tick tick-bottom">0</span>
          </div>
        {/if}
      </div>
      <div class="panel" data-lane={lane.key} data-state={lane.state} data-disabled={lane.disabled ?? undefined} style="--row:{laneIndex + 1}">
        <p class="panel-title">{PANEL_TITLE[lane.key]}</p>
        {#if drawBars}
          <!-- The note sits above the bars so the bars stay at the panel's bottom, level with the axis "0". -->
          {#if lane.state === 'incomplete' || laneCutShort(lane)}
            <p role="status" class="lane-note {laneNoteTone(lane)}">{laneNote(lane)}</p>
          {/if}
          <div class="bars">
            {#each outside as i (i)}
              <div class="outside-dash" style="left:{leftPct(i)}%;width:{widthPct}%"></div>
            {/each}
            {#each bars as bar (bar.index)}
              <div class="bar" class:nonzero={bar.count > 0} style="left:{leftPct(bar.index)}%;width:calc({widthPct}% - 1px);height:{barHeightPct(bar.count, max)}%">
                {#if bar.count > 0 && bar.errors > 0}
                  <div class="bar-err" style="height:{(bar.errors / bar.count) * 100}%"></div>
                {/if}
              </div>
            {/each}
            {#if lane.key === 'audit' && latency.segments.length > 0}
              <svg class="latency" viewBox="0 0 100 100" preserveAspectRatio="none" aria-hidden="true" data-percentile={percentile}>
                {#each latency.segments as points, k (k)}
                  <polyline {points} fill="none" vector-effect="non-scaling-stroke" />
                {/each}
              </svg>
              <span class="latency-max">{PERCENTILE_LABEL[percentile]}: up to {formatDuration(latency.max)}</span>
            {/if}
          </div>
        {:else}
          <p class="lane-note">{laneStatusLine(lane)}</p>
        {/if}
      </div>
      <div class="axis axis-right" style="--row:{laneIndex + 1}"></div>
    {/each}

    {#if grid}
      <div class="axisrow" style="--row:{orderedLanes.length + 1}">
        {#each ticks as tick (tick.ms)}
          <span class="axtick" style="left:{tick.offset * widthPct}%">{at(tick.ms)}</span>
        {/each}
      </div>
    {/if}

    <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <div
      role="group"
      aria-roledescription="bucket selector"
      aria-label="Time buckets. Left and right arrow keys move, Home and End jump to the first and last bucket, Enter opens Run analysis for the selected bucket."
      aria-describedby="tc-tip"
      tabindex="0"
      class="overlay"
      style="--end:{Math.max(2, orderedLanes.length + 1)}"
      bind:this={overlayEl}
      bind:clientWidth={plotW}
      onkeydown={onOverlayKey}
      onpointerdown={onOverlayDown}
      onpointermove={onOverlayMove}
      onpointerup={() => endDrag(true)}
      onpointercancel={() => endDrag(false)}
      onpointerleave={onOverlayLeave}
      onclick={onOverlayClick}
      onblur={hideTip}
    >
      <div id="tc-tip" role="tooltip" class="tip" style={tipStyle} hidden={tipGroups.length === 0} bind:this={tipEl}>
        {#each tipGroups as group (group.key)}
          <div class="tip-group">
            <strong>{group.name}</strong>
            {#each group.lines as line, i (i)}<span>{line}</span>{/each}
          </div>
        {/each}
        {#if tipOpenable}<span class="tip-action">{TOOLTIP_TEXT.tapAgain}</span>{/if}
      </div>
      {#if grid}
        {#each slotIndexes as i (i)}
          <div class="column" class:selected={current === i} data-slot={i} aria-hidden="true" style="left:{leftPct(i)}%;width:{widthPct}%"></div>
        {/each}
        {#if dragFrom != null && dragTo != null}
          <div class="brush" style="left:{leftPct(Math.min(dragFrom, dragTo))}%;width:{(Math.abs(dragTo - dragFrom) + 1) * widthPct}%"></div>
        {/if}
      {/if}
    </div>
  </div>

  <span class="sr-only" aria-live="polite" aria-atomic="true">{liveText}</span>

  {#if grid}
    <p class="axcap">{caption}</p>
  {/if}

  {#if response.truncated}
    <p class="foot">{truncatedNote(response.lanes)}</p>
  {/if}
</div>

<style>
  .tracing-chart { display: grid; gap: var(--space-3); min-width: 0; }
  /* The note banner follows the wireframe: a warm tint, a thick amber edge on the left, a bold lead line. */
  .notice { display: grid; gap: var(--space-1); margin: 0; padding: var(--space-3) var(--space-4); color: var(--text); background: color-mix(in srgb, var(--warn) 9%, var(--surface)); border: var(--hairline-width) solid var(--line); border-left: var(--space-1) solid var(--warn); border-radius: var(--radius-sm); font-size: var(--fs-3); }
  .notice p { margin: 0; }
  .mixed-title { font-size: var(--fs-4); font-weight: 700; }
  .muted { color: var(--text-2); }
  .sr-only { position: absolute; width: 1px; height: 1px; margin: -1px; padding: 0; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; border: 0; }

  /* The label column is capped so the plot gets the rest; the right gutter is only breathing room for the last time label. */
  .lanes-grid { --bars-h: 54px; position: relative; display: grid; grid-template-columns: minmax(8rem, 13rem) 3rem minmax(0, 1fr) var(--space-2); column-gap: var(--space-3); row-gap: var(--space-2); align-items: stretch; min-width: 0; }

  .lane-label, .axis, .panel, .axisrow { grid-row: var(--row); }
  .lane-label { grid-column: 1; display: grid; gap: 2px; align-content: start; min-width: 0; }
  .lane-label .lane-title { display: flex; align-items: center; gap: var(--space-1); min-width: 0; }
  .lane-label .swatch { flex: none; width: 10px; height: 10px; border-radius: 50%; }
  .lane-label small { color: var(--text-2); font-size: var(--fs-2); }
  .lane-label { position: relative; }
  .hint { display: none; position: absolute; left: 0; top: 100%; z-index: 3; width: max-content; max-width: 22rem; padding: var(--space-2); color: var(--text); background: var(--surface-2); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); font-size: var(--fs-2); }
  .lane-label:hover .hint, .lane-label:focus-within .hint { display: block; }
  .lane-label .origin { color: var(--text-2); }

  .axis { position: relative; grid-column: 2; min-height: 72px; font-size: var(--fs-1); color: var(--text-2); }
  .axis-right { grid-column: 4; }
  /* The scale box is as tall as the bars and sits on the panel's bottom padding, so "0" is level with the bars' baseline. */
  .axis .scale { position: absolute; left: 0; right: 0; bottom: calc(var(--space-1) + var(--hairline-width)); height: var(--bars-h); }
  .axis .tick { position: absolute; right: var(--space-1); }
  .axis .tick-top { top: 0; }
  .axis .tick-bottom { bottom: 0; }

  .panel { grid-column: 3; position: relative; min-height: 72px; display: flex; flex-direction: column; justify-content: flex-end; padding: var(--space-1) 0; background: var(--surface-2); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); overflow: hidden; }
  .panel-title { margin: 0 0 auto; padding: 0 var(--space-2); color: var(--text-2); font-size: var(--fs-2); }
  .bars { position: relative; flex: none; height: var(--bars-h); margin-top: var(--space-1); }
  .bar { position: absolute; bottom: 0; background: var(--accent); border-radius: var(--radius-sm) var(--radius-sm) 0 0; }
  /* Any bucket with events stays visible next to a huge spike. */
  .bar.nonzero { min-height: 2px; }
  .panel[data-lane='audit'] .bar { background: var(--lane-audit); }
  .panel[data-lane='sql'] .bar { background: var(--lane-sql); }
  .panel[data-lane='iis'] .bar { background: var(--lane-iis); }
  /* Errors differ from the bar by pattern and lightness, not hue alone: stripes of the page background (at least 3:1 against every lane colour, both themes) and --bad. */
  .bar-err { position: absolute; bottom: 0; left: 0; right: 0; min-height: 2px; background: repeating-linear-gradient(135deg, var(--bad) 0 2px, var(--bg) 2px 4px); }
  .outside-dash { position: absolute; bottom: 0; height: 2px; background: var(--line); opacity: .6; }
  .lane-note { margin: var(--space-1) var(--space-2) 0; color: var(--text-2); font-size: var(--fs-3); text-align: center; }
  .lane-note.bad { color: var(--bad); text-align: left; }
  /* A lane cut short or capped is partial, not failed: the warm edge of the note banner, normal text colour. */
  .lane-note.warn { color: var(--text); text-align: left; padding-left: var(--space-2); border-left: var(--space-1) solid var(--warn); }
  .panel > .lane-note:not(.bad):not(.warn) { margin-top: auto; margin-bottom: auto; }

  .axisrow { grid-column: 3; position: relative; height: var(--space-5); font-size: var(--fs-1); color: var(--text-2); }
  .axtick { position: absolute; top: 0; white-space: nowrap; transform: translateX(-50%); }

  .overlay { grid-column: 3; grid-row: 1 / var(--end); position: relative; touch-action: pan-y; }
  .overlay:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .tip { position: absolute; z-index: 4; display: grid; gap: var(--space-2); width: max-content; max-width: min(22rem, 100%); padding: var(--space-2) var(--space-3); color: var(--text); background: var(--surface-2); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); font-size: var(--fs-2); pointer-events: none; }
  .tip[hidden] { display: none; }
  .tip-group { display: grid; gap: 2px; }
  .tip-action { color: var(--text-2); }
  .column { position: absolute; top: 0; bottom: 0; margin: 0; padding: 0; border: 0; background: transparent; cursor: pointer; }
  .column.selected { background: color-mix(in srgb, var(--accent) 18%, transparent); outline: 1px solid var(--accent); }

  .brush { position: absolute; top: 0; bottom: 0; background: color-mix(in srgb, var(--accent) 24%, transparent); outline: 1px dashed var(--accent); pointer-events: none; }
  .canvas-bar { display: flex; flex-wrap: wrap; gap: var(--space-2); align-items: center; font-size: var(--fs-2); }
  .canvas-group { display: inline-flex; gap: var(--space-1); }
  .latency { position: absolute; inset: 0; width: 100%; height: 100%; overflow: visible; pointer-events: none; }
  .latency polyline { stroke: var(--text); stroke-width: 1.5; }
  .latency-max { position: absolute; top: 0; right: var(--space-1); color: var(--text-2); font-size: var(--fs-2); pointer-events: none; }
  .zoom-note { color: var(--text-2); }

  .axcap { margin: 0; color: var(--text-2); font-size: var(--fs-2); }
  .foot { margin: 0; color: var(--text-2); font-size: var(--fs-2); }

  /* Narrow screens: each lane's label sits above its panel, so the plot gets the whole width less the scale gutter. */
  @media (max-width: 768px) {
    .lanes-grid { grid-template-columns: 3rem minmax(0, 1fr) var(--space-2); column-gap: var(--space-2); }
    .lane-label { grid-column: 1 / -1; }
    .axis { grid-column: 1; }
    .panel { grid-column: 2; }
    .axis-right { grid-column: 3; }
    .axisrow, .overlay { grid-column: 2; }
    /* Each lane takes two rows: its label, then its panel. The overlay spans them all; the labels stay above it. */
    .lane-label { grid-row: calc(var(--row) * 2 - 1); z-index: 5; }
    .axis, .panel { grid-row: calc(var(--row) * 2); }
    .axisrow { grid-row: calc(var(--row) * 2 - 1); }
    .overlay { grid-row: 1 / calc(var(--end) * 2 - 1); }
  }
</style>
