import { formatTime } from './time';
import type { CoverageReport, PatternReport } from './types';

/**
 * The last Patterns reports and the last coverage reports, kept in module memory so a closed drawer can show its result again when it reopens.
 * RAM only: nothing goes to storage, and nothing is read from a server here. A reopened result is never run again by itself.
 * The drawer shows when the result was found and offers Run again, which always asks the server and replaces the entry.
 * No TTL, because the age is always on screen. A coverage refusal (409) and an error are never stored.
 */

export const PATTERNS_MAX = 3;
export const COVERAGE_MAX = 8;

export const DRAWER_RESULTS_TEXT = {
  runAgain: 'Run again',
  savedNote: 'This is the result from when you last looked. It has not read the data again.',
  justNow: 'less than 1 min ago'
} as const;

export interface StoredPatterns {
  /** The drawer's own ask key: sources, start, end, limits and the quiet-gap option. */
  key: string;
  /** Sources, start and end only. A reopened drawer looks the last report up by this. */
  windowKey: string;
  sourceIds: string[];
  /** The limits and the quiet-gap option the report was run with, put back in the boxes on reopen. */
  stallSec: string;
  gapSec: string;
  idleAsked: boolean;
  /** Days B9 was asked to read (0 or missing: not asked), and whether the audit-create series was asked for. */
  stepDays?: number;
  auditAsked?: boolean;
  report: PatternReport;
  savedAt: number;
}
export interface StoredCoverage {
  key: string;
  sourceId: string;
  report: CoverageReport;
  savedAt: number;
}

// Oldest first. A save moves its entry to the end, so the front is the one to drop.
let patternsStore: StoredPatterns[] = [];
let coverageStore: StoredCoverage[] = [];

// Plain listeners, so a panel that reads the stored report (the resource panel reads its stall periods) can look again when it is replaced or cleared.
const patternsListeners = new Set<() => void>();
export function onPatternsChange(listener: () => void): () => void {
  patternsListeners.add(listener);
  return () => { patternsListeners.delete(listener); };
}
const patternsChanged = () => { for (const listener of [...patternsListeners]) listener(); };

export const patternsWindowKey = (sourceIds: string[], start: string, end: string): string => [sourceIds.join(','), start, end].join('|');
export const coverageKey = (sourceId: string, start: string, end: string): string => [sourceId, start, end].join('|');

const known = (ids: string[], knownSourceIds: string[]): boolean => ids.every((id) => knownSourceIds.includes(id));

/** Keep a report. The entry for the same ask is replaced; beyond three, the oldest goes. */
export function savePatterns(entry: Omit<StoredPatterns, 'savedAt' | 'sourceIds'> & { sourceIds: string[]; savedAt?: number }): StoredPatterns {
  const stored: StoredPatterns = { ...entry, sourceIds: [...entry.sourceIds], savedAt: entry.savedAt ?? Date.now() };
  patternsStore = [...patternsStore.filter((item) => item.key !== stored.key), stored].slice(-PATTERNS_MAX);
  patternsChanged();
  return stored;
}

/**
 * The newest report for this window, or null. On the way, any entry that names a source missing from `knownSourceIds` is dropped for good.
 */
export function readPatterns(windowKey: string, knownSourceIds: string[]): StoredPatterns | null {
  patternsStore = patternsStore.filter((item) => known(item.sourceIds, knownSourceIds));
  for (let index = patternsStore.length - 1; index >= 0; index--) {
    if (patternsStore[index].windowKey === windowKey) return patternsStore[index];
  }
  return null;
}

/** Keep a finished census. Callers pass only a real report: a refusal or an error is not a result. */
export function saveCoverage(entry: Omit<StoredCoverage, 'savedAt'> & { savedAt?: number }): StoredCoverage {
  const stored: StoredCoverage = { ...entry, savedAt: entry.savedAt ?? Date.now() };
  coverageStore = [...coverageStore.filter((item) => item.key !== stored.key), stored].slice(-COVERAGE_MAX);
  return stored;
}

export function readCoverage(key: string, knownSourceIds: string[]): StoredCoverage | null {
  coverageStore = coverageStore.filter((item) => known([item.sourceId], knownSourceIds));
  return coverageStore.find((item) => item.key === key) ?? null;
}

/** Forget everything: the session ended, so nothing on screen may outlive it. */
export function clearDrawerResults(): void {
  patternsStore = [];
  coverageStore = [];
  patternsChanged();
}

export const drawerResultCounts = () => ({ patterns: patternsStore.length, coverage: coverageStore.length });

/** "less than 1 min ago", "5 min ago", "2 h ago", "3 d ago". */
export function ageText(savedAt: number, now: number): string {
  const minutes = Math.floor(Math.max(0, now - savedAt) / 60_000);
  if (minutes < 1) return DRAWER_RESULTS_TEXT.justNow;
  if (minutes < 60) return minutes + ' min ago';
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return hours + ' h ago';
  return Math.floor(hours / 24) + ' d ago';
}

/** "Found at 14:32 UTC (5 min ago)". The day is added once the result is a day old, so the clock cannot be misread. */
export function foundAtText(savedAt: number, now: number, zone: string): string {
  const old = now - savedAt >= 24 * 3_600_000;
  return 'Found at ' + formatTime(savedAt, { zone, precision: 'minute', date: old }) + ' (' + ageText(savedAt, now) + ')';
}
