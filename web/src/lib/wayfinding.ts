import type { Crumb } from './depth';
import type { Lens } from './pulse';

/**
 * Wayfinding: how the app tells the address bar where the reader is, and how it keeps the trail short on a narrow screen.
 * Everything here is pure (the browser objects are passed in), so the rules can be tested without a browser.
 */

/** The breadcrumb for a narrow screen: the first level, then an ellipsis menu for the middle, then the last levels. */
export interface CollapsedCrumbs { visible: Crumb[]; hidden: Crumb[]; /** Index in visible after which the ellipsis menu sits; -1 when nothing is hidden. */ gapAfter: number }

/** Keeps the first crumb and the last `keepTail` crumbs; the ones between move into the menu. Nothing collapses unless at least two would be hidden (a menu for one crumb saves nothing). */
export function collapseCrumbs(crumbs: Crumb[], keepTail = 1): CollapsedCrumbs {
  const tail = Math.max(1, keepTail);
  if (crumbs.length - 1 - tail < 2) return { visible: crumbs, hidden: [], gapAfter: -1 };
  return { visible: [crumbs[0], ...crumbs.slice(crumbs.length - tail)], hidden: crumbs.slice(1, crumbs.length - tail), gapAfter: 0 };
}

/** The menu item to focus after an arrow, Home or End key (index is -1 when none has focus); null for any other key. Arrows wrap around. */
export function nextMenuIndex(key: string, index: number, count: number): number | null {
  if (count <= 0) return null;
  switch (key) {
    case 'ArrowDown': return (index + 1) % count;
    case 'ArrowUp': return index <= 0 ? count - 1 : index - 1;
    case 'Home': return 0;
    case 'End': return count - 1;
    default: return null;
  }
}

/* ---- View state in the address bar ---- */

/** 'list' is a comparison-wide list a link cannot rebuild (an overview tile or quality drill, a breakdown filter on its own); it still takes a history step so Back climbs one level at a time. */
export type ViewType = 'where' | 'op' | 'all' | 'list' | 'record';

/** The part of the screen that can be rebuilt from the address: depth, the operation, the tab and (at the deepest level) one record. Filters and ranges are not in it. */
export interface View {
  type: ViewType;
  /** The ranked list is open (always set for 'where'; also set when an operation or record sits above it). */
  lens?: Lens;
  compare?: boolean;
  controller?: string;
  action?: string;
  opKind?: string;
  tab?: string;
  sourceId?: string;
  recordId?: string;
  /** A record opened with no operation or all-operations list behind it (from the Patterns drawer, or from a list a link cannot rebuild). */
  solo?: boolean;
}

const lenses: readonly Lens[] = ['requests', 'completion', 'p95', 'client', 'server'];
const tabPattern = /^[a-z]{1,24}$/;
const maxField = 300;

/** The launch link carries the session token as t=..., which the page removes at start. If one is ever present it is carried along untouched and never read or shown. */
function splitHash(hash: string): { path: string; params: URLSearchParams } {
  const body = hash.startsWith('#') ? hash.slice(1) : hash;
  if (!body.startsWith('/')) return { path: '', params: new URLSearchParams(body) };
  const cut = body.indexOf('?');
  return cut < 0 ? { path: body, params: new URLSearchParams() } : { path: body.slice(0, cut), params: new URLSearchParams(body.slice(cut + 1)) };
}

/** The view an address describes, or null for the first screen (empty, the bare session link, or anything this app did not write). */
export function parseViewHash(hash: string): View | null {
  const { path, params } = splitHash(hash);
  const type = path.slice(1);
  if (type !== 'where' && type !== 'op' && type !== 'all' && type !== 'list' && type !== 'record') return null;
  const field = (name: string) => { const value = params.get(name); return value && value.length <= maxField ? value : undefined; };
  const lensValue = params.get('lens');
  const lens = lenses.find(item => item === lensValue);
  const tabValue = params.get('tab') ?? '';
  const tab = tabPattern.test(tabValue) ? tabValue : undefined;
  if (type === 'where') return { type, lens: lens ?? 'requests', ...(params.get('compare') === '1' ? { compare: true } : {}) };
  if (type === 'list') return { type, ...(lens ? { lens } : {}) };
  const view: View = { type, ...(lens ? { lens } : {}), ...(tab ? { tab } : {}) };
  if (type === 'all') return view;
  if (type === 'record' && params.get('solo') === '1') {
    const sourceId = field('s'), recordId = field('id');
    if (!sourceId || !recordId) return null;
    return { type, ...(lens ? { lens } : {}), solo: true, sourceId, recordId };
  }
  const controller = field('c'), action = field('a');
  // An operation needs both names. A record with neither sits in the all-operations list.
  if (type === 'op' ? !controller || !action : !controller !== !action) return null;
  if (controller && action) Object.assign(view, { controller, action }, field('k') ? { opKind: field('k') } : {});
  if (type === 'record') {
    const sourceId = field('s'), recordId = field('id');
    if (!sourceId || !recordId) return null;
    Object.assign(view, { sourceId, recordId });
  }
  return view;
}

