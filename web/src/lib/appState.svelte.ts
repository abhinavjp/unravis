// Shared application state for the Unravis UI (Svelte 5 runes).
// The state lives in module-level runes so the logic below reads exactly as it did inside App.svelte; components read and
// write it through the exported accessor objects at the end of this file (app, scope, results, evidence, navigation, panels, conn).
// startApp() must be called once from App.svelte's script: it registers the effects and the mount work, which need a component.
import { onMount, tick, untrack } from 'svelte';
import { api, ApiError, hasSessionToken } from './api';
import { createDrawerHistory } from './drawerHistory';
import { classifyStartupFailure } from './copy/startupCopy';
import { bumpEpoch } from './evidenceMemo';
import { defaultQuery } from './time';
import { initialComparison, kindLabel, queryFingerprint, resultNotes, rankComparisons, stateLabel } from './presentation';
import { clearInvestigationSnapshot, createInvestigationSnapshot, forgetInvestigationSource, hasUnresolvedKnownSources, readInvestigationSnapshot, restoreInvestigation, serializeRecordFilters, shouldSelectAddedSource, writeInvestigationSnapshot } from './investigation';
import type { Analysis, BreakdownDim, BreakdownFilter, BreakdownRow, Comparison, ConnectionInput, ConnectionTestResult, Info, Page, Query, RecentConnection, RecordDetail, RecordFilters, RequestRecord, Rollup, Source, SourceInput, TLSMode } from './types';
import { applyDrill, hasRecordFilter, mergeRecordFilter, recordFilterChips } from './breakdown';
import type { RunningImport } from './cases';
import { theme } from './theme.svelte';
import { timeZone } from './timeZone.svelte';
import { applyChipPatch, breadcrumb, depthOf, evidenceTabs, filterChips, moreFiltersCount, type FilterChip } from './depth';
import { autoRunDecision, createCoalescer, landingMayAutoRun, scopeHint, scopeProblem } from './autorun';
import type { Lens } from './pulse';
import { GLOSSARY_EVENT, glossaryTermFrom } from './copy/hintState';
import { plainTypicalVsSlowest } from './copy/plainSummary';
import { applyStaleFilters, scopeStaleDrill } from './incomplete';
import { allOperationsDrill, operationDrill, type OverviewDrill } from './overview';
import { phaseSegments } from './phases';
import { rollupFor } from './traffic';
import { createViewHistory, describeView, parseViewHash, type View } from './wayfinding';
import { hourWindowQuery, type RollupWindow } from './rollup';

