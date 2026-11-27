/**
 * The "What happened" side of the Blackbox charts view, with no DOM: crash dumps, restarts, Windows events and recording
 * health, all taken from the series answer (nothing new is collected). Every text that comes from the recorded PC (a dump's
 * file name, a note's detail, an unknown note kind) is untrusted: it goes through safeText, which keeps a file name only (no
 * folders), drops control and direction-changing characters, and cuts it short. The view prints it as plain text, never as HTML.
 */
import { own } from './blackboxSeries';
import type { SeriesAnswer, SeriesDump, SeriesGap, SeriesMarker, SeriesEventId, SeriesLine } from './blackboxSeries';

/** At most this many marks are drawn on one chart or timeline, and this many rows are listed, however much the answer carries. */
export const MAX_DRAWN = 60;
export const MAX_LISTED = 60;
export const MAX_EVENT_DOTS = 120;
const MAX_TEXT = 60;

/** Text from the recorded PC made safe to show: last path part only, no control or bidi characters, one line, cut short. */
export function safeText(raw: unknown, max = MAX_TEXT): string {
  if (typeof raw !== 'string') return '';
  // eslint-disable-next-line no-control-regex
  let s = raw.replace(/[\u0000-\u001f\u007f-\u009f​-‏‪-‮⁦-⁩﻿]/g, ' ');
  const cut = Math.max(s.lastIndexOf('/'), s.lastIndexOf('\\'));
  if (cut >= 0) s = s.slice(cut + 1);
  s = s.replace(/\s+/g, ' ').trim();
  return s.length > max ? s.slice(0, max - 1) + '…' : s;
}

export type Tone = 'bad' | 'warn' | 'info';
export type HappenedKind = 'dump' | 'restart' | 'crash' | 'late' | 'disk' | 'note';
export interface HappenedItem { id: string; kind: HappenedKind; t: number; end?: number; title: string; detail: string; tone: Tone }

const CAUSE_TITLES: Record<string, { title: string; kind: HappenedKind; tone: Tone }> = {
  late: { title: 'The recorder skipped samples', kind: 'late', tone: 'warn' },
  restart: { title: 'The recorder was stopped and started again', kind: 'restart', tone: 'info' },
  crash: { title: 'The recorder ended without a clean stop', kind: 'crash', tone: 'bad' },
  'disk-low': { title: 'Recording paused: the drive was nearly full', kind: 'disk', tone: 'warn' }
};

const NOTE_TITLES: Record<string, { title: string; tone: Tone }> = {
  restart: { title: 'The recorder started again', tone: 'info' },
  'clock-jump': { title: 'The PC clock jumped forward', tone: 'warn' },
  'clock-back': { title: 'The PC clock went back', tone: 'warn' },
  'manifest-rebuilt': { title: 'The recorder rebuilt its file list', tone: 'warn' },
  'file-missing': { title: 'A recording file was missing', tone: 'bad' },
  'file-repaired': { title: 'A damaged recording file was repaired', tone: 'info' },
  'paused-disk-low': { title: 'Recording paused: the drive was nearly full', tone: 'warn' },
  resumed: { title: 'Recording resumed', tone: 'info' },
  housekeeping: { title: 'Old recordings were removed', tone: 'info' },
  'host-slow': { title: 'Reading this PC was slow', tone: 'warn' },
  'host-still-slow': { title: 'Reading this PC is still slow', tone: 'warn' },
  'host-recovered': { title: 'Reading this PC is back to normal', tone: 'info' },
  'collect-failed': { title: 'A reading failed', tone: 'warn' },
  'write-failed': { title: 'A sample could not be saved', tone: 'bad' },
  'collect-still-failing': { title: 'Readings are still failing', tone: 'warn' },
  'collect-recovered': { title: 'Readings work again', tone: 'info' },
  'write-still-failing': { title: 'Samples still cannot be saved', tone: 'bad' },
  'write-recovered': { title: 'Samples are being saved again', tone: 'info' },
  'settings-applied': { title: 'New settings were applied', tone: 'info' },
  'diag-log-stopped': { title: "The recorder's diagnostics file stopped growing", tone: 'warn' },
  'diag-log-resumed': { title: "The recorder's diagnostics file is growing again", tone: 'info' },
  'recorder-crashed': { title: 'The recorder crashed and was started again', tone: 'bad' }
};
const RESTART_DETAIL: Record<string, string> = {
  'after-stop': 'The earlier run had stopped cleanly.',
  'after-crash': 'The earlier run did not stop cleanly (power loss or a kill).'
};

