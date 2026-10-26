// The calls picked for the call trace (at most three). It lives here, not in a component, so a panel that redraws or resets keeps it.
// It is emptied only by the person (Clear, Remove) or when the open case changes.
import { addCall, MAX_CALLS, removeCall, trayKey, type CallRef, type TrayCall } from './callTrace';
import { CALL_TRACE_TEXT } from './copy/callTraceCopy';

export const tray = $state({
  calls: [] as TrayCall[],
  /** The case the chosen calls belong to. Empty is the demo or a session with no case. */
  caseId: '',
  /** Why the last add was refused, in plain words. Empty when nothing was refused. */
  notice: ''
});

const REASON = {
  full: CALL_TRACE_TEXT.trayFull,
  duplicate: CALL_TRACE_TEXT.trayDuplicate,
  missing: CALL_TRACE_TEXT.trayNoId
} as const;

/** Adds a call. Returns true when it was added; otherwise tray.notice says why (full, already chosen, or no id). */
export function addToTray(call: TrayCall): boolean {
  const out = addCall(tray.calls, call, MAX_CALLS);
  if (out.outcome === 'added') {
    tray.calls = out.list;
    tray.notice = '';
    return true;
  }
  tray.notice = REASON[out.outcome];
  return false;
}

export function removeFromTray(call: CallRef): void {
  tray.calls = removeCall(tray.calls, call);
  tray.notice = '';
}

/** Forgets the last refusal. Called when a trace is asked for, so an old reason does not sit beside a new answer. */
export function clearNotice(): void {
  tray.notice = '';
}

export function clearTray(): void {
  tray.calls = [];
  tray.notice = '';
}

export const inTray = (call: CallRef): boolean => tray.calls.some((item) => trayKey(item) === trayKey(call));
export const trayIsFull = (): boolean => tray.calls.length >= MAX_CALLS;

/** The one line shown beside the tray: the last refusal, or, when three calls are chosen, why no more can be added. */
export const trayMessage = (): string => tray.notice || (trayIsFull() ? CALL_TRACE_TEXT.trayFull : '');

/** Calls belong to the case they were chosen in. Pointing at another case empties the tray; the same case changes nothing. */
export function syncTrayCase(caseId: string): void {
  if (tray.caseId === caseId) return;
  tray.caseId = caseId;
  clearTray();
}
