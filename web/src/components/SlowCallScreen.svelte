<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { api } from '../lib/api';
  import { DRILL_TEXT } from '../lib/drill/drillCopy';
  import { createCallController, type CallDeps, type CallState } from '../lib/drill/callController';
  import { fetchCallEvents } from '../lib/drill/callData';
  import type { SlowCallTarget } from '../lib/drill/callTarget';
  import { fetchTracing, nextRequestSeq, type TracingLane } from '../lib/tracing/model';
  import { fetchLaneDays, newestDay, type LaneDay } from '../lib/lanes';
  import { buildLane, packBars, type CallLane, type LaneBar, type LaneIn, type LaneKey } from '../lib/drill/callLanes';
  import { allSources, readoutAt, type Readout, type SourceItem } from '../lib/drill/callReadout';
  import { dbState, type DbStateKey } from '../lib/drill/dbState';
  import { mapRow } from '../lib/drill/callMap';
  import { centreOn, clampWindow, coversOverview, overviewBounds, windowBounds } from '../lib/drill/callWindow';
  import { filterRows, type CallRef, type NearbyRow } from '../lib/drill/nearbyCalls';
  import { formatTime, timeParts } from '../lib/time';
  import DrillOverview from './DrillOverview.svelte';
  import CallWindowBar from './CallWindowBar.svelte';
  import CallLanes from './CallLanes.svelte';
  import CallMinimap from './CallMinimap.svelte';
  import CallNearby from './CallNearby.svelte';
  import CallAllSources from './CallAllSources.svelte';
  import CallInspector from './CallInspector.svelte';

  // The full-page Slow call screen (layout W1): breadcrumb, title, the two header buttons, the loading / not found / failed states, then the window bar,
  // the pinned timeline (three lanes and the overview strip), the "This call" facts, the two list tabs and the inspector on the right.
  // This is a page, not a dialog: no focus trap; Esc is handled by the window listener in appState.
  let {
    target,
    zone,
    demo,
    onBack,
    onOpenAnalysis,
    onFound
  }: {
    target: SlowCallTarget;
    zone: string;
    demo: boolean;
    onBack: () => void;
    onOpenAnalysis: (slot: number) => void;
    onFound: (call: { sourceId: string; id: string }) => void;
  } = $props();

  type Tab = 'calls' | 'all';

  let callState = $state<CallState | null>(null);
  let controller = $state.raw<ReturnType<typeof createCallController> | null>(null);
  let titleEl = $state<HTMLElement>();
  let shown = $state({ api: true, iis: true, db: true });
  let tab = $state<Tab>('calls');
  let selectedKey = $state<string | null>(null);
  let readout = $state<{ value: Readout | null; pinned: boolean }>({ value: null, pinned: false });
  let pretend = $state<DbStateKey | null>(null);
  let savedDay = $state<LaneDay | null>(null);
  let callsTab = $state<HTMLElement>();
  let allTab = $state<HTMLElement>();

  // Every read gets its own request number and its own answer: the tracing read is not "latest wins" here, so the overview strip and the
  // visible window can run side by side and both fill. The comparison period is the equal period right before the asked range.
  function tracing(caseId: string, startIso: string, endIso: string, sourceIds: string[], signal: AbortSignal) {
    const startMs = Date.parse(startIso);
    const endMs = Date.parse(endIso);
    const referenceStart = new Date(startMs - (endMs - startMs)).toISOString();
    const body = {
      sourceIds,
      incident: { start: startIso, end: endIso },
      reference: { start: referenceStart, end: startIso }
    };
    return fetchTracing(caseId, body, nextRequestSeq(), signal);
  }

  const deps: CallDeps = {
    slowest: (query, signal) => api.slowest(query, signal),
    record: (sourceId, id, signal) => api.record(sourceId, id, signal),
    tracing,
    activity: (input, signal) => api.activity(input, signal),
    events: (caseId, fromIso, toIso, signal) => fetchCallEvents(caseId, fromIso, toIso, signal)
  };

  // Keyed on the case and slot VALUES, not on the target object. Reading `target.caseId` inside the effect would track the whole
  // `target` prop, and the parent replaces `target` once the call is found (same case and slot) — that must not start the search over.
  // A derived primitive string only changes when the values do, so a same-value replacement leaves the effect (and the controller) alone.
  const restartKey = $derived(`${target.caseId}:${target.slot}`);

  $effect(() => {
    void restartKey;
    // Only a different case or slot restarts it: a new controller is made and the old one dropped.
    const ctl = untrack(() => {
      callState = null;
      selectedKey = null;
      const made = createCallController(target, deps, (next) => { callState = next; }, (call) => onFound(call));
      controller = made;
      made.start();
      return made;
    });
    return () => {
      ctl.dispose();
      if (controller === ctl) controller = null;
    };
  });

  onMount(() => {
    titleEl?.focus({ preventScroll: true });
    // The newest saved database day tells the database lane which Query Store state it is in. A failed read is ignored: the lane then relies on the tracing answer alone.
    const ask = new AbortController();
    fetchLaneDays(target.caseId, ask.signal)
      .then((days) => { savedDay = newestDay(days); })
      .catch(() => {});
    return () => ask.abort();
  });

  // The database lane draws one block per Query Store interval when the server sent no block length of its own.
  function qsBlockSeconds(key: LaneKey): number | undefined {
    const minutes = savedDay?.lane?.queryStore?.intervalMinutes;
    return key === 'sql' && typeof minutes === 'number' && Number.isFinite(minutes) && minutes > 0 ? minutes * 60 : undefined;
  }

  // The tracing parser keeps the raw reason code, the lane events, the block seconds and the bucket busy time; all four pass through here.
  function toLaneIn(lane: TracingLane | undefined): LaneIn | undefined {
    if (!lane) return undefined;
    return {
      key: lane.key,
      state: lane.state,
      reasonCode: lane.reasonCode ?? lane.incompleteReason,
      blockSeconds: lane.blockSeconds ?? qsBlockSeconds(lane.key),
      buckets: lane.buckets,
      events: lane.events ?? [],
      hidden: lane.hidden
    };
  }

  const SQL_EVENT_SOURCES = ['sql-xe', 'sql-errorlog'];
  const fmtDb = {
    date: (iso: string) => {
      const parts = timeParts(iso, zone, 'minute');
      return parts ? `${parts.day} ${parts.year}` : DRILL_TEXT.notRecorded;
    },
    time: (iso: string) => formatTime(iso, { zone, date: false, precision: 'minute' })
  };

  // Everything the workbench draws, worked out once per change. Null until the call record is there.
  const bench = $derived.by(() => {
    const s = callState;
    if (!s || !s.record) return null;
    const bounds = windowBounds(s.callStartMs, s.window);
    const over = overviewBounds(s.callStartMs);
    const resp = s.visible ?? s.overview;
    const win = resp?.window ?? { axisStart: new Date(bounds.start).toISOString(), bucketSeconds: 60 };
    const step = win.bucketSeconds;
    const lane = (key: LaneKey) => toLaneIn(resp?.lanes.find((l) => l.key === key));
    const lanes = {
      audit: buildLane(lane('audit'), 'audit', win, bounds.start, bounds.end),
      iis: buildLane(lane('iis'), 'iis', win, bounds.start, bounds.end),
      sql: buildLane(lane('sql'), 'sql', win, bounds.start, bounds.end)
    };

    // API bars come from the slowest-call read, so each bar is one exact record. Ids follow the same rule as the all-sources list.
    const many = new Set(s.slowRows.map((r) => r.sourceId).filter(Boolean)).size > 1;
    const apiKeyOf = (sourceId: string, id: string) => (many && sourceId ? `api:${sourceId}:${id}` : `api:${id}`);
    const packItems = s.slowRows.flatMap((row) => {
      const start = Date.parse(row.initiatedAt ?? '');
      if (!Number.isFinite(start)) return [];
      // A missing duration is never drawn as a 0 ms bar; the call stays in the list as "not recorded".
      if (row.durationMs == null || !Number.isFinite(row.durationMs)) return [];
      const took = row.durationMs;
      return [{ id: many && row.sourceId ? `${row.sourceId}:${row.id}` : row.id, label: `${row.controller}.${row.action}`, start, end: start + took }];
    });
    const apiBars = packBars(packItems);
    lanes.audit = { ...lanes.audit, bars: apiBars.bars, moreBars: apiBars.more };

    // Only real bars are timed IIS items. A zero-length IIS marker has no known duration: it goes in as a marker, never as "0 ms".
    const fromAll = allSources({
      apiRows: s.slowRows.map((r) => ({ id: r.id, controller: r.controller, action: r.action, initiatedAt: r.initiatedAt ?? '', durationMs: r.durationMs, sourceId: r.sourceId })),
      iisBars: lanes.iis.bars.filter((b) => b.kind === 'bar'),
      events: s.events,
      callId: s.record.id,
      callSourceId: s.record.sourceId,
      start: bounds.start,
      end: bounds.end,
      shown
    });
    const iisMarkers: SourceItem[] = shown.iis
      ? lanes.iis.bars
          .filter((b) => b.kind === 'marker' && Number.isFinite(b.start) && b.start >= bounds.start && b.start <= bounds.end)
          .map((b) => ({ key: `iis:${b.id}`, source: 'iis' as const, startMs: b.start, endMs: null, what: b.label, tookMs: null, marker: true, thisCall: false }))
      : [];
    const items = iisMarkers.length === 0 ? fromAll : [...fromAll, ...iisMarkers].sort((a, b) => a.startMs - b.startMs);
    const markers = items.filter((i) => i.marker);

    // The "Pretend the database is" states only ever apply in demo mode.
    const pretendNow = demo ? pretend : null;
    const sqlLane = resp?.lanes.find((l) => l.key === 'sql');
    const db = dbState(
      {
        laneReasonCode: toLaneIn(sqlLane)?.reasonCode ?? '',
        laneHasShape: lanes.sql.hasShape,
        laneAsOf: sqlLane?.asOf || null,
        laneSince: sqlLane?.since || null,
        blockSeconds: lanes.sql.blockSeconds,
        queryStore: savedDay?.lane?.queryStore ?? null,
        hasEvents: s.events.some((e) => SQL_EVENT_SOURCES.includes(e.source) && Date.parse(e.start) >= bounds.start && Date.parse(e.start) <= bounds.end),
        windowStart: bounds.start,
        windowEnd: bounds.end,
        pretend: pretendNow
      },
      fmtDb
    );
    // A pretend state with another shape than the real lane makes the lane draw that shape: none means no cells at all.
    let drawnSql: CallLane = lanes.sql;
    if (pretendNow && db.shape === 'none') drawnSql = { ...lanes.sql, cells: [], peak: null, hasShape: false };
    else if (pretendNow && (db.shape === 'blocks' || db.shape === 'apiseen')) drawnSql = { ...lanes.sql, hasShape: true };
    else if (pretendNow && db.shape === 'markers') drawnSql = { ...lanes.sql, hasShape: false };

    const call = { startMs: s.callStartMs, endMs: s.callEndMs };
    const callRef: CallRef = {
      id: s.record.id,
      sourceId: s.record.sourceId,
      controller: s.record.controller,
      action: s.record.action,
      userId: s.record.userId ?? null,
      startMs: s.callStartMs,
      endMs: s.callEndMs
    };
    const overviewLane = (key: LaneKey) => toLaneIn(s.overview?.lanes.find((l) => l.key === key));
    const mapRows = (['audit', 'iis', 'sql'] as const).map((key) =>
      mapRow(key, s.overview ? buildLane(overviewLane(key), key, s.overview.window, over.start, over.end).cells : [])
    );
    const nearbyAll: NearbyRow[] = s.nearby.rows.map((r) => ({
      id: r.id,
      sourceId: r.sourceId,
      controller: r.controller,
      action: r.action,
      initiatedAt: r.initiatedAt ?? '',
      durationMs: r.durationMs,
      status: r.status !== '' && Number.isFinite(Number(r.status)) ? Number(r.status) : undefined,
      userId: r.userId ?? null,
      light: r.light ?? null
    }));
    const nearbyRows = filterRows(nearbyAll, s.chip, callRef, bounds.start, bounds.end);
    const whileRan = readoutAt(s.callStartMs, { audit: lanes.audit.cells, iis: lanes.iis.cells, sql: drawnSql.cells }, call);
    const sources = [
      { key: 'api' as const, label: DRILL_TEXT.laneApi, recorded: lanes.audit.hasShape || s.slowRows.length > 0 },
      { key: 'iis' as const, label: DRILL_TEXT.laneIis, recorded: lanes.iis.hasShape || lanes.iis.bars.length > 0 },
      { key: 'db' as const, label: db.source, recorded: db.shape !== 'none' }
    ];
    return {
      s,
      record: s.record,
      bounds,
      over,
      step,
      lanes: { audit: lanes.audit, iis: lanes.iis, sql: drawnSql },
      items,
      markers,
      db,
      call,
      callRef,
      mapRows,
      nearbyRows,
      whileRan,
      sources,
      apiKeyOf,
      covers: coversOverview(s.callStartMs, s.window)
    };
  });

  // The selected item. The key is the one the lanes and the all-sources list share. A lane bar the list does not carry (database bars)
  // and a nearby row that is not among the slowest calls are rebuilt from what was clicked.
  const selected = $derived.by((): SourceItem | null => {
    const b = bench;
    const key = selectedKey;
    if (!b || key === null) return null;
    const hit = b.items.find((i) => i.key === key);
    if (hit) return hit;
    const fromBar = (source: SourceItem['source'], bar: LaneBar): SourceItem => ({
      key,
      source,
      startMs: bar.start,
      endMs: bar.end > bar.start ? bar.end : null,
      what: bar.label,
      tookMs: bar.end > bar.start ? bar.end - bar.start : null,
      marker: bar.kind === 'marker',
      thisCall: false
    });
    const laneBars: [SourceItem['source'], string, CallLane][] = [['api', 'api', b.lanes.audit], ['iis', 'iis', b.lanes.iis], ['db', 'db', b.lanes.sql]];
    for (const [source, prefix, lane] of laneBars) {
      const bar = lane.bars.find((x) => `${prefix}:${x.id}` === key);
      if (bar) return fromBar(source, bar);
    }
    const row = b.s.nearby.rows.find((r) => b.apiKeyOf(r.sourceId, r.id) === key);
    const startMs = row ? Date.parse(row.initiatedAt ?? '') : NaN;
    if (row && Number.isFinite(startMs)) {
      const took = row.durationMs != null && Number.isFinite(row.durationMs) ? row.durationMs : null;
      return {
        key,
        source: 'api',
        startMs,
        endMs: took === null ? null : startMs + took,
        what: [row.controller, row.action].filter(Boolean).join(' / '),
        tookMs: took,
        marker: false,
        thisCall: row.id === b.s.record?.id
      };
    }
    return null;
  });

  const TABS: Tab[] = ['calls', 'all'];
  function onTabKey(e: KeyboardEvent) {
    let next = TABS.indexOf(tab);
    if (e.key === 'ArrowRight') next = (next + 1) % TABS.length;
    else if (e.key === 'ArrowLeft') next = (next + TABS.length - 1) % TABS.length;
    else if (e.key === 'Home') next = 0;
    else if (e.key === 'End') next = TABS.length - 1;
    else return;
    e.preventDefault();
    tab = TABS[next];
    (tab === 'calls' ? callsTab : allTab)?.focus();
  }

  function pickNearby(row: NearbyRow) {
    selectedKey = bench ? bench.apiKeyOf(row.sourceId, row.id) : null;
  }

  const time = $derived(formatTime(callState?.record?.initiatedAt ?? target.start, { zone, precision: 'second' }));
</script>

<section class="slow-call" data-testid="slow-call-screen" data-zone={zone} data-demo={demo ? 'true' : 'false'} aria-labelledby="scall-title">
  <header class="scall-head">
    <nav class="scall-crumb" aria-label="Breadcrumb"><button type="button" class="link" data-testid="scall-back" onclick={onBack}>{DRILL_TEXT.crumbTimeline}</button> <span aria-hidden="true">›</span> <span>{DRILL_TEXT.crumbCall(time)}</span></nav>
    <h2 id="scall-title" tabindex="-1" bind:this={titleEl}>{DRILL_TEXT.title}</h2>
    <div class="scall-actions">
      <button type="button" class="btn outlined" data-testid="scall-analysis" onclick={() => onOpenAnalysis(target.slot)}>{DRILL_TEXT.openAnalysis}</button>
      <button type="button" class="btn outlined" data-testid="scall-back-btn" onclick={onBack}>{DRILL_TEXT.backToTimeline} <kbd>{DRILL_TEXT.escHint}</kbd></button>
    </div>
  </header>

  {#if !callState || callState.phase === 'finding'}
    <p class="note" role="status" data-testid="scall-finding">{DRILL_TEXT.finding}</p>
  {:else if callState.phase === 'not-found'}
    <p class="note" data-testid="scall-notfound">{DRILL_TEXT.notFound}</p>
  {:else if callState.phase === 'failed'}
    <div class="problem" role="alert" data-testid="scall-failed">
      <p>{DRILL_TEXT.loadFailed}</p>
      <button type="button" class="btn outlined" data-testid="scall-retry" onclick={() => controller?.retry()}>{DRILL_TEXT.retry}</button>
    </div>
  {/if}

  <div class="scall-body" data-testid="scall-body">
    {#if bench}
      <div class="scall-grid" data-layout="w1">
        <div class="scall-main">
          <CallWindowBar
            window={bench.s.window}
            callStartMs={bench.s.callStartMs}
            callEndMs={bench.s.callEndMs}
            {zone}
            {shown}
            onCommit={(w) => controller?.commit(clampWindow(w))}
            onShown={(v) => { shown = v; }}
          />

          <div class="scall-pin" data-testid="scall-pin" data-pinned={readout.pinned ? 'true' : 'false'}>
            <CallLanes
              lanes={bench.lanes}
              start={bench.bounds.start}
              end={bench.bounds.end}
              stepSeconds={bench.step}
              call={bench.call}
              db={bench.db}
              markers={bench.markers}
              {shown}
              {zone}
              loading={bench.s.visibleLoading}
              {selectedKey}
              onSelect={(key) => { selectedKey = key; }}
              onReadout={(r, pinned) => { readout = { value: r, pinned }; }}
            />
            {#if bench.s.visibleFailed}
              <p class="note" role="status" data-testid="scall-lanes-failed">{DRILL_TEXT.lanesFailed}</p>
            {/if}
            {#if bench.s.slowRowsFailed}
              <p class="note" role="status" data-testid="scall-slowrows-failed">{DRILL_TEXT.slowRowsFailed}</p>
            {/if}
            <CallMinimap
              rows={bench.mapRows}
              overview={bench.over}
              win={bench.bounds}
              call={bench.call}
              bucketSeconds={bench.s.overview?.window.bucketSeconds ?? bench.step}
              {zone}
              hidden={bench.covers}
              onPreview={(c) => controller?.preview(centreOn(bench.s.callStartMs, bench.s.window, c))}
              onCommit={(c) => controller?.commit(centreOn(bench.s.callStartMs, bench.s.window, c))}
            />
            {#if bench.s.overviewFailed}
              <p class="note" role="status" data-testid="scall-overview-failed">{DRILL_TEXT.overviewFailed}</p>
            {/if}
          </div>

          <div class="scall-thiscall" data-testid="scall-thiscall">
            <DrillOverview record={bench.record} />
          </div>

          <div class="scall-tabs" role="tablist" aria-label={DRILL_TEXT.tabsLabel} data-testid="scall-tabs">
            <button
              type="button"
              role="tab"
              class="tab"
              id="scall-tab-calls"
              aria-selected={tab === 'calls'}
              aria-controls="scall-panel-calls"
              tabindex={tab === 'calls' ? 0 : -1}
              data-testid="scall-tab-calls"
              bind:this={callsTab}
              onclick={() => { tab = 'calls'; }}
              onkeydown={onTabKey}
            >{DRILL_TEXT.tabNearbyCalls(bench.nearbyRows.length)}</button>
            <button
              type="button"
              role="tab"
              class="tab"
              id="scall-tab-all"
              aria-selected={tab === 'all'}
              aria-controls="scall-panel-all"
              tabindex={tab === 'all' ? 0 : -1}
              data-testid="scall-tab-all"
              bind:this={allTab}
              onclick={() => { tab = 'all'; }}
              onkeydown={onTabKey}
            >{DRILL_TEXT.tabAllSources(bench.items.length)}</button>
          </div>
          {#if tab === 'calls'}
            <div role="tabpanel" id="scall-panel-calls" aria-labelledby="scall-tab-calls">
              <CallNearby
                rows={bench.nearbyRows}
                call={bench.callRef}
                chip={bench.s.chip}
                start={bench.bounds.start}
                end={bench.bounds.end}
                asking={bench.s.nearby.asking}
                failed={bench.s.nearby.failed}
                truncated={bench.s.nearby.loaded?.truncated ?? false}
                {zone}
                onChip={(c) => controller?.setChip(c)}
                onSelect={pickNearby}
              />
            </div>
          {:else}
            <div role="tabpanel" id="scall-panel-all" aria-labelledby="scall-tab-all">
              {#if bench.s.eventsTruncated}
                <p class="note" role="status" data-testid="scall-events-truncated">{DRILL_TEXT.eventsTruncated}</p>
              {/if}
              <CallAllSources items={bench.items} {zone} {selectedKey} onSelect={(key) => { selectedKey = key; }} />
            </div>
          {/if}
        </div>

        <CallInspector
          {selected}
          whileRan={bench.whileRan}
          db={bench.db}
          sources={bench.sources}
          {zone}
          {demo}
          {pretend}
          onPretend={(k) => { pretend = k; }}
        />
      </div>
    {/if}
  </div>
</section>

<style>
  .slow-call { display: grid; gap: 16px; padding: 16px 20px; color: var(--text); }
  .scall-head { display: flex; align-items: center; flex-wrap: wrap; gap: 8px 12px; }
  .scall-crumb { flex: 1 0 100%; display: flex; align-items: center; gap: 6px; color: var(--text-2); font-size: 13px; }
  .scall-head h2 { margin: 0; font-size: 18px; font-weight: 600; letter-spacing: -.01em; outline: none; }
  .scall-actions { display: flex; gap: 8px; flex-wrap: wrap; margin-left: auto; }
  .link { padding: 0; color: var(--text-2); background: none; border: 0; font: inherit; text-decoration: underline; cursor: pointer; }
  .link:hover { color: var(--text); }
  .link:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  kbd { padding: 1px 6px; color: var(--text-2); border: 1px solid var(--line); border-radius: var(--radius); font: inherit; font-size: 11px; }
  .note { margin: 0; color: var(--text-2); font-size: 13px; }
  .problem { display: grid; justify-items: start; gap: 12px; padding: 12px; background: var(--surface-2); border: 1px solid var(--warn); border-radius: var(--radius); }
  .problem p { margin: 0; font-size: 13px; }
  .scall-thiscall { padding: 16px 20px; background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius); }

  /* Layout W1: the main column (window bar, pinned timeline, facts, lists) and the inspector on the right. Nothing here scrolls on its own. */
  [data-layout='w1'].scall-grid { display: grid; grid-template-columns: minmax(0, 1fr) 320px; gap: 16px; align-items: start; }
  [data-layout='w1'] .scall-main { --scall-lane-h: clamp(72px, calc((100vh - 540px) / 3), 132px); display: grid; gap: 8px; min-width: 0; }
  [data-layout='w1'] .scall-pin { position: sticky; top: 0; z-index: 2; display: grid; gap: 8px; padding-bottom: 8px; background: var(--bg); }
  [data-layout='w1'] .scall-tabs { display: flex; gap: 4px; border-bottom: 1px solid var(--line); }
  [data-layout='w1'] .tab { padding: 10px 12px; color: var(--text-2); background: none; border: 0; border-bottom: 2px solid transparent; font-size: 13px; font-weight: 600; cursor: pointer; }
  [data-layout='w1'] .tab[aria-selected='true'] { color: var(--text); border-bottom-color: var(--accent); }
  [data-layout='w1'] .tab:hover { color: var(--text); }
  [data-layout='w1'] .tab:focus-visible { outline: 2px solid var(--focus); outline-offset: -2px; }
  @media (max-width: 1180px) {
    [data-layout='w1'].scall-grid { grid-template-columns: minmax(0, 1fr); }
  }

  .btn { display: inline-flex; align-items: center; justify-content: center; gap: 8px; min-height: 36px; padding: 8px 14px; border-radius: var(--radius); font-size: 13px; font-weight: 600; cursor: pointer; transition: background var(--dur) var(--ease), border-color var(--dur) var(--ease), color var(--dur) var(--ease); }
  .btn.outlined { color: var(--text); background: transparent; border: 1px solid var(--line); }
  .btn.outlined:hover { border-color: var(--text-2); background: var(--surface-2); }
  .btn:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }

  @media (pointer: coarse), (max-width: 720px) {
    .btn { min-height: 44px; }
  }
  @media (prefers-reduced-motion: reduce) {
    *, *::before, *::after { animation-duration: .01ms !important; transition-duration: .01ms !important; }
  }
</style>
