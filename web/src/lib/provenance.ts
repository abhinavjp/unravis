import type { CoverageRefused, CoverageReport, CoverageRow, FieldCoverage, IDMargin, Provenance } from './types';

/**
 * Pure text and shape logic for the provenance note and the coverage panel (P1a, T7).
 * Every sentence the two components show lives here, so the copy lint can read it.
 * The wordings of prov.* and cov.* follow the P1a plan "Copy" table; the hint sentence is the roadmap 6.4(2) text.
 */

/** The fixed sentences, keyed by their copy id. Placeholders are written {like this}. */
export const PROVENANCE_COPY = {
  'prov.complete': 'Complete: every matching record in this window was read. Examined {examined}, returned {returned}, in {elapsed}.',
  'prov.ranToEnd': 'Finished with no time limit, row limit or read error. Records examined are not counted on a live source.',
  'prov.partial': 'Partial: {reason}. Some matching records may be missing.',
  'prov.reason.timeout': 'the {limit} s time limit was reached',
  'prov.reason.timeoutUnknown': 'the time limit was reached',
  'prov.reason.rowcap': 'the row limit was reached',
  'prov.reason.error': 'a read error stopped the query',
  'prov.reason.unknown': 'the query did not finish',
  'prov.hint': 'bounded by margin {margin}, calibrated on {sample} indexed rows; rows outside {margin} would be missed',
  'prov.hintFallback': 'Hint mode was not available ({why}); this result used exact mode.',
  'prov.scan.broad': 'This query reads the whole window, not an index.'
} as const;

export const COVERAGE_REFUSED = 'This check reads every record in the window, so it does not run on a live source by default. Run it on a restored copy, or start Unravis with --allow-broad-scans.';

/** Fixed labels and headings for the coverage panel. */
export const COVERAGE_TEXT = {
  button: 'Check coverage',
  running: 'Checking…',
  heading: 'Coverage census',
  intro: 'Counts how many records have each field filled in, by request type and day. It reads every record in the period, and it only runs when you press the button.',
  clamped: 'The period is longer than 31 days, so the check uses the last 31 days of it.',
  noPeriod: 'Pick a period first.',
  accountNote: 'What AccountId identifies is not verified.',
  empty: 'No records in this period.',
  failed: 'The check did not finish.',
  columns: ['Type', 'Day', 'Records', 'RequestInitiatedAt', 'CreatedOn', 'ApiTime', 'AccountId'],
  tableLabel: 'Share of records with each field filled in',
  tenantAvailable: 'tenant attribution is available.',
  total: 'All',
  allDays: 'All days'
} as const;

export const COVERAGE_TERM = 'coverage-census';

const KIND_LABELS: Record<string, string> = {
  synchronous: 'Synchronous', asynchronous: 'Asynchronous', hangfire: 'Hangfire', unknown: 'Unknown', all: COVERAGE_TEXT.total
};
const WINDOW_LABELS: Record<string, string> = {
  current: 'Current period', reference: 'Earlier period', page: 'Request list', breakdown: 'Breakdown', coverage: 'Coverage check'
};

const fill = (template: string, values: Record<string, string>) => template.replace(/\{(\w+)\}/g, (_, key: string) => values[key] ?? '');
const count = (value: number) => Math.round(value).toLocaleString('en-US');

/** A margin as whole seconds, the way the server states it ("61 s"). */
export const formatMargin = (ms: number) => Math.floor(ms / 1000) + ' s';

/** An elapsed time: milliseconds under a second, then seconds with at most one decimal, then minutes. */
export function formatElapsed(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return '0 ms';
  if (ms < 1000) return Math.round(ms) + ' ms';
  if (ms < 60_000) return String(Number((ms / 1000).toFixed(1))) + ' s';
  const minutes = Math.floor(ms / 60_000);
  const seconds = Math.round((ms - minutes * 60_000) / 1000);
  return minutes + ' min' + (seconds ? ' ' + seconds + ' s' : '');
}

/** The roadmap 6.4(2) sentence. The server's own label is used when it sends one. */
export function hintSentence(margin: IDMargin): string {
  if (margin.label?.trim()) return margin.label.trim();
  const m = formatMargin(margin.marginMs);
  return fill(PROVENANCE_COPY['prov.hint'], { margin: m, sample: String(margin.sampleSize) });
}

function partialReason(p: Provenance): string {
  switch (p.partialReason) {
    case 'timeout': {
      const seconds = p.maxTimeMs / 1000;
      return seconds > 0
        ? fill(PROVENANCE_COPY['prov.reason.timeout'], { limit: String(Number(seconds.toFixed(1))) })
        : PROVENANCE_COPY['prov.reason.timeoutUnknown'];
    }
    case 'rowcap': return PROVENANCE_COPY['prov.reason.rowcap'];
    case 'error': return PROVENANCE_COPY['prov.reason.error'];
    default: return PROVENANCE_COPY['prov.reason.unknown'];
  }
}

