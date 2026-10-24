/**
 * Copy lint (roadmap §6.6 "Copy lint" and §8.14 writing rules). Pure TypeScript, no DOM.
 * `lintCopy` returns the findings for a set of on-screen texts; an empty array means pass.
 */
import type { Term } from './types';
import { auditCreate, notObservedGap, notObservedThreshold, overlapAmbiguous, overlapUnique, plainAuditCreate } from './wordings';

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

/** Causal verbs and phrases. None may name a cause for a measurement. */
const CAUSAL_PATTERNS: RegExp[] = [
  /\bcaus(?:ed|es|ing)\b/gi,
  /\bbecause\b/gi,
  /\bwaiting on\b/gi,
  /\bblocked by\b/gi,
  /\bdue to\b/gi,
  /\bthe database was waiting\b/gi,
  /\bthe API waited for\b/gi
];

/** A negation ("does not prove", "cannot show") before the phrase in the same sentence makes it a disclaimer. */
const NEGATED_BEFORE = /\b(?:not|never|cannot|can't|n't)\s+(?:\w+\s+){0,2}?(?:prove|show|mean|say|imply|tell|explain|establish|confirm|demonstrate|state)\b[^.;!?]*$/i;
const NEGATED_PROOF_BEFORE = /\b(?:not|never|cannot|can't|n't|no)\s+(?:\w+\s+){0,1}$/i;

const WEAK_LABEL = /\bsymptom\b|(?<!strong\s)\bcorrelation\b/i;

/** Text before `index` within its own sentence. */
function sentenceBefore(text: string, index: number): string {
  const head = text.slice(0, index);
  const cut = Math.max(head.lastIndexOf('.'), head.lastIndexOf('!'), head.lastIndexOf('?'), head.lastIndexOf(';'));
  return head.slice(cut + 1);
}

function lintCausal(entry: LintEntry): LintFinding[] {
  const findings: LintFinding[] = [];
  const { text } = entry;
  const hasWeak = WEAK_LABEL.test(text);
  for (const pattern of CAUSAL_PATTERNS) {
    for (const m of text.matchAll(pattern)) {
      if (NEGATED_BEFORE.test(sentenceBefore(text, m.index ?? 0))) continue;
      findings.push({
        id: entry.id,
        rule: 'causal',
        message: hasWeak
          ? `"${m[0]}" sits next to a weak evidence label (Symptom or Correlation). Describe what was measured, not a cause.`
          : `"${m[0]}" names a cause. Copy must describe what was measured, never a cause.`
      });
    }
  }
  for (const m of text.matchAll(/\b(?:proven|proves|proved)\b/gi)) {
    findings.push({ id: entry.id, rule: 'causal', message: `"${m[0]}" overstates the evidence. "Proven" never appears.` });
  }
  for (const m of text.matchAll(/\b(?:prove|proof)\b/gi)) {
    if (NEGATED_PROOF_BEFORE.test(sentenceBefore(text, m.index ?? 0))) continue;
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

const asNumber = (s: string) => s as unknown as number;

/** Every sentence the app may show for absence, linkage and plain measurement. */
const WORDING_PATTERNS: RegExp[] = [
  notObservedThreshold(TEXT, TEXT),
  notObservedGap(TEXT),
  overlapUnique(TEXT, TEXT, asNumber(NUM)),
  overlapAmbiguous(asNumber(NUM)),
  auditCreate(asNumber(NUM)),
  plainAuditCreate(asNumber(NUM))
].map((sample) => new RegExp('^' + toPattern(sample)));

/** Phrases that must only ever appear as the exact permitted wordings. */
const WORDING_TRIGGERS = ['Not observed', 'Possible overlap', 'Audit create took', 'overlapping this interval'];

/** True when the whole text is one or more permitted wordings and nothing else. */
function isFixedWording(text: string): boolean {
  let rest = text.trim();
  if (!rest) return false;
  while (rest) {
    const hit = WORDING_PATTERNS.map((p) => p.exec(rest)).find((m) => m !== null);
    if (!hit) return false;
    rest = rest.slice(hit[0].length).trim();
  }
  return true;
}

function lintWording(entry: LintEntry): LintFinding[] {
  const trigger = WORDING_TRIGGERS.find((t) => entry.text.includes(t));
  if (!trigger || isFixedWording(entry.text)) return [];
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
  if (wordsOf(entry.text).length < MIN_WORDS_FOR_GRADE) return [];
  if (isFixedWording(entry.text)) return []; // fixed sentences are reviewed once, in wordings.ts
  const grade = fleschKincaidGrade(entry.text);
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

/** Lints all on-screen text. An empty array means every entry passes. */
export function lintCopy(entries: LintEntry[], terms: Term[]): LintFinding[] {
  return entries.flatMap((entry) => [
    ...lintCausal(entry),
    ...lintWording(entry),
    ...lintReadingLevel(entry),
    ...lintUndefinedTerms(entry, terms)
  ]);
}
