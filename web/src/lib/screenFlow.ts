import type { LockState } from './masterKey';

/**
 * Which screen the app shows, and the moves between them. Pure: no DOM, no fetch, no state of its own.
 * appState.svelte.ts holds the current screen and asks `go` whether a move is allowed.
 *
 *   loading -> unlock | start | running
 *   unlock  -> start | running | create | case  (after the right key; back to the work it interrupted: the analysis, the case home, or the create guide)
 *   setup   -> start | unlock | case   (case: only when set-up was opened from a case page, so it can go back there)
 *   start   -> setup | create | running | unlock | case
 *   create  -> start | running | unlock | case
 *   running -> start | unlock | case
 *   case    -> start | running | create | unlock | setup   (the home page of one case: story, theories, health checks, files)
 *
 * Staying where you are is allowed and does nothing. Any other move is refused and left as it was.
 */
export type Screen = 'loading' | 'unlock' | 'setup' | 'start' | 'create' | 'running' | 'case';

export const SCREENS: readonly Screen[] = ['loading', 'unlock', 'setup', 'start', 'create', 'running', 'case'];

const MOVES: Readonly<Record<Screen, readonly Screen[]>> = {
  loading: ['unlock', 'start', 'running'],
  unlock: ['start', 'running', 'create', 'case'],
  setup: ['start', 'unlock', 'case'],
  start: ['setup', 'create', 'running', 'unlock', 'case'],
  create: ['start', 'running', 'unlock', 'case'],
  running: ['start', 'unlock', 'case'],
  case: ['start', 'running', 'create', 'unlock', 'setup']
};

export type Move = { ok: true; screen: Screen } | { ok: false; screen: Screen; reason: string };

/** True when the app may move from one screen to another (or stay put). */
export function canGo(from: Screen, to: Screen): boolean {
  return from === to || MOVES[from].includes(to);
}

/** The reducer: the screen after a request to move. A refused move keeps the current screen and says which move it was. */
export function go(from: Screen, to: Screen): Move {
  if (canGo(from, to)) return { ok: true, screen: to };
  return { ok: false, screen: from, reason: 'The move from ' + from + ' to ' + to + ' is not allowed.' };
}

export interface StartFacts {
  /** The demo server: sample data, no case store, nothing to unlock. */
  demo: boolean;
  /** The address carries a view (a reload or a shared link), so the person asked for a place inside the analysis. */
  deepLink: boolean;
  /** This tab remembered an investigation, so a reload goes back to it. */
  resume: boolean;
  /** The master key state, or 'unknown' when the status could not be read. */
  lock: LockState | 'unknown';
}

/** The first screen once the app has started. A locked key comes first; demo data, a link and a remembered investigation skip the start screen. */
export function initialScreen(facts: StartFacts): Screen {
  if (facts.demo) return 'running';
  if (facts.lock === 'locked' || facts.lock === 'damaged') return 'unlock';
  if (facts.deepLink || facts.resume) return 'running';
  return 'start';
}

/**
 * Where a successful unlock goes: back to the work it interrupted, otherwise the start screen. The create guide comes back as it was, so a
 * case that was being made carries on from where it stopped (TSK-19).
 */
export function afterUnlock(origin: Screen | null): Screen {
  return origin === 'running' || origin === 'create' || origin === 'case' ? origin : 'start';
}

/** Where leaving the master key set-up goes: back to the case page it was opened from, otherwise the start screen. */
export function setupReturn(origin: Screen | null): Screen {
  return origin === 'case' ? 'case' : 'start';
}

/**
 * The guided "create a new case" flow is built (phase 05), so "Create a new one" opens it. With this off, the card opens the existing
 * import panel over the working screen instead.
 */
export const WIZARD_READY = true;

export type CreateRoute = { screen: 'create'; panel: null } | { screen: 'running'; panel: 'import' };

/** Where "Create a new one" leads: the guided flow when it exists, otherwise the import panel over the working screen. */
export function createRoute(wizardReady: boolean = WIZARD_READY): CreateRoute {
  return wizardReady ? { screen: 'create', panel: null } : { screen: 'running', panel: 'import' };
}

/** Screens that show nothing from a case, so a case drawer must not stay open on them. */
export function leavesCaseWork(to: Screen): boolean {
  return to !== 'running';
}
