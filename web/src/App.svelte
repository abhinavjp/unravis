<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { api } from './lib/api';
  import { defaultQuery } from './lib/time';
  import { countEpochBuckets } from './lib/time';
  import { initialComparison, queryFingerprint, resultNotes, rankComparisons } from './lib/presentation';
  import { clearInvestigationSnapshot, createInvestigationSnapshot, forgetInvestigationSource, hasUnresolvedKnownSources, readInvestigationSnapshot, restoreInvestigation, shouldSelectAddedSource, writeInvestigationSnapshot } from './lib/investigation';
import type { Analysis, ConnectionInput, ConnectionTestResult, Info, Page, Query, RecentConnection, RecordDetail, RequestRecord, Source, SourceInput, TLSMode } from './lib/types';
  import QueryControls from './components/QueryControls.svelte';
  import Charts from './components/Charts.svelte';
  import MultiTimeline from './components/MultiTimeline.svelte';
  import ActivityContext from './components/ActivityContext.svelte';
  import RequestVolume from './components/RequestVolume.svelte';
  import TrafficSlowness from './components/TrafficSlowness.svelte';
  import ComparisonTable from './components/ComparisonTable.svelte';
  import RequestTable from './components/RequestTable.svelte';
  import RecordDetailPanel from './components/RecordDetail.svelte';

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
  let contextAnchor = $state<RequestRecord | null>(null);
  let timelineKeys = $state<string[]>([]);
  let requestPage = $state<Page | null>(null);
  let requestRows = $state<RequestRecord[]>([]);
  let requestPageNumber = $state(1);
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
  const targetAssessment = $derived.by(() => {
    if (targetMs == null || !Number.isFinite(targetMs) || targetMs < 0 || !selected) return '';
    const p95 = selected.current.p95;
    if (!p95) return 'No current P95 estimate is available for this API group.';
    if (p95.upperMs != null && p95.upperMs <= targetMs) return 'The current P95 estimate is at or below your target.';
    if (p95.lowerMs > targetMs) return 'The current P95 estimate is above your target.';
    return 'The estimated P95 range spans your target, so we cannot tell which side it falls on.';
  });

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

  onMount(() => { void initialize(); });
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
    requestPage = null; requestRows = []; record = null;
    requestPageNumber = 1;
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
    }
    querySnapshot = current;
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
    analysis = null; selectedKey = ''; error = '';
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
      selectedKey = '';
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
    loading = true; invalidateDetails(); selectedKey = ''; analysis = null;
    try {
      const result = await api.analyze(cloneQuery(), analysisController.signal);
      if (run !== analysisSequence) return;
      analysis = result;
      timelineKeys = rankComparisons(result.apis).filter(row => row.state === 'compared').slice(0, 3).map(row => row.key);
      const firstHealthy = initialComparison(result.apis);
      if (firstHealthy) void selectAPI(firstHealthy.key);
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
    if (event.key === 'Escape') { closeConnections(); return; }
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
  async function loadRequests(cursor: string, pageNumber: number) {
    if (!selectedKey || !analysis) return;
    requestController?.abort(); requestController = new AbortController();
    const run = ++requestSequence;
    requestLoading = true; requestError = '';
    try {
      const page = await api.requests(JSON.parse(JSON.stringify(analysis.query)) as Query, selectedKey, cursor, requestController.signal);
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
    if (!selected) return;
    requestError = '';
    try {
      const result = await api.export(JSON.parse(JSON.stringify(analysis?.query ?? query)) as Query, selected.key);
      const url = URL.createObjectURL(result.blob);
      const link = document.createElement('a'); link.href = url; link.download = 'api-audit-requests.csv';
      document.body.append(link); link.click(); link.remove(); setTimeout(() => URL.revokeObjectURL(url), 1000);
      if (result.limited) requestError = 'Export reached a server-configured row or byte cap.';
    } catch (cause) { requestError = message(cause); }
  }

</script>

<svelte:head><title>API Audit Explorer</title></svelte:head>

<div class="app-shell">
  <header class="topbar">
    <div class="brand-lockup"><div class="brand-mark" aria-hidden="true"><span></span><span></span><span></span></div><div><h1>API Audit Explorer</h1><p>Compare each API with its own recent history</p></div></div>
    <div class="topbar-meta">
      {#if info}<span class:demo-badge={info.demo} class="mode-badge">{info.demo ? 'Demo data' : 'Live mode'}</span>{/if}
      {#if info?.demo}<span class="updated-label">Data reference time <time>{new Date(info.now).toLocaleString()}</time></span>{/if}
    </div>
  </header>

  {#if initError}<div class="banner error-banner" role="alert"><strong>Explorer unavailable</strong><span>{initError}</span><button class="quiet-button" onclick={() => { initError = ''; void initialize(); }}>Retry</button></div>{/if}
  <div class="workspace">
    <section class="source-sidebar" aria-label="Audit sources" inert={connectionsOpen}>
      <div class="sidebar-title"><div><h2>Sources</h2><p>{query.sourceIds.length} selected · {sources.length} available</p></div></div>
      {#if !info?.demo}<button class="secondary-button connection-launch" onclick={() => openConnections('saved')}>Connections <span>{recentConnections.length} saved</span></button>{:else}<p class="demo-note">Demo sources are provided by the sample repository.</p>{/if}
      {#if sourceError}<div class="inline-error" role="alert">{sourceError}</div>{/if}
      {#if sourceSuccess}<div class="connection-status" role="status">{sourceSuccess}</div>{/if}
      <div class="source-list">
        {#each connectionGroups as group (group.id)}
          <section class="source-group" aria-label={group.label}>
            <div class="source-group-heading"><strong>{group.label}</strong><small>{group.sources.length} audit source{group.sources.length === 1 ? '' : 's'}</small></div>
            {#each group.sources as source (source.id)}
              <div class="source-item">
                <label class="source-choice"><input type="checkbox" checked={query.sourceIds.includes(source.id)} onchange={(event) => { const checked = event.currentTarget.checked; query.sourceIds = checked ? [...query.sourceIds, source.id] : query.sourceIds.filter(id => id !== source.id); }} /><span><strong>{source.database}</strong><small>{source.collection}</small></span></label>
                <button class="icon-button" aria-label={'Remove ' + source.label} title="Remove source" disabled={busyDelete === source.id} onclick={() => deleteSource(source)}>×</button>
              </div>
            {/each}
          </section>
        {:else}
          <div class="source-empty"><span class="empty-rings" aria-hidden="true"></span><strong>{info?.demo ? 'No sample sources' : 'No connections yet'}</strong><p>{info?.demo ? 'This demo has no audit sources configured.' : 'Add a connection or load saved settings, then choose audit databases.'}</p>{#if !info?.demo}<button class="primary-button" onclick={startNewConnection}>Add a connection</button><button class="quiet-button" onclick={() => openConnections('saved')}>View saved connections</button>{/if}</div>
        {/each}
      </div>
      {#if sources.length && !info?.demo}<button class="quiet-button manage-connections" onclick={() => openConnections('saved')}>Manage connections</button>{/if}
    </section>

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

    <main class="analysis-workspace" inert={connectionsOpen}>
      <div class="frozen-run-note" role="status">{restoredInTab ? 'Restored this tab’s investigation filters. ' : ''}{investigationNotice} <button class="quiet-button" onclick={resetInvestigation}>Reset investigation</button></div>
      {#key controlsResetKey}<QueryControls bind:query bind:zone bind:targetMs bind:dateInputsValid />{/key}
      <section class="analysis-main">
        <div class="analysis-heading">
          <div><h2>Compare API speed</h2><p>See how the selected period compares with earlier requests.</p></div>
          <div class="run-actions">{#if loading}<button class="quiet-button" onclick={cancelAnalysis}>Cancel run</button>{/if}<button class="primary-button" disabled={loading || !query.sourceIds.length || !dateInputsValid} onclick={runAnalysis}>{loading ? 'Comparing…' : 'Run comparison'}</button></div>
        </div>
        {#if error}<div class="banner error-banner" role="alert"><strong>Comparison not run</strong><span>{error}</span></div>{/if}
        {#if analysis}
          {#if displayedRunIsStale}<div class="frozen-run-note">The charts and reference window below remain from the last run. Click <strong>Run comparison</strong> to apply your edited windows and filters.</div>{/if}
          {#if failingSources.length}
            <div class="banner source-failure" role="alert"><strong>{failingSources.length === analysis.sources.length ? 'No sources returned data' : 'Some sources failed'}</strong>
              {#each failingSources as failed}<span><b>{sourceLabels.get(failed.sourceId) ?? failed.sourceId}:</b> {failed.error || failed.indexWarning || 'Source unavailable'}</span>{/each}
            </div>
          {/if}
          <details class="result-notes"><summary>About these results and sample sizes</summary><div class="calculation-notes">{#each calculationNotes as note}<p>{note}</p>{/each}</div></details>
          <div class="quality-strip">
            <div><strong>{analysis.apis.length.toLocaleString()}</strong><span>API groups</span></div>
            <div><strong>{analysis.quality.incomplete.toLocaleString()}</strong><span>Missing timing</span></div>
            <div><strong>{analysis.quality.invalid.toLocaleString()}</strong><span>Unusable timing</span></div>
            <div><strong>{analysis.quality.unknownOutcome.toLocaleString()}</strong><span>Unclassified result</span></div>
            {#if analysis.quality.pendingAsync}<div><strong>{analysis.quality.pendingAsync.toLocaleString()}</strong><span>pending async</span></div>{/if}
            {#if analysis.query.outcome === 'client_error' || analysis.query.outcome === 'server_error'}<div class="quality-filter-note"><span>Counts above cover only the selected status class.</span></div>{/if}
          <p>Response times are estimates. A comparison shows change, not a health verdict.</p>
          <details class="quality-definitions"><summary>What these counts mean</summary><p><strong>Missing timing:</strong> no complete duration was recorded. <strong>Unusable timing:</strong> a duration was present but could not be used. <strong>Unclassified result:</strong> the request could not be identified as a success or failure. <strong>Pending async:</strong> an asynchronous request that only answered 202 Accepted; its work is not finished, so it is counted as missing timing and never as a response-time sample.</p></details>
          </div>
          <RequestVolume {analysis} {zone} />
          <TrafficSlowness {analysis} {zone} {sourceLabels} cohort={selected} onContext={async(row)=>{contextAnchor=row;await tick();document.getElementById('nearby-activity')?.scrollIntoView({block:'start'});}} />
          <section class="data-section" id="api-group-results">
            <div class="section-heading"><div><h3>Explore {analysis.apis.length.toLocaleString()} API groups</h3><p>Largest slowdowns first. Each group keeps one source, API action, request type, and outcome together. P95 is the time within which about 95 of 100 requests finish.</p></div></div>
            <ComparisonTable rows={analysis.apis} rollups={analysis.rollups ?? []} timeline={analysis.timeline} kindFilter={analysis.query.kind} {selectedKey} {timelineKeys} onToggleTimeline={(key) => timelineKeys = timelineKeys.includes(key) ? timelineKeys.filter(item => item !== key) : timelineKeys.length < 8 ? [...timelineKeys, key] : timelineKeys} sources={sourceLabels} outcomeFilter={analysis.query.outcome} onSelect={async (key) => { void selectAPI(key); await tick(); const details = document.getElementById('selected-api-details'); details?.scrollIntoView({ block: 'start' }); details?.focus({ preventScroll: true }); }} />
          </section>
          <MultiTimeline {analysis} keys={timelineKeys} sources={sourceLabels} {zone} onRemove={(key) => timelineKeys = timelineKeys.filter(item => item !== key)} />
          {#if selected}
            <section class="selected-api-heading" id="selected-api-details" aria-label="Selected API" aria-live="polite" tabindex="-1">
              <a class="back-to-groups" href="#api-group-results">Back to API groups</a>
              <h3>{selected.controller}.{selected.action}</h3>
              <p>{sourceLabels.get(selected.sourceId) ?? 'Source unavailable'} · {selected.kind} · {selected.outcome}</p>
              <div class="selected-api-stats"><span><strong>{selected.current.count.toLocaleString()}</strong> current timings</span><span><strong>{selected.reference.count.toLocaleString()}</strong> reference timings</span><span><strong>{selected.p95Ratio == null ? 'Unavailable' : '≈ ' + selected.p95Ratio.toFixed(2) + '×'}</strong> P95 vs reference</span>{#if targetMs != null}<span>Target {targetMs} ms: {targetAssessment}</span>{/if}</div>
              {#if selected.current.count < 20 || selected.reference.count < 20}<p class="sample-note">{selected.current.count === 0 ? 'No usable timings in the current period.' : selected.reference.count === 0 ? 'No usable timings in the reference period.' : 'Fewer than 20 usable timings in at least one period.'} A slowdown comparison needs at least 20 usable timings in each period.</p>{/if}
            </section>
            <Charts {analysis} {selected} {zone} {targetMs} />
          {/if}
          <section class="data-section request-section">
            <div class="section-heading"><div><h3>Request history</h3><p>{selected ? selected.controller + '.' + selected.action + ' · ' + selected.kind + ' · ' + selected.outcome : 'Choose an API group above.'} Total ms is full request time; API ms is API processing time. Inspect opens every stored field and the captured request and response text.</p></div>
              {#if selected}<button class="secondary-button" onclick={exportCSV}>Export CSV</button>{/if}
            </div>
            {#if requestError}<div class="inline-error" role="alert">{requestError}</div>{/if}
            {#if requestPage?.sources.some(source => !source.ok)}<div class="source-failure compact-failure" role="alert">{#each requestPage.sources.filter(source => !source.ok) as failed}<span>{sourceLabels.get(failed.sourceId) ?? failed.sourceId}: {failed.error}</span>{/each}</div>{/if}
            <RequestTable rows={requestRows} {sourceLabels} loading={requestLoading} pageNumber={requestPageNumber} nextCursor={requestPage?.nextCursor ?? ''} truncated={requestPage?.truncated ?? false} recordId={recordLoadingId} onFirst={() => loadRequests('', 1)} onNext={() => loadRequests(requestPage?.nextCursor ?? '', requestPageNumber + 1)} onInspect={showRecord} onContext={async (row) => {contextAnchor=row;await tick();document.getElementById('nearby-activity')?.scrollIntoView({block:'start'});}} />
            {#if recordError}<div class="inline-error" role="alert">{recordError}</div>{/if}
            <RecordDetailPanel detail={record} sourceLabel={record ? (sourceLabels.get(record.sourceId) ?? record.sourceId) : ''} onClose={() => record = null} />
          </section>
          {#if contextAnchor}<ActivityContext anchor={contextAnchor} sourceIds={analysis.query.sourceIds} {sourceLabels} {zone} />{/if}
        {:else if loading}
          <div class="loading-state"><div class="loading-pulse"></div><strong>Reading selected audit sources</strong><p>Current and reference windows are aggregated separately.</p></div>
        {:else if !sources.length && !info?.demo}
          <div class="welcome-state no-connections-state"><h3>Connect an audit source</h3><p>Load saved settings or create a MongoDB connection, then choose databases that contain API audit logs.</p><div class="empty-actions"><button class="primary-button" onclick={startNewConnection}>New connection</button><button class="secondary-button" onclick={() => openConnections('saved')}>Saved connections</button></div></div>
        {:else}
          <div class="welcome-state">
          <h3>Start an API comparison</h3>
            {#if info?.demo}
              <p>Sample audit sources are ready. Choose the periods and filters above, then run a comparison.</p>
            {:else}
              <p>Your sources are ready. Choose the periods and filters above, then run a comparison.</p>
            {/if}
            {#if sources.length && !query.sourceIds.length}<p>Select at least one database source in the left panel to run a comparison.</p>{/if}
            <button class="primary-button" disabled={!query.sourceIds.length || !dateInputsValid} onclick={runAnalysis}>Run comparison</button>
          </div>
        {/if}
      </section>
    </main>
  </div>
</div>
