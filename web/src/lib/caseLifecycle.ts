import { believedState, type CaseInfo, type CaseState, type RetryItem } from './cases';

/**
 * The page rules for a case's life (decisions A7, A8, A14), kept apart from the screens so a test can read them.
 * Nothing here calls the server and nothing holds words.
 */

/** hidden: nothing to show (the case is open). enabled: press to reopen. busy: a reopen is running. disabled: shown, but the case cannot come back. */
export type ReopenButton = 'hidden' | 'enabled' | 'busy' | 'disabled';

/**
 * Reopen is for a Closed case only. An Open case has nothing to reopen, so the button is hidden. An archived (or archiving) case is shown
 * with the button off: its way back is Restore, not Reopen. An untrusted or erased case has no button at all.
 */
export function reopenButton(c: Pick<CaseInfo, 'state' | 'closed' | 'untrusted'>, busy: boolean): ReopenButton {
  if (c.untrusted) return 'hidden';
  switch (believedState(c)) {
    case 'closed': return busy ? 'busy' : 'enabled';
    case 'archiving':
    case 'archived': return 'disabled';
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
