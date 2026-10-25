import { groupOf, isSelectable } from './discover';
import type { Finding, GroupId, ProbeResult } from './discover';
import type { DraftSource } from './wizardFlow';

/**
 * The pure model of the sources step of the create wizard (TSK-18): what may be ticked, how a source is named and tagged
 * (Environment > Portal > API), how a Mongo database is classified, how a typed address is read, and what a reachability answer does.
 * No DOM, no fetch, no state. The store (wizardSources.svelte.ts) and the components call these and keep the results.
 *
 * Rules this file holds:
 *  - Only a finding the scan verified can be ticked (isSelectable). An unverified one is shown with its reason and a way to point to the folder.
 *  - Blank environment, portal and API are allowed and mean "not named yet". Suggestions are offered, never filled in.
 *  - A source is named by its kind and ref; the same pair never appears twice.
 *  - A Mongo database needs a tested connection and a classification before it is a source. "Not used" removes it.
 *  - An unreachable address keeps its source, flagged. The person decides.
 *  - Nothing here holds a password or a connection string. A typed address that carries one is refused.
 */

/** Source kinds the draft uses. Scan findings are `discover`; the rest are added by hand under "Another computer". */
export const SOURCE_KINDS = ['discover', 'mongo-database', 'sql-server', 'redis', 'iis-logs', 'app-log', 'hangfire-csv', 'redis-slowlog-csv', 'file'] as const;
export type SourceKind = typeof SOURCE_KINDS[number];

/** Kinds that are a network address checked with the reachability probe. */
export const ADDRESS_KINDS = ['sql-server', 'redis'] as const;
export type AddressKind = typeof ADDRESS_KINDS[number];
export const DEFAULT_PORT: Readonly<Record<AddressKind | 'mongo', number>> = { 'sql-server': 1433, redis: 6379, mongo: 27017 };

/** Kinds that are files the person copied or exported from another computer. The file itself is chosen when the case is made. */
export const FILE_KINDS = ['iis-logs', 'app-log', 'hangfire-csv', 'redis-slowlog-csv', 'file'] as const;
export type FileKind = typeof FILE_KINDS[number];

/** The server takes at most this many sources, with these text limits (see the tag routes). */
export const MAX_SOURCES = 200;
export const ENV_TAG_MAX = 60;
export const TAG_MAX = 80;
export const LABEL_MAX = 80;

export const CLASSIFICATIONS = ['audit', 'other', 'unused'] as const;
export type Classification = typeof CLASSIFICATIONS[number];
export const isClassification = (value: string): value is Classification => (CLASSIFICATIONS as readonly string[]).includes(value);

/* ------------------------------------------------------------------ text of a tag */

