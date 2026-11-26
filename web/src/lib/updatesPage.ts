import type { UpdateCheckReply } from './types';
import { UPD_TEXT, type UpdatePhase } from './copy/updatesCopy';
import { downloadPercent, failedReason } from './updateCheck';
import { shortVersion } from './updateNotes';

// What the Updates page says and offers for each answer of the update server. Pure: the component only draws it.

/** The one state the page is in. `checking` is the page's own: a check was started here and has not answered yet. */
export function updatePhase(reply: UpdateCheckReply | null, checking: boolean): UpdatePhase {
  if (checking) return 'checking';
  if (!reply) return 'none';
  const step = reply.install;
  if (reply.status === 'available') {
    if (step?.state === 'downloading' || step?.state === 'launched') return 'installing';
    if (step?.state === 'failed') return 'failed';
    if (step?.state === 'ready') return reply.applyAuto ? 'autowait' : 'ready';
    return 'available';
  }
  // A verified update that is ready or running stays in view even if the next check answered something else.
  if (step?.state === 'launched') return 'installing';
  switch (reply.status) {
    case 'latest': return 'uptodate';
    case 'dev': return 'dev';
    case 'unverified': return 'unverified';
    case 'none': return 'none';
    default: return 'error';
  }
}

/** Which of the four install steps is under way, 1 to 4: 0 when none is. */
export function installStep(reply: UpdateCheckReply | null, chaining: boolean): number {
  const state = reply?.install?.state;
  if (state === 'downloading') return 1;
  if (state === 'launched') return 3;
  if (state === 'ready' && chaining) return 2;
  return 0;
}

export interface StatusWords { title: string; sub: string; reason?: string }

export function statusWords(phase: UpdatePhase, reply: UpdateCheckReply | null): StatusWords {
  const t = UPD_TEXT;
  const current = shortVersion(reply?.current ?? '');
  const latest = shortVersion(reply?.install?.version || reply?.latest || '');
  switch (phase) {
    case 'available': return { title: t.available(latest), sub: t.availableSub(current) };
    case 'ready': return { title: t.ready(latest), sub: t.readySub };
    case 'autowait': return { title: t.autowait(latest), sub: t.autowaitSub };
    case 'installing': return { title: t.installingTitle(latest), sub: reply?.applyAuto && !reply.inUse ? t.installingAutoSub : t.installingSub };
    case 'failed': return { title: t.failedTitle(latest), sub: t.failedSub(current), reason: failedReason(reply?.install?.error) };
    case 'uptodate': return { title: t.statusTitle.uptodate, sub: reply?.held ? t.heldSub(current, reply.held) : t.uptodateSub(current) };
    default: return { title: t.statusTitle[phase], sub: t.statusSub[phase] };
  }
}

/** The text in the top bar chip: the short version, or what needs attention. */
export function chipState(reply: UpdateCheckReply | null): { text: string; pip: 'none' | 'ready' | 'busy' } {
  if (!reply) return { text: '', pip: 'none' };
  const phase = updatePhase(reply, false);
  if (phase === 'installing') return { text: UPD_TEXT.chipBusy, pip: 'busy' };
  if (phase === 'available' || phase === 'ready' || phase === 'autowait') return { text: UPD_TEXT.chipReady, pip: 'ready' };
  return { text: reply.current && reply.current !== 'dev' ? shortVersion(reply.current) : '', pip: 'none' };
}

/** Whole percent of the download for the progress bar. */
export function installPercent(reply: UpdateCheckReply | null): number {
  const step = reply?.install;
  if (!step) return 0;
  if (step.state === 'downloading') return downloadPercent(step.done, step.total);
  return step.state === 'ready' || step.state === 'launched' ? 100 : 0;
}

/** Version and time of the next check, in the words of the help line: "Next check in about 25 minutes." */
export function nextCheckNote(checkedAt: string | undefined, intervalMinutes: number | undefined, now: Date = new Date()): string {
  if (!checkedAt || !intervalMinutes) return '';
  const last = new Date(checkedAt).getTime();
  if (Number.isNaN(last)) return '';
  const left = Math.max(0, Math.round((last + intervalMinutes * 60000 - now.getTime()) / 60000));
  if (left < 1) return 'Next check is due now.';
  if (left < 60) return 'Next check in about ' + left + (left === 1 ? ' minute.' : ' minutes.');
  const hours = Math.round(left / 60);
  return 'Next check in about ' + hours + (hours === 1 ? ' hour.' : ' hours.');
}

/** "26 Nov 2026" for a release date such as "2026-11-26". Anything else is shown as written. */
export function formatNoteDate(value: string): string {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  if (!match) return value;
  const date = new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]));
  return Number.isNaN(date.getTime()) ? value : date.toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' });
}

/** The time of the last check in the person's own clock: "Today, 14:02", or "25 Nov, 09:10". */
export function checkedLabel(checkedAt: string | undefined, now: Date = new Date()): string {
  if (!checkedAt) return UPD_TEXT.neverChecked;
  const when = new Date(checkedAt);
  if (Number.isNaN(when.getTime())) return UPD_TEXT.neverChecked;
  const time = when.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' });
  if (when.toDateString() === now.toDateString()) return 'Today, ' + time;
  return when.toLocaleDateString('en-GB', { day: 'numeric', month: 'short' }) + ', ' + time;
}
