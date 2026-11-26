<script lang="ts">
  import { logSeriesReadTiming, watchSize } from '../lib/chartPerf';
  import { timeZone } from '../lib/timeZone.svelte';
  import { onMount, untrack } from 'svelte';
  import { MarkAreaComponent } from 'echarts/components';
  import { echarts, palette } from '../lib/charts';
  import { theme } from '../lib/theme.svelte';
  import { chartTime } from '../lib/chartTime';
  import { blackboxApi, refusalText, shownTime, type BlackboxItem } from '../lib/blackbox';
  import { BB_REFUSAL } from '../lib/copy/blackboxCopy';
  import { BBC_TEXT as T } from '../lib/copy/blackboxChartsCopy';
  import { sqlNote } from '../lib/blackboxSqlNames';
  import {
    PRESET_LABELS, UNIT_TITLES, axisValue, chartData, gapsForLine, coverNote, defaultSelection, firstPreset, formatValue, lineFigures, pickerGroups,
    alignedRequest, presetWindow, readSeriesAnswer, seriesTitle, seriesUnit, unitStrips, GROUP_TITLES, own, niceBinaryMax, biggestPlotted, settleWidth,
    type SeriesAnswer, type SeriesLine, type SeriesPoint, type SeriesUnit, type WindowPreset
  } from '../lib/blackboxSeries';
  import { MAX_DRAWN, eventDots, eventRows, happenedItems, healthRows, statusChips, type HappenedItem, type Happened } from '../lib/blackboxHappened';
  import { POSITION_WORDS, QUICK_QUESTIONS, compareLatest, meaningOf, quickKeys, shapePath, type QuickQuestion } from '../lib/blackboxCompare';

  // The Blackbox charts view: what one Blackbox recorded, drawn like Perfmon (pick counters, linear or log scale, a figures bar
  // under the chart). The numbers come from POST /api/blackbox/{id}/series, which reads the recordings with the same reader as the
  // host check. Nothing is collected here. A value that was not read is a break in the line, never 0. Times follow the header's time zone choice, like the panel.
  echarts.use([MarkAreaComponent]);

  let { item, onClose }: { item: BlackboxItem; onClose: () => void } = $props();

  const POINTS = 600;
  const STORE = 'unravis.bbCharts.';

  type Win = { startMs: number; endMs: number };
  type Saved = { keys?: string[]; log?: boolean };
  const loadSaved = (id: string): Saved => { try { return JSON.parse(localStorage.getItem(STORE + id) ?? '{}') as Saved; } catch { return {}; } };
  const save = (id: string, v: Saved) => { try { localStorage.setItem(STORE + id, JSON.stringify(v)); } catch { /* a convenience only */ } };

  // svelte-ignore state_referenced_locally
  const facts = item.facts;
  // svelte-ignore state_referenced_locally
  const saved = loadSaved(item.id);
  // svelte-ignore state_referenced_locally
  let preset = $state<WindowPreset | 'detail'>(firstPreset(facts?.firstSample, facts?.lastSample));
  let win = $state<Win | null>(presetWindow(firstPreset(facts?.firstSample, facts?.lastSample), facts?.firstSample, facts?.lastSample));
  let back = $state<Array<{ win: Win; preset: WindowPreset | 'detail' }>>([]);
  let answer = $state.raw<SeriesAnswer | null>(null);
  let busy = $state(false);
  let problem = $state('');
  let attempt = $state(0);
  // 0 until the chart box has a real size: nothing is read before then (a hidden box would ask for a 1-point answer).
  let chartWidth = $state(0);
  let readRun = 0;
  // The window the current zoom belongs to: a new window clears the zoom, a new width does not.
  let zoomWin: Win | null = null;
  let paintSample = $state.raw<{ answer: SeriesAnswer; startedAt: number } | null>(null);
  // The width the reads use: in steps of WIDTH_STEP, with some slack on a shrink, so a scrollbar or a nudge does not read again.
  let pixelWidth = $state(0);
  $effect(() => { const measured = chartWidth; untrack(() => { pixelWidth = settleWidth(pixelWidth, measured); }); });
  let log = $state(saved.log === true);
  let keys = $state<string[]>(Array.isArray(saved.keys) ? saved.keys : []);
  let tab = $state<'counters' | 'processes'>('counters');
  let view = $state<'charts' | 'happened' | 'latest' | 'everything'>('charts');
  const INTRO = 'unravis.bbCharts.intro';
  const introWasHidden = (): boolean => { try { return localStorage.getItem(INTRO) === 'hidden'; } catch { return false; } };
  let showIntro = $state(!introWasHidden());
  const setIntro = (on: boolean) => { showIntro = on; try { if (on) localStorage.removeItem(INTRO); else localStorage.setItem(INTRO, 'hidden'); } catch { /* a convenience only */ } };
  let focusKey = $state('');
  let openGroups = $state<string[]>(['host']);
  let everySearch = $state('');
  let procMetric = $state<'cpu' | 'workingSet' | 'privateBytes'>('cpu');
  let search = $state('');
  let zoom = $state<Win | null>(null);

  let dialog = $state<HTMLDialogElement>();
  let node = $state<HTMLDivElement>();
  let chart = $state<ReturnType<typeof echarts.init>>();

  onMount(() => { dialog?.showModal(); });

  // The chart exists only while the Charts view is on screen: leaving it (or closing the pop-up) removes the listener, stops the
  // size watcher and frees the chart object, so nothing is held for a view nobody is looking at.
  $effect(() => {
    const target = node;
    if (!target) return;
    let made: ReturnType<typeof echarts.init> | undefined;
    const stopSize = watchSize(target, () => {
      if (!made) {
        if (target.clientWidth > 0 && target.clientHeight > 0) {
          made = echarts.init(target);
          made.on('datazoom', onZoom);
          chart = made;
        }
        return;
      }
      made.resize();
    });
    return () => {
      stopSize();
      if (made) { made.off('datazoom', onZoom); made.dispose(); made = undefined; }
      chart = undefined;
    };
  });

  // One debounced read per window or visible width step. A newer read cancels the older request and rejects a late answer even if abort lost the race.
  // A width that is not measured yet waits. The user's zoom is kept when only the width changed; a new window clears it.
  $effect(() => {
    const w = win, width = pixelWidth;
    void attempt;
    const mine = ++readRun;
    if (!w || width <= 0) return;
    const ctl = new AbortController();
    paintSample = null;
    untrack(() => { busy = true; problem = ''; if (w !== zoomWin) { zoom = null; zoomWin = w; } });
    const maxPoints = Math.max(1, Math.min(POINTS, width));
    const req = alignedRequest(w.startMs, w.endMs, facts?.effectiveIntervalSeconds || facts?.intervalSeconds || 15, maxPoints);
    const timer = setTimeout(() => {
      const began = performance.now();
      blackboxApi.series(item.id, {
        start: new Date(req.startMs).toISOString(), end: new Date(req.endMs).toISOString(), points: req.points, width, shape: 'm4'
      }, ctl.signal, true)
        .then((raw) => {
          if (mine !== readRun || ctl.signal.aborted) return;
          const a = readSeriesAnswer(raw);
          if (!a) { problem = T.badAnswer; answer = null; return; }
          answer = a;
          paintSample = { answer: a, startedAt: began };
          const have = new Set(a.lines.map((l) => l.key));
          const kept = untrack(() => keys).filter((k) => have.has(k));
          if (!kept.length) keys = defaultSelection(a.lines);
        })
        .catch((e: unknown) => {
          if (mine !== readRun || ctl.signal.aborted || (e instanceof DOMException && e.name === 'AbortError')) return;
          problem = refusalText(e, BB_REFUSAL) || T.readFailed;
        })
        .finally(() => { if (mine === readRun && !ctl.signal.aborted) busy = false; });
    }, 150);
    return () => { clearTimeout(timer); ctl.abort(); };
  });

  $effect(() => { save(item.id, { keys, log }); });

  function pickPreset(p: WindowPreset) {
    const w = presetWindow(p, facts?.firstSample, facts?.lastSample);
    if (!w) return;
    back = [];
    preset = p;
    win = w;
  }
  function readZoom() {
    if (!zoom || !win) return;
    back = [...back, { win, preset }];
    preset = 'detail';
    win = { ...zoom };
  }
  function goBack() {
    const prev = back[back.length - 1];
    if (!prev) return;
    back = back.slice(0, -1);
    preset = prev.preset;
    win = prev.win;
    dialog?.querySelector<HTMLElement>('.bbc-seg button')?.focus();
  }
  function onZoom() {
    if (!chart || !win) return;
    const dz = (chart.getOption() as { dataZoom?: Array<{ startValue?: number; endValue?: number; start?: number; end?: number }> }).dataZoom?.[0];
    if (!dz) return;
    const span = win.endMs - win.startMs;
    const s = typeof dz.startValue === 'number' ? dz.startValue : win.startMs + span * (dz.start ?? 0) / 100;
    const e = typeof dz.endValue === 'number' ? dz.endValue : win.startMs + span * (dz.end ?? 100) / 100;
    zoom = e - s < span * 0.98 ? { startMs: Math.round(s), endMs: Math.round(e) } : null;
  }

  function toggle(key: string) { keys = keys.includes(key) ? keys.filter((k) => k !== key) : [...keys, key]; }

  // The lines the chart draws: picked counters, or the busiest programs for one measure.
  type Drawn = { key: string; title: string; unit: SeriesUnit; points: SeriesPoint[] };
  const drawn = $derived.by((): Drawn[] => {
    if (!answer) return [];
    if (tab === 'processes') {
      const unit: SeriesUnit = procMetric === 'cpu' ? 'pct' : 'bytes';
      return answer.processes.map((p) => ({ key: 'proc:' + p.name, title: p.name, unit, points: p[procMetric] })).filter((d) => d.points.length);
    }
    const byKey = new Map(answer.lines.map((l) => [l.key, l]));
    return keys.map((k) => byKey.get(k)).filter((l): l is SeriesLine => !!l).slice(0, MAX_LINES).map((l) => ({ key: l.key, title: seriesTitle(l.key), unit: seriesUnit(l.key, l.unit), points: l.points }));
  });
  const strips = $derived(unitStrips(drawn.map((d) => d.key), drawn.map((d) => ({ key: d.key, group: '', label: '', unit: d.unit, points: [] }))));
  const groups = $derived(answer ? pickerGroups(answer.lines) : []);
  const shownGroups = $derived.by(() => {
    const q = search.trim().toLowerCase();
    if (!q) return groups;
    return groups.map((g) => ({ ...g, lines: g.lines.filter((l) => (seriesTitle(l.key) + ' ' + l.key + ' ' + g.title).toLowerCase().includes(q)) })).filter((g) => g.lines.length);
  });
  const sqlNoteText = $derived(answer && answer.samples > 0 ? sqlNote(answer.sql) : null);
  const notRead = $derived((answer?.groups ?? []).map((c) => ({ c, note: answer && answer.samples > 0 ? coverNote(c) : null })).filter((x) => x.note));
  // BLACKBOX-04: a colour is never used twice inside one chart. The colours run per chart (per unit), not across all of them; only a
  // chart with more lines than the palette has colours goes on to dashed and then dotted lines, and the swatches show that too.
  type Dash = 'solid' | 'dashed' | 'dotted';
  const colourOf = $derived.by(() => {
    const p = (void theme.epoch, palette());
    const n = p.series.length;
    const out = new Map<string, { color: string; dash: Dash }>();
    for (const s of strips) s.keys.forEach((k, j) => out.set(k, { color: p.series[j % n], dash: j < n ? 'solid' : j < 2 * n ? 'dashed' : 'dotted' }));
    return out;
  });
  const legends = $derived(strips.map((s) => {
    const names = s.keys.map((k) => ({ key: k, title: drawn.find((d) => d.key === k)?.title ?? k, c: colourOf.get(k) }));
    return { unit: s.unit, shown: names.slice(0, 4), more: Math.max(0, names.length - 4) };
  }));
  let hidden = $state(0);
  const MAX_LINES = 24;
  const chips = $derived(statusChips(answer));
  const happened = $derived(answer && view !== 'latest' && view !== 'everything' ? happenedItems(answer) : ({ items: [] as HappenedItem[], more: 0, drawn: { gaps: [], dumps: [], restarts: [] } } as Happened));
  const evRows = $derived(answer && view === 'happened' ? eventRows(answer.eventIds) : { rows: [], more: 0 });
  const evDots = $derived(answer && view === 'happened' ? eventDots(answer.lines) : { dots: [], more: 0 });
  const health = $derived(answer && view === 'happened' ? healthRows(answer) : []);
  const quick = $derived(answer ? QUICK_QUESTIONS.map((q) => ({ q, keys: quickKeys(q, answer!.lines) })).filter((x) => x.keys.length) : []);
  const lineByKey = $derived(new Map((answer?.lines ?? []).map((l) => [l.key, l])));
  const compared = $derived(view === 'latest' ? drawn.map((d) => ({ d, c: compareLatest(d.points, zoom?.startMs ?? -Infinity, zoom?.endMs ?? Infinity) })) : []);
  const allGroups = $derived.by(() => {
    if (view !== 'everything' || !answer) return [];
    const q = everySearch.trim().toLowerCase();
    return pickerGroups(answer.lines).map((g) => ({ ...g, lines: q ? g.lines.filter((l) => (seriesTitle(l.key) + ' ' + l.key).toLowerCase().includes(q)) : g.lines })).filter((g) => g.lines.length);
  });
  const whenText = (ms: number | null | undefined) => ms == null ? '' : chartTime(ms, timeZone.zone, { seconds: false });
  const focusLine = $derived(drawn.find((d) => d.key === focusKey));
  function ask(q: QuickQuestion, ks: string[]) { void q; tab = 'counters'; view = 'charts'; keys = ks; focusKey = ''; }
  function toggleGroup(g: string) { openGroups = openGroups.includes(g) ? openGroups.filter((x) => x !== g) : [...openGroups, g]; }
  function pickFocus(k: string) { focusKey = focusKey === k ? '' : k; }

  const STRIP_H = 150;
  const TOP = 30;
  const GAP = 54;
  const LEFT = 86;
  const BOTTOM = 56;
  const height = $derived(Math.max(1, strips.length) * (STRIP_H + GAP) - GAP + TOP + BOTTOM);

  const ticks = (w: Win) => {
    const longSpan = w.endMs - w.startMs > 20 * 3_600_000;
    return (v: number) => chartTime(v, timeZone.zone, { seconds: false, zone: false, date: longSpan });
  };

  // Zoom with buttons, for people who cannot drag (BLACKBOX-05). The same chart zoom as the bar and the wheel: it goes through
  // the chart, which reports it back to onZoom, so the table and "Read this range in detail" follow.
  function moveZoom(how: 'in' | 'out' | 'earlier' | 'later') {
    if (!win || !chart) return;
    const full = { startMs: win.startMs, endMs: win.endMs };
    const cur = zoom ?? full;
    const span = cur.endMs - cur.startMs;
    const mid = (cur.startMs + cur.endMs) / 2;
    let s = cur.startMs, e = cur.endMs;
    if (how === 'in') { s = mid - span / 4; e = mid + span / 4; }
    else if (how === 'out') { s = mid - span; e = mid + span; }
    else { const step = span / 4 * (how === 'earlier' ? -1 : 1); s += step; e += step; }
    const fullSpan = full.endMs - full.startMs;
    if (e - s >= fullSpan * 0.98) { s = full.startMs; e = full.endMs; }
    else if (s < full.startMs) { e += full.startMs - s; s = full.startMs; }
    else if (e > full.endMs) { s -= e - full.endMs; e = full.endMs; }
    chart.dispatchAction({ type: 'dataZoom', startValue: Math.round(s), endValue: Math.round(e) });
    zoom = e - s < fullSpan * 0.98 ? { startMs: Math.round(s), endMs: Math.round(e) } : null;
  }

  // On a phone the list of counters is behind a button, and the chart comes first (BLACKBOX-24).
  let narrow = $state(false);
  let sideOpen = $state(false);
  onMount(() => {
    const q = window.matchMedia('(max-width: 639px)');
    const set = () => { narrow = q.matches; };
    set();
    q.addEventListener('change', set);
    return () => q.removeEventListener('change', set);
  });

  $effect(() => {
    if (!chart) return;
    void theme.epoch;
    void timeZone.zone; // a new zone choice redraws the axis
    const p = palette();
    const a = answer;
    const w = win;
    if (!a || !w || !strips.length) { chart.clear(); hidden = 0; return; }
    const byKey = new Map(drawn.map((d) => [d.key, d]));
    const lookup = new Map(drawn.map((d) => [d.key, new Map(d.points.map((pt) => [pt[0], pt]))]));
    const axis = { axisLine: { lineStyle: { color: p.axisLine } }, axisLabel: { color: p.text2 }, nameTextStyle: { color: p.text2 }, splitLine: { lineStyle: { color: p.grid } } };
    const gapAreas = a.gaps.slice(0, MAX_DRAWN).map((g) => [{ xAxis: Date.parse(g.from), name: T.gapLabel }, { xAxis: Date.parse(g.to) }]);
    // Crash dumps and restarts are drawn as dotted lines (at most MAX_DRAWN, newest first). The label is plain text from safeText.
    const drawnNow = untrack(() => happened).drawn;
    const markerLines = [...drawnNow.dumps.map((it) => ({ xAxis: it.t, name: T.markDump })), ...drawnNow.restarts.map((it) => ({ xAxis: it.t, name: T.markRestart }))];
    const focus = untrack(() => focusKey);
    let hiddenNow = 0;
    const grids = strips.map((_, i) => ({ left: LEFT, right: 24, containLabel: false, top: TOP + i * (STRIP_H + GAP), height: STRIP_H }));
    const xAxes = strips.map((_, i) => ({
      type: 'time', gridIndex: i, min: Math.min(w.startMs, Date.parse(a.start) || w.startMs), max: Math.max(w.endMs, Date.parse(a.end) || w.endMs), ...axis, splitLine: { show: false },
      axisLabel: { color: p.text2, formatter: ticks(w), hideOverlap: true }
    }));
    const yAxes = strips.map((s, i) => {
      // Byte axes get a round top in binary units, so the grid reads 512 MB, 1 GB, 1.5 GB, 2 GB and not 954 MB, 1.86 GB.
      const top = !log && (s.unit === 'bytes' || s.unit === 'bytesPerSec') ? niceBinaryMax(biggestPlotted(s.keys.map((k) => byKey.get(k)!).filter(Boolean)) ?? 0) : null;
      return {
        type: log ? 'log' : 'value', gridIndex: i, name: UNIT_TITLES[s.unit], nameLocation: 'middle', nameGap: 70, scale: !log && s.unit !== 'pct',
        min: !log && (s.unit === 'pct' || s.unit === 'count' || s.unit === 'perSec' || s.unit === 'bytes' || s.unit === 'bytesPerSec') ? 0 : undefined,
        ...(top ? { max: top, interval: top / 4 } : { splitNumber: 4 }),
        // On the log scale the minor lines show where 2, 3 ... 9 times a step fall, so a value between two labels can still be read.
        ...(log ? { minorTick: { show: true, splitNumber: 9 }, minorSplitLine: { show: true, lineStyle: { color: p.grid, opacity: 0.4 } } } : {}),
        ...axis, axisLabel: { color: p.text2, formatter: (v: number) => axisValue(s.unit, v) }
      };
    });
    const series: unknown[] = [];
    strips.forEach((s, i) => {
      s.keys.forEach((k, j) => {
        const d = byKey.get(k)!;
        const c = colourOf.get(k);
        const cd = chartData(d.points, gapsForLine(a, k), log);
        hiddenNow += cd.hidden;
        series.push({
          type: 'line', name: d.title, id: k, xAxisIndex: i, yAxisIndex: i, data: cd.data, connectNulls: false, showSymbol: cd.data.length < 40, symbolSize: 4,
          sampling: 'lttb', lineStyle: { width: focus === k ? 3 : 1.6, opacity: focus && focus !== k ? 0.35 : 1, color: c?.color, type: c?.dash ?? 'solid' }, itemStyle: { color: c?.color },
          markArea: j === 0 && gapAreas.length ? { silent: true, itemStyle: { color: p.empty, opacity: 0.5 }, label: { show: i === 0 && gapAreas.length <= 6, position: 'insideBottom', color: p.text2, fontSize: 11 }, data: gapAreas } : undefined,
          markLine: j === 0 && markerLines.length ? { silent: true, symbol: 'none', lineStyle: { color: p.warn, type: 'dotted' }, label: { show: i === 0 && markerLines.length <= 6, formatter: '{b}', color: p.text2, fontSize: 11 }, data: markerLines } : undefined
        });
      });
    });
    hidden = hiddenNow;
    const all = strips.map((_, i) => i);
    const z = untrack(() => zoom);
    const keep = z ? { startValue: z.startMs, endValue: z.endMs } : {};
    chart.setOption({
      animation: false, textStyle: { color: p.text2 }, grid: grids, xAxis: xAxes, yAxis: yAxes, series,
      axisPointer: { link: [{ xAxisIndex: 'all' }] },
      dataZoom: [
        { type: 'inside', xAxisIndex: all, filterMode: 'none', ...keep },
        { type: 'slider', xAxisIndex: all, filterMode: 'none', ...keep, bottom: 8, height: 22, left: LEFT, right: 40, labelFormatter: ticks(w), textStyle: { color: p.text2 } }
      ],
      tooltip: {
        // BLACKBOX-18: the tooltip sits at the top of the chart, beside the pointer, so it does not cover the lines being read.
        trigger: 'axis', confine: true, renderMode: 'richText', axisPointer: { type: 'line' },
        position: (pt: number[]) => [pt[0] + 16, 8],
        textStyle: { rich: Object.fromEntries(drawn.map((d, i) => ['s' + i, { color: colourOf.get(d.key)?.color ?? p.text2 }])) },
        formatter: (raw: unknown) => {
          const items = (Array.isArray(raw) ? raw : [raw]) as Array<{ seriesId: string; seriesName: string; value: [number, number | null] }>;
          if (!items.length) return '';
          const t = items[0].value[0];
          const rows = items.map((it) => {
            const d = byKey.get(it.seriesId);
            const pt = lookup.get(it.seriesId)?.get(t);
            if (!d) return '';
            // A coloured line mark first, so a row can be matched to its line (and to the dash of a repeated colour).
            const dash = colourOf.get(d.key)?.dash ?? 'solid';
            const mark = '{s' + drawn.indexOf(d) + '|' + (dash === 'solid' ? '━━' : dash === 'dashed' ? '╍╍' : '┅┅') + '} ';
            const name = it.seriesName.replace(/[{}|]/g, '');
            if (!pt) return mark + name + ': ' + T.notRead;
            const range = pt[2] !== pt[3] ? ' (' + formatValue(d.unit, pt[2]) + ' to ' + formatValue(d.unit, pt[3]) + ')' : '';
            return mark + name + ': ' + formatValue(d.unit, pt[1]) + range;
          }).filter(Boolean);
          return chartTime(t, timeZone.zone) + '\n' + rows.join('\n');
        }
      }
    }, true);
    const sample = untrack(() => paintSample);
    if (sample?.answer === a) {
      paintSample = null;
      logSeriesReadTiming('blackbox-paint', performance.now() - sample.startedAt);
    }
  });

  // A click on a table row only restyles the lines (no rebuild of the chart).
  $effect(() => {
    const c = chart;
    const f = focusKey;
    if (!c || !untrack(() => answer) || !((c.getOption() as { series?: unknown[] }).series?.length)) return;
    const ids = untrack(() => drawn).map((d) => d.key);
    c.setOption({ series: ids.map((id) => ({ id, lineStyle: { width: f === id ? 3 : 1.6, opacity: f && f !== id ? 0.35 : 1 } })) });
  });

  const figures = $derived(drawn.map((d) => ({ d, f: lineFigures(d.points, zoom?.startMs ?? -Infinity, zoom?.endMs ?? Infinity) })));
  const bucketWords = (s: number) => s >= 3600 ? (s / 3600).toFixed(s % 3600 ? 1 : 0) + ' h' : s >= 60 ? Math.round(s / 60) + ' min' : s + ' s';
