import type { Change, ChecklistItem, Readiness, Result, Step, Suspect, SuspectColour } from './types';

/**
 * Pure models for the guided pages. The engine answers; this file only picks fields and words for them. It never changes an engine value,
 * never turns a missing number into zero, and never gives a "pass" word: the only words are Ready, Partly ready, Cannot test yet and Failed,
 * plus the pending words Waiting and Running while no answer is on screen.
 */

export const STEP_COUNT = 11;

export interface StepDef { number: number; label: string; title: string }

export const STEPS: readonly StepDef[] = [
  { number: 0, label: 'Frame', title: 'Frame the question' },
  { number: 1, label: 'Trust data', title: 'Can I trust the data?' },
  { number: 2, label: 'Shape', title: 'Shape' },
  { number: 3, label: 'Time split', title: 'Time split' },
  { number: 4, label: 'Load', title: 'Load' },
  { number: 5, label: 'Changes', title: 'Changes' },
  { number: 6, label: 'App tier', title: 'App tier' },
  { number: 7, label: 'SQL', title: 'SQL Server' },
  { number: 8, label: 'Mongo', title: 'MongoDB' },
  { number: 9, label: 'Host', title: 'Host' },
  { number: 10, label: 'Origin', title: 'Origin and suspects' }
];

export type RunPhase = 'idle' | 'running' | 'done' | 'failed';
export type StickerTone = 'ready' | 'partly' | 'cannot' | 'failed' | 'waiting' | 'running';
export interface Sticker { word: string; tone: StickerTone; outOfDate: boolean }

const TONE: Record<Readiness, StickerTone> = { Ready: 'ready', 'Partly ready': 'partly', 'Cannot test yet': 'cannot', Failed: 'failed' };

export const stepOf = (result: Result | null | undefined, n: number): Step | undefined => result?.steps.find((s) => s.number === n);

/**
 * The sticker on a step. With no answer yet it is Waiting (hatched); while a run is going it is Running. With an answer it is the engine's word,
 * and `outOfDate` says the windows, sources or lists have changed since: the word then stays but the page asks for a rerun.
 */
export function stickerFor(result: Result | null | undefined, n: number, phase: RunPhase, outOfDate = false): Sticker {
  if (phase === 'running') return { word: 'Running', tone: 'running', outOfDate: false };
  const step = stepOf(result, n);
  if (!step) return { word: 'Waiting', tone: 'waiting', outOfDate: false };
  return { word: step.readiness, tone: TONE[step.readiness] ?? 'cannot', outOfDate };
}

export interface RailItem { number: number; label: string; sticker: Sticker; current: boolean }

export function railItems(result: Result | null | undefined, phase: RunPhase, current: number, outOfDate: boolean): RailItem[] {
  return STEPS.map((s) => ({ number: s.number, label: s.label, sticker: stickerFor(result, s.number, phase, outOfDate && s.number > 0), current: s.number === current }));
}

/** The step a person lands on after a run: 0 when nothing ran yet, else where they were (clamped). */
export const clampStep = (n: number): number => (Number.isInteger(n) ? Math.min(STEP_COUNT - 1, Math.max(0, n)) : 0);

/* ----------------------------------------------------------------- numbers and words */

const NONE = 'No data';

export const fmtCount = (n: number | undefined): string => (typeof n === 'number' && Number.isFinite(n) ? Math.round(n).toLocaleString('en-US') : NONE);
export const fmtMs = (n: number | undefined): string => (typeof n === 'number' && Number.isFinite(n) ? Math.round(n).toLocaleString('en-US') + ' ms' : NONE);
export const fmtNumber = (n: number | undefined, digits = 1): string => (typeof n === 'number' && Number.isFinite(n) ? n.toLocaleString('en-US', { maximumFractionDigits: digits, minimumFractionDigits: 0 }) : NONE);
export const fmtShare = (n: number | undefined): string => (typeof n === 'number' && Number.isFinite(n) ? Math.round(n * 100) + ' %' : NONE);

/** A signed whole percent such as +733 % or -12 %. */
export function fmtPct(n: number | undefined): string {
  if (typeof n !== 'number' || !Number.isFinite(n)) return NONE;
  const r = Math.round(n);
  return (r > 0 ? '+' : r < 0 ? '-' : '') + Math.abs(r).toLocaleString('en-US') + ' %';
}

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const p2 = (n: number) => String(n).padStart(2, '0');

