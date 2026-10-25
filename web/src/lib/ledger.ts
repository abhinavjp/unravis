/**
 * Pure helpers for the hypothesis ledger drawer (roadmap 8.6). No DOM, no network.
 * The analyst sets every state; nothing here suggests or computes one. There is no "proven cause" state.
 */
import type { Hypothesis, HypothesisAlternative, HypothesisFacts, HypothesisInput, HypothesisState } from './types';

/** States in the order the selector shows them. */
export const STATE_ORDER: HypothesisState[] = ['open', 'supported_by_correlation', 'supported_by_strong_correlation', 'contradicted', 'not_testable'];

export const STATE_LABEL: Record<HypothesisState, string> = {
  open: 'Open',
  supported_by_correlation: 'Supported by correlation',
  supported_by_strong_correlation: 'Supported by strong correlation',
  contradicted: 'Contradicted',
  not_testable: 'Not testable with available evidence'
};

export const ALT_STATUS_ORDER: HypothesisAlternative['status'][] = ['open', 'ruled_out', 'not_testable'];
export const ALT_STATUS_LABEL: Record<HypothesisAlternative['status'], string> = { open: 'Open', ruled_out: 'Ruled out', not_testable: 'Not testable' };

/** The most characters the settling observation may hold. The server enforces the same limit (internal/ledger MaxSettlingLen). */
export const SETTLING_MAX = 500;

/** Every fixed sentence and label the drawer shows. The component reads these, and the tests lint them. */
export const ledgerCopy = {
  title: 'Ledger',
  intro: 'What we believe, what supports it, what contradicts it, and what is not yet known.',
  notice: 'The ledger keeps hypotheses and references only. Do not paste request rows, ids, users or IPs.',
  empty: 'No hypotheses yet. Add the first one below.',
  addLabel: 'New hypothesis',
  addPlaceholder: 'What do you think happened?',
  addButton: 'Add',
  textLabel: 'Hypothesis',
  stateLabel: 'State',
  stateNote: 'You set the state. The app never suggests one.',
  evidenceLabel: 'Evidence references',
  evidenceHint: 'Use Cite in ledger on Patterns to collect references, or paste them here. They are not request ids. Separate them with commas or new lines.',
  notesLabel: 'Notes',
  examinedLabel: 'Episodes examined',
  presentLabel: 'Episodes where it was present',
  counterLabel: 'Counterexamples',
  counterHint: 'One per line: episodes where it was absent, or present without a stall.',
  alternativesLabel: 'Other explanations',
  alternativeText: 'Other explanation',
  alternativeStatus: 'Status of this explanation',
  addAlternative: 'Add an explanation',
  removeAlternative: 'Remove',
  joinLabel: 'Join ambiguity (%)',
  joinHint: 'Optional. The share of rows where the join was ambiguous.',
  joinProblem: 'Enter a number from 0 to 100, or leave the box empty.',
  timezone: 'Timezone mapping recorded',
  confounder: 'Confounder overlaps',
  coverageGap: 'Coverage gap',
  overlapOnly: 'Support is temporal overlap only',
  guardLead: 'Strong correlation is blocked now:',
  blockedLead: 'Not saved. Strong correlation is blocked:',
  blockedNote: 'Your other edits are not saved yet.',
  rateProblem: 'Present cannot be higher than examined.',
  save: 'Save',
  saving: 'Saving',
  reuse: 'Start again in a new incident',
  reuseNote: 'Starts a new open entry. The old state is kept as history, not as support.',
  remove: 'Delete',
  removeConfirm: 'Delete this hypothesis?',
  removeYes: 'Yes, delete',
  removeNo: 'Keep it',
  historyLabel: 'History',
  historyNote: 'Earlier states are history, never support.',
  loading: 'Loading',
  noRate: 'Episodes not stated',
  addEmpty: 'Write a hypothesis first.',
  numberProblem: 'Enter a whole number.',
  unsaved: 'Unsaved changes',
  draftNote: 'Unsaved changes are kept until this tab closes.',
  discard: 'Discard changes',
  strongHint: 'Choose Supported by strong correlation above to see what is missing.',
  saveBlocked: 'Save is off until the points above are covered, or you pick another state.',
  citedLead: 'References you cited',
  citedHint: 'Open an entry and choose Add to evidence to attach them.',
  citedRemove: 'Remove this reference',
  citedAdd: 'Add to evidence',
  citedAdded: 'Added to the evidence box. Save to keep them.',
  idWarningTail: 'The ledger should not hold identifiers.'
} as const;

export function stateLabel(state: HypothesisState | string): string {
  return STATE_LABEL[state as HypothesisState] ?? 'Open';
}

/** "3 of 3 episodes" (singular: "1 of 1 episode"). Empty when no episodes are stated. */
export function presenceText(presentK: number, totalN: number): string {
  const n = Math.max(0, Math.floor(totalN || 0));
  const k = Math.max(0, Math.floor(presentK || 0));
  if (n === 0) return '';
  return k + ' of ' + n + (n === 1 ? ' episode' : ' episodes');
}

