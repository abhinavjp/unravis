import type { SqlAccessReport } from './types';
import { ACCESS_TEXT } from './copy/sqlAccessCopy';

const own = <T>(rec: Record<string, T>, key: string): T | undefined => (Object.prototype.hasOwnProperty.call(rec, key) ? rec[key] : undefined);

/** The one sentence for a verdict. An unknown verdict reads as "could not finish", never as ready. */
export const verdictLine = (r: SqlAccessReport): string => own(ACCESS_TEXT.verdict, r.verdict) ?? ACCESS_TEXT.verdict.unreadable;

/** The word for a check state. An unknown state reads as "Not checked", never as passed. */
export const stateWord = (state: string): string => own(ACCESS_TEXT.state, state) ?? ACCESS_TEXT.state.skipped;

/** True only when the server said ready and no check failed: a report that says ready but carries a failed line is not shown as ready. */
export const isReady = (r: SqlAccessReport): boolean => r.ready && r.verdict === 'ready' && !r.checks.some((c) => c.state === 'failed');

/** The script to show: only when the server printed one for a verdict that needs it. */
export const scriptToShow = (r: SqlAccessReport): string => ((r.verdict === 'needs-grant' || r.verdict === 'too-many-rights') && r.script ? r.script : '');
