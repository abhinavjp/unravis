import { stateLabel } from './ledger';
import type { EvidenceCount, EvidenceExport, EvidenceRecord, ExportPreview, ExportPreviewInput, Query } from './types';

/**
 * Pure text and shape logic for the Export evidence drawer (P13). Every fixed sentence lives here so the copy lint can read it.
 * Nothing here adds data: the drawer shows what the server returned and nothing else.
 */

export const EXPORT_TEXT = {
  title: 'Export evidence',
  intro: 'Build a file of the findings in this window. You see everything in it before anything is saved.',
  options: 'Options',
  pseudonymsLabel: 'Include user pseudonyms',
  pseudonymsNote: 'Adds stable labels such as user-3fa91c2e in place of user ids. The key that makes them is never saved.',
  ledgerLabel: 'Include my ledger',
  ledgerNote: 'Adds your hypothesis text, state and evidence references. Nothing else from the ledger is added.',
  preview: 'Preview export',
  previewing: 'Building the preview…',
  previewAgain: 'Preview again',
  cancel: 'Cancel',
  previewNote: 'This only builds a preview. Nothing is saved until you press Save file.',
  masking: 'What is hidden',
  findings: 'Findings',
  noFindings: 'No findings in this window, so there is nothing to export.',
  lowN: 'Low n',
  knownGaps: 'Known gaps',
  noGaps: 'None recorded',
  sourceQuality: 'Source quality',
  pseudonyms: 'User pseudonyms',
  pseudonymsNone: 'No pseudonyms in this file.',
  ledger: 'Ledger entries',
  ledgerNone: 'No ledger entries in this file.',
  noEvidenceIds: 'No evidence references',
  manifest: 'Manifest',
  hashLabel: 'Body SHA-256',
  showFile: 'Show the exact file',
  showFileNote: 'The file content as the server built it, before it is saved.',
  save: 'Save file',
  saving: 'Saving…',
  saveNote: 'Saving downloads the file to your computer.',
  failedPreview: 'The preview did not finish. Try again.',
  failedSave: 'The file was not saved. Try again.',
  unavailable: 'Export is not available right now. Try again later.',
  goneSave: 'This preview is no longer available. Preview it again.',
  notFound: 'Export was not found on this server.',
  noSources: 'Choose at least one source first.'
} as const;

/** Labels for the facts shown on each record. */
export const RECORD_FIELDS = {
  evidenceId: 'Reference',
  source: 'Source',
  window: 'Window',
  join: 'Join',
  tolerance: 'Tolerance',
  provenance: 'Provenance'
} as const;

const QUALITY_LABELS: [keyof EvidenceRecord['sourceQuality'], string][] = [
  ['sourceVersion', 'Source version'],
  ['threshold', 'Threshold'],
  ['coverageFirst', 'First record covered'],
  ['coverageLast', 'Last record covered'],
  ['sampling', 'Row selection'],
  ['timezoneRule', 'Time zone rule']
];

/** What the server sent for a count, as written. Masked counts arrive as "<20" and stay that way. */
export function describeCount(count: Pick<EvidenceCount, 'text' | 'masked' | 'value'>): string {
  if (count.text) return count.text;
  if (count.masked) return '<20';
  return count.value == null ? '' : String(count.value);
}

/** The source quality facts that are present, with a plain label each. Empty values are left out, never guessed. */
export function qualityRows(quality: EvidenceRecord['sourceQuality'] | undefined): { label: string; value: string }[] {
  if (!quality) return [];
  return QUALITY_LABELS.flatMap(([key, label]) => (quality[key] ? [{ label, value: String(quality[key]) }] : []));
}

const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;

export interface ExportSummary {
  records: number;
  lowN: number;
  pseudonyms: number;
  ledger: number;
  hash: string;
  empty: boolean;
  statements: string[];
  line: string;
}

/** Counts of what is in the file, for the line above the preview. */
export function exportSummary(exp: EvidenceExport): ExportSummary {
  const records = exp.body.records?.length ?? 0;
  const lowN = (exp.body.records ?? []).filter((r) => r.lowN).length;
  const pseudonyms = exp.body.pseudonyms?.length ?? 0;
  const ledger = exp.body.ledger?.length ?? 0;
  const parts = [plural(records, 'finding', 'findings'), `${lowN} low n`];
  if (exp.body.pseudonyms) parts.push(plural(pseudonyms, 'pseudonym', 'pseudonyms'));
  if (exp.body.ledger) parts.push(plural(ledger, 'ledger entry', 'ledger entries'));
  return {
    records, lowN, pseudonyms, ledger,
    hash: exp.manifest.bodySha256,
    empty: records === 0,
    statements: exp.manifest.masking?.statements ?? [],
    line: parts.join(', ')
  };
}

/** The request body for the preview: the window on screen plus the two options. */
export function exportInput(query: Pick<Query, 'sourceIds' | 'start' | 'end'>, includePseudonyms: boolean, includeLedger: boolean): ExportPreviewInput {
  return { sourceIds: query.sourceIds, start: query.start, end: query.end, includePseudonyms, includeLedger };
}

const FALLBACK_NAME = 'evidence-export.json';

/** A safe file name from the returned one: no folders, no control characters. */
export function downloadName(file: { filename?: string }): string {
  const last = (file.filename ?? '').split(/[\\/]/).pop() ?? '';
  const clean = last.replace(/[\u0000-\u001f<>:"|?*]/g, '').trim();
  return clean && clean !== '.' && clean !== '..' ? clean : FALLBACK_NAME;
}

export type ExportPhase =
  | { kind: 'idle' }
  | { kind: 'previewing' }
  | { kind: 'preview'; preview: ExportPreview }
  | { kind: 'saving'; preview: ExportPreview }
  | { kind: 'saved'; preview: ExportPreview; filename: string; sha256: string };

/** Save is allowed only while a preview with at least one finding is on screen. */
export function canSave(phase: ExportPhase): boolean {
  return phase.kind === 'preview' && phase.preview.export.body.records.length > 0;
}

export const savedLine = (sha256: string) => `Saved. SHA-256: ${sha256}`;

/** The message to show for a failed call. A message the server wrote is shown as it is; a bare status gets a plain sentence. */
export function errorText(cause: unknown, stage: 'preview' | 'save'): string {
  const status = typeof cause === 'object' && cause !== null && 'status' in cause ? Number((cause as { status?: unknown }).status) : 0;
  const message = cause instanceof Error ? cause.message.trim() : '';
  if (message && !/^Request failed \(\d+\)$/.test(message)) return message;
  if (status === 503) return EXPORT_TEXT.unavailable;
  if (status === 404) return stage === 'save' ? EXPORT_TEXT.goneSave : EXPORT_TEXT.notFound;
  return stage === 'save' ? EXPORT_TEXT.failedSave : EXPORT_TEXT.failedPreview;
}

export { stateLabel };
