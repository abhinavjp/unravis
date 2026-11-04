import type { LintEntry } from '../copy/copylint';
import type { TracingResponse, TracingSourceRef } from './model';

/**
 * The "Sources traced" picker of the Investigation Timeline (the chip row of the wireframe's timeline toolbar). Pure module, no
 * Svelte. A source is on or off; nothing is selected by default, so the starting state is "all". The picker keeps three states
 * apart because they mean three different things to the server and to the person:
 *   all  - nothing is sent ("sourceIds" absent), the server traces every source of the case;
 *   some - the chosen ids are sent as `sourceIds`;
 *   none - NOTHING is sent and nothing is read: the screen says so. It is never turned into "all".
 */

export type SourceKind = 'audit' | 'sql' | 'iis' | 'file';

export interface SourceChip {
  id: string;
  label: string;
  kind: SourceKind;
}

export type SourceSelection = { mode: 'all' } | { mode: 'none' } | { mode: 'some'; ids: string[] };

export const ALL_SOURCES: SourceSelection = { mode: 'all' };
export const NO_SOURCES: SourceSelection = { mode: 'none' };

export const SOURCES_TEXT = {
  label: 'Sources traced',
  note: 'Choose which sources the timeline reads. Each one can be switched off.',
  noSources: 'This case has no sources yet.',
  selectAll: 'Select all',
  clearAll: 'Clear all',
  savedNotConnected: 'saved, not connected',
  connect: 'Connect',
  connectTitle: 'Connect this database so its requests can be read.',
  noneSelected: 'No sources are selected, so nothing is traced. Choose at least one source.',
  laneUnselected: 'No source for this lane is selected.',
  readLabel: 'Read',
  savedLabel: 'Saved, not connected',
  kindAudit: 'API audit',
  kindSql: 'SQL',
  kindIis: 'IIS',
  kindFile: 'File',
  capNote: 'At most 50 sources can be traced at once. Switch one off first.',
  newNotOn: 'The new file’s source was not switched on. 50 sources are already on.'
} as const;

const KIND_WORD: Record<SourceKind, string> = {
  audit: SOURCES_TEXT.kindAudit,
  sql: SOURCES_TEXT.kindSql,
  iis: SOURCES_TEXT.kindIis,
  file: SOURCES_TEXT.kindFile
};

/** The chip text as the wireframe words it: the kind, a dot, then the name ("API audit · Orders"). */
export const chipText = (chip: SourceChip): string => `${KIND_WORD[chip.kind]} · ${chip.label}`;

/** "Tracing all 3 sources." / "Tracing 2 of 3 sources." / "Tracing no sources." */
export function sourcesSummary(selection: SourceSelection, chips: SourceChip[]): string {
  const total = chips.length;
  const word = (n: number) => (n === 1 ? 'source' : 'sources');
  if (selection.mode === 'none') return 'Tracing no sources.';
  if (selection.mode === 'all') return `Tracing all ${total} ${word(total)}.`;
  const on = chips.filter((chip) => selection.ids.includes(chip.id)).length;
  return `Tracing ${on} of ${total} ${word(total)}.`;
}

export function isChipOn(selection: SourceSelection, id: string): boolean {
  if (selection.mode === 'all') return true;
  if (selection.mode === 'none') return false;
  return selection.ids.includes(id);
}

/** Turns the chosen ids back into a selection: every chip on is "all", none on is "none", anything else "some". */
export function selectionOf(on: string[], chips: SourceChip[]): SourceSelection {
  const known = chips.map((chip) => chip.id);
  const chosen = known.filter((id) => on.includes(id));
  if (chips.length > 0 && chosen.length === chips.length) return ALL_SOURCES;
  if (chosen.length === 0) return NO_SOURCES;
  return { mode: 'some', ids: chosen };
}

/** The longest explicit sourceIds list the server accepts; a longer one is refused with a 400. */
export const MAX_EXPLICIT_SOURCES = 50;

/** The result of pressing a chip: the selection that follows, and whether the press was refused because the list would pass the cap. */
export function toggleOutcome(selection: SourceSelection, chips: SourceChip[], id: string): { selection: SourceSelection; refused: boolean } {
  if (!chips.some((chip) => chip.id === id)) return { selection, refused: false };
  const on = chips.filter((chip) => isChipOn(selection, chip.id)).map((chip) => chip.id);
  const next = selectionOf(on.includes(id) ? on.filter((item) => item !== id) : [...on, id], chips);
  if (next.mode === 'some' && next.ids.length > MAX_EXPLICIT_SOURCES) return { selection, refused: true };
  return { selection: next, refused: false };
}

/** Flips one chip. Works from the chips that are listed, so "all" minus one becomes a list of the rest. A flip whose list reaches the
 * server's cap changes nothing: the request would be refused, so the chip stays as it is (toggleOutcome says so, for the note). */
