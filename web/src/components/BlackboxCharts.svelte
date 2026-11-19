<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { MarkAreaComponent } from 'echarts/components';
  import { echarts, palette } from '../lib/charts';
  import { theme } from '../lib/theme.svelte';
  import { chartTime } from '../lib/chartTime';
  import { blackboxApi, refusalText, shownTime, type BlackboxItem } from '../lib/blackbox';
  import { BB_REFUSAL } from '../lib/copy/blackboxCopy';
  import { BBC_TEXT as T } from '../lib/copy/blackboxChartsCopy';
  import {
    PRESET_LABELS, UNIT_TITLES, axisValue, chartData, gapRanges, coverNote, defaultSelection, firstPreset, formatValue, lineFigures, pickerGroups,
    alignedRequest, presetWindow, readSeriesAnswer, seriesTitle, seriesUnit, unitStrips, GROUP_TITLES,
    type SeriesAnswer, type SeriesLine, type SeriesPoint, type SeriesUnit, type WindowPreset
  } from '../lib/blackboxSeries';

  // The Blackbox charts view: what one Blackbox recorded, drawn like Perfmon (pick counters, linear or log scale, a figures bar
  // under the chart). The numbers come from POST /api/blackbox/{id}/series, which reads the recordings with the same reader as the
  // host check. Nothing is collected here. A value that was not read is a break in the line, never 0. Times are UTC, like the panel.
  echarts.use([MarkAreaComponent]);

  let { item, onClose }: { item: BlackboxItem; onClose: () => void } = $props();

  const ZONE = 'UTC';
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
  let log = $state(saved.log === true);
  let keys = $state<string[]>(Array.isArray(saved.keys) ? saved.keys : []);
  let tab = $state<'counters' | 'processes'>('counters');
  let procMetric = $state<'cpu' | 'workingSet' | 'privateBytes'>('cpu');
  let search = $state('');
  let zoom = $state<Win | null>(null);

  let dialog = $state<HTMLDialogElement>();
  let node = $state<HTMLDivElement>();
  let chart = $state<ReturnType<typeof echarts.init>>();

  onMount(() => {
    dialog?.showModal();
    if (!node) return;
    const target = node;
    const observer = new ResizeObserver(() => {
      if (!chart) {
        if (target.clientWidth > 0 && target.clientHeight > 0) {
          chart = echarts.init(target);
          chart.on('datazoom', onZoom);
        }
        return;
      }
      chart.resize();
    });
    observer.observe(target);
    return () => { observer.disconnect(); chart?.dispose(); };
  });

  // One read per window. A newer window cancels the older read.
  $effect(() => {
    const w = win;
    void attempt;
    if (!w) return;
    const ctl = new AbortController();
    untrack(() => { busy = true; problem = ''; zoom = null; });
    const req = alignedRequest(w.startMs, w.endMs, facts?.effectiveIntervalSeconds || facts?.intervalSeconds || 15, POINTS);
    blackboxApi.series(item.id, { start: new Date(req.startMs).toISOString(), end: new Date(req.endMs).toISOString(), points: req.points }, ctl.signal)
      .then((raw) => {
        const a = readSeriesAnswer(raw);
        if (!a) { problem = T.badAnswer; answer = null; return; }
        answer = a;
        const have = new Set(a.lines.map((l) => l.key));
        const kept = untrack(() => keys).filter((k) => have.has(k));
        if (!kept.length) keys = defaultSelection(a.lines);
      })
      .catch((e: unknown) => {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        problem = refusalText(e, BB_REFUSAL) || T.readFailed;
      })
      .finally(() => { if (!ctl.signal.aborted) busy = false; });
    return () => ctl.abort();
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
    return keys.map((k) => byKey.get(k)).filter((l): l is SeriesLine => !!l).map((l) => ({ key: l.key, title: seriesTitle(l.key), unit: seriesUnit(l.key, l.unit), points: l.points }));
  });
  const strips = $derived(unitStrips(drawn.map((d) => d.key), drawn.map((d) => ({ key: d.key, group: '', label: '', unit: d.unit, points: [] }))));
  const groups = $derived(answer ? pickerGroups(answer.lines) : []);
  const shownGroups = $derived.by(() => {
    const q = search.trim().toLowerCase();
    if (!q) return groups;
    return groups.map((g) => ({ ...g, lines: g.lines.filter((l) => (seriesTitle(l.key) + ' ' + l.key + ' ' + g.title).toLowerCase().includes(q)) })).filter((g) => g.lines.length);
  });
  const notRead = $derived((answer?.groups ?? []).map((c) => ({ c, note: answer && answer.samples > 0 ? coverNote(c) : null })).filter((x) => x.note));
  const colourOf = $derived.by(() => {
    const p = (void theme.epoch, palette());
    return new Map(drawn.map((d, i) => [d.key, { color: p.series[i % p.series.length], dashed: i >= p.series.length }]));
  });
  let hidden = $state(0);

  const STRIP_H = 150;
  const TOP = 22;
  const GAP = 44;
  const BOTTOM = 56;
  const height = $derived(Math.max(1, strips.length) * (STRIP_H + GAP) - GAP + TOP + BOTTOM);

  const ticks = (w: Win) => {
    const longSpan = w.endMs - w.startMs > 20 * 3_600_000;
    return (v: number) => chartTime(v, ZONE, { seconds: false, zone: false, date: longSpan });
  };

  $effect(() => {
    if (!chart) return;
    void theme.epoch;
    const p = palette();
    const a = answer;
    const w = win;
    if (!a || !w || !strips.length) { chart.clear(); hidden = 0; return; }
    const byKey = new Map(drawn.map((d) => [d.key, d]));
    const lookup = new Map(drawn.map((d) => [d.key, new Map(d.points.map((pt) => [pt[0], pt]))]));
    const axis = { axisLine: { lineStyle: { color: p.axisLine } }, axisLabel: { color: p.text2 }, nameTextStyle: { color: p.text2 }, splitLine: { lineStyle: { color: p.grid } } };
    const gapAreas = a.gaps.map((g) => [{ xAxis: Date.parse(g.from), name: T.gapLabel }, { xAxis: Date.parse(g.to) }]);
    const markerLines = a.markers.map((m) => ({ xAxis: Date.parse(m.t), name: m.kind }));
    let hiddenNow = 0;
    const grids = strips.map((_, i) => ({ left: 86, right: 24, top: TOP + i * (STRIP_H + GAP), height: STRIP_H }));
    const xAxes = strips.map((_, i) => ({
      type: 'time', gridIndex: i, min: Math.min(w.startMs, Date.parse(a.start) || w.startMs), max: Math.max(w.endMs, Date.parse(a.end) || w.endMs), ...axis, splitLine: { show: false },
      axisLabel: { color: p.text2, formatter: ticks(w), hideOverlap: true }
    }));
    const yAxes = strips.map((s, i) => ({
      type: log ? 'log' : 'value', gridIndex: i, name: UNIT_TITLES[s.unit], nameLocation: 'middle', nameGap: 70, scale: !log && s.unit !== 'pct',
      min: !log && (s.unit === 'pct' || s.unit === 'count' || s.unit === 'perSec' || s.unit === 'bytes' || s.unit === 'bytesPerSec') ? 0 : undefined,
      ...axis, axisLabel: { color: p.text2, formatter: (v: number) => axisValue(s.unit, v) }
    }));
    const series: unknown[] = [];
    strips.forEach((s, i) => {
      s.keys.forEach((k, j) => {
        const d = byKey.get(k)!;
        const c = colourOf.get(k);
        const cd = chartData(d.points, gapRanges(a.gaps), log);
        hiddenNow += cd.hidden;
        series.push({
          type: 'line', name: d.title, id: k, xAxisIndex: i, yAxisIndex: i, data: cd.data, connectNulls: false, showSymbol: cd.data.length < 40, symbolSize: 4,
          sampling: 'lttb', lineStyle: { width: 1.6, color: c?.color, type: c?.dashed ? 'dashed' : 'solid' }, itemStyle: { color: c?.color },
          markArea: j === 0 && gapAreas.length ? { silent: true, itemStyle: { color: p.empty, opacity: 0.5 }, label: { show: i === 0, position: 'insideTop', color: p.text2, fontSize: 11 }, data: gapAreas } : undefined,
          markLine: j === 0 && markerLines.length ? { silent: true, symbol: 'none', lineStyle: { color: p.warn, type: 'dotted' }, label: { show: i === 0, formatter: '{b}', color: p.text2, fontSize: 11 }, data: markerLines } : undefined
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
        { type: 'slider', xAxisIndex: all, filterMode: 'none', ...keep, bottom: 8, height: 22, labelFormatter: ticks(w), textStyle: { color: p.text2 } }
      ],
      tooltip: {
        trigger: 'axis', confine: true, renderMode: 'richText', axisPointer: { type: 'line' },
        formatter: (raw: unknown) => {
          const items = (Array.isArray(raw) ? raw : [raw]) as Array<{ seriesId: string; seriesName: string; value: [number, number | null] }>;
          if (!items.length) return '';
          const t = items[0].value[0];
          const rows = items.map((it) => {
            const d = byKey.get(it.seriesId);
            const pt = lookup.get(it.seriesId)?.get(t);
            if (!d) return '';
            if (!pt) return it.seriesName + ': ' + T.notRead;
            const range = pt[2] !== pt[3] ? ' (' + formatValue(d.unit, pt[2]) + ' to ' + formatValue(d.unit, pt[3]) + ')' : '';
            return it.seriesName + ': ' + formatValue(d.unit, pt[1]) + range;
          }).filter(Boolean);
          return chartTime(t, ZONE) + '\n' + rows.join('\n');
        }
      }
    }, true);
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
      <p class="bbc-note">{[facts?.firstSample && facts?.lastSample ? T.recordedSpan(shownTime(facts.firstSample), shownTime(facts.lastSample)) : '', T.timesUtc, T.sameAsPerfmon].filter(Boolean).join(' ')}</p>
    </div>
    <button type="button" class="bbc-close" aria-label={T.close} onclick={() => dialog?.close()}>×</button>
  </header>

  {#if !win}
    <p class="bbc-body">{T.nothingRecorded}</p>
  {:else}
    <div class="bbc-bar">
      <div class="bbc-seg" role="group" aria-label={T.window}>
        {#each Object.keys(PRESET_LABELS) as p (p)}
          <button type="button" class:on={preset === p} aria-pressed={preset === p} onclick={() => pickPreset(p as WindowPreset)}>{PRESET_LABELS[p as WindowPreset]}</button>
        {/each}
      </div>
      <div class="bbc-seg" role="group" aria-label={T.scale} title={T.logHelp}>
        <button type="button" class:on={!log} aria-pressed={!log} onclick={() => { log = false; }}>{T.linear}</button>
        <button type="button" class:on={log} aria-pressed={log} onclick={() => { log = true; }}>{T.log}</button>
      </div>
      {#if zoom}<button type="button" class="secondary-button" onclick={readZoom}>{T.zoomRead}</button>{/if}
      {#if back.length}<button type="button" class="quiet-button" onclick={goBack}>{T.zoomBack}</button>{/if}
    </div>

    <div class="bbc-main">
      <aside class="bbc-side">
        <div class="bbc-tabs" role="tablist">
          <button type="button" role="tab" aria-selected={tab === 'counters'} class:on={tab === 'counters'} onclick={() => { tab = 'counters'; }}>{T.tabCounters}</button>
          <button type="button" role="tab" aria-selected={tab === 'processes'} class:on={tab === 'processes'} onclick={() => { tab = 'processes'; }}>{T.tabProcesses}</button>
        </div>
        {#if tab === 'counters'}
          <input class="bbc-search" type="search" placeholder={T.search} aria-label={T.search} bind:value={search} />
          <div class="bbc-pick-actions">
            <button type="button" class="quiet-button" onclick={() => { keys = answer ? defaultSelection(answer.lines) : []; }}>{T.reset}</button>
            <button type="button" class="quiet-button" onclick={() => { keys = []; }}>{T.clear}</button>
          </div>
          <div class="bbc-list" aria-label={T.pickerTitle}>
            {#each shownGroups as g (g.group)}
              <details open={g.group === 'host' || !!search || g.lines.some((l) => keys.includes(l.key))}>
                <summary>{g.title} <span class="bbc-count">{g.lines.length}</span></summary>
                {#each g.lines as l (l.key)}
                  {@const c = colourOf.get(l.key)}
                  <label class="bbc-pick" title={l.key}>
                    <input type="checkbox" checked={keys.includes(l.key)} onchange={() => toggle(l.key)} />
                    <span class="bbc-swatch" style:background={c?.color ?? 'transparent'}></span>
                    <span>{seriesTitle(l.key)}</span>
                  </label>
                {/each}
              </details>
            {:else}
              {#if answer}<p class="bbc-note">{T.noMatch}</p>{/if}
            {/each}
          </div>
          {#if notRead.length}
            <div class="bbc-notread">
              <strong>{T.notReadGroups}</strong>
              <ul>{#each notRead as x (x.c.group)}<li>{GROUP_TITLES[x.c.group] ?? x.c.group}: {x.note}</li>{/each}</ul>
            </div>
          {/if}
        {:else}
          <label class="bbc-field">{T.procMetric}
            <select bind:value={procMetric}>
              <option value="cpu">{T.procCpu}</option>
              <option value="workingSet">{T.procWs}</option>
              <option value="privateBytes">{T.procPriv}</option>
            </select>
          </label>
          <p class="bbc-note">{T.procHelp}</p>
        {/if}
      </aside>

      <section class="bbc-chart-area" aria-label={T.title(item.name)}>
        <div aria-live="polite">
          {#if busy}<p class="bbc-note" role="status">{T.reading}</p>{/if}
          {#if problem}<div class="inline-error" role="alert">{problem} <button type="button" class="secondary-button" onclick={() => { attempt++; }}>{T.retry}</button></div>{/if}
        </div>
        {#if answer && !problem}
          {#if answer.samples === 0}<p class="bbc-note">{T.noSamples}</p>
          {:else if !drawn.length}<p class="bbc-note">{tab === 'processes' ? T.procNone : T.pickSome}</p>{/if}
        {/if}
        <div class="bbc-canvas" bind:this={node} style:height={height + 'px'} class:empty={!drawn.length || !!problem}></div>
        {#if answer && !problem && drawn.length}
          <p class="bbc-note">
            {T.bucket(bucketWords(answer.bucketSeconds), bucketWords(answer.intervalSeconds || answer.bucketSeconds))}
            {#if answer.gaps.length}{T.gaps(answer.gaps.length)} {/if}
            {#if log && hidden}{T.logHidden(hidden)} {/if}
            {#if answer.read.dropped?.length || answer.read.missing?.length}{T.incomplete} {/if}
            {#if answer.truncated}{T.truncated} {/if}
            {T.zoomHelp}
          </p>
          <table class="bbc-figs">
            <thead><tr><th scope="col"></th><th scope="col">{tab === 'processes' ? T.colProgram : T.colCounter}</th><th scope="col">{T.last}</th><th scope="col">{T.average}</th><th scope="col">{T.lowest}</th><th scope="col">{T.highest}</th><th scope="col"></th></tr></thead>
            <tbody>
              {#each figures as { d, f } (d.key)}
                {@const c = colourOf.get(d.key)}
                <tr>
                  <td><span class="bbc-swatch" class:dashed={c?.dashed} style:background={c?.color}></span></td>
                  <th scope="row" title={d.key}>{d.title}</th>
                  <td>{formatValue(d.unit, f.last)}</td><td>{formatValue(d.unit, f.avg)}</td><td>{formatValue(d.unit, f.min)}</td><td>{formatValue(d.unit, f.max)}</td>
                  <td>{#if tab === 'counters'}<button type="button" class="bbc-x" aria-label={T.remove(d.title)} onclick={() => toggle(d.key)}>×</button>{/if}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        {/if}
      </section>
    </div>
  {/if}
</dialog>

<style>
  .bbc { width: min(1400px, calc(100vw - 32px)); max-width: none; height: calc(100vh - 32px); max-height: none; padding: 0; color: var(--text); background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius-md, 8px); display: flex; flex-direction: column; overflow: hidden; }
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
  .bbc-tabs { display: flex; border: 1px solid var(--line); border-radius: var(--radius-sm); overflow: hidden; }
  .bbc-tabs button { flex: 1; }
  .bbc-tabs button:last-child { border-right: 0; }
  .bbc-search, .bbc-field select { width: 100%; box-sizing: border-box; padding: 6px 8px; font: inherit; font-size: 13px; color: var(--text); background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius-sm); }
  .bbc-field { display: grid; gap: 4px; font-size: 13px; font-weight: 500; }
  .bbc-pick-actions { display: flex; gap: var(--space-2); }
  .bbc-list details { border-top: 1px solid var(--line-soft, var(--line)); padding: 4px 0; }
  .bbc-list summary { cursor: pointer; font-size: 13px; font-weight: 600; padding: 4px 0; }
  .bbc-count { color: var(--text-2); font-weight: 400; font-size: 12px; }
  .bbc-pick { display: flex; align-items: center; gap: 6px; padding: 3px 0 3px 4px; font-size: 12.5px; line-height: 1.3; cursor: pointer; }
  .bbc-swatch { display: inline-block; flex: 0 0 auto; width: 10px; height: 10px; border-radius: 2px; }
  .bbc-swatch.dashed { height: 3px; }
  .bbc-notread { font-size: 12px; color: var(--text-2); border-left: 3px solid var(--warn); padding-left: var(--space-2); }
  .bbc-notread ul { margin: 4px 0 0; padding-left: 16px; }
  .bbc-chart-area { min-width: 0; min-height: 0; overflow: auto; padding: var(--space-3) var(--space-4); }
  .bbc-canvas { width: 100%; }
  .bbc-canvas.empty { visibility: hidden; height: 0 !important; }
  .bbc-figs { font-family: inherit; background: transparent; width: 100%; margin-top: var(--space-3); border-collapse: collapse; font-size: 12.5px; font-variant-numeric: tabular-nums; }
  .bbc-figs th, .bbc-figs td { font-family: inherit; background: transparent; letter-spacing: normal; text-transform: none; padding: 4px 8px; text-align: right; border-top: 1px solid var(--line); white-space: nowrap; }
  .bbc-figs th[scope="row"], .bbc-figs thead th:nth-child(2) { text-align: left; white-space: normal; font-weight: 500; }
  .bbc-figs thead th { color: var(--text-2); font-weight: 500; border-top: 0; }
  .bbc-x { padding: 0 6px; color: var(--text-2); background: transparent; border: 0; font-size: 16px; cursor: pointer; }
  @media (max-width: 899px) {
    .bbc-main { grid-template-columns: 1fr; overflow: auto; }
    .bbc-side { border-right: 0; border-bottom: 1px solid var(--line); max-height: 40vh; }
  }
</style>