/** A tag as it will be kept: control characters dropped, runs of spaces made one, the ends trimmed, cut to the limit. */
export function cleanTag(text: string, max = TAG_MAX): string {
  const plain = String(text ?? '').replace(/[\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/gu, ' ').replace(/\s+/g, ' ').trim();
  return [...plain].slice(0, max).join('').trim();
}

/** The same shape the server refuses: text that looks like a connection string or a password. Checked here so the person hears it first. */
const SECRET_LOOKING = /(?:[a-z][a-z0-9+.-]*:\/\/)|(?:\bpass(?:word|wd)?\s*[=:])|(?:\bpwd\s*[=:])|(?:\bsecret\s*[=:])|(?:\btoken\s*[=:])|(?:[^\s@/]+:[^\s@/]*@[^\s@/]+)/i;

export function looksLikeSecret(text: string): boolean {
  return SECRET_LOOKING.test(String(text ?? ''));
}

export type TagField = 'env' | 'portal' | 'api';
export type TagProblem = 'secret' | 'unknown-env';

/** What is wrong with a typed tag, or null. A blank tag is fine. */
export function tagProblem(field: TagField, text: string, envNames: readonly string[]): TagProblem | null {
  const value = cleanTag(text, field === 'env' ? ENV_TAG_MAX : TAG_MAX);
  if (value === '') return null;
  if (looksLikeSecret(value)) return 'secret';
  if (field === 'env' && envNames.length > 0 && !envNames.some((name) => sameName(name, value))) return 'unknown-env';
  return null;
}

const sameName = (a: string, b: string): boolean => a.toLocaleLowerCase() === b.toLocaleLowerCase();

/** The environment name as step 1 spells it when the typed one matches ignoring case, else the typed one. */
export function canonicalEnv(text: string, envNames: readonly string[]): string {
  const value = cleanTag(text, ENV_TAG_MAX);
  return envNames.find((name) => sameName(name, value)) ?? value;
}

/* ------------------------------------------------------------------ the list of chosen sources */

export const sourceKey = (source: Pick<DraftSource, 'kind' | 'ref'>): string => source.kind + '|' + source.ref;

export function makeSource(kind: string, ref: string, label: string): DraftSource {
  return { kind, ref, label: cleanTag(label, LABEL_MAX), env: '', portal: '', api: '', classification: '' };
}

/** The draft entry for a ticked scan finding. Tags start blank; nothing from `suggest` is copied in. */
export const sourceFromFinding = (finding: Pick<Finding, 'id' | 'display'>): DraftSource => makeSource('discover', finding.id, finding.display);

export const hasSource = (list: readonly DraftSource[], key: string): boolean => list.some((source) => sourceKey(source) === key);

export type AddResult = { ok: true; list: DraftSource[] } | { ok: false; reason: 'full' | 'duplicate' };

/** Adds a source. The same kind and ref twice collapses to the first. Past the server's limit it is refused. */
export function addSource(list: readonly DraftSource[], source: DraftSource): AddResult {
  if (hasSource(list, sourceKey(source))) return { ok: false, reason: 'duplicate' };
  if (list.length >= MAX_SOURCES) return { ok: false, reason: 'full' };
  return { ok: true, list: [...list.map((s) => ({ ...s })), { ...source }] };
}

/** The list with duplicates removed (the first of each stays). For a list that came from somewhere else. */
export function collapseDuplicates(list: readonly DraftSource[]): DraftSource[] {
  const seen = new Set<string>();
  const out: DraftSource[] = [];
  for (const source of list) {
    const key = sourceKey(source);
    if (seen.has(key)) continue;
    seen.add(key);
    out.push({ ...source });
  }
  return out;
}

export const removeSource = (list: readonly DraftSource[], key: string): DraftSource[] => list.filter((s) => sourceKey(s) !== key).map((s) => ({ ...s }));

/** Sets one tag on one source. The value is cleaned; a value that looks like a password is refused (the list is unchanged). */
export function setTag(list: readonly DraftSource[], key: string, field: TagField, value: string, envNames: readonly string[] = []): DraftSource[] {
  const clean = field === 'env' ? canonicalEnv(value, envNames) : cleanTag(value);
  if (looksLikeSecret(clean)) return list.map((s) => ({ ...s }));
  return list.map((s) => (sourceKey(s) === key ? { ...s, [field]: clean } : { ...s }));
}

/**
 * Ticks or unticks a scan finding. Only a finding that can be ticked is added; asking to tick any other changes nothing. Unticking removes the
 * source whatever the finding looks like now.
 */
export function toggleFinding(list: readonly DraftSource[], finding: Finding, on: boolean): DraftSource[] {
  const key = sourceKey({ kind: 'discover', ref: finding.id });
  if (!on) return removeSource(list, key);
  if (!canTick(finding)) return list.map((s) => ({ ...s }));
  const added = addSource(list, sourceFromFinding(finding));
  return added.ok ? added.list : list.map((s) => ({ ...s }));
}

/** True when the scan has checked the source and Unravis can read it. The same rule as the This computer panel. */
export const canTick = (finding: Pick<Finding, 'kind' | 'importer'> & { verified?: boolean }): boolean => isSelectable(finding);

/** The ids of the scan findings that are chosen now. */
export function chosenFindingIds(list: readonly DraftSource[]): string[] {
  return list.filter((s) => s.kind === 'discover').map((s) => s.ref);
}

/**
 * After a new scan: a chosen finding that the new scan no longer offers as readable is dropped. Returns what is left and how many went,
 * so the screen can say so.
 */
export function reconcileWithScan(list: readonly DraftSource[], findings: readonly Finding[]): { list: DraftSource[]; dropped: number } {
  const readable = new Set(findings.filter(canTick).map((f) => f.id));
  const kept = list.filter((s) => s.kind !== 'discover' || readable.has(s.ref)).map((s) => ({ ...s }));
  return { list: kept, dropped: list.length - kept.length };
}

/**
 * After the first step's environment list changed: when the list names environments, a source whose environment is not one of them
 * goes back to "not named yet" and is counted. With no names the field is free text and nothing changes.
 */
export function reconcileEnvs(list: readonly DraftSource[], envNames: readonly string[]): { list: DraftSource[]; cleared: number } {
  if (envNames.length === 0) return { list: list.map((s) => ({ ...s })), cleared: 0 };
  let cleared = 0;
  const out = list.map((s) => {
    if (s.env === '') return { ...s };
    const match = envNames.find((name) => sameName(name, s.env));
    if (match) return { ...s, env: match };
    cleared++;
    return { ...s, env: '' };
  });
  return { list: out, cleared };
}

/* ------------------------------------------------------------------ suggestions and used names (chips) */

export interface ChipSet { env: string[]; portal: string[]; api: string[] }

/** Distinct values already used on other sources, for one field, in the order first seen. */
export function usedValues(list: readonly DraftSource[], field: 'portal' | 'api', exceptKey = ''): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const source of list) {
    if (exceptKey !== '' && sourceKey(source) === exceptKey) continue;
    const value = source[field];
    if (value === '' || seen.has(value.toLocaleLowerCase())) continue;
    seen.add(value.toLocaleLowerCase());
    out.push(value);
  }
  return out;
}

