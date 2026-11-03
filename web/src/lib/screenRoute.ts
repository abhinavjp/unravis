import type { Screen } from './screenFlow';

/**
 * The top-level screen as an address: a bare screen name and, in the guided analysis, a step number. Nothing else belongs here: no case
 * code, id, token or window (the session token is carried by wayfinding.ts, never by a route). Pure, so the rules are testable without a browser.
 *
 *   #/cases              the start screen            #/case                the case page
 *   #/create             the guided create flow      #/case/analysis       the analysis explorer (its own views are #/where, #/op, ... in wayfinding.ts)
 *   #/case/guided/3      the guided analysis, step 3
 */
export type ScreenRoute =
  | { screen: 'start' | 'create' | 'case' | 'running' }
  | { screen: 'case'; guided: number };

/** Guided steps are 0 to STEP_COUNT - 1 (lib/guided/stepModel.ts); an address outside that range is not a route. */
export const MAX_GUIDED_STEP = 10;

/** Screens that have an address of their own. The key screens (loading, unlock, setup) never do. */
export const ROUTE_SCREENS: readonly Screen[] = ['start', 'create', 'case', 'running'];

/** The address for a route, always of the form '#/...'. A guided step outside its range is clamped, never written raw. */
export function formatScreenRoute(route: ScreenRoute): string {
  if ('guided' in route) {
    const step = Number.isInteger(route.guided) ? Math.min(MAX_GUIDED_STEP, Math.max(0, route.guided)) : 0;
    return '#/case/guided/' + step;
  }
  switch (route.screen) {
    case 'start': return '#/cases';
    case 'create': return '#/create';
    case 'case': return '#/case';
    case 'running': return '#/case/analysis';
  }
}

/** The route an address names, or null for anything else (empty, a session link, a view address, an unknown name, a bad step). A query is never read. */
export function parseScreenRoute(hash: string): ScreenRoute | null {
  const body = hash.startsWith('#') ? hash.slice(1) : hash;
  if (!body.startsWith('/')) return null;
  const cut = body.indexOf('?');
  const path = (cut < 0 ? body : body.slice(0, cut)).replace(/\/+$/, '');
  switch (path) {
    case '/cases': return { screen: 'start' };
    case '/create': return { screen: 'create' };
    case '/case': return { screen: 'case' };
    case '/case/analysis': return { screen: 'running' };
  }
  const guided = /^\/case\/guided\/(\d{1,2})$/.exec(path);
  if (guided && guided[1].length === String(Number(guided[1])).length) {
    const step = Number(guided[1]);
    return step <= MAX_GUIDED_STEP ? { screen: 'case', guided: step } : null;
  }
  return null;
}

/**
 * The screen a route may restore. A case route needs a case that is still open and a key that is not locked; the start screen is the safe
 * fallback for anything else.
 */
export function restorableScreen(route: ScreenRoute | null, facts: { caseOpen: boolean; locked: boolean }): Screen {
  if (!route || facts.locked) return 'start';
  if (route.screen === 'case' || route.screen === 'running') return facts.caseOpen ? route.screen : 'start';
  return route.screen;
}

/** The guided step an address names, or null when it names none. */
export const guidedStepOf = (route: ScreenRoute | null): number | null => (route && 'guided' in route ? route.guided : null);
