import { serializeRecordFilters } from './investigation';
import type { NumberRange, Query, RangeKey } from './types';

/**
 * Progressive depth. D0 is the pulse (figures and one timeline), D1 ranks where the problem is, D2 is the evidence for one
 * operation or list, D3 is a single record. The helpers here are pure so the transitions can be tested without a browser.
 */
export type Depth = 0 | 1 | 2 | 3;

export interface Nav {
  /** The ranked "where is it" view is open. */
  whereOpen: boolean;
  /** A request list context exists: an operation, a quality or tile drill-down, or a breakdown filter. */
  hasContext: boolean;
  /** One record is open. */
  recordOpen: boolean;
}

export const depthOf = (nav: Nav): Depth => nav.recordOpen ? 3 : nav.hasContext ? 2 : nav.whereOpen ? 1 : 0;

export interface Crumb { id: 'pulse' | 'where' | 'evidence' | 'record'; label: string; depth: Depth; current: boolean }

/** The trail beyond D0: Pulse, then Where (when it was used), the operation or list, then the request. Empty at D0, where there is nowhere to step back to. */
export function breadcrumb(nav: Nav, labels: { evidence?: string; record?: string } = {}): Crumb[] {
  const depth = depthOf(nav);
  if (depth === 0) return [];
  const crumbs: Crumb[] = [{ id: 'pulse', label: 'Pulse', depth: 0, current: false }];
  if (nav.whereOpen) crumbs.push({ id: 'where', label: 'Where is it?', depth: 1, current: false });
  if (nav.hasContext) crumbs.push({ id: 'evidence', label: labels.evidence || 'Operation', depth: 2, current: false });
  if (nav.recordOpen) crumbs.push({ id: 'record', label: labels.record || 'Request', depth: 3, current: false });
  crumbs[crumbs.length - 1].current = true;
  return crumbs;
}

/** What one step up (Esc or Back) closes: the record, then the operation or list, then Where; null at D0. */
export type StepTarget = 'record' | 'evidence' | 'where' | null;
export const stepUpTarget = (nav: Nav): StepTarget => nav.recordOpen ? 'record' : nav.hasContext ? 'evidence' : nav.whereOpen ? 'where' : null;

/** The navigation after one step up. Closing the operation keeps Where open, so the ranked list is still there. */
export function stepUp(nav: Nav): Nav {
  switch (stepUpTarget(nav)) {
    case 'record': return { ...nav, recordOpen: false };
    case 'evidence': return { ...nav, hasContext: false };
    case 'where': return { ...nav, whereOpen: false };
    default: return nav;
  }
}

/** The navigation after stepping up until at most the given depth remains. */
export function stepUpTo(nav: Nav, depth: Depth): Nav {
  let next = nav;
  for (let guard = 0; guard < 4 && depthOf(next) > depth; guard++) next = stepUp(next);
  return next;
}

/** The quick range that the query's windows match exactly: current window ends at some "now", the reference is the equal window right before it. */
export function activeRangeId(query: Pick<Query, 'start' | 'end' | 'baselineStart' | 'baselineEnd'>, ranges: readonly { id: string; minutes: number }[]): string | null {
  const start = Date.parse(query.start), end = Date.parse(query.end);
  const baselineStart = Date.parse(query.baselineStart), baselineEnd = Date.parse(query.baselineEnd);
  if (![start, end, baselineStart, baselineEnd].every(Number.isFinite)) return null;
  const span = end - start;
  if (span <= 0 || baselineEnd !== start || start - baselineStart !== span) return null;
  return ranges.find(range => range.minutes * 60_000 === span)?.id ?? null;
}

export type ChipGroup = 'kind' | 'outcome' | 'metric' | 'scope' | 'record' | 'range';
/** One active filter that lives in the More filters panel, with the patch that removes it. A patch value of undefined clears an optional field. */
export interface FilterChip { id: string; group: ChipGroup; label: string; value: string; patch: Partial<Query> }

const outcomeLabels: Record<string, string> = { success: 'Success', failure: 'Failure', client_error: '4xx client errors', server_error: '5xx server errors' };
const completionLabels: Record<string, string> = { complete: 'Complete', incomplete: 'Missing timing', pending: 'Pending async', invalid: 'Unusable timing' };
const rangeLabels: Record<RangeKey, string> = {
  observedMs: 'Observed time', apiTimeMs: 'API time', auditCreateTimeMs: 'Audit create time', requestKb: 'Request size', responseKb: 'Response size', incompleteAgeMs: 'Open for'
};
const rangeUnits: Record<RangeKey, string> = { observedMs: 'ms', apiTimeMs: 'ms', auditCreateTimeMs: 'ms', requestKb: 'KB', responseKb: 'KB', incompleteAgeMs: 'ms' };
const rangeText = (range: NumberRange, unit: string) => range.min != null && range.max != null ? `${range.min}–${range.max} ${unit}` : range.min != null ? `at least ${range.min} ${unit}` : `at most ${range.max} ${unit}`;

/**
 * The filters that sit inside More filters and are currently in use, as removable chips. The request type and the two time windows are not
 * chips: they have their own always-visible controls. The one exception is the polling-type-unknown kind, which only the panel can set.
 */