/**
 * The chips for one source. Environment chips are step 1's names. Portal and API chips are first what the scan guessed from the folders,
 * then names already used on other sources. A chip whose text is already in the field is left out. Never filled in on their own.
 */
export function chipsFor(
  source: DraftSource, suggestion: { portal: string; api: string } | null, list: readonly DraftSource[], envNames: readonly string[]
): ChipSet {
  const key = sourceKey(source);
  const mix = (field: 'portal' | 'api'): string[] => {
    const out: string[] = [];
    const add = (text: string) => { const t = cleanTag(text); if (t !== '' && !looksLikeSecret(t) && !out.some((o) => sameName(o, t)) && !sameName(source[field], t)) out.push(t); };
    if (suggestion) add(suggestion[field]);
    for (const value of usedValues(list, field, key)) add(value);
    return out;
  };
  return { env: envNames.filter((name) => !sameName(name, source.env)), portal: mix('portal'), api: mix('api') };
}

/** The suggestion the scan made for a chosen source, or null (a manual source has none). */
export function suggestionFor(source: DraftSource, findings: readonly Finding[]): { portal: string; api: string } | null {
  if (source.kind !== 'discover') return null;
  return findings.find((f) => f.id === source.ref)?.suggest ?? null;
}

/** "Client A production › Portal A › Orders", leaving out what is blank. 'Not named yet' when all three are blank. */
export function tagPath(source: Pick<DraftSource, 'env' | 'portal' | 'api'>, showEnv = true): string {
  const parts = [showEnv ? source.env : '', source.portal, source.api].filter((part) => part !== '');
  return parts.join(' › ');
}

/* ------------------------------------------------------------------ the scan list */

