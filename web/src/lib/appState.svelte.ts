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
import { clearInvestigationSnapshot, createInvestigationSnapshot, forgetInvestigationSource, hasUnresolvedKnownSources, readInvestigationSnapshot, restoreInvestigation, serializeRecordFilters, shouldSelectAddedSource, writeInvestigationSnapshot, type InvestigationSnapshot } from './investigation';
import { buildWorkspace, classifyRestoreFailure, createWorkspaceSaver, parseWorkspace, planRestore, pruneWorkspace, queueWorkspace, RESTORE_TIMEOUT_MS, sourceMatches, workspaceSaveAllowed, type Workspace, type WorkspaceDatabase } from './caseWorkspace';
import { forgetSavedRecord, forgetSourceSaved, registerSourceSaved, resolveSavedRecent, savedIdFor, suggestedConnectionLabel } from './sourceSaved';
import type { Analysis, BreakdownDim, BreakdownFilter, BreakdownRow, Comparison, ConnectionInput, ConnectionTestResult, Info, Page, Query, RecentConnection, RecordDetail, RecordFilters, RequestRecord, Rollup, Source, SourceInput, TLSMode } from './types';
import { applyDrill, hasRecordFilter, mergeRecordFilter, recordFilterChips } from './breakdown';
import type { RunningImport } from './cases';
import { afterUnlock, createRoute, go as moveScreen, historyStep, initialScreen, isHistoryScreen, leavesCaseWork, setupReturn, type Screen } from './screenFlow';
import { onCasesLocked } from './lockSignal';
import { requestCase } from './startIntent';
import { CASE_RESTORE_TEXT } from './copy/caseRestoreCopy';
import { holdLedger, setLedgerCase } from './ledgerStore.svelte';
import { getMasterKeyStatus, lockMasterKey } from './masterKeyApi';
import type { KeyStatus, LockState } from './masterKey';
import { KEY_TEXT } from './copy/masterKeyCopy';
import { theme } from './theme.svelte';
import { timeZone } from './timeZone.svelte';
import { applyChipPatch, breadcrumb, depthOf, evidenceTabs, filterChips, moreFiltersCount, type FilterChip } from './depth';
import { landingMayAutoRun, scopeHint, scopeProblem } from './autorun';
import type { Lens } from './pulse';
import { GLOSSARY_EVENT, glossaryTermFrom } from './copy/hintState';
import { plainTypicalVsSlowest } from './copy/plainSummary';
import { applyStaleFilters, scopeStaleDrill } from './incomplete';
import { allOperationsDrill, operationDrill, type OverviewDrill } from './overview';
import { phaseSegments } from './phases';
import { rollupFor } from './traffic';
import { createViewHistory, describeView, parseViewHash, type View } from './wayfinding';
import { hourWindowQuery, type RollupWindow } from './rollup';
import { backTarget, planBack, planInvestigation, rootOf, type TrailEntry } from './promote';
import { zoomView } from './zoomView.svelte';
import { keeperPreview, keeperUnreadable, onKeeperPreview, onKeeperRefusal } from './keeper';