export function filterChips(query: Query): FilterChip[] {
  const chips: FilterChip[] = [];
  const add = (id: string, group: ChipGroup, label: string, value: string, patch: Partial<Query>) => chips.push({ id, group, label, value, patch });
  if (query.kind === 'http_unknown') add('kind', 'kind', 'Request type', 'HTTP (polling type unknown)', { kind: 'all' });
  if (query.outcome && query.outcome !== 'all') add('outcome', 'outcome', 'Result', outcomeLabels[query.outcome] ?? query.outcome, { outcome: 'all' });
  if (query.metric === 'api') add('metric', 'metric', 'Time measured', 'API processing', { metric: 'total' });
  if (query.controller?.trim()) add('controller', 'scope', 'Controller', query.controller.trim(), { controller: '' });
  if (query.action?.trim()) add('action', 'scope', 'Action', query.action.trim(), { action: '' });
  const record = serializeRecordFilters(query);
  if (record.operation) add('operation', 'record', 'Operation', record.operation, { operation: undefined });
  if (record.completion && record.completion !== 'all') add('completion', 'record', 'Completion', completionLabels[record.completion] ?? record.completion, { completion: undefined });
  if (record.userIds?.length) add('userIds', 'record', 'User', record.userIds.join(', '), { userIds: undefined });
  if (record.uiVersions?.length) add('uiVersions', 'record', 'UI version', record.uiVersions.join(', '), { uiVersions: undefined });
  if (record.userIps?.length) add('userIps', 'record', 'User IP', record.userIps.join(', '), { userIps: undefined });
  if (record.statusCodes?.length) add('statusCodes', 'record', 'Status code', record.statusCodes.join(', '), { statusCodes: undefined });
  if (record.userAgentContains) add('userAgentContains', 'record', 'User agent contains', record.userAgentContains, { userAgentContains: undefined });
  if (record.urlContains) add('urlContains', 'record', 'URL contains', record.urlContains, { urlContains: undefined });
  if (record.auditId) add('auditId', 'record', 'Audit id', record.auditId, { auditId: undefined });
  for (const key of Object.keys(rangeLabels) as RangeKey[]) {
    const range = record[key];
    if (range) add(key, 'range', rangeLabels[key], rangeText(range, rangeUnits[key]), { [key]: undefined } as Partial<Query>);
  }
  return chips;
}

/** Removes one chip's filter from the query in place, the same way a quick range is applied, so reactive state notices every changed key. */
export function applyChipPatch(query: Query, chip: Pick<FilterChip, 'patch'>): void {
  Object.assign(query, chip.patch);
}

/** The count shown on the More filters button. */
export const moreFiltersCount = (query: Query): number => filterChips(query).length;

export interface EvidenceTab { id: 'timeline' | 'phases' | 'failures' | 'incomplete' | 'slowest' | 'breakdown'; label: string }
export interface EvidenceContext {
  /** An operation (controller, action and request type) is open. */
  operation: boolean;
  /** One API group (source, action, type, outcome) drives the charts. */
  group: boolean;
  /** The query that scopes Failures and Breakdown exists. */
  scoped: boolean;
  /** The group has a phase split or a payload time to show. */
  phases: boolean;
  /** Failed events in the operation or group; 0 when none are known. */
  failed: number;
  /** Incomplete, pending and unusable events of the operation or group. */
  unfinished: number;
}

/**
 * The tabs the evidence frame offers. An operation or one of its groups shows only what exists for it (Slowest is limited to that
 * operation). A comparison-wide context (all operations, an overview or quality drill, a breakdown filter) has no single group, but its
 * comparison-wide Failures, Breakdown and Slowest stay reachable, as they always were.
 */
export function evidenceTabs(context: EvidenceContext): EvidenceTab[] {
  const tabs: EvidenceTab[] = [];
  const focused = context.operation || context.group;
  if (context.group) tabs.push({ id: 'timeline', label: 'Timeline' });
  if (context.group && context.phases) tabs.push({ id: 'phases', label: 'Phases' });
  if (context.scoped && (!focused || context.failed > 0)) tabs.push({ id: 'failures', label: 'Failures' });
  if (context.operation && context.unfinished > 0) tabs.push({ id: 'incomplete', label: 'Incomplete' });
  tabs.push({ id: 'slowest', label: 'Slowest' });
  if (context.scoped) tabs.push({ id: 'breakdown', label: 'Breakdown' });
  return tabs;
}

/** The four More filters groups that carry a count. The When group has no chips: the time windows are not filters in use. */
export type PanelGroup = 'what' | 'details' | 'ranges' | 'chart';

const panelGroupOf: Record<string, PanelGroup> = {
  kind: 'what', outcome: 'what', controller: 'what', action: 'what', operation: 'what', completion: 'what',
  userIds: 'details', uiVersions: 'details', userIps: 'details', statusCodes: 'details', userAgentContains: 'details', urlContains: 'details', auditId: 'details',
  metric: 'chart',
  ...Object.fromEntries(Object.keys(rangeLabels).map(key => [key, 'ranges' as PanelGroup]))
};

/** How many active chips each panel group holds. A chip with an id this table does not know throws, so a new chip cannot silently drop out of the counts. */
export function panelGroupCounts(chips: readonly FilterChip[]): Record<PanelGroup, number> {
  const counts: Record<PanelGroup, number> = { what: 0, details: 0, ranges: 0, chart: 0 };
  for (const chip of chips) {
    const group = Object.hasOwn(panelGroupOf, chip.id) ? panelGroupOf[chip.id] : undefined;
    if (!group) throw new Error(`No More filters group for chip "${chip.id}"`);
    counts[group]++;
  }
  return counts;
}

/** The count line at the top of the panel. */
export const filtersInUseText = (count: number): string => count === 0 ? 'No filters in use' : count === 1 ? '1 filter in use' : `${count} filters in use`;

/** The kind the "polling type is unknown" checkbox sets. Unchecking returns to plain HTTP, so the bar's HTTP segment lights up again. */
export const unknownKindToggle = (checked: boolean): Query['kind'] => checked ? 'http_unknown' : 'http';