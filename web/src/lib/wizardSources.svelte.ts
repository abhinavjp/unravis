import { api } from './api';
import { CaseApiError } from './cases';
import { cachedEntryState, classifyError, discoverClient, locateFinding, probeReachable, scanExpired, waitForScan } from './discover';
import type { DetectorProgress, DiscoverClient, EntryState, Finding, Gap, LocateResult, ProbeResult, ScanHost } from './discover';
import { DISCOVER_TEXT, problemText } from './copy/discoverCopy';
import { safeServerLine, scanDroppedText, envsClearedText } from './copy/wizardSourcesCopy';
import { draftEnvs, onWizardReset, setSources, wizard } from './wizard.svelte';
import { heldConnection, holdCollection, holdConnection, releaseConnection, secretsOf } from './wizardConnections';
import {
  ADDRESS_KINDS, DEFAULT_PORT, addSource, addressSource, addressText, classifyDatabase, cleanTag, fileSource, hostLabel, looksLikeSecret, nextFileNumber, parseAddress,
  parseAddressRef, reachFrom, reconcileEnvs, reconcileWithScan, removeConnectionSources, removeSource, replaceFinding, setTag, sourceKey, toggleFinding, withReach, withoutReach
} from './wizardSources';
import type { AddressKind, AddressProblem, DatabaseProblem, FileKind, ReachState, TagField } from './wizardSources';
import type { ConnectionInput, ConnectionTestResult } from './types';

/**
 * The state of the sources step of the create wizard (TSK-18) that has to outlive the step: the scan of this computer, the checks of typed
 * addresses, and the MongoDB connections that were tested. The step's components unmount when the person goes Back or Next, so this
 * keeps their work. It is cleared when the draft is thrown away (see onWizardReset).
 *
 * What is chosen lives in `wizard.sources` (lib/wizard.svelte.ts). This file never puts a password, an address with a login or a path in
 * it: a MongoDB connection is held in wizardConnections.ts and referred to by an id.
 *
 * Only `$state` is used (no `.raw`, no `$derived`) so the logic runs under node tests with a plain stand-in.
 */

export type ScanPhase = 'idle' | 'checking' | 'scanning' | 'done';

/** A tested MongoDB connection as the screen shows it: the id the draft uses, the host names only, the databases the test listed. */
export interface ConnectionView { id: string; host: string; databases: string[]; warning: string }

export interface SourcesView {
  phase: ScanPhase;
  entry: EntryState;
  scanId: string;
  startedAt: string;
  findings: Finding[];
  gaps: Gap[];
  host: ScanHost | null;
  progress: DetectorProgress[];
  cancelling: boolean;
  /** A plain line that is not a fault (a cancelled scan, sources taken off the list). */
  info: string;
  /** A line about something that went wrong, already in plain words. */
  problem: string;
  /** Reachability answers by source key. */
  reach: Record<string, ReachState>;
  /** Source keys being checked right now. */
  checking: string[];
  connections: ConnectionView[];
}

const blankView = (): SourcesView => ({
  phase: 'idle', entry: { kind: 'loading' }, scanId: '', startedAt: '', findings: [], gaps: [], host: null, progress: [], cancelling: false,
  info: '', problem: '', reach: {}, checking: [], connections: []
});

export const sourcesView = $state<SourcesView>(blankView());

/** The calls this step makes. A test passes a fake. */
export interface SourcesDeps {
  client: DiscoverClient;
  testConnection: (input: ConnectionInput, signal?: AbortSignal) => Promise<ConnectionTestResult>;
  probe: (host: string, port: number, signal?: AbortSignal) => Promise<ProbeResult>;
  locate: (scanId: string, findingId: string, path: string, signal?: AbortSignal) => Promise<LocateResult>;
  now: () => number;
}

const realDeps = (): SourcesDeps => ({ client: discoverClient, testConnection: (input, signal) => api.testConnection(input, signal), probe: probeReachable, locate: locateFinding, now: () => Date.now() });
const depsOf = (given?: Partial<SourcesDeps>): SourcesDeps => ({ ...realDeps(), ...given });

let controller: AbortController | null = null;
let generation = 0;
let cancelWanted = false;

