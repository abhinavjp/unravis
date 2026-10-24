<script lang="ts">
  import { onMount, tick, untrack } from 'svelte';
  import { api } from './lib/api';
  import { defaultQuery } from './lib/time';
  import { countEpochBuckets } from './lib/time';
  import { initialComparison, kindLabel, metricLabels, queryFingerprint, qualityByKindText, resultNotes, rankComparisons, stateLabel, unknownKindTip } from './lib/presentation';
  import { clearInvestigationSnapshot, createInvestigationSnapshot, forgetInvestigationSource, hasUnresolvedKnownSources, readInvestigationSnapshot, restoreInvestigation, serializeRecordFilters, shouldSelectAddedSource, writeInvestigationSnapshot } from './lib/investigation';
  import type { Analysis, BreakdownDim, BreakdownFilter, BreakdownRow, Comparison, ConnectionInput, ConnectionTestResult, Info, Page, Query, RecentConnection, RecordDetail, RecordFilters, RequestRecord, Rollup, Source, SourceInput, TLSMode } from './lib/types';
  import { applyDrill, breakdownDims, hasRecordFilter, mergeRecordFilter, recordFilterChips } from './lib/breakdown';
  import { theme } from './lib/theme.svelte';
  import { applyChipPatch, breadcrumb, depthOf, filterChips, moreFiltersCount, type FilterChip } from './lib/depth';
  import type { Lens } from './lib/pulse';
  import Pulse from './components/Pulse.svelte';
  import WhereList from './components/WhereList.svelte';
  import Inspector from './components/Inspector.svelte';
  import Breadcrumb from './components/Breadcrumb.svelte';
  import FilterChips from './components/FilterChips.svelte';
  import SourcePopover from './components/SourcePopover.svelte';
  import PhaseBar from './components/PhaseBar.svelte';
  import BreakdownPanel from './components/BreakdownPanel.svelte';
  import FailurePanel from './components/FailurePanel.svelte';
  import QueryControls from './components/QueryControls.svelte';
  import Charts from './components/Charts.svelte';
  import MultiTimeline from './components/MultiTimeline.svelte';
  import ActivityContext from './components/ActivityContext.svelte';
  import RequestVolume from './components/RequestVolume.svelte';
  import TrafficSlowness from './components/TrafficSlowness.svelte';
  import ComparisonTable from './components/ComparisonTable.svelte';
  import RequestTable from './components/RequestTable.svelte';
  import RecordDetailPanel from './components/RecordDetail.svelte';
  import OverviewStrip from './components/OverviewStrip.svelte';
  import IncompletePanel from './components/IncompletePanel.svelte';
  import { applyStaleFilters } from './lib/incomplete';
  import { drillHeading, operationDrill, type OverviewDrill } from './lib/overview';
  import { phaseSegments } from './lib/phases';
  import { rollupFor } from './lib/traffic';

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
  let querySnapshot = '';
  let connectionsOpen = $state(false);
  let connectionTab = $state<'saved' | 'new'>('saved');
  let drawerElement = $state<HTMLElement | undefined>(undefined);
  let drawerReturnFocus: HTMLElement | null = null;
  // Progressive depth. D0 pulse; D1 ranked "where is it"; D2 evidence for an operation or a list; D3 one record.
  let whereOpen = $state(false);
  let lens = $state<Lens>('requests');
  let compare = $state(false);
  let operation = $state<Rollup | null>(null);
  let inspectorTab = $state('timeline');
  let splitDim = $state<BreakdownDim | ''>('');
  let autoRan = false;
  let evidenceReturnFocus: HTMLElement | null = null;

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
  const crumbs = $derived(breadcrumb(nav, { evidence: evidenceName }));
  const filterChipList = $derived(filterChips(query));
  const moreCount = $derived(moreFiltersCount(query));
  const scopedQuery = $derived<Query | null>(breakdownQuery && operation ? operationQuery(breakdownQuery, operation) : breakdownQuery);
  const selectedStats = $derived(selected?.current);
  const inspectorTabs = $derived.by(() => {
    const tabs: Array<{ id: string; label: string }> = [];
    if (!operation && !selected) return tabs;
    if (selected) tabs.push({ id: 'timeline', label: 'Timeline' });
    if (selected && (phaseSegments(selectedStats?.phases) || (selectedStats?.phases?.payloadMs != null && Number.isFinite(selectedStats.phases.payloadMs)))) tabs.push({ id: 'phases', label: 'Phases' });
    const failed = (operation?.failures ?? 0) + (selectedStats?.clientErrors ?? 0) + (selectedStats?.serverErrors ?? 0);
    if (failed > 0 && scopedQuery) tabs.push({ id: 'failures', label: 'Failures' });
    const unfinished = (operation?.incomplete ?? 0) + (operation?.pending ?? 0) + (operation?.invalid ?? 0) + (selectedStats?.incomplete ?? 0) + (selectedStats?.pending ?? 0) + (selectedStats?.invalid ?? 0);
    if (unfinished > 0 && operation) tabs.push({ id: 'incomplete', label: 'Incomplete' });
    if (selected) tabs.push({ id: 'slowest', label: 'Slowest' });
    if (scopedQuery) tabs.push({ id: 'breakdown', label: 'Breakdown' });
    return tabs;
  });
  const activeTab = $derived(inspectorTabs.find(tab => tab.id === inspectorTab)?.id ?? inspectorTabs[0]?.id ?? '');
  const openKey = $derived(operation ? operation.controller + '\u0000' + operation.action + '\u0000' + (operation.kind ?? '') : '');
  const failureCohort = $derived<string[] | null>(operation ? operationGroups.map(group => group.key) : selected ? [selected.key] : null);
  const lensChoices: Array<{ id: Lens; label: string }> = [{ id: 'requests', label: 'Requests' }, { id: 'completion', label: 'Completion' }, { id: 'p95', label: 'P95' }, { id: 'client', label: '4xx' }, { id: 'server', label: '5xx' }];
  const groupLabel = (row: Comparison) => (sourceLabels.get(row.sourceId) ?? 'Source unavailable') + ' · ' + row.outcome;
  function pickSplit(dim: BreakdownDim | '', event: MouseEvent) {
    splitDim = splitDim === dim ? '' : dim;
    const menu = (event.currentTarget as HTMLElement).closest('details');
    if (menu) { menu.open = false; menu.querySelector('summary')?.focus(); }
  }

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

  onMount(() => { theme.start(); void initialize(); });
  async function initialize() {
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
      initError = message(cause);
    }
  }
  function setLocalInputs() {
  }
  function message(cause: unknown) { return cause instanceof Error ? cause.message : 'The request could not be completed.'; }
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
  function redactedAddress(uri: string) {
    return uri.replace(/(mongodb(?:\+srv)?:\/\/)[^/@]*@/gi, '$1[credentials]@');
  }
  function displayLastUsed(value: string) {
    const time = new Date(value);
    return Number.isFinite(time.getTime()) ? new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(time) : 'Recent';
  }
  function loadRecent(recent: RecentConnection) {
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
  async function forgetRecent(recent: RecentConnection) {
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
  async function saveConnectionDetails() {
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
  async function loadCACertificate(event: Event) {
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
  function clearCACertificate() {
    addCACertificate = ''; caCertificateName = '';
    if (caCertificateInput) caCertificateInput.value = '';
  }
  async function testConnection() {
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
    requestController?.abort(); recordController?.abort();
    requestController = null; recordController = null;
    requestPage = null; requestRows = []; record = null; drill = {};
    requestPageNumber = 1; requestState = ''; queryWide = false; drillOverview = null;
    requestError = ''; recordError = ''; recordLoadingId = '';
  }

  function setDatabaseSelected(database: string, checked: boolean) {
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

  function selectAllDatabases() {
    sourceError = ''; sourceSuccess = '';
    selectedDatabases = availableDatabases.slice(0, remainingSourceSlots);
    if (availableDatabases.length > remainingSourceSlots) {
      sourceSuccess = `Selected the first ${remainingSourceSlots} database${remainingSourceSlots === 1 ? '' : 's'} to fit the remaining profile capacity. The other databases remain available to add later.`;
    }
  }

  function clearConnectionDetails() {
    connectionSequence++;
    connectionController?.abort(); connectionController = null; connectionTesting = false;
    addUri = ''; addUsername = ''; addPassword = ''; addAuthSource = '';
    addTLSMode = 'system'; clearCACertificate(); savedConnectionId = ''; savedCredentialAvailable = false; savedCredentialSessionOnly = false; rememberPassword = false;
    discoveredDatabases = []; completedDatabases = []; selectedDatabases = []; manualDatabaseName = '';
    labelDatabasesIndividually = false;
    connectionWarning = ''; connectionError = ''; connectionTestedSignature = '';
    manualDatabase = false; sourceError = ''; sourceSuccess = '';
  }

  $effect(() => {
    const current = querySignature;
    if (querySnapshot && querySnapshot !== current) {
      analysisSequence++;
      analysisController?.abort();
      analysisController = null;
      loading = false;
      invalidateDetails();
      operation = null; selectedKey = ''; contextAnchor = null;
    }
    querySnapshot = current;
  });

  // The landing run: sources are chosen and the dates are valid, so the pulse is ready without a button press. Later scope changes run through onScopeChange.
  $effect(() => {
    if (autoRan || !info || !sources.length || !query.sourceIds.length || !dateInputsValid || !investigationReady) return;
    autoRan = true;
    untrack(() => { void runAnalysis(); });
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

  function resetInvestigation() {
    analysisSequence++;
    analysisController?.abort(); analysisController = null; loading = false;
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

  async function addSource(event: SubmitEvent) {
    event.preventDefault(); sourceError = ''; sourceSuccess = '';
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
        sourceSuccess = `Added ${addedCount} source profile${addedCount === 1 ? '' : 's'}. Connection details are kept so you can add more databases from this host.`;
      }
    }
    finally { sourceBusy = false; }
  }
  async function deleteSource(source: Source) {
    sourceError = ''; busyDelete = source.id;
    try {
      await api.deleteSource(source.id);
      investigationIntent = forgetInvestigationSource(investigationIntent, source);
      sources = sources.filter(item => item.id !== source.id);
      if (!hasUnresolvedKnownSources(investigationIntent, sources)) investigationIntent = null;
      query.sourceIds = query.sourceIds.filter(id => id !== source.id);
      analysis = analysis ? { ...analysis, sources: analysis.sources.filter(result => result.sourceId !== source.id), apis: analysis.apis.filter(row => row.sourceId !== source.id) } : null;
      selectedKey = ''; operation = null; contextAnchor = null;
      invalidateDetails();
    } catch (cause) { sourceError = message(cause); }
    finally { busyDelete = ''; }
  }
  async function runAnalysis() {
    if (loading) return;
    error = '';
    if (!dateInputsValid) { error = 'Complete every date and time with a valid local value before running a comparison.'; return; }
    if (!query.sourceIds.length) { error = 'Select at least one source before running a comparison.'; return; }
    const startMs = new Date(query.start).getTime();
    const endMs = new Date(query.end).getTime();
    const baselineStartMs = new Date(query.baselineStart).getTime();
    const baselineEndMs = new Date(query.baselineEnd).getTime();
    if (!(startMs < endMs) || !(baselineStartMs < baselineEndMs)) {
      error = 'Each window needs a start time earlier than its end time.'; return;
    }
    if (endMs - startMs > 7 * 86400_000 || baselineEndMs - baselineStartMs > 7 * 86400_000) { error = 'Each window can be at most 7 days.'; return; }
    if (baselineEndMs > startMs) { error = 'The reference window must end at or before the current window starts.'; return; }
    if (!(Number.isInteger(query.bucketSeconds) && query.bucketSeconds >= 10 && query.bucketSeconds <= 86400)) { error = 'Bucket size must be a whole number between 10 and 86,400 seconds.'; return; }
    if (countEpochBuckets(startMs, endMs, query.bucketSeconds) > 120 || countEpochBuckets(baselineStartMs, baselineEndMs, query.bucketSeconds) > 120) {
      error = 'This bucket size creates more than 120 time points. Increase the bucket size or shorten a window.'; return;
    }
    analysisController?.abort(); analysisController = new AbortController();
    const run = ++analysisSequence;
    // The previous analysis stays on screen (dimmed) while the new one loads, and nothing is auto-selected afterwards: the pulse is the landing view.
    loading = true; invalidateDetails(); selectedKey = ''; operation = null; contextAnchor = null;
    try {
      const result = await api.analyze(cloneQuery(), analysisController.signal);
      if (run !== analysisSequence) return;
      analysis = result;
      timelineKeys = rankComparisons(result.apis).filter(row => row.state === 'compared').slice(0, 3).map(row => row.key);
    } catch (cause) {
      if (run === analysisSequence && !(cause instanceof DOMException && cause.name === 'AbortError')) {
        const detail = message(cause);
        error = /exceeds a configured limit/i.test(detail)
          ? 'This comparison reached a processing safety limit. Your settings are kept. Try shorter periods, fewer sources, or a controller or action filter.'
          : detail;
      }
    } finally { if (run === analysisSequence) loading = false; }
  }
  function cancelAnalysis() { analysisSequence++; analysisController?.abort(); loading = false; }
  async function closeConnections() {
    connectionsOpen = false;
    await tick();
    drawerReturnFocus?.focus();
    drawerReturnFocus = null;
  }
  async function openConnections(tab: 'saved' | 'new' = 'saved') {
    if (sourceBusy || connectionTesting || historyMutationBusy) return;
    drawerReturnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    connectionTab = tab;
    connectionsOpen = true;
    await tick();
    drawerElement?.querySelector<HTMLElement>('button, input, select, [tabindex="0"]')?.focus();
  }
  async function startNewConnection() {
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
  function trapDrawerFocus(event: KeyboardEvent) {
    if (event.key === 'Escape') { event.preventDefault(); closeConnections(); return; }
    if (event.key !== 'Tab' || !drawerElement) return;
    const focusable = [...drawerElement.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), select:not(:disabled), [href], [tabindex]:not([tabindex="-1"])')];
    if (!focusable.length) return;
    const first = focusable[0]; const last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
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
  async function openOperation(row: Rollup, key = '') {
    if (!analysis) return;
    evidenceReturnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    contextAnchor = null; invalidateDetails(); selectedKey = '';
    operation = row; inspectorTab = 'timeline';
    queryWide = true; requestState = 'all'; drillOverview = operationDrill(row, analysis.query.kind);
    selectedKey = key || initialComparison(operationRows(row))?.key || '';
    await loadRequests('', 1);
    focusSoon('inspector-title');
  }
  function closeInspector() {
    requestController?.abort();
    selectedKey = ''; queryWide = false; requestState = ''; drillOverview = null; drill = {}; operation = null; contextAnchor = null;
    requestPage = null; requestRows = []; requestPageNumber = 1; requestError = ''; requestLoading = false;
    record = null; recordError = '';
    const back = evidenceReturnFocus; evidenceReturnFocus = null;
    void tick().then(() => { if (back?.isConnected) back.focus(); });
  }
  function openWhere(next: Lens) {
    lens = next; whereOpen = true;
    focusSoon('where-title');
  }
  // One step toward Pulse: the record, then the operation or list, then the ranked view.
  function stepUpOnce() {
    if (record != null) { record = null; recordError = ''; return; }
    if (hasContext) { closeInspector(); return; }
    if (whereOpen) { whereOpen = false; compare = false; splitDim = ''; }
  }
  function goTo(target: number) {
    for (let guard = 0; guard < 4 && depth > target; guard++) stepUpOnce();
  }
  function onWindowKey(event: KeyboardEvent) {
    if (event.key !== 'Escape' || event.defaultPrevented || connectionsOpen) return;
    const target = event.target;
    if (target instanceof HTMLElement && (target.tagName === 'SELECT' || target.closest('.popover'))) return;
    // An open menu or the filters panel closes first; only then does Esc step one level up.
    const open = document.querySelector<HTMLDetailsElement>('details.menu[open], details.more-filters[open]');
    const inside = open != null && target instanceof HTMLElement && open.contains(target);
    const loose = !(target instanceof HTMLElement) || target === document.body;
    if (open && (inside || loose)) { open.open = false; open.querySelector<HTMLElement>('summary')?.focus(); return; }
    stepUpOnce();
  }
  async function onScopeChange() { await tick(); void runAnalysis(); }
  async function removeChip(chip: FilterChip) { applyChipPatch(query, chip); controlsResetKey++; await onScopeChange(); }
  async function clearChips() { for (const chip of filterChipList) applyChipPatch(query, chip); controlsResetKey++; await onScopeChange(); }
  async function openFromTable(key: string) {
    const row = analysis?.apis.find(item => item.key === key);
    const rollup = row ? rollupFor(analysis?.rollups, row) : undefined;
    if (rollup) await openOperation(rollup, key); else await selectAPI(key);
  }
  function chooseGroup(key: string) {
    if (queryWide) { selectedKey = key; contextAnchor = null; } else void selectAPI(key);
  }
  async function drillInto(filter: BreakdownFilter, _row: BreakdownRow) {
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
  async function drillQuality(state: string) {
    evidenceReturnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null; operation = null;
    contextAnchor = null; invalidateDetails(); selectedKey = '';
    queryWide = true; requestState = state;
    await loadRequests('', 1);
    await tick();
    document.getElementById('request-history')?.scrollIntoView({ block: 'start' });
  }
  async function drillFromOverview(drill: OverviewDrill) {
    evidenceReturnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null; operation = null;
    contextAnchor = null; invalidateDetails(); selectedKey = '';
    queryWide = true; requestState = drill.state; drillOverview = drill;
    await loadRequests('', 1);
    await tick();
    document.getElementById('request-history')?.scrollIntoView({ block: 'start' });
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
  function clearDrill() {
    drill = {};
    if (operation) { void loadRequests('', 1); return; }
    closeInspector();
  }
  async function loadRequests(cursor: string, pageNumber: number) {
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
  async function showRecord(row: RequestRecord) {
    recordController?.abort(); recordController = new AbortController();
    recordError = ''; record = null; recordLoadingId = row.id;
    try { record = await api.record(row.sourceId, row.id, recordController.signal); }
    catch (cause) { if (!(cause instanceof DOMException && cause.name === 'AbortError')) recordError = message(cause); }
    finally { if (recordLoadingId === row.id) recordLoadingId = ''; }
  }
  async function exportCSV() {
    if (!selected && !queryWide) return;
    requestError = '';
    try {
      const result = await api.export(drillQuery(analysis?.query ?? query), queryWide ? '' : (selected?.key ?? ''), undefined, requestState, drillOverview?.order ?? '');
      const url = URL.createObjectURL(result.blob);
      const link = document.createElement('a'); link.href = url; link.download = 'unravis-requests.csv';
      document.body.append(link); link.click(); link.remove(); setTimeout(() => URL.revokeObjectURL(url), 1000);
      if (result.limited) requestError = 'Export reached a server-configured row or byte cap.';
    } catch (cause) { requestError = message(cause); }
  }

</script>

<svelte:head><title>Unravis</title></svelte:head>
<svelte:window onkeydown={onWindowKey} />

<div class="app-shell">
  <header class="topbar">
    <div class="brand-lockup"><div class="brand-mark" aria-hidden="true"><span></span><span></span><span></span></div><h1>Unravis</h1></div>
    {#if crumbs.length}<Breadcrumb {crumbs} onNavigate={(crumb) => goTo(crumb.depth)} />{:else}<div class="crumbs-spacer"></div>{/if}
    <div class="topbar-meta">
      {#if info}<span class:demo-badge={info.demo} class="mode-badge">{info.demo ? 'Demo data' : 'Live mode'}</span>{/if}
      {#if info?.demo}<span class="updated-label">Data reference time <time>{new Date(info.now).toLocaleString()}</time></span>{/if}
      {#if info && !firstRun}
        <SourcePopover groups={connectionGroups} selectedIds={query.sourceIds} total={sources.length} demo={info.demo} {busyDelete} {sourceError} {sourceSuccess} savedCount={recentConnections.length}
          onToggle={(id, checked) => { query.sourceIds = checked ? [...query.sourceIds.filter(item => item !== id), id] : query.sourceIds.filter(item => item !== id); onScopeChange(); }}
          onDelete={deleteSource} onManage={() => openConnections('saved')} onAdd={startNewConnection} />
      {/if}
      <button type="button" class="header-button theme-toggle" onclick={() => theme.cycle()} aria-label={'Theme: ' + theme.choice + '. Change theme'} title={'Theme: ' + theme.choice}>
        {#if theme.choice === 'light'}
          <svg viewBox="0 0 20 20" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true"><circle cx="10" cy="10" r="3.4"></circle><path d="M10 2.5v2M10 15.5v2M2.5 10h2M15.5 10h2M4.7 4.7l1.4 1.4M13.9 13.9l1.4 1.4M4.7 15.3l1.4-1.4M13.9 6.1l1.4-1.4"></path></svg>
        {:else if theme.choice === 'dark'}
          <svg viewBox="0 0 20 20" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" aria-hidden="true"><path d="M16.5 11.6A6.6 6.6 0 0 1 8.4 3.5a6.6 6.6 0 1 0 8.1 8.1Z"></path></svg>
        {:else}
          <svg viewBox="0 0 20 20" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" aria-hidden="true"><circle cx="10" cy="10" r="6.6"></circle><path d="M10 3.4a6.6 6.6 0 0 1 0 13.2Z" fill="currentColor" stroke="none"></path></svg>
        {/if}
      </button>
    </div>
  </header>

  {#if initError}<div class="banner error-banner" role="alert"><strong>Explorer unavailable</strong><span>{initError}</span><button class="quiet-button" onclick={() => { initError = ''; void initialize(); }}>Retry</button></div>{/if}

  {#if info && !firstRun}
    <section class="scope" aria-label="Scope" inert={connectionsOpen}>
      <div class="scope-main">
        {#key controlsResetKey}<QueryControls bind:query bind:zone bind:targetMs bind:dateInputsValid part="bar" groupNames={(analysis?.apis ?? []).map(row => row.controller + '.' + row.action)} now={info?.now ?? ''} {onScopeChange} />{/key}
        <details class="more-filters">
          <summary>More filters{#if moreCount > 0}<span class="count-badge" aria-label={moreCount + ' active'}>{moreCount}</span>{/if}</summary>
          <div class="more-body">
            {#key controlsResetKey}<QueryControls bind:query bind:zone bind:targetMs bind:dateInputsValid part="panel" groupNames={(analysis?.apis ?? []).map(row => row.controller + '.' + row.action)} now={info?.now ?? ''} {onScopeChange} />{/key}
          </div>
        </details>
        <div class="run-actions">{#if loading}<button class="quiet-button" onclick={cancelAnalysis}>Cancel run</button>{/if}<button class="primary-button" disabled={loading || !query.sourceIds.length || !dateInputsValid} onclick={runAnalysis}>{loading ? 'Comparing…' : 'Run comparison'}</button></div>
      </div>
      <FilterChips chips={filterChipList} onRemove={removeChip} onClear={clearChips} />
    </section>
  {/if}

    {#if connectionsOpen}
      <button class="drawer-scrim" aria-label="Close connections" onclick={closeConnections}></button>
      <div class="connections-drawer" bind:this={drawerElement} onkeydown={trapDrawerFocus} role="dialog" aria-modal="true" aria-labelledby="connections-title" tabindex="-1">
        <div class="drawer-header"><div><h2 id="connections-title">Connections</h2><p>Saved settings and database sources</p></div><button class="icon-button drawer-close" aria-label="Close connections" onclick={closeConnections}>×</button></div>
        <div class="connection-tabs"><button class:active={connectionTab === 'saved'} disabled={sourceBusy || connectionTesting || historyMutationBusy} onclick={() => connectionTab = 'saved'}>Saved connections</button><button class:active={connectionTab === 'new'} disabled={sourceBusy || connectionTesting || historyMutationBusy} onclick={startNewConnection}>New connection</button></div>
        {#if connectionTab === 'saved'}
          <div class="saved-connections-panel">
            {#if recentConnections.length}{#each recentConnections as recent (recent.id)}
              <article class="recent-card"><div class="recent-card-copy"><strong>{recent.label}</strong><div class="recent-username"><span>Username</span><b>{recent.username || 'Not provided'}</b></div><small>{redactedAddress(recent.uri)}</small><small>{recent.tlsMode === 'system' ? 'TLS · Verified certificate' : recent.tlsMode === 'custom' ? 'TLS · Certificate file' : recent.tlsMode === 'insecure' ? 'TLS · Verification skipped' : recent.tlsMode === 'off' ? 'TLS off' : 'TLS · From address'} · {recent.collection || 'ApiAuditLog'}</small><small>Saved {displayLastUsed(recent.lastUsedAt)}</small>{#if recent.hasCredentials}<span class="saved-badge">Password saved on this account</span>{/if}</div><div class="recent-card-actions"><button class="quiet-button" disabled={sourceBusy || connectionTesting || historyMutationBusy} onclick={() => { if (sourceBusy || connectionTesting || historyMutationBusy) return; clearConnectionDetails(); loadRecent(recent); connectionTab = 'new'; }}>{sourceBusy || connectionTesting || historyMutationBusy ? 'Working…' : 'Use settings'}</button><button class="quiet-button" onclick={() => void forgetRecent(recent)} disabled={sourceBusy || connectionTesting || historyMutationBusy || recentBusyId === recent.id}>{recentBusyId === recent.id ? 'Removing…' : 'Forget'}</button></div></article>
            {/each}{:else}<div class="drawer-empty"><strong>No saved connections</strong><p>Save connection settings to load them here later.</p><button class="primary-button" onclick={startNewConnection}>Create a connection</button></div>{/if}
            {#if recentHistoryError}<p class="connection-status warning" role="status">{recentHistoryError}</p>{/if}
            {#if recentSaveError}<p class="connection-status warning" role="status">{#if recentSaveErrorStage}<span class="error-stage">{recentSaveErrorStage}</span>{/if}{recentSaveError}</p>{/if}
            {#if recentSaveSuccess}<p class="connection-status" role="status">{recentSaveSuccess}</p>{/if}
          </div>
        {:else}
        {#if !info?.demo}
        <form class="source-form" onsubmit={addSource}>
          {#if sourceError}<div class="inline-error" role="alert">{sourceError}</div>{/if}
          {#if sourceSuccess}<p class="connection-status" role="status">{sourceSuccess}</p>{/if}
          {#if recentSaveError}<p class="connection-status warning" role="status">{#if recentSaveErrorStage}<span class="error-stage">{recentSaveErrorStage}</span>{/if}{recentSaveError}</p>{/if}
          {#if recentSaveSuccess}<p class="connection-status" role="status">{recentSaveSuccess}</p>{/if}
          <fieldset class="connection-section">
          <legend>Connection</legend>
          <label for="source-label">Connection name</label><input id="source-label" bind:value={addLabel} placeholder="UK production" autocomplete="off" disabled={sourceBusy} />
          <label for="source-uri">Server / connection address</label><input id="source-uri" bind:value={addUri} type="text" placeholder="uk-01-mongo.brainpayroll.co.uk" autocomplete="off" disabled={sourceBusy} />
          <p class="field-note">Enter a host name (uses MongoDB port 27017) or a full mongodb:// or mongodb+srv:// address.</p>
          </fieldset>
          <fieldset class="connection-section">
          <legend>Authentication</legend>
          <label for="source-username">Username</label><input id="source-username" bind:value={addUsername} type="text" autocomplete="username" disabled={sourceBusy} />
          <label for="source-password">Password</label><input id="source-password" bind:value={addPassword} type="password" autocomplete="new-password" disabled={sourceBusy} />
          {#if savedCredentialAvailable && savedConnectionId && !addPassword}
            <p class="field-note saved-password-note" role="status">
              {savedCredentialSessionOnly ? 'Password is available for this session only; it is not saved to this Windows account.' : 'A saved password will be used. The server never returns it to this page.'}
            </p>
          {:else if savedConnectionId && !addPassword}
            <p class="field-note" role="status">This recent has no saved password. Enter it again before testing; the server never returns passwords to this page.</p>
          {/if}
          <details class="auth-options">
            <summary>Authentication database</summary>
            <label for="source-auth-source">Authentication database</label><input id="source-auth-source" bind:value={addAuthSource} placeholder="URI setting or admin" autocomplete="off" disabled={sourceBusy} />
            <p class="field-note">Where this user account was created. It can differ from the audit database you select after connecting.</p>
          </details>
          </fieldset>
          <fieldset class="connection-section">
          <legend>SSL / TLS</legend>
          <label for="source-tls">Encryption and server trust</label>
          <select id="source-tls" bind:value={addTLSMode} disabled={sourceBusy}>
            <option value="system">On — verify certificate</option>
            <option value="custom">On — use certificate file</option>
            <option value="insecure">On — skip verification (advanced)</option>
            <option value="uri">From connection URI</option>
            <option value="off">Off</option>
          </select>
          {#if addTLSMode === 'system'}<p class="field-note">Uses Windows trusted certificates to verify the server and encrypt traffic.</p>{/if}
          {#if addTLSMode === 'custom'}
            <p class="field-note">For a private CA or self-signed server: import its public certificate (.pem/.crt). This verifies the server; it is not a client certificate or private key.</p>
            <label for="source-ca-file">Certificate authority file</label>
            <input id="source-ca-file" bind:this={caCertificateInput} type="file" accept=".pem,.crt,application/x-pem-file" onchange={loadCACertificate} disabled={sourceBusy} />
            {#if caCertificateName}<div class="certificate-file"><span>{caCertificateName} · {Math.ceil(new TextEncoder().encode(addCACertificate).length / 1024)} KiB</span><button class="quiet-button" type="button" onclick={clearCACertificate} disabled={sourceBusy}>Clear certificate</button></div>{/if}
          {/if}
          {#if addTLSMode === 'insecure'}<div class="tls-insecure-warning" role="alert"><strong>Certificate verification is disabled.</strong> Traffic remains encrypted, but the server certificate and host name are not checked. Accepts self-signed and other unverified certificates. Use only for a trusted network or temporary diagnosis.</div>{/if}
          {#if addTLSMode === 'off'}<p class="field-note">Traffic is not encrypted. Use only when the server is on a trusted local network.</p>{/if}
          </fieldset>
          <div class="remember-password-control">
            <label><input type="checkbox" bind:checked={rememberPassword} disabled={sourceBusy || connectionTesting || historyMutationBusy} /> <span>Save password on this Windows account</span></label>
            <p class="field-note">Applied when you save these details. You can change it before saving. Passwords are encrypted for the current Windows user when saved.</p>
          </div>
          <button class="secondary-button full-button" type="button" onclick={testConnection} disabled={connectionTesting || sourceBusy || historyMutationBusy || !addUri.trim()}>{connectionTesting ? 'Testing connection…' : 'Test connection & load databases'}</button>
          <button class="quiet-button full-button save-connection-button" type="button" onclick={saveConnectionDetails} disabled={connectionTesting || sourceBusy || historyMutationBusy || forgetInProgress || !addUri.trim() || !addCollection.trim() || (addTLSMode === 'custom' && !addCACertificate)}>{historyMutationBusy ? 'Saving connection…' : 'Save connection'}</button>
          <p class="field-note">You can save these details even if the server is unavailable. Saving does not test or connect to the database.</p>
          <button class="quiet-button clear-connection" type="button" onclick={clearConnectionDetails} disabled={sourceBusy || (!addUri && !addUsername && !addPassword && !addAuthSource && addTLSMode === 'system' && !addCACertificate && !savedConnectionId && !discoveredDatabases.length)}>Clear connection details</button>
          {#if connectionTesting}<p class="connection-status" role="status">Testing credentials and discovering databases…</p>{:else if historyMutationBusy}<p class="connection-status" role="status">Updating recent connection…</p>{/if}
          {#if connectionTestedSignature === connectionSignature && !manualDatabase}
            {#if discoveredDatabases.length}
              <section class="database-discovery" aria-label="Available databases">
                <div class="database-heading"><strong>Select databases</strong><span>{selectedDatabases.length} selected · {remainingSourceSlots} profile slots remaining</span></div>
                <div class="database-actions"><button type="button" class="quiet-button" onclick={selectAllDatabases} disabled={sourceBusy || remainingSourceSlots === 0}>Select all</button><button type="button" class="quiet-button" onclick={() => { selectedDatabases = []; sourceError = ''; sourceSuccess = ''; }} disabled={sourceBusy || !selectedDatabases.length}>Clear selection</button></div>
                <div class="database-checks">
                  {#each availableDatabases as database}
                    <label class="database-option"><input type="checkbox" checked={selectedDatabases.includes(database)} disabled={sourceBusy || (!selectedDatabases.includes(database) && selectedDatabases.length >= remainingSourceSlots)} onchange={(event) => setDatabaseSelected(database, event.currentTarget.checked)} /><span>{database}</span></label>
                  {/each}
                </div>
                {#if completedDatabases.length}<p class="field-note">{completedDatabases.length} database{completedDatabases.length === 1 ? ' was' : 's were'} already added from this connection and removed from the available choices.</p>{/if}
                {#if availableDatabases.length > remainingSourceSlots}<p class="field-note">This utility supports up to {maxSources} source profiles. Select up to the {remainingSourceSlots} remaining slot{remainingSourceSlots === 1 ? '' : 's'}; you can add the other databases later.</p>{/if}
              </section>
            {:else}
              <p class="connection-status" role="status">Connection succeeded, but the server returned no database names.</p>
            {/if}
            {#if connectionWarning}<p class="connection-status warning" role="status">{connectionWarning}</p>{/if}
          {/if}
          {#if connectionError}<div class="inline-error" role="alert">{#if connectionErrorStage}<span class="error-stage">{connectionErrorStage}</span>{/if}{connectionErrorContext ? `${connectionErrorContext}: ` : ''}{connectionError}</div>{/if}
          <button class="quiet-button manual-database-toggle" type="button" aria-expanded={manualDatabase} disabled={sourceBusy} onclick={() => { manualDatabase = !manualDatabase; manualDatabaseName = ''; selectedDatabases = []; }}>
            {manualDatabase ? 'Choose a discovered database instead' : 'Enter database manually'}
          </button>
          {#if manualDatabase}
            <label for="source-database-manual">Database name</label><input id="source-database-manual" bind:value={manualDatabaseName} placeholder="payroll" autocomplete="off" disabled={sourceBusy} />
            <p class="field-note">Use this when your account can read a database but cannot list database names. Saving still verifies access.</p>
          {/if}
          <label for="source-collection">Audit log collection</label><input id="source-collection" bind:value={addCollection} placeholder="ApiAuditLog" autocomplete="off" disabled={sourceBusy} />
          <p class="field-note">Exact collection where API logs are stored. Defaults to ApiAuditLog.</p>
          <button class="primary-button full-button" type="submit" disabled={sourceBusy || connectionTesting || historyMutationBusy || (addTLSMode === 'custom' && !addCACertificate) || remainingSourceSlots === 0 || !addLabel.trim() || !addUri.trim() || !(manualDatabase ? manualDatabaseName.trim() : selectedDatabases.length) || !addCollection.trim() || (!manualDatabase && connectionTestedSignature !== connectionSignature)}>{sourceBusy ? 'Adding databases…' : manualDatabase ? 'Add database' : 'Add selected databases'}</button>
          <p class="field-note">Credentials are used to test and save each database profile; they are not shown in the source list.</p>
        </form>
        {/if}
        {/if}
        </div>
    {/if}

    <main class="stage" class:with-drawer={depth === 2 && analysis != null} aria-busy={loading} inert={connectionsOpen}>
      <div class="stage-inner">
        {#if !info && !initError}
          <div class="loading-state"><div class="loading-pulse"></div><strong>Starting Unravis</strong><p>Loading sources and settings.</p></div>
        {:else if firstRun}
          <div class="welcome-state no-connections-state"><h3>Connect an audit source</h3><p>Load saved settings or create a MongoDB connection, then choose databases that contain API audit logs.</p><div class="empty-actions"><button class="primary-button" onclick={startNewConnection}>New connection</button><button class="secondary-button" onclick={() => openConnections('saved')}>Saved connections</button></div></div>
        {:else if info}
          {#if error}<div class="banner error-banner" role="alert"><strong>Comparison not run</strong><span>{error}</span></div>{/if}
          {#if analysis}
            {#if displayedRunIsStale && !loading}<div class="notice frozen-run-note" role="status">The figures below are from the last run. Press <strong>Run comparison</strong> to apply your changes.</div>{/if}
            {#if failingSources.length}
              <div class="banner source-failure" role="alert"><strong>{failingSources.length === analysis.sources.length ? 'No sources returned data' : 'Some sources failed'}</strong>
                {#each failingSources as failed}<span><b>{sourceLabels.get(failed.sourceId) ?? failed.sourceId}:</b> {failed.error || failed.indexWarning || 'Source unavailable'}</span>{/each}
              </div>
            {/if}
            <div class="stage-views" inert={loading}>
              <!-- D0 Pulse -->
              <div class="view" class:off={depth > 0}>
                <Pulse {analysis} {zone} onOpen={openWhere} />
                <div class="pulse-actions"><button type="button" class="primary-button" onclick={() => openWhere(lens)}>Where is it?</button><p>Tap a figure, or open the ranked list of operations.</p></div>
              </div>

              <!-- D1 Where -->
              <div class="view" class:off={depth === 0 || depth === 3} class:under={depth === 2}>
                <div class="view-head">
                  <div><h2 id="where-title" tabindex="-1">Where is it?</h2><p>Operations ranked by the figure you chose. Open one to see the evidence behind it.</p></div>
                  <div class="view-tools">
                    <button type="button" class="chip" aria-pressed={compare} onclick={() => compare = !compare}>Compare to earlier</button>
                    <details class="menu">
                      <summary>{splitDim ? 'Split by ' + (breakdownDims.find(item => item.dim === splitDim)?.label ?? '') : 'Split by…'}</summary>
                      <div class="menu-list">
                        {#each breakdownDims as item (item.dim)}<button type="button" aria-pressed={splitDim === item.dim} onclick={(event) => pickSplit(item.dim, event)}>{item.label}</button>{/each}
                        {#if splitDim}<button type="button" onclick={(event) => pickSplit('', event)}>No split</button>{/if}
                      </div>
                    </details>
                  </div>
                </div>
                <div class="lens-chips" role="group" aria-label="Rank by">
                  {#each lensChoices as item (item.id)}<button type="button" class="chip" aria-pressed={lens === item.id} title={item.id === 'p95' ? metricLabels[analysis.query.metric] : undefined} onclick={() => lens = item.id}>{item.label}</button>{/each}
                </div>
                <WhereList {analysis} {lens} {compare} {openKey} onOpen={(row) => openOperation(row)} />
                {#if splitDim && breakdownQuery}<BreakdownPanel query={breakdownQuery} {sourceLabels} lockedDim={splitDim} panelId="split-breakdown" onDrill={drillInto} />{/if}
                <div class="disclosures">
                  <details class="disclosure">
                    <summary>Quality of the data<small>timing, results and pending work</small></summary>
                    <div class="disclosure-body">
                      <OverviewStrip overview={analysis.overview ?? []} outcomeFilter={analysis.query.outcome} kindFilter={analysis.query.kind} onDrill={drillFromOverview}>
                        {#snippet quality()}{#if analysis}
                      <div class="quality-strip">
                        <div><strong>{analysis.apis.length.toLocaleString()}</strong><span>API groups</span></div>
                        <div><button type="button" class="quality-count" disabled={!analysis.quality.incomplete} title="List the requests behind this count" onclick={() => drillQuality('incomplete')}><strong>{analysis.quality.incomplete.toLocaleString()}</strong><span>Missing timing</span></button></div>
                        <div><button type="button" class="quality-count" disabled={!analysis.quality.invalid} title="List the requests behind this count" onclick={() => drillQuality('invalid')}><strong>{analysis.quality.invalid.toLocaleString()}</strong><span>Unusable timing</span></button></div>
                        <div><button type="button" class="quality-count" disabled={!analysis.quality.unknownOutcome} title="List the requests behind this count" onclick={() => drillQuality('unclassified')}><strong>{analysis.quality.unknownOutcome.toLocaleString()}</strong><span>Unclassified result</span></button></div>
                        {#if analysis.quality.pendingAsync}<div><button type="button" class="quality-count" title="List the requests behind this count" onclick={() => drillQuality('pending')}><strong>{analysis.quality.pendingAsync.toLocaleString()}</strong><span>pending async</span></button></div>{/if}
                        {#if qualityByKindText(analysis.quality)}<div class="quality-filter-note" title={unknownKindTip}><span>{qualityByKindText(analysis.quality)}</span></div>{/if}
                        {#if analysis.query.outcome === 'client_error' || analysis.query.outcome === 'server_error'}<div class="quality-filter-note"><span>Counts above cover only the selected status class.</span></div>{/if}
                      <p>Response times are estimates. A comparison shows change, not a health verdict.</p>
                      <details class="quality-definitions"><summary>What these counts mean</summary><p><strong>Missing timing:</strong> no complete duration was recorded. <strong>Unusable timing:</strong> a duration was present but could not be used. <strong>Unclassified result:</strong> the request could not be identified as a success or failure. <strong>Pending async:</strong> an asynchronous request that only answered 202 Accepted; its work is not finished, so it is counted as missing timing and never as a response-time sample.</p></details>
                      </div>
                        {/if}{/snippet}
                      </OverviewStrip>
                      <IncompletePanel rollups={analysis.rollups ?? []} kindFilter={analysis.query.kind} onDrill={drillFromOverview} />
                    </div>
                  </details>
                  <details class="disclosure">
                    <summary>Request volume<small>every interval in the period</small></summary>
                    <div class="disclosure-body"><RequestVolume {analysis} {zone} /></div>
                  </details>
                  <details class="disclosure">
                    <summary>All {analysis.apis.length.toLocaleString()} API groups<small>one source, action, type and outcome each</small></summary>
                    <div class="disclosure-body">
                      <section class="data-section" id="api-group-results">
                        <div class="section-heading"><div><h3>Explore {analysis.apis.length.toLocaleString()} API groups</h3><p>Largest slowdowns first. Each group keeps one source, API action, request type, and outcome together. P95 is the time within which about 95 of 100 requests finish.</p></div></div>
                        <ComparisonTable rows={analysis.apis} rollups={analysis.rollups ?? []} timeline={analysis.timeline} kindFilter={analysis.query.kind} {selectedKey} {timelineKeys} onToggleTimeline={(key) => timelineKeys = timelineKeys.includes(key) ? timelineKeys.filter(item => item !== key) : timelineKeys.length < 8 ? [...timelineKeys, key] : timelineKeys} sources={sourceLabels} outcomeFilter={analysis.query.outcome} onDrill={drillFromOverview} onSelect={openFromTable} />
                      </section>
                      <MultiTimeline {analysis} keys={timelineKeys} sources={sourceLabels} {zone} onRemove={(key) => timelineKeys = timelineKeys.filter(item => item !== key)} />
                    </div>
                  </details>
                  <details class="disclosure">
                    <summary>About these results<small>calculation notes and sample sizes</small></summary>
                    <div class="disclosure-body"><div class="calculation-notes">{#each calculationNotes as note}<p>{note}</p>{/each}</div></div>
                  </details>
                </div>
              </div>

              <!-- D3 Record -->
              <div class="view record-view" class:off={depth !== 3}>
                <button type="button" class="back-to-groups" onclick={() => { record = null; recordError = ''; }}>Back to the evidence</button>
                {#if recordError}<div class="inline-error" role="alert">{recordError}</div>{/if}
                {#if record}<RecordDetailPanel detail={record} sourceLabel={sourceLabels.get(record.sourceId) ?? record.sourceId} onClose={() => record = null} />{/if}
              </div>
            </div>

            <!-- D2 Evidence -->
            <Inspector off={depth !== 2} title={evidenceName} subtitle={operation ? kindLabel(operation.kind ?? 'unknown') + ' · ' + operation.requests.toLocaleString() + ' requests' : ''} tabs={inspectorTabs} active={activeTab} onTab={(id) => inspectorTab = id} onClose={closeInspector}>
              {#if activeTab === 'timeline' && selected}
                {#if operationGroups.length > 1}
                  <div class="group-pick"><label for="group-select">Group</label>
                    <select id="group-select" value={selectedKey} onchange={(event) => chooseGroup(event.currentTarget.value)}>{#each operationGroups as group (group.key)}<option value={group.key}>{groupLabel(group)}</option>{/each}</select>
                  </div>
                {/if}
                <Charts {analysis} {selected} {zone} {targetMs} />
              {:else if activeTab === 'phases' && selected}
                {#if operationGroups.length > 1}
                  <div class="group-pick"><label for="group-select-phases">Group</label>
                    <select id="group-select-phases" value={selectedKey} onchange={(event) => chooseGroup(event.currentTarget.value)}>{#each operationGroups as group (group.key)}<option value={group.key}>{groupLabel(group)}</option>{/each}</select>
                  </div>
                {/if}
                <section class="phase-group" aria-label="Where the time goes">
                  <p>{sourceLabels.get(selected.sourceId) ?? 'Source unavailable'} · <span title={selected.kind === 'unknown' ? unknownKindTip : undefined}>{kindLabel(selected.kind)}</span> · {selected.outcome}</p>
                  <PhaseBar phases={selected.current.phases ?? null} delta={selected.phaseDeltaMs ?? null} />
                  <div class="selected-api-stats"><span><strong>{selected.current.count.toLocaleString()}</strong> current timings</span><span><strong>{selected.reference.count.toLocaleString()}</strong> reference timings</span><span><strong>{selected.p95Ratio == null ? 'Unavailable' : '≈ ' + selected.p95Ratio.toFixed(2) + '×'}</strong> P95 vs reference</span>{#if targetMs != null}<span>Target {targetMs} ms: {targetAssessment}</span>{/if}</div>
                  {#if selected.current.count < 20 || selected.reference.count < 20}<p class="sample-note">{selected.current.count === 0 ? 'No usable timings in the current period.' : selected.reference.count === 0 ? 'No usable timings in the reference period.' : 'Fewer than 20 usable timings in at least one period.'} A slowdown comparison needs at least 20 usable timings in each period.</p>{/if}
                </section>
              {:else if activeTab === 'failures' && scopedQuery}
                <FailurePanel {analysis} query={scopedQuery} {sourceLabels} {zone} onDrill={drillInto} cohortKeys={failureCohort} />
              {:else if activeTab === 'incomplete' && operation}
                <IncompletePanel rollups={[operation]} kindFilter={analysis.query.kind} onDrill={drillFromOverview} />
              {:else if activeTab === 'slowest' && selected}
                <TrafficSlowness {analysis} {zone} {sourceLabels} cohort={selected} onContext={async (row) => { contextAnchor = row; await tick(); document.getElementById('nearby-activity')?.scrollIntoView({ block: 'start' }); }} />
              {:else if activeTab === 'breakdown' && scopedQuery}
                <BreakdownPanel query={scopedQuery} {sourceLabels} onDrill={drillInto} />
              {/if}
              <section class="data-section request-section" id="request-history">
                <div class="section-heading"><div><h3>Request history</h3><p>{queryWide ? (drillOverview ? drillHeading(drillOverview) : stateLabel(requestState)) + ' · ' + (operation ? 'every group of this operation' : 'every API group in this comparison') : selected ? selected.controller + '.' + selected.action + ' · ' + kindLabel(selected.kind) + ' · ' + selected.outcome : drillActive ? 'Requests matching ' + drillChips.map(chip => chip.text).join(' · ') + '.' : 'Choose an operation to list its requests.'} Total ms is full request time; API ms is API processing time. Inspect opens every stored field and the captured request and response text.</p></div>
                  {#if selected || queryWide}<button class="secondary-button" onclick={exportCSV}>Export CSV</button>{/if}
                  {#if drillActive}<button class="quiet-button" onclick={clearDrill}>{operation ? 'Back to all of this operation' : 'Clear breakdown filter'}</button>{/if}
                </div>
                {#if requestError}<div class="inline-error" role="alert">{requestError}</div>{/if}
                {#if requestPage?.sources.some(source => !source.ok)}<div class="source-failure compact-failure" role="alert">{#each requestPage.sources.filter(source => !source.ok) as failed}<span>{sourceLabels.get(failed.sourceId) ?? failed.sourceId}: {failed.error}</span>{/each}</div>{/if}
                <RequestTable rows={requestRows} {sourceLabels} loading={requestLoading} pageNumber={requestPageNumber} nextCursor={requestPage?.nextCursor ?? ''} truncated={requestPage?.truncated ?? false} recordId={recordLoadingId} onFirst={() => loadRequests('', 1)} onNext={() => loadRequests(requestPage?.nextCursor ?? '', requestPageNumber + 1)} onInspect={showRecord} onContext={async (row) => { contextAnchor = row; await tick(); document.getElementById('nearby-activity')?.scrollIntoView({ block: 'start' }); }} />
              </section>
              {#if contextAnchor}<ActivityContext anchor={contextAnchor} sourceIds={analysis.query.sourceIds} {sourceLabels} {zone} />{/if}
            </Inspector>
          {:else if loading}
            <div class="skeleton" role="status" aria-label="Reading selected audit sources">
              <div class="skeleton-figures"><div class="skeleton-block"></div><div class="skeleton-block"></div><div class="skeleton-block"></div><div class="skeleton-block"></div><div class="skeleton-block"></div></div>
              <div class="skeleton-block skeleton-hero"></div>
            </div>
          {:else}
            <div class="welcome-state">
              <h3>Start an API comparison</h3>
              {#if info.demo}
                <p>Sample audit sources are ready. Choose the period and filters above, then run a comparison.</p>
              {:else}
                <p>Your sources are ready. Choose the period and filters above, then run a comparison.</p>
              {/if}
              {#if sources.length && !query.sourceIds.length}<p>Choose at least one database source from the Sources button above to run a comparison.</p>{/if}
              <button class="primary-button" disabled={!query.sourceIds.length || !dateInputsValid} onclick={runAnalysis}>Run comparison</button>
            </div>
          {/if}
          <div class="notice frozen-run-note" role="status">{restoredInTab ? 'Restored this tab’s investigation filters. ' : ''}{investigationNotice} <button class="quiet-button" onclick={resetInvestigation}>Reset investigation</button></div>
        {/if}
      </div>
    </main>
</div>