/** Why a reading or a save failed, from the recorder's fixed codes. Anything else is not repeated. */
const FAILURE_CAUSE: Record<string, string> = {
  'collect-failed': 'the Windows counters could not be read',
  'host-slow': 'reading this PC took too long',
  'host-busy': 'an earlier reading of this PC was still running',
  'write-failed': 'the sample could not be written',
  'permission-denied': 'permission was denied',
  'disk-full': 'the drive is full'
};
const roundsWords = (n: number) => n + (n === 1 ? ' round' : ' rounds');

/**
 * The detail of a "still failing" note ("code=disk-full rounds=3") or a "recovered" note ("rounds=11 seconds=3300") in plain
 * words. Only the fixed codes and whole numbers are read; a detail that does not fit gives no text at all.
 */
export function failureDetail(kind: string, detail: unknown): string {
  if (typeof detail !== 'string') return '';
  if (kind === 'diag-log-stopped') return detail === 'diag-log-stuck' ? 'Your recordings are not affected. Another program may be holding the file diagnostics.log in the recordings folder. The recorder checks again every minute and adds a note here when it is writing to the file again.' : '';
  if (kind === 'diag-log-resumed') return detail === 'diag-log-ok' ? 'The recorder can write to the file diagnostics.log in the recordings folder again.' : '';
  const slow = kind === 'host-still-slow' || kind === 'host-recovered';
  if (kind === 'collect-still-failing' || kind === 'write-still-failing' || kind === 'host-still-slow') {
    const m = /^code=([a-z-]{1,30}) rounds=(\d{1,9})$/.exec(detail);
    if (!m) return '';
    const cause = own(FAILURE_CAUSE, m[1]);
    return (cause ? 'Latest cause: ' + cause + '. ' : '') + roundsWords(Number(m[2])) + (slow ? ' had no reading of this PC since the last note.' : ' failed since the last note.');
  }
  if (kind === 'recorder-crashed') {
    const m = /^crashes=([0-9]{1,6}) at=/.exec(detail);
    if (!m) return '';
    const n = Number(m[1]);
    return (n === 1 ? 'A crash file was saved' : n + ' crash files were saved') + ' in the "crash" folder inside the recordings folder. Keep it: it shows what the recorder was doing when it stopped.';
  }
  if (kind === 'collect-recovered' || kind === 'write-recovered' || kind === 'host-recovered') {
    const m = /^rounds=(\d{1,9}) seconds=(\d{1,9})$/.exec(detail);
    if (!m) return '';
    return slow ? 'It was slow for ' + roundsWords(Number(m[1])) + ' over ' + lengthWords(Number(m[2])) + ', then was back to normal.' : 'It failed for ' + roundsWords(Number(m[1])) + ' over ' + lengthWords(Number(m[2])) + ', then worked again.';
  }
  return '';
}

export function lengthWords(seconds: number): string {
  const s = Math.max(0, Math.round(seconds));
  if (s < 90) return s + ' s';
  if (s < 5400) return Math.round(s / 60) + ' min';
  return (s / 3600).toFixed(s % 3600 < 360 ? 0 : 1) + ' h';
}
const bytesWords = (n: number) => {
  if (!Number.isFinite(n) || n < 0) return '';
  const u = ['B', 'KB', 'MB', 'GB'];
  let v = n, i = 0;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return (i === 0 || v >= 100 ? Math.round(v) : Number(v.toFixed(1))) + ' ' + u[i];
};

export function dumpItem(d: SeriesDump): HappenedItem | null {
  const t = Date.parse(d.mtime);
  if (!Number.isFinite(t)) return null;
  const target = safeText(d.target);
  const size = bytesWords(d.size);
  return {
    id: 'dump:' + safeText(d.name, 120) + ':' + t, kind: 'dump', t, tone: 'bad',
    title: 'Crash dump: ' + (safeText(d.name) || 'unnamed file'),
    detail: [target ? 'Program: ' + target + '.' : '', size ? 'Size ' + size + '.' : ''].filter(Boolean).join(' ')
  };
}
export function gapItem(g: SeriesGap, i: number): HappenedItem | null {
  const t = Date.parse(g.from), end = Date.parse(g.to);
  if (!Number.isFinite(t) || !Number.isFinite(end)) return null;
  const c = own(CAUSE_TITLES, g.cause) ?? { title: 'No samples for a while', kind: 'late' as HappenedKind, tone: 'warn' as Tone };
  return { id: 'gap:' + i + ':' + t, kind: c.kind, t, end, tone: c.tone, title: c.title, detail: 'No samples for ' + lengthWords(g.seconds) + '.' };
}
export function noteItem(m: SeriesMarker, i: number): HappenedItem | null {
  const t = Date.parse(m.t);
  if (!Number.isFinite(t)) return null;
  const known = own(NOTE_TITLES, m.kind);
  const detail = m.kind === 'restart' ? (own(RESTART_DETAIL, m.detail) ?? '') : failureDetail(m.kind, m.detail);
  return { id: 'note:' + i + ':' + t, kind: m.kind === 'restart' ? 'restart' : 'note', t, tone: known?.tone ?? 'info', title: known?.title ?? 'Recorder note: ' + (safeText(m.kind, 40) || 'unknown'), detail };
}

