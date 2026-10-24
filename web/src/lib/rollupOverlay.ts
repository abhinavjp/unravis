import { axisDuration } from './chartScale';
import { HOURS_PER_ROW, dayLabel, gridRows, latencyText, whole, zoneNote, type GridRow, type HourCell, type HourGridResponse } from './rollup';

/*
 * Two days laid over each other on one 0 to 23 hour axis (the "Compare two days" part of the daily and hourly view).
 * It reads the same cells as the heat map: no new request, no Svelte, no ECharts import, so tests can load it.
 * A cell with fewer than 5 requests is noise; a P95 needs 20 timed requests (the server leaves it out otherwise).
 */

export type OverlayMetric = 'p95' | 'count';
export const overlayMetrics: { id: OverlayMetric; label: string }[] = [
  { id: 'p95', label: 'P95 time' },
  { id: 'count', label: 'Request count' }
];

/** Every fixed sentence of the overlay, in one place so the copy lint can read them. */
export const overlayCopy = {
  heading: 'Compare two days',
  intro: 'Each line is one day, laid over the other by hour of the day. Choose the two days to compare.',
  firstDay: 'First day',
  secondDay: 'Second day',
  showLabel: 'Show',
  sameDay: 'Choose two different days to compare.',
  tooFew: 'This view needs two days. Choose a longer span.',
  noise: 'Hours with fewer than 5 requests are dashed on the count line and left blank on the P95 line.',
  masked: 'A P95 value needs at least 20 timed requests in the hour. A break in a line means the hour had fewer.',
  shortDay: 'A 23-hour day has no point at the hour the clocks skipped.',
  longDay: 'A 25-hour day counts one hour twice, so that hour can show a higher count.',
  partial: 'A partial day is cut by the span, so some hours have no point.',
  tableToggle: 'Show the values as a table',
  tableCaption: 'The same values as the chart, one row for each hour of the day.',
  hourHeader: 'Hour',
  keyFew: 'Few requests',
  noHour: 'No such hour on this day',
  fewRequests: 'Too few requests',
  fewTimings: 'Too few timed requests'
};

/** One hour of one day. `exists` is false for an hour the clocks skipped or the span cut off. */
export interface OverlayPoint { hour: number; exists: boolean; count: number; p95Ms: number | null; noise: boolean }
export interface OverlayDay { date: string; label: string; hours: number; partial: boolean; points: OverlayPoint[] }
export interface DayChoice { date: string; label: string; hours: number; partial: boolean }

const emptyPoint = (hour: number): OverlayPoint => ({ hour, exists: false, count: 0, p95Ms: null, noise: false });

function pointOf(hour: number, cell: HourCell | null | undefined): OverlayPoint {
  if (!cell) return emptyPoint(hour);
  const ms = cell.p95?.valueMs;
  return { hour, exists: true, count: Math.max(0, Number(cell.count) || 0), p95Ms: ms != null && Number.isFinite(ms) ? ms : null, noise: !!cell.noise };
}

/** "25 Oct", with "partial" or "23 h day" added when the day is not a whole 24 hours. */
export function overlayDayLabel(day: { date: string; hours: number; partial: boolean }): string {
  const notes: string[] = [];
  if (day.partial) notes.push('partial');
  if (day.hours !== 24) notes.push((Number.isInteger(day.hours) ? String(day.hours) : day.hours.toFixed(1)) + ' h day');
  return dayLabel(day.date) + (notes.length ? ' (' + notes.join(', ') + ')' : '');
}

/** The days the two selects offer, oldest first. */
export function overlayChoices(grid: Pick<HourGridResponse, 'days' | 'cells'>): DayChoice[] {
  return gridRows(grid).map(row => ({ date: row.date, label: overlayDayLabel(row), hours: row.hours, partial: row.partial }));
}

/** The latest two whole days (not cut by the span), oldest first. With fewer than two, the latest two days of any kind. Null with fewer than two days. */
export function defaultOverlayDays(choices: DayChoice[]): [string, string] | null {
  const full = choices.filter(choice => !choice.partial);
  const pool = full.length >= 2 ? full : choices;
  if (pool.length < 2) return null;
  return [pool[pool.length - 2].date, pool[pool.length - 1].date];
}

