import * as echarts from 'echarts/core';
import { LineChart, BarChart, HeatmapChart, ScatterChart } from 'echarts/charts';
import { GridComponent, TooltipComponent, DataZoomComponent, VisualMapComponent, LegendComponent, MarkLineComponent } from 'echarts/components';
import { CanvasRenderer } from 'echarts/renderers';
import type { Analysis, Comparison, Point } from './types';
import { axisLabels, chartTime } from './chartTime';
import { axisDuration, niceHistogram } from './chartScale';
import { gapBridge, heroOutliers, type HeroSlot } from './chartHero';
import { MIN_SAMPLE } from './thresholds';

echarts.use([LineChart, BarChart, HeatmapChart, ScatterChart, GridComponent, TooltipComponent, DataZoomComponent, VisualMapComponent, LegendComponent, MarkLineComponent, CanvasRenderer]);
export { echarts };

/**
 * Chart colours come from the CSS tokens so the charts follow the theme. ECharts draws on a canvas and cannot read CSS variables itself,
 * so this resolves them to concrete colours when an option is built. Without a document (tests, server) the light fallback is used.
 */
export interface Palette {
  text: string; accent: string; accentSoft: string; warn: string; bad: string; pending: string; text2: string; axisLine: string; grid: string;
  empty: string; heat: string[]; series: string[]; surface: string;
}
const fallbackPalette: Palette = {
  text: '#151b24', accent: '#2457c5', accentSoft: '#9db6ea', warn: '#96600a', bad: '#b02a3d', pending: '#5b6878', text2: '#4d5a6b', axisLine: '#d5dbe3', grid: '#e6eaf0',
  empty: '#edf0f4', heat: ['#dce9f6', '#83bdb5', '#e4ae5b', '#b85a42'], surface: '#ffffff',
  series: ['#146b58', '#2457c5', '#ad6511', '#9956a1', '#b02a3d', '#157d94', '#6e6f24', '#76563f']
};
export const palette = (): Palette => {
  if (typeof document === 'undefined' || typeof getComputedStyle !== 'function') return fallbackPalette;
  const style = getComputedStyle(document.documentElement);
  const read = (name: string, fallback: string) => style.getPropertyValue(name).trim() || fallback;
  return {
    text: read('--text', fallbackPalette.text), accent: read('--accent', fallbackPalette.accent), accentSoft: withAlpha(read('--accent', fallbackPalette.accent), .42), warn: read('--warn', fallbackPalette.warn),
    bad: read('--bad', fallbackPalette.bad), pending: read('--pending', fallbackPalette.pending), text2: read('--text-2', fallbackPalette.text2),
    axisLine: read('--line', fallbackPalette.axisLine), grid: read('--line-soft', fallbackPalette.grid), surface: read('--surface', fallbackPalette.surface),
    empty: read('--chart-empty', fallbackPalette.empty),
    heat: fallbackPalette.heat.map((fallback, index) => read('--heat-' + (index + 1), fallback)),
    series: fallbackPalette.series.map((fallback, index) => read('--series-' + (index + 1), fallback))
  };
};
const finite = (value: number | null | undefined) => value != null && Number.isFinite(value) ? value : null;
const escapeHTML = (value: string) => value.replace(/[&<>"']/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char] ?? char);
/** Axis labels land on round clock times (15:30, 16:00), in the chart's zone. They are written by chartTime.ts, the one place timestamps are formatted. */
const axisTimeLabels = (points: Point[], zone: string) => axisLabels(points.map(point => Date.parse(point.at)), zone);
const tooltipTimestamp = (value: string | number, zone: string) => escapeHTML(chartTime(value, zone));
const comparisonStateLabels: Record<string, string> = {
  compared: 'Compared',
  missing_current: 'No current samples',
  missing_reference: 'No reference samples',
  insufficient_data: 'Too few samples',
  undefined_baseline: 'No usable reference'
};
const comparisonStateLabel = (state: string) => comparisonStateLabels[state] ?? state.replaceAll('_', ' ');

export const countEpochBuckets = (startMs: number, endMs: number, bucketSeconds: number) => {
  if (!Number.isFinite(startMs) || !Number.isFinite(endMs) || startMs >= endMs || bucketSeconds <= 0) return 0;
  const widthMs = bucketSeconds * 1000;
  return Math.ceil(endMs / widthMs) - Math.floor(startMs / widthMs);
};

export const alignedPoints = (analysis: Analysis, cohortKey: string | null): Point[] => {
  const widthMs = Math.max(1, analysis.query.bucketSeconds) * 1000;
  const startMs = Date.parse(analysis.query.start);
  const endMs = Date.parse(analysis.query.end);
  if (!Number.isFinite(startMs) || !Number.isFinite(endMs) || startMs >= endMs) return [];
  const series = cohortKey == null ? undefined : analysis.timeline.find(item => item.key === cohortKey);
  const observations = new Map((series?.points ?? []).map(point => [Date.parse(point.at), point]));
  const aligned: Point[] = [];
  for (let at = Math.floor(startMs / widthMs) * widthMs; at < endMs; at += widthMs) {
    aligned.push(observations.get(at) ?? { at: new Date(at).toISOString(), count: null, p50: null, p95: null, p95Ratio: null });
  }
  return aligned;
};

export type FailureField = 'count4xx' | 'count5xx' | 'incomplete';
/** A slot's failure counter: null when the slot has no point (no observation), 0 only when a real point carried none. */
export const failureField = (point: Point | undefined, field: FailureField): number | null => point == null || point.count == null ? null : (point[field] ?? 0);
/**
 * One value per chart slot for one cohort, or summed across every cohort when
 * cohortKey is null. A slot that no selected cohort has a point for is null, never zero.
 */
export const failureSeries = (analysis: Analysis, cohortKey: string | null, field: FailureField): Array<number | null> => {
  if (cohortKey != null) return alignedPoints(analysis, cohortKey).map(point => failureField(point, field));
  const perCohort = analysis.timeline.map(series => alignedPoints(analysis, series.key));
  return alignedPoints(analysis, null).map((_, index) => perCohort.reduce<number | null>((total, points) => {
    const value = failureField(points[index], field);
    return value == null ? total : (total ?? 0) + value;
  }, null));
};

/** Failure counts per slot for a set of cohorts: every cohort when keys is null, otherwise just those summed. A slot none of them has a point for stays null. */
export const failureSeriesFor = (analysis: Analysis, keys: string[] | null, field: FailureField): Array<number | null> => {
  if (keys == null) return failureSeries(analysis, null, field);
  if (keys.length === 1) return failureSeries(analysis, keys[0], field);
  const perCohort = keys.map(key => alignedPoints(analysis, key));
  return alignedPoints(analysis, null).map((_, index) => perCohort.reduce<number | null>((total, points) => {
    const value = failureField(points[index], field);
    return value == null ? total : (total ?? 0) + value;
  }, null));
};

/** The one zoom slider: a thin hairline track in the line colour, no readout, no chunky handles. Inside (wheel/drag) zoom is added next to it. */
const zoomSlider = (p: Palette) => ({ type: 'slider', height: 14, bottom: 8, showDetail: false, borderColor: p.axisLine, backgroundColor: 'transparent', fillerColor: withAlpha(p.accent, .18), handleSize: 14, handleStyle: { color: p.accent, borderColor: p.accent }, moveHandleStyle: { color: p.axisLine }, dataBackground: { lineStyle: { opacity: 0 }, areaStyle: { opacity: 0 } }, selectedDataBackground: { lineStyle: { opacity: 0 }, areaStyle: { opacity: 0 } }, textStyle: { color: p.text2 } });
const tipStyle = (p: Palette) => ({ backgroundColor: p.surface, borderColor: p.axisLine, borderWidth: 1, padding: [6, 10], extraCssText: 'border-radius:4px;box-shadow:none;backdrop-filter:none;', textStyle: { color: p.text, fontSize: 12 } });
const withAlpha =(color: string, alpha: number) => /^#[0-9a-f]{6}$/i.test(color) ? color + Math.round(alpha * 255).toString(16).padStart(2, '0') : color;

export const failureTrendOption = (analysis: Analysis, zone: string, cohortKeys: string[] | null = null) => {
  const p = palette(), axis = p.text2;
  const points = alignedPoints(analysis, null);
  const labels = axisTimeLabels(points, zone);
  const client = failureSeriesFor(analysis, cohortKeys, 'count4xx');
  const server = failureSeriesFor(analysis, cohortKeys, 'count5xx');
  const incomplete = failureSeriesFor(analysis, cohortKeys, 'incomplete');
  const text = (value: number | null) => value == null ? 'No observations' : value.toLocaleString();
  return {
    animation: false,
    grid: { left: 76, right: 18, top: 40, bottom: 58 },
    tooltip: {
      ...tipStyle(p), trigger: 'axis',
      formatter: (items: Array<{ dataIndex: number }>) => {
        const index = items[0]?.dataIndex ?? 0;
        return (points[index] ? tooltipTimestamp(points[index].at, zone) + '<br/>' : '') +
          '4xx client errors: ' + text(client[index] ?? null) + '<br/>5xx server errors and failed jobs: ' + text(server[index] ?? null) +
          ((incomplete[index] ?? 0) > 0 ? '<br/>Missing timing (not a failure): ' + (incomplete[index] ?? 0).toLocaleString() : '');
      }
    },
    legend: { type: 'scroll', orient: 'horizontal', top: 2, left: 76, right: 12, itemGap: 10, pageIconSize: 9, textStyle: { color: p.text2, fontSize: 12 } },
    xAxis: { type: 'category', data: labels, boundaryGap: false, axisLabel: { color: axis, interval: 0, hideOverlap: true }, axisLine: { lineStyle: { color: p.axisLine } } },
    yAxis: { type: 'value', name: 'Responses per interval', nameLocation: 'middle', nameGap: 44, min: 0, minInterval: 1, axisLabel: { color: axis }, splitLine: { lineStyle: { color: p.grid } } },
    dataZoom: [{ type: 'inside', zoomOnMouseWheel: false }, zoomSlider(p)],
    series: [
      { name: '4xx client errors', type: 'line', connectNulls: false, showSymbol: true, symbolSize: 6, data: client, lineStyle: { color: p.warn, width: 2.5 }, itemStyle: { color: p.warn } },
      { name: '5xx server errors and failed jobs', type: 'line', connectNulls: false, showSymbol: true, symbolSize: 6, data: server, lineStyle: { color: p.bad, width: 2.5 }, itemStyle: { color: p.bad } }
    ]
  };
};

const heroDuration = axisDuration;
/**
 * The pulse's one timeline: requests per interval as pale bars behind, and (only when one request type is in scope, never pooled) the P95 as the
 * focal line on a second axis. An interval with no timed requests is an empty slot, not a zero latency: the line stops there and a dashed bridge
 * joins the neighbours. The slowest request of each interval is a small hollow dot, and one far above the usual P95 is a labelled diamond, so a
 * one-minute outlier does not read as noise.
 */
export const heroOption = (windows: HeroSlot[], zone: string, withLatency: boolean) => {
  const p = palette(), axis = p.text2;
  const labels = axisLabels(windows.map(window => window.at), zone);
  const latency = windows.map(window => finite(window.latency));
  const outliers = withLatency ? heroOutliers(windows) : { typical: null, flagged: [] as number[], labelled: [] as number[] };
  const flagged = new Set(outliers.flagged), labelled = new Set(outliers.labelled);
  const slowest = windows.map((window, index) => {
    const max = finite(window.max);
    if (max == null) return null;
    return flagged.has(index)
      ? { value: max, symbol: 'diamond', symbolSize: 11, itemStyle: { color: p.bad }, label: { show: labelled.has(index), position: 'top', distance: 4, color: p.bad, fontSize: 11, formatter: () => axisDuration(max) } }
      : { value: max, symbol: 'circle', symbolSize: 5, itemStyle: { color: 'transparent', borderColor: p.text2, borderWidth: 1, opacity: .7 } };
  });
  return {
    animation: false,
    grid: { left: 40, right: withLatency ? 52 : 14, top: 22, bottom: 26 },
    tooltip: {
      ...tipStyle(p), trigger: 'axis',
      formatter: (items: Array<{ dataIndex: number }>) => {
        const index = items[0]?.dataIndex ?? 0;
        const window = windows[index];
        if (!window) return '';
        return tooltipTimestamp(window.at, zone) + '<br/>Requests: ' + window.count.toLocaleString() +
          (withLatency ? '<br/>P95: ' + (window.latency == null ? 'no timed requests' : '≈ ' + heroDuration(window.latency)) +
            (window.max != null ? '<br/>Slowest request: ' + heroDuration(window.max) + (flagged.has(index) ? ' (far above the usual P95)' : '') : '') : '');
      }
    },
    xAxis: { type: 'category', data: labels, axisTick: { show: false }, axisLabel: { color: axis, interval: 0, hideOverlap: true }, axisLine: { lineStyle: { color: p.axisLine } } },
    yAxis: [
      { type: 'value', min: 0, minInterval: 1, splitNumber: 4, axisLabel: { color: axis }, splitLine: { lineStyle: { color: p.grid } } },
      ...(withLatency ? [{ type: 'value', min: 0, minInterval: 1, splitNumber: 4, position: 'right', axisLabel: { color: p.accent, formatter: (value: number) => heroDuration(value) }, splitLine: { show: false } }] : [])
    ],
    series: [
      { name: 'Requests', type: 'bar', yAxisIndex: 0, data: windows.map(window => window.count), itemStyle: { color: withAlpha(p.accent, .3) }, barCategoryGap: '24%' },
      ...(withLatency ? [
        { name: 'P95 bridge', type: 'line', yAxisIndex: 1, silent: true, showSymbol: false, data: gapBridge(latency), lineStyle: { color: p.accent, width: 1.5, type: 'dashed', opacity: .7 }, tooltip: { show: false }, z: 2 },
        { name: 'P95', type: 'line', yAxisIndex: 1, connectNulls: false, showSymbol: true, symbolSize: 5, data: latency, lineStyle: { color: p.accent, width: 2 }, itemStyle: { color: p.accent }, z: 3 },
        { name: 'Slowest request', type: 'scatter', yAxisIndex: 1, data: slowest, z: 4 }
      ] : [])
    ]
  };
};

export const histogramBinBounds =(bin: number): { lower: number; upper: number | null } => {
  if (bin === 0) return { lower: 0, upper: 1 };
  if (bin >= 1 && bin <= 192) return { lower: Math.pow(1.1, bin - 1), upper: Math.pow(1.1, bin) };
  if (bin === 193) return { lower: Math.pow(1.1, 192), upper: null };
  throw new RangeError('Histogram bin must be between 0 and 193.');
};
const formatMs = (value: number) => Number(value.toPrecision(8)).toLocaleString('en-US');
export const histogramBinLabel = (bin: number) => {
  const bounds = histogramBinBounds(bin);
  return bounds.upper == null
    ? '≥ ' + formatMs(bounds.lower) + ' ms (overflow)'
    : '[' + formatMs(bounds.lower) + ', ' + formatMs(bounds.upper) + ') ms';
};
const quantileText = (q: Point['p95']) => {
  if (!q) return 'N/A';
  const range = q.upperMs == null ? '≥ ' + q.lowerMs.toFixed(1) : q.lowerMs.toFixed(1) + '–' + q.upperMs.toFixed(1);
  return '≈ ' + (q.valueMs == null ? range : q.valueMs.toFixed(1) + ' ms') + ' (bin ' + range + ' ms)';
};
const quantileIntervalText = (label: string, q: Point['p50']) => {
  if (!q) return label + ': N/A';
  const bounds = q.upperMs == null ? '≥ ' + q.lowerMs.toFixed(1) : q.lowerMs.toFixed(1) + '–' + q.upperMs.toFixed(1);
  return label + ': ' + (q.valueMs == null ? 'N/A' : '≈ ' + q.valueMs.toFixed(2) + ' ms') + ' (bin ' + bounds + ' ms)';
};

export const timelineOption = (analysis: Analysis, selected: Comparison | null, zone: string, targetMs: number | null) => {
  const p = palette(), axis = p.text2;
  const points = alignedPoints(analysis, selected?.key ?? null);
  const labels = axisTimeLabels(points, zone);
  const chosen = selected;
  return {
    animation: false,
    grid: { left: 84, right: 18, top: 62, bottom: 58 },
    tooltip: {
      ...tipStyle(p), trigger: 'axis',
      formatter: (items: Array<{ seriesName: string; data: number | null; dataIndex: number }>) => {
        const index = items[0]?.dataIndex ?? 0;
        const point = points[index];
        return (point ? tooltipTimestamp(point.at, zone) + '<br/>Requests: ' + (point.count ?? 'N/A') + '<br/>' : '') +
          items.map(item => item.seriesName + ': ' + (item.data == null ? 'N/A' : '≈ ' + item.data.toFixed(2) + ' ms')).join('<br/>') +
          (point ? '<br/>' + quantileIntervalText('Median', point.p50) + '<br/>95th percentile: ' + quantileText(point.p95) : '');
      }
    },
    legend: { type: 'scroll', orient: 'horizontal', top: 2, left: 76, right: 12, itemGap: 10, pageIconSize: 9, textStyle: { color: p.text2, fontSize: 12 } },
    xAxis: { type: 'category', data: labels, boundaryGap: false, axisLabel: { color: axis, interval: 0, hideOverlap: true }, axisLine: { lineStyle: { color: p.axisLine } } },
    yAxis: { type: 'value', name: 'Response time', nameLocation: 'middle', nameGap: 52, min: 0, minInterval: 1, axisLabel: { color: axis, formatter: (value: number) => axisDuration(value) }, splitLine: { lineStyle: { color: p.grid } } },
    dataZoom: [{ type: 'inside', zoomOnMouseWheel: false }, zoomSlider(p)],
    series: [
      { name: 'Current median', type: 'line', connectNulls: false, showSymbol: true, symbolSize: 6, data: points.map(point => finite(point.p50?.valueMs)), lineStyle: { color: p.text2, width: 2.5 }, itemStyle: { color: p.text2 } },
      { name: 'Current p95', type: 'line', connectNulls: false, showSymbol: true, symbolSize: 6, data: points.map(point => finite(point.p95?.valueMs)), lineStyle: { color: p.accent, width: 2.5 }, itemStyle: { color: p.accent } },
      { name: 'Reference median', type: 'line', showSymbol: false, data: labels.map(() => finite(chosen?.reference.p50?.valueMs)), lineStyle: { color: p.text2, type: 'dashed', opacity: .55 }, itemStyle: { color: p.text2 } },
      { name: 'Reference p95', type: 'line', showSymbol: false, data: labels.map(() => finite(chosen?.reference.p95?.valueMs)), lineStyle: { color: p.accent, type: 'dashed', opacity: .55 }, itemStyle: { color: p.accent } },
      ...(targetMs == null ? [] : [{ name: 'Response-time target', type: 'line', showSymbol: false, data: labels.map(() => targetMs), lineStyle: { color: p.series[0], type: 'dotted', width: 2 }, itemStyle: { color: p.series[0] } }])
    ]
  };
};

export const ratioOption = (analysis: Analysis, selected: Comparison | null, zone: string) => {
  const p = palette(), axis = p.text2;
  const points = alignedPoints(analysis, selected?.key ?? null);
  const labels = axisTimeLabels(points, zone);
  return {
    animation: false,
    grid: { left: 76, right: 18, top: 22, bottom: 58 },
    tooltip: { ...tipStyle(p), trigger: 'axis', formatter: (items: Array<{ data: number | null; dataIndex: number }>) => {
      const item = items[0]; const point = points[item.dataIndex];
      return (point ? tooltipTimestamp(point.at, zone) + '<br/>' : '') +
        '95th-percentile response time vs reference: ' + (item.data == null ? 'N/A' : '≈ ' + item.data.toFixed(2) + '×') +
        '<br/>Observed requests: ' + (point?.count ?? 'N/A');
    } },
    xAxis: { type: 'category', data: labels, boundaryGap: false, axisLabel: { color: axis, interval: 0, hideOverlap: true } },
    yAxis: { type: 'value', name: 'Response time vs reference', nameLocation: 'middle', nameGap: 48, min: 0, axisLabel: { color: axis, formatter: (value: number) => Number(value.toFixed(2)) + '×' }, splitLine: { lineStyle: { color: p.grid } } },
    dataZoom: [{ type: 'inside' }, zoomSlider(p)],
    series: [{ type: 'line', name: 'Response time vs reference', showSymbol: false, connectNulls: false, data: points.map(point => finite(point.p95Ratio)), lineStyle: { color: p.accent, width: 2.5 }, markLine: { symbol: 'none', lineStyle: { type: 'dashed', color: p.pending }, data: [{ yAxis: 1 }] } }]
  };
};

export const histogramOption = (comparison: Comparison | null) => {
  const p = palette(), axis = p.text2;
  const bins = new Map<number, { current: number; reference: number }>();
  for (const item of comparison?.current.histogram ?? []) bins.set(item.bin, { current: item.count, reference: 0 });
  for (const item of comparison?.reference.histogram ?? []) {
    const current = bins.get(item.bin) ?? { current: 0, reference: 0 };
    current.reference = item.count; bins.set(item.bin, current);
  }
  // The server counts in ranges 10 % wider than the last; the chart regroups them into ranges with round edges so the axis reads 50-100 ms, not 49.8-54.8.
  const grouped = niceHistogram([...bins.entries()].sort((x, y) => x[0] - y[0]).map(([bin, counts]) => ({ ...histogramBinBounds(bin), ...counts })));
  const labels = grouped.map(bin => bin.label);
  return {
    animation: false,
    grid: { left: 70, right: 18, top: 62, bottom: 64 },
    tooltip: { ...tipStyle(p), trigger: 'axis', formatter: (items: Array<{ data: number; seriesName: string; dataIndex: number }>) =>
      escapeHTML(labels[items[0]?.dataIndex ?? -1] ?? '') + '<br/>' + items.map(item => item.seriesName + ': ' + item.data).join('<br/>') },
    legend: { type: 'scroll', orient: 'horizontal', top: 2, left: 70, right: 12, itemGap: 10, pageIconSize: 9, textStyle: { color: p.text2, fontSize: 12 } },
    xAxis: { type: 'category', name: 'Response-time range', nameLocation: 'middle', nameGap: 44, data: labels, axisLabel: { color: axis, rotate: labels.length > 7 ? 25 : 0, interval: 0, hideOverlap: true } },
    yAxis: { type: 'value', name: 'Requests', minInterval: 1, nameLocation: 'middle', nameGap: 38, axisLabel: { color: axis }, splitLine: { lineStyle: { color: p.grid } } },
    series: [
      { name: 'Current', type: 'bar', data: grouped.map(bin => bin.current), itemStyle: { color: p.accent }, barMaxWidth: 22 },
      { name: 'Reference', type: 'bar', data: grouped.map(bin => bin.reference), itemStyle: { color: p.accentSoft }, barMaxWidth: 22 }
    ]
  };
};

export const orderedHeatmapCohorts = (analysis: Analysis) => analysis.apis.slice().sort((a, b) =>
  a.sourceId.localeCompare(b.sourceId) || a.controller.localeCompare(b.controller) || a.action.localeCompare(b.action) || a.kind.localeCompare(b.kind) || a.outcome.localeCompare(b.outcome));
export const heatmapCohortKeyAt = (analysis: Analysis, rowIndex: number) => orderedHeatmapCohorts(analysis)[rowIndex]?.key ?? null;

export const heatmapOption = (analysis: Analysis, sources: Map<string, string>, zone: string) => {
  const p = palette(), axis = p.text2;
  const rows = orderedHeatmapCohorts(analysis);
  const timePoints = alignedPoints(analysis, null);
  const timeLabels = axisTimeLabels(timePoints, zone);
  const max = Math.max(1, ...analysis.timeline.flatMap(series => series.points.map(point => finite(point.p95Ratio) ?? 0)));
  const pointsByCohort = new Map(rows.map(row => [row.key, alignedPoints(analysis, row.key)]));
  const data = rows.flatMap((row, rowIndex) => (pointsByCohort.get(row.key) ?? []).map((point, timeIndex) => ({
    name: row.key,
    value: [timeIndex, rowIndex, finite(point.p95Ratio)],
    itemStyle: point.p95Ratio == null ? { color: p.empty } : undefined
  })));
  return {
    animation: false,
    grid: { left: 205, right: 32, top: 12, bottom: 70 },
    tooltip: { ...tipStyle(p), position: 'top', formatter: (item: { value: [number, number, number | null] }) => {
      const [timeIndex, rowIndex, ratio] = item.value;
      const row = rows[rowIndex];
      const point = pointsByCohort.get(row?.key ?? '')?.[timeIndex];
      return row ? escapeHTML(sources.get(row.sourceId) ?? row.sourceId) + ' / ' + escapeHTML(row.controller) + '.' + escapeHTML(row.action) +
        '<br/>' + escapeHTML(row.kind) + ' · ' + escapeHTML(row.outcome) +
        '<br/>' + tooltipTimestamp(timePoints[timeIndex]?.at ?? '', zone) +
        '<br/>95th-percentile response time vs reference: ' + (ratio == null ? 'N/A' : '≈ ' + ratio.toFixed(2) + '×') +
        '<br/>Estimated interval: ' + quantileText(point?.p95 ?? null) +
        '<br/>Comparison: ' + escapeHTML(comparisonStateLabel(row.state)) + '<br/>Current events: ' + row.currentEvents.toLocaleString() : '';
    } },
    xAxis: { type: 'category', data: timeLabels, axisLabel: { color: axis, interval: 0, hideOverlap: true }, axisLine: { lineStyle: { color: p.axisLine } } },
    yAxis: { type: 'category', data: rows.map(row => (sources.get(row.sourceId) ?? row.sourceId) + ' / ' + row.controller + '.' + row.action + ' / ' + row.kind + ' / ' + row.outcome), axisLabel: { color: p.text2, width: 195, overflow: 'truncate' } },
    visualMap: { min: 0, max, calculable: true, orient: 'horizontal', left: 'center', bottom: 0, inRange: { color: p.heat }, outOfRange: { color: p.empty }, textStyle: { color: axis } },
    series: [{ type: 'heatmap', data, label: { show: false }, emphasis: { itemStyle: { shadowBlur: 8, shadowColor: withAlpha(p.accent, .4) } } }]
  };
};

export type PercentileSeries = 'p50' | 'p90' | 'p95' | 'p99';
export type FailureSeries = 'count4xx' | 'count5xx';
export type TimelineSeries = PercentileSeries | FailureSeries;
export const timelineSeriesOptions: Array<{ value: TimelineSeries; label: string }> = [
  { value: 'p50', label: 'P50 (median)' }, { value: 'p90', label: 'P90' }, { value: 'p95', label: 'P95' }, { value: 'p99', label: 'P99' },
  { value: 'count4xx', label: '4xx responses' }, { value: 'count5xx', label: '5xx responses and failed jobs' }
];
export const isFailureSeries = (series: TimelineSeries): series is FailureSeries => series === 'count4xx' || series === 'count5xx';
export const baselineChange = (row: Comparison, point: Point | undefined, mode: 'percent' | 'milliseconds', series: PercentileSeries = 'p95'): number | null => {
  const current = finite(point?.[series]?.valueMs);
  const reference = finite(row.reference[series]?.valueMs);
  if (current == null || reference == null) return null;
  if (mode === 'milliseconds') return current - reference;
  if (reference <= 0 || (row.reference[series]?.lowerMs ?? 0) <= 0) return null;
  return (current / reference - 1) * 100;
};
/** The categorical series colours of the current theme; read again whenever the theme changes. */
export const seriesColors = () => palette().series;
/**
 * Line style of the i-th compared group. Groups that have identical values would hide each other, so each gets its own dash pattern and a
 * thinner stroke than the one before; later groups draw on top of earlier, wider ones and every line stays visible.
 */
export const timelineLineStyle = (index: number): { width: number; type: 'solid' | 'dashed' | 'dotted' } =>
  ({ width: Math.max(1.5, 3.5 - index * .5), type: (['solid', 'dashed', 'dotted'] as const)[index % 3] });
/** The same pattern as an SVG stroke-dasharray, for the key beside the chart. */
export const timelineDashArray = (index: number) => ({ solid: '', dashed: '6 3', dotted: '1.5 3' })[timelineLineStyle(index).type];
export const multiTimelineOption = (analysis: Analysis, keys: string[], sources: Map<string, string>, zone: string, mode: 'percent' | 'milliseconds', series: TimelineSeries = 'p95') => {
  const p = palette(), axis = p.text2;
  const failure = isFailureSeries(series);
  const seriesLabel = timelineSeriesOptions.find(item => item.value === series)?.label ?? series;
  // A percentile is a change from its own reference; a failure count is absolute, and null where the slot has no point.
  const valueAt = (row: Comparison, point: Point | undefined) => isFailureSeries(series) ? failureField(point, series) : baselineChange(row, point, mode, series);
  const rows = keys.flatMap(key => { const row = analysis.apis.find(item => item.key === key); return row ? [row] : []; });
  const aligned = rows.map(row => alignedPoints(analysis, row.key));
  const slots = aligned[0] ?? [];
  const displaySlots = slots.map(point => ({ ...point, at: new Date(Math.max(Date.parse(point.at), Date.parse(analysis.query.start))).toISOString() }));
  const labels = axisTimeLabels(slots, zone);
  return {
    animation: false,
    grid: { left: 90, right: 78, top: 20, bottom: 64 },
    tooltip: { ...tipStyle(p), trigger: 'axis', confine: true, formatter: (items: Array<{ dataIndex: number }>) => {
      const index = items[0]?.dataIndex ?? 0;
      return (displaySlots[index] ? tooltipTimestamp(displaySlots[index].at, zone) + '<br/>' : '') + rows.map((row, i) => {
        const point = aligned[i][index];
        const change = baselineChange(row, point, mode, failure ? 'p95' : series as PercentileSeries);
        const reliable = (point?.count ?? 0) >= MIN_SAMPLE && row.state === 'compared';
        if (failure) return `<b>${i + 1}. ${escapeHTML(row.controller)}.${escapeHTML(row.action)}</b><br/>${escapeHTML(sources.get(row.sourceId) ?? row.sourceId)} · ${escapeHTML(row.kind)} · ${escapeHTML(row.outcome)}<br/>${seriesLabel}: ${point?.count == null ? 'No observations' : String(failureField(point, series as FailureSeries))}${(point?.incomplete ?? 0) > 0 ? ' · ' + point?.incomplete + ' missing timing (not counted as failures)' : ''}`;
        return `<b>${i + 1}. ${escapeHTML(row.controller)}.${escapeHTML(row.action)}</b><br/>${escapeHTML(sources.get(row.sourceId) ?? row.sourceId)} · ${escapeHTML(row.kind)} · ${escapeHTML(row.outcome)}<br/>Change: ${change == null ? 'Unavailable' : (change > 0 ? '+' : '') + change.toFixed(1) + (mode === 'percent' ? '%' : ' ms')} · Usable timings: ${point?.count ?? 0}<br/>Current ${series.toUpperCase()}: ${finite(point?.[series as PercentileSeries]?.valueMs)?.toFixed(1) ?? 'N/A'} ms · Reference ${series.toUpperCase()}: ${finite(row.reference[series as PercentileSeries]?.valueMs)?.toFixed(1) ?? 'N/A'} ms${reliable ? '' : '<br/>Few or missing samples — treat this point cautiously.'}`;
      }).join('<br/><br/>');
    } },
    xAxis: { type: 'category', data: labels, boundaryGap: false, axisLabel: { color: axis, interval: 0, hideOverlap: true } },
    yAxis: { type: 'value', name: failure ? 'Responses per interval' : mode === 'percent' ? 'Change from own reference (%)' : 'Change from own reference', nameLocation: 'middle', nameGap: 62, ...(failure ? { min: 0, minInterval: 1 } : mode === 'milliseconds' ? { minInterval: 1 } : {}), axisLabel: { color: axis, ...(!failure && mode === 'milliseconds' ? { formatter: (value: number) => (value > 0 ? '+' : '') + axisDuration(value) } : {}) }, splitLine: { lineStyle: { color: p.grid } } },
    dataZoom: [{ type: 'inside', zoomOnMouseWheel: false }, zoomSlider(p)],
    series: rows.map((row, i) => ({ name: `${i + 1}. ${row.controller}.${row.action}`, type: 'line', connectNulls: false, showSymbol: true, symbolSize: 6, z: 3 + i, lineStyle: { color: p.series[i], ...timelineLineStyle(i) }, itemStyle: { color: p.series[i] },
      data: aligned[i].map(point => ({ value: valueAt(row, point), symbol: !failure && ((point.count ?? 0) < MIN_SAMPLE || row.state !== 'compared') ? 'emptyCircle' : 'circle', itemStyle: { opacity: !failure && ((point.count ?? 0) < MIN_SAMPLE || row.state !== 'compared') ? .45 : 1 } })),
      ...(i === 0 && !failure ? { markLine: { symbol: 'none', label: { formatter: 'Reference', position: 'end', color: p.text2, fontSize: 11 }, lineStyle: { type: 'dashed', color: p.pending }, data: [{ yAxis: 0 }] } } : {})
    }))
  };
};
