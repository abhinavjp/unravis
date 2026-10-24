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

/** Every fixed sentence and label the drawer shows. The component reads these, and the tests lint them. */
export const ledgerCopy = {
  title: 'Ledger',
  intro: 'What we believe, what supports it, what contradicts it, and what is not yet known.',
  notice: 'The ledger keeps hypotheses and references only: no request rows, ids, users or IPs.',
  empty: 'No hypotheses yet. Add the first one below.',
  addLabel: 'New hypothesis',
  addPlaceholder: 'What do you think happened?',
  addButton: 'Add',
  textLabel: 'Hypothesis',
  stateLabel: 'State',
  stateNote: 'You set the state. The app never suggests one.',
  evidenceLabel: 'Evidence references',
  evidenceHint: 'These are references to the evidence list, not request ids. Separate them with commas or new lines.',
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
  noRate: 'Episodes not stated'
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
  return { text: e.text.trim(), state: e.state, evidenceIds: parseList(e.evidenceText), notes: e.notes, facts };
}

/** One history line: the earlier state, the date, and the note. Shown as history, never as support. */
export function historyLine(entry: { state: HypothesisState; at: string; note: string }): string {
  const date = /^\d{4}-\d{2}-\d{2}/.test(entry.at) ? entry.at.slice(0, 10) : '';
  return [stateLabel(entry.state), date, entry.note].filter(Boolean).join(', ');
}
