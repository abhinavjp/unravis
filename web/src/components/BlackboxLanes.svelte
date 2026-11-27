<script lang="ts">
  // The Blackbox lanes of the Investigation timeline: one line per figure the recorder wrote, on one clock, with the
  // stretches the recorder was not running hatched and its events marked. The lanes are read from the case's own files
  // (GET /series and GET /events). Nothing here names a cause: it shows what was recorded, and "which lane moved first"
  // is a clue, not a finding. The choice of lanes (at most 10) is kept in this browser only.
  import { onDestroy } from 'svelte';
  import BlackboxLanePicker from './BlackboxLanePicker.svelte';
  import { BBL_TEXT } from '../lib/copy/bbLanesCopy';
  import { fetchBbData, caseHasBlackbox, type BbData } from '../lib/tracing/bbLanesApi';
  import {
    MAX_LANES, addLane, defaultLaneIds, eventText, firstToChange, formatLaneValue, gapSpans, laneById, laneName, laneView,
    laneWhat, readLaneChoice, removeLane, scaleLine, type LaneView
  } from '../lib/tracing/bbLanes';

  let { caseId, fromMs, toMs, zone }: { caseId: string; fromMs: number; toMs: number; zone: string } = $props();

  const STORE = 'unravis.bbLanes.v1';
  function load(): string[] {
    try { return readLaneChoice(JSON.parse(localStorage.getItem(STORE) ?? 'null')); } catch { return defaultLaneIds(); }
  }
  function save(ids: string[]): void {
    try { localStorage.setItem(STORE, JSON.stringify(ids)); } catch { /* kept for this visit only */ }
  }

  let present = $state<boolean | null>(null);
  let chosen = $state<string[]>(load());
  let data = $state.raw<BbData | null>(null);
  let status = $state<'idle' | 'loading' | 'ready' | 'error'>('idle');
  let width = $state(900);
  let pickerOpen = $state(false);
  let undo = $state<{ text: string; ids: string[] } | null>(null);
  let hover = $state<{ x: number; t: number } | null>(null);
  let controller: AbortController | null = null;
  let timer: ReturnType<typeof setTimeout> | null = null;

  const COLOUR: Record<string, string> = {
    host: 'var(--series-6)', sql: 'var(--series-2)', iis: 'var(--series-3)', redis: 'var(--series-4)',
    mem: 'var(--series-5)', net: 'var(--series-7)', log: 'var(--series-8)'
  };
  const colour = (group: string): string => COLOUR[group] ?? 'var(--series-8)';

  $effect(() => {
    const id = caseId;
    present = null;
    const c = new AbortController();
    caseHasBlackbox(id, c.signal).then((v) => { present = v; }, () => { present = false; });
    return () => c.abort();
  });

  // A new read when the window, the case or the lanes change. A short wait lets a few ticks in the picker become one read.
  $effect(() => {
    const ids = [...chosen];
    const from = fromMs, to = toMs, id = caseId, w = Math.max(200, Math.round(width));
    if (present !== true || !(to > from)) return;
    if (timer) clearTimeout(timer);
    timer = setTimeout(() => {
      controller?.abort();
      controller = new AbortController();
      const mine = controller;
      status = 'loading';
      fetchBbData(id, ids, from, to, w, mine.signal).then(
        (d) => { if (controller === mine) { data = d; status = 'ready'; } },
        () => { if (controller === mine && !mine.signal.aborted) status = 'error'; }
      );
    }, 150);
  });

  onDestroy(() => { if (timer) clearTimeout(timer); controller?.abort(); });

  const gaps = $derived(data ? gapSpans(data.events.filter((e) => e.kind === 'recording-gap'), data.fromMs, data.toMs) : []);
  const marks = $derived((data?.events ?? []).filter((e) => e.kind !== 'recording-gap' && eventText(e.kind) !== ''));
  const views = $derived.by((): LaneView[] => {
    const d = data;
    if (!d) return [];
    const out: LaneView[] = [];
    for (const id of chosen) {
      const def = laneById(id);
      if (!def) continue;
      const times = def.eventKind ? d.events.filter((e) => e.kind === def.eventKind).map((e) => e.start) : [];
      out.push(laneView(def, d.series.get(id), { times, fromMs: d.fromMs, toMs: d.toMs, bucketMs: d.bucketMs, gaps }));
    }
    return out;
  });
  const first = $derived(data ? firstToChange(views, data.fromMs, data.toMs).slice(0, 4) : []);

  const fmtTime = (ms: number): string =>
    new Intl.DateTimeFormat('en-GB', { hour: '2-digit', minute: '2-digit', hour12: false, timeZone: zone || 'UTC' }).format(new Date(ms));
  const span = $derived(Math.max(1, toMs - fromMs));
  const xOf = (ms: number): number => ((ms - fromMs) / span) * 1000;
  const ticks = $derived([0, 1, 2, 3, 4, 5, 6].map((i) => fromMs + (span * i) / 6));

  function path(v: LaneView, h: number): string {
    const pad = 6;
    let d = '';
    let pen = false;
    // A reading further from the last one than twice the bucket is after a gap: the line breaks and never joins across it.
    const step = (data?.bucketMs ?? 0) * 2.5;
    let last = -Infinity;
    for (const p of v.points) {
      const y = h - pad - (p.v / v.top) * (h - 2 * pad);
      const joined = pen && p.t - last <= step;
      d += (joined ? 'L' : 'M') + xOf(p.t).toFixed(1) + ' ' + y.toFixed(1);
      pen = true;
      last = p.t;
    }
    return d;
  }

  function setChosen(ids: string[], text?: string): void {
    if (text) undo = { text, ids: [...chosen] };
    chosen = ids;
    save(ids);
  }
  const hideLane = (id: string): void => setChosen(removeLane(chosen, id), BBL_TEXT.undid(laneName(id)));
  const toggle = (id: string, on: boolean): void => setChosen(on ? addLane(chosen, id) : removeLane(chosen, id));
  const reset = (): void => setChosen(defaultLaneIds(), BBL_TEXT.reset);
  function undoLast(): void {
    if (!undo) return;
    chosen = undo.ids;
    save(chosen);
    undo = null;
  }

  function move(e: PointerEvent): void {
    const plot = (e.currentTarget as HTMLElement).querySelector('.plot') ?? (e.currentTarget as HTMLElement);
    const box = plot.getBoundingClientRect();
    const f = Math.min(1, Math.max(0, (e.clientX - box.left) / Math.max(1, box.width)));
    hover = { x: f * 100, t: fromMs + f * span };
  }
  function nearest(v: LaneView, t: number): number | null {
    const step = (data?.bucketMs ?? 0) * 1.5;
    let best: { d: number; v: number } | null = null;
    for (const p of v.points) {
      const d = Math.abs(p.t - t);
      if (d <= step && (!best || d < best.d)) best = { d, v: p.v };
    }
    return best ? best.v : null;
  }
  const inGap = (t: number): boolean => gaps.some((g) => t >= g.from && t < g.to);