export const toggleSource = (selection: SourceSelection, chips: SourceChip[], id: string): SourceSelection => toggleOutcome(selection, chips, id).selection;

/**
 * The ids of the file sources a case lists by default, as the server forms them: one `cs_<tag>` for every tag that is not a saved audit
 * database, then one `imp_<import>` for every kept import no tag points at. Used to find the sources a file added meanwhile brought.
 */
export function fileSourceIds(tags: ReadonlyArray<{ id: number; kind: string; importId: number }>, imports: ReadonlyArray<{ id: number; status: string }>): string[] {
  const own = tags.filter((tag) => tag.kind !== 'mongo-database');
  const linked = new Set(own.map((tag) => tag.importId).filter((id) => id > 0));
  return [
    ...own.map((tag) => 'cs_' + tag.id),
    ...imports.filter((rec) => (rec.status === 'ok' || rec.status === '') && !linked.has(rec.id)).sort((a, b) => a.id - b.id).map((rec) => 'imp_' + rec.id)
  ];
}

/**
 * After files were added: a hand-picked list ("some") also switches on the new sources, so the file just added is traced. "All" already
 * includes them and "none" was the person's choice, so both stay. If the list would pass the cap nothing is added and `refused` is set.
 */
export function switchOnNew(selection: SourceSelection, fresh: readonly string[]): { selection: SourceSelection; refused: boolean } {
  if (selection.mode !== 'some') return { selection, refused: false };
  const add = fresh.filter((id) => !selection.ids.includes(id));
  if (add.length === 0) return { selection, refused: false };
  if (selection.ids.length + add.length > MAX_EXPLICIT_SOURCES) return { selection, refused: true };
  return { selection: { mode: 'some', ids: [...selection.ids, ...add] }, refused: false };
}

/** What the request carries: `nothing` for no sources (no request at all), else the ids ([] = every source). */
export type SourceRequest = { nothing: true } | { nothing: false; ids: string[] };

export function composeSourceIds(selection: SourceSelection): SourceRequest {
  if (selection.mode === 'none') return { nothing: true };
  if (selection.mode === 'all') return { nothing: false, ids: [] };
  return selection.ids.length === 0 ? { nothing: true } : { nothing: false, ids: [...selection.ids] };
}

// ---------------------------------------------------------------------------------------------------------------------------------
// The chips: the live audit sources the server lists, the case's file sources, and the audit databases the case saved that no open
// connection carries (those are shown but cannot be switched on: there is nothing to read until they are connected).

export interface SourceUniverse {
  chips: SourceChip[];
  /** Audit databases the case saved that are not connected, by saved name. */
  saved: string[];
}

export const EMPTY_UNIVERSE: SourceUniverse = { chips: [], saved: [] };

function kindOfFile(res: TracingResponse, ref: TracingSourceRef): SourceKind {
  for (const lane of res.lanes) {
    if ((lane.key === 'sql' || lane.key === 'iis') && lane.sources.some((source) => source.sourceId === ref.sourceId)) return lane.key;
  }
  const text = ref.label.toLowerCase();
  if (/\bsql\b/.test(text)) return 'sql';
  if (/\biis\b/.test(text)) return 'iis';
  return 'file';
}

/** The chips one response names. File sources get their lane's kind from the lane that read them. */
export function chipsOf(res: TracingResponse): SourceChip[] {
  const out: SourceChip[] = [];
  const seen = new Set<string>();
  const add = (ref: TracingSourceRef, kind: SourceKind) => {
    if (seen.has(ref.sourceId)) return;
    seen.add(ref.sourceId);
    out.push({ id: ref.sourceId, label: ref.label || ref.sourceId, kind });
  };
  for (const ref of res.auditSources) add(ref, 'audit');
  for (const ref of res.sources) add(ref, kindOfFile(res, ref));
  return out;
}

/**
 * Folds a response into what is known so far. The audit chips follow the server's live listing whenever the answer carries one (always
 * for an "all" request, for a subset only when it listed audit sources): the row then shows exactly that listing and its saved names, so
 * a disconnected source disappears. When the listing failed (an incomplete audit lane and nothing listed) or was never made (a file-only
 * subset), the known audit chips and saved names stay. File chips: "all" replaces them, a subset adds and relabels. Audit chips stay
 * first, file chips after them.
 */
export function mergeUniverse(known: SourceUniverse, res: TracingResponse, askedForAll: boolean): SourceUniverse {
  const fresh = chipsOf(res);
  const listedAudit = res.auditSources.length > 0 || res.savedNotConnected.length > 0;
  const listFailed = !listedAudit && res.lanes.some((lane) => lane.key === 'audit' && lane.state === 'incomplete');
  const authoritative = listedAudit || (askedForAll && !listFailed);
  const audit = authoritative ? fresh.filter((chip) => chip.kind === 'audit') : known.chips.filter((chip) => chip.kind === 'audit');
  const saved = authoritative ? res.savedNotConnected : known.saved;
  if (askedForAll) return { chips: [...audit, ...fresh.filter((chip) => chip.kind !== 'audit')], saved };
  const byId = new Map(known.chips.filter((chip) => chip.kind !== 'audit').map((chip) => [chip.id, chip]));
  for (const chip of fresh) if (chip.kind !== 'audit') byId.set(chip.id, chip);
  return { chips: [...audit, ...byId.values()], saved };
}

