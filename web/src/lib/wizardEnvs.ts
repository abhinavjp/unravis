/**
 * The environment names of the create wizard (R4). Free text, comma or line separated, blank allowed. No built-in Production or Staging.
 * Pure: no DOM, no fetch. The bounds are the server's (internal/casestore: 32 names, 60 characters each, no control or format characters),
 * so a list that comes out of `parseEnvs` is one the server accepts.
 */

export const MAX_ENVS = 32;
export const MAX_ENV_CHARS = 60;
/** The most characters the environments field takes. Far above 32 names of 60 characters, so a normal list is never cut. */
export const ENV_FIELD_MAX = 4000;

/** What was left out or changed, by name, so the screen can say so. Each list holds the names as they were kept or typed. */
export interface EnvDropped {
  /** Left out: the same name (in any letter case) was already listed. */
  duplicates: string[];
  /** Kept, but cut to 60 characters. The cut name is listed. */
  shortened: string[];
  /** Left out: the case holds at most 32 environments. */
  overflow: string[];
  /** Kept, but the sign › was replaced with a dash, because › separates environment, portal and API on screen. */
  changed: string[];
}

export interface ParsedEnvs { names: string[]; dropped: EnvDropped }

const SEPARATORS = /[,\r\n]+/;
// Characters the server refuses in a tag: control, invisible formatting (bidi marks, zero-width joiners) and stray surrogates.
const REFUSED = /[\p{Cc}\p{Cf}\p{Cs}]/gu;
const LINE_BREAKS = /[\p{Zl}\p{Zp}]/gu;
const CHEVRON = '›';

const emptyDropped = (): EnvDropped => ({ duplicates: [], shortened: [], overflow: [], changed: [] });

/** One name as the case will hold it: refused characters removed, inner white space collapsed, trimmed. May be empty. */
function cleanName(piece: string): string {
  return piece.replace(LINE_BREAKS, ' ').replace(REFUSED, (ch) => (/\s/.test(ch) ? ' ' : '')).replace(/\s+/g, ' ').trim();
}

/**
 * Reads the environments field. Splits on commas and line breaks; trims each name and collapses inner spaces; drops empty names and names
 * already listed (letter case ignored, the first spelling wins); cuts a name to 60 characters; keeps the first 32 names.
 * Everything left out or changed is reported in `dropped`.
 */
export function parseEnvs(text: string): ParsedEnvs {
  const names: string[] = [];
  const dropped = emptyDropped();
  const seen = new Set<string>();
  for (const piece of String(text ?? '').split(SEPARATORS)) {
    let name = cleanName(piece);
    if (!name) continue;
    let replaced = false;
    if (name.includes(CHEVRON)) { name = name.replaceAll(CHEVRON, '-').replace(/\s+/g, ' ').trim(); replaced = true; }
    if (!name) continue;
    let cut = false;
    const letters = [...name];
    if (letters.length > MAX_ENV_CHARS) { name = letters.slice(0, MAX_ENV_CHARS).join('').trimEnd(); cut = true; }
    const key = name.toLowerCase();
    if (seen.has(key)) { dropped.duplicates.push(name); continue; }
    seen.add(key);
    if (names.length >= MAX_ENVS) { dropped.overflow.push(name); continue; }
    names.push(name);
    if (replaced) dropped.changed.push(name);
    if (cut) dropped.shortened.push(name);
  }
  return { names, dropped };
}

/** True when something was left out or changed. */
export function hasDropped(dropped: EnvDropped): boolean {
  return dropped.duplicates.length + dropped.shortened.length + dropped.overflow.length + dropped.changed.length > 0;
}

/** The names as one line, for a summary. Empty when there are none. */
export const envsLine = (names: readonly string[]): string => names.join(', ');