/** Quartz.NET is noticed by the scan but not offered in the wizard (decision R12). It stays reachable through "A file or export". */
const HIDDEN = /quartz/i;
export const isHiddenInWizard = (finding: Pick<Finding, 'detector' | 'importer' | 'shows' | 'how' | 'kind'>): boolean =>
  finding.kind === 'live-series' || HIDDEN.test(finding.detector) || HIDDEN.test(finding.importer) || HIDDEN.test(finding.shows) || HIDDEN.test(finding.how);

export type EntryState = 'ready' | 'unverified' | 'steps';
export interface GalleryEntry { finding: Finding; state: EntryState }
export interface GalleryGroup { id: GroupId; entries: GalleryEntry[] }

const FILE_FINDING = new Set(['log-file', 'log-set', 'event-channel']);

/** How a finding is drawn: ready (can be ticked), unverified (a file source Unravis has not been able to read: greyed, with its reason), or steps (needs an export or lives elsewhere). */
export function entryState(finding: Finding): EntryState {
  if (canTick(finding)) return 'ready';
  return FILE_FINDING.has(finding.kind) ? 'unverified' : 'steps';
}

/** A source that is unverified can be pointed at a folder; one that needs steps cannot. */
export const canLocate = (finding: Finding): boolean => entryState(finding) === 'unverified';

/** The findings for the gallery: hidden kinds left out, ready ones first within each of the five groups. */
export function galleryGroups(findings: readonly Finding[], order: readonly GroupId[]): GalleryGroup[] {
  const rank: Record<EntryState, number> = { ready: 0, unverified: 1, steps: 2 };
  const entries = findings.filter((f) => !isHiddenInWizard(f)).map((finding) => ({ finding, state: entryState(finding) }));
  return order
    .map((id) => ({ id, entries: entries.filter((e) => groupOf(e.finding) === id).sort((a, b) => rank[a.state] - rank[b.state]) }))
    .filter((group) => group.entries.length > 0);
}

/** Replaces a finding in the list by id (after "Where is it?" verified it). */
export const replaceFinding = (findings: readonly Finding[], found: Finding): Finding[] => findings.map((f) => (f.id === found.id ? found : f));

/** The text of a path the person typed, cleaned for sending. Empty when it holds nothing. */
export const cleanPath = (text: string): string => String(text ?? '').replace(/[\p{Cc}\p{Cf}]/gu, '').trim();

/* ------------------------------------------------------------------ a typed address */

export type AddressProblem = 'empty' | 'secret' | 'bad-host' | 'bad-port';
export type ParsedAddress = { ok: true; host: string; port: number } | { ok: false; problem: AddressProblem };

const HOST_CHARS = /^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$/;

/**
 * Reads "host", "host:port" or "[::1]:port". With no port the default for the kind is used. A text that carries a scheme, a user name or a
 * password is refused (it is a connection string, which never goes in this field). A port outside 1 to 65535 is refused.
 */
export function parseAddress(text: string, defaultPort: number): ParsedAddress {
  const raw = String(text ?? '').trim();
  if (raw === '') return { ok: false, problem: 'empty' };
  if (looksLikeSecret(raw) || raw.includes('@') || raw.includes('/')) return { ok: false, problem: 'secret' };
  if (/\s/.test(raw)) return { ok: false, problem: 'bad-host' };
  let host = raw;
  let portText = '';
  const bracket = /^\[([0-9A-Fa-f:.]+)\](?::(\d*))?$/.exec(raw);
  if (bracket) {
    host = bracket[1];
    portText = bracket[2] ?? '';
  } else {
    const colons = raw.split(':').length - 1;
    if (colons === 1) { [host, portText] = raw.split(':'); }
    else if (colons > 1) return { ok: false, problem: 'bad-host' };
  }
  if (host === '' || host.length > 253) return { ok: false, problem: 'bad-host' };
  if (!bracket && !HOST_CHARS.test(host)) return { ok: false, problem: 'bad-host' };
  let port = defaultPort;
  if (portText !== '') {
    if (!/^\d{1,5}$/.test(portText)) return { ok: false, problem: 'bad-port' };
    port = Number(portText);
  }
  if (!Number.isInteger(port) || port < 1 || port > 65535) return { ok: false, problem: 'bad-port' };
  return { ok: true, host, port };
}