let info = $state<Info | null>(null);
let sources = $state<Source[]>([]);
let query = $state<Query>(defaultQuery(new Date(), []));
let dateInputsValid = $state(true);
let zone = $state(Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC');
let targetMs = $state<number | null>(null);
let restoredInTab = $state(false);
let controlsResetKey = $state(0);
let investigationNotice = $state('Changes stay in this tab until it closes.');
let investigationReady = false;
let investigationFingerprint = '';
let pendingInvestigation = null as ReturnType<typeof readInvestigationSnapshot>;
let investigationIntent = null as ReturnType<typeof readInvestigationSnapshot>;
let analysis = $state<Analysis | null>(null);
let selectedKey = $state('');
// A quality drill-down pages the whole comparison (no API group) narrowed to one state.
let requestState = $state('');
let queryWide = $state(false);
// An overview tile drill-down narrows the same request list to one kind (and status class) of this comparison.
let drillOverview = $state<OverviewDrill | null>(null);
let contextAnchor = $state<RequestRecord | null>(null);
let timelineKeys = $state<string[]>([]);
let requestPage = $state<Page | null>(null);
let requestRows = $state<RequestRecord[]>([]);
let requestPageNumber = $state(1);
let drill = $state<RecordFilters>({});
let record = $state<RecordDetail | null>(null);
let loading = $state(false);
let requestLoading = $state(false);
let recordLoadingId = $state('');
let sourceBusy = $state(false);
let busyDelete = $state('');
let error = $state('');
let sourceError = $state('');
let sourceSuccess = $state('');
let requestError = $state('');
let recordError = $state('');
let initError = $state('');
let initCanRetry = $state(true);
let addLabel = $state('');
let addUri = $state('');
let addUsername = $state('');
let addPassword = $state('');
let addAuthSource = $state('');
let addTLSMode = $state<TLSMode>('system');
let addCACertificate = $state('');
let caCertificateName = $state('');
let caCertificateInput = $state<HTMLInputElement | undefined>(undefined);
let savedConnectionId = $state('');
let savedCredentialAvailable = $state(false);
let savedCredentialSessionOnly = $state(false);
let sessionCredentialIds = new Set<string>();
let rememberPassword = $state(false);
let recentConnections = $state<RecentConnection[]>([]);
let recentHistoryError = $state('');
let recentSaveError = $state('');
let recentSaveSuccess = $state('');
let recentSaveErrorStage = $state('');
let connectionErrorStage = $state('');
let connectionErrorContext = $state('');
let recentBusyId = $state('');
let historyMutationBusy = $state(false);
let forgetInProgress = $state(false);
let acceptedRecentSignature = '';
let manualDatabaseName = $state('');
let selectedDatabases = $state<string[]>([]);
let addCollection = $state('ApiAuditLog');
let discoveredDatabases = $state<string[]>([]);
let completedDatabases = $state<string[]>([]);
let connectionWarning = $state('');
let connectionError = $state('');
let connectionTestedSignature = $state('');
let connectionTesting = $state(false);
let manualDatabase = $state(false);
let labelDatabasesIndividually = $state(false);
let connectionController: AbortController | null = null;
let connectionSequence = 0;
let connectionObservedSignature = '';
let analysisController: AbortController | null = null;
let requestController: AbortController | null = null;
let recordController: AbortController | null = null;
let analysisSequence = 0;
let requestSequence = 0;
let recordSequence = 0;
let querySnapshot = '';
let connectionsOpen = $state(false);
let glossaryOpen = $state(false);
// P2-lite drawers: incident patterns and the hypothesis ledger.
let sidePanel = $state<'patterns' | 'ledger' | 'export' | 'import' | null>(null);
let sidePanelReturnFocus: HTMLElement | null = null;
// The file import the person started that is still running on the server. It lives here, not in the import drawer, so closing and reopening the drawer finds it again.
let runningImport = $state.raw<RunningImport | null>(null);
let glossaryTerm = $state<string | null>(null);
let glossaryReturnFocus: HTMLElement | null = null;
let connectionTab = $state<'saved' | 'new'>('saved');
let drawerElement = $state<HTMLElement | undefined>(undefined);
let drawerReturnFocus: HTMLElement | null = null;
// One source's data coverage, in its own drawer (opened from the Source menu).
let coverageSource = $state<{ id: string; label: string } | null>(null);
let coverageReturnFocus: HTMLElement | null = null;
// Progressive depth. D0 pulse; D1 ranked "where is it"; D2 evidence for an operation or a list; D3 one record.
let whereOpen = $state(false);
let lens = $state<Lens>('requests');
let compare = $state(false);
let operation = $state<Rollup | null>(null);
let inspectorTab = $state('timeline');
let splitDim = $state<BreakdownDim | ''>('');
let autoRan = false;
// The scope fingerprint of the run in flight; '' when none. Scope changes coalesce into one automatic run through scopeRun.
let inflightSignature = '';
const scopeRun = createCoalescer(() => flushScope());
// Where the reader was one level up (scroll positions and the control used), so stepping back lands in the same place.
interface Place { stage: number; page: number; body: number; opener: HTMLElement | null }
let evidencePlace: Place | null = null;
let recordPlace: Place | null = null;
// The address bar follows the screen (see lib/wayfinding.ts). viewReady turns on once a link's view has been rebuilt or ruled out.
let viewReady = $state(false);
let applyingView = false;
let viewRun = 0;
let initialView: View | null = null;
let viewHistory: ReturnType<typeof createViewHistory> | null = null;
let drawerHistory: ReturnType<typeof createDrawerHistory> | null = null;
let viewSyncQueued = false;

const connectionSignature = $derived(JSON.stringify([addUri, addUsername, addPassword, addAuthSource, addTLSMode, addCACertificate, savedConnectionId]));
const maxSources = $derived(info?.limits.maxSources ?? 8);
const remainingSourceSlots = $derived(Math.max(0, maxSources - sources.length));
const availableDatabases = $derived(discoveredDatabases.filter(database => !completedDatabases.includes(database)));

const selected = $derived(analysis?.apis.find(row => row.key === selectedKey) ?? null);
const sourceLabels = $derived(new Map(sources.map(source => [source.id, `${source.connectionLabel || source.label} / ${source.database}.${source.collection}`])));
const connectionGroups = $derived.by(() => {
  const groups = new Map<string, { id: string; label: string; sources: Source[] }>();
  for (const source of sources) {
    const id = source.connectionId || source.id;
    const group = groups.get(id) ?? { id, label: source.connectionLabel || source.label, sources: [] };
    group.sources.push(source);
    groups.set(id, group);
  }
  return [...groups.values()];
});
const failingSources = $derived((analysis?.sources ?? []).filter(source => !source.ok));
const querySignature = $derived(queryFingerprint(query));
const displayedRunIsStale = $derived(analysis != null && queryFingerprint(analysis.query) !== querySignature);
const calculationNotes = $derived(resultNotes(analysis?.apis ?? []));
const drillActive = $derived(hasRecordFilter(drill));
const drillChips = $derived(recordFilterChips(drill));
const breakdownQuery = $derived<Query | null>(analysis ? applyDrill(analysis.query, drill) : null);
const targetAssessment = $derived.by(() => {
  if (targetMs == null || !Number.isFinite(targetMs) || targetMs < 0 || !selected) return '';
  const p95 = selected.current.p95;
  if (!p95) return 'No current P95 estimate is available for this API group.';
  if (p95.upperMs != null && p95.upperMs <= targetMs) return 'The current P95 estimate is at or below your target.';
  if (p95.lowerMs > targetMs) return 'The current P95 estimate is above your target.';
  return 'The estimated P95 range spans your target, so we cannot tell which side it falls on.';
});

// ---- Progressive depth (see lib/depth.ts) ----
const firstRun = $derived(info != null && !sources.length && !info.demo);
const hasContext = $derived(operation != null || queryWide || drillActive || !!selectedKey);
const nav = $derived({ whereOpen, hasContext, recordOpen: record != null });
const depth = $derived(depthOf(nav));
const operationGroups = $derived(operation ? operationRows(operation) : []);
const evidenceName = $derived(operation ? operation.controller + '.' + operation.action : selected ? selected.controller + '.' + selected.action : drillOverview ? drillOverview.label : queryWide ? stateLabel(requestState) : drillActive ? 'Filtered requests' : 'Operation');
// No operation and no group: the evidence is comparison-wide (all operations, an overview or quality drill, a breakdown filter).
const drillOperation = $derived(drillOverview?.controller !== undefined && drillOverview.action !== undefined ? drillOverview : null);
const comparisonWide = $derived(hasContext && !operation && !selected && !drillOperation);
const runHint = $derived(scopeHint(query, dateInputsValid));
// Slowest follows the operation that is open, or the one a drill (a table row's counts, the stale list) narrowed to; otherwise it is comparison-wide.
const slowScope = $derived(operation && analysis
  ? { ...operationDrill(operation, analysis.query.kind), label: operation.controller + '.' + operation.action + ' · ' + kindLabel(operation.kind ?? 'unknown') }
  : drillOperation ? { kind: drillOperation.kind, controller: drillOperation.controller, action: drillOperation.action, label: drillOperation.controller + '.' + drillOperation.action + (drillOperation.kind === 'all' ? '' : ' · ' + kindLabel(drillOperation.kind === 'http_unknown' ? 'unknown' : drillOperation.kind)) } : null);
const crumbs = $derived(breadcrumb(nav, { evidence: evidenceName }));
const filterChipList = $derived(filterChips(query));
const moreCount = $derived(moreFiltersCount(query));
const scopedQuery = $derived<Query | null>(breakdownQuery && operation ? operationQuery(breakdownQuery, operation) : breakdownQuery);
const selectedStats = $derived(selected?.current);
// The typical and slowest times of the open group in plain words, with the earlier period as the comparison.
const selectedPlain = $derived(selected && analysis ? plainTypicalVsSlowest({
  comparison: selected, metric: analysis.query.metric,
  window: { start: analysis.query.start, end: analysis.query.end, zone: timeZone.zone }, referenceWindow: { start: analysis.query.baselineStart, end: analysis.query.baselineEnd, zone: timeZone.zone }
}) : null);
const inspectorTabs = $derived(evidenceTabs({
  operation: operation != null, group: selected != null, scoped: scopedQuery != null,
  phases: !!(selected && (phaseSegments(selectedStats?.phases) || (selectedStats?.phases?.payloadMs != null && Number.isFinite(selectedStats.phases.payloadMs)))),
  failed: (operation?.failures ?? 0) + (selectedStats?.clientErrors ?? 0) + (selectedStats?.serverErrors ?? 0),
  unfinished: (operation?.incomplete ?? 0) + (operation?.pending ?? 0) + (operation?.invalid ?? 0) + (selectedStats?.incomplete ?? 0) + (selectedStats?.pending ?? 0) + (selectedStats?.invalid ?? 0)
}));
const activeTab = $derived(inspectorTabs.find(tab => tab.id === inspectorTab)?.id ?? inspectorTabs[0]?.id ?? '');
const openKey = $derived(operation ? operation.controller + '\u0000' + operation.action + '\u0000' + (operation.kind ?? '') : '');
const failureCohort = $derived<string[] | null>(operation ? operationGroups.map(group => group.key) : selected ? [selected.key] : null);
// What the address bar says about the screen (see describeView in lib/wayfinding.ts). null is the first screen. Every level has an address, so Back climbs one level at a time.
const currentView = $derived(describeView({
  depth, whereOpen, lens, compare, tab: activeTab,
  operation: operation ? { controller: operation.controller, action: operation.action, kind: operation.kind } : null,
  allOperations: operation == null && queryWide && drillOverview?.label === 'All operations',
  record: record ? { sourceId: record.sourceId, id: record.id } : null
}));

// Registers the effects and the mount work in the order App.svelte declared them. Call once, from App.svelte's script.
export function startApp() {
  initialView = typeof location === 'undefined' ? null : parseViewHash(location.hash);
  $effect(() => {
    const signature = connectionSignature;
    if (connectionObservedSignature && connectionObservedSignature !== signature) {
      const wasLoadedRecent = acceptedRecentSignature === signature;
      acceptedRecentSignature = '';
      if (!wasLoadedRecent && savedConnectionId) {
        savedConnectionId = '';
        savedCredentialAvailable = false;
        savedCredentialSessionOnly = false;
        rememberPassword = false;
      }
      connectionSequence++;
      connectionController?.abort();
      connectionController = null;
      connectionTesting = false;
      discoveredDatabases = [];
      completedDatabases = [];
      selectedDatabases = [];
      manualDatabaseName = '';
      labelDatabasesIndividually = false;
      connectionWarning = '';
      connectionError = '';
      connectionErrorStage = '';
      connectionErrorContext = '';
      connectionTestedSignature = '';
      sourceError = '';
      sourceSuccess = '';
      recentSaveError = '';
      recentSaveSuccess = '';
      recentSaveErrorStage = '';
      connectionErrorStage = '';
    }
    connectionObservedSignature = signature;
  });

  onMount(() => {
    theme.start(); void initialize();
    window.addEventListener(GLOSSARY_EVENT, onGlossaryRequest);
    drawerHistory = createDrawerHistory(history, closeDrawerById);
    viewHistory = createViewHistory(location, history);
    // Browser Back closes an open drawer first; only an event no drawer used is a move between views.
    const onPop = () => { if (drawerHistory?.pop()) return; const target = viewHistory?.pop(); if (target !== undefined) void applyView(target); };
    window.addEventListener('popstate', onPop);
    return () => { window.removeEventListener(GLOSSARY_EVENT, onGlossaryRequest); window.removeEventListener('popstate', onPop); viewHistory = null; drawerHistory = null; };
  });

  $effect(() => {
    const current = querySignature;
    if (querySnapshot && querySnapshot !== current) {
      analysisSequence++;
      analysisController?.abort();
      analysisController = null;
      inflightSignature = '';
      loading = false; error = '';
      invalidateDetails();
      operation = null; selectedKey = ''; contextAnchor = null;
    }
    querySnapshot = current;
  });

  // The landing run: sources are chosen and the dates are valid, so the pulse is ready without a button press. Only a primary window of at most
  // 24 hours starts by itself; a wider one (a restored 7-day snapshot) waits for the Run analysis button. Later scope changes run through onScopeChange.
  $effect(() => {
    if (autoRan || !info || !sources.length || !query.sourceIds.length || !dateInputsValid || !investigationReady) return;
    autoRan = true;
    if (!landingMayAutoRun(query)) return;
    untrack(() => { void runAnalysis(true); });
  });

  $effect(() => {
    const fingerprint = JSON.stringify([query, zone, targetMs, sources]);
    if (!investigationReady || fingerprint === investigationFingerprint) return;
    investigationFingerprint = fingerprint;
    const remembered = writeInvestigationSnapshot(undefined, createInvestigationSnapshot(query, sources, zone, targetMs, investigationIntent), info?.demo ? 'demo' : 'live');
    investigationNotice = remembered
      ? 'Remembered in this tab. Changes stay until this tab closes.'
      : 'Could not remember these edits in this tab. The explorer remains usable.';
  });

  // A link that carries a view (a reload or a shared address): rebuild it once the comparison is on screen, or give up when it cannot come.
  $effect(() => {
    const ready = viewReady, shown = analysis, failed = error || initError || firstRun;
    if (ready) return;
    if (!initialView) { viewReady = true; return; }
    if (shown) { const wanted = initialView; initialView = null; untrack(() => { void applyView(wanted).then(() => { viewReady = true; }); }); }
    else if (failed) { initialView = null; viewReady = true; }
  });

  // The address bar follows the screen: going deeper adds a history step, so the browser's Back (and Esc) climb back up one level at a time.
  // A view change waits until no drawer entry is on the history, so it never lands on top of one.
  $effect(() => {
    const view = currentView, ready = viewReady;
    if (!ready || applyingView) return;
    untrack(() => {
      if (!drawerHistory || !viewHistory) { viewHistory?.sync(view); return; }
      if (viewSyncQueued) return; // the queued call reads the view as it is when it runs
      viewSyncQueued = true;
      drawerHistory.whenClear(() => { viewSyncQueued = false; if (viewReady && !applyingView) viewHistory?.sync(currentView); });
    });
  });

  // Each open drawer shares one history entry, so the browser's Back closes it. Closing from the page steps back over that entry.
  $effect(() => {
    const open = [connectionsOpen ? 'connections' : '', glossaryOpen ? 'glossary' : '', coverageSource ? 'coverage' : '', sidePanel ? 'side' : ''].filter(Boolean);
    untrack(() => { drawerHistory?.sync(open); });
  });
}

export async function initialize() {
  const hadToken = hasSessionToken();
  initCanRetry = true;
  try {
    info = await api.info();
    pendingInvestigation = readInvestigationSnapshot(undefined, info.demo ? 'demo' : 'live');
    const loaded = (await api.sources()) ?? [];
    sources = loaded;
    if (pendingInvestigation) {
      investigationIntent = pendingInvestigation;
      const restored = restoreInvestigation(pendingInvestigation, loaded);
      const { zone: restoredZone, targetMs: restoredTargetMs, ...restoredQuery } = restored;
      query = restoredQuery;
      zone = restoredZone;
      targetMs = restoredTargetMs;
      restoredInTab = true;
    } else query = defaultQuery(new Date(info.now), loaded.map(source => source.id));
    if (!hasUnresolvedKnownSources(investigationIntent, loaded)) investigationIntent = null;
    investigationFingerprint = JSON.stringify([query, zone, targetMs, sources]);
    investigationReady = true;
    pendingInvestigation = null;
    setLocalInputs();
    if (!info.demo) {
      try {
        const recents = await api.recentConnections();
        recentConnections = Array.isArray(recents) ? recents.filter(isRecentConnection).slice(0, 20) : [];
      } catch {
        recentHistoryError = 'Recent connections could not be loaded. You can still test and add a connection.';
      }
    }
  } catch (cause) {
    const text = message(cause);
    const failure = classifyStartupFailure({ hadToken, cause });
    initError = failure.kind === 'other' ? text : failure.message;
    initCanRetry = failure.canRetry;
  }
}
function setLocalInputs() {
}
// A 401 means the session was forgotten: the evidence memo must not serve anything shown under it.
function message(cause: unknown) { if (cause instanceof ApiError && cause.status === 401) bumpEpoch(); return cause instanceof Error ? cause.message : 'The request could not be completed.'; }
function friendlyConnectionStage(cause: unknown) {
  const stage = cause && typeof cause === 'object' && 'stage' in cause ? String((cause as { stage?: unknown }).stage ?? '') : '';
  return ({ validation: 'Settings', connection: 'Network', tls: 'TLS', authentication: 'Sign-in', discovery: 'Database list' } as Record<string, string>)[stage] ?? '';
}
function safeConnectionMessage(cause: unknown, credentials: ConnectionInput) {
  let text = message(cause);
  for (const secret of [credentials.uri, credentials.username, credentials.password, credentials.caCertificate, credentials.savedConnectionId ?? '']) {
    if (secret) text = text.split(secret).join('[redacted]');
  }
  return text.replace(/mongodb(?:\+srv)?:\/\/\S+/gi, '[connection address redacted]');
}
function cloneQuery(): Query { return JSON.parse(JSON.stringify(query)) as Query; }
function connectionInput(): ConnectionInput {
  return {
    uri: addUri, username: addUsername, password: addPassword, authSource: addAuthSource,
    tlsMode: addTLSMode, caCertificate: addTLSMode === 'custom' ? addCACertificate : '',
    ...(savedConnectionId ? { savedConnectionId } : {})
  };
}
function isRecentConnection(value: unknown): value is RecentConnection {
  if (!value || typeof value !== 'object') return false;
  const item = value as Partial<RecentConnection>;
  return typeof item.id === 'string' && typeof item.label === 'string' && typeof item.uri === 'string' && typeof item.hasCredentials === 'boolean';
}
export function redactedAddress(uri: string) {
  return uri.replace(/(mongodb(?:\+srv)?:\/\/)[^/@]*@/gi, '$1[credentials]@');
}
export function displayLastUsed(value: string) {
  const time = new Date(value);
  return Number.isFinite(time.getTime()) ? new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(time) : 'Recent';
}
export function loadRecent(recent: RecentConnection) {
  if (sourceBusy || connectionTesting || historyMutationBusy) return;
  connectionSequence++;
  connectionController?.abort(); connectionController = null; connectionTesting = false;
  addLabel = recent.label;
  addUri = recent.uri;
  addUsername = recent.username;
  addPassword = '';
  addAuthSource = recent.authSource;
  addTLSMode = recent.tlsMode || 'uri';
  addCACertificate = recent.caCertificate || '';
  caCertificateName = recent.caCertificate ? 'Saved certificate authority' : '';
  if (caCertificateInput) caCertificateInput.value = '';
  addCollection = recent.collection || 'ApiAuditLog';
  savedConnectionId = recent.id;
  savedCredentialSessionOnly = !recent.hasCredentials && sessionCredentialIds.has(recent.id);
  savedCredentialAvailable = recent.hasCredentials || savedCredentialSessionOnly;
  rememberPassword = recent.hasCredentials;
  acceptedRecentSignature = JSON.stringify([addUri, addUsername, addPassword, addAuthSource, addTLSMode, addCACertificate, savedConnectionId]);
  discoveredDatabases = []; completedDatabases = []; selectedDatabases = []; manualDatabaseName = '';
  manualDatabase = false; connectionTestedSignature = ''; connectionWarning = ''; connectionError = '';
  recentSaveError = ''; recentSaveSuccess = ''; recentSaveErrorStage = ''; sourceError = ''; sourceSuccess = '';
}
export async function forgetRecent(recent: RecentConnection) {
  if (connectionTesting || sourceBusy || historyMutationBusy) return;
  recentHistoryError = '';
  recentBusyId = recent.id;
  forgetInProgress = true;
  historyMutationBusy = true;
  try {
    await api.forgetConnection(recent.id);
    sessionCredentialIds.delete(recent.id);
    recentConnections = recentConnections.filter(item => item.id !== recent.id);
    if (savedConnectionId === recent.id) {
      savedConnectionId = ''; savedCredentialAvailable = false; savedCredentialSessionOnly = false; rememberPassword = false; addPassword = '';
      connectionTestedSignature = ''; discoveredDatabases = []; completedDatabases = []; selectedDatabases = [];
      connectionWarning = ''; connectionError = '';
    }
  } catch (cause) {
    recentHistoryError = `Could not remove this recent connection: ${message(cause)}`;
  } finally { recentBusyId = ''; forgetInProgress = false; historyMutationBusy = false; }
}
async function rememberConnection(connection: ConnectionInput, label: string, collection: string, keepPassword: boolean, mode: 'tested' | 'manual'): Promise<boolean> {
  if (forgetInProgress || historyMutationBusy || sourceBusy) return false;
  recentSaveError = '';
  recentSaveSuccess = '';
  recentSaveErrorStage = '';
  historyMutationBusy = true;
  try {
    const saved = await api.rememberConnection({
      label: label.trim() || 'MongoDB connection',
      connection,
      collection, rememberPassword: keepPassword
    });
    if (!isRecentConnection(saved)) throw new Error('The server returned an invalid recent connection record.');
    recentConnections = [saved, ...recentConnections.filter(item => item.id !== saved.id)].slice(0, 20);
    if (connection.savedConnectionId) {
      if (keepPassword) {
        sessionCredentialIds.delete(saved.id);
      } else {
        sessionCredentialIds.add(saved.id);
      }
      if (savedConnectionId === connection.savedConnectionId) {
        savedCredentialAvailable = keepPassword ? saved.hasCredentials : true;
        savedCredentialSessionOnly = !keepPassword;
      }
    }
    if (mode === 'manual') {
      recentSaveSuccess = 'Connection details saved to Recents. This save does not test reachability or add a source profile.';
      if (!addLabel.trim()) addLabel = label;
    }
    return true;
  } catch (cause) {
    recentSaveError = mode === 'tested'
      ? `Connected, but this connection could not be saved to Recents: ${safeConnectionMessage(cause, connection)}`
      : `Saving failed: ${safeConnectionMessage(cause, connection)}`;
    recentSaveErrorStage = friendlyConnectionStage(cause);
    return false;
  } finally { historyMutationBusy = false; }
}
function suggestedConnectionLabel(uri: string) {
  const address = uri.trim();
  try {
    const parsed = new URL(address.includes('://') ? address : `mongodb://${address}`);
    if (parsed.hostname) return parsed.hostname;
  } catch { /* backend validation gives the address error */ }
  return address.split(/[/?#]/, 1)[0] || 'MongoDB connection';
}
export async function saveConnectionDetails() {
  if (forgetInProgress || historyMutationBusy || connectionTesting || sourceBusy) return;
  recentSaveError = ''; recentSaveSuccess = ''; recentSaveErrorStage = ''; connectionError = ''; connectionErrorStage = '';
  const connection = connectionInput();
  if (!connection.uri.trim()) {
    connectionError = 'Enter a server address before saving connection details.';
    connectionErrorStage = 'Settings';
    return;
  }
  if (!addCollection.trim()) {
    connectionError = 'Enter the audit log collection before saving connection details.';
    connectionErrorStage = 'Settings';
    return;
  }
  if (addTLSMode === 'custom' && !addCACertificate) {
    connectionError = 'Choose a PEM certificate authority file before saving this connection.';
    connectionErrorStage = 'Settings';
    return;
  }
  const label = addLabel.trim() || suggestedConnectionLabel(connection.uri);
  await rememberConnection(connection, label, addCollection, rememberPassword, 'manual');
}
export async function loadCACertificate(event: Event) {
  const input = event.currentTarget as HTMLInputElement;
  const file = input.files?.[0];
  if (!file) return;
  if (file.size > 256 * 1024) {
    connectionError = 'The certificate file must be 256 KiB or smaller.'; input.value = ''; return;
  }
  try {
    const contents = await file.text();
    if (!/-----BEGIN CERTIFICATE-----[\s\S]+?-----END CERTIFICATE-----/.test(contents) || /-----BEGIN [^-]*PRIVATE KEY-----/.test(contents)) {
      connectionError = 'Choose a PEM certificate authority file containing a CERTIFICATE block. Private keys are not accepted.';
      input.value = ''; return;
    }
    addCACertificate = contents;
    caCertificateName = file.name;
    connectionError = ''; connectionWarning = '';
  } catch {
    connectionError = 'The certificate file could not be read.'; input.value = '';
  }
}
export function clearCACertificate() {
  addCACertificate = ''; caCertificateName = '';
  if (caCertificateInput) caCertificateInput.value = '';
}
export async function testConnection() {
  if (forgetInProgress || historyMutationBusy || sourceBusy) return;
  connectionError = ''; connectionErrorStage = ''; connectionErrorContext = ''; connectionWarning = ''; discoveredDatabases = []; selectedDatabases = []; manualDatabaseName = ''; sourceError = ''; sourceSuccess = '';
  connectionTestedSignature = '';
  connectionController?.abort(); connectionController = new AbortController();
  const run = ++connectionSequence;
  const signature = connectionSignature;
  const connection = connectionInput();
  const label = addLabel;
  const collection = addCollection;
  const keepPassword = rememberPassword;
  if (addTLSMode === 'custom' && !addCACertificate) {
    connectionError = 'Choose a PEM certificate authority file before testing this connection.';
    connectionTesting = false; return;
  }
  connectionTesting = true;
  try {
    const result: ConnectionTestResult = await api.testConnection(connection, connectionController.signal);
    if (run !== connectionSequence || signature !== connectionSignature) return;
    discoveredDatabases = Array.isArray(result?.databases) ? result.databases.filter((item): item is string => typeof item === 'string' && item.length > 0) : [];
    connectionWarning = result?.warning ?? '';
    connectionTestedSignature = signature;
    if (!discoveredDatabases.length && !connectionWarning) connectionWarning = 'Connected, but no databases were returned. Enter a database manually if your account has restricted database discovery.';
    if (!info?.demo) await rememberConnection(connection, label.trim() || suggestedConnectionLabel(connection.uri), collection, keepPassword, 'tested');
  } catch (cause) {
    if (run === connectionSequence && !(cause instanceof DOMException && cause.name === 'AbortError')) {
      connectionError = safeConnectionMessage(cause, connection);
      connectionErrorStage = friendlyConnectionStage(cause);
      connectionErrorContext = 'Connection test failed';
    }
  } finally {
    if (run === connectionSequence) connectionTesting = false;
  }
}
function invalidateDetails() {
  requestController?.abort(); recordController?.abort(); recordSequence++;
  requestController = null; recordController = null;
  requestPage = null; requestRows = []; record = null; drill = {};
  requestPageNumber = 1; requestState = ''; queryWide = false; drillOverview = null;
  requestError = ''; recordError = ''; recordLoadingId = '';
}

export function setDatabaseSelected(database: string, checked: boolean) {
  sourceError = ''; sourceSuccess = '';
  if (checked) {
    if (selectedDatabases.includes(database)) return;
    if (selectedDatabases.length >= remainingSourceSlots) {
      sourceError = `Only ${remainingSourceSlots} source profile slot${remainingSourceSlots === 1 ? '' : 's'} remain.`;
      return;
    }
    selectedDatabases = [...selectedDatabases, database];
  } else selectedDatabases = selectedDatabases.filter(item => item !== database);
}

export function selectAllDatabases() {
  sourceError = ''; sourceSuccess = '';
  selectedDatabases = availableDatabases.slice(0, remainingSourceSlots);
  if (availableDatabases.length > remainingSourceSlots) {
    sourceSuccess = `Selected the first ${remainingSourceSlots} database${remainingSourceSlots === 1 ? '' : 's'} to fit the remaining profile capacity. The other databases remain available to add later.`;
  }
}

export function clearConnectionDetails() {
  connectionSequence++;
  connectionController?.abort(); connectionController = null; connectionTesting = false;
  addUri = ''; addUsername = ''; addPassword = ''; addAuthSource = '';
  addTLSMode = 'system'; clearCACertificate(); savedConnectionId = ''; savedCredentialAvailable = false; savedCredentialSessionOnly = false; rememberPassword = false;
  discoveredDatabases = []; completedDatabases = []; selectedDatabases = []; manualDatabaseName = '';
  labelDatabasesIndividually = false;
  connectionWarning = ''; connectionError = ''; connectionTestedSignature = '';
  manualDatabase = false; sourceError = ''; sourceSuccess = '';
}
export function resetInvestigation() {
  scopeRun.cancel();
  analysisSequence++;
  analysisController?.abort(); analysisController = null; inflightSignature = ''; loading = false;
  invalidateDetails();
  analysis = null; selectedKey = ''; error = ''; operation = null; whereOpen = false; contextAnchor = null;
  query = defaultQuery(new Date(), sources.map(source => source.id));
  investigationIntent = null;
  zone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
  targetMs = null; dateInputsValid = true; restoredInTab = false;
  controlsResetKey++;
  investigationNotice = 'Investigation reset. Defaults are ready.';
  clearInvestigationSnapshot(undefined, info?.demo ? 'demo' : 'live');
  investigationFingerprint = JSON.stringify([query, zone, targetMs, sources]);
}

export async function addSource(event: SubmitEvent) {
  event.preventDefault();
  // A second submit while one add is running (a double click, Enter held down) must not add every database twice.
  if (sourceBusy) return;
  sourceError = ''; sourceSuccess = '';
  if (historyMutationBusy) { sourceError = 'Wait for the recent connection update to finish before adding a profile.'; return; }
  const databaseNames = manualDatabase ? [manualDatabaseName.trim()].filter(Boolean) : selectedDatabases.slice();
  if (!addLabel.trim() || !addUri.trim() || !databaseNames.length || !addCollection.trim()) {
    sourceError = 'Enter a profile label and collection, then select or enter at least one database.'; return;
  }
  if (!manualDatabase && connectionTestedSignature !== connectionSignature) {
    sourceError = 'Test this connection before adding a discovered database, or choose manual database entry.'; return;
  }
  if (databaseNames.length > remainingSourceSlots) {
    sourceError = `Only ${remainingSourceSlots} source profile slot${remainingSourceSlots === 1 ? '' : 's'} remain. Reduce the database selection before adding.`; return;
  }
  sourceBusy = true;
  if (!manualDatabase && databaseNames.length > 1) labelDatabasesIndividually = true;
  const connection = connectionInput();
  const labelPrefix = addLabel.trim();
  const collection = addCollection.trim();
  const failures: Array<{ database: string; error: string }> = [];
  let addedCount = 0;
  let allAdded = false;
  try {
    for (const database of databaseNames) {
      try {
        const label = !manualDatabase && (databaseNames.length > 1 || labelDatabasesIndividually) ? `${labelPrefix} / ${database}`.slice(0, 100) : labelPrefix;
        const input: SourceInput = { ...connection, label, connectionLabel: labelPrefix, database, collection };
        const added = await api.addSource(input);
        const selectAdded = shouldSelectAddedSource(added, investigationIntent);
        sources = [...sources, added];
        if (selectAdded) query.sourceIds = [...query.sourceIds, added.id];
        if (!hasUnresolvedKnownSources(investigationIntent, sources)) investigationIntent = null;
        completedDatabases = [...completedDatabases, database];
        addedCount++;
      } catch (cause) { failures.push({ database, error: safeConnectionMessage(cause, connection) }); }
    }
    if (manualDatabase) {
      if (!failures.length) manualDatabaseName = '';
    } else selectedDatabases = failures.map(item => item.database);
    if (failures.length) {
      const details = failures.map(item => `${item.database}: ${item.error}`).join(' · ');
      sourceError = `Added ${addedCount} of ${databaseNames.length} selected database${databaseNames.length === 1 ? '' : 's'}. Failed databases remain selected so you can retry: ${details}`;
    } else {
      labelDatabasesIndividually = false;
      sourceSuccess = `Added ${addedCount} source profile${addedCount === 1 ? '' : 's'}. Reopen New connection to add more databases from this host.`;
      allAdded = true;
    }
  }
  finally { sourceBusy = false; }
  // A fully successful add is done: close the drawer and show the result in the source list. A partial failure stays open so the failed databases can be retried.
  if (allAdded) await closeConnections();
}
export async function deleteSource(source: Source) {
  sourceError = ''; busyDelete = source.id;
  try {
    await api.deleteSource(source.id);
    investigationIntent = forgetInvestigationSource(investigationIntent, source);
    sources = sources.filter(item => item.id !== source.id);
    if (!hasUnresolvedKnownSources(investigationIntent, sources)) investigationIntent = null;
    query.sourceIds = query.sourceIds.filter(id => id !== source.id);
    analysis = analysis ? { ...analysis, sources: analysis.sources.filter(result => result.sourceId !== source.id), apis: analysis.apis.filter(row => row.sourceId !== source.id) } : null;
    selectedKey = ''; operation = null; contextAnchor = null;
    invalidateDetails(); bumpEpoch();
  } catch (cause) { sourceError = message(cause); }
  finally { busyDelete = ''; }
}
// A manual run (the button) reports a problem in the error banner and ignores clicks while loading. An automatic run (a scope change)
// never gets here with a problem, replaces an in-flight run, and leaves the banner alone.
export async function runAnalysis(auto = false) {
  if (loading && !auto) return;
  if (!auto) error = '';
  const problem = scopeProblem(query, dateInputsValid);
  if (problem) { if (!auto) error = problem; return; }
  if (auto) error = '';
  scopeRun.cancel();
  analysisController?.abort(); analysisController = new AbortController();
  const run = ++analysisSequence;
  inflightSignature = queryFingerprint(query);
  // The previous analysis stays on screen (dimmed) while the new one loads, and nothing is auto-selected afterwards: the pulse is the landing view.
  loading = true; invalidateDetails(); bumpEpoch(); selectedKey = ''; operation = null; contextAnchor = null;
  try {
    const result = await api.analyze(cloneQuery(), analysisController.signal);
    if (run !== analysisSequence) return;
    bumpEpoch(); // the old analysis stays on screen while this runs, so anything read meanwhile belongs to it, not to this result
    analysis = result;
    timelineKeys = rankComparisons(result.apis).filter(row => row.state === 'compared').slice(0, 1).map(row => row.key); // only the top group starts checked: three overlapping lines were unreadable
  } catch (cause) {
    if (run === analysisSequence && !(cause instanceof DOMException && cause.name === 'AbortError')) {
      const detail = message(cause);
      error = /exceeds a configured limit/i.test(detail)
        ? 'This comparison reached a processing safety limit. Your settings are kept. Try shorter periods, fewer sources, or a controller or action filter.'
        : detail;
    }
  } finally { if (run === analysisSequence) { loading = false; inflightSignature = ''; } }
}
// Runs once the scope has been quiet for the debounce period: only a valid scope that is not already shown or in flight.
function flushScope() {
  // A skipped scope (invalid or unchanged) never leaves an old "Comparison not run" banner behind; the hint explains what is missing.
  error = '';
  const decision = autoRunDecision({
    problem: scopeProblem(query, dateInputsValid), signature: queryFingerprint(query),
    shown: analysis ? queryFingerprint(analysis.query) : null, inflight: inflightSignature || null
  });
  if (decision === 'run') void runAnalysis(true);
}
export function cancelAnalysis() { scopeRun.cancel(); analysisSequence++; analysisController?.abort(); inflightSignature = ''; loading = false; }
export async function closeConnections() {
  connectionsOpen = false;
  await tick();
  drawerReturnFocus?.focus();
  drawerReturnFocus = null;
}
export async function closeGlossary() {
  glossaryOpen = false;
  glossaryTerm = null;
  await tick();
  glossaryReturnFocus?.focus();
  glossaryReturnFocus = null;
}
export async function closeCoverage() {
  coverageSource = null;
  await tick();
  coverageReturnFocus?.focus();
  coverageReturnFocus = null;
}
export function openCoverage(source: { id: string; label: string }) {
  coverageReturnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  coverageSource = source;
}
export async function closeSidePanel() {
  sidePanel = null;
  await tick();
  sidePanelReturnFocus?.focus();
  sidePanelReturnFocus = null;
}
export function openSidePanel(which: 'patterns' | 'ledger' | 'export' | 'import') {
  sidePanelReturnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  sidePanel = which;
}
export function openGlossary(termId: string | null = null) {
  glossaryReturnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  glossaryTerm = termId;
  glossaryOpen = true;
}
// A hint's "Learn more in the glossary" button sends this window event.
function onGlossaryRequest(event: Event) {
  const termId = glossaryTermFrom((event as CustomEvent).detail);
  if (termId && !glossaryOpen) openGlossary(termId);
}
export async function openConnections(tab: 'saved' | 'new' = 'saved') {
  if (sourceBusy || connectionTesting || historyMutationBusy) return;
  drawerReturnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  connectionTab = tab;
  connectionsOpen = true;
  await tick();
  drawerElement?.querySelector<HTMLElement>('button, input, select, [tabindex="0"]')?.focus();
}
export async function startNewConnection() {
  if (sourceBusy || connectionTesting || historyMutationBusy) return;
  if (!connectionsOpen) drawerReturnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  clearConnectionDetails();
  addLabel = '';
  addCollection = 'ApiAuditLog';
  connectionTab = 'new';
  connectionsOpen = true;
  await tick();
  drawerElement?.querySelector<HTMLElement>('input, select, button')?.focus();
}
async function selectAPI(key: string) {
  contextAnchor = null;
  const row = analysis?.apis.find(item => item.key === key);
  if (!row || !analysis) return;
  selectedKey = key; invalidateDetails();
  await loadRequests('', 1);
}
// An operation (controller + action + request type) is the unit D1 ranks. Its comparison groups are the source and outcome splits of it.
function operationRows(op: Pick<Rollup, 'controller' | 'action' | 'kind'>): Comparison[] {
  return (analysis?.apis ?? []).filter(row => row.controller === op.controller && row.action === op.action && (op.kind == null || row.kind === op.kind));
}
// The comparison's query scoped to one operation. Separate from drillQuery, which keeps its own overview-tile behaviour.
function operationQuery(source: Query, op: Pick<Rollup, 'controller' | 'action' | 'kind'>): Query {
  const copy = JSON.parse(JSON.stringify(source)) as Query;
  const scope = operationDrill(op, source.kind);
  copy.kind = scope.kind;
  if (scope.controller !== undefined) copy.controller = scope.controller;
  if (scope.action !== undefined) copy.action = scope.action;
  return copy;
}
function focusSoon(id: string) { void tick().then(() => document.getElementById(id)?.focus({ preventScroll: false })); }
// D2 for a ranked operation: its whole request list (every event of that controller, action and type), with the first comparison group driving the charts.
// The control that opened the evidence frame, kept so Esc or Back returns focus to it. A drill made from inside the open frame (a tab's own
// button) never replaces it: that button unmounts with the frame, so the opener is the only place focus can sensibly return to.
function rememberOpener() {
  const active = document.activeElement;
  if (!(active instanceof HTMLElement) || active.closest('.inspector')) return;
  evidencePlace = placeNow();
}
function placeNow(): Place {
  const active = document.activeElement;
  return {
    stage: document.querySelector('.stage')?.scrollTop ?? 0, page: window.scrollY, body: document.querySelector('.inspector-body')?.scrollTop ?? 0,
    opener: active instanceof HTMLElement && active !== document.body ? active : null
  };
}
// Puts the scroll positions back and focuses the control that was used, or the first visible heading named when it is gone or hidden. Hidden views reset their scroll, so this waits for the redraw.
async function restorePlace(place: Place | null, fallbackIds: string[]) {
  await tick();
  const shown = (element: HTMLElement | null | undefined): element is HTMLElement => !!element && element.isConnected && element.getClientRects().length > 0;
  const target = [place?.opener, ...fallbackIds.map(id => document.getElementById(id)), document.querySelector<HTMLElement>('.pulse-actions .primary-button')].find(shown);
  target?.focus({ preventScroll: true });
  if (!place) return;
  const stage = document.querySelector('.stage'), body = document.querySelector('.inspector-body');
  if (stage) stage.scrollTop = place.stage;
  if (body) body.scrollTop = place.body;
  window.scrollTo({ top: place.page });
}
export async function openOperation(row: Rollup, key = '') {
  if (!analysis) return;
  rememberOpener();
  contextAnchor = null; invalidateDetails(); selectedKey = '';
  operation = row; inspectorTab = 'timeline';
  queryWide = true; requestState = 'all'; drillOverview = operationDrill(row, analysis.query.kind);
  selectedKey = key || initialComparison(operationRows(row))?.key || '';
  await loadRequests('', 1);
  focusSoon('inspector-title');
}
// Stops a record that is still loading, so a late response cannot reopen the request view after the user stepped away.
// Going back to the evidence returns to the row that was opened, at the scroll position it was left.
function releaseRecord(restore: boolean) {
  recordSequence++; recordController?.abort(); recordController = null;
  const place = recordPlace; recordPlace = null;
  const wasOpen = record != null || recordLoadingId !== '';
  record = null; recordError = ''; recordLoadingId = '';
  if (restore && wasOpen) void restorePlace(place, ['inspector-title', 'where-title']);
}
export function dropRecord() { releaseRecord(true); }
export function closeInspector() {
  requestController?.abort(); requestSequence++;
  releaseRecord(false);
  selectedKey = ''; queryWide = false; requestState = ''; drillOverview = null; drill = {}; operation = null; contextAnchor = null;
  requestPage = null; requestRows = []; requestPageNumber = 1; requestError = ''; requestLoading = false;
  record = null; recordError = '';
  const place = evidencePlace; evidencePlace = null;
  void restorePlace(place, ['where-title']);
}
export function openWhere(next: Lens) {
  lens = next; whereOpen = true;
  focusSoon('where-title');
}
// One step toward Pulse: the record, then the operation or list, then the ranked view.
function stepUpOnce() {
  if (record != null || recordLoadingId) { dropRecord(); return; }
  if (hasContext) { closeInspector(); return; }
  if (whereOpen) { whereOpen = false; compare = false; splitDim = ''; }
}
export function goTo(target: number) {
  for (let guard = 0; guard < 4 && depth > target; guard++) stepUpOnce();
}
// ---- The address bar (see lib/wayfinding.ts) ----
async function applyView(view: View | null) {
  const run = ++viewRun;
  applyingView = true;
  try { await settleView(view, run); await tick(); }
  finally {
    if (run === viewRun) {
      applyingView = false;
      viewHistory?.sync(untrack(() => currentView), true);
    }
  }
}
// Rebuilds a view from an address: the ranked list, then the operation or all-operations list, its tab and one record. What cannot be found is left out.
async function settleView(view: View | null, run: number) {
  if (!view) { goTo(0); return; }
  const found = analysis;
  if (!found) return;
  if (view.type === 'where') {
    goTo(1);
    if (whereOpen) lens = view.lens ?? lens; else openWhere(view.lens ?? lens);
    compare = !!view.compare;
    return;
  }
  if (view.type === 'list' || (view.type === 'record' && view.solo)) {
    // A list or a lone record a link cannot rebuild: an open list stays, otherwise the level above it is shown (and the record opens on it).
    if (hasContext) goTo(2);
    else {
      goTo(view.lens ? 1 : 0);
      if (view.lens) { if (whereOpen) lens = view.lens; else openWhere(view.lens); }
    }
    if (view.type === 'record' && view.sourceId && view.recordId) await loadRecord(view.sourceId, view.recordId);
    return;
  }
  const wantsOperation = view.controller !== undefined;
  const rollup = wantsOperation ? (found.rollups ?? []).find(item => item.controller === view.controller && item.action === view.action && (!view.opKind || item.kind === view.opKind)) : undefined;
  if (wantsOperation && !rollup) { await settleView(view.lens ? { type: 'where', lens: view.lens } : null, run); return; }
  const inPlace = rollup
    ? operation != null && operation.controller === rollup.controller && operation.action === rollup.action && operation.kind === rollup.kind
    : operation == null && queryWide && !drillActive && drillOverview?.label === 'All operations';
  if (inPlace) goTo(2);
  else {
    whereOpen = view.lens !== undefined;
    if (view.lens) lens = view.lens;
    if (rollup) await openOperation(rollup); else await openComparison();
    if (run !== viewRun) return;
  }
  if (view.tab && inspectorTabs.some(tab => tab.id === view.tab)) inspectorTab = view.tab;
  if (view.type === 'record' && view.sourceId && view.recordId) await loadRecord(view.sourceId, view.recordId);
}
// The browser's Back asks for the drawer that opened last to close.
function closeDrawerById(id: string) {
  if (id === 'glossary') void closeGlossary();
  else if (id === 'coverage') void closeCoverage();
  else if (id === 'side') void closeSidePanel();
  else if (id === 'connections') void closeConnections();
}
export function onWindowKey(event: KeyboardEvent) {
  if (event.key !== 'Escape' || event.defaultPrevented) return;
  // A drawer closes on Esc even when focus is on the page itself (after a click on the page behind it); one with focus inside handles its own Esc.
  if (glossaryOpen) { void closeGlossary(); return; }
  if (coverageSource) { void closeCoverage(); return; }
  if (sidePanel) { void closeSidePanel(); return; }
  if (connectionsOpen) { void closeConnections(); return; }
  const target = event.target;
  if (target instanceof HTMLElement && (target.tagName === 'SELECT' || target.closest('.popover'))) return;
  // An open menu or the filters panel closes first; only then does Esc step one level up.
  const open = document.querySelector<HTMLDetailsElement>('details.menu[open], details.more-filters[open]');
  const inside = open != null && target instanceof HTMLElement && open.contains(target);
  const loose = !(target instanceof HTMLElement) || target === document.body;
  if (open && (inside || loose)) { open.open = false; open.querySelector<HTMLElement>('summary')?.focus(); return; }
  stepUpOnce();
}
// A scope change (range chip, request type, source, filter) re-runs the analysis after a short quiet period; a burst becomes one run and the last change wins.
export function onScopeChange() { error = ''; scopeRun.schedule(); }
/** A chosen hour of the hour grid becomes the primary window, with the hour before it as the reference. */
export function setWindowFromHour(window: RollupWindow) { Object.assign(query, hourWindowQuery(window)); controlsResetKey++; onScopeChange(); }
export function removeChip(chip: FilterChip) { applyChipPatch(query, chip); controlsResetKey++; onScopeChange(); }
export function clearChips() { for (const chip of filterChipList) applyChipPatch(query, chip); controlsResetKey++; onScopeChange(); }
export async function openFromTable(key: string) {
  const row = analysis?.apis.find(item => item.key === key);
  const rollup = row ? rollupFor(analysis?.rollups, row) : undefined;
  if (rollup) await openOperation(rollup, key); else await selectAPI(key);
}
export function chooseGroup(key: string) {
  if (queryWide) { selectedKey = key; contextAnchor = null; } else void selectAPI(key);
}
export async function drillInto(filter: BreakdownFilter, _row: BreakdownRow) {
  if (!breakdownQuery) return;
  const merged = serializeRecordFilters(mergeRecordFilter(breakdownQuery, filter));
  // Inside an operation the breakdown narrows that operation's list; the operation, its group and its scope are kept.
  const keep = operation ? { operation, selectedKey, queryWide, requestState, drillOverview } : null;
  contextAnchor = null; if (!keep) selectedKey = ''; invalidateDetails();
  if (keep) { operation = keep.operation; selectedKey = keep.selectedKey; queryWide = keep.queryWide; requestState = keep.requestState; drillOverview = keep.drillOverview; }
  drill = merged;
  await loadRequests('', 1);
  await tick();
  document.getElementById('request-history')?.scrollIntoView({ block: 'start' });
}
export async function drillQuality(which: string) {
  rememberOpener(); operation = null;
  contextAnchor = null; invalidateDetails(); selectedKey = '';
  queryWide = true; requestState = which;
  await loadRequests('', 1);
  await tick();
  document.getElementById('request-history')?.scrollIntoView({ block: 'start' });
}
export async function drillFromOverview(drill: OverviewDrill) {
  rememberOpener(); operation = null;
  contextAnchor = null; invalidateDetails(); selectedKey = '';
  queryWide = true; requestState = drill.state; drillOverview = drill;
  await loadRequests('', 1);
  await tick();
  document.getElementById('request-history')?.scrollIntoView({ block: 'start' });
}
// The Incomplete tab's own drill keeps the operation and its group (the list narrows, the frame does not change) and keeps the opener's focus target.
export async function drillFromIncomplete(raw: OverviewDrill) {
  const keep = operation ? { operation, selectedKey } : null;
  if (!keep) { await drillFromOverview(raw); return; }
  // The panel's totals buttons are every-API drills; inside an operation the list, heading and export stay that operation's.
  const next = scopeStaleDrill(raw, keep.operation);
  contextAnchor = null; invalidateDetails();
  operation = keep.operation; selectedKey = keep.selectedKey;
  queryWide = true; requestState = next.state; drillOverview = next;
  await loadRequests('', 1);
  await tick();
  document.getElementById('request-history')?.scrollIntoView({ block: 'start' });
}
// All operations as one evidence frame: every event of the comparison, with its comparison-wide Failures, Breakdown and Slowest.
export async function openComparison() {
  if (!analysis) return;
  rememberOpener();
  await drillFromOverview(allOperationsDrill(analysis.query.kind));
  focusSoon('inspector-title');
}
// The comparison's query with the overview drill-down's kind and status class applied; unchanged without one.
function drillQuery(source: Query): Query {
  const copy = JSON.parse(JSON.stringify(source)) as Query;
  if (drillOverview) {
    copy.kind = drillOverview.kind;
    if (drillOverview.outcome) copy.outcome = drillOverview.outcome;
    if (drillOverview.controller !== undefined) copy.controller = drillOverview.controller;
    if (drillOverview.action !== undefined) copy.action = drillOverview.action;
    // The stale list: unfinished requests open at least the threshold. A latency range describes completed rows, so it cannot apply.
    applyStaleFilters(copy, drillOverview);
  }
  return copy;
}
// Inside an operation this lifts the breakdown filter and shows the whole operation again; elsewhere the filter was the whole context.
export function clearDrill() {
  drill = {};
  if (operation) { void loadRequests('', 1); return; }
  closeInspector();
}
export async function loadRequests(cursor: string, pageNumber: number) {
  if ((!selectedKey && !queryWide && !drillActive) || !analysis) return;
  requestController?.abort(); requestController = new AbortController();
  const run = ++requestSequence;
  requestLoading = true; requestError = '';
  try {
    const page = await api.requests(drillQuery(breakdownQuery ?? analysis.query), queryWide ? '' : selectedKey, cursor, requestController.signal, requestState, drillOverview?.order ?? '');
    if (run !== requestSequence) return;
    requestPage = page;
    requestRows = page.rows.slice(0, info?.limits.maxPageSize ?? 100);
    requestPageNumber = pageNumber;
  } catch (cause) {
    if (run === requestSequence && !(cause instanceof DOMException && cause.name === 'AbortError')) requestError = message(cause);
  } finally { if (run === requestSequence) requestLoading = false; }
}
export async function showRecord(row: RequestRecord) {
  if (record == null) recordPlace = placeNow();
  await loadRecord(row.sourceId, row.id);
}
async function loadRecord(sourceId: string, id: string) {
  recordController?.abort(); recordController = new AbortController();
  const run = ++recordSequence;
  recordError = ''; record = null; recordLoadingId = id;
  try {
    const result = await api.record(sourceId, id, recordController.signal);
    if (run === recordSequence) record = result;
  }
  catch (cause) { if (run === recordSequence && !(cause instanceof DOMException && cause.name === 'AbortError')) recordError = message(cause); }
  finally { if (run === recordSequence) recordLoadingId = ''; }
}
export async function exportCSV() {
  if (!selected && !queryWide) return;
  requestError = '';
  try {
    const result = await api.export(drillQuery(breakdownQuery ?? analysis?.query ?? query), queryWide ? '' : (selected?.key ?? ''), undefined, requestState, drillOverview?.order ?? '');
    const url = URL.createObjectURL(result.blob);
    const link = document.createElement('a'); link.href = url; link.download = 'unravis-requests.csv';
    document.body.append(link); link.click(); link.remove(); setTimeout(() => URL.revokeObjectURL(url), 1000);
    if (result.limited) requestError = 'Export reached a server-configured row or byte cap.';
  } catch (cause) { requestError = message(cause); }
}

// Session: server info, source profiles and the start-up error.
export const app = {
  get info() { return info; }, set info(value) { info = value; },
  get sources() { return sources; }, set sources(value) { sources = value; },
  get initError() { return initError; }, set initError(value) { initError = value; },
  get initCanRetry() { return initCanRetry; },
  get firstRun() { return firstRun; },
  get maxSources() { return maxSources; },
  get remainingSourceSlots() { return remainingSourceSlots; },
  get sourceLabels() { return sourceLabels; },
  get connectionGroups() { return connectionGroups; }
};

// The investigation scope: the query, time zone, target and the per-tab investigation notice.
export const scope = {
  get query() { return query; }, set query(value) { query = value; },
  get zone() { return timeZone.zone; },
  get targetMs() { return targetMs; }, set targetMs(value) { targetMs = value; },
  get dateInputsValid() { return dateInputsValid; }, set dateInputsValid(value) { dateInputsValid = value; },
  get controlsResetKey() { return controlsResetKey; }, set controlsResetKey(value) { controlsResetKey = value; },
  get restoredInTab() { return restoredInTab; }, set restoredInTab(value) { restoredInTab = value; },
  get investigationNotice() { return investigationNotice; }, set investigationNotice(value) { investigationNotice = value; },
  get runHint() { return runHint; },
  get filterChipList() { return filterChipList; },
  get moreCount() { return moreCount; }
};

// The analysis on screen and the run in flight.
export const results = {
  get analysis() { return analysis; }, set analysis(value) { analysis = value; },
  get loading() { return loading; }, set loading(value) { loading = value; },
  get error() { return error; }, set error(value) { error = value; },
  get timelineKeys() { return timelineKeys; }, set timelineKeys(value) { timelineKeys = value; },
  get failingSources() { return failingSources; },
  get displayedRunIsStale() { return displayedRunIsStale; },
  get calculationNotes() { return calculationNotes; },
  get breakdownQuery() { return breakdownQuery; }
};

// D2 evidence and D3 record: the open operation or list, its request page and the open record.
export const evidence = {
  get selectedKey() { return selectedKey; }, set selectedKey(value) { selectedKey = value; },
  get requestState() { return requestState; }, set requestState(value) { requestState = value; },
  get queryWide() { return queryWide; }, set queryWide(value) { queryWide = value; },
  get drillOverview() { return drillOverview; }, set drillOverview(value) { drillOverview = value; },
  get contextAnchor() { return contextAnchor; }, set contextAnchor(value) { contextAnchor = value; },
  get requestPage() { return requestPage; }, set requestPage(value) { requestPage = value; },
  get requestRows() { return requestRows; }, set requestRows(value) { requestRows = value; },
  get requestPageNumber() { return requestPageNumber; }, set requestPageNumber(value) { requestPageNumber = value; },
  get drill() { return drill; }, set drill(value) { drill = value; },
  get record() { return record; }, set record(value) { record = value; },
  get requestLoading() { return requestLoading; }, set requestLoading(value) { requestLoading = value; },
  get recordLoadingId() { return recordLoadingId; }, set recordLoadingId(value) { recordLoadingId = value; },
  get requestError() { return requestError; }, set requestError(value) { requestError = value; },
  get recordError() { return recordError; }, set recordError(value) { recordError = value; },
  get operation() { return operation; }, set operation(value) { operation = value; },
  get inspectorTab() { return inspectorTab; }, set inspectorTab(value) { inspectorTab = value; },
  get selected() { return selected; },
  get drillActive() { return drillActive; },
  get drillChips() { return drillChips; },
  get targetAssessment() { return targetAssessment; },
  get hasContext() { return hasContext; },
  get operationGroups() { return operationGroups; },
  get evidenceName() { return evidenceName; },
  get comparisonWide() { return comparisonWide; },
  get slowScope() { return slowScope; },
  get scopedQuery() { return scopedQuery; },
  get selectedPlain() { return selectedPlain; },
  get inspectorTabs() { return inspectorTabs; },
  get activeTab() { return activeTab; },
  get openKey() { return openKey; },
  get failureCohort() { return failureCohort; }
};

// Progressive depth: D0 pulse, D1 ranked list, D2 evidence, D3 record.
export const navigation = {
  get whereOpen() { return whereOpen; }, set whereOpen(value) { whereOpen = value; },
  get lens() { return lens; }, set lens(value) { lens = value; },
  get compare() { return compare; }, set compare(value) { compare = value; },
  get splitDim() { return splitDim; }, set splitDim(value) { splitDim = value; },
  get depth() { return depth; },
  get crumbs() { return crumbs; }
};

// The background file import, if one is running. The import drawer reads and writes it; nothing else does.
export const importWork = {
  get running() { return runningImport; }, set running(value) { runningImport = value; }
};

// The modal drawers: connections, glossary and the side panels (patterns, ledger, export).
export const panels = {
  get connectionsOpen() { return connectionsOpen; }, set connectionsOpen(value) { connectionsOpen = value; },
  get glossaryOpen() { return glossaryOpen; }, set glossaryOpen(value) { glossaryOpen = value; },
  get sidePanel() { return sidePanel; }, set sidePanel(value) { sidePanel = value; },
  get glossaryTerm() { return glossaryTerm; }, set glossaryTerm(value) { glossaryTerm = value; },
  get connectionTab() { return connectionTab; }, set connectionTab(value) { connectionTab = value; },
  get drawerElement() { return drawerElement; }, set drawerElement(value) { drawerElement = value; },
  get coverage() { return coverageSource; },
  get modalOpen() { return connectionsOpen || glossaryOpen || sidePanel != null || coverageSource != null; }
};

// The Connections drawer form, recent connections and source profile changes.
export const conn = {
  get sourceBusy() { return sourceBusy; }, set sourceBusy(value) { sourceBusy = value; },
  get busyDelete() { return busyDelete; }, set busyDelete(value) { busyDelete = value; },
  get sourceError() { return sourceError; }, set sourceError(value) { sourceError = value; },
  get sourceSuccess() { return sourceSuccess; }, set sourceSuccess(value) { sourceSuccess = value; },
  get addLabel() { return addLabel; }, set addLabel(value) { addLabel = value; },
  get addUri() { return addUri; }, set addUri(value) { addUri = value; },
  get addUsername() { return addUsername; }, set addUsername(value) { addUsername = value; },
  get addPassword() { return addPassword; }, set addPassword(value) { addPassword = value; },
  get addAuthSource() { return addAuthSource; }, set addAuthSource(value) { addAuthSource = value; },
  get addTLSMode() { return addTLSMode; }, set addTLSMode(value) { addTLSMode = value; },
  get addCACertificate() { return addCACertificate; }, set addCACertificate(value) { addCACertificate = value; },
  get caCertificateName() { return caCertificateName; }, set caCertificateName(value) { caCertificateName = value; },
  get caCertificateInput() { return caCertificateInput; }, set caCertificateInput(value) { caCertificateInput = value; },
  get savedConnectionId() { return savedConnectionId; }, set savedConnectionId(value) { savedConnectionId = value; },
  get savedCredentialAvailable() { return savedCredentialAvailable; }, set savedCredentialAvailable(value) { savedCredentialAvailable = value; },
  get savedCredentialSessionOnly() { return savedCredentialSessionOnly; }, set savedCredentialSessionOnly(value) { savedCredentialSessionOnly = value; },
  get rememberPassword() { return rememberPassword; }, set rememberPassword(value) { rememberPassword = value; },
  get recentConnections() { return recentConnections; }, set recentConnections(value) { recentConnections = value; },
  get recentHistoryError() { return recentHistoryError; }, set recentHistoryError(value) { recentHistoryError = value; },
  get recentSaveError() { return recentSaveError; }, set recentSaveError(value) { recentSaveError = value; },
  get recentSaveSuccess() { return recentSaveSuccess; }, set recentSaveSuccess(value) { recentSaveSuccess = value; },
  get recentSaveErrorStage() { return recentSaveErrorStage; }, set recentSaveErrorStage(value) { recentSaveErrorStage = value; },
  get connectionErrorStage() { return connectionErrorStage; }, set connectionErrorStage(value) { connectionErrorStage = value; },
  get connectionErrorContext() { return connectionErrorContext; }, set connectionErrorContext(value) { connectionErrorContext = value; },
  get recentBusyId() { return recentBusyId; }, set recentBusyId(value) { recentBusyId = value; },
  get historyMutationBusy() { return historyMutationBusy; }, set historyMutationBusy(value) { historyMutationBusy = value; },
  get forgetInProgress() { return forgetInProgress; }, set forgetInProgress(value) { forgetInProgress = value; },
  get manualDatabaseName() { return manualDatabaseName; }, set manualDatabaseName(value) { manualDatabaseName = value; },
  get selectedDatabases() { return selectedDatabases; }, set selectedDatabases(value) { selectedDatabases = value; },
  get addCollection() { return addCollection; }, set addCollection(value) { addCollection = value; },
  get discoveredDatabases() { return discoveredDatabases; }, set discoveredDatabases(value) { discoveredDatabases = value; },
  get completedDatabases() { return completedDatabases; }, set completedDatabases(value) { completedDatabases = value; },
  get connectionWarning() { return connectionWarning; }, set connectionWarning(value) { connectionWarning = value; },
  get connectionError() { return connectionError; }, set connectionError(value) { connectionError = value; },
  get connectionTestedSignature() { return connectionTestedSignature; }, set connectionTestedSignature(value) { connectionTestedSignature = value; },
  get connectionTesting() { return connectionTesting; }, set connectionTesting(value) { connectionTesting = value; },
  get manualDatabase() { return manualDatabase; }, set manualDatabase(value) { manualDatabase = value; },
  get connectionSignature() { return connectionSignature; },
  get availableDatabases() { return availableDatabases; }
};