/** `26 Oct 10:42` in UTC. A string that is not a time comes back as "No data", never as a made-up date. */
export function fmtWhen(value: string | undefined): string {
  const ms = value ? Date.parse(value) : NaN;
  if (!Number.isFinite(ms)) return NONE;
  const d = new Date(ms);
  return `${d.getUTCDate()} ${MONTHS[d.getUTCMonth()]} ${p2(d.getUTCHours())}:${p2(d.getUTCMinutes())}`;
}

export interface ChangeMark { symbol: '▲' | '▼' | '●'; word: 'Slower' | 'Faster' | 'No change' | 'Not sure'; tone: 'bad' | 'ok' | 'grey' }

/**
 * The mark beside a change: ▲ red when the slow end rose by the engine's rule (30 % and 100 ms), ▼ green when it fell by the same rule, ● grey
 * for anything under the rule. Grey also covers a change that could not be worked out, so a gap never reads as a result.
 */
export function markFor(change: Change | undefined): ChangeMark {
  if (!change || !change.known) return { symbol: '●', word: 'Not sure', tone: 'grey' };
  if (change.meaningful && change.direction === 'up') return { symbol: '▲', word: 'Slower', tone: 'bad' };
  if (change.meaningful && change.direction === 'down') return { symbol: '▼', word: 'Faster', tone: 'ok' };
  return { symbol: '●', word: 'No change', tone: 'grey' };
}

/** The change as words for a table cell: "+733 % (+2,100 ms)" or "No data". */
export function fmtChange(change: Change | undefined): string {
  if (!change || !change.known) return NONE;
  const ms = typeof change.deltaMs === 'number' ? ` (${change.deltaMs > 0 ? '+' : change.deltaMs < 0 ? '-' : ''}${Math.abs(Math.round(change.deltaMs)).toLocaleString('en-US')} ms)` : '';
  return fmtPct(change.deltaPct) + ms;
}

/* ----------------------------------------------------------------- Step 1 */

export interface Card { label: string; value: string; note: string }

export function trustCards(result: Result): Card[] {
  const f = result.facts;
  const gaps = f.gaps ?? [];
  const long = gaps.filter((g) => g.kind === 'long').length;
  const clockValue = f.clock.anomaly === 'yes' ? 'Off in ' + fmtCount(f.clock.anomalous) + ' calls' : f.clock.anomaly === 'no' ? 'Looks fine' : 'Not checked';
  return [
    { label: 'Calls in the incident window', value: fmtCount(f.counts.incident), note: f.counts.incident ? 'Calls that started in the window.' : 'No call started in this window.' },
    { label: 'Calls in the reference window', value: fmtCount(f.counts.reference), note: f.counts.reference ? 'The normal period.' : 'No call started in this window.' },
    { label: 'Time gaps', value: fmtCount(gaps.length), note: long ? `${long} long: those minutes are not tested.` : gaps.length ? 'Short: the step runs on the minutes that exist.' : 'No minute is missing.' },
    { label: 'Clock difference', value: clockValue, note: f.clock.checked ? `${fmtCount(f.clock.checked)} calls checked.` : 'No call had both a start and an end.' }
  ];
}

export interface SourceRow { name: string; state: 'Ready' | 'Cannot test yet'; used: boolean }

export function sourceRows(result: Result): SourceRow[] {
  const c = result.confidence;
  return [
    ...c.sourcesUsed.map((name): SourceRow => ({ name, state: 'Ready', used: true })),
    ...c.sourcesMissing.map((name): SourceRow => ({ name, state: 'Cannot test yet', used: false }))
  ];
}

/** True when Step 1 has nothing to run on: no call in either window. The page then offers to change the window or add data. */
export const notEnoughData = (result: Result): boolean => result.facts.counts.incident === 0 || result.facts.counts.reference === 0;

/* ----------------------------------------------------------------- Step 2 */

export interface ShapeRow { route: string; calls: number; incident: string; reference: string; change: string; mark: ChangeMark }