/** One day as 24 points on the 0 to 23 axis. A repeated hour is one point (the cell spans both passes). */
export function overlayDay(grid: Pick<HourGridResponse, 'days' | 'cells'>, date: string): OverlayDay | null {
  const row: GridRow | undefined = gridRows(grid).find(item => item.date === date);
  if (!row) return null;
  const points: OverlayPoint[] = [];
  for (let hour = 0; hour < HOURS_PER_ROW; hour++) points.push(pointOf(hour, row.cells[hour]));
  return { date: row.date, label: overlayDayLabel(row), hours: row.hours, partial: row.partial, points };
}

/** The number a point plots for a metric, or null for a gap. A count of 0 is a real value; a noise or masked P95 is a gap. */
export function pointValue(point: OverlayPoint, metric: OverlayMetric): number | null {
  if (!point.exists) return null;
  if (metric === 'count') return point.count;
  return point.noise ? null : point.p95Ms;
}

/** True for a point drawn dashed: a count from an hour with fewer than 5 requests. */
export const isDashed = (point: OverlayPoint, metric: OverlayMetric): boolean => metric === 'count' && point.exists && point.noise;

export interface OverlayLine { solid: (number | null)[]; dashed: (number | null)[]; dashedHours: number[] }

/**
 * The values of one day as two lines. `solid` has every plain point. `dashed` has the dashed points and the plain
 * points next to them, so the dashed line is the one that joins them. Gaps are null.
 */
export function overlayLine(day: OverlayDay, metric: OverlayMetric): OverlayLine {
  const values = day.points.map(point => pointValue(point, metric));
  const dashedAt = day.points.map(point => isDashed(point, metric));
  const solid = values.map((value, index) => dashedAt[index] ? null : value);
  const dashed: (number | null)[] = values.map((value, index) => {
    if (value == null) return null;
    if (dashedAt[index]) return value;
    const near = (other: number) => other >= 0 && other < values.length && dashedAt[other] && values[other] != null;
    return near(index - 1) || near(index + 1) ? value : null;
  });
  return { solid, dashed, dashedHours: dashedAt.flatMap((dash, index) => dash ? [index] : []) };
}

/** The words of one value, for the table and the tooltip. */
export function pointText(point: OverlayPoint, metric: OverlayMetric): string {
  if (!point.exists) return overlayCopy.noHour;
  if (metric === 'count') return whole(point.count) + (point.count === 1 ? ' request' : ' requests') + (point.noise ? ' (few)' : '');
  if (point.noise) return overlayCopy.fewRequests;
  if (point.p95Ms == null) return overlayCopy.fewTimings;
  return latencyText({ lowerMs: point.p95Ms, upperMs: point.p95Ms, valueMs: point.p95Ms });
}

export const hourText = (hour: number): string => String(hour).padStart(2, '0') + ':00';

const metricName = (metric: OverlayMetric): string => overlayMetrics.find(item => item.id === metric)?.label ?? metric;

/** The caption under the chart: what the lines are, and the zone the hours follow. */
export function overlayCaption(metric: OverlayMetric, zone: string): string {
  return metricName(metric) + ' for each hour of the day, one line for each day. ' + zoneNote(zone);
}

/** The notes that apply to this pair of days, in plain words. */
export function overlayNotes(days: OverlayDay[], metric: OverlayMetric): string[] {
  const notes = [metric === 'p95' ? overlayCopy.masked : overlayCopy.noise];
  if (days.some(day => day.hours < 24)) notes.push(overlayCopy.shortDay);
  if (days.some(day => day.hours > 24)) notes.push(overlayCopy.longDay);
  if (days.some(day => day.partial)) notes.push(overlayCopy.partial);
  return notes;
}

function daySummary(day: OverlayDay, metric: OverlayMetric): string {
  let high: OverlayPoint | null = null, low: OverlayPoint | null = null, gaps = 0;
  for (const point of day.points) {
    const value = pointValue(point, metric);
    if (value == null) { if (point.exists) gaps++; continue; }
    if (!high || value > pointValue(high, metric)!) high = point;
    if (!low || value < pointValue(low, metric)!) low = point;
  }
  const missing = day.points.filter(point => !point.exists).length;
  const none = missing >= HOURS_PER_ROW;
  if (!high || !low) return day.label + ': no values' + (none ? '.' : ', ' + gaps + ' hours left blank.');
  const parts = ['highest ' + pointText(high, metric) + ' at ' + hourText(high.hour), 'lowest ' + pointText(low, metric) + ' at ' + hourText(low.hour)];
  if (gaps) parts.push(gaps + (gaps === 1 ? ' hour' : ' hours') + ' left blank');
  return day.label + ': ' + parts.join(', ') + '.';
}