/**
 * Everything that happened inside the answer's range, newest first. The recorder lists the whole dump folder on every scan, so a
 * dump (or note) older than the range is left out here. A restart that shows both as a note and as a gap is listed once (the gap
 * carries the length). `drawn` holds each kind on its own, capped on its own, so many gaps never push dumps off the timeline.
 */
export interface Happened { items: HappenedItem[]; more: number; drawn: { gaps: HappenedItem[]; dumps: HappenedItem[]; restarts: HappenedItem[] } }
export function happenedItems(a: SeriesAnswer): Happened {
  const lo = Date.parse(a.start), hi = Date.parse(a.end);
  const inRange = (x: HappenedItem) => !(Number.isFinite(lo) && (x.end ?? x.t) < lo) && !(Number.isFinite(hi) && x.t > hi);
  const gaps = a.gaps.map(gapItem).filter((x): x is HappenedItem => !!x).filter(inRange);
  const gapSpans = gaps.filter((g) => g.kind === 'restart' || g.kind === 'crash' || g.kind === 'disk');
  const notes = a.markers.map(noteItem).filter((x): x is HappenedItem => !!x).filter(inRange)
    .filter((n) => !(n.kind === 'restart' && gapSpans.some((g) => n.t >= g.t - 60_000 && n.t <= (g.end ?? g.t) + 60_000)));
  const dumps = a.dumps.map(dumpItem).filter((x): x is HappenedItem => !!x).filter(inRange);
  const newest = (xs: HappenedItem[]) => xs.slice().sort((x, y) => y.t - x.t);
  const all = newest([...dumps, ...gaps, ...notes]);
  const restarts = [...notes, ...gaps].filter((x) => x.kind === 'restart' || x.kind === 'crash');
  return {
    items: all.slice(0, MAX_LISTED), more: Math.max(0, all.length - MAX_LISTED),
    drawn: { gaps: newest(gaps).slice(0, MAX_DRAWN), dumps: newest(dumps).slice(0, MAX_DRAWN), restarts: newest(restarts).slice(0, MAX_DRAWN) }
  };
}

/* ------------------------------------------------------------ windows events */

const LEVELS: Record<string, string> = { 1: 'Critical', 2: 'Error', 3: 'Warning', 4: 'Information' };
const CHANNELS: Record<string, string> = { 1: 'System', 2: 'Application' };
/** Well-known events, by log and ID. Anything else is shown by its number, to look up in Event Viewer. */
const KNOWN_EVENTS: Record<string, string> = {
  '1:41': 'Windows restarted without a clean shutdown (Kernel-Power)',
  '1:6008': 'The last shutdown was not expected',
  '1:1074': 'A shutdown or restart was started',
  '1:6005': 'The Windows event log started (the PC booted)',
  '1:6006': 'The Windows event log stopped (the PC shut down)',
  '1:7031': 'A service ended without being asked to',
  '1:7034': 'A service ended without being asked to',
  '2:1000': 'A program crashed',
  '2:1002': 'A program stopped responding'
};
export interface EventRow { id: string; log: string; level: string; levelNum: number; eventId: number; count: number; meaning: string }

export function eventRows(ids: SeriesEventId[]): { rows: EventRow[]; more: number } {
  const rows = ids.filter((e) => Number.isFinite(e.eventId) && Number.isFinite(e.count)).map((e): EventRow => ({
    id: e.channel + ':' + e.eventId + ':' + e.level,
    log: own(CHANNELS, String(e.channel)) ?? 'Log ' + Math.trunc(e.channel),
    level: own(LEVELS, String(e.level)) ?? 'Level ' + Math.trunc(e.level), levelNum: e.level,
    eventId: Math.trunc(e.eventId), count: Math.trunc(e.count),
    meaning: own(KNOWN_EVENTS, e.channel + ':' + e.eventId) ? 'Usually means: ' + own(KNOWN_EVENTS, e.channel + ':' + e.eventId) : ''
  })).sort((x, y) => (x.levelNum - y.levelNum) || (y.count - x.count) || (x.eventId - y.eventId));
  return { rows: rows.slice(0, MAX_LISTED), more: Math.max(0, rows.length - MAX_LISTED) };
}