/** "host:port", with brackets round an IPv6 address. */
export const addressText = (host: string, port: number): string => (host.includes(':') ? '[' + host + ']' : host) + ':' + port;

export const addressRef = (kind: AddressKind, host: string, port: number): string => 'net:' + kind + ':' + addressText(host, port).toLocaleLowerCase();

/** Reads a ref made by addressRef back into its kind, host and port. Null for any other ref. */
export function parseAddressRef(ref: string): { kind: AddressKind; host: string; port: number } | null {
  const match = /^net:(sql-server|redis):(.+):(\d{1,5})$/.exec(ref);
  if (!match) return null;
  const port = Number(match[3]);
  if (port < 1 || port > 65535) return null;
  const host = match[2].startsWith('[') && match[2].endsWith(']') ? match[2].slice(1, -1) : match[2];
  return host === '' ? null : { kind: match[1] as AddressKind, host, port };
}

/* ------------------------------------------------------------------ reachability */

export type ReachState = { state: 'ok' } | { state: 'failed'; reason: string };

/** The answer of the probe as a state the list keeps. */
export const reachFrom = (result: ProbeResult): ReachState => (result.reachable ? { state: 'ok' } : { state: 'failed', reason: result.reason || 'unknown' });

/** Records an answer for a source. The list of sources is not touched: an unreachable source stays chosen, flagged. */
export const withReach = (reach: Readonly<Record<string, ReachState>>, key: string, state: ReachState): Record<string, ReachState> => ({ ...reach, [key]: state });

export const withoutReach = (reach: Readonly<Record<string, ReachState>>, key: string): Record<string, ReachState> => {
  const next = { ...reach };
  delete next[key];
  return next;
};

export const isFlagged = (reach: Readonly<Record<string, ReachState>>, key: string): boolean => reach[key]?.state === 'failed';

export const flaggedCount = (list: readonly DraftSource[], reach: Readonly<Record<string, ReachState>>): number => list.filter((s) => isFlagged(reach, sourceKey(s))).length;

/** The source for a checked address. It is added whatever the answer was; the answer is kept beside it. */
export function addressSource(kind: AddressKind, host: string, port: number, label: string): DraftSource {
  return makeSource(kind, addressRef(kind, host, port), label);
}

/* ------------------------------------------------------------------ files from another computer */

/** A file source the person describes. The file itself is picked when the case is made. `n` keeps two of the same kind apart. */
export function fileSource(kind: FileKind, label: string, n: number): DraftSource {
  return makeSource(kind, 'file:' + kind + ':' + n, label);
}

/** The next free number for a file kind, so a second file of the same kind is a second source. */
export function nextFileNumber(list: readonly DraftSource[], kind: FileKind): number {
  let n = 1;
  while (hasSource(list, sourceKey({ kind, ref: 'file:' + kind + ':' + n }))) n++;
  return n;
}

/* ------------------------------------------------------------------ MongoDB */

export const databaseRef = (connectionId: string, database: string): string => 'mongo:' + connectionId + '/' + database;

/** A held connection is named by this id (letters and digits). The ref of a database is `mongo:<id>/<name>`. */
export function parseDatabaseRef(ref: string): { connectionId: string; database: string } | null {
  const match = /^mongo:([A-Za-z0-9]{6,40})\/(.+)$/s.exec(ref);
  return match ? { connectionId: match[1], database: match[2] } : null;
}

export type DatabaseProblem = 'not-tested' | 'needs-classification' | 'full' | 'bad-name';

/** A tested connection, as the list needs to know it: its id and the database names the test returned. No address, no password. */
export interface TestedConnection { id: string; databases: readonly string[] }

