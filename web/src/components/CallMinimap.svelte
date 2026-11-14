<script lang="ts" module>
  // One hatch pattern per component instance, so two strips on a page never share an id.
  let instanceCounter = 0;
</script>

<script lang="ts">
  import { DRILL_TEXT } from '../lib/drill/drillCopy';
  import { formatAtOnce } from '../lib/drill/callLanes';
  import {
    KEY_STEP_FRACTION,
    boxOf,
    centreFromFraction,
    columnMinutes,
    fractionOf,
    type MapRow
  } from '../lib/drill/callMap';
  import { formatTime } from '../lib/time';

  // The overview strip: the two hours around the call as three small rows, an outlined box for the part the big chart shows
  // (drag it or use the arrow keys), a red line for the call, the busiest moment of each row, and a caption in plain words.
  // The box only reports where it is: onPreview while dragging (no read happens), onCommit when the move is done.

  let {
    rows,
    overview,
    win,
    call,
    bucketSeconds,
    zone,
    hidden,
    onPreview,
    onCommit
  }: {
    rows: MapRow[];
    overview: { start: number; end: number };
    win: { start: number; end: number };
    call: { startMs: number; endMs: number };
    bucketSeconds: number;
    zone: string;
    hidden: boolean;
    onPreview: (centreMs: number) => void;
    onCommit: (centreMs: number) => void;
  } = $props();

  const patternId = 'scall-map-hatch-' + ++instanceCounter;
  const ROW_H = 20; // px per row; three rows plus the label line stay under 84 px
  const COLOR: Record<MapRow['key'], string> = { audit: 'var(--lane-audit)', iis: 'var(--lane-iis)', sql: 'var(--lane-sql)' };
  const LANE_NAME: Record<MapRow['key'], string> = {
    audit: DRILL_TEXT.laneApi,
    iis: DRILL_TEXT.laneIis,
    sql: DRILL_TEXT.laneDb
  };

  let plotEl = $state<HTMLDivElement | null>(null);
  let boxEl = $state<HTMLDivElement | null>(null);
  // While the box is being dragged: the pointer id, how far the pointer sat from the box centre when it was grabbed,
  // and where the centre is now (a fraction of the strip). Null when nothing is being dragged.
  let dragPointer = $state<number | null>(null);
  let dragGrab = 0;
  let dragFraction = $state<number | null>(null);
  let dragCentreMs = 0;

  const span = $derived(overview.end - overview.start);
  const box = $derived.by(() => {
    const shown = boxOf(overview, win);
    if (dragFraction === null) return shown;
    return { left: Math.min(1 - shown.width, Math.max(0, dragFraction - shown.width / 2)), width: shown.width };
  });
  const centre = $derived(box.left + box.width / 2);
  const callFraction = $derived(fractionOf(overview, call.startMs));

  const clock = (ms: number, suffix = false): string =>
    formatTime(ms, { zone, precision: 'minute', date: false, suffix });
  const boxText = $derived(
    formatTime(overview.start + box.left * span, { zone, precision: 'minute', date: false, suffix: false }) +
      ' to ' +
      formatTime(overview.start + (box.left + box.width) * span, { zone, precision: 'minute', date: false, suffix: true })
  );
  const pct = (fraction: number): string => (fraction * 100).toFixed(3) + '%';

  const fractionAt = (clientX: number): number => {
    if (!plotEl) return 0;
    const rect = plotEl.getBoundingClientRect();
    if (!(rect.width > 0)) return 0;
    return Math.min(1, Math.max(0, (clientX - rect.left) / rect.width));
  };

  function onBoxDown(e: PointerEvent): void {
    if (e.button !== undefined && e.button !== 0) return;
    const target = e.currentTarget as HTMLElement;
    target.setPointerCapture(e.pointerId);
    dragPointer = e.pointerId;
    dragGrab = fractionAt(e.clientX) - centre;
    dragFraction = centre;
    dragCentreMs = centreFromFraction(overview, centre);
    e.preventDefault();
  }

  function onBoxMove(e: PointerEvent): void {
    if (dragPointer === null || e.pointerId !== dragPointer) return;
    const fraction = Math.min(1, Math.max(0, fractionAt(e.clientX) - dragGrab));
    dragFraction = fraction;
    dragCentreMs = centreFromFraction(overview, fraction);
    onPreview(dragCentreMs);
  }

  function onBoxUp(e: PointerEvent): void {
    if (dragPointer === null || e.pointerId !== dragPointer) return;
    const target = e.currentTarget as HTMLElement;
    if (target.hasPointerCapture?.(e.pointerId)) target.releasePointerCapture(e.pointerId);
    dragPointer = null;
    dragFraction = null;
    onCommit(dragCentreMs);
  }

  function onBoxKey(e: KeyboardEvent): void {
    let next: number | null = null;
    if (e.key === 'ArrowLeft') next = centreFromFraction(overview, centre - KEY_STEP_FRACTION);
    else if (e.key === 'ArrowRight') next = centreFromFraction(overview, centre + KEY_STEP_FRACTION);
    else if (e.key === 'Home') next = (call.startMs + call.endMs) / 2;
    if (next === null) return;
    e.preventDefault();
    onCommit(next);
  }

  // A click on the strip outside the box moves the window there.
  function onStripClick(e: MouseEvent): void {
    if (boxEl && e.target instanceof Node && boxEl.contains(e.target)) return;
    onCommit(centreFromFraction(overview, fractionAt(e.clientX)));
  }

  const peakText = (row: MapRow): string =>
    row.peak ? DRILL_TEXT.mapPeak(formatAtOnce(row.peak.atOnce), clock(row.peak.start)) : DRILL_TEXT.mapNoData;
  const cellTitle = (row: MapRow, start: number, height: number | null, atOnce: number | null): string =>
    height === null || atOnce === null
      ? DRILL_TEXT.mapCellNone(LANE_NAME[row.key], clock(start))
      : DRILL_TEXT.mapCell(LANE_NAME[row.key], formatAtOnce(atOnce), clock(start));
  // A column only keeps its height as a share of the row's own busiest column; turn it back into a count for the hover text.
  const atOnceOf = (row: MapRow, height: number | null): number | null =>
    height === null || !row.peak ? null : height * row.peak.atOnce;