const isAbort = (error: unknown): boolean => error instanceof DOMException && error.name === 'AbortError';
const errorLine = (error: unknown): string => problemText(classifyError(error), error instanceof CaseApiError ? error : undefined);

/* ------------------------------------------------------------------ the entry, and the scan of this computer */

/** Asks once whether this computer can be scanned (Windows only). `force` asks again (after a lost connection). */
export async function loadEntry(given?: Partial<SourcesDeps>, force = false): Promise<void> {
  const deps = depsOf(given);
  if (force) sourcesView.entry = { kind: 'loading' };
  sourcesView.entry = await cachedEntryState(deps.client, force);
}

export const scanIsRunning = (): boolean => sourcesView.phase === 'scanning' || sourcesView.phase === 'checking';

/**
 * Scans this computer. A second call while a scan runs does nothing. The result replaces the list of findings, and a chosen source the
 * new scan cannot read any more is taken off the list (and counted in `info`). Resolves when the scan is over.
 */
export async function startScan(given?: Partial<SourcesDeps>): Promise<void> {
  if (scanIsRunning()) return;
  const deps = depsOf(given);
  sourcesView.info = ''; sourcesView.problem = ''; sourcesView.progress = []; sourcesView.cancelling = false; sourcesView.scanId = '';
  cancelWanted = false;
  const mine = ++generation;
  controller?.abort();
  controller = new AbortController();
  const signal = controller.signal;
  sourcesView.phase = 'scanning';
  try {
    const scanId = await deps.client.startScan(signal);
    if (mine !== generation) { void deps.client.cancelScan(scanId).catch(() => {}); return; }
    sourcesView.scanId = scanId;
    if (cancelWanted) await deps.client.cancelScan(scanId, signal).catch(() => {});
    const done = await waitForScan(deps.client, scanId, { signal, onUpdate: (next) => { if (mine === generation) sourcesView.progress = next.progress; } });
    if (mine !== generation) return;
    if (done.state === 'cancelled') { sourcesView.info = DISCOVER_TEXT.scanCancelled; sourcesView.phase = 'idle'; return; }
    if (done.state === 'failed') { sourcesView.problem = DISCOVER_TEXT.scanFailed; sourcesView.phase = 'idle'; return; }
    sourcesView.findings = done.findings;
    sourcesView.gaps = done.gaps;
    sourcesView.host = done.host;
    sourcesView.startedAt = done.startedAt;
    const kept = reconcileWithScan(wizard.sources, done.findings);
    if (kept.dropped > 0) {
      setSources(kept.list);
      sourcesView.info = scanDroppedText(kept.dropped);
    }
    sourcesView.phase = 'done';
  } catch (error) {
    if (isAbort(error) || mine !== generation) return;
    if (cancelWanted && error instanceof CaseApiError && (error.status === 404 || classifyError(error) === 'expired')) sourcesView.info = DISCOVER_TEXT.scanCancelled;
    else sourcesView.problem = errorLine(error);
    sourcesView.phase = 'idle';
  }
}

/** Asks the server to stop the scan that is running. The scan then ends as cancelled. */
export async function cancelScan(given?: Partial<SourcesDeps>): Promise<void> {
  if (sourcesView.phase !== 'scanning' || sourcesView.cancelling) return;
  const deps = depsOf(given);
  sourcesView.cancelling = true;
  cancelWanted = true;
  if (sourcesView.scanId === '') return;
  try { await deps.client.cancelScan(sourcesView.scanId); } catch (error) { sourcesView.cancelling = false; sourcesView.problem = errorLine(error); }
}

/** True when the scan held by this step is too old for the server to still hold it. */
export const scanIsStale = (now: number = Date.now()): boolean => sourcesView.phase === 'done' && scanExpired(sourcesView.startedAt, now);

/* ------------------------------------------------------------------ ticking, tagging, removing */

const envNames = (): string[] => draftEnvs().names;

/** Ticks or unticks a scan finding. Only a finding the scan verified can be ticked. Returns whether it is chosen now. */
export function tickFinding(findingId: string, on: boolean): boolean {
  const finding = sourcesView.findings.find((f) => f.id === findingId);
  if (!finding) return false;
  setSources(toggleFinding(wizard.sources, finding, on));
  return wizard.sources.some((s) => s.kind === 'discover' && s.ref === findingId);
}