/** Times where Windows wrote events at each level (from the per-level count lines the recorder keeps). Newest kept when capped. */
export interface EventDot { t: number; level: number; count: number }
export function eventDots(lines: Pick<SeriesLine, 'key' | 'points'>[]): { dots: EventDot[]; more: number } {
  const all: EventDot[] = [];
  for (const l of lines) {
    const m = /^events\.level(\d)$/.exec(l.key);
    if (!m) continue;
    for (const p of l.points) if (p[1] > 0) all.push({ t: p[0], level: Number(m[1]), count: p[1] });
  }
  all.sort((a, b) => (a.level - b.level) || (b.t - a.t));
  const kept = all.slice(0, MAX_EVENT_DOTS).sort((a, b) => a.t - b.t);
  return { dots: kept, more: Math.max(0, all.length - kept.length) };
}

/* ------------------------------------------------------------ recording health */

export interface HealthRow { label: string; value: string; tone: Tone }

export function healthRows(a: SeriesAnswer): HealthRow[] {
  const rows: HealthRow[] = [];
  if (a.state) rows.push({ label: 'Blackbox state', value: safeText(a.state.label, 40) + (a.state.detail ? ': ' + safeText(a.state.detail, 140) : ''), tone: a.state.tone === 'bad' ? 'bad' : a.state.tone === 'warn' ? 'warn' : 'info' });
  const span = (Date.parse(a.end) - Date.parse(a.start)) / 1000;
  const want = a.intervalSeconds > 0 && span > 0 ? Math.floor(span / a.intervalSeconds) : 0;
  if (want > 0) {
    const pct = Math.min(100, Math.round((a.samples / want) * 100));
    rows.push({ label: 'Samples in this range', value: a.samples + ' of about ' + want + ' (' + pct + ' %)', tone: pct < 90 ? 'warn' : 'info' });
  } else rows.push({ label: 'Samples in this range', value: String(a.samples), tone: 'info' });
  const lost = a.gaps.reduce((n, g) => n + (Number.isFinite(g.seconds) ? g.seconds : 0), 0);
  rows.push({ label: 'Time with no recording', value: a.gaps.length ? lengthWords(lost) + ' in ' + a.gaps.length + (a.gaps.length === 1 ? ' gap' : ' gaps') : 'None', tone: a.gaps.length ? 'warn' : 'info' });
  const unread = (a.read.dropped?.length ?? 0) + (a.read.missing?.length ?? 0);
  rows.push({ label: 'Recording files', value: unread ? unread + ' of ' + a.read.files + ' could not be read to the end' : a.read.files + ' read', tone: unread ? 'warn' : 'info' });
  if (a.read.torn > 0) rows.push({ label: 'Damaged lines', value: a.read.torn + ' skipped', tone: 'warn' });
  return rows;
}

/* ------------------------------------------------------------ status chips */

export interface Chip { id: 'saved' | 'late' | 'partly'; text: string; hint: string }

/**
 * The chips come from this answer only: the recorder's own state (stopped, running late) read with the series, and what could not
 * be read in this range. The drawer's older snapshot and this PC's clock are not used, so a chip never disagrees with the health list.
 */
export function statusChips(a: SeriesAnswer | null): Chip[] {
  const chips: Chip[] = [];
  if (!a) return chips;
  if (a.state?.state === 'stopped') chips.push({ id: 'saved', text: 'Saved copy', hint: 'This Blackbox is not recording right now. You are looking at what it saved earlier.' });
  if (a.state?.state === 'running-late') chips.push({ id: 'late', text: 'Running late', hint: 'The recorder is taking samples later than planned right now. A busy PC makes it slow down on purpose to stay light.' });
  const unread = (a.read.dropped?.length ?? 0) + (a.read.missing?.length ?? 0) > 0;
  const cut = a.groups.some((g) => a.samples > 0 && g.read === 0 && (g.slow > 0 || g.busy > 0 || g.missing > 0)) || !!a.truncated;
  if (unread || cut) chips.push({ id: 'partly', text: 'Partly ready', hint: 'Some recording files or some counters could not be read in this range. Charts show what was read.' });
  return chips;
}