export function defaultFacts(): HypothesisFacts {
  return { presentK: 0, totalN: 0, counterexamples: [], alternatives: [], timezoneRecorded: false, confounderOverlap: false, coverageGap: false, overlapOnly: false };
}

/** What the editor holds while the analyst types. Numbers stay text so a half-typed value is not rewritten. */
export interface EditorState {
  text: string;
  state: HypothesisState;
  evidenceText: string;
  notes: string;
  settling: string;
  presentK: string;
  totalN: string;
  counterText: string;
  alternatives: HypothesisAlternative[];
  joinPct: string;
  timezoneRecorded: boolean;
  confounderOverlap: boolean;
  coverageGap: boolean;
  overlapOnly: boolean;
}

export function editorFromHypothesis(h: Hypothesis): EditorState {
  const f = { ...defaultFacts(), ...h.facts };
  return {
    text: h.text,
    state: h.state,
    evidenceText: (h.evidenceIds ?? []).join('\n'),
    notes: h.notes ?? '',
    settling: h.settlingObservation ?? '',
    presentK: String(f.presentK ?? 0),
    totalN: String(f.totalN ?? 0),
    counterText: (f.counterexamples ?? []).join('\n'),
    alternatives: (f.alternatives ?? []).map((a) => ({ text: a.text, status: a.status })),
    joinPct: f.joinAmbiguousPct === undefined || f.joinAmbiguousPct === null ? '' : String(f.joinAmbiguousPct),
    timezoneRecorded: !!f.timezoneRecorded,
    confounderOverlap: !!f.confounderOverlap,
    coverageGap: !!f.coverageGap,
    overlapOnly: !!f.overlapOnly
  };
}

/** Splits a comma or line list into trimmed, non-empty, de-duplicated items. */
export function parseList(text: string): string[] {
  const seen = new Set<string>();
  for (const part of text.split(/[,\n\r]+/)) {
    const item = part.trim();
    if (item) seen.add(item);
  }
  return [...seen];
}

/** One item per line (commas are allowed inside a counterexample). */
export function parseLines(text: string): string[] {
  return text.split(/\r?\n/).map((s) => s.trim()).filter(Boolean);
}

function wholeNumber(text: string): number {
  const n = Math.floor(Number(text));
  return Number.isFinite(n) && n > 0 ? n : 0;
}

/**
 * A percentage from 0 to 100, or undefined. A value outside 0..100 is never clamped into range:
 * it is undefined, and joinProblem reports it so Save stays disabled.
 */
export function parsePercent(text: string): number | undefined {
  if (text.trim() === '') return undefined;
  const n = Number(text);
  if (!Number.isFinite(n) || n < 0 || n > 100) return undefined;
  return n;
}

/** A problem with the join ambiguity box, or an empty string. Empty is fine (not stated). */
export function joinProblem(e: Pick<EditorState, 'joinPct'>): string {
  return e.joinPct.trim() !== '' && parsePercent(e.joinPct) === undefined ? ledgerCopy.joinProblem : '';
}

/** A problem with the stated rate, or an empty string. Save is disabled while there is one. */
export function rateProblem(e: Pick<EditorState, 'presentK' | 'totalN'>): string {
  return wholeNumber(e.presentK) > wholeNumber(e.totalN) ? ledgerCopy.rateProblem : '';
}

export function buildInput(e: EditorState): HypothesisInput {
  const joinAmbiguousPct = parsePercent(e.joinPct);
  const facts: HypothesisFacts = {
    presentK: wholeNumber(e.presentK),
    totalN: wholeNumber(e.totalN),
    counterexamples: parseLines(e.counterText),
    alternatives: e.alternatives.map((a) => ({ text: a.text.trim(), status: a.status })).filter((a) => a.text !== ''),
    timezoneRecorded: e.timezoneRecorded,
    confounderOverlap: e.confounderOverlap,
    coverageGap: e.coverageGap,
    overlapOnly: e.overlapOnly,
    ...(joinAmbiguousPct === undefined ? {} : { joinAmbiguousPct })
  };
  return { text: e.text.trim(), state: e.state, evidenceIds: parseList(e.evidenceText), notes: e.notes, settlingObservation: e.settling.trim(), facts };
}

/** One history line: the earlier state, the date, and the note. Shown as history, never as support. */
export function historyLine(entry: { state: HypothesisState; at: string; note: string }): string {
  const date = /^\d{4}-\d{2}-\d{2}/.test(entry.at) ? entry.at.slice(0, 10) : '';
  return [stateLabel(entry.state), date, entry.note].filter(Boolean).join(', ');
}

/** Whole-number text for the two episode boxes. Empty is fine (not stated); anything else must be digits only. */
export function numberProblem(text: string): string {
  const t = text.trim();
  return t === '' || /^\d+$/.test(t) ? '' : ledgerCopy.numberProblem;
}

/** True when the editor holds changes that are not saved yet. Compares what Save would send. */
export function isDirty(h: Hypothesis, e: EditorState): boolean {
  return JSON.stringify(buildInput(editorFromHypothesis(h))) !== JSON.stringify(buildInput(e));
}