export const isChosen = (key: string): boolean => wizard.sources.some((s) => sourceKey(s) === key);

/** Sets one tag of a chosen source. Returns false when the value was refused (it looked like a password). */
export function setSourceTag(key: string, field: TagField, value: string): boolean {
  if (looksLikeSecret(cleanTag(value))) return false;
  setSources(setTag(wizard.sources, key, field, value, envNames()));
  return true;
}

/** Takes a source off the list, and forgets what was learned about its address. */
export function removeChosen(key: string): void {
  setSources(removeSource(wizard.sources, key));
  sourcesView.reach = withoutReach(sourcesView.reach, key);
}

/**
 * Called when the sources step opens: a source whose environment is no longer on the first step's list goes back to "not named yet".
 * Returns how many were set back, and says so in `info`.
 */
export function reconcileEnvironments(): number {
  const result = reconcileEnvs(wizard.sources, envNames());
  if (result.cleared > 0) {
    setSources(result.list);
    sourcesView.info = envsClearedText(result.cleared);
  }
  return result.cleared;
}

export function clearInfo(): void { sourcesView.info = ''; sourcesView.problem = ''; }

/* ------------------------------------------------------------------ "Where is it?" */

export type LocateOutcome = { ok: true; files: number } | { ok: false; reason: string } | { ok: false; problem: string };

/**
 * Asks the server whether a typed place is the one for a source it found but could not read. The path leaves the browser in the request
 * body only. On success the finding in the list is replaced by the verified one, which can then be ticked.
 */
export async function locateSource(findingId: string, path: string, given?: Partial<SourcesDeps>): Promise<LocateOutcome> {
  const deps = depsOf(given);
  if (sourcesView.scanId === '') return { ok: false, problem: errorLine(new CaseApiError('', 404, 'scan-unknown')) };
  try {
    const result = await deps.locate(sourcesView.scanId, findingId, path);
    if (!result.ok) return { ok: false, reason: result.reason };
    sourcesView.findings = replaceFinding(sourcesView.findings, result.finding);
    return { ok: true, files: result.files };
  } catch (error) {
    return { ok: false, problem: errorLine(error) };
  }
}

/* ------------------------------------------------------------------ another computer: typed addresses and files */

export type AddOutcome =
  | { ok: true; key: string; reached: boolean | null }
  | { ok: false; problem: AddressProblem | 'full' | 'duplicate' };

/**
 * Adds SQL Server or Redis by its address and checks that it can be reached. The source is added whatever the answer was; an address that
 * could not be reached is flagged, and the person decides. `reached` is null when the check itself could not be made.
 */
export async function addByAddress(kind: AddressKind, text: string, given?: Partial<SourcesDeps>): Promise<AddOutcome> {
  if (!ADDRESS_KINDS.includes(kind)) return { ok: false, problem: 'bad-host' };
  const parsed = parseAddress(text, DEFAULT_PORT[kind], kind === 'sql-server');
  if (!parsed.ok) return { ok: false, problem: parsed.problem };
  const source = addressSource(kind, parsed.host, parsed.port, addressText(parsed.host, parsed.port));
  const added = addSource(wizard.sources, source);
  if (!added.ok) return { ok: false, problem: added.reason };
  setSources(added.list);
  const key = sourceKey(source);
  const reached = await checkReach(key, given);
  return { ok: true, key, reached };
}

/** Checks (again) whether a chosen address can be reached. Records the answer beside the source. Returns the answer, or null if it could not be asked. */
export async function checkReach(key: string, given?: Partial<SourcesDeps>): Promise<boolean | null> {
  const found = wizard.sources.find((s) => sourceKey(s) === key);
  const parsed = found ? parseAddressRef(found.ref) : null;
  if (!found || !parsed) return null;
  const deps = depsOf(given);
  if (sourcesView.checking.includes(key)) return null;
  sourcesView.checking = [...sourcesView.checking, key];
  try {
    const result = await deps.probe(parsed.host, parsed.port);
    // The source may have been removed while the check ran.
    if (isChosen(key)) sourcesView.reach = withReach(sourcesView.reach, key, reachFrom(result));
    return result.reachable;
  } catch (error) {
    if (isChosen(key)) sourcesView.reach = withReach(sourcesView.reach, key, { state: 'failed', reason: classifyError(error) === 'import-busy' ? 'busy' : 'unknown' });
    return null;
  } finally {
    sourcesView.checking = sourcesView.checking.filter((k) => k !== key);
  }
}

