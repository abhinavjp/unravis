// Pure rules of the Add sources guards: when typed input counts as unsaved, when an unfinished add list is kept across a hub tab switch, and
// which chosen sources the case already holds. No state and no component imports, so the rules can be tested alone.
//
// Nothing here ever reads or stores what a field holds beyond "is it empty": a password typed into a form is looked at only to see whether the
// field is blank, and the answer (a yes or no) is all that leaves this file.

/** What the guard needs to know about one form field. */
export interface FieldSnap { type: string; /** Whether the field holds anything but spaces. The text itself is never copied. */ filled: boolean; files: number; touched: boolean }

/** Field types whose typed text counts as entered input. */
const TEXT_TYPES: readonly string[] = ['', 'text', 'password', 'search', 'url', 'email', 'tel', 'number', 'textarea'];

/**
 * True when one field holds input the person gave and has not saved. Text counts while it is not blank; a file field while a file is picked;
 * a tick box, a radio or a list only after the person changed it (a tick the form made by itself is not their input).
 */
export function fieldHoldsInput(field: FieldSnap): boolean {
  if (field.type === 'hidden' || field.type === 'button' || field.type === 'submit') return false;
  if (field.type === 'file') return field.files > 0;
  if (TEXT_TYPES.includes(field.type)) return field.touched && field.filled;
  return field.touched;
}

/** True when any field holds unsaved input. */
export const holdsUnsavedInput = (fields: readonly FieldSnap[]): boolean => fields.some(fieldHoldsInput);

/** Reads the fields below `root` as snapshots. `touched` holds the fields the person has typed in or changed since the form opened. */
export function snapshotFields(root: ParentNode, touched: WeakSet<Element>): FieldSnap[] {
  const out: FieldSnap[] = [];
  for (const el of Array.from(root.querySelectorAll('input, textarea, select'))) {
    const field = el as HTMLInputElement;
    out.push({ type: el.tagName === 'SELECT' ? 'select' : el.tagName === 'TEXTAREA' ? 'textarea' : field.type, filled: field.value.trim() !== '', files: field.files?.length ?? 0, touched: touched.has(el) });
  }
  return out;
}

export interface LeaveFacts {
  /** Sources chosen and not yet sent. */
  sources: number;
  /** The run has started (the case is being, or has been, written). */
  started: boolean;
  /** Sources of that run that did not go through. */
  failures: number;
  /** A run is in flight right now. */
  running: boolean;
}

/** Whether leaving the Add data tab keeps what is on it: an unfinished list, or a finished run that left failures on screen. A clean end keeps nothing. */
export function keepOnLeave(f: LeaveFacts): boolean {
  if (f.running) return false;
  return f.started ? f.failures > 0 : f.sources > 0;
}

/** The kinds whose tag is the whole source (no file is read), so a second one for the same name is a plain duplicate. */
export const DUPLICATE_KINDS: readonly string[] = ['sql-server', 'redis', 'blackbox'];

/** The usual port of a kind, left out when the name is compared (the form fills it in, another window may not). */
const DEFAULT_PORT: Readonly<Record<string, string>> = { redis: '6379', 'sql-server': '1433' };

/** A name as it is compared: lower case, no trailing dot on the host, and no default port. A name that is not host or host:port is only lower-cased. */
export function nameKey(kind: string, label: string): string {
  const text = label.trim().toLowerCase();
  // Only the kinds that are a host and a port get this; a Blackbox name or path is only lower-cased.
  if (!(kind in DEFAULT_PORT)) return text;
  const m = /^([^:,\\\[\]\s]+?)\.?(?::(\d+))?$/.exec(text);
  if (!m) return text;
  return m[2] && m[2] !== DEFAULT_PORT[kind] ? m[1] + ':' + m[2] : m[1];
}
const same = (kind: string, a: string, b: string): boolean => nameKey(kind, a) === nameKey(kind, b);

/** The chosen sources the case already holds (same kind and same name), so adding them again would only make a second copy. */
export function alreadyInCase<T extends { kind: string; label: string }>(chosen: readonly T[], existing: readonly { kind: string; label: string }[]): T[] {
  return chosen.filter((s) => DUPLICATE_KINDS.includes(s.kind) && s.label.trim() !== '' && existing.some((e) => e.kind === s.kind && same(s.kind, e.label, s.label)));
}

export type CaseGate = 'open' | 'closed' | 'gone';

/** What a fresh read of the case says about adding to it: open, not open (closed, archived, or one that cannot be opened), or gone. */
export function gateOfView(view: { state?: string; closed?: unknown; untrusted?: unknown } | null): CaseGate {
  if (view === null) return 'gone';
  if (view.untrusted) return 'closed';
  const state = view.state ? view.state : view.closed ? 'closed' : 'open';
  return state === 'open' ? 'open' : 'closed';
}