/** The screen reader summary of the chart: both days, the high and the low of each. */
export function overlaySummary(first: OverlayDay, second: OverlayDay, metric: OverlayMetric, zone: string): string {
  return metricName(metric) + ' by hour, ' + first.label + ' and ' + second.label + '. ' + zoneNote(zone) + ' ' +
    daySummary(first, metric) + ' ' + daySummary(second, metric);
}

// ---- The chart option ----

/** The palette fields the option reads. `Palette` from charts.ts fits it. */
export interface OverlayPalette { text: string; text2: string; axisLine: string; grid: string; surface: string; series: string[] }

const escapeHTML = (value: string) => value.replace(/[&<>"']/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char] ?? char);

/** The line looks of the first and second day. The second is thinner and drawn on top, with another dot shape, so two equal lines both show. */
export const overlayLook = [
  { width: 3.5, symbol: 'circle', size: 7 },
  { width: 2, symbol: 'diamond', size: 8 }
] as const;

/**
 * The ECharts option for two days. Each day is a solid series, plus a dashed series for hours with few requests.
 * Hours are a category axis 00 to 23, so a DST day has no shifted hour.
 */
export function overlayOption(first: OverlayDay, second: OverlayDay, metric: OverlayMetric, p: OverlayPalette) {
  const days = [first, second];
  const lines = days.map(day => overlayLine(day, metric));
  const colors = [p.series[0] ?? p.text, p.series[1] ?? p.text2];
  const hours = Array.from({ length: HOURS_PER_ROW }, (_, hour) => hourText(hour));
  const ms = metric === 'p95';
  const series = days.flatMap((day, index) => {
    const look = overlayLook[index];
    const base = { type: 'line', connectNulls: false, symbol: look.symbol, symbolSize: look.size, z: 3 + index, itemStyle: { color: colors[index] } };
    return [
      { ...base, name: day.label, lineStyle: { color: colors[index], width: look.width, type: 'solid' }, data: lines[index].solid },
      {
        ...base, name: day.label + ' ' + overlayCopy.keyFew, silent: true, lineStyle: { color: colors[index], width: look.width, type: 'dashed' },
        data: lines[index].dashed.map((value, hour) => ({
          value, itemStyle: lines[index].dashedHours.includes(hour) ? { color: p.surface, borderColor: colors[index], borderWidth: 2 } : { opacity: 0 }
        }))
      }
    ];
  });
  return {
    animation: false,
    color: colors,
    // No chart legend: the key above the chart (plain HTML) names both lines and the dashed style.
    grid: { left: 62, right: 16, top: 30, bottom: 30 },
    tooltip: {
      trigger: 'axis', confine: true, backgroundColor: p.surface, borderColor: p.axisLine, textStyle: { color: p.text, fontSize: 12 },
      formatter: (items: Array<{ dataIndex: number }>) => {
        const hour = items[0]?.dataIndex ?? 0;
        return '<b>' + hourText(hour) + '</b><br/>' + days.map(day => escapeHTML(day.label) + ': ' + escapeHTML(pointText(day.points[hour] ?? emptyPoint(hour), metric))).join('<br/>');
      }
    },
    xAxis: { type: 'category', data: hours, boundaryGap: false, axisLabel: { color: p.text2, interval: 2, hideOverlap: true }, axisLine: { lineStyle: { color: p.axisLine } } },
    yAxis: {
      type: 'value', min: 0, minInterval: ms ? undefined : 1, name: ms ? 'P95 time' : 'Requests', nameTextStyle: { color: p.text2, align: 'left' },
      axisLabel: { color: p.text2, ...(ms ? { formatter: (value: number) => axisDuration(value) } : {}) }, splitLine: { lineStyle: { color: p.grid } }
    },
    series
  };
}

/** The rows of the text table: one for each hour, with the words of each day's value. */
export function overlayRows(first: OverlayDay, second: OverlayDay, metric: OverlayMetric): { hour: string; first: string; second: string }[] {
  return first.points.map((point, hour) => ({ hour: hourText(hour), first: pointText(point, metric), second: pointText(second.points[hour], metric) }));
}