</script>

<!-- Esc closes only this pop-up, wherever focus is: it is caught first here so the Blackbox drawer behind it and the app's own Esc stay put. -->
<svelte:window onkeydowncapture={(e) => { const f = e.target as HTMLElement | null; if (f instanceof HTMLSelectElement || (f instanceof HTMLInputElement && f.type === 'search' && f.value)) return; if (e.key === 'Escape' && dialog?.open) { e.preventDefault(); e.stopPropagation(); dialog.close(); } }} />
<dialog bind:this={dialog} class="bbc" aria-labelledby="bbc-title" onclose={onClose}>
  <header class="bbc-head">
    <div>
      <h2 id="bbc-title">{T.title(item.name)}</h2>
      <p class="bbc-note">{[facts?.firstSample && facts?.lastSample ? T.recordedSpan(shownTime(facts.firstSample, timeZone.zone), shownTime(facts.lastSample, timeZone.zone)) : '', T.timesIn(timeZone.short), T.sameAsPerfmon].filter(Boolean).join(' ')}</p>
      {#if chips.length}
        <ul class="bbc-chips" aria-label={T.chipsLabel}>
          {#each chips as c (c.id)}<li><span class="bbc-chip {c.id}" title={c.hint}>{c.text}<span class="bbc-sr">. {c.hint}</span></span></li>{/each}
        </ul>
      {/if}
    </div>
    <div class="bbc-head-actions">
      {#if !showIntro}<button type="button" class="quiet-button" title={T.helpShowHint} onclick={() => setIntro(true)}>{T.helpShow}</button>{/if}
      <button type="button" class="bbc-close" aria-label={T.close} title={T.close} onclick={() => dialog?.close()}>×</button>
    </div>
  </header>

  {#if !win}
    <p class="bbc-body">{T.nothingRecorded}</p>
  {:else}
    {#if showIntro}
      <section class="bbc-intro" aria-labelledby="bbc-intro-title">
        <h3 id="bbc-intro-title">{T.introTitle}</h3>
        <ol><li>{T.intro1}</li><li>{T.intro2}</li><li>{T.intro3}</li></ol>
        <button type="button" class="secondary-button" title={T.introDismissHint} onclick={() => setIntro(false)}>{T.introDismiss}</button>
      </section>
    {/if}
    <div class="bbc-bar">
      <div class="bbc-seg" role="group" aria-label={T.window}>
        {#each Object.keys(PRESET_LABELS) as p (p)}
          <button type="button" class:on={preset === p} aria-pressed={preset === p} title={T.windowHint(PRESET_LABELS[p as WindowPreset])} onclick={() => pickPreset(p as WindowPreset)}>{PRESET_LABELS[p as WindowPreset]}</button>
        {/each}
      </div>
      <div class="bbc-seg" role="group" aria-label={T.viewLabel}>
        <button type="button" class:on={view === 'charts'} aria-pressed={view === 'charts'} title={T.viewChartsHint} onclick={() => { view = 'charts'; }}>{T.viewCharts}</button>
        <button type="button" class:on={view === 'happened'} aria-pressed={view === 'happened'} title={T.viewHappenedHint} onclick={() => { view = 'happened'; }}>{T.viewHappened}</button>
        <button type="button" class:on={view === 'latest'} aria-pressed={view === 'latest'} title={T.viewLatestHint} onclick={() => { view = 'latest'; }}>{T.viewLatest}</button>
        <button type="button" class:on={view === 'everything'} aria-pressed={view === 'everything'} title={T.viewEverythingHint} onclick={() => { view = 'everything'; }}>{T.viewEverything}</button>
      </div>
      {#if view === 'charts'}
        <div class="bbc-labelled">
          <span class="bbc-seg-label" aria-hidden="true" title={T.scaleHint}>{T.scale}</span>
          <div class="bbc-seg" role="group" aria-label={T.scale}>
            <button type="button" class:on={!log} aria-pressed={!log} title={T.linearHelp} onclick={() => { log = false; }}>{T.linear}</button>
            <button type="button" class:on={log} aria-pressed={log} title={T.logHelp} onclick={() => { log = true; }}>{T.log}</button>
          </div>
        </div>
        {#if answer && drawn.length}
          <div class="bbc-seg" role="group" aria-label={T.zoomGroup}>
            <button type="button" title={T.zoomEarlierHint} onclick={() => moveZoom('earlier')}>{T.zoomEarlier}</button>
            <button type="button" title={T.zoomLaterHint} onclick={() => moveZoom('later')}>{T.zoomLater}</button>
            <button type="button" title={T.zoomInHint} onclick={() => moveZoom('in')}>{T.zoomIn}</button>
            <button type="button" title={T.zoomOutHint} onclick={() => moveZoom('out')}>{T.zoomOut}</button>
          </div>
        {/if}
        {#if zoom}<button type="button" class="secondary-button" title={T.zoomReadHint} onclick={readZoom}>{T.zoomRead}</button>{/if}
        {#if narrow}<button type="button" class="secondary-button" aria-expanded={sideOpen} title={T.sideToggleHint} onclick={() => { sideOpen = !sideOpen; }}>{T.sideToggle(tab === 'processes' ? drawn.length : keys.length)}</button>{/if}
      {/if}
      {#if back.length}<button type="button" class="quiet-button" title={T.zoomBackHint} onclick={goBack}>{T.zoomBack}</button>{/if}
    </div>
    <!-- What the log scale means is written out while it is chosen, not only in a tooltip that keyboard and touch users never see (SWEEP-F2-S7). -->
    {#if view === 'charts' && log}<p class="bbc-note">{T.scaleHint}</p>{/if}

    <div class="bbc-main" class:wide={view !== 'charts'}>
      {#if view === 'charts'}
      <aside class="bbc-side" class:folded={narrow && !sideOpen}>
        <div class="bbc-tabs" role="group" aria-label={T.sideLabel}>
          <button type="button" aria-pressed={tab === 'counters'} class:on={tab === 'counters'} title={T.tabCountersHint} onclick={() => { tab = 'counters'; }}>{T.tabCounters}</button>
          <button type="button" aria-pressed={tab === 'processes'} class:on={tab === 'processes'} title={T.tabProcessesHint} onclick={() => { tab = 'processes'; }}>{T.tabProcesses}</button>
        </div>
        {#if tab === 'counters'}
          {#if quick.length}
            <div class="bbc-quick">
              <strong>{T.quickTitle}</strong>
              {#each quick as x (x.q.id)}
                <button type="button" class="bbc-q" title={x.q.hint} onclick={() => ask(x.q, x.keys)}>{x.q.label}</button>
              {/each}
            </div>
          {/if}
          <input class="bbc-search" type="search" placeholder={T.search} aria-label={T.search} title={T.searchHint} bind:value={search} />
          <div class="bbc-pick-actions">
            <button type="button" class="quiet-button" title={T.resetHint} onclick={() => { keys = answer ? defaultSelection(answer.lines) : []; }}>{T.reset}</button>
            <button type="button" class="quiet-button" title={T.clearHint} onclick={() => { keys = []; }}>{T.clear}</button>
          </div>
          <div class="bbc-list" role="group" aria-label={T.pickerTitle}>
            {#each shownGroups as g (g.group)}
              <details open={g.group === 'host' || !!search || g.lines.some((l) => keys.includes(l.key))}>
                <summary>{g.title} <span class="bbc-count">{g.lines.length}</span></summary>
                {#each g.lines as l (l.key)}
                  {@const c = colourOf.get(l.key)}
                  <label class="bbc-pick" title={meaningOf(l.key, l.unit)}>
                    <input type="checkbox" checked={keys.includes(l.key)} onchange={() => toggle(l.key)} />
                    <span class="bbc-swatch {c?.dash ?? 'solid'}" style:--sw={c?.color ?? 'transparent'} aria-hidden="true"></span>
                    <span>{seriesTitle(l.key)}</span>
                  </label>
                {/each}
              </details>
            {:else}
              {#if answer}<p class="bbc-note">{T.noMatch}</p>{/if}
            {/each}
          </div>
          {#if notRead.length || sqlNoteText}
            <div class="bbc-notread">
              <strong>{T.notReadGroups}</strong>
              <ul>{#each notRead as x (x.c.group)}<li>{own(GROUP_TITLES, x.c.group) ?? x.c.group}: {x.note}</li>{/each}{#if sqlNoteText}<li>{own(GROUP_TITLES, 'sqlDmv')}: {sqlNoteText}</li>{/if}</ul>
            </div>
          {/if}
        {:else}
          <label class="bbc-field">{T.procMetric}
            <select bind:value={procMetric} title={T.procMetricHint}>
              <option value="cpu">{T.procCpu}</option>
              <option value="workingSet">{T.procWs}</option>
              <option value="privateBytes">{T.procPriv}</option>
            </select>
          </label>
          <p class="bbc-note">{T.procHelp}</p>
        {/if}
      </aside>
      {/if}

      <section class="bbc-chart-area" aria-label={T.title(item.name)}>
        <div aria-live="polite">
          {#if busy}<p class="bbc-note" role="status">{T.reading}</p>{/if}
          {#if problem}<div class="inline-error" role="alert">{problem} <button type="button" class="secondary-button" title={T.retryHint} onclick={() => { attempt++; }}>{T.retry}</button></div>{/if}
        </div>

        {#if view === 'charts'}
          {#if answer && !problem}
            {#if answer.samples === 0}<p class="bbc-note">{T.noSamples}</p>
            {:else if !drawn.length}
              <div class="bbc-empty">
                <p class="bbc-empty-title">{tab === 'processes' ? T.procNone : T.pickSome}</p>
                {#if tab !== 'processes'}
                  <p class="bbc-empty-help">{T.pickSomeHelp}</p>
                  <button type="button" class="secondary-button" onclick={() => { keys = answer ? defaultSelection(answer.lines) : []; }}>{T.useDefault}</button>
                {/if}
              </div>
            {/if}
          {/if}
          <div class="bbc-canvas-wrap" role="img" aria-label={T.chartLabel(item.name, drawn.slice(0, 6).map((d) => d.title).join(', '), Math.max(0, drawn.length - 6))} style:height={height + 'px'} class:empty={!drawn.length || !!problem}>
            <div class="bbc-canvas" bind:this={node} bind:clientWidth={chartWidth}></div>
            <div class="bbc-legends" aria-hidden="true">
              {#each legends as lg, i (lg.unit)}
                <ul class="bbc-legend" style:top={(TOP + i * (STRIP_H + GAP) - 24) + 'px'} style:left={LEFT + 'px'}>
                  {#each lg.shown as n (n.key)}<li><span class="bbc-swatch {n.c?.dash ?? 'solid'}" style:--sw={n.c?.color ?? 'transparent'}></span><span class="bbc-lgt" title={n.title}>{n.title}</span></li>{/each}
                  {#if lg.more}<li class="bbc-legend-more">{T.legendMore(lg.more)}</li>{/if}
                </ul>
              {/each}
            </div>
          </div>
          {#if answer && !problem && drawn.length}
            <p class="bbc-note">
              {T.bucket(bucketWords(answer.bucketSeconds), bucketWords(answer.intervalSeconds || answer.bucketSeconds))}
              {#if answer.gaps.length}{T.gaps(answer.gaps.length)} {/if}
              {#if log && hidden}{T.logHidden(hidden)} {/if}
              {#if answer.read.dropped?.length || answer.read.missing?.length}{T.incomplete} {/if}
              {#if answer.truncated}{T.truncated} {/if}
              {#if tab === 'counters' && keys.length > MAX_LINES}{T.tooMany(MAX_LINES)} {/if}
              {T.zoomHelp}
            </p>
            <table class="bbc-figs">
              <caption class="bbc-cap">{T.meaningFocus}</caption>
              <thead><tr><th scope="col"></th><th scope="col">{tab === 'processes' ? T.colProgram : T.colCounter}</th><th scope="col">{T.last}</th><th scope="col">{T.average}</th><th scope="col">{T.lowest}</th><th scope="col">{T.highest}</th><th scope="col" title={T.highestAtHint}>{T.highestAt}</th><th scope="col"></th></tr></thead>
              <tbody>
                {#each figures as { d, f } (d.key)}
                  {@const c = colourOf.get(d.key)}
                  <tr class:focus={focusKey === d.key}>
                    <td><span class="bbc-swatch {c?.dash ?? 'solid'}" style:--sw={c?.color ?? 'transparent'} aria-hidden="true"></span></td>
                    <th scope="row"><button type="button" class="bbc-rowbtn" aria-pressed={focusKey === d.key} title={T.rowHint(d.title)} onclick={() => pickFocus(d.key)}>{d.title}</button></th>
                    <td>{formatValue(d.unit, f.last)}</td><td>{formatValue(d.unit, f.avg)}</td><td>{formatValue(d.unit, f.min)}</td><td>{formatValue(d.unit, f.max)}</td>
                    <td class="bbc-when">{whenText(f.maxAt)}</td>
                    <td>{#if tab === 'counters'}<button type="button" class="bbc-x" aria-label={T.remove(d.title)} title={T.remove(d.title)} onclick={() => toggle(d.key)}>×</button>{/if}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
            <section class="bbc-meaning" aria-labelledby="bbc-mean-title">
              <h3 id="bbc-mean-title">{T.meaningTitle}</h3>
              {#if focusLine}
                <p><strong>{focusLine.title}.</strong> {tab === 'processes' ? meaningOf(procMetric === 'cpu' ? 'cpuPct' : procMetric, '') : meaningOf(focusLine.key, lineByKey.get(focusLine.key)?.unit ?? '')}</p>
              {:else}<p class="bbc-note">{T.meaningEmpty}</p>{/if}
            </section>
          {/if}

        {:else if view === 'happened'}
          {#if answer && !problem}
            {#if answer.samples === 0}<p class="bbc-note">{T.noSamples}</p>{/if}
            <h3 class="bbc-h">{T.happenedTimeline}</h3>
            <p class="bbc-note">{T.happenedTimelineHint}</p>
            {@const w0 = Math.min(win.startMs, Date.parse(answer.start) || win.startMs)}
            {@const w1 = Math.max(win.endMs, Date.parse(answer.end) || win.endMs)}
            {@const pos = (t: number) => Math.max(0, Math.min(100, ((t - w0) / (w1 - w0)) * 100))}
            <div class="bbc-tl" role="group" aria-label={T.happenedTimeline}>
              <div class="bbc-lane"><span class="bbc-lane-name">{T.laneGaps}</span><div class="bbc-lane-track">
                {#each happened.drawn.gaps.filter((x) => x.end && x.end > x.t) as it (it.id)}
                  <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
                  <span tabindex="0" class="bbc-gapblock {it.tone}" style:left={pos(it.t) + '%'} style:width={Math.max(0.4, pos(it.end ?? it.t) - pos(it.t)) + '%'} title={whenText(it.t) + ': ' + it.title + '. ' + it.detail} role="img" aria-label={it.title}></span>
                {/each}
              </div></div>
              <div class="bbc-lane"><span class="bbc-lane-name">{T.laneDumps}</span><div class="bbc-lane-track">
                {#each happened.drawn.dumps as it (it.id)}
                  <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
                  <span class="bbc-mark dump" style:left={pos(it.t) + '%'} title={whenText(it.t) + ': ' + it.title + ' ' + it.detail} tabindex="0" role="img" aria-label={whenText(it.t) + ': ' + it.title + ' ' + it.detail}></span>
                {/each}
              </div></div>
              <div class="bbc-lane"><span class="bbc-lane-name">{T.laneRestarts}</span><div class="bbc-lane-track">
                {#each happened.drawn.restarts as it (it.id)}
                  <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
                  <span class="bbc-mark restart" style:left={pos(it.t) + '%'} title={whenText(it.t) + ': ' + it.title + ' ' + it.detail} tabindex="0" role="img" aria-label={whenText(it.t) + ': ' + it.title + ' ' + it.detail}></span>
                {/each}
              </div></div>
              <div class="bbc-lane"><span class="bbc-lane-name">{T.laneEvents}</span><div class="bbc-lane-track">
                {#each evDots.dots as e (e.t + ':' + e.level)}
                  <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
                  <span class="bbc-mark ev l{e.level}" style:left={pos(e.t) + '%'} title={whenText(e.t) + ': ' + (e.level === 1 ? 'Critical' : e.level === 2 ? 'Error' : e.level === 3 ? 'Warning' : 'Information') + ' events from Windows'} tabindex="0" role="img" aria-label={whenText(e.t) + ': Windows wrote events'}></span>
                {/each}
              </div></div>
              <div class="bbc-axis"><span>{whenText(w0)}</span><span>{whenText(w1)}</span></div>
            </div>
            {#if evDots.more}<p class="bbc-note">{T.drawnMore(evDots.more)}</p>{/if}

            <h3 class="bbc-h">{T.happenedList}</h3>
            {#if !happened.items.length}<p class="bbc-note">{T.happenedEmpty}</p>
            {:else}
              <table class="bbc-list-table">
                <thead><tr><th scope="col">{T.colWhen}</th><th scope="col">{T.colWhat}</th></tr></thead>
                <tbody>
                  {#each happened.items as it (it.id)}
                    <tr><td class="bbc-when">{whenText(it.t)}</td><td><div class="bbc-what"><span class="bbc-dot {it.tone}" aria-hidden="true"></span><div><strong>{it.title}</strong>{#if it.detail}<div class="bbc-sub">{it.detail}</div>{/if}</div></div></td></tr>
                  {/each}
                </tbody>
              </table>
              {#if happened.more}<p class="bbc-note">{T.happenedMore(happened.more)}</p>{/if}
              <p class="bbc-note">{T.untrustedHint}</p>
            {/if}

            <h3 class="bbc-h">{T.happenedEvents}</h3>
            <p class="bbc-note">{T.happenedEventsHint}</p>
            {#if !evRows.rows.length}<p class="bbc-note">{T.happenedEmpty}</p>
            {:else}
              <table class="bbc-list-table">
                <thead><tr><th scope="col">{T.colLog}</th><th scope="col">{T.colLevel}</th><th scope="col" class="num">{T.colId}</th><th scope="col" class="num">{T.colCount}</th><th scope="col">{T.colMeaningShort}</th></tr></thead>
                <tbody>
                  {#each evRows.rows as r (r.id)}
                    <tr><td>{r.log}</td><td>{r.level}</td><td class="num">{r.eventId}</td><td class="num">{r.count}</td><td>{r.meaning}</td></tr>
                  {/each}
                </tbody>
              </table>
              {#if evRows.more}<p class="bbc-note">{T.happenedMore(evRows.more)}</p>{/if}
            {/if}

            <h3 class="bbc-h">{T.happenedHealth}</h3>
            <table class="bbc-list-table">
              <tbody>{#each health as h (h.label)}<tr><th scope="row">{h.label}</th><td><span class="bbc-dot {h.tone}" aria-hidden="true"></span>{h.value}</td></tr>{/each}</tbody>
            </table>
          {/if}

        {:else if view === 'latest'}
          {#if answer && !problem}
            <p class="bbc-note">{T.latestHelp} {#if zoom}{T.latestZoomed}{/if}</p>
            {#if !compared.length}<p class="bbc-note">{T.latestEmpty}</p>
            {:else}
              <ul class="bbc-cmp">
                {#each compared as { d, c } (d.key)}
                  <li>
                    <div class="bbc-cmp-name">{d.title}</div>
                    {#if c}
                      <div class="bbc-bar-track" role="img" aria-label={d.title + ': ' + POSITION_WORDS[c.position] + '. ' + formatValue(d.unit, c.last) + ' now. Lowest ' + formatValue(d.unit, c.min) + ', highest ' + formatValue(d.unit, c.max) + '.'}>
                        <span class="bbc-bar-mark" style:left={(c.at * 100) + '%'}></span>
                      </div>
                      <div class="bbc-cmp-nums"><span>{formatValue(d.unit, c.min)}</span><strong>{formatValue(d.unit, c.last)} · {POSITION_WORDS[c.position]}</strong><span>{formatValue(d.unit, c.max)}</span></div>
                    {:else}<p class="bbc-note">{T.notRead}</p>{/if}
                  </li>
                {/each}
              </ul>
            {/if}
          {/if}

        {:else}
          {#if answer && !problem}
            <p class="bbc-note">{T.everythingHelp}</p>
            <input class="bbc-search" type="search" placeholder={T.search} aria-label={T.search} title={T.searchHint} bind:value={everySearch} />
            {#each allGroups as g (g.group)}
              <details class="bbc-eg" open={openGroups.includes(g.group) || !!everySearch} ontoggle={(e) => { const o = (e.currentTarget as HTMLDetailsElement).open; if (!everySearch && o !== openGroups.includes(g.group)) toggleGroup(g.group); }}>
                <summary>{g.title} <span class="bbc-count">{g.lines.length}</span></summary>
                {#if openGroups.includes(g.group) || everySearch}
                  <table class="bbc-list-table bbc-every">
                    <thead><tr><th scope="col">{T.colCounter}</th><th scope="col" class="num">{T.last}</th><th scope="col" class="num">{T.average}</th><th scope="col" class="num">{T.highest}</th><th scope="col">{T.colShape}</th><th scope="col">{T.colMeaning}</th></tr></thead>
                    <tbody>
                      {#each g.lines as l (l.key)}
                        {@const u = seriesUnit(l.key, l.unit)}
                        {@const f = lineFigures(l.points)}
                        <tr>
                          <th scope="row">{seriesTitle(l.key)}</th>
                          <td class="num">{formatValue(u, f.last)}</td><td class="num">{formatValue(u, f.avg)}</td><td class="num">{formatValue(u, f.max)}</td>
                          <td><svg class="bbc-spark" viewBox="0 0 80 18" aria-hidden="true"><path d={shapePath(l.points)} /></svg></td>
                          <td class="bbc-mean">{meaningOf(l.key, l.unit)}</td>
                        </tr>
                      {/each}
                    </tbody>
                  </table>
                {/if}
              </details>
            {:else}
              <p class="bbc-note">{answer.lines.length ? T.noMatch : T.everythingNone}</p>
            {/each}
          {/if}
        {/if}
      </section>
    </div>
  {/if}
</dialog>

<style>
  .bbc { width: min(1840px, calc(100vw - 32px)); max-width: none; height: calc(100vh - 32px); max-height: none; padding: 0; color: var(--text); background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius-md, 8px); display: flex; flex-direction: column; overflow: hidden; }
  .bbc:not([open]) { display: none; }
  .bbc::backdrop { background: var(--scrim); }
  .bbc-head { display: flex; justify-content: space-between; gap: var(--space-3); padding: var(--space-3) var(--space-4); border-bottom: 1px solid var(--line); }
  .bbc-head h2 { margin: 0; font-size: var(--fs-6); font-weight: 600; }
  .bbc-close { width: 32px; height: 32px; flex: 0 0 auto; padding: 0; color: var(--text-2); background: transparent; border: 1px solid transparent; border-radius: var(--radius-sm); font-size: 20px; }
  .bbc-close:hover { border-color: var(--line); }
  .bbc-body { padding: var(--space-4); }
  .bbc-note { margin: 4px 0 0; color: var(--text-2); font-size: 12px; line-height: 1.5; }
  .bbc-bar { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-3); padding: var(--space-2) var(--space-4); border-bottom: 1px solid var(--line); }
  .bbc-seg { display: inline-flex; border: 1px solid var(--line); border-radius: var(--radius-sm); overflow: hidden; }
  .bbc-seg button, .bbc-tabs button { padding: 5px 10px; font: inherit; font-size: 13px; color: var(--text-2); background: transparent; border: 0; border-right: 1px solid var(--line); cursor: pointer; }
  .bbc-seg button:last-child { border-right: 0; }
  .bbc-seg button.on, .bbc-tabs button.on { color: var(--text); background: var(--surface-2); font-weight: 600; }
  .bbc-main { flex: 1; min-height: 0; display: grid; grid-template-columns: 300px minmax(0, 1fr); }
  .bbc-side { display: flex; flex-direction: column; gap: var(--space-2); min-height: 0; padding: var(--space-3); border-right: 1px solid var(--line); overflow: auto; }
  /* The side column is a scrolling flex column and this strip has overflow: hidden (so it has no minimum height): without flex-shrink 0 a long list squeezed it to 12px (BL-496). */
  .bbc-tabs { display: flex; flex-shrink: 0; border: 1px solid var(--line); border-radius: var(--radius-sm); overflow: hidden; }
  .bbc-tabs button { flex: 1; }
  .bbc-tabs button:last-child { border-right: 0; }
  .bbc-search, .bbc-field select { width: 100%; box-sizing: border-box; padding: 6px 8px; font: inherit; font-size: 13px; color: var(--text); background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius-sm); }
  .bbc-field { display: grid; gap: 4px; font-size: 13px; font-weight: 500; }
  .bbc-pick-actions { display: flex; gap: var(--space-2); }
  .bbc-list details { border-top: 1px solid var(--line-soft, var(--line)); padding: 4px 0; }
  .bbc-list summary { cursor: pointer; font-size: 13px; font-weight: 600; padding: 4px 0; }
  .bbc-count { color: var(--text-2); font-weight: 400; font-size: 12px; }
  .bbc-pick { display: flex; align-items: center; gap: 6px; padding: 3px 0 3px 4px; font-size: 12.5px; line-height: 1.3; cursor: pointer; }
  /* A checkbox never shrinks beside a long name, so the names of all rows start at the same place. */
  .bbc-pick input { flex: 0 0 auto; }
  /* A swatch is the line itself: a solid bar, or a dashed or dotted one for a colour that a crowded chart has to use again. */
  .bbc-swatch { --sw: currentColor; display: inline-block; flex: 0 0 auto; width: 16px; height: 0; border-top: 3px solid var(--sw); vertical-align: middle; }
  .bbc-swatch.dashed { border-top-style: dashed; }
  .bbc-swatch.dotted { border-top-style: dotted; }
  .bbc-labelled { display: inline-flex; align-items: center; gap: var(--space-2); }
  .bbc-seg-label { color: var(--text-2); font-size: 12px; font-weight: 500; }
  .bbc-side.folded { display: none; }
  .bbc-canvas-wrap { position: relative; width: 100%; }
  .bbc-canvas-wrap .bbc-canvas { position: absolute; inset: 0; }
  .bbc-canvas-wrap.empty { visibility: hidden; height: 0 !important; }
  .bbc-legends { position: absolute; inset: 0; pointer-events: none; }
  .bbc-legend { position: absolute; display: flex; flex-wrap: nowrap; gap: var(--space-4); margin: 0; padding: 0; list-style: none; max-width: calc(100% - 100px); overflow: hidden; color: var(--text-2); font-size: 12px; white-space: nowrap; }
  .bbc-legend li { display: inline-flex; align-items: center; gap: 6px; min-width: 0; flex: 0 1 auto; }
  .bbc-lgt { min-width: 0; overflow: hidden; text-overflow: ellipsis; }
  .bbc-legend-more { color: var(--text-2); }
  .bbc-empty { display: grid; justify-items: center; gap: var(--space-2); padding: var(--space-8, 48px) var(--space-4); text-align: center; }
  .bbc-empty-title { margin: 0; font-size: 14px; font-weight: 600; }
  .bbc-empty-help { margin: 0 0 var(--space-2); color: var(--text-2); font-size: 13px; }
  .bbc-notread { font-size: 12px; color: var(--text-2); border-left: 3px solid var(--warn); padding-left: var(--space-2); }
  .bbc-notread ul { margin: 4px 0 0; padding-left: 16px; }
  .bbc-chart-area { min-width: 0; min-height: 0; overflow: auto; padding: var(--space-3) var(--space-4); }
  .bbc-chart-area .bbc-note { max-width: 110ch; }
  .bbc-figs { font-family: inherit; background: transparent; width: 100%; margin-top: var(--space-3); border-collapse: collapse; font-size: 12.5px; font-variant-numeric: tabular-nums; }
  .bbc-figs th, .bbc-figs td { font-family: inherit; background: transparent; letter-spacing: normal; text-transform: none; padding: 4px 8px; text-align: right; border-top: 1px solid var(--line); white-space: nowrap; }
  .bbc-figs th[scope="row"], .bbc-figs thead th:nth-child(2) { text-align: left; white-space: normal; font-weight: 500; }
  .bbc-figs thead th { color: var(--text-2); font-weight: 500; border-top: 0; }
  .bbc-x { box-sizing: border-box; min-width: 24px; min-height: 24px; padding: 0.45px 7.5px; margin: -0.45px -1.5px; color: var(--text-2); background: transparent; border: 0; font-size: 16px; cursor: pointer; }
  .bbc-head-actions { display: flex; align-items: flex-start; gap: var(--space-2); }
  .bbc-chips { display: flex; flex-wrap: wrap; gap: var(--space-2); list-style: none; margin: 6px 0 0; padding: 0; }
  .bbc-chip { display: inline-block; padding: 2px 10px; font-size: 12px; font-weight: 600; color: var(--text); background: var(--surface-2); border: 1px solid var(--line); border-radius: 999px; cursor: help; }
  .bbc-sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
  .bbc-chip.saved { border-color: var(--control-line); }
  .bbc-chip.late, .bbc-chip.partly { border-color: var(--warn); }
  .bbc-chip:focus-visible, .bbc-mark:focus-visible, .bbc-rowbtn:focus-visible, .bbc-seg button:focus-visible, .bbc-tabs button:focus-visible, .bbc-q:focus-visible { outline: 2px solid var(--focus); outline-offset: 1px; }
  .bbc-intro { margin: 0; padding: var(--space-2) var(--space-4); background: var(--surface-2); border-bottom: 1px solid var(--line); display: grid; grid-template-columns: auto minmax(0, 1fr) auto; align-items: center; gap: var(--space-3); }
  .bbc-intro h3 { margin: 0; font-size: var(--fs-5, 15px); }
  .bbc-intro ol { margin: 0; padding-left: 18px; font-size: 12.5px; line-height: 1.45; display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: var(--space-4); }
  .bbc-quick { display: grid; gap: 4px; }
  .bbc-quick strong { font-size: 12px; color: var(--text-2); font-weight: 600; }
  .bbc-q { text-align: left; padding: 5px 8px; font: inherit; font-size: 12.5px; color: var(--text); background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius-sm); cursor: pointer; }
  .bbc-q:hover { border-color: var(--control-line); }
  .bbc-main.wide { grid-template-columns: minmax(0, 1fr); }
  .bbc-h { margin: var(--space-4) 0 0; font-size: var(--fs-5, 15px); font-weight: 600; }
  .bbc-cap { caption-side: top; text-align: left; padding: 0 8px 4px; color: var(--text-2); font-size: 12px; }
  .bbc-rowbtn { box-sizing: border-box; min-height: 24px; padding: 3.5px 0; margin-block: -3.5px; font: inherit; color: inherit; text-align: left; background: transparent; border: 0; cursor: pointer; text-decoration: underline dotted; text-underline-offset: 3px; }
  .bbc-rowbtn[aria-pressed='true'] { font-weight: 700; text-decoration: underline; }
  tr.focus { background: var(--surface-2); }
  .bbc-when { color: var(--text-2); }
  .bbc-meaning { margin-top: var(--space-3); padding: var(--space-3); background: var(--surface-2); border-radius: var(--radius-sm); }
  .bbc-meaning h3 { margin: 0 0 4px; font-size: 13px; }
  .bbc-meaning p { margin: 0; font-size: 13px; line-height: 1.5; }
  .bbc-tl { display: grid; gap: 6px; margin-top: var(--space-2); }
  .bbc-lane { display: grid; grid-template-columns: 120px minmax(0, 1fr); align-items: center; gap: var(--space-2); font-size: 12px; }
  .bbc-lane-name { color: var(--text-2); }
  .bbc-lane-track { position: relative; height: 22px; background: var(--surface-2); border: 1px solid var(--line); border-radius: 3px; }
  .bbc-gapblock { position: absolute; top: 0; bottom: 0; background: repeating-linear-gradient(45deg, var(--pending) 0 3px, transparent 3px 6px); opacity: 0.8; }
  .bbc-gapblock.bad { background: repeating-linear-gradient(45deg, var(--bad) 0 3px, transparent 3px 6px); }
  .bbc-gapblock.warn { background: repeating-linear-gradient(45deg, var(--warn) 0 3px, transparent 3px 6px); }
  .bbc-mark { position: absolute; top: 50%; width: 10px; height: 10px; margin: -5px 0 0 -5px; }
  .bbc-mark.dump { background: var(--bad); transform: rotate(45deg); }
  .bbc-mark.restart { width: 4px; height: 16px; margin: -8px 0 0 -2px; background: var(--accent); border-radius: 1px; }
  .bbc-mark.ev { border-radius: 50%; background: var(--warn); }
  .bbc-mark.ev.l1, .bbc-mark.ev.l2 { background: var(--bad); }
  .bbc-mark.ev.l4 { background: var(--pending); }
  .bbc-axis { display: flex; justify-content: space-between; margin-left: 128px; font-size: 11px; color: var(--text-2); }
  .bbc-list-table { width: 100%; margin-top: var(--space-2); border-collapse: collapse; font-size: 12.5px; background: transparent; font-family: inherit; }
  .bbc-list-table th, .bbc-list-table td { padding: 4px 8px; text-align: left; vertical-align: top; border-top: 1px solid var(--line); background: transparent; font-family: inherit; letter-spacing: normal; text-transform: none; overflow-wrap: anywhere; }
  .bbc-list-table thead th { color: var(--text-2); font-weight: 500; border-top: 0; }
  .bbc-list-table th, .bbc-list-table td { border-bottom: 0; }
  .bbc-list-table .num { text-align: right; font-variant-numeric: tabular-nums; white-space: nowrap; }
  .bbc-list-table th[scope='row'] { font-weight: 500; }
  .bbc-what { display: flex; align-items: flex-start; gap: 6px; }
  .bbc-what .bbc-dot { margin: 5px 0 0; flex: 0 0 auto; }
  .bbc-sub { color: var(--text-2); }
  .bbc-dot { display: inline-block; width: 8px; height: 8px; margin-right: 6px; border-radius: 50%; background: var(--pending); }
  .bbc-dot.bad { background: var(--bad); }
  .bbc-dot.warn { background: var(--warn); }
  .bbc-cmp { list-style: none; margin: var(--space-2) 0 0; padding: 0; display: grid; gap: var(--space-3); }
  .bbc-cmp-name { font-size: 13px; font-weight: 600; }
  .bbc-bar-track { position: relative; height: 10px; margin: 6px 0 2px; background: var(--surface-2); border: 1px solid var(--control-line); border-radius: 5px; }
  .bbc-bar-mark { position: absolute; top: -5px; width: 6px; height: 18px; margin-left: -3px; background: var(--accent); border: 1px solid var(--surface); border-radius: 2px; }
  .bbc-cmp-nums { display: flex; justify-content: space-between; gap: var(--space-2); font-size: 12px; color: var(--text-2); font-variant-numeric: tabular-nums; }
  .bbc-cmp-nums strong { color: var(--text); font-weight: 600; text-align: center; }
  .bbc-eg { border-top: 1px solid var(--line-soft, var(--line)); padding: 4px 0; }
  .bbc-eg summary { cursor: pointer; font-weight: 600; font-size: 13px; padding: 4px 0; }
  .bbc-every .bbc-mean { color: var(--text-2); min-width: 240px; }
  .bbc-spark { width: 80px; height: 18px; fill: none; stroke: var(--series-1); stroke-width: 1.4; }
  @media (max-width: 899px) {
    .bbc-intro { grid-template-columns: minmax(0, 1fr); }
    .bbc-intro ol { grid-template-columns: minmax(0, 1fr); gap: 2px; }
    .bbc-main { grid-template-columns: 1fr; }
    /* The chart comes first and the list of counters under it. */
    .bbc-side { order: 2; border-right: 0; border-top: 1px solid var(--line); max-height: none; overflow: visible; }
    .bbc-lane { grid-template-columns: 84px minmax(0, 1fr); }
    .bbc-axis { margin-left: 92px; }
  }
  /* A small or zoomed window: the whole pop-up scrolls as one, so the chart is never squeezed between two small scrolling boxes (BLACKBOX-06). */
  @media (max-width: 899px), (max-height: 799px) {
    .bbc { display: block; overflow: auto; }
    .bbc-head { position: static; }
    .bbc-main { overflow: visible; }
    .bbc-chart-area { overflow: visible; }
    .bbc-side { overflow: visible; max-height: none; }
  }
  @media (max-width: 639px) {
    .bbc { width: 100vw; height: 100vh; height: 100dvh; max-height: 100dvh; border-radius: 0; border: 0; }
    .bbc-legend { max-width: calc(100% - 40px); }
  }
</style>