/** One plain line that says how complete the read was. Only a measured count may be called Complete. */
export function provenanceLine(p: Provenance): string {
  if (p.completeness === 'partial') return fill(PROVENANCE_COPY['prov.partial'], { reason: partialReason(p) });
  if (p.completeness === 'hint' && p.margin) return 'Hint mode: ' + hintSentence(p.margin) + '.';
  if (p.completeness === 'complete' && p.docsExamined != null) {
    return fill(PROVENANCE_COPY['prov.complete'], { examined: count(p.docsExamined), returned: count(p.docsReturned), elapsed: formatElapsed(p.elapsedMs) });
  }
  return PROVENANCE_COPY['prov.ranToEnd'];
}

/** Extra sentences about how the read ran: a hint-mode fallback, and a read of the whole window. Empty when there is nothing to add. */
export function scanNote(p: Provenance): string {
  const parts: string[] = [];
  if (p.note && p.queryMode !== 'hint') parts.push(fill(PROVENANCE_COPY['prov.hintFallback'], { why: p.note }));
  if (p.scanClass === 'broad') parts.push(PROVENANCE_COPY['prov.scan.broad']);
  return parts.join(' ');
}

export interface ProvenanceRow {
  key: string; label: string; line: string; note: string;
  /** Glossary ids to show a hint for. */
  terms: string[];
  tone: 'ok' | 'warn' | 'note';
}

function termsFor(p: Provenance): string[] {
  if (p.completeness === 'partial') return ['partial'];
  if (p.completeness === 'hint') return ['query-mode', 'margin'];
  return ['complete'];
}

/** One display row per provenance entry; an absent or empty list gives no rows, so older servers show nothing. */
export function provenanceRows(list: Provenance[] | null | undefined, sourceLabels: ReadonlyMap<string, string>): ProvenanceRow[] {
  if (!Array.isArray(list)) return [];
  return list.map((p, index) => ({
    key: `${p.sourceId}|${p.window}|${index}`,
    label: (sourceLabels.get(p.sourceId) ?? p.sourceId) + ' · ' + (WINDOW_LABELS[p.window] ?? p.window),
    line: provenanceLine(p),
    note: scanNote(p),
    terms: termsFor(p),
    tone: p.completeness === 'partial' ? 'warn' : p.completeness === 'hint' ? 'note' : 'ok'
  }));
}

/** The refusal text shown for a 409. It is the fixed sentence, never the server's own words. */
export function coverageRefusalText(_refused: CoverageRefused): string {
  return COVERAGE_REFUSED;
}

export interface CoverageDisplayRow { key: string; kind: string; day: string; docs: string; fields: string[]; total: boolean }

const pct = (field: FieldCoverage | undefined) => (field && Number.isFinite(field.pct) ? field.pct : 0).toFixed(1) + ' %';

function displayRow(row: CoverageRow, total: boolean): CoverageDisplayRow {
  return {
    key: total ? 'total' : row.kind + '|' + row.day,
    kind: KIND_LABELS[row.kind] ?? row.kind,
    day: total ? COVERAGE_TEXT.allDays : row.day,
    docs: count(row.docs),
    fields: [row.requestInitiatedAt, row.createdOn, row.apiTime, row.accountId].map(pct),
    total
  };
}

/** The table rows: each kind and day in the server's order, then the total. */
export function coverageRowsForDisplay(report: CoverageReport | null | undefined): CoverageDisplayRow[] {
  if (!report) return [];
  const rows = (report.rows ?? []).map((row) => displayRow(row, false));
  return report.total ? [...rows, displayRow(report.total, true)] : rows;
}

/** The tenant line. An unavailable text is the server's sentence, unchanged. */
export function tenantLine(report: CoverageReport): string {
  const text = report.tenantAttribution === 'available' ? COVERAGE_TEXT.tenantAvailable : report.tenantAttribution;
  return 'Tenant check: ' + text;
}

const DAY_MS = 86_400_000;
export const MAX_COVERAGE_DAYS = 31;

/** The period to send to the census: the end stays, the start is pulled in to at most 31 days. Null when the period is not usable. */
export function coverageRange(start: string, end: string): { start: string; end: string; clamped: boolean } | null {
  const from = Date.parse(start);
  const to = Date.parse(end);
  if (!Number.isFinite(from) || !Number.isFinite(to) || from >= to) return null;
  const limit = MAX_COVERAGE_DAYS * DAY_MS;
  if (to - from <= limit) return { start: new Date(from).toISOString(), end: new Date(to).toISOString(), clamped: false };
  return { start: new Date(to - limit).toISOString(), end: new Date(to).toISOString(), clamped: true };
}

/**
 * Every fixed sentence in this file, for the copy lint. A template with a {placeholder} is left out: its rendered
 * lines are linted instead (the placeholder name would count as an extra word in the reading-level score).
 */
export function allCopyStrings(): string[] {
  const { columns: _columns, ...rest } = COVERAGE_TEXT;
  return [...Object.values(PROVENANCE_COPY), COVERAGE_REFUSED, ...Object.values(rest).map(String)].filter((text) => !/\{\w+\}/.test(text));
}
