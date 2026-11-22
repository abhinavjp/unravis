import { labelProblem } from './zoneChoice';

/**
 * Which computer a file goes under when the person has not said (gap: "This file needs a computer"). Pure: no DOM, no fetch.
 *
 * An IIS web log made with the default settings names no computer, and HTTPERR, Query Store and the app logs never do. Asking for each file
 * is a dead end for a person who has forty of them, so the wizard picks the computer itself and says which one it picked:
 *  1. the computer the file names (the `s-computername` field of an IIS log),
 *  2. else the only computer already in the list,
 *  3. else the name of this PC, when the scan found it,
 *  4. else the plain name `this-pc`.
 * The person can change it on the review step.
 */

export const DEFAULT_HOST_LABEL = 'this-pc';

/** How much of the start of a file is read to find the computer name. An IIS header and its first lines are far smaller than this. */
export const SNIFF_BYTES = 64 * 1024;

/**
 * The computer an IIS W3C log names, read from the start of the file, or ''. The `#Fields:` line says which column is `s-computername`;
 * the first row under it holds the name. A `-` means the log keeps no name there.
 */
export function sniffW3cComputer(head: string): string {
  let columns: string[] | null = null;
  for (const raw of head.split(/\r?\n/)) {
    const line = raw.trim();
    if (line === '') continue;
    if (line.startsWith('#')) {
      if (/^#Fields:/i.test(line)) columns = line.replace(/^#Fields:\s*/i, '').split(/\s+/);
      continue;
    }
    if (columns === null) return '';
    const at = columns.findIndex((name) => name.toLowerCase() === 's-computername');
    if (at < 0) return '';
    const value = line.split(/\s+/)[at] ?? '';
    return value === '-' ? '' : value;
  }
  return '';
}

/** A computer name as the host list accepts it, or '' when it cannot be one. Case is kept. */
export function cleanHostLabel(name: string): string {
  const text = String(name ?? '').trim();
  return labelProblem(text, null) === '' ? text : '';
}

/** The entry of the list that has this name, ignoring case, or ''. */
export function listedLabel(name: string, entries: readonly { label: string }[] | null): string {
  const wanted = name.toLowerCase();
  return entries?.find((entry) => entry.label.toLowerCase() === wanted)?.label ?? '';
}

export interface HostChoice {
  /** The name the file itself gives, or ''. */
  sniffed: string;
  /** The computers already in the list, or null when the list could not be read. */
  entries: readonly { label: string }[] | null;
  /** The name of this PC from the scan, or ''. */
  thisPc: string;
}

/** The label a file goes under, per the order above. Always a valid label. */
export function chooseHostLabel(input: HostChoice): string {
  const named = cleanHostLabel(input.sniffed);
  if (named !== '') return listedLabel(named, input.entries) || named;
  if (input.entries && input.entries.length === 1) return input.entries[0].label;
  const pc = cleanHostLabel(input.thisPc);
  if (pc !== '') return listedLabel(pc, input.entries) || pc;
  return listedLabel(DEFAULT_HOST_LABEL, input.entries) || DEFAULT_HOST_LABEL;
}
