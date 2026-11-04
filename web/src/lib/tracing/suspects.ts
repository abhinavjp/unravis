/**
 * The Suspects rail of the Investigation Timeline (the wireframe's right-hand panel), worked out in the browser from the one answer the
 * timeline already holds: per lane, the event counts in each bar. Pure TypeScript; the panel is `SuspectsRail.svelte`.
 *
 * The rules are the wireframe's: a suspect names the readings it MUST have and the ones it should EXPECT. A reading is yes, no or
 * unknown. A "no" on a must-reading rules the suspect out (green); an unknown one means it cannot be tested yet (grey, with what it
 * needs); every must-reading present is "consistent" (orange). Red is the earliest tier clearly moving first, and it needs call times
 * to date each tier. No suspect is called a cause and nothing is scored.
 *
 * What this build can honestly read is narrower than the wireframe's synthetic data, so most readings are "unknown" and say so:
 *  - arrivalsUp (API audit) and sqlUp (slow database calls) compare event counts per bar in the first half of the window with the second half;
 *  - clockSkew reads the lane states (a lane switched off until its time zone is checked);
 *  - auditDip compares audit calls per IIS request between the two halves.
 * The server sends no call times and the response covers the incident window only, so nothing here is compared with the reference period.
 */
import type { TracingLane, TracingLaneKey, TracingResponse } from './model';
import { LANE_NAME, barsFor, laneNote, laneNotFullyRead, slotGrid, slotOutside, slotStartMs, unreadSlots, type SlotGrid } from './lanes';
import {
  CATALOGUE_TOTAL, RESULT_TEXT, RESULT_WHY, SIGNAL_TEXT, SIGNAL_UNKNOWN, SIGNAL_WHY, SUSPECT_ENTRY_TEXT
} from '../copy/suspectsCopy';

export { CATALOGUE_TOTAL };

export type SuspectColour = 'red' | 'orange' | 'green' | 'grey';
export type SignalValue = 'yes' | 'no' | 'unknown';

/** The numbers behind every reading, in one place. 1.5 and 30 are the timeline's own placeholders (CHARTS-DECISIONS.md, R1 and R4). */
export const SUSPECT_RULES = { riseRatio: 1.5, minEvents: 30, minBars: 4, dipShare: 0.3, top: 5 } as const;
/** The data-quality checks stay above the list while they are not green. */
export const PINNED_IDS: readonly string[] = ['Q1', 'Q2'];

interface Signal { v: SignalValue; why: string; momentSlot?: number }

interface CatalogueEntry { id: string; group: string; must: string[]; expect?: string[] }

/** The 26 suspects the rail can show, in catalogue order. The sentences are in `suspectsCopy.ts`. */
export const SUSPECT_CATALOGUE: readonly CatalogueEntry[] = [
  { id: 'Q1', group: 'Q', must: ['clockSkew'] },
  { id: 'Q2', group: 'Q', must: ['auditDip'] },
  { id: 'L1', group: 'L', must: ['arrivalsUp'] },
  { id: 'L2', group: 'L', must: ['auditUp', 'scopeNarrow'], expect: ['p50Flat'] },
  { id: 'S1', group: 'S', must: ['queueBurst'], expect: ['outsideLarge'] },
  { id: 'S2', group: 'S', must: ['dotnetQueue'], expect: ['outsideLarge'] },
  { id: 'S5', group: 'S', must: ['auditUp', 'mongoSlow', 'insertUp'], expect: ['scopeAll'] },
  { id: 'S8', group: 'S', must: ['payloadUp'] },
  { id: 'S9', group: 'S', must: ['filterReject'] },
  { id: 'S10', group: 'S', must: ['appPoolFail'] },
  { id: 'S11', group: 'S', must: ['recvTimeout'] },
  { id: 'S12', group: 'S', must: ['drain'] },
  { id: 'D1', group: 'D', must: ['sqlUp', 'blocking'], expect: ['lockWait'] },
  { id: 'D2', group: 'D', must: ['sqlUp', 'deadlocks'] },
  { id: 'D6', group: 'D', must: ['sqlUp', 'memGrant'] },
  { id: 'D9', group: 'D', must: ['sqlUp', 'diskSlow'] },
  { id: 'D10', group: 'D', must: ['sqlUp', 'runQueue'] },
  { id: 'D16', group: 'D', must: ['attention'] },
  { id: 'D17', group: 'D', must: ['loginStorm', 'sqlUp'] },
  { id: 'M5', group: 'M', must: ['auditUp', 'mongoQueue'] },
  { id: 'M10', group: 'M', must: ['auditUp', 'otherShare'] },
  { id: 'M11', group: 'M', must: ['spill', 'mongoOpUp'] },
  { id: 'M12', group: 'M', must: ['connRejected'] },
  { id: 'H1', group: 'H', must: ['cpuHigh'], expect: ['cpuUp'] },
  { id: 'H6', group: 'H', must: ['sharedHost'], expect: ['allSlow', 'sameMoment'] },
  { id: 'C1', group: 'C', must: ['changesLead'] }
];