/** The by-API table, the biggest rise first. An API with no known change goes last. */
export function shapeRows(result: Result): ShapeRow[] {
  const sortKey = (c: Change) => (c.known && typeof c.deltaMs === 'number' ? c.deltaMs : -Infinity);
  return [...result.facts.routes]
    .sort((a, b) => sortKey(b.p95Change) - sortKey(a.p95Change) || a.route.localeCompare(b.route))
    .map((r) => ({ route: r.route, calls: r.incident.n, incident: fmtMs(r.incident.p95), reference: fmtMs(r.reference.p95), change: fmtChange(r.p95Change), mark: markFor(r.p95Change) }));
}

export const movedCount = (result: Result): number => result.facts.routes.filter((r) => markFor(r.p95Change).tone === 'bad' || markFor(r.p95Change).tone === 'ok').length;

/** Nothing moved: at least one API could be compared and none changed by the rule. */
export function nothingMoved(result: Result): boolean {
  const compared = result.facts.routes.filter((r) => r.p95Change.known).length;
  return compared > 0 && movedCount(result) === 0;
}

/* ----------------------------------------------------------------- Step 3 */

export interface PartRow { key: string; label: string; normal: string; incident: string; change: string; share: string; tested: boolean; why: string }

const PART_LABELS: Record<string, string> = { A: 'Audit set-up', C: 'Action time', P: 'Payload record', O: 'Outside time' };

export function partRows(result: Result): PartRow[] {
  return result.facts.parts.items.map((p) => ({
    key: p.name, label: PART_LABELS[p.name] ? `${PART_LABELS[p.name]} (${p.name})` : p.name,
    normal: fmtMs(p.refMeanMs), incident: fmtMs(p.incMeanMs), change: p.tested ? fmtChange(p.change) : 'Cannot test yet',
    share: p.tested ? fmtShare(p.share) : 'Cannot test yet', tested: p.tested, why: p.why ?? ''
  }));
}

export interface Bar { key: string; label: string; ms: number; pct: number }

/**
 * The two bars of the time split, on one scale: the longer bar is the full width, so the lengths can be compared. Parts that could not be
 * tested are left out of the bar and listed by the caller; they are never drawn as zero.
 */
export function partBars(result: Result): { normal: Bar[]; incident: Bar[] } {
  const tested = result.facts.parts.items.filter((p) => p.tested);
  const sum = (pick: (p: (typeof tested)[number]) => number | undefined) => tested.reduce((t, p) => t + (pick(p) ?? 0), 0);
  const top = Math.max(sum((p) => p.refMeanMs), sum((p) => p.incMeanMs), 0);
  const make = (pick: (p: (typeof tested)[number]) => number | undefined): Bar[] => tested
    .filter((p) => typeof pick(p) === 'number' && (pick(p) as number) > 0)
    .map((p) => ({ key: p.name, label: PART_LABELS[p.name] ?? p.name, ms: pick(p) as number, pct: top > 0 ? ((pick(p) as number) / top) * 100 : 0 }));
  return { normal: make((p) => p.refMeanMs), incident: make((p) => p.incMeanMs) };
}

/* ----------------------------------------------------------------- Step 4 */

export interface LoadRow { measure: string; normal: string; incident: string; direction: string }

export function loadRows(result: Result): LoadRow[] {
  const l = result.facts.load;
  const dir = (word: string) => (word === 'up' ? 'Higher' : word === 'down' ? 'Lower' : word === 'flat' ? 'About the same' : 'Not sure');
  return [
    { measure: 'Calls arriving per second', normal: fmtNumber(l.refPerSec, 2), incident: fmtNumber(l.incPerSec, 2), direction: dir(l.lambdaDirection) },
    { measure: 'Time a call spends in the system', normal: fmtMs(l.refWms), incident: fmtMs(l.incWms), direction: dir(l.wDirection) },
    { measure: 'Calls in flight (average)', normal: fmtNumber(l.refL, 1), incident: fmtNumber(l.incL, 1), direction: dir(l.lDirection) },
    { measure: 'Calls in flight (highest minute)', normal: fmtCount(l.refPeak), incident: fmtCount(l.incPeak), direction: l.incPeak > l.refPeak ? 'Higher' : l.incPeak < l.refPeak ? 'Lower' : 'About the same' }
  ];
}

/* ----------------------------------------------------------------- Steps 6 to 9 and 10 */

/** Which suspect group each step reads. Steps 2 to 4 have none: they show facts only. */
export const STEP_GROUP: Readonly<Record<number, string>> = { 1: 'Q', 4: 'L', 5: 'C', 6: 'S', 7: 'D', 8: 'M', 9: 'H' };

