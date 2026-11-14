<script lang="ts" module>
  // One hatch pattern per component instance, so two charts on a page never share an id.
  let instanceCounter = 0;
</script>

<script lang="ts">
  import { DRILL_TEXT } from '../lib/drill/drillCopy';
  import { formatAtOnce, type CallLane, type LaneBar, type LaneCell } from '../lib/drill/callLanes';
  import { readoutAt, type Readout, type SourceItem } from '../lib/drill/callReadout';
  import { formatDuration } from '../lib/format';
  import { formatTime, zoneAbbreviation } from '../lib/time';

  // The three lanes (API, web server, database) on one time axis: the faint "running at once" shape,
  // hatched gaps, the red column for the call, slow items as bars, event markers, a hover line and its readout.
  // Declared locally until dbState.ts is mounted by the screen; the shape is the same.
  type DbStateKey = 'qs' | 'qsfile' | 'api' | 'off' | 'ro' | 'late' | 'denied' | 'events' | 'notread' | 'blackbox';
  interface DbStateView { key: DbStateKey; source: string; message: string; short: string; shape: 'blocks' | 'apiseen' | 'markers' | 'none' }
  type LaneName = 'audit' | 'iis' | 'sql';

  let {
    lanes,
    start,
    end,
    stepSeconds,
    call,
    db,
    markers,
    shown,
    zone,
    loading,
    selectedKey,
    onSelect,
    onReadout
  }: {
    lanes: { audit: CallLane; iis: CallLane; sql: CallLane };
    start: number;
    end: number;
    stepSeconds: number;
    call: { startMs: number; endMs: number };
    db: DbStateView;
    markers: SourceItem[];
    shown: { api: boolean; iis: boolean; db: boolean };
    zone: string;
    loading: boolean;
    selectedKey: string | null;
    onSelect: (key: string) => void;
    onReadout: (r: Readout | null, pinned: boolean) => void;
  } = $props();

  const patternId = 'scall-hatch-' + ++instanceCounter;
  const W = 1000; // drawing width in x units; the svg stretches to the real width
  const H = 100; // drawing height in y units
  const SHAPE_H = 62; // the tallest shape step, in y units
  const ROW_H = 9;
  const ROW_STEP = 13;
  const TICKS = 6;
  const COLOR: Record<LaneName, string> = { audit: 'var(--lane-audit)', iis: 'var(--lane-iis)', sql: 'var(--lane-sql)' };
  const PREFIX: Record<LaneName, string> = { audit: 'api', iis: 'iis', sql: 'db' };
  const LANE_OF: Record<SourceItem['source'], LaneName> = { api: 'audit', iis: 'iis', db: 'sql' };

  // Real pixel size of the plot, so diamonds stay square and the call column keeps a 2 px minimum.
  let plotW = $state(0);
  let plotH = $state(0);

  let hoverMs = $state<number | null>(null);
  let pinnedMs = $state<number | null>(null);

  const span = $derived(end > start ? end - start : 1);
  const stepMs = $derived(Math.max(1, stepSeconds) * 1000);
  const pxX = $derived(W / (plotW > 0 ? plotW : 800));
  const pxY = $derived(H / (plotH > 0 ? plotH : 72));

  const laneList = $derived(
    ([
      { name: 'audit' as LaneName, on: shown.api, title: DRILL_TEXT.laneApi },
      { name: 'iis' as LaneName, on: shown.iis, title: DRILL_TEXT.laneIis },
      { name: 'sql' as LaneName, on: shown.db, title: DRILL_TEXT.laneDb }
    ]).filter((l) => l.on)
  );

  const x = (t: number): number => Math.min(W, Math.max(0, ((t - start) / span) * W));
  const n2 = (v: number): string => String(Math.round(v * 100) / 100);

  // A database lane with no shape is all hatched; so is any lane with no cell that holds a number.
  const hatchAll = (name: LaneName): boolean =>
    name === 'sql' ? db.shape === 'none' || db.shape === 'markers' || !lanes.sql.hasShape : !lanes[name].hasShape;

  function gapRuns(name: LaneName): { x1: number; x2: number }[] {
    const cells = lanes[name].cells;
    if (hatchAll(name) || cells.length === 0) return [{ x1: 0, x2: W }];
    const runs: { x1: number; x2: number }[] = [];
    let open: { from: number; to: number } | null = null;
    for (const cell of cells) {
      if (cell.atOnce === null) {
        if (open) open.to = cell.end;
        else open = { from: cell.start, to: cell.end };
      } else if (open) {
        runs.push({ x1: x(open.from), x2: x(open.to) });
        open = null;
      }
    }
    if (open) runs.push({ x1: x(open.from), x2: x(open.to) });
    return runs.filter((r) => r.x2 > r.x1);
  }

  // A step path through the cells that hold a number. Cells with no number leave a break (the hatch shows there).
  function shapePath(name: LaneName): string {
    const lane = lanes[name];
    if (hatchAll(name) || !lane.peak) return '';
    const scale = lane.peak.atOnce > 0 ? lane.peak.atOnce : 1;
    const parts: string[] = [];
    let run: LaneCell[] = [];
    const flush = () => {
      if (run.length === 0) return;
      const x0 = x(run[0].start);
      let d = `M${n2(x0)} ${H}`;
      for (const cell of run) {
        const v = cell.atOnce ?? 0;
        let h = (v / scale) * SHAPE_H;
        if (v > 0 && h < 1.5) h = 1.5;
        const y = n2(H - h);
        d += ` L${n2(x(cell.start))} ${y} L${n2(x(cell.end))} ${y}`;
      }
      d += ` L${n2(x(run[run.length - 1].end))} ${H} Z`;
      parts.push(d);
      run = [];
    };
    for (const cell of lane.cells) {
      if (cell.atOnce === null) flush();
      else run.push(cell);
    }
    flush();
    return parts.join(' ');
  }

  const drawnBars = (name: LaneName): LaneBar[] =>
    lanes[name].bars.filter((b) => b.kind === 'bar' && b.end > start && b.start < end);
  const barKey = (name: LaneName, bar: LaneBar): string => `${PREFIX[name]}:${bar.id}`;
  const barX = (bar: LaneBar): number => x(bar.start);
  const barW = (bar: LaneBar): number => Math.max(x(bar.end) - x(bar.start), 3 * pxX);
  const barLabel = (bar: LaneBar): string => `${bar.label}, ${formatDuration(bar.end - bar.start)}`;

  const laneMarkers = (name: LaneName): SourceItem[] =>
    markers.filter((m) => m.marker && LANE_OF[m.source] === name && m.startMs >= start && m.startMs <= end);
  const diamond = (t: number): string => {
    const cx = x(t);
    const cy = H - 6 * pxY;
    const dx = 5 * pxX;
    const dy = 5 * pxY;
    return `${n2(cx)},${n2(cy - dy)} ${n2(cx + dx)},${n2(cy)} ${n2(cx)},${n2(cy + dy)} ${n2(cx - dx)},${n2(cy)}`;
  };

  const slowCount = (name: LaneName): number => lanes[name].bars.filter((b) => b.kind === 'bar').length + lanes[name].moreBars;

  const callX1 = $derived(x(call.startMs));
  const callW = $derived(Math.max(x(call.endMs) - x(call.startMs), 2 * pxX));
  const callVisible = $derived(call.endMs >= start && call.startMs <= end);

  const ticks = $derived(
    Array.from({ length: TICKS }, (_, i) => {
      const t = start + (span * i) / (TICKS - 1);
      return { pct: (i / (TICKS - 1)) * 100, label: formatTime(t, { zone, date: false, suffix: false }), edge: i === 0 ? 'first' : i === TICKS - 1 ? 'last' : 'mid' };
    })
  );
  const zoneName = $derived(zoneAbbreviation(zone, start));

  function readoutFor(t: number | null): Readout | null {
    if (t === null) return null;
    return readoutAt(
      t,
      {
        audit: shown.api ? lanes.audit.cells : [],
        iis: shown.iis ? lanes.iis.cells : [],
        sql: shown.db && !hatchAll('sql') ? lanes.sql.cells : []
      },
      call
    );
  }

  const hoverReadout = $derived(readoutFor(hoverMs));
  const pinnedReadout = $derived(readoutFor(pinnedMs));
  const current = $derived(hoverMs !== null ? hoverReadout : pinnedReadout);
  const hint = $derived(
    pinnedMs !== null ? (hoverMs !== null ? DRILL_TEXT.readMoving : DRILL_TEXT.readPinned) : DRILL_TEXT.readHover
  );

  function timeAt(e: PointerEvent | MouseEvent): number {
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
    const frac = rect.width > 0 ? Math.min(1, Math.max(0, (e.clientX - rect.left) / rect.width)) : 0;
    return start + frac * span;
  }

  function onMove(e: PointerEvent) {
    hoverMs = timeAt(e);
    onReadout(readoutFor(hoverMs), false);
  }
  function onClick(e: MouseEvent) {
    pinnedMs = timeAt(e);
    onReadout(readoutFor(pinnedMs), true);
  }
  function onLeave() {
    hoverMs = null;
    onReadout(pinnedReadout, pinnedMs !== null);
  }
  function onKey(e: KeyboardEvent) {
    if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return;
    e.preventDefault();
    const base = pinnedMs ?? Math.min(end, Math.max(start, (call.startMs + call.endMs) / 2));
    const next = Math.min(end - 1, Math.max(start, base + (e.key === 'ArrowRight' ? stepMs : -stepMs)));
    pinnedMs = next;
    onReadout(readoutFor(next), true);
  }

  function pick(e: Event, key: string) {
    e.stopPropagation();
    onSelect(key);
  }
  function pickKey(e: KeyboardEvent, key: string) {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      pick(e, key);
    }
  }

  const fmtAt = (cell: { atOnce: number | null } | null | undefined): string =>
    cell && cell.atOnce !== null ? formatAtOnce(cell.atOnce) : DRILL_TEXT.notRecorded;
  const fmtP95 = (cell: { p95Ms: number | null } | null | undefined): string =>
    cell && cell.p95Ms !== null ? formatDuration(cell.p95Ms) : DRILL_TEXT.notRecorded;
  // readRefused is typed for a number; it only joins the text, so the "not recorded" words go through the same sentence.
  const refusedLine = (cell: { refused: number | null } | null | undefined): string =>
    cell && cell.refused !== null
      ? DRILL_TEXT.readRefused(cell.refused)
      : (DRILL_TEXT.readRefused as unknown as (v: string) => string)(DRILL_TEXT.notRecorded);
  const dbLine = (cell: { atOnce: number | null } | null | undefined): string =>
    cell && cell.atOnce !== null && db.shape !== 'none' ? DRILL_TEXT.readDb(formatAtOnce(cell.atOnce)) : DRILL_TEXT.readDbNone;

  const headShape = (name: LaneName): string => {
    const lane = lanes[name];
    if (name === 'sql') {
      if (db.shape === 'none' || db.shape === 'markers' || !lane.peak) return db.short;
      return `${DRILL_TEXT.laneShape(formatAtOnce(lane.peak.atOnce))} · ${db.short}`;
    }
    return lane.peak ? DRILL_TEXT.laneShape(formatAtOnce(lane.peak.atOnce)) : DRILL_TEXT.notRecorded;
  };