/** The session token fragment of an address (t=...), or '' when there is none. Used only to carry it forward. */
function carriedToken(hash: string): string {
  return splitHash(hash).params.get('t') ?? '';
}

/**
 * The address for a view. A null view is the first screen: no view part. Any session token already in the address is kept as it was,
 * after the view, so writing the view never removes or changes it.
 */
export function formatViewHash(view: View | null, currentHash = ''): string {
  const token = carriedToken(currentHash);
  if (!view) return token ? '#t=' + encodeURIComponent(token) : '';
  const params = new URLSearchParams();
  if (view.type === 'where') {
    params.set('lens', view.lens ?? 'requests');
    if (view.compare) params.set('compare', '1');
  } else {
    if (view.lens) params.set('lens', view.lens);
    if (view.controller !== undefined) params.set('c', view.controller);
    if (view.action !== undefined) params.set('a', view.action);
    if (view.opKind) params.set('k', view.opKind);
    if (view.type === 'record') { params.set('s', view.sourceId ?? ''); params.set('id', view.recordId ?? ''); if (view.solo) params.set('solo', '1'); }
    if (view.tab) params.set('tab', view.tab);
  }
  if (token) params.set('t', token);
  const query = params.toString();
  return '#/' + view.type + (query ? '?' + query : '');
}

const listLike = (type: ViewType) => type === 'all' || type === 'list';

/** Two views are the same place when only the tab, the ranking figure or the compare switch differ; those replace the address rather than add a step. All operations and a list drill are one level. */
export function sameLevel(a: View | null, b: View | null): boolean {
  if (!a || !b) return a === b;
  return (a.type === b.type || (listLike(a.type) && listLike(b.type))) && (a.lens !== undefined) === (b.lens !== undefined) && !a.solo === !b.solo
    && a.controller === b.controller && a.action === b.action && a.opKind === b.opKind && a.sourceId === b.sourceId && a.recordId === b.recordId;
}

/** What the screen shows, in the terms the address uses. */
export interface ViewScreen {
  /** 0 pulse, 1 ranked list, 2 evidence, 3 one record. */
  depth: 0 | 1 | 2 | 3;
  whereOpen: boolean;
  lens: Lens;
  compare: boolean;
  operation: { controller: string; action: string; kind?: string } | null;
  /** The list is the all-operations frame (a breakdown filter inside it is the same place). */
  allOperations: boolean;
  tab: string;
  record: { sourceId: string; id: string } | null;
}

/** The address for the screen. Every level has one, so each takes its own history step and Back climbs one level at a time. */
export function describeView(screen: ViewScreen): View | null {
  const { depth } = screen;
  if (depth === 0) return null;
  if (depth === 1) return { type: 'where', lens: screen.lens, ...(screen.compare ? { compare: true } : {}) };
  const above = screen.whereOpen ? { lens: screen.lens } : {};
  const tab = screen.tab ? { tab: screen.tab } : {};
  let view: View;
  if (screen.operation) view = { type: 'op', ...above, controller: screen.operation.controller, action: screen.operation.action, ...(screen.operation.kind ? { opKind: screen.operation.kind } : {}), ...tab };
  else if (screen.allOperations) view = { type: 'all', ...above, ...tab };
  else if (depth === 3 && screen.record) return { type: 'record', ...above, solo: true, sourceId: screen.record.sourceId, recordId: screen.record.id };
  else return { type: 'list', ...above };
  if (depth === 3 && screen.record) return { ...view, type: 'record', sourceId: screen.record.sourceId, recordId: screen.record.id };
  return view;
}

/* ---- The browser history for those views ---- */

export interface LocationLike { hash: string; pathname: string; search: string }
export interface HistoryLike { state: unknown; pushState(state: unknown, unused: string, url?: string | URL | null): void; replaceState(state: unknown, unused: string, url?: string | URL | null): void; back(): void }

export type SyncResult = 'none' | 'replace' | 'push' | 'back';

const indexOf = (state: unknown): number => {
  const value = state && typeof state === 'object' ? (state as { uvIdx?: unknown }).uvIdx : undefined;
  return typeof value === 'number' && Number.isInteger(value) && value >= 0 ? value : 0;
};

/** history.state keys that name the screen an entry belongs to. The life key ties a mark to this page load, so a reload ignores old marks. */
export const SCREEN_MARK = 'uvScreen';
const LIFE_MARK = 'uvLife';

/**
 * Keeps the address in step with the screen. Going deeper adds a history step, so the browser Back button walks back up; stepping up with Esc
 * or a breadcrumb goes back one step when the entry before is the place being returned to, so Back and Esc stay the same thing.
 * Changing only the tab or the ranking figure replaces the current entry. The browser's own Back and Forward are read by pop().
 */
export function createViewHistory(loc: LocationLike, hist: HistoryLike) {
  let index = indexOf(hist.state);
  const stack: string[] = [];
  stack[index] = loc.hash;
  let current = parseViewHash(loc.hash);
  let ownBacks = 0;
  const url = (hash: string) => loc.pathname + loc.search + hash;
  // The screen the current entry was made on ('' when unknown), read only from marks written during this page load.
  const life = Math.random().toString(36).slice(2);
  const screenOf = (state: unknown): string => {
    if (!state || typeof state !== 'object') return '';
    const record = state as Record<string, unknown>;
    return record[LIFE_MARK] === life && typeof record[SCREEN_MARK] === 'string' ? (record[SCREEN_MARK] as string) : '';
  };
  const stamp = (name: string): Record<string, unknown> => (name ? { uvIdx: index, [SCREEN_MARK]: name, [LIFE_MARK]: life } : { uvIdx: index });
  let shown = screenOf(hist.state);
  const marks: string[] = [];
  marks[index] = shown;
  return {
    /** The view the address describes right now. */
    get view() { return current; },
    /** The screen the current history entry stands for, or '' when it has no mark from this page load. */
    get screen() { return shown; },
    /** Makes the address match a view. `replace` forces a replace (used after the screen was rebuilt from the address). */
    sync(view: View | null, replace = false): SyncResult {
      const target = formatViewHash(view, loc.hash);
      if (target === loc.hash) { current = view; stack[index] = target; return 'none'; }
      if (replace || sameLevel(current, view)) {
        hist.replaceState(stamp(shown), '', url(target));
        stack[index] = target; current = view; return 'replace';
      }
      // Stepping up goes back only when the entry before was made on the same screen.
      if (index > 0 && stack[index - 1] === target && marks[index - 1] === shown) {
        ownBacks++; index--; current = view; hist.back(); return 'back';
      }
      index++; stack.length = index; marks.length = index; stack[index] = target; marks[index] = shown; current = view;
      hist.pushState(stamp(shown), '', url(target));
      return 'push';
    },
    /** Marks the current entry (`replace`) or adds an entry (default) for a screen, at the address shown now. Does nothing when the entry already says `name`. */
    markScreen(name: string, replace = false): void {
      if (!name || name === shown) return;
      shown = name;
      if (replace) { hist.replaceState(stamp(name), '', url(loc.hash)); marks[index] = name; return; }
      index++; stack.length = index; marks.length = index; stack[index] = loc.hash; marks[index] = name;
      hist.pushState(stamp(name), '', url(loc.hash));
    },
    /** Call on popstate. Returns the view to show, null for the first screen, or undefined when the event was this module's own Back. `screen` then names the entry's screen. */
    pop(): View | null | undefined {
      index = indexOf(hist.state);
      if (stack[index] === undefined) stack[index] = loc.hash;
      current = parseViewHash(loc.hash);
      shown = screenOf(hist.state);
      marks[index] = shown;
      if (ownBacks > 0) { ownBacks--; return undefined; }
      return current;
    }
  };
}