let info = $state<Info | null>(null);
let sources = $state<Source[]>([]);
let query = $state<Query>(defaultQuery(new Date(), []));
let dateInputsValid = $state(true);
let zone = $state(Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC');
let targetMs = $state<number | null>(null);
let restoredInTab = $state(false);
let controlsResetKey = $state(0);
/** The windows "Investigate this window" replaced, newest last. In memory only; never more than 10 (see promote.ts). */
let windowTrail = $state.raw<TrailEntry[]>([]);
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
// Which screen shows (see screenFlow.ts): the master key unlock, the start screen, the working analysis. Changed only through go().
let screen = $state<Screen>('loading');
// The master key state the server last reported, or 'unknown' when it could not be read. Never holds a key.
let lockState = $state<LockState | 'unknown'>('unknown');
let dpapiCases = $state(0);
let lockError = $state('');
// Where a successful unlock returns to: the screen that was interrupted, or the first screen the person would have had.
let unlockOrigin: Screen | null = null;
// Where set-up was opened from ('case' returns there; anything else returns to the start screen). Read once on the way out of set-up.
let setupOrigin: Screen | null = null;
// The case the person is working in. The case home, the theories and the analysis screen's case tools all follow it. Empty until one is opened.
// This tab remembers it, so a reload keeps the case and its theories instead of falling back to the older shared list.
const ACTIVE_CASE_KEY = 'unravis.activeCase';
function storedCase(write?: string): string {
  try {
    if (typeof sessionStorage === 'undefined') return '';
    if (write === undefined) return sessionStorage.getItem(ACTIVE_CASE_KEY) ?? '';
    if (write) sessionStorage.setItem(ACTIVE_CASE_KEY, write); else sessionStorage.removeItem(ACTIVE_CASE_KEY);
  } catch { /* storage blocked: the case is simply not remembered */ }
  return '';
}
let activeCaseId = $state(storedCase());
// Opening a case restores what it saved: its MongoDB connection and databases, then its filters (see caseWorkspace.ts). 'working' is the quiet
// reconnect; the others wait for the person. Saves of the case workspace stay off until the case's own workspace has been read and applied.
type RestorePhase = 'idle' | 'working' | 'signin' | 'unreachable' | 'missing' | 'failed';
let restorePhase = $state<RestorePhase>('idle');
let restoreDatabases = $state<string[]>([]);
let restoreRecentId = $state('');
let restoreLabel = $state('');
let restoredCaseId = '';
let workspaceBaseline: Workspace | null = null;
let restoreSequence = 0;
let pendingRestore: { workspace: Workspace | null; keepTab: boolean } | null = null;
// What the running restore knows so far, so the time limit and Cancel can end it the right way from outside the run.
interface RestoreRun { id: string; run: number; workspace: Workspace | null; keepTab: boolean; databases: WorkspaceDatabase[]; recent: RecentConnection | null }
let restoreRun: RestoreRun | null = null;
let restoreTimer: ReturnType<typeof setTimeout> | null = null;
let sourceCountSeen = 0;
let tabInvestigationHeld = false;
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
let sidePanel = $state<'patterns' | 'ledger' | 'export' | 'import' | 'blackbox' | null>(null);
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
let autoRan = $state(false);
// The scope fingerprint of the run in flight; '' when none.
let inflightSignature = '';
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
// True while Back or Forward is moving the screen: that move must not add a history entry of its own.
let poppingScreen = false;
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
// A changed scope never runs by itself: while the analysis on screen is for an older scope, the hint says so and points at the button.
export const FILTERS_CHANGED_HINT = 'Filters changed. Click Run analysis to use the new window.';
const runHint = $derived(scopeHint(query, dateInputsValid) || (displayedRunIsStale && !loading ? FILTERS_CHANGED_HINT : ''));
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
    const stopLockListener = onCasesLocked(noteLocked);
    window.addEventListener(GLOSSARY_EVENT, onGlossaryRequest);
    drawerHistory = createDrawerHistory(history, closeDrawerById);
    viewHistory = createViewHistory(location, history);
    // Browser Back closes an open drawer first; an event no drawer used steps to the screen the entry was made on, then rebuilds the view.
    const onPop = () => {
      if (drawerHistory?.pop()) return;
      const target = viewHistory?.pop();
      if (target === undefined) return;
      stepScreenFromHistory(viewHistory?.screen ?? '');
      void applyView(target);
    };
    window.addEventListener('popstate', onPop);
    return () => { stopLockListener(); window.removeEventListener(GLOSSARY_EVENT, onGlossaryRequest); window.removeEventListener('popstate', onPop); viewHistory = null; drawerHistory = null; };
  });

  $effect(() => {
    const current = querySignature;
    // A run already started for exactly this query (Investigate and Back write the window, then run at once) is the one to keep.
    if (querySnapshot && querySnapshot !== current && current !== inflightSignature) {
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
    if (autoRan || screen !== 'running' || restorePhase === 'working' || !info || !sources.length || !query.sourceIds.length || !dateInputsValid || !investigationReady) return;
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

  // The case remembers the same filters, and which saved connection and databases it used. Saved quietly after a pause; see queueWorkspaceSave.
  $effect(() => {
    const changed = JSON.stringify([query, zone, targetMs, sources]);
    const caseId = activeCaseId, phase = restorePhase;
    if (!changed || !caseId || phase === 'working') return;
    untrack(() => queueWorkspaceSave());
  });

  // A source added by any route (the connect drawer, the import panel, the create guide) while the reconnect waits ends the waiting: the saved filters go on.
  (() => {
    const count = sources.length;
    untrack(() => {
      if (count > sourceCountSeen && restoreWaiting(restorePhase)) completePendingRestore();
      sourceCountSeen = count;
    });
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

let keeperPreviewWatched = false;
export async function initialize() {
  const hadToken = hasSessionToken();
  initCanRetry = true;
  try {
    info = await api.info();
    // A reload keeps the case this tab was working in, so its theories stay in view. The demo has no cases.
    if (info.demo) setActiveCase(''); else if (activeCaseId) setLedgerCase(activeCaseId);
    pendingInvestigation =readInvestigationSnapshot(undefined, info.demo ? 'demo' : 'live');
    const asked = keeperPreview();
    const loaded = (await api.sources(asked)) ?? [];
    sources = loaded;
    // In the demo a keeper preview named after the page loaded marks the sources too (the screenshots of P14b), also one named
    // while this list was on its way.
    if (info.demo && !keeperPreviewWatched) { keeperPreviewWatched = true; onKeeperPreview(() => void refreshKeeperSources()); }
    if (info.demo && keeperPreview() !== asked) void refreshKeeperSources();
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
    unselectUnreadableSources();
    investigationFingerprint = JSON.stringify([query, zone, targetMs, sources]);
    investigationReady = true;
    pendingInvestigation = null;
    setLocalInputs();
    tabInvestigationHeld = restoredInTab;
    await chooseFirstScreen(info.demo, restoredInTab);
    tabInvestigationHeld = false;
    if (!info.demo) {
      try {
        const recents = await api.recentConnections();
        recentConnections = Array.isArray(recents) ? recents.filter(isRecentConnection).slice(0, 20) : [];
      } catch {
        recentHistoryError = 'Recent connections could not be loaded. You can still test and add a connection.';
      }
    }
  } catch (cause) {
    // A failure after the app info arrived keeps the screen the app always showed (the connect prompt and the Retry banner).
    if (screen === 'loading' && info) go('running');
    const text = message(cause);
    const failure = classifyStartupFailure({ hadToken, cause });
    initError = failure.kind === 'other' ? text : failure.message;
    initCanRetry = failure.canRetry;
  }
}
function setLocalInputs() {
}

// ---- Screens: unlock, start, working (see lib/screenFlow.ts for the legal moves) ----
function devLog(text: string) {
  // Dev only: a refused move is logged for the developer and ignored for the person. Never anything a person typed.
  if ((import.meta as unknown as { env?: { DEV?: boolean } }).env?.DEV) console.warn('[screens] ' + text);
}
/** Asks to move to another screen. A move the table does not allow is ignored (and logged in dev); returns whether the app is on `to` now. */
export function go(to: Screen): boolean {
  const from = screen;
  // Leaving set-up with "Later" or "Continue" (both ask for 'start') goes back to where set-up was opened from.
  if (screen === 'setup' && to === 'start' && setupOrigin) to = setupReturn(setupOrigin);
  const move = moveScreen(screen, to);
  if (!move.ok) { devLog(move.reason); return false; }
  if (move.screen === screen) return true;
  // Leaving the working screen puts away its case panel. The case itself stays open on the server; nothing is closed or erased.
  if (leavesCaseWork(move.screen) && sidePanel) void closeSidePanel();
  screen = move.screen;
  if (move.screen !== 'setup') setupOrigin = null;
  if (!poppingScreen) noteScreenHistory(from, move.screen);
  // Arriving at the case or the analysis with a case open: bring back what the case saved, once (see restoreCaseWorkspace).
  if ((move.screen === 'running' || move.screen === 'case') && activeCaseId && restoredCaseId !== activeCaseId && restorePhase === 'idle' && info && !info.demo) void restoreCaseWorkspace(activeCaseId);
  return true;
}
// A move to start, case, create or the analysis gets a browser history entry once no drawer entry is open (see lib/screenFlow.ts historyStep).
function noteScreenHistory(from: Screen, to: Screen) {
  const marker = viewHistory, drawers = drawerHistory;
  if (!marker || !drawers || !isHistoryScreen(to)) return;
  drawers.whenClear(() => {
    if (screen !== to || viewHistory !== marker) return;
    const step = historyStep(from, to, marker.screen);
    if (step !== 'none') marker.markScreen(to, step === 'replace');
  });
}
// Back or Forward landed on an entry marked with another screen: go there through the same move table. Key screens are never stepped onto or off.
// A move the table refuses, or a case page with no case open, re-marks the entry for the screen that stays, so Back cannot loop or trap.
function stepScreenFromHistory(wanted: string) {
  const marker = viewHistory;
  if (!marker || !isHistoryScreen(wanted) || !isHistoryScreen(screen) || wanted === screen) return;
  if (wanted === 'case' && !activeCaseId) { marker.markScreen(screen, true); return; }
  poppingScreen = true;
  try { if (!go(wanted)) marker.markScreen(screen, true); } finally { poppingScreen = false; }
}
// Reads the key state once the app info is in, then picks the first screen. A status that cannot be read is treated as "no key" for routing.
async function chooseFirstScreen(demo: boolean, resume: boolean) {
  let lock: LockState | 'unknown' = 'unknown';
  if (!demo) {
    try { noteKeyStatus(await getMasterKeyStatus()); lock = lockState; } catch { lockState = 'unknown'; }
  }
  const deepLink = initialView != null;
  const target = initialScreen({ demo, deepLink, resume, lock });
  if (lock === 'locked' || lock === 'damaged') unlockOrigin = initialScreen({ demo, deepLink, resume, lock: 'unlocked' });
  go(target);
}
/** Takes in a status the server reported. */
export function noteKeyStatus(status: KeyStatus) {
  lockState = status.state;
  dpapiCases = status.dpapiCases;
}
/** A case route answered 423, or the status said locked: the key is closed, so the unlock screen takes over. Nothing is cleared. */
export function noteLocked() {
  if (screen === 'unlock') return;
  // A damaged key file stays "damaged": the unlock screen shows the start-over screen for it, not the key field.
  if (lockState !== 'damaged') lockState = 'locked';
  if (screen === 'running' || screen === 'start' || screen === 'create' || screen === 'case') unlockOrigin = screen;
  bumpEpoch();
  go('unlock');
}
/** The right key was typed: back to the work that was interrupted, or to the start screen. */
export function finishUnlock(status: KeyStatus) {
  noteKeyStatus(status);
  lockError = '';
  const target = afterUnlock(unlockOrigin);
  unlockOrigin = null;
  go(target);
}
/**
 * A damaged key file was set aside. The status is now "none", so the set-up screen can make a new key. `unlock` cannot move to `setup` (that
 * move is refused on purpose), so the route is unlock -> start -> setup.
 */
export function finishReset(status: KeyStatus) {
  noteKeyStatus(status);
  unlockOrigin = null;
  setupOrigin = null;
  lockError = '';
  go('start');
  go('setup');
}
/** The header Lock action: locks the key on the server, then shows the unlock screen. Nothing is cleared from the open analysis. */
let locking = false;
// A function, so the screen is read again after the await (a 423 on another call may have moved it to unlock meanwhile).
const onUnlockScreen = (): boolean => screen === 'unlock';
export async function lockCases() {
  // A second click while the first is in flight must not run, or its reply would see the unlock screen and reset where unlocking returns to.
  if (locking || screen === 'unlock') return;
  locking = true;
  lockError = '';
  try {
    noteKeyStatus(await lockMasterKey());
    if (!onUnlockScreen()) unlockOrigin = screen === 'running' || screen === 'create' || screen === 'case' ? screen : 'start';
    bumpEpoch();
    go('unlock');
  } catch {
    lockError = KEY_TEXT.lockFailed;
  } finally {
    locking = false;
  }
}
/** Home: back to the start screen. The open case is not closed and the analysis keeps its filters, zoom and ledger. */
export function goHome() { go('start'); }
// ---- The case workspace: saving it, and bringing it back when the case is opened (see lib/caseWorkspace.ts) ----
function setRestore(phase: RestorePhase, databases: string[] = [], recentId = '', label = '') {
  restorePhase = phase; restoreDatabases = databases; restoreRecentId = recentId; restoreLabel = label;
  if (phase !== 'working' && restoreTimer !== null) { clearTimeout(restoreTimer); restoreTimer = null; }
}
/** The restore is waiting for the person (sign in, a new connection, a retry) rather than running. */
const restoreWaiting = (phase: RestorePhase): boolean => phase === 'signin' || phase === 'unreachable' || phase === 'missing' || phase === 'failed';
const saveAllowed = (caseId: string): boolean =>
  caseId === activeCaseId && workspaceSaveAllowed({ demo: !!info?.demo, caseId: activeCaseId, ready: investigationReady && !!info, restoring: restorePhase === 'working', restoredCaseId });
const workspaceSaver = createWorkspaceSaver({
  send: (caseId, workspace) => api.saveCaseWorkspace(caseId, workspace),
  allowed: saveAllowed,
  sent: (caseId, workspace) => { if (caseId === activeCaseId) workspaceBaseline = workspace; }
});
/** Saves the case's workspace after a quiet pause. Does nothing in the demo, with no case, or until the case's own workspace was read. */
function queueWorkspaceSave() {
  if (!saveAllowed(activeCaseId)) return;
  const workspace = buildWorkspace({ query: cloneQuery(), sources, zone, targetMs, intent: investigationIntent, savedIdOf: source => savedIdFor(source, recentConnections), previous: workspaceBaseline });
  if (!workspace) return;
  queueWorkspace(workspaceSaver, activeCaseId, workspaceBaseline, workspace); // back to what the case holds also drops an older change still waiting
}
/** Puts a saved investigation on the page: the filters, the chosen sources, the zone; the old results and details go. */
function applyWorkspaceInvestigation(snapshot: InvestigationSnapshot | null) {
  if (!snapshot) return;
  const restored = restoreInvestigation(snapshot, sources);
  const { zone: restoredZone, targetMs: restoredTargetMs, ...restoredQuery } = restored;
  if (!restoredQuery.sourceIds.length) restoredQuery.sourceIds = query.sourceIds.filter(id => sources.some(source => source.id === id));
  if (!restoredQuery.sourceIds.length) restoredQuery.sourceIds = sources.map(source => source.id);
  analysisSequence++; analysisController?.abort(); analysisController = null; inflightSignature = ''; loading = false; error = '';
  invalidateDetails(); bumpEpoch();
  analysis = null; selectedKey = ''; operation = null; contextAnchor = null; windowTrail = [];
  query = restoredQuery;
  zone = restoredZone; targetMs = restoredTargetMs;
  dateInputsValid = true; restoredInTab = false; controlsResetKey++;
  investigationIntent = hasUnresolvedKnownSources(snapshot, sources) ? snapshot : null;
}
function finishRestore(id: string, workspace: Workspace | null, keepTab: boolean) {
  if (!keepTab) applyWorkspaceInvestigation(workspace?.investigation ?? null);
  workspaceBaseline = workspace;
  restoredCaseId = id;
  pendingRestore = null;
  restoreRun = null;
  autoRan = false;
  setRestore('idle');
}
/** The person connected something, or put the waiting aside: the saved filters go on now and the case saves again. */
function completePendingRestore() {
  const pending = pendingRestore;
  if (!pending || !activeCaseId || !restoreWaiting(restorePhase)) return;
  finishRestore(activeCaseId, pending.workspace, pending.keepTab);
}
const namesOf = (databases: readonly WorkspaceDatabase[]): string[] => databases.map(item => item.name);
/** The restore stops and waits for the person. The saved filters are held back until they connect something or put the waiting aside. */
function enterRestoreWait(ctx: RestoreRun, phase: 'signin' | 'unreachable' | 'missing' | 'failed', databases: readonly WorkspaceDatabase[], recent: RecentConnection | null) {
  pendingRestore = { workspace: ctx.workspace, keepTab: ctx.keepTab };
  if (!ctx.keepTab && ctx.workspace?.investigation) investigationIntent = ctx.workspace.investigation;
  restoreRun = null;
  setRestore(phase, namesOf(databases), recent?.id ?? '', recent?.label ?? '');
}
/** The time limit ran out: whatever the run still brings back is dropped, and MongoDB is reported as not reachable. */
function expireRestore(run: number) {
  restoreTimer = null;
  const ctx = restoreRun;
  if (!ctx || ctx.run !== run || run !== restoreSequence || restorePhase !== 'working') return;
  restoreSequence++;
  if (!ctx.workspace) { restoreRun = null; setRestore('idle'); return; } // the case's own settings never came: nothing is saved over them
  enterRestoreWait(ctx, 'unreachable', ctx.databases.length ? ctx.databases : ctx.workspace.mongo?.databases ?? [], ctx.recent);
}
/**
 * Brings back what the case saved: its MongoDB connection and databases (reconnected from the saved connection when that holds a login,
 * otherwise asked for), then its filters. One run at a time; a newer one or a change of case ends an older one, and so does the time limit
 * (RESTORE_TIMEOUT_MS for all of it) or Cancel.
 */
async function restoreCaseWorkspace(id: string) {
  const keepTab = tabInvestigationHeld; // a reload keeps the filters this tab already had
  tabInvestigationHeld = false;
  const run = ++restoreSequence;
  const alive = () => run === restoreSequence && activeCaseId === id;
  restoredCaseId = ''; workspaceBaseline = null; pendingRestore = null;
  workspaceSaver.cancel(); workspaceSaver.forget();
  const ctx: RestoreRun = { id, run, workspace: null, keepTab, databases: [], recent: null };
  restoreRun = ctx;
  setRestore('working');
  restoreTimer = setTimeout(() => expireRestore(run), RESTORE_TIMEOUT_MS);
  let workspace: Workspace | null;
  try { workspace = parseWorkspace(await api.caseWorkspace(id)); } catch { if (alive()) { restoreRun = null; setRestore('idle'); } return; }
  if (!alive()) return;
  if (!workspace) { restoreRun = null; setRestore('idle'); return; }
  ctx.workspace = workspace;
  const mongo = workspace.mongo;
  if (!mongo) { finishRestore(id, workspace, keepTab); return; }
  try {
    const fresh = await api.recentConnections();
    if (Array.isArray(fresh)) recentConnections = fresh.filter(isRecentConnection).slice(0, 20);
  } catch { /* the list already on the page is used */ }
  if (!alive()) return;
  const plan = planRestore({ workspace, sources, recents: recentConnections, sessionIds: sessionCredentialIds });
  if (plan.kind === 'none') { finishRestore(id, workspace, keepTab); return; }
  if (plan.kind === 'connected') {
    for (const source of sources) if (mongo.databases.some(item => sourceMatches(source, item))) registerSourceSaved(source, mongo.savedConnectionId);
    finishRestore(id, workspace, keepTab);
    return;
  }
  ctx.databases = plan.databases;
  if (plan.kind === 'missing') { enterRestoreWait(ctx, 'missing', plan.databases, null); return; }
  ctx.recent = plan.recent;
  if (plan.kind === 'signin') { enterRestoreWait(ctx, 'signin', plan.databases, plan.recent); return; }
  const recent = plan.recent;
  const failures: unknown[] = [];
  let added = 0;
  for (const database of plan.databases) {
    const input: SourceInput = {
      uri: recent.uri, username: recent.username, password: '', authSource: recent.authSource, tlsMode: recent.tlsMode || 'uri', caCertificate: recent.caCertificate || '',
      savedConnectionId: recent.id,
      label: plan.databases.length > 1 ? `${recent.label} / ${database.name}`.slice(0, 100) : recent.label,
      // The collection the source itself read; the saved record's one only for a workspace saved before collections were kept.
      connectionLabel: recent.label, database: database.name, collection: database.collection || recent.collection || 'ApiAuditLog'
    };
    let source: Source;
    try { source = await api.addSource(input); } catch (cause) { if (!alive()) return; failures.push(cause); continue; }
    // The case changed (or the restore was ended) while this was connecting: the result belongs to nobody here, so it is neither listed nor registered.
    if (!alive()) return;
    if (!sources.some(item => item.id === source.id)) sources = [...sources, source];
    registerSourceSaved(source, recent.id);
    if (keepTab && shouldSelectAddedSource(source, investigationIntent)) query.sourceIds = [...query.sourceIds, source.id];
    added++;
  }
  if (added === 0) { enterRestoreWait(ctx, classifyRestoreFailure(failures, recent.username !== ''), plan.databases, recent); return; }
  if (added < plan.databases.length) investigationNotice = CASE_RESTORE_TEXT.partial;
  finishRestore(id, workspace, keepTab);
}
/** "Sign in to reconnect": opens the saved connection in the connection drawer, ready for the password. */
export async function reconnectRestore() {
  const recent = recentConnections.find(item => item.id === restoreRecentId);
  if (!recent) { await startNewConnection(); return; }
  clearConnectionDetails();
  loadRecent(recent);
  await openConnections('new');
}
/** "Try again" after MongoDB could not be reached: the same reconnect, from the start. */
export async function retryRestore() {
  if (restorePhase !== 'unreachable' || !activeCaseId) return;
  tabInvestigationHeld = pendingRestore?.keepTab ?? false;
  pendingRestore = null;
  setRestore('idle');
  await restoreCaseWorkspace(activeCaseId);
}
/**
 * Cancel. While the reconnect runs it ends at once and the case opens without those databases; while it waits for the person it is put
 * aside. Either way the case's saved connection stays on record (a save never drops it), and saving carries on. When the case's own
 * settings were not read yet nothing is saved over them.
 */
export function cancelRestore() {
  if (restorePhase === 'working') {
    const ctx = restoreRun;
    restoreSequence++;
    if (ctx && ctx.id === activeCaseId && ctx.workspace) finishRestore(ctx.id, ctx.workspace, ctx.keepTab);
    else { restoreRun = null; setRestore('idle'); }
    return;
  }
  completePendingRestore();
}
/** Makes a case the one being worked in: the theories follow it. Passing an empty id clears it. Opening a case on the working screens brings its workspace back. */
export function setActiveCase(id: string, options: { restore?: boolean } = {}) {
  if (activeCaseId === id) return;
  // What waits to be saved belongs to the case being left, so it goes now; the new case starts with nothing restored.
  workspaceSaver.flush();
  restoreSequence++;
  restoredCaseId = ''; workspaceBaseline = null; pendingRestore = null; restoreRun = null;
  setRestore('idle');
  activeCaseId = id;
  storedCase(id);
  setLedgerCase(id);
  // Already on the case or the analysis (the import drawer picks a case here), so no screen change will start the restore: start it now.
  if (options.restore !== false && id && (screen === 'running' || screen === 'case') && info && !info.demo) void restoreCaseWorkspace(id);
}
/** A case was erased. If it was the one being worked in, it stops being active and its page gives way to the start screen. */
export function caseErased(id: string) {
  if (!id || activeCaseId !== id) return;
  setActiveCase('');
  if (screen === 'case') go('start');
}
/** "Use an existing case": opens that case's home page (story, theories, health checks, files). The analysis is one tap away from there. */
export function openCase(id: string, options: { archived?: boolean } = {}) {
  // An archived case's theories are not asked for: the server answers 409 for them.
  holdLedger(id, options.archived === true);
  setActiveCase(id);
  go('case');
}
/** Back to the home page of the case being worked in. Does nothing when no case has been opened. */
export function openCaseHome() {
  if (activeCaseId) go('case');
}
/** From the case home to the analysis. The case stays the active one. */
export function openAnalysis() { go('running'); }
/** From the case home to the import drawer for that case, over the case home. */
export function openCaseImport() {
  if (!activeCaseId) return;
  requestCase(activeCaseId);
  openSidePanel('import');
}
/**
 * A case just made by the create guide. The guide added its audit sources on the server directly, so this page's source list is stale:
 * reload it and select the new sources (the analysis starts by itself when the user opens it), then show Case home. The import drawer
 * opens over Case home only when there is still nothing to analyse (a case made from files alone).
 */
export async function openCreatedCase(id: string) {
  requestCase(id);
  setActiveCase(id, { restore: false });
  // A case just made has no workspace yet, so there is nothing to restore; what it connects from now on is saved with it.
  restoredCaseId = id;
  let hasSources = sources.length > 0;
  try {
    const loaded = (await api.sources()) ?? [];
    const known = new Set(sources.map(source => source.id));
    const added = loaded.filter(source => !known.has(source.id));
    sources = loaded;
    if (added.length) {
      query.sourceIds = [...new Set([...query.sourceIds, ...added.map(source => source.id)])];
      autoRan = false;
    }
    hasSources = loaded.length > 0;
  } catch { /* the list could not be read: the analysis screen still opens and its own connect step can add sources */ }
  if (go('case') && !hasSources) openSidePanel('import');
}
/** "Create a new one": the guided flow when it exists, otherwise the import panel over the working screen. */
export function startCreate() {
  const route = createRoute();
  if (go(route.screen) && route.panel) openSidePanel(route.panel);
}
export function openKeySetup() {
  const from = screen;
  if (go('setup')) setupOrigin = from === 'case' ? 'case' : null;
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
    forgetSavedRecord(recent.id);
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
    // A case waiting for its database: the databases it used are ticked already.
    if (restoreRecentId && restoreRecentId === connection.savedConnectionId && (restorePhase === 'signin' || restorePhase === 'failed' || restorePhase === 'unreachable')) {
      selectedDatabases = restoreDatabases.filter(name => discoveredDatabases.includes(name)).slice(0, remainingSourceSlots);
    }
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
  analysisSequence++;
  analysisController?.abort(); analysisController = null; inflightSignature = ''; loading = false;
  invalidateDetails();
  analysis = null; selectedKey = ''; error = ''; operation = null; whereOpen = false; contextAnchor = null;
  query = defaultQuery(new Date(), sources.map(source => source.id));
  investigationIntent = null;
  zone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
  targetMs = null; dateInputsValid = true; restoredInTab = false; windowTrail = [];
  controlsResetKey++;
  investigationNotice = 'Investigation reset. Defaults are ready.';
  clearInvestigationSnapshot(undefined, info?.demo ? 'demo' : 'live');
  investigationFingerprint = JSON.stringify([query, zone, targetMs, sources]);
}

/**
 * Learns which saved connection new sources came from, so the case can name it (never the address or login). The record in use, else the
 * record that matches the details just used, else a quiet attempt to save them. Failing to find one only means the case cannot reopen
 * this connection by itself.
 */
async function noteSavedConnection(added: Source[], connection: ConnectionInput, label: string, collection: string) {
  // Never saves over a record that exists (that would replace it and wipe its stored password): the one in use, else the one at the same
  // address, else a new one. The list is read again first, since the page's copy may be old.
  const found = await resolveSavedRecent({
    details: { uri: connection.uri, username: connection.username, collection, authSource: connection.authSource, tlsMode: connection.tlsMode, caCertificate: connection.caCertificate },
    savedId: connection.savedConnectionId,
    recents: recentConnections,
    refresh: async () => { const fresh = await api.recentConnections(); return Array.isArray(fresh) ? fresh.filter(isRecentConnection).slice(0, 20) : null; },
    create: async () => {
      const made = await api.rememberConnection({ label: label.trim() || suggestedConnectionLabel(connection.uri), connection: { ...connection, savedConnectionId: undefined }, collection, rememberPassword: false });
      return isRecentConnection(made) ? made : null;
    }
  });
  recentConnections = found.recents.slice(0, 20);
  const saved = found.recent;
  if (!saved) return; // the case just cannot reopen this connection by itself
  for (const source of added) registerSourceSaved(source, saved.id);
  if (connection.password && !saved.hasCredentials) sessionCredentialIds.add(saved.id);
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
  const addedSources: Source[] = [];
  try {
    for (const database of databaseNames) {
      try {
        const label = !manualDatabase && (databaseNames.length > 1 || labelDatabasesIndividually) ? `${labelPrefix} / ${database}`.slice(0, 100) : labelPrefix;
        const input: SourceInput = { ...connection, label, connectionLabel: labelPrefix, database, collection };
        const added = await api.addSource(input);
        const selectAdded = shouldSelectAddedSource(added, investigationIntent);
        sources = [...sources, added];
        addedSources.push(added);
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
  if (addedSources.length && !info?.demo) await noteSavedConnection(addedSources, connection, labelPrefix, collection);
  if (addedCount) { completePendingRestore(); queueWorkspaceSave(); }
  // A fully successful add is done: close the drawer and show the result in the source list. A partial failure stays open so the failed databases can be retried.
  if (allAdded) await closeConnections();
}
export async function deleteSource(source: Source) {
  sourceError = ''; busyDelete = source.id;
  try {
    await api.deleteSource(source.id);
    forgetSource(source);
  } catch (cause) { sourceError = message(cause); }
  finally { busyDelete = ''; }
}
/** Takes a source that is gone on the server off the page: its selection, its results and the case's record of it. */
function forgetSource(source: Source) {
  investigationIntent = forgetInvestigationSource(investigationIntent, source);
  sources = sources.filter(item => item.id !== source.id);
  forgetSourceSaved(source);
  workspaceBaseline = pruneWorkspace(workspaceBaseline, source);
  if (!hasUnresolvedKnownSources(investigationIntent, sources)) investigationIntent = null;
  query.sourceIds = query.sourceIds.filter(id => id !== source.id);
  analysis = analysis ? { ...analysis, sources: analysis.sources.filter(result => result.sourceId !== source.id), apis: analysis.apis.filter(row => row.sourceId !== source.id) } : null;
  selectedKey = ''; operation = null; contextAnchor = null;
  invalidateDetails(); bumpEpoch();
  queueWorkspaceSave();
}
/**
 * A read the key keeper refused (P14b): it is not running, or it closed a source. The list is read again, so each source the keeper
 * opened says whether it can be read now, and a source the keeper closed leaves the page as a removal would. The error banner stays.
 */
/** A source the stopped key keeper holds cannot be read, so it is not left chosen (a run would only be refused). It can be chosen again once the list says it is held. */
function unselectUnreadableSources() {
  const unreadable = new Set(sources.filter(keeperUnreadable).map(source => source.id));
  if (unreadable.size && query.sourceIds.some(id => unreadable.has(id))) query.sourceIds = query.sourceIds.filter(id => !unreadable.has(id));
}
let keeperRefreshing = false;
async function refreshKeeperSources() {
  if (keeperRefreshing) return; // a burst of refused reads (and the list read itself) makes one re-read
  keeperRefreshing = true;
  let loaded: Source[];
  try { loaded = (await api.sources(keeperPreview())) ?? []; } catch { return; } finally { keeperRefreshing = false; }
  const now = new Set(loaded.map(source => source.id));
  for (const source of sources) if (!now.has(source.id)) forgetSource(source);
  sources = loaded;
  unselectUnreadableSources();
}
// Any read the keeper refuses (coverage, requests, payload, record, grids, an analysis) comes here from the one place that reads the server.
onKeeperRefusal(() => void refreshKeeperSources());
// A manual run (the button) reports a problem in the error banner and ignores clicks while loading. An automatic run (a scope change)
// never gets here with a problem, replaces an in-flight run, and leaves the banner alone.
export async function runAnalysis(auto = false) {
  if (loading && !auto) return;
  if (!auto) error = '';
  const problem = scopeProblem(query, dateInputsValid);
  if (problem) { if (!auto) error = problem; return; }
  if (auto) error = '';
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
    queueWorkspaceSave();
  } catch (cause) {
    if (run === analysisSequence && !(cause instanceof DOMException && cause.name === 'AbortError')) {
      const detail = message(cause);
      error = /exceeds a configured limit/i.test(detail)
        ? 'This comparison reached a processing safety limit. Your settings are kept. Try shorter periods, fewer sources, or a controller or action filter.'
        : detail;
    }
  } finally { if (run === analysisSequence) { loading = false; inflightSignature = ''; } }
}
export function cancelAnalysis() { analysisSequence++; analysisController?.abort(); inflightSignature = ''; loading = false; }
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
export function openSidePanel(which: 'patterns' | 'ledger' | 'export' | 'import' | 'blackbox') {
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
// A scope change (range chip, request type, source, filter) only marks the analysis on screen as out of date (runHint); the Run analysis button starts the run.
export function onScopeChange() { error = ''; }
/** A chosen hour of the hour grid becomes the primary window, with the hour before it as the reference. */
export function setWindowFromHour(window: RollupWindow) { Object.assign(query, hourWindowQuery(window)); controlsResetKey++; onScopeChange(); }
/**
 * Make the zoomed window the queried window and rerun the full analysis on it. The window fields come from the analysis on screen, so
 * the lens edges and the root agree. A refusal (root, run in progress, unapplied changes, unusable window) is said in the strip and
 * changes nothing. The plan is made before anything is written, so a double click gives one run and one trail entry.
 */
export function investigateWindow() {
  if (!analysis) return;
  const plan = planInvestigation({
    current: { start: analysis.query.start, end: analysis.query.end, baselineStart: analysis.query.baselineStart, baselineEnd: analysis.query.baselineEnd, bucketSeconds: analysis.query.bucketSeconds },
    view: zoomView.view, runLoading: loading, stale: displayedRunIsStale, trail: windowTrail
  });
  if ('refused' in plan) { zoomView.notify(plan.refused); return; }
  windowTrail = plan.trail;
  Object.assign(query, plan.patch);
  dateInputsValid = true; // every window field was just written with a valid value
  controlsResetKey++;
  void runAnalysis();
}
/** Undo the last promotion: restore the earlier window and reopen the lens on the window that was investigated. Replaces a run still in flight. */
export function backToPreviousWindow() {
  // Panel edits not run yet: not the run in flight (Back replaces that one), nor the one on screen.
  const covered = inflightSignature || (analysis ? queryFingerprint(analysis.query) : '');
  const plan = planBack({ trail: windowTrail, current: query, scope: query, unapplied: querySignature !== covered, datesValid: dateInputsValid });
  if (!plan.ok) { if (plan.reason) zoomView.notify(plan.reason); return; }
  windowTrail = plan.trail;
  Object.assign(query, plan.entry.from);
  dateInputsValid = true;
  controlsResetKey++;
  zoomView.expect(rootOf(plan.entry.from), plan.entry.lens);
  void runAnalysis(true).then(() => { if (!loading && error) zoomView.cancelExpect(); });
  if (!loading) zoomView.cancelExpect(); // no run started: nothing may reopen the lens later
}
// Removing one chip must not remount the controls: every group resyncs its boxes from the query, so a half-typed value elsewhere stays. Only Clear all is a reset.
export function removeChip(chip: FilterChip) { applyChipPatch(query, chip); onScopeChange(); }
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
  get screen() { return screen; },
  get activeCaseId() { return activeCaseId; },
  get lockState() { return lockState; },
  get dpapiCases() { return dpapiCases; },
  get lockError() { return lockError; }, set lockError(value) { lockError = value; },
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
  /** The promotion "Back to ..." would undo, or null when there is none or the query window was changed since. */
  get windowBack() { return backTarget(windowTrail, query); },
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

/** A case that was opened and is getting its MongoDB sources back (or needs the person to sign in). Read by the screens that would otherwise show the first-run connect step. */
export const restore = {
  get phase() { return restorePhase; },
  get active() { return restorePhase !== 'idle'; },
  get working() { return restorePhase === 'working'; },
  get unreachable() { return restorePhase === 'unreachable'; },
  get needsAttention() { return restoreWaiting(restorePhase); },
  get databases() { return restoreDatabases; },
  get label() { return restoreLabel; },
  reconnect: reconnectRestore,
  retry: retryRestore,
  cancel: cancelRestore,
  another: startNewConnection
};