export interface EvidenceRow { signal: string; role: 'must' | 'expect'; value: SignalValue; why: string }

export interface Suspect {
  id: string;
  group: string;
  title: string;
  colour: SuspectColour;
  result: string;
  why: string;
  evidence: EvidenceRow[];
  check: string;
  overturn: string;
  needs: string;
  /** The absolute slot of the chart where a rising reading of this suspect begins, or null when none could be dated. */
  momentSlot: number | null;
  /** The lane whose count that moment belongs to. */
  momentLane: TracingLaneKey | null;
  order: number;
}

// ---------------------------------------------------------------------------------------------------------------------------------
// Readings from the lanes.

interface Series { idx: number[]; counts: number[] }
type SeriesRead = { ok: true; series: Series } | { ok: false; signal: Signal };

const unknown = (why: string): Signal => ({ v: 'unknown', why });
const one = (n: number): string => (Math.round(n * 10) / 10).toFixed(1);
const two = (n: number): string => (Math.round(n * 100) / 100).toFixed(2);
const sum = (values: number[]): number => values.reduce((a, b) => a + b, 0);

function findLane(response: TracingResponse, key: TracingLaneKey): TracingLane | undefined {
  return response.lanes.find((lane) => lane.key === key);
}

/** The counts of the bars inside the window, in order, or the reason the lane cannot be read. A lane that was not read in full is never read as quiet. */
function readSeries(g: SlotGrid, lane: TracingLane | undefined, key: TracingLaneKey): SeriesRead {
  const name = LANE_NAME[key];
  if (!lane) return { ok: false, signal: unknown(SIGNAL_WHY.laneMissing(name)) };
  if (lane.disabled) return { ok: false, signal: unknown(SIGNAL_WHY.laneZone(name)) };
  if (lane.state === 'no-data') return { ok: false, signal: unknown(SIGNAL_WHY.laneNoFile(name)) };
  if (lane.state !== 'events') return { ok: false, signal: unknown(SIGNAL_WHY.laneNotEvents(name, laneNote(lane))) };
  if (laneNotFullyRead(lane)) return { ok: false, signal: unknown(SIGNAL_WHY.laneNotFull(name)) };
  const unread = unreadSlots(g, lane);
  const byIndex = new Map(barsFor(g, lane.buckets).map((bar) => [bar.index, bar.count]));
  const idx: number[] = [];
  const counts: number[] = [];
  for (let i = 0; i < g.n; i++) {
    if (slotOutside(g, i) || unread.has(i)) continue;
    idx.push(i);
    counts.push(byIndex.get(i) ?? 0);
  }
  if (idx.length < SUSPECT_RULES.minBars) return { ok: false, signal: unknown(SIGNAL_WHY.tooShort(name)) };
  const total = sum(counts);
  if (total < SUSPECT_RULES.minEvents) return { ok: false, signal: unknown(SIGNAL_WHY.tooFew(name, total)) };
  return { ok: true, series: { idx, counts } };
}

/** The bars split in two: the first half and the second half (the second takes the odd bar). */
function halves(series: Series): { early: number[]; late: number[]; mid: number } {
  const mid = Math.floor(series.counts.length / 2);
  return { early: series.counts.slice(0, mid), late: series.counts.slice(mid), mid };
}