</script>

{#if present}
  <section class="card bbl" aria-labelledby="bbl-h" data-testid="bbl">
    <header class="top">
      <h3 id="bbl-h">{BBL_TEXT.heading}</h3>
      <span class="hint">{BBL_TEXT.summary(chosen.length, MAX_LANES)}</span>
      <span class="push">
        <button type="button" class="secondary-button" onclick={reset} title={BBL_TEXT.reset}>{BBL_TEXT.reset}</button>
        <button type="button" class="primary-button" onclick={() => { pickerOpen = true; }} data-testid="bbl-choose">{BBL_TEXT.choose}</button>
      </span>
    </header>
    <p class="hint">{BBL_TEXT.intro}</p>
    {#if undo}
      <p class="undo" role="status">{undo.text} <button type="button" class="link" onclick={undoLast}>{BBL_TEXT.undo}</button></p>
    {/if}

    <div class="chart" bind:clientWidth={width}>
      <div class="ticks" aria-hidden="true">{#each ticks as t, i (i)}<span style:left="{(i / 6) * 100}%" class:end={i === 6}>{fmtTime(t)}</span>{/each}</div>
      {#if status === 'loading' && !data}
        <p class="hint" role="status">{BBL_TEXT.loading}</p>
      {:else if status === 'error'}
        <p class="problem" role="alert">{BBL_TEXT.failed}</p>
      {:else if views.length === 0}
        <p class="hint">{BBL_TEXT.empty}</p>
      {:else}
        {#if data?.partial}<p class="hint" role="status">{BBL_TEXT.partial}</p>{/if}
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <div class="lanes" onpointermove={move} onpointerleave={() => { hover = null; }}>
          {#each views as v (v.def.id)}
            <div class="lane">
              <div class="name">
                <b title={laneName(v.def.id)}>{laneName(v.def.id)}</b>
                <small><span class="dot" style:background={colour(v.def.group)}></span>{laneWhat(v.def.id)}</small>
                <small>{scaleLine(v)}</small>
              </div>
              <div class="plot">
                <svg viewBox="0 0 1000 54" preserveAspectRatio="none" role="img" aria-label="{laneName(v.def.id)} over time">
                  {#each gaps as g, i (i)}<rect x={xOf(g.from)} y="0" width={xOf(g.to) - xOf(g.from)} height="54" class="gap" />{/each}
                  {#if v.points.length > 0}<path d={path(v, 54)} fill="none" stroke={colour(v.def.group)} stroke-width="1.6" vector-effect="non-scaling-stroke" stroke-linejoin="round" />{/if}
                  {#each marks as m (m.start + m.kind)}<line x1={xOf(m.start)} x2={xOf(m.start)} y1="0" y2="54" class="mark" vector-effect="non-scaling-stroke" />{/each}
                </svg>
                <button type="button" class="rm" aria-label={BBL_TEXT.hide(laneName(v.def.id))} title={BBL_TEXT.hideTitle} onclick={() => hideLane(v.def.id)}>&times;</button>
              </div>
            </div>
          {/each}
          {#if hover}
            <div class="cross" style:left="calc(var(--lw) + (100% - var(--lw)) * {hover.x / 100})" aria-hidden="true"></div>
            <div class="tip" style:left="calc(var(--lw) + (100% - var(--lw)) * {hover.x / 100})" class:flip={hover.x > 60} aria-hidden="true">
              <b>{fmtTime(hover.t)}</b>{#if inGap(hover.t)}<span class="muted">{BBL_TEXT.gapKey}</span>{/if}
              {#each views as v (v.def.id)}<span>{laneName(v.def.id)}: {formatLaneValue(v.def.unit, nearest(v, hover.t))}</span>{/each}
            </div>
          {/if}
        </div>
        <div class="key">
          <span><i class="gapkey"></i>{BBL_TEXT.gapKey}</span>
          <span><i class="evkey"></i>{BBL_TEXT.eventKey}</span>
          <span>{BBL_TEXT.scaleKey}</span>
        </div>
        <div class="first">
          <h4>{BBL_TEXT.firstHeading}</h4>
          <p class="hint">{BBL_TEXT.firstHelp}</p>
          {#if first.length > 0}
            <ol>{#each first as f (f.id)}<li>{BBL_TEXT.firstAt(laneName(f.id), fmtTime(f.t))}</li>{/each}</ol>
          {:else}<p class="hint">{BBL_TEXT.firstNone}</p>{/if}
        </div>
        <div class="events">
          <h4>{BBL_TEXT.eventsHeading}</h4>
          {#if marks.length === 0}<p class="hint">{BBL_TEXT.eventsNone}</p>
          {:else}<ol>{#each marks as m (m.start + m.kind)}<li><time>{fmtTime(m.start)}</time> {eventText(m.kind)}</li>{/each}</ol>{/if}
        </div>
      {/if}
    </div>
  </section>
  <BlackboxLanePicker open={pickerOpen} {chosen} {colour} onToggle={toggle} onClose={() => { pickerOpen = false; }} />
{/if}

<style>
  .bbl { display: grid; gap: var(--space-3); padding: var(--space-4); min-width: 0; }
  .top { display: flex; align-items: center; gap: var(--space-3); flex-wrap: wrap; }
  h3, h4 { margin: 0; }
  .push { margin-left: auto; display: flex; gap: var(--space-2); flex-wrap: wrap; }
  .hint { margin: 0; color: var(--text-2); font-size: var(--fs-3); }
  .problem { margin: 0; color: var(--bad); }
  .undo { margin: 0; font-size: var(--fs-3); }
  .link { background: none; border: 0; padding: 0; color: var(--accent); text-decoration: underline; cursor: pointer; font: inherit; }
  .chart { --lw: 180px; display: grid; gap: var(--space-2); min-width: 0; position: relative; }
  .ticks { position: relative; height: 18px; margin-left: var(--lw); font-size: var(--fs-2); color: var(--text-2); }
  .ticks span { position: absolute; top: 0; transform: translateX(-50%); }
  .ticks span:first-child { transform: none; }
  .ticks span.end { transform: translateX(-100%); }
  .lanes { position: relative; border-top: 1px solid var(--line); }
  .lane { display: grid; grid-template-columns: var(--lw) 1fr; border-bottom: 1px solid var(--line); min-height: 62px; }
  .name { padding: var(--space-1) var(--space-2) var(--space-1) 0; display: grid; align-content: center; gap: 1px; min-width: 0; }
  .name b { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .name small { color: var(--text-2); font-size: var(--fs-2); overflow-wrap: anywhere; }
  .dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; margin-right: 6px; }
  .plot { position: relative; min-width: 0; }
  svg { display: block; width: 100%; height: 100%; min-height: 54px; }
  .gap { fill: var(--line); opacity: 0.55; }
  .mark { stroke: var(--bad); stroke-opacity: 0.45; stroke-width: 1; }
  .rm { position: absolute; top: 4px; right: 4px; width: 28px; height: 28px; min-height: 0; border: 1px solid var(--line); border-radius: var(--radius-sm); background: var(--surface); color: var(--text-2); cursor: pointer; }
  .cross { position: absolute; top: 0; bottom: 0; width: 1px; background: var(--text-2); opacity: 0.6; pointer-events: none; }
  .tip { position: absolute; top: 4px; z-index: 2; display: grid; gap: 2px; padding: var(--space-2); background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius-sm); font-size: var(--fs-2); pointer-events: none; max-width: 280px; transform: translateX(8px); }
  .tip.flip { transform: translateX(calc(-100% - 8px)); }
  .muted { color: var(--text-2); }
  .key { display: grid; gap: var(--space-1); color: var(--text-2); font-size: var(--fs-2); }
  .key i { display: inline-block; width: 12px; height: 10px; margin-right: 6px; vertical-align: middle; }
  .gapkey { background: var(--line); opacity: 0.7; }
  .evkey { width: 2px !important; background: var(--bad); }
  .first ol, .events ol { margin: var(--space-1) 0 0; padding-left: var(--space-4); }
  time { font-variant-numeric: tabular-nums; color: var(--text-2); margin-right: var(--space-2); }
  @media (max-width: 700px) { .chart { --lw: 120px; } }
</style>
