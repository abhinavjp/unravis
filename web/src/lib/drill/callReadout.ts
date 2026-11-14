// Slow call screen: the numbers under the hover line, and one time-ordered list of
// slow items and event markers from every source. Pure functions, no imports.

export interface ReadCell { start: number; end: number; atOnce: number | null; p95Ms: number | null; refused: number | null }
export interface Readout { stepStart: number; api: ReadCell | null; iis: ReadCell | null; db: ReadCell | null; isThisCall: boolean }

function cellAt(cells: readonly ReadCell[], tMs: number): ReadCell | null {
  for (const cell of cells) {
    if (tMs >= cell.start && tMs < cell.end) return cell;
  }
  return null;
}

// A lane with no cell at tMs gives null (the screen prints "not recorded", never 0).
// Null for the whole readout when tMs is outside every lane's cells.
export function readoutAt(
  tMs: number,
  lanes: { audit: readonly ReadCell[]; iis: readonly ReadCell[]; sql: readonly ReadCell[] },
  call: { startMs: number; endMs: number }
): Readout | null {
  if (!Number.isFinite(tMs)) return null;
  const api = cellAt(lanes.audit, tMs);
  const iis = cellAt(lanes.iis, tMs);
  const db = cellAt(lanes.sql, tMs);
  const first = api ?? iis ?? db;
  if (!first) return null;
  return {
    stepStart: first.start,
    api,
    iis,
    db,
    isThisCall: tMs >= call.startMs && tMs <= call.endMs
  };
}

export type SourceKey = 'api' | 'iis' | 'db';
export interface SourceItem { key: string; source: SourceKey; startMs: number; endMs: number | null; what: string; tookMs: number | null; marker: boolean; thisCall: boolean }
export interface EventIn { evidenceId: string; source: string; kind: string; start: string; end?: string | null; durationMs?: number | null; severity?: string }

const EVENT_SOURCES: Record<string, SourceKey> = {
  'sql-xe': 'db',
  'sql-errorlog': 'db',
  'sql-pdh': 'db',
  'sql-querystore': 'db',
  'iis-httperr': 'iis',
  'iis-w3c': 'iis',
  'iis-freb': 'iis'
};

export function sourceOfEvent(source: string): SourceKey | null {
  return Object.prototype.hasOwnProperty.call(EVENT_SOURCES, source) ? EVENT_SOURCES[source] : null;
}

// Uses the kind field only; record text never reaches a label.
export function eventLabel(e: EventIn): string {
  const text = String(e.kind ?? '').replace(/-/g, ' ').trim();
  return text ? text.charAt(0).toUpperCase() + text.slice(1) : '';
}

const SOURCE_ORDER: Record<SourceKey, number> = { api: 0, iis: 1, db: 2 };

function finiteMs(value: string | null | undefined): number | null {
  if (value == null || value === '') return null;
  const ms = Date.parse(value);
  return Number.isFinite(ms) ? ms : null;
}

export function allSources(input: {
  apiRows: readonly { id: string; controller: string; action: string; initiatedAt: string; durationMs: number | null; sourceId?: string }[];
  iisBars: readonly { id: string; label: string; start: number; end: number }[];
  events: readonly EventIn[];
  callId: string;
  callSourceId?: string;
  start: number;
  end: number;
  shown: Record<SourceKey, boolean>;
}): SourceItem[] {
  const items: SourceItem[] = [];
  const overlaps = (startMs: number, endMs: number | null) => startMs <= input.end && (endMs ?? startMs) >= input.start;

  if (input.shown.api) {
    // Key is `api:<id>` unless rows from more than one source are in play; then `api:<sourceId>:<id>`.
    const sourceIds = new Set(input.apiRows.map((r) => r.sourceId).filter((s): s is string => !!s));
    const manySources = sourceIds.size > 1;
    for (const row of input.apiRows) {
      const startMs = finiteMs(row.initiatedAt);
      if (startMs === null) continue;
      const took = row.durationMs != null && Number.isFinite(row.durationMs) ? row.durationMs : null;
      const endMs = took === null ? null : startMs + took;
      if (!overlaps(startMs, endMs)) continue;
      items.push({
        key: manySources && row.sourceId ? `api:${row.sourceId}:${row.id}` : `api:${row.id}`,
        source: 'api',
        startMs,
        endMs,
        what: [row.controller, row.action].filter(Boolean).join(' / '),
        tookMs: took,
        marker: false,
        thisCall: row.id === input.callId
          && (input.callSourceId === undefined || row.sourceId === undefined || row.sourceId === input.callSourceId)
      });
    }
  }

  if (input.shown.iis) {
    for (const bar of input.iisBars) {
      if (!Number.isFinite(bar.start) || !Number.isFinite(bar.end)) continue;
      if (!overlaps(bar.start, bar.end)) continue;
      items.push({
        key: `iis:${bar.id}`,
        source: 'iis',
        startMs: bar.start,
        endMs: bar.end,
        what: bar.label,
        tookMs: Math.max(0, bar.end - bar.start),
        marker: false,
        thisCall: false
      });
    }
  }

  for (const e of input.events) {
    const source = sourceOfEvent(e.source);
    if (!source || !input.shown[source]) continue;
    const startMs = finiteMs(e.start);
    if (startMs === null) continue;
    const endParsed = finiteMs(e.end);
    let took: number | null = null;
    if (e.durationMs != null && Number.isFinite(e.durationMs) && e.durationMs > 0) took = e.durationMs;
    else if (endParsed !== null && endParsed > startMs) took = endParsed - startMs;
    const endMs = took === null ? null : (endParsed !== null && endParsed > startMs ? endParsed : startMs + took);
    if (!overlaps(startMs, endMs)) continue;
    items.push({
      key: `${source}:${e.source}:${e.evidenceId || `${e.kind}@${startMs}`}`,
      source,
      startMs,
      endMs,
      what: eventLabel(e),
      tookMs: took,
      marker: took === null,
      thisCall: false
    });
  }

  const seen = new Set<string>();
  const unique = items.filter((item) => {
    if (seen.has(item.key)) return false;
    seen.add(item.key);
    return true;
  });
  unique.sort((a, b) => a.startMs - b.startMs || SOURCE_ORDER[a.source] - SOURCE_ORDER[b.source]);
  return unique;
}