</script>

<div class="scall-wrap">
  <svg class="scall-defs" width="0" height="0" aria-hidden="true" focusable="false">
    <defs>
      <pattern id={patternId} patternUnits="userSpaceOnUse" width="8" height="8" patternTransform="rotate(45)">
        <line x1="0" y1="0" x2="0" y2="8" stroke="var(--control-line)" stroke-width="2" stroke-opacity="0.55" />
      </pattern>
    </defs>
  </svg>

  {#if loading}
    <p class="scall-loading" role="status">{DRILL_TEXT.loadingLanes}</p>
  {/if}

  <!-- svelte-ignore a11y_no_noninteractive_element_interactions, a11y_no_noninteractive_tabindex -->
  <div
    class="scall-lanes"
    data-testid="scall-lanes"
    aria-busy={loading}
    role="application"
    aria-label={DRILL_TEXT.timelineTitle}
    aria-describedby="scall-hint-{patternId}"
    tabindex="0"
    onpointermove={onMove}
    onpointerleave={onLeave}
    onclick={onClick}
    onkeydown={onKey}
  >
    {#each laneList as lane (lane.name)}
      {@const name = lane.name}
      {@const color = COLOR[name]}
      {@const path = shapePath(name)}
      <div class="scall-lane" data-lane={name} data-testid="scall-lane-{name}">
        <div class="scall-head">
          <span class="scall-name" style="color: {color}">{lane.title}</span>
          {#if name === 'sql'}
            <span class="scall-source" data-testid="scall-db-source">{DRILL_TEXT.laneSource(db.source)}</span>
          {/if}
          <span class="scall-slow">{slowCount(name) > 0 ? DRILL_TEXT.laneSlow(slowCount(name)) : DRILL_TEXT.laneNothingSlow}</span>
          <span class="scall-shape">{headShape(name)}</span>
        </div>
        <div class="scall-plot" bind:clientWidth={plotW} bind:clientHeight={plotH}>
          <svg viewBox="0 0 {W} {H}" preserveAspectRatio="none" aria-hidden="false" role="group" aria-label={lane.title}>
            {#each gapRuns(name) as run}
              <rect class="gap" x={run.x1} y="0" width={run.x2 - run.x1} height={H} fill="url(#{patternId})" />
            {/each}
            {#if path}
              <path class="shape" d={path} fill={color} fill-opacity="0.22" stroke={color} stroke-opacity="0.6" stroke-width="1" vector-effect="non-scaling-stroke" />
            {/if}
            {#if callVisible}
              <rect class="thiscall" x={callX1} y="0" width={callW} height={H} />
            {/if}
            {#each drawnBars(name) as bar (bar.id + ':' + bar.start)}
              {@const key = barKey(name, bar)}
              <g
                class="bar-g"
                class:selected={selectedKey === key}
                role="button"
                tabindex="0"
                aria-label={barLabel(bar)}
                aria-pressed={selectedKey === key}
                onclick={(e) => pick(e, key)}
                onkeydown={(e) => pickKey(e, key)}
              >
                <rect class="bar" data-key={key} x={barX(bar)} y={3 + bar.row * ROW_STEP} width={barW(bar)} height={ROW_H} fill={color} />
              </g>
            {/each}
            {#each laneMarkers(name) as mark (mark.key)}
              <g
                class="marker-g"
                class:selected={selectedKey === mark.key}
                role="button"
                tabindex="0"
                aria-label={mark.what}
                aria-pressed={selectedKey === mark.key}
                onclick={(e) => pick(e, mark.key)}
                onkeydown={(e) => pickKey(e, mark.key)}
              >
                <polygon class="marker" data-key={mark.key} points={diamond(mark.startMs)} fill={color} />
              </g>
            {/each}
          </svg>
          {#if name === 'sql' && db.shape === 'none'}
            <p class="scall-note">{db.short}</p>
          {/if}
          {#if lanes[name].moreBars > 0}
            <p class="scall-more">{DRILL_TEXT.laneMoreBars(lanes[name].moreBars)}</p>
          {/if}
        </div>
      </div>
    {/each}

    {#if hoverMs !== null}
      <div class="scall-line hover" style="left: {((hoverMs - start) / span) * 100}%"></div>
    {/if}
    {#if pinnedMs !== null}
      <div class="scall-line pinned" style="left: {((pinnedMs - start) / span) * 100}%"></div>
    {/if}
  </div>

  <div class="scall-axis" aria-hidden="true">
    {#each ticks as tick}
      <span class="tick {tick.edge}" style="left: {tick.pct}%">{tick.label}</span>
    {/each}
    <span class="scall-zone">{zoneName}</span>
  </div>

  <div class="scall-readout" data-testid="scall-readout" aria-live="polite">
    <p class="read-title">
      {DRILL_TEXT.readTitle}{#if current}
        · {formatTime(current.stepStart, { zone })}{#if current.isThisCall} {DRILL_TEXT.readThisCall}{/if}{/if}
    </p>
    {#if current}
      <ul class="read-rows">
        {#if shown.api}
          <li>{DRILL_TEXT.readApi(fmtAt(current.api))}</li>
          <li>{DRILL_TEXT.readP95(fmtP95(current.api))}</li>
        {/if}
        {#if shown.iis}
          <li>{DRILL_TEXT.readIis(fmtAt(current.iis))}</li>
          <li>{refusedLine(current.iis)}</li>
        {/if}
        {#if shown.db}
          <li>{dbLine(current.db)}</li>
        {/if}
      </ul>
    {/if}
    <p class="read-hint" id="scall-hint-{patternId}">{hint}</p>
  </div>

  <ul class="scall-legend" data-testid="scall-legend">
    <li>{DRILL_TEXT.legendWidth}</li>
    <li>{DRILL_TEXT.legendShape}</li>
    <li>{DRILL_TEXT.legendHatched}</li>
    <li>{DRILL_TEXT.legendCall}</li>
  </ul>
</div>

<style>
  .scall-wrap {
    display: flex;
    flex-direction: column;
    gap: 6px;
    min-width: 0;
  }
  .scall-defs {
    position: absolute;
    width: 0;
    height: 0;
    pointer-events: none;
  }
  .scall-loading {
    margin: 0;
    font-size: 12px;
    color: var(--text-2);
  }
  .scall-lanes {
    position: relative;
    overflow: visible;
    display: flex;
    flex-direction: column;
    border: 1px solid var(--line);
    background: var(--surface);
    touch-action: pan-y;
    cursor: crosshair;
  }
  .scall-lanes:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: 2px;
  }
  .scall-lanes[aria-busy='true'] .scall-plot {
    opacity: 0.5;
  }
  .scall-lane {
    position: relative;
    display: flex;
    flex-direction: column;
    height: var(--scall-lane-h, 96px);
    overflow: hidden;
    border-top: 1px solid var(--line-soft);
  }
  .scall-lane:first-child {
    border-top: 0;
  }
  .scall-head {
    display: flex;
    flex: none;
    gap: 10px;
    align-items: baseline;
    padding: 3px 8px 0;
    font-size: 12px;
    line-height: 16px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    color: var(--text-2);
  }
  .scall-name {
    font-weight: 600;
  }
  .scall-plot {
    position: relative;
    flex: 1 1 0;
    min-height: 0;
  }
  .scall-plot svg {
    display: block;
    width: 100%;
    height: 100%;
  }
  .thiscall {
    fill: var(--bad);
    fill-opacity: 0.28;
    stroke: var(--bad);
    stroke-width: 1;
    vector-effect: non-scaling-stroke;
  }
  .bar-g,
  .marker-g {
    cursor: pointer;
    outline: none;
  }
  .bar-g:focus-visible .bar,
  .marker-g:focus-visible .marker,
  .bar-g.selected .bar,
  .marker-g.selected .marker {
    stroke: var(--text);
    stroke-width: 2;
    vector-effect: non-scaling-stroke;
  }
  .bar {
    fill-opacity: 0.9;
  }
  .scall-note {
    position: absolute;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    margin: 0;
    padding: 0 8px;
    font-size: 12px;
    color: var(--text);
    text-align: center;
    pointer-events: none;
  }
  .scall-more {
    position: absolute;
    right: 6px;
    bottom: 2px;
    margin: 0;
    padding: 0 4px;
    font-size: 11px;
    color: var(--text-2);
    background: var(--surface);
    pointer-events: none;
  }
  .scall-line {
    position: absolute;
    top: 0;
    bottom: 0;
    width: 0;
    pointer-events: none;
  }
  .scall-line.hover {
    border-left: 1px dashed var(--text-2);
  }
  .scall-line.pinned {
    border-left: 2px solid var(--text);
  }
  .scall-axis {
    position: relative;
    height: 30px;
    font-size: 11px;
    color: var(--text-2);
  }
  .tick {
    position: absolute;
    top: 2px;
    white-space: nowrap;
    transform: translateX(-50%);
  }
  .tick.first {
    transform: none;
  }
  .tick.last {
    transform: translateX(-100%);
  }
  .scall-zone {
    position: absolute;
    right: 0;
    top: 16px;
    font-size: 10px;
  }
  .scall-readout {
    padding: 6px 8px;
    border: 1px solid var(--line-soft);
    background: var(--surface-2);
    font-size: 12px;
    color: var(--text);
  }
  .scall-readout p,
  .scall-readout ul {
    margin: 0;
  }
  .read-title {
    font-weight: 600;
  }
  .read-rows {
    display: flex;
    flex-wrap: wrap;
    gap: 2px 16px;
    padding: 0;
    list-style: none;
  }
  .read-hint {
    color: var(--text-2);
  }
  .scall-legend {
    display: flex;
    flex-wrap: wrap;
    gap: 2px 16px;
    margin: 0;
    padding: 0;
    list-style: none;
    font-size: 11px;
    color: var(--text-2);
  }
</style>
