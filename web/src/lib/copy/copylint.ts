/**
 * Copy lint (roadmap §6.6 "Copy lint" and §8.14 writing rules). Pure TypeScript, no DOM.
 * `lintCopy` returns the findings for a set of on-screen texts; an empty array means pass.
 * `lintScreenStrings` is the entry point for component text; `lintTerms` lints the glossary entries.
 */
import type { Term } from './types';
import {
  auditCreate, countInWindow, episodeShare, lowestInWindow, markerAfter, markerBefore, metricRose, metricSampleOverlap, notObservedGap,
  notObservedThreshold, overlapAmbiguous, overlapBuffered, overlapUnique, plainAuditCreate, recycleGapEnabledTriggers,
  recycleGapLegacyDefaults, topInWindow
} from './wordings';

export interface LintEntry { id: string; text: string }
export interface LintFinding { id: string; rule: 'causal' | 'wording' | 'reading-level' | 'undefined-term'; message: string }

/** Highest Flesch-Kincaid grade an entry may have (tunable). */
export const MAX_GRADE = 8.5;
/** Reading level is only checked for entries with at least this many words. */
export const MIN_WORDS_FOR_GRADE = 8;

/** Technical words that need a glossary term or alias before they may appear on screen. */
export const JARGON: string[] = [
  'P50', 'P95', 'P99', 'percentile', 'coverage', 'episode', 'correlation', 'join', 'baseline', 'sampling', 'low sample'
];

/** Causal verbs and phrases. None may name a cause for a measurement (unless negated, see below). */
const CAUSAL_PATTERNS: RegExp[] = [
  /\bcaus(?:e|es|ed|ing)\b/gi, // the verb or the noun, including "root cause" and "the cause of"
  /\bbecause\b/gi,
  /\bwaiting on\b/gi,
  /\bwait(?:s|ed|ing)? for\b/gi,
  /\bblocked by\b/gi,
  /\bdue to\b/gi,
  /\bthe database was waiting\b/gi,
  /\b(?:is|was|were|are)\s+the\s+reason\b/gi,
  /\b(?:led|leads|leading)\s+to\b/gi,
  /\bresult(?:ed|s|ing)\s+(?:from|in)\b/gi,
  /\bresponsible for\b/gi
];

/** Diagnosis words (wave-2 plan 4.0). Copy describes what was measured, so these never appear, negated or not. */
const DIAGNOSIS_WORDS = /\b(?:leak|starv|saturat)\w*/gi;

/** A negation word, including the contractions don't, doesn't, isn't, can't and "cannot". */
const NEGATION = String.raw`(?:\b(?:not|never|cannot)\b|\b\w+n['’]t\b)`;
/** Claim verbs and nouns a negation can disclaim ("does not prove", "is not proof of"). */
const CLAIM_WORD = String.raw`(?:prove|proof|show|mean|say|imply|tell|explain|establish|confirm|demonstrate|state|evidence)`;

/**
 * Text that shows the causal word sits inside a negated clause. Two shapes:
 *  - a negation, up to two words, then a claim word ("does not prove that X caused Y", "is not proof of a cause");
 *  - the negation directly before the causal word ("is not because of", "is not the cause of").
 * The clause head passed in starts after the last comma, semicolon, colon, dash or sentence end,
 * so "does not prove a link, but it is because of" is never exempt.
 */
const NEGATED_CLAUSE = new RegExp(String.raw`${NEGATION}\s+(?:\w+\s+){0,2}?${CLAIM_WORD}\b`, 'i');
const NEGATED_DIRECTLY = new RegExp(String.raw`${NEGATION}\s+(?:(?:the|a|an|any|one)\s+)?$`, 'i');
const NEGATED_PROOF_BEFORE = new RegExp(String.raw`(?:${NEGATION}|\bno\b)\s+(?:\w+\s+)?$`, 'i');

const WEAK_LABEL = /\bsymptom\b|(?<!strong\s)\bcorrelation\b/i;

/** Text before `index` within its own clause (after the last comma, semicolon, colon, dash, bracket or sentence end). */
function clauseBefore(text: string, index: number): string {
  const head = text.slice(0, index);
  const cut = Math.max(...[...'.!?;:,—–()\n'].map((c) => head.lastIndexOf(c)));
  return head.slice(cut + 1);
}

function isNegatedClaim(text: string, index: number): boolean {
  const head = clauseBefore(text, index);
  return NEGATED_CLAUSE.test(head) || NEGATED_DIRECTLY.test(head);
}

function lintCausal(entry: LintEntry): LintFinding[] {
  const findings: LintFinding[] = [];
  const { text } = entry;
  const hasWeak = WEAK_LABEL.test(text);
  for (const pattern of CAUSAL_PATTERNS) {
    for (const m of text.matchAll(pattern)) {
      if (isNegatedClaim(text, m.index ?? 0)) continue;
      findings.push({
        id: entry.id,
        rule: 'causal',
        message: hasWeak
          ? `"${m[0]}" sits next to a weak evidence label (Symptom or Correlation). Describe what was measured, not a cause.`
          : `"${m[0]}" names a cause. Copy must describe what was measured, never a cause.`
      });
    }
  }
  for (const m of text.matchAll(DIAGNOSIS_WORDS)) {
    findings.push({ id: entry.id, rule: 'causal', message: `"${m[0]}" is a diagnosis. Copy describes what was measured.` });
  }
  for (const m of text.matchAll(/\b(?:proven|proves|proved)\b/gi)) {
    findings.push({ id: entry.id, rule: 'causal', message: `"${m[0]}" overstates the evidence. "Proven" never appears.` });
  }
  for (const m of text.matchAll(/\b(?:prove|proof)\b/gi)) {
    if (NEGATED_PROOF_BEFORE.test(clauseBefore(text, m.index ?? 0))) continue;
    findings.push({ id: entry.id, rule: 'causal', message: `"${m[0]}" overstates the evidence. Only "does not prove" or "not proof" is allowed.` });
  }
  return findings;
}

// ---- Permitted wordings (built from the real templates in wordings.ts) ----

const TEXT = '@@T@@';
const NUM = '@@N@@';

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

/** Turns a template output (built with sentinel arguments) into a regex source that accepts any argument. */
function toPattern(sample: string): string {
  return escapeRegExp(sample).split(TEXT).join('([^\\n]+?)').split(NUM).join('\\d+(?:\\.\\d+)?');
}

