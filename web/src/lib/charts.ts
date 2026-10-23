import * as echarts from 'echarts/core';
import { LineChart, BarChart, HeatmapChart, ScatterChart } from 'echarts/charts';
import { GridComponent, TooltipComponent, DataZoomComponent, VisualMapComponent, LegendComponent, MarkLineComponent } from 'echarts/components';
import { CanvasRenderer } from 'echarts/renderers';
import type { Analysis, Comparison, Point } from './types';

echarts.use([LineChart, BarChart, HeatmapChart, ScatterChart, GridComponent, TooltipComponent, DataZoomComponent, VisualMapComponent, LegendComponent, MarkLineComponent, CanvasRenderer]);
export { echarts };

const cobalt = '#315bd4';
const amber = '#c17a22';
const teal = '#238277';
const axis = '#78869a';
const finite = (value: number | null | undefined) => value != null && Number.isFinite(value) ? value : null;
const escapeHTML = (value: string) => value.replace(/[&<>"']/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char] ?? char);
const axisDateFormatter = (zone: string) => new Intl.DateTimeFormat(undefined, { timeZone: zone, year: 'numeric', month: 'short', day: '2-digit' });
const axisClockFormatter = (zone: string) => new Intl.DateTimeFormat(undefined, { timeZone: zone, hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' });
const axisTimeLabels = (points: Point[], zone: string) => {
  const dateFormatter = axisDateFormatter(zone);
  const clockFormatter = axisClockFormatter(zone);
  const dates = points.map(point => dateFormatter.format(new Date(point.at)));
  const clocks = points.map(point => clockFormatter.format(new Date(point.at)));
  const includeDate = new Set(dates).size > 1;
  return points.map((_, index) => includeDate ? dates[index] + ' ' + clocks[index] : clocks[index]);
};
const tooltipTimestamp = (value: string, zone: string) =>
  new Intl.DateTimeFormat(undefined, { timeZone: zone, year: 'numeric', month: 'short', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' }).format(new Date(value)) + ' (' + escapeHTML(zone) + ')';
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

const red = '#b74545';
export const failureTrendOption = (analysis: Analysis, zone: string) => {
  const points = alignedPoints(analysis, null);
  const labels = axisTimeLabels(points, zone);
  const client = failureSeries(analysis, null, 'count4xx');
  const server = failureSeries(analysis, null, 'count5xx');
  const incomplete = failureSeries(analysis, null, 'incomplete');
  const text = (value: number | null) => value == null ? 'No observations' : value.toLocaleString();
  return {
    animation: false,
    grid: { left: 76, right: 18, top: 40, bottom: 58 },
    tooltip: {
      trigger: 'axis',
      formatter: (items: Array<{ dataIndex: number }>) => {
        const index = items[0]?.dataIndex ?? 0;
        return (points[index] ? tooltipTimestamp(points[index].at, zone) + '<br/>' : '') +
          '4xx client errors: ' + text(client[index] ?? null) + '<br/>5xx server errors and failed jobs: ' + text(server[index] ?? null) +
          ((incomplete[index] ?? 0) > 0 ? '<br/>Missing timing (not a failure): ' + (incomplete[index] ?? 0).toLocaleString() : '');
      }
    },
    legend: { type: 'scroll', orient: 'horizontal', top: 2, left: 76, right: 12, itemGap: 10, pageIconSize: 9, textStyle: { color: '#526176', fontSize: 11 } },
    xAxis: { type: 'category', data: labels, boundaryGap: false, axisLabel: { color: axis, hideOverlap: true }, axisLine: { lineStyle: { color: '#dbe2eb' } } },
    yAxis: { type: 'value', name: 'Responses per interval', nameLocation: 'middle', nameGap: 44, min: 0, minInterval: 1, axisLabel: { color: axis }, splitLine: { lineStyle: { color: '#edf0f5' } } },
    dataZoom: [{ type: 'inside', zoomOnMouseWheel: false }, { type: 'slider', height: 16, bottom: 8, borderColor: 'transparent', fillerColor: '#315bd422' }],
    series: [
      { name: '4xx client errors', type: 'line', connectNulls: false, showSymbol: true, symbolSize: 6, data: client, lineStyle: { color: amber, width: 2.5 }, itemStyle: { color: amber } },
      { name: '5xx server errors and failed jobs', type: 'line', connectNulls: false, showSymbol: true, symbolSize: 6, data: server, lineStyle: { color: red, width: 2.5 }, itemStyle: { color: red } }
    ]
  };
};

export const histogramBinBounds = (bin: number): { lower: number; upper: number | null } => {
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
  const points = alignedPoints(analysis, selected?.key ?? null);
  const labels = axisTimeLabels(points, zone);
  const chosen = selected;
  return {
    animation: false,
    grid: { left: 76, right: 18, top: 62, bottom: 58 },
    tooltip: {
      trigger: 'axis',
      formatter: (items: Array<{ seriesName: string; data: number | null; dataIndex: number }>) => {
        const index = items[0]?.dataIndex ?? 0;
        const point = points[index];
        return (point ? tooltipTimestamp(point.at, zone) + '<br/>Requests: ' + (point.count ?? 'N/A') + '<br/>' : '') +
          items.map(item => item.seriesName + ': ' + (item.data == null ? 'N/A' : '≈ ' + item.data.toFixed(2) + ' ms')).join('<br/>') +
          (point ? '<br/>' + quantileIntervalText('Median', point.p50) + '<br/>95th percentile: ' + quantileText(point.p95) : '');
      }
    },
    legend: { type: 'scroll', orient: 'horizontal', top: 2, left: 76, right: 12, itemGap: 10, pageIconSize: 9, textStyle: { color: '#526176', fontSize: 10 } },
    xAxis: { type: 'category', data: labels, boundaryGap: false, axisLabel: { color: axis, hideOverlap: true }, axisLine: { lineStyle: { color: '#dbe2eb' } } },
    yAxis: { type: 'value', name: 'Response time (ms)', nameLocation: 'middle', nameGap: 44, min: 0, axisLabel: { color: axis }, splitLine: { lineStyle: { color: '#edf0f5' } } },
    dataZoom: [{ type: 'inside', zoomOnMouseWheel: false }, { type: 'slider', height: 16, bottom: 8, borderColor: 'transparent', fillerColor: '#315bd422' }],
    series: [
      { name: 'Current median', type: 'line', connectNulls: false, showSymbol: true, symbolSize: 6, data: points.map(point => finite(point.p50?.valueMs)), lineStyle: { color: cobalt, width: 2.5 }, itemStyle: { color: cobalt } },
      { name: 'Current p95', type: 'line', connectNulls: false, showSymbol: true, symbolSize: 6, data: points.map(point => finite(point.p95?.valueMs)), lineStyle: { color: amber, width: 2.5 }, itemStyle: { color: amber } },
      { name: 'Reference median', type: 'line', showSymbol: false, data: labels.map(() => finite(chosen?.reference.p50?.valueMs)), lineStyle: { color: cobalt, type: 'dashed', opacity: .55 }, itemStyle: { color: cobalt } },
      { name: 'Reference p95', type: 'line', showSymbol: false, data: labels.map(() => finite(chosen?.reference.p95?.valueMs)), lineStyle: { color: amber, type: 'dashed', opacity: .55 }, itemStyle: { color: amber } },
      ...(targetMs == null ? [] : [{ name: 'Response-time target', type: 'line', showSymbol: false, data: labels.map(() => targetMs), lineStyle: { color: teal, type: 'dotted', width: 2 }, itemStyle: { color: teal } }])
    ]
  };
};

export const ratioOption = (analysis: Analysis, selected: Comparison | null, zone: string) => {
  const points = alignedPoints(analysis, selected?.key ?? null);
  const labels = axisTimeLabels(points, zone);
  return {
    animation: false,
    grid: { left: 76, right: 18, top: 22, bottom: 58 },
    tooltip: { trigger: 'axis', formatter: (items: Array<{ data: number | null; dataIndex: number }>) => {
      const item = items[0]; const point = points[item.dataIndex];
      return (point ? tooltipTimestamp(point.at, zone) + '<br/>' : '') +
        '95th-percentile response time vs reference: ' + (item.data == null ? 'N/A' : '≈ ' + item.data.toFixed(2) + '×') +
        '<br/>Observed requests: ' + (point?.count ?? 'N/A');
    } },
    xAxis: { type: 'category', data: labels, boundaryGap: false, axisLabel: { color: axis, hideOverlap: true } },
    yAxis: { type: 'value', name: 'Response time vs reference', nameLocation: 'middle', nameGap: 48, min: 0, axisLabel: { color: axis }, splitLine: { lineStyle: { color: '#edf0f5' } } },
    dataZoom: [{ type: 'inside' }, { type: 'slider', height: 16, bottom: 8, borderColor: 'transparent', fillerColor: '#315bd422' }],
    series: [{ type: 'line', name: 'Response time vs reference', showSymbol: false, connectNulls: false, data: points.map(point => finite(point.p95Ratio)), lineStyle: { color: teal, width: 2.5 }, markLine: { symbol: 'none', lineStyle: { type: 'dashed', color: '#9ba7b5' }, data: [{ yAxis: 1 }] } }]
  };
};

export const histogramOption = (comparison: Comparison | null) => {
  const bins = new Map<number, { current: number; reference: number }>();
  for (const item of comparison?.current.histogram ?? []) bins.set(item.bin, { current: item.count, reference: 0 });
  for (const item of comparison?.reference.histogram ?? []) {
    const current = bins.get(item.bin) ?? { current: 0, reference: 0 };
    current.reference = item.count; bins.set(item.bin, current);
  }
  const indices = [...bins.keys()].sort((a, b) => a - b);
  const labels = indices.map(histogramBinLabel);
  return {
    animation: false,
    grid: { left: 70, right: 18, top: 62, bottom: 64 },
    tooltip: { trigger: 'axis', formatter: (items: Array<{ data: number; seriesName: string; dataIndex: number }>) =>
      (labels[items[0]?.dataIndex ?? -1] ?? '') + '<br/>' + items.map(item => item.seriesName + ': ' + item.data).join('<br/>') },
    legend: { type: 'scroll', orient: 'horizontal', top: 2, left: 70, right: 12, itemGap: 10, pageIconSize: 9, textStyle: { color: '#526176', fontSize: 10 } },
    xAxis: { type: 'category', name: 'Response-time range (ms)', nameLocation: 'middle', nameGap: 44, data: labels, axisLabel: { color: axis, rotate: 25, interval: Math.max(0, Math.ceil(indices.length / 6) - 1), hideOverlap: true, formatter: (_label: string, index: number) => {
      const bounds = histogramBinBounds(indices[index]);
      const short = (value: number) => value >= 1000 ? Number((value / 1000).toPrecision(3)) + 'k' : String(Number(value.toPrecision(3)));
      return bounds.upper == null ? '≥ ' + short(bounds.lower) : short(bounds.lower) + '–' + short(bounds.upper);
    } } },
    yAxis: { type: 'value', name: 'Requests', minInterval: 1, nameLocation: 'middle', nameGap: 38, axisLabel: { color: axis }, splitLine: { lineStyle: { color: '#edf0f5' } } },
    series: [
      { name: 'Current', type: 'bar', data: indices.map(index => bins.get(index)?.current ?? 0), itemStyle: { color: cobalt }, barMaxWidth: 22 },
      { name: 'Reference', type: 'bar', data: indices.map(index => bins.get(index)?.reference ?? 0), itemStyle: { color: '#a6b7e7' }, barMaxWidth: 22 }
    ]
  };
};

export const orderedHeatmapCohorts = (analysis: Analysis) => analysis.apis.slice().sort((a, b) =>
  a.sourceId.localeCompare(b.sourceId) || a.controller.localeCompare(b.controller) || a.action.localeCompare(b.action) || a.kind.localeCompare(b.kind) || a.outcome.localeCompare(b.outcome));
export const heatmapCohortKeyAt = (analysis: Analysis, rowIndex: number) => orderedHeatmapCohorts(analysis)[rowIndex]?.key ?? null;

export const heatmapOption = (analysis: Analysis, sources: Map<string, string>, zone: string) => {
  const rows = orderedHeatmapCohorts(analysis);
  const timePoints = alignedPoints(analysis, null);
  const timeLabels = axisTimeLabels(timePoints, zone);
  const max = Math.max(1, ...analysis.timeline.flatMap(series => series.points.map(point => finite(point.p95Ratio) ?? 0)));
  const pointsByCohort = new Map(rows.map(row => [row.key, alignedPoints(analysis, row.key)]));
  const data = rows.flatMap((row, rowIndex) => (pointsByCohort.get(row.key) ?? []).map((point, timeIndex) => ({
    name: row.key,
    value: [timeIndex, rowIndex, finite(point.p95Ratio)],
    itemStyle: point.p95Ratio == null ? { color: '#edf0f4' } : undefined
  })));
  return {
    animation: false,
    grid: { left: 205, right: 32, top: 12, bottom: 70 },
    tooltip: { position: 'top', formatter: (item: { value: [number, number, number | null] }) => {
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
    xAxis: { type: 'category', data: timeLabels, axisLabel: { color: axis, rotate: 35, interval: Math.max(0, Math.ceil(timeLabels.length / 8) - 1), hideOverlap: true }, axisLine: { lineStyle: { color: '#dbe2eb' } } },
    yAxis: { type: 'category', data: rows.map(row => (sources.get(row.sourceId) ?? row.sourceId) + ' / ' + row.controller + '.' + row.action + ' / ' + row.kind + ' / ' + row.outcome), axisLabel: { color: '#526176', width: 195, overflow: 'truncate' } },
    visualMap: { min: 0, max, calculable: true, orient: 'horizontal', left: 'center', bottom: 0, inRange: { color: ['#dce9f6', '#83bdb5', '#e4ae5b', '#b85a42'] }, outOfRange: { color: '#edf0f4' }, textStyle: { color: axis } },
    series: [{ type: 'heatmap', data, label: { show: false }, emphasis: { itemStyle: { shadowBlur: 8, shadowColor: '#315bd466' } } }]
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
export const timelineColors = ['#146b58', '#315bd4', '#ad6511', '#9956a1', '#b74545', '#157d94', '#6e6f24', '#76563f'];
export const multiTimelineOption = (analysis: Analysis, keys: string[], sources: Map<string, string>, zone: string, mode: 'percent' | 'milliseconds', series: TimelineSeries = 'p95') => {
  const failure = isFailureSeries(series);
  const seriesLabel = timelineSeriesOptions.find(item => item.value === series)?.label ?? series;
  // A percentile is a change from its own reference; a failure count is absolute, and null where the slot has no point.
  const valueAt = (row: Comparison, point: Point | undefined) => isFailureSeries(series) ? failureField(point, series) : baselineChange(row, point, mode, series);
  const rows = keys.flatMap(key => { const row = analysis.apis.find(item => item.key === key); return row ? [row] : []; });
  const aligned = rows.map(row => alignedPoints(analysis, row.key));
  const slots = aligned[0] ?? [];
  const displaySlots = slots.map(point => ({ ...point, at: new Date(Math.max(Date.parse(point.at), Date.parse(analysis.query.start))).toISOString() }));
  const labels = axisTimeLabels(displaySlots, zone);
  return {
    animation: false,
    grid: { left: 82, right: 24, top: 68, bottom: 64 },
    legend: { type: 'scroll', top: 2, left: 16, right: 16, textStyle: { fontSize: 12 }, formatter: (name: string) => name.length > 65 ? name.slice(0, 62) + '…' : name },
    tooltip: { trigger: 'axis', confine: true, formatter: (items: Array<{ dataIndex: number }>) => {
      const index = items[0]?.dataIndex ?? 0;
      return (displaySlots[index] ? tooltipTimestamp(displaySlots[index].at, zone) + '<br/>' : '') + rows.map((row, i) => {
        const point = aligned[i][index];
        const change = baselineChange(row, point, mode, failure ? 'p95' : series as PercentileSeries);
        const reliable = (point?.count ?? 0) >= 20 && row.state === 'compared';
        if (failure) return `<b>${i + 1}. ${escapeHTML(row.controller)}.${escapeHTML(row.action)}</b><br/>${escapeHTML(sources.get(row.sourceId) ?? row.sourceId)} · ${escapeHTML(row.kind)} · ${escapeHTML(row.outcome)}<br/>${seriesLabel}: ${point?.count == null ? 'No observations' : String(failureField(point, series as FailureSeries))}${(point?.incomplete ?? 0) > 0 ? ' · ' + point?.incomplete + ' missing timing (not counted as failures)' : ''}`;
        return `<b>${i + 1}. ${escapeHTML(row.controller)}.${escapeHTML(row.action)}</b><br/>${escapeHTML(sources.get(row.sourceId) ?? row.sourceId)} · ${escapeHTML(row.kind)} · ${escapeHTML(row.outcome)}<br/>Change: ${change == null ? 'Unavailable' : (change > 0 ? '+' : '') + change.toFixed(1) + (mode === 'percent' ? '%' : ' ms')} · Usable timings: ${point?.count ?? 0}<br/>Current ${series.toUpperCase()}: ${finite(point?.[series as PercentileSeries]?.valueMs)?.toFixed(1) ?? 'N/A'} ms · Reference ${series.toUpperCase()}: ${finite(row.reference[series as PercentileSeries]?.valueMs)?.toFixed(1) ?? 'N/A'} ms${reliable ? '' : '<br/>Few or missing samples — treat this point cautiously.'}`;
      }).join('<br/><br/>');
    } },
    xAxis: { type: 'category', data: labels, boundaryGap: false, axisLabel: { color: axis, hideOverlap: true } },
    yAxis: { type: 'value', name: failure ? 'Responses per interval' : mode === 'percent' ? 'Change from own reference (%)' : 'Change from own reference (ms)', nameLocation: 'middle', nameGap: 58, ...(failure ? { min: 0, minInterval: 1 } : {}), axisLabel: { color: axis }, splitLine: { lineStyle: { color: '#edf0f5' } } },
    dataZoom: [{ type: 'inside', zoomOnMouseWheel: false }, { type: 'slider', height: 16, bottom: 8, borderColor: 'transparent', fillerColor: '#146b5822' }],
    series: rows.map((row, i) => ({ name: `${i + 1}. ${row.controller}.${row.action}`, type: 'line', connectNulls: false, showSymbol: true, symbolSize: 6, lineStyle: { color: timelineColors[i], width: 2 }, itemStyle: { color: timelineColors[i] },
      data: aligned[i].map(point => ({ value: valueAt(row, point), symbol: !failure && ((point.count ?? 0) < 20 || row.state !== 'compared') ? 'emptyCircle' : 'circle', itemStyle: { opacity: !failure && ((point.count ?? 0) < 20 || row.state !== 'compared') ? .45 : 1 } })),
      ...(i === 0 && !failure ? { markLine: { symbol: 'none', label: { formatter: 'Reference', position: 'insideEndTop' }, lineStyle: { type: 'dashed', color: '#7b8c84' }, data: [{ yAxis: 0 }] } } : {})
    }))
  };
};