/**
 * Sets what a database is for. 'audit' and 'other' make it a source (replacing an earlier choice for the same database); 'unused' removes it.
 * A database the test did not list, a missing choice, or a full list is refused and changes nothing.
 */
export function classifyDatabase(
  list: readonly DraftSource[], connection: TestedConnection, database: string, classification: string
): { ok: true; list: DraftSource[] } | { ok: false; reason: DatabaseProblem } {
  if (!connection.databases.includes(database)) return { ok: false, reason: 'not-tested' };
  if (!isClassification(classification)) return { ok: false, reason: 'needs-classification' };
  const key = sourceKey({ kind: 'mongo-database', ref: databaseRef(connection.id, database) });
  if (classification === 'unused') return { ok: true, list: removeSource(list, key) };
  const existing = list.find((s) => sourceKey(s) === key);
  if (existing) return { ok: true, list: list.map((s) => (sourceKey(s) === key ? { ...s, classification } : { ...s })) };
  // The server accepts a tag for a database only under the exact name the test listed, so the label is that name as it is (not cleaned).
  if (looksLikeSecret(database) || [...database].length > LABEL_MAX) return { ok: false, reason: 'bad-name' };
  const added = addSource(list, { ...makeSource('mongo-database', databaseRef(connection.id, database), database), label: database, classification });
  return added.ok ? { ok: true, list: added.list } : { ok: false, reason: 'full' };
}

/** What a database is classified as now: '' when nothing was chosen, 'unused' when it was set aside on purpose is not kept, so also ''. */
export function databaseClass(list: readonly DraftSource[], connectionId: string, database: string): Classification | '' {
  const found = list.find((s) => s.kind === 'mongo-database' && s.ref === databaseRef(connectionId, database));
  return found && isClassification(found.classification) ? found.classification : '';
}

/** Drops every database of a connection (the person removed it). */
export const removeConnectionSources = (list: readonly DraftSource[], connectionId: string): DraftSource[] =>
  list.filter((s) => !(s.kind === 'mongo-database' && parseDatabaseRef(s.ref)?.connectionId === connectionId)).map((s) => ({ ...s }));

/** How many Mongo databases are chosen as API audit logs. */
export const auditDatabaseCount = (list: readonly DraftSource[]): number => list.filter((s) => s.kind === 'mongo-database' && s.classification === 'audit').length;

/**
 * The host part of a connection address for the screen: scheme, user name, password and path left off. "mongodb://u:p@db1:27017,db2/x?y" gives
 * "db1:27017 and 1 more". Never the user name or the password, whatever the string holds.
 */
export function hostLabel(uri: string): string {
  const bare = String(uri ?? '').trim().replace(/^[a-z][a-z0-9+.-]*:\/\//i, '');
  // Options and fragment come after the first ? or #, so an @ there (a password inside an option) is never the user-info end. A password
  // may hold an unencoded /, so the last @ before the options still counts, and the host ends at the first / after it.
  const head = bare.split(/[?#]/, 1)[0];
  const lastAt = head.lastIndexOf('@');
  const afterAt = lastAt >= 0 ? head.slice(lastAt + 1) : head;
  const text = afterAt.split('/', 1)[0];
  const hosts = text.split(',').map((h) => h.trim()).filter((h) => h !== '');
  if (hosts.length === 0) return '';
  const first = [...hosts[0]].slice(0, 80).join('');
  return hosts.length === 1 ? first : first + ' and ' + (hosts.length - 1) + ' more';
}

/** Removes anything of a connection from a message: the typed address, user name, password and any mongodb:// text. */
export function redactMessage(text: string, secrets: readonly string[]): string {
  let out = String(text ?? '');
  for (const secret of secrets) if (secret.length >= 3) out = out.split(secret).join('[hidden]');
  return out.replace(/[a-z][a-z0-9+.-]*:\/\/\S+/gi, '[address hidden]');
}
