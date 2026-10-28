import { caseCode, type CaseInfo } from './cases';
import type { LockState } from './masterKey';
import { ARCHIVE_TEXT } from './copy/archiveCopy';
import { START_TEXT, importsText, shownText, sourcesText } from './copy/startCopy';
import { formatTime } from './time';

/**
 * The start screen's model. Pure: no DOM, no fetch. StartScreen.svelte binds these helpers.
 *
 * Three cards are always present when the cases are open: use an existing case, enter an existing case, create a new one.
 * While the master key is locked there are no cards, no titles and no counts: the unlock screen takes the place of all of it.
 */

export type StartCardId = 'existing' | 'enter' | 'create';

export interface StartCard { id: StartCardId; title: string; text: string }

/** Rows shown before "Show more". A person with hundreds of cases still sees a short list. */
export const CASE_PAGE_SIZE = 50;
/** Longest title shown in a row, in characters. The full title stays in the tooltip. */
export const TITLE_MAX = 60;

/** A case as the list knows it. `sources` is shown only when the server sent it; the case list does not carry it today. */
export type StartCase = Pick<CaseInfo, 'id' | 'title' | 'created' | 'closed' | 'imports' | 'untrusted' | 'state' | 'percent' | 'missing'> & { sources?: number };

export interface CaseRow {
  id: string;
  /** The title cut to fit one line, ending in an ellipsis when it was cut. */
  title: string;
  /** The whole title, for a tooltip. */
  fullTitle: string;
  /** For example "24 Oct 2026 15:19 UTC". */
  created: string;
  /** For example "3 imports, 2 sources". */
  counts: string;
  /** The short case code, for example "Code 3F9A27C1". Two cases with the same title (a restore as new) differ by it. */
  codeText: string;
  closed: boolean;
  closedLabel: string;
  /** True when the server refuses this folder: show a plain note and no Open button. */
  untrusted: boolean;
  untrustedLabel: string;
  /** True while an archive is running on this case: show "Archiving n%" and no Open button (A18). */
  archiving: boolean;
  archivingLabel: string;
  /** True when the working copy is gone: no Open button, a plain note instead. */
  missing: boolean;
  missingLabel: string;
}

/** Cuts a title to `max` characters (as a person counts them) and adds an ellipsis. A blank title becomes "Untitled case". */
export function truncateTitle(title: string, max = TITLE_MAX): string {
  const clean = title.replace(/\s+/g, ' ').trim();
  if (!clean) return START_TEXT.untitled;
  const chars = [...clean];
  if (chars.length <= max) return clean;
  return chars.slice(0, Math.max(1, max - 1)).join('').trimEnd() + '…';
}

export function caseCounts(c: Pick<StartCase, 'imports' | 'sources'>): string {
  const imports = importsText(Number.isFinite(c.imports) && c.imports > 0 ? Math.floor(c.imports) : 0);
  return typeof c.sources === 'number' && Number.isFinite(c.sources) && c.sources >= 0 ? imports + ', ' + sourcesText(Math.floor(c.sources)) : imports;
}

export function caseRow(c: StartCase): CaseRow {
  return {
    id: c.id,
    title: truncateTitle(c.title),
    fullTitle: c.title.trim() || START_TEXT.untitled,
    created: formatTime(c.created, { year: true, precision: 'minute', fallback: '' }),
    counts: caseCounts(c),
    codeText: START_TEXT.codeLabel + ' ' + caseCode(c.id),
    closed: Boolean(c.closed),
    closedLabel: START_TEXT.closedLabel,
    untrusted: c.untrusted === true,
    untrustedLabel: START_TEXT.untrustedLabel,
    archiving: c.state === 'archiving',
    archivingLabel: ARCHIVE_TEXT.chipArchiving + (typeof c.percent === 'number' ? ' ' + Math.round(c.percent) + '%' : ''),
    missing: c.missing === true,
    missingLabel: ARCHIVE_TEXT.workingCopyMissing
  };
}

/** The three cards, always in this order. */
export const START_CARDS: readonly StartCard[] = [
  { id: 'existing', title: START_TEXT.existingTitle, text: START_TEXT.existingText },
  { id: 'enter', title: START_TEXT.enterTitle, text: START_TEXT.enterText },
  { id: 'create', title: START_TEXT.createTitle, text: START_TEXT.createText }
];

export interface StartView {
  /** True while the key is locked: show the unlock screen and nothing from the cases. */
  locked: boolean;
  cards: readonly StartCard[];
  rows: CaseRow[];
  /** Cases not shown yet. Zero when the whole list fits. */
  hidden: number;
  total: number;
  /** "50 of 500 cases shown." when the list is capped, else empty. */
  shown: string;
  /** The line for an empty list, else empty. */
  empty: string;
  /** Whether the "Protect your cases with a master key" link shows: only while no key is set. */
  offerSetup: boolean;
}

/** How many rows to show for a "Show more" count: the first page, then a page more each time. */
export const visibleCount = (pages: number): number => CASE_PAGE_SIZE * Math.max(1, Math.floor(Number.isFinite(pages) ? pages : 1));

/**
 * Everything the screen shows, from the key state and the case list. A locked key returns no rows, no titles and no counts, even
 * if a list is passed in. `lock` of null means the status has not been read yet: show the cards without the setup link.
 */
export function startView(lock: LockState | null, cases: readonly StartCase[], pages = 1): StartView {
  if (lock === 'locked' || lock === 'damaged') {
    return { locked: true, cards: [], rows: [], hidden: 0, total: 0, shown: '', empty: '', offerSetup: false };
  }
  const total = cases.length;
  const limit = visibleCount(pages);
  const rows = cases.slice(0, limit).map(caseRow);
  const hidden = Math.max(0, total - rows.length);
  return {
    locked: false,
    cards: START_CARDS,
    rows,
    hidden,
    total,
    shown: hidden > 0 ? shownText(rows.length, total) : '',
    empty: total === 0 ? START_TEXT.existingEmpty : '',
    offerSetup: lock === 'none'
  };
}