/** An article before an argument is "a" with the sentinel, and "an" for a vowel sound: the pattern accepts both. */
function withArticles(source: string): string {
  return source.replace(/^A (?=\()/, 'An? ').replace(/ a (?=\()/g, ' an? ');
}

const asNumber = (s: string) => s as unknown as number;

/** Every sentence the app may show for absence, linkage and plain measurement (as regex sources). */
const WORDING_SOURCES: string[] = [
  notObservedThreshold(TEXT, TEXT),
  notObservedGap(TEXT),
  overlapAmbiguous(asNumber(NUM)),
  auditCreate(asNumber(NUM)),
  plainAuditCreate(asNumber(NUM)),
  metricRose(TEXT, TEXT, TEXT, TEXT, TEXT, TEXT),
  episodeShare(TEXT, asNumber(NUM), asNumber(NUM), asNumber(NUM), asNumber(NUM)),
  topInWindow(TEXT, TEXT),
  lowestInWindow(TEXT, TEXT),
  countInWindow(TEXT, asNumber(NUM)),
  recycleGapEnabledTriggers(),
  recycleGapLegacyDefaults()
].map(toPattern).concat([
  overlapUnique(TEXT, TEXT, asNumber(NUM)),
  overlapBuffered(TEXT, TEXT, asNumber(NUM)),
  markerBefore(TEXT, TEXT, TEXT),
  markerAfter(TEXT, TEXT, TEXT),
  metricSampleOverlap(TEXT, 1, TEXT, TEXT),
  metricSampleOverlap(TEXT, asNumber(NUM), TEXT, TEXT)
].map((sample) => withArticles(toPattern(sample))));

/** Sticky patterns: each matches one whole permitted sentence (or sentences) at an exact position. */
const WORDING_PATTERNS: RegExp[] = WORDING_SOURCES.map((source) => new RegExp(source + '(?=\\s|$)', 'y'));

/** Phrases that must only ever appear as the exact permitted wordings. */
const WORDING_TRIGGERS = [
  'Not observed', 'Possible overlap', 'Audit create took', 'overlapping this interval', 'during this interval (Correlation)',
  'This interval starts', 'This interval ends', 'comparison windows outside stalls', 'in this window:',
  'Application pool recycle events are logged'
];

/** Length of the permitted wording that starts exactly at `pos`, or 0. */
function wordingAt(text: string, pos: number): number {
  for (const pattern of WORDING_PATTERNS) {
    pattern.lastIndex = pos;
    const m = pattern.exec(text);
    if (m) return m[0].length;
  }
  return 0;
}

/**
 * Removes every exact permitted wording that stands as its own sentence (starting at the start of the text
 * or of a sentence or line) and returns what is left, so it can be linted as ordinary prose.
 */
function withoutWordings(text: string): string {
  const kept: string[] = [];
  let keepFrom = 0;
  let pos = 0;
  while (pos < text.length) {
    const length = wordingAt(text, pos);
    if (length > 0) {
      kept.push(text.slice(keepFrom, pos));
      pos += length;
      while (pos < text.length && /\s/.test(text[pos])) pos++;
      keepFrom = pos;
      continue;
    }
    const boundary = /[.!?]["')\]]*\s+|\n\s*/g;
    boundary.lastIndex = pos;
    const m = boundary.exec(text);
    if (!m) break;
    pos = m.index + m[0].length;
  }
  kept.push(text.slice(keepFrom));
  return kept.join(' ').replace(/\s+/g, ' ').trim();
}

interface LintOptions {
  /** The text is a bare term label such as "Not observed", which may be one of the wording phrases on its own. */
  bareLabel?: boolean;
}

function lintWording(entry: LintEntry, options: LintOptions = {}): LintFinding[] {
  const text = entry.text.trim();
  if (options.bareLabel && WORDING_TRIGGERS.some((t) => t.toLowerCase() === text.toLowerCase())) return [];
  const rest = withoutWordings(entry.text);
  const trigger = WORDING_TRIGGERS.find((t) => rest.includes(t));
  if (!trigger) return [];
  return [{
    id: entry.id,
    rule: 'wording',
    message: `"${trigger}" must use the exact permitted wording from wordings.ts (roadmap 6.6). Paraphrasing is not allowed.`
  }];
}

// ---- Reading level ----

function countSyllables(word: string): number {
  return word.split('-').reduce((sum, part) => sum + syllablesInPart(part), 0);
}

function syllablesInPart(part: string): number {
  const w = part.toLowerCase().replace(/[^a-z]/g, '');
  if (!w) return 1; // digits
  if (w.length <= 3) return 1;
  const stripped = w.replace(/(?:[^laeiouy]es|[^laeiouy]ed|[^laeiouy]e)$/, '').replace(/^y/, '');
  const groups = stripped.match(/[aeiouy]{1,2}/g);
  return Math.max(1, groups ? groups.length : 1);
}

function wordsOf(text: string): string[] {
  return text.match(/[A-Za-z0-9][A-Za-z0-9'’-]*/g) ?? [];
}

/** Flesch-Kincaid grade level: 0.39 * words/sentences + 11.8 * syllables/words - 15.59. */
export function fleschKincaidGrade(text: string): number {
  const words = wordsOf(text);
  if (words.length === 0) return 0;
  const sentences = Math.max(1, text.split(/[.!?]+(?=\s|$)/).filter((s) => /[A-Za-z0-9]/.test(s)).length);
  const syllables = words.reduce((sum, w) => sum + countSyllables(w), 0);
  return 0.39 * (words.length / sentences) + 11.8 * (syllables / words.length) - 15.59;
}

function lintReadingLevel(entry: LintEntry): LintFinding[] {
  // Exact permitted sentences are reviewed once, in wordings.ts, so only the rest is scored.
  const rest = withoutWordings(entry.text);
  if (wordsOf(rest).length < MIN_WORDS_FOR_GRADE) return [];
  const grade = fleschKincaidGrade(rest);
  if (grade <= MAX_GRADE) return [];
  return [{
    id: entry.id,
    rule: 'reading-level',
    message: `Reading level is grade ${grade.toFixed(1)}; the limit is ${MAX_GRADE}. Use shorter sentences and everyday words.`
  }];
}

// ---- Undefined terms ----

function normalise(s: string): string {
  return s.trim().toLowerCase();
}

function hasWord(haystack: string, word: string): boolean {
  return new RegExp(`(?:^|[^a-z0-9])${escapeRegExp(word)}(?:$|[^a-z0-9])`).test(haystack);
}

function isDefined(jargon: string, terms: Term[]): boolean {
  const j = normalise(jargon);
  return terms.some((t) => [t.term, ...(t.aliases ?? [])].some((name) => hasWord(normalise(name), j)));
}

function lintUndefinedTerms(entry: LintEntry, terms: Term[]): LintFinding[] {
  const findings: LintFinding[] = [];
  for (const jargon of JARGON) {
    const found = new RegExp(`\\b${escapeRegExp(jargon)}(?:s|es|ed|ing)?\\b`, 'i').test(entry.text);
    if (found && !isDefined(jargon, terms)) {
      findings.push({
        id: entry.id,
        rule: 'undefined-term',
        message: `"${jargon}" has no glossary term or alias. Add it to the copy deck or use an everyday word.`
      });
    }
  }
  return findings;
}

function lintEntry(entry: LintEntry, terms: Term[], options: LintOptions = {}): LintFinding[] {
  return [
    ...lintCausal(entry),
    ...lintWording(entry, options),
    ...lintReadingLevel(entry),
    ...lintUndefinedTerms(entry, terms)
  ];
}

/** Lints all on-screen text. An empty array means every entry passes. */
export function lintCopy(entries: LintEntry[], terms: Term[]): LintFinding[] {
  return entries.flatMap((entry) => lintEntry(entry, terms));
}

/**
 * The entry point for fixed UI strings: component text, labels, buttons, tour steps. Same checks as `lintCopy`;
 * use this name wherever strings that are not glossary entries are checked.
 */
export function lintScreenStrings(entries: LintEntry[], terms: Term[]): LintFinding[] {
  return lintCopy(entries, terms);
}

/**
 * Lints every term's hint, definition, notMeaning and name. The name is linted too, except that a bare label
 * that is itself a wording phrase ("Not observed") is exempt from the wording rule only: it is a label, not a sentence.
 */
export function lintTerms(terms: Term[]): LintFinding[] {
  return terms.flatMap((t) => [
    ...lintEntry({ id: `${t.id}.name`, text: t.term }, terms, { bareLabel: true }),
    ...lintEntry({ id: `${t.id}.hint`, text: t.hint }, terms),
    ...lintEntry({ id: `${t.id}.definition`, text: t.definition }, terms),
    ...lintEntry({ id: `${t.id}.notMeaning`, text: t.notMeaning }, terms)
  ]);
}