/** The server blocks "strong correlation" with these checks (internal/ledger/guards.go). They are mirrored here so Save can stay off instead of sending a save that is known to fail. The server still has the last word. */
export interface StrongCheck { id: string; ok: boolean; reason: string }

const MIN_EPISODES = 3;
const MAX_JOIN_AMBIGUOUS = 10;

function episodeCount(n: number): string {
  return n === 1 ? '1 independent episode' : n + ' independent episodes';
}

export function strongChecks(e: EditorState): StrongCheck[] {
  const input = buildInput(e);
  const f = input.facts;
  const n = f.totalN, k = f.presentK;
  const countsValid = k <= n;
  const listed = f.counterexamples.length;
  const missing = n - k;
  const join = f.joinAmbiguousPct;
  let presence = '';
  if (n === 0 && k === 0) presence = 'The presence rate is not stated (k of n episodes with the linked event present).';
  else if (!countsValid) presence = 'The presence rate ' + k + ' of ' + n + ' is not valid.';
  else if (k * 5 < n * 4) presence = 'The presence rate is ' + k + ' of ' + n + ', below the minimum (3 of 3, 4 of 4, or 80% of 5 or more).';
  let joinReason = '';
  if (join === undefined) joinReason = 'The join class is not stated (share of rows with an ambiguous join).';
  else if (join > MAX_JOIN_AMBIGUOUS) joinReason = 'The join is ambiguous for ' + join + '% of rows, above the 10% limit.';
  const checks: StrongCheck[] = [
    { id: 'coverage', ok: !f.coverageGap, reason: 'The evidence window has a coverage gap.' },
    { id: 'episodes', ok: n >= MIN_EPISODES, reason: episodeCount(n) + '; strong correlation needs at least ' + MIN_EPISODES + ' (a volley counts as one episode).' },
    { id: 'presence', ok: presence === '', reason: presence },
    { id: 'counterexamples', ok: countsValid && listed >= missing, reason: countsValid && listed < missing ? listed + ' counterexample(s) listed, but ' + episodeCount(missing) + ' lacked the linked event; list each one.' : '' },
    { id: 'alternatives', ok: f.alternatives.length > 0, reason: 'No alternative explanation is named; name every plausible alternative with its status.' },
    { id: 'join', ok: joinReason === '', reason: joinReason },
    { id: 'timezone', ok: f.timezoneRecorded, reason: "The source's timezone mapping is not recorded." },
    { id: 'confounder', ok: !f.confounderOverlap, reason: 'A known confounder (a scheduled job or a recycle event) overlaps the episodes.' },
    { id: 'overlap', ok: !f.overlapOnly, reason: 'The support is temporal overlap alone; temporal overlap alone never reaches strong correlation.' },
    { id: 'evidence', ok: input.evidenceIds.length > 0, reason: 'No evidence references are linked.' }
  ];
  return checks;
}

/** The reasons strong correlation is blocked for this editor right now. Empty when no check blocks it. */
export function strongReasons(e: EditorState): string[] {
  return strongChecks(e).filter((c) => !c.ok && c.reason !== '').map((c) => c.reason);
}

/** "4 of 10 checks met for strong correlation". */
export function strongProgress(e: EditorState): string {
  const checks = strongChecks(e);
  return checks.filter((c) => c.ok).length + ' of ' + checks.length + ' checks met for strong correlation.';
}

/**
 * An inline warning when the text looks like it holds an identifier (IP address, email, UUID, or a user, account, tenant
 * or request id). This only warns; it never blocks, because it can be wrong. Empty when nothing looks like one.
 */
export function identifierWarning(text: string): string {
  let what = '';
  if (/\b(?:\d{1,3}\.){3}\d{1,3}\b/.test(text) || /\b(?:[0-9a-f]{1,4}:){4,}[0-9a-f]{0,4}\b/i.test(text)) what = 'an IP address';
  else if (/\b[\w.+-]+@[\w-]+\.[\w.-]+/.test(text)) what = 'an email address';
  else if (/\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b/i.test(text)) what = 'an id';
  else if (/\b(?:user|users|userid|uid|account|accountid|tenant|request|requestid|session|ip)[\s_-]*(?:id)?\s*[:=#]?\s*[\w-]*\d[\w-]{2,}\b/i.test(text)) what = /\b(?:user|users|userid|uid)\b/i.test(text) ? 'a user id' : 'an id';
  return what ? 'This looks like ' + what + '. ' + ledgerCopy.idWarningTail : '';
}

/** One reference collected from Patterns, waiting to be attached to an entry. */
export interface CitedRef { id: string; label: string }

/** Adds a reference unless that id is already there. Blank ids are ignored. Returns the same list when nothing changes. */
export function addCitation(list: CitedRef[], id: string, label: string): CitedRef[] {
  const key = id.trim();
  if (!key || list.some((c) => c.id === key)) return list;
  return [...list, { id: key, label: label.trim() || key }];
}

/** Appends references to an evidence box, keeping what is there and skipping duplicates. */
export function appendEvidence(evidenceText: string, ids: string[]): string {
  return parseList(evidenceText + '\n' + ids.join('\n')).join('\n');
}