</script>

{#if hidden}
  <p class="scall-map-hidden" data-testid="scall-map-hidden">{DRILL_TEXT.mapHidden}</p>
{:else}
  <div class="scall-map" data-testid="scall-map">
    <svg class="scall-map-defs" width="0" height="0" aria-hidden="true" focusable="false">
      <defs>
        <pattern id={patternId} patternUnits="userSpaceOnUse" width="6" height="6" patternTransform="rotate(45)">
          <line x1="0" y1="0" x2="0" y2="6" stroke="var(--control-line)" stroke-width="2" stroke-opacity="0.55" />
        </pattern>
      </defs>
    </svg>
    <div class="scall-map-strip">
      <div class="scall-map-grid scall-map-head">
        <span></span>
        <div class="scall-map-headplot">
          {#if callFraction !== null}
            <span
              class="scall-map-callname"
              class:near-start={callFraction < 0.2}
              class:near-end={callFraction > 0.8}
              style:left={pct(callFraction)}>{DRILL_TEXT.mapThisCall(clock(call.startMs, true))}</span
            >
          {/if}
        </div>
        <span></span>
      </div>
      <div class="scall-map-grid scall-map-body">
        <div class="scall-map-side">
          {#each rows as row (row.key)}
            <span class="scall-map-label" title={LANE_NAME[row.key]}>{LANE_NAME[row.key]}</span>
          {/each}
        </div>
        <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
        <div class="scall-map-plot" bind:this={plotEl} onclick={onStripClick}>
          {#each rows as row (row.key)}
            <svg class="scall-map-row" data-row={row.key} width="100%" height={ROW_H} aria-hidden="true" focusable="false">
              {#each row.columns as col (col.start)}
                {@const x = span > 0 ? (col.start - overview.start) / span : 0}
                {@const w = span > 0 ? Math.max(0, (col.end - col.start) / span) : 0}
                {#if col.height === null}
                  <rect class="gap" x={pct(x)} y="0" width={pct(w)} height={ROW_H} fill="url(#{patternId})">
                    <title>{cellTitle(row, col.start, null, null)}</title>
                  </rect>
                {:else}
                  <rect
                    class="col"
                    x={pct(x)}
                    y={ROW_H - col.height * ROW_H}
                    width={pct(w)}
                    height={col.height * ROW_H}
                    fill={COLOR[row.key]}
                  >
                    <title>{cellTitle(row, col.start, col.height, atOnceOf(row, col.height))}</title>
                  </rect>
                {/if}
              {/each}
            </svg>
          {/each}
          {#if callFraction !== null}
            <div class="scall-map-callline" style:left={pct(callFraction)}></div>
          {/if}
          <div
            class="scall-map-box"
            class:dragging={dragPointer !== null}
            data-testid="scall-map-box"
            role="slider"
            tabindex="0"
            aria-label={DRILL_TEXT.mapBoxLabel}
            aria-valuemin="0"
            aria-valuemax="100"
            aria-valuenow={Math.round(centre * 100)}
            aria-valuetext={boxText}
            bind:this={boxEl}
            style:left={pct(box.left)}
            style:width={pct(box.width)}
            onpointerdown={onBoxDown}
            onpointermove={onBoxMove}
            onpointerup={onBoxUp}
            onpointercancel={onBoxUp}
            onkeydown={onBoxKey}
          >
            <span class="scall-map-drag">{DRILL_TEXT.mapDrag}</span>
          </div>
        </div>
        <div class="scall-map-side scall-map-peaks">
          {#each rows as row (row.key)}
            <span class="scall-map-peak" title={peakText(row)}>{peakText(row)}</span>
          {/each}
        </div>
      </div>
    </div>
    <p class="scall-map-caption" data-testid="scall-map-caption">{DRILL_TEXT.mapCaption(columnMinutes(bucketSeconds))}</p>
    <details class="scall-map-more">
      <summary>{DRILL_TEXT.mapHow}</summary>
      <p>{DRILL_TEXT.mapCaptionMore}</p>
    </details>
  </div>
{/if}

<style>
  .scall-map {
    display: flex;
    flex-direction: column;
    gap: 8px;
    min-width: 0;
  }
  .scall-map-defs {
    position: absolute;
    width: 0;
    height: 0;
  }
  .scall-map-hidden {
    margin: 0;
    font-size: 12px;
    color: var(--text-2);
  }
  .scall-map-strip {
    max-height: 84px;
    border: 1px solid var(--line);
    background: var(--surface);
    padding: 0 6px;
  }
  .scall-map-grid {
    display: grid;
    grid-template-columns: 7.5rem minmax(0, 1fr) 8rem;
    column-gap: 8px;
  }
  .scall-map-head {
    height: 14px;
  }
  .scall-map-headplot {
    position: relative;
    height: 14px;
  }
  .scall-map-callname {
    position: absolute;
    top: 0;
    transform: translateX(-50%);
    font-size: 11px;
    line-height: 14px;
    white-space: nowrap;
    color: var(--bad);
  }
  .scall-map-callname.near-start {
    transform: none;
  }
  .scall-map-callname.near-end {
    transform: translateX(-100%);
  }
  .scall-map-body {
    height: 64px;
  }
  .scall-map-side {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }
  .scall-map-label,
  .scall-map-peak {
    height: 20px;
    font-size: 11px;
    line-height: 20px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    color: var(--text-2);
  }
  .scall-map-plot {
    position: relative;
    display: flex;
    flex-direction: column;
    gap: 2px;
    height: 64px;
    cursor: pointer;
  }
  .scall-map-row {
    display: block;
    flex: none;
    width: 100%;
    height: 20px;
    background: var(--surface-2);
  }
  .scall-map-callline {
    position: absolute;
    top: 0;
    bottom: 0;
    width: 0;
    border-left: 2px solid var(--bad);
    pointer-events: none;
  }
  .scall-map-box {
    position: absolute;
    top: 0;
    bottom: 0;
    box-sizing: border-box;
    border: 2px solid var(--text);
    background: transparent;
    touch-action: none;
    cursor: grab;
    display: flex;
    align-items: flex-end;
    justify-content: center;
  }
  .scall-map-box.dragging {
    cursor: grabbing;
  }
  .scall-map-box:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: 2px;
  }
  .scall-map-drag {
    font-size: 10px;
    line-height: 12px;
    padding: 0 3px;
    color: var(--text);
    background: var(--surface);
    pointer-events: none;
    user-select: none;
  }
  .scall-map-caption {
    margin: 0;
    font-size: 12px;
    color: var(--text-2);
  }
  .scall-map-more {
    font-size: 12px;
    color: var(--text-2);
  }
  .scall-map-more summary {
    cursor: pointer;
  }
  .scall-map-more p {
    margin: 4px 0 0;
  }
</style>