/**
 * "Up": events per bar in the second half are at least `riseRatio` times those of the first half (or the first half had none and the
 * window passed the event gate). The moment is read over the whole series, so a rise that starts before the midpoint is still dated at
 * its onset: the first bar that is up and followed by another that is up (or that is the last bar); the first up bar when none is; the
 * first bar of the second half as the last resort.
 */
function riseOf(series: Series, laneName: string): Signal {
  const { early, late, mid } = halves(series);
  const before = sum(early) / early.length;
  const after = sum(late) / late.length;
  const fromNone = before === 0;
  const ratio = fromNone ? Infinity : after / before;
  if (!(ratio >= SUSPECT_RULES.riseRatio)) return { v: 'no', why: SIGNAL_WHY.steady(laneName, one(before), one(after), two(ratio)) };
  const up = (count: number): boolean => (fromNone ? count > 0 : count >= before * SUSPECT_RULES.riseRatio);
  let at = -1;
  for (let j = 0; j < series.counts.length; j++) {
    if (!up(series.counts[j])) continue;
    if (at < 0) at = j;
    if (j + 1 >= series.counts.length || up(series.counts[j + 1])) { at = j; break; }
  }
  const slot = series.idx[at >= 0 ? at : mid];
  const why = fromNone ? SIGNAL_WHY.riseFromNone(laneName, one(after)) : SIGNAL_WHY.rise(laneName, one(before), one(after), two(ratio));
  return { v: 'yes', why, momentSlot: slot };
}

function laneRise(g: SlotGrid, response: TracingResponse, key: TracingLaneKey): Signal {
  const read = readSeries(g, findLane(response, key), key);
  return read.ok ? riseOf(read.series, LANE_NAME[key]) : read.signal;
}

function clockReading(response: TracingResponse): Signal {
  const off = response.lanes.filter((lane) => lane.disabled || lane.state === 'clock-unchecked');
  if (off.length > 0) return { v: 'yes', why: SIGNAL_WHY.clockYes(off.map((lane) => LANE_NAME[lane.key]).join(', ')) };
  if (response.lanes.some((lane) => lane.state === 'events' || lane.state === 'none-seen' || lane.state === 'incomplete')) return { v: 'no', why: SIGNAL_WHY.clockNo };
  return unknown(SIGNAL_WHY.clockNone);
}

function dipReading(g: SlotGrid, response: TracingResponse): Signal {
  const audit = readSeries(g, findLane(response, 'audit'), 'audit');
  const iis = readSeries(g, findLane(response, 'iis'), 'iis');
  if (!audit.ok) return audit.signal;
  if (!iis.ok) return iis.signal;
  // Both lanes draw on the same axis, so the bars inside the window line up one for one.
  if (audit.series.idx.length !== iis.series.idx.length) return unknown(SIGNAL_WHY.dipNeedBoth);
  const a = halves(audit.series);
  const w = halves(iis.series);
  const iisEarly = sum(w.early);
  const iisLate = sum(w.late);
  if (iisEarly === 0 || iisLate === 0) return unknown(SIGNAL_WHY.dipNeedBoth);
  const before = sum(a.early) / iisEarly;
  const after = sum(a.late) / iisLate;
  if (before > 0 && after <= before * (1 - SUSPECT_RULES.dipShare)) return { v: 'yes', why: SIGNAL_WHY.dipYes(two(before), two(after)) };
  return { v: 'no', why: SIGNAL_WHY.dipNo(two(before), two(after)) };
}

/** Every reading the timeline can make; any other reading a suspect names is unknown, with the one honest reason. */
function readSignals(response: TracingResponse): Record<string, Signal> {
  const g = slotGrid(response.window);
  const out: Record<string, Signal> = { clockSkew: clockReading(response) };
  if (!g) {
    out.arrivalsUp = unknown(SIGNAL_UNKNOWN);
    out.sqlUp = unknown(SIGNAL_UNKNOWN);
    out.auditDip = unknown(SIGNAL_UNKNOWN);
    return out;
  }
  out.arrivalsUp = laneRise(g, response, 'audit');
  out.sqlUp = laneRise(g, response, 'sql');
  out.auditDip = dipReading(g, response);
  return out;
}

// ---------------------------------------------------------------------------------------------------------------------------------
// The wireframe's colour rule.

const MOMENT_LANE: Record<string, TracingLaneKey> = { arrivalsUp: 'audit', sqlUp: 'sql' };