/** The address text of a chosen address source, for the screen: "db1:1433". Empty for any other source. */
export function addressOfSource(ref: string): string {
  const parsed = parseAddressRef(ref);
  return parsed ? addressText(parsed.host, parsed.port) : '';
}

/** Adds a file or export from another computer. The file itself is chosen when the case is made. */
export function addFile(kind: FileKind, label: string): AddOutcome {
  const n = nextFileNumber(wizard.sources, kind);
  const source = fileSource(kind, label, n);
  const added = addSource(wizard.sources, source);
  if (!added.ok) return { ok: false, problem: added.reason };
  setSources(added.list);
  return { ok: true, key: sourceKey(source), reached: null };
}

/* ------------------------------------------------------------------ MongoDB */

export type TestOutcome = { ok: true; id: string; databases: number } | { ok: false; text: string };

/**
 * Tests a MongoDB connection and lists its databases. A tested connection is held in memory (wizardConnections.ts), never in the draft.
 * Any message from the server is cleaned of the address, user name and password before it is returned.
 * With `existingId` the connection is tested again and its list of databases is refreshed.
 */
export async function testMongo(input: ConnectionInput, existingId = '', given?: Partial<SourcesDeps>, collection = ''): Promise<TestOutcome> {
  const deps = depsOf(given);
  const secrets = secretsOf(input);
  let result: ConnectionTestResult;
  try {
    result = await deps.testConnection(input);
  } catch (error) {
    if (isAbort(error)) return { ok: false, text: '' };
    return { ok: false, text: safeServerLine(error instanceof Error ? error.message : '', secrets) };
  }
  const databases = Array.isArray(result?.databases) ? result.databases.filter((d): d is string => typeof d === 'string' && d.length > 0 && d.length <= 120).slice(0, 200) : [];
  const warning = typeof result?.warning === 'string' ? safeServerLine(result.warning, secrets) : '';
  const id = existingId !== '' && heldConnection(existingId) ? existingId : '';
  const connectionId = holdConnection(input, id === '' ? undefined : id);
  holdCollection(connectionId, collection);
  const view: ConnectionView = { id: connectionId, host: hostLabel(input.uri), databases, warning };
  if (id === '') {
    sourcesView.connections = [...sourcesView.connections, view];
  } else {
    sourcesView.connections = sourcesView.connections.map((c) => (c.id === id ? view : c));
    // A database that the new list no longer has cannot stay a source.
    const prefix = 'mongo:' + id + '/';
    setSources(wizard.sources.filter((s) => s.kind !== 'mongo-database' || !s.ref.startsWith(prefix) || databases.includes(s.ref.slice(prefix.length))));
  }
  return { ok: true, id: connectionId, databases: databases.length };
}

/** Sets what one database of a tested connection is for. Returns null when it worked, or what was wrong. */
export function classifyMongo(connectionId: string, database: string, classification: string): DatabaseProblem | null {
  const connection = sourcesView.connections.find((c) => c.id === connectionId);
  if (!connection) return 'not-tested';
  const result = classifyDatabase(wizard.sources, { id: connection.id, databases: connection.databases }, database, classification);
  if (!result.ok) return result.reason;
  setSources(result.list);
  return null;
}

/** Removes a tested connection, its databases from the list, and the held password. */
export function removeConnection(connectionId: string): void {
  setSources(removeConnectionSources(wizard.sources, connectionId));
  releaseConnection(connectionId);
  sourcesView.connections = sourcesView.connections.filter((c) => c.id !== connectionId);
}

/* ------------------------------------------------------------------ clearing */

/** Forgets the scan, the checks and the connections of this step. The chosen sources are the draft's own and go with it. */
export function resetSources(): void {
  generation++;
  const scanId = sourcesView.scanId;
  const running = sourcesView.phase === 'scanning';
  controller?.abort();
  controller = null;
  if (running && scanId !== '') void discoverClient.cancelScan(scanId).catch(() => {});
  Object.assign(sourcesView, blankView());
}

onWizardReset(resetSources);