export const suspectsForStep = (result: Result, n: number): Suspect[] => {
  const group = STEP_GROUP[n];
  return group ? result.suspects.filter((s) => s.group === group).sort((a, b) => a.rank - b.rank) : [];
};

export const COLOUR_WORD: Readonly<Record<SuspectColour, string>> = {
  red: 'Most likely suspect', orange: 'Likely suspect', green: 'Ruled out by the data we have', grey: 'Not sure'
};

export interface SuspectGroups { ranked: Suspect[]; notTested: Suspect[]; setAside: Suspect[] }

/** Step 10's three groups: red and orange (most likely first), grey (never a pass), and green (set aside). */
export function suspectGroups(result: Result): SuspectGroups {
  const byRank = (a: Suspect, b: Suspect) => a.rank - b.rank || a.id.localeCompare(b.id);
  const all = [...result.suspects].sort(byRank);
  return {
    ranked: all.filter((s) => s.colour === 'red' || s.colour === 'orange'),
    notTested: all.filter((s) => s.colour === 'grey'),
    setAside: all.filter((s) => s.colour === 'green')
  };
}

/** The one check that is cheapest to run now: the next check of the first red or orange suspect, if there is one. */
export function cheapestCheck(result: Result): { suspect: Suspect; text: string } | null {
  const first = suspectGroups(result).ranked[0];
  return first && first.nextCheck ? { suspect: first, text: first.nextCheck } : null;
}

export interface OriginRow { tier: string; when: string; what: string; role: string }

export function originRows(result: Result): OriginRow[] {
  return result.origin.roles.map((r) => ({ tier: r.tier, when: r.at ? fmtWhen(r.at) : 'Not dated', what: r.why, role: r.role }));
}

export const hasNamedOrigin = (result: Result): boolean => result.origin.status === 'named';

/** Observed or inferred: a tier that moved on its own data is observed; a suspect with only an inference chain is inferred. */
export function evidenceKind(s: Suspect): 'Observed' | 'Inferred' {
  return s.evidence.some((e) => e.value === 'yes' && e.role !== 'lane') && s.result !== 'Cannot test yet' ? 'Observed' : 'Inferred';
}

/* ----------------------------------------------------------------- the checklist */

export const LISTS: ReadonlyArray<{ id: ChecklistItem['list']; label: string }> = [
  { id: 'todo', label: 'Need to do' },
  { id: 'doing', label: 'Working on' },
  { id: 'learned', label: 'What we learned' },
  { id: 'ruled_out', label: 'Ruled out' }
];

export function groupItems(items: readonly ChecklistItem[]): Record<ChecklistItem['list'], ChecklistItem[]> {
  const out: Record<ChecklistItem['list'], ChecklistItem[]> = { todo: [], doing: [], learned: [], ruled_out: [] };
  for (const item of items) out[item.list]?.push(item);
  for (const key of Object.keys(out) as Array<ChecklistItem['list']>) out[key].sort((a, b) => a.order - b.order || a.created.localeCompare(b.created));
  return out;
}

/** Done is what we learned plus what is ruled out; Open is what still needs doing or is being worked on. */
export const counters = (items: readonly ChecklistItem[]): { done: number; open: number } => ({
  done: items.filter((i) => i.list === 'learned' || i.list === 'ruled_out').length,
  open: items.filter((i) => i.list === 'todo' || i.list === 'doing').length
});

/**
 * The checks Step 10 suggests: the next check of each red, orange and grey suspect that has one. A check already on the list (same text) is not
 * suggested again, so pressing the button twice adds nothing the second time.
 */
export function suggestedChecks(result: Result, items: readonly ChecklistItem[]): Array<{ text: string; suspect: string }> {
  const have = new Set(items.map((i) => i.text.trim().toLowerCase()));
  const seen = new Set<string>();
  const out: Array<{ text: string; suspect: string }> = [];
  const { ranked, notTested } = suspectGroups(result);
  for (const s of [...ranked, ...notTested]) {
    const text = `${s.id}: ${s.nextCheck}`.slice(0, 300);
    const key = text.toLowerCase();
    if (!s.nextCheck || have.has(key) || seen.has(key)) continue;
    seen.add(key);
    out.push({ text, suspect: s.id });
  }
  return out;
}
