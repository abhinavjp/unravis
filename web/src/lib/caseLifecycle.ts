import { believedState, type CaseInfo, type CaseState, type RetryItem } from './cases';

/**
 * The page rules for a case's life (decisions A7, A8, A14), kept apart from the screens so a test can read them.
 * Nothing here calls the server and nothing holds words.
 */

/** hidden: nothing to show (the case is open). enabled: press to reopen. busy: a reopen is running. disabled: shown, but the case cannot come back. */
export type ReopenButton = 'hidden' | 'enabled' | 'busy' | 'disabled';

/**
 * Reopen is for a Closed case only. An Open case has nothing to reopen, so the button is hidden. An archived (or archiving) case has no Reopen
 * either (spec section 9: n/a): its way back is Restore, so the page offers that and never a button that cannot work. An untrusted or erased case
 * has no button at all.
 */
export function reopenButton(c: Pick<CaseInfo, 'state' | 'closed' | 'untrusted'>, busy: boolean): ReopenButton {
  if (c.untrusted) return 'hidden';
  switch (believedState(c)) {
    case 'closed': return busy ? 'busy' : 'enabled';
    default: return 'hidden';
  }
}

/**
 * What one item of the "needs another try" list offers (A14).
 * retry: press to do it again on the server. add-file: a file has to be picked again, so the button opens the file drawer.
 * in-wizard: the server cannot do it alone (an audit source needs the saved connection), so the line says where to do it and offers only Remove.
 * reopen-first: the case is closed, so the line says to reopen it. hidden: the case is archived, untrusted or erased, so no button.
 */
export type RetryAction = 'retry' | 'add-file' | 'in-wizard' | 'reopen-first' | 'hidden';

export function retryAction(item: Pick<RetryItem, 'kind'>, c: Pick<CaseInfo, 'state' | 'closed' | 'untrusted'>): RetryAction {
  if (c.untrusted) return 'hidden';
  const state: CaseState = believedState(c);
  if (state === 'closed') return 'reopen-first';
  if (state !== 'open') return 'hidden';
  if (item.kind === 'import') return 'add-file';
  return item.kind === 'audit-source' ? 'in-wizard' : 'retry';
}

/**
 * Archive is offered on an Open or a Closed case, without closing it first (A2). An archiving case is already being archived, an archived case is archived,
 * and an untrusted or missing one cannot be read, so the button is hidden for those. disabled: shown but off.
 */
export type ArchiveButton = 'hidden' | 'enabled' | 'disabled';

export function archiveButton(c: Pick<CaseInfo, 'state' | 'closed' | 'untrusted' | 'missing'>): ArchiveButton {
  if (c.untrusted || c.missing) return 'hidden';
  const state = believedState(c);
  return state === 'open' || state === 'closed' ? 'enabled' : 'hidden';
}

/**
 * Whether the case home may read the case's own data (files, health checks, theories) and offer the analysis. An archiving or archived case
 * answers 409 case-archived to every one of those reads, and the spec (section 9) does not offer them, so the page neither asks nor shows them:
 * it shows the archive record and the way back (Restore).
 */
export function readsCaseData(c: Pick<CaseInfo, 'state' | 'closed'>): boolean {
  const state = believedState(c);
  return state !== 'archiving' && state !== 'archived';
}