// ---------------------------------------------------------------------------------------------------------------------------------
// Kept per case in this browser tab, the way the windows are, so a reload shows the same choice.

const STORAGE_KEY = 'unravis.tracing.sources.';

export interface StoredSources {
  selection: SourceSelection;
  /** What was known when the choice was made, so a reload can draw the whole row before any answer comes back. */
  universe: SourceUniverse;
}

const KINDS: SourceKind[] = ['audit', 'sql', 'iis', 'file'];

export function loadSources(caseId: string, storage?: Pick<Storage, 'getItem'>): StoredSources | null {
  try {
    const raw = (storage ?? window.sessionStorage).getItem(STORAGE_KEY + caseId);
    const value: unknown = raw ? JSON.parse(raw) : null;
    if (typeof value !== 'object' || value === null) return null;
    const record = value as Record<string, unknown>;
    const chips: SourceChip[] = [];
    if (Array.isArray(record.chips)) {
      for (const item of record.chips) {
        if (typeof item !== 'object' || item === null) continue;
        const chip = item as Record<string, unknown>;
        if (typeof chip.id === 'string' && chip.id !== '' && typeof chip.label === 'string' && KINDS.includes(chip.kind as SourceKind)) {
          chips.push({ id: chip.id, label: chip.label, kind: chip.kind as SourceKind });
        }
      }
    }
    const saved = Array.isArray(record.saved) ? record.saved.filter((name): name is string => typeof name === 'string' && name !== '') : [];
    const universe: SourceUniverse = { chips, saved };
    if (record.mode === 'none') return { selection: NO_SOURCES, universe };
    if (record.mode === 'some' && Array.isArray(record.ids)) {
      const ids = record.ids.filter((id): id is string => typeof id === 'string' && id !== '');
      return ids.length > 0 ? { selection: { mode: 'some', ids }, universe } : null;
    }
    return null;
  } catch {
    return null;
  }
}

/** Keeps the choice for this case. "All" is the default, so it removes the entry. Never throws. */
export function saveSources(caseId: string, selection: SourceSelection, universe: SourceUniverse, storage?: Pick<Storage, 'setItem' | 'removeItem'>): void {
  try {
    const target = storage ?? window.sessionStorage;
    if (selection.mode === 'all') { target.removeItem(STORAGE_KEY + caseId); return; }
    target.setItem(STORAGE_KEY + caseId, JSON.stringify({ mode: selection.mode, ids: selection.mode === 'some' ? selection.ids : [], chips: universe.chips, saved: universe.saved }));
  } catch { /* unavailable storage only means the choice is not kept */ }
}

/** Drops the choice kept for a case that was erased. Never throws. */
export function forgetSources(caseId: string, storage?: Pick<Storage, 'removeItem'>): void {
  if (!caseId) return;
  try {
    (storage ?? window.sessionStorage).removeItem(STORAGE_KEY + caseId);
  } catch { /* unavailable storage only means the choice is not dropped */ }
}

/**
 * The SQL and IIS lanes whose files are all switched off. Their empty lane must say so instead of "no file": the case has files, they
 * are simply not part of this reading. A lane with no file chip at all is not listed (it really has no file).
 */
export function unselectedLanes(selection: SourceSelection, chips: SourceChip[]): Array<'sql' | 'iis'> {
  const out: Array<'sql' | 'iis'> = [];
  for (const kind of ['sql', 'iis'] as const) {
    const mine = chips.filter((chip) => chip.kind === kind);
    if (mine.length > 0 && mine.every((chip) => !isChipOn(selection, chip.id))) out.push(kind);
  }
  return out;
}

export const sourcesCopyEntries = (): LintEntry[] => [
  ...Object.entries(SOURCES_TEXT).map(([id, text]) => ({ id: 'tracingSources.' + id, text })),
  { id: 'tracingSources.summaryAll', text: sourcesSummary(ALL_SOURCES, [{ id: 'a', label: 'A', kind: 'audit' }, { id: 'b', label: 'B', kind: 'sql' }]) },
  { id: 'tracingSources.summarySome', text: sourcesSummary({ mode: 'some', ids: ['a'] }, [{ id: 'a', label: 'A', kind: 'audit' }, { id: 'b', label: 'B', kind: 'sql' }]) },
  { id: 'tracingSources.summaryNone', text: sourcesSummary(NO_SOURCES, []) }
];
