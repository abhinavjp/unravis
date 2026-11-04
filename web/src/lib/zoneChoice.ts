import { hostNeed } from './fileKinds';

/**
 * Which computer a file came from, and which time zone that computer's clock uses (packet P4). Pure: no DOM, no fetch.
 *
 * The server keeps one list of computers (the host list). An import names one by its label, and the server refuses a label that is not
 * in the list. A kind whose file does not say which computer wrote it (HTTPERR, Query Store) needs a label. A kind that writes the clock of
 * its computer (the SQL Server error log, the Windows event log) needs a label whose entry also has a time zone.
 */

/** The part of a host entry this module reads. The full entry is `HostEntry` in cases.ts. */
export interface HostRef { label: string; tz: string }

/** The server's limit for a label, in bytes (internal/hosts). */
export const MAX_HOST_LABEL = 253;
/** The server's limit for a zone name. */
export const MAX_ZONE_NAME = 64;

const LABEL = /^[A-Za-z0-9][A-Za-z0-9._-]*$/;

export type LabelProblem = '' | 'empty' | 'long' | 'chars' | 'taken';

/** Why a new computer's name cannot be used, or ''. The same rules the server applies, so a bad name is caught before the call. */
export function labelProblem(label: string, entries: readonly Pick<HostRef, 'label'>[] | null): LabelProblem {
  const text = label.trim();
  if (text === '') return 'empty';
  if (text.length > MAX_HOST_LABEL) return 'long';
  if (!LABEL.test(text)) return 'chars';
  if (entries && entries.some((entry) => entry.label.toLowerCase() === text.toLowerCase())) return 'taken';
  return '';
}

export type ZoneProblem = '' | 'empty' | 'unknown';

/** Why a time zone name cannot be used, or ''. A zone is a name such as Asia/Dubai or UTC; the browser knows the list. */
export function zoneNameProblem(tz: string): ZoneProblem {
  const text = tz.trim();
  if (text === '') return 'empty';
  if (text.length > MAX_ZONE_NAME || /[\s\\]/.test(text) || text.toLowerCase() === 'local') return 'unknown';
  try {
    new Intl.DateTimeFormat('en', { timeZone: text });
    return '';
  } catch {
    return 'unknown';
  }
}

/** The zone names to suggest while typing: UTC first, then the browser's own list when it has one. */
export function zoneSuggestions(): string[] {
  const intl = Intl as unknown as { supportedValuesOf?: (key: string) => string[] };
  let names: string[] = [];
  try { names = intl.supportedValuesOf?.('timeZone') ?? []; } catch { names = []; }
  return ['UTC', ...names.filter((name) => name !== 'UTC')];
}

export type HostProblem = '' | 'pick' | 'gone' | 'no-zone';

/**
 * What is missing for a file of this kind. '' when nothing is, or when the kind needs no computer. `entries` is null before the list has been
 * read: a label already chosen is then trusted rather than called missing.
 */
export function hostProblem(kind: string, host: string, entries: readonly HostRef[] | null): HostProblem {
  const need = hostNeed(kind);
  if (need === 'none') return '';
  const label = host.trim();
  if (label === '') return need === 'optional' ? '' : 'pick';
  if (entries === null) return '';
  const entry = entries.find((item) => item.label === label);
  if (!entry) return 'gone';
  if (need === 'host-tz' && entry.tz.trim() === '') return 'no-zone';
  return '';
}

/** The time zone saved for a label, or ''. */
export const zoneOf = (label: string, entries: readonly HostRef[] | null): string => entries?.find((entry) => entry.label === label)?.tz.trim() ?? '';