export function evaluateSuspects(response: TracingResponse): Suspect[] {
  const signals = readSignals(response);
  const read = (name: string): Signal => signals[name] ?? unknown(SIGNAL_UNKNOWN);
  return SUSPECT_CATALOGUE.map((entry, order) => {
    const text = SUSPECT_ENTRY_TEXT[entry.id];
    const evidence: EvidenceRow[] = [];
    let momentSlot: number | null = null;
    let momentLane: TracingLaneKey | null = null;
    const take = (name: string, role: 'must' | 'expect'): Signal => {
      const signal = read(name);
      evidence.push({ signal: SIGNAL_TEXT[name], role, value: signal.v, why: signal.why });
      if (momentSlot == null && signal.v === 'yes' && signal.momentSlot != null) { momentSlot = signal.momentSlot; momentLane = MOMENT_LANE[name] ?? null; }
      return signal;
    };
    const must = entry.must.map((name) => take(name, 'must'));
    const expect = (entry.expect ?? []).map((name) => take(name, 'expect'));
    const no = must.find((s) => s.v === 'no');
    const missing = must.find((s) => s.v === 'unknown');
    const expectNo = expect.find((s) => s.v === 'no');
    let colour: SuspectColour;
    let result: string;
    let why: string;
    if (no) { colour = 'green'; result = RESULT_TEXT.contradicted; why = RESULT_WHY.contradicted(no.why); }
    else if (missing) { colour = 'grey'; result = RESULT_TEXT.cannot; why = RESULT_WHY.cannot(missing.why); }
    else if (expectNo) { colour = 'orange'; result = RESULT_TEXT.partly; why = RESULT_WHY.partlyExpect(expectNo.why); }
    else { colour = 'orange'; result = RESULT_TEXT.consistent; why = RESULT_WHY.consistent(must[must.length - 1].why); }
    return { id: entry.id, group: entry.group, title: text.title, colour, result, why, evidence, check: text.check, overturn: text.overturn, needs: text.needs, momentSlot, momentLane, order };
  });
}

// ---------------------------------------------------------------------------------------------------------------------------------
// What the rail lists: pinned data checks, then the top of the list, then the rest on request; the ruled-out ones fold away.

const RANK: Record<SuspectColour, number> = { red: 0, orange: 1, grey: 2, green: 3 };

export interface SuspectsView {
  total: number;
  counts: Record<SuspectColour, number>;
  pinned: Suspect[];
  shown: Suspect[];
  green: Suspect[];
  /** True when "See all" has anything more to show. */
  more: boolean;
  /** False when no suspect rose to orange or red, so the rail says nothing is ranked. */
  anyRanked: boolean;
}

export function suspectsView(list: Suspect[], all: boolean): SuspectsView {
  const counts: Record<SuspectColour, number> = { red: 0, orange: 0, grey: 0, green: 0 };
  for (const s of list) counts[s.colour]++;
  const sorted = list.filter((s) => s.colour !== 'green').sort((a, b) => RANK[a.colour] - RANK[b.colour] || a.order - b.order);
  const pinned = PINNED_IDS.map((id) => sorted.find((s) => s.id === id)).filter((s): s is Suspect => !!s);
  const rest = sorted.filter((s) => !PINNED_IDS.includes(s.id));
  const top = rest.slice(0, Math.max(SUSPECT_RULES.top, rest.filter((s) => s.colour === 'red').length));
  const green = list.filter((s) => s.colour === 'green').sort((a, b) => a.order - b.order);
  return {
    total: list.length,
    counts,
    pinned,
    shown: all ? rest : top,
    green: all ? green : [],
    more: rest.length > top.length || green.length > 0,
    anyRanked: counts.red + counts.orange > 0
  };
}

/** True when at least one lane holds events, so the rail has something to test. */
export function anyLaneRead(response: TracingResponse): boolean {
  return response.lanes.some((lane) => !lane.disabled && lane.state === 'events');
}

/** The start of a suspect's moment on the chart, for its sentence. Null when the grid is not usable. */
export function momentStartMs(response: TracingResponse, slot: number): number | null {
  const g = slotGrid(response.window);
  return g && slot >= 0 && slot < g.n ? slotStartMs(g, slot) : null;
}
