/**
 * The AI access screen's model (BL-347): what the server sends about each host, what the person's choices become, and the plain text
 * built from them. Pure: no DOM, no fetch. The routes are in internal/mcpadmin (mounted by internal/server/mcp_admin.go).
 *
 * Rules this file keeps (security review of the grant store, PR 260 and PR 263):
 *  - Everything the host said about itself (name, version, program path) is DATA. It is returned as a string and shown as plain text by
 *    the screen; nothing here builds markup, and the screen never inserts raw HTML.
 *  - An approval always names the revision the card showed (`expectRev`). `approveBody` throws without one, so no code path can approve
 *    "whatever is there now".
 *  - A host that another program also tried to register as (`contested`) is approved only with an explicit extra tick.
 *  - Nothing is allowed by default: the form starts with summaries only, the connected sources, no identifiers, and the person presses Approve.
 */

export type McpState = 'pending' | 'active' | 'revoked' | 'denied';
export type McpTier = 'aggregate' | 'evidence';

export interface McpProfile {
  id: string;
  state: McpState;
  expired: boolean;
  rev: number;
  /** A program has connected under this id and waits: only then can the entry be approved. */
  enrolled: boolean;
  /** How many times another program also tried to register as this host (capped by the server). */
  contested: number;
  hostName: string;
  hostVersion: string;
  exe: string;
  ppid: number;
  tier: string;
  tools: string[];
  sources: string[];
  identifiers: boolean;
  identifiersUntil: string;
  requestedIdentifiers: boolean;
  idleMinutes: number;
  expiresAt: string;
  approvedAt: string;
  createdAt: string;
  lastCallAt: string;
  /** The host has collected its credential. An approved host that has not yet is "waiting for the host". */
  claimed: boolean;
}

export interface McpTierInfo { id: McpTier; tools: string[] }

export interface McpLimits {
  maxProfiles: number;
  defaultIdleMinutes: number;
  minIdleMinutes: number;
  maxIdleMinutes: number;
  defaultIdentifiersMinutes: number;
  maxIdentifiersMinutes: number;
  minCohort: number;
}

export type McpOffReason = 'sealing' | 'store' | '';

export interface McpList {
  /** on: the grant store is open. legacy: this run has no grants (browser mode). off: the store could not be opened. */
  mode: 'on' | 'legacy' | 'off';
  reason: McpOffReason;
  stopped: boolean;
  now: string;
  tiers: McpTierInfo[];
  limits: McpLimits;
  profiles: McpProfile[];
}

const str = (v: unknown): string => (typeof v === 'string' ? v : '');
const num = (v: unknown): number => (typeof v === 'number' && Number.isFinite(v) ? v : 0);
const strings = (v: unknown): string[] => (Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : []);
const rec = (v: unknown): Record<string, unknown> => (v && typeof v === 'object' ? (v as Record<string, unknown>) : {});

const STATES: readonly string[] = ['pending', 'active', 'revoked', 'denied'];

export const EMPTY_LIMITS: McpLimits = {
  maxProfiles: 64, defaultIdleMinutes: 480, minIdleMinutes: 5, maxIdleMinutes: 43200, defaultIdentifiersMinutes: 60, maxIdentifiersMinutes: 10080, minCohort: 5
};

/** Reads one profile; a row with no usable id or state is dropped (null), never guessed at. */
export function parseProfile(raw: unknown): McpProfile | null {
  const r = rec(raw);
  const id = str(r.id);
  const state = str(r.state);
  if (!id || !STATES.includes(state)) return null;
  return {
    id, state: state as McpState, expired: r.expired === true, rev: num(r.rev), enrolled: r.enrolled === true, contested: num(r.contested),
    hostName: str(r.hostName), hostVersion: str(r.hostVersion), exe: str(r.exe), ppid: num(r.ppid), tier: str(r.tier),
    tools: strings(r.tools), sources: strings(r.sources), identifiers: r.identifiers === true, identifiersUntil: str(r.identifiersUntil),
    requestedIdentifiers: r.requestedIdentifiers === true, idleMinutes: num(r.idleMinutes), expiresAt: str(r.expiresAt), approvedAt: str(r.approvedAt),
    createdAt: str(r.createdAt), lastCallAt: str(r.lastCallAt), claimed: r.claimed === true
  };
}

/** Reads the answer of GET /api/mcp/profiles. Anything that is not an object with a known mode is unreadable (null). */
export function parseList(raw: unknown): McpList | null {
  const r = rec(raw);
  const mode = str(r.mode);
  if (mode !== 'on' && mode !== 'legacy' && mode !== 'off') return null;
  const reason = str(r.reason);
  const tiers: McpTierInfo[] = (Array.isArray(r.tiers) ? r.tiers : []).flatMap((t) => {
    const x = rec(t);
    return x.id === 'aggregate' || x.id === 'evidence' ? [{ id: x.id as McpTier, tools: strings(x.tools) }] : [];
  });
  const lim = rec(r.limits);
  const limits: McpLimits = { ...EMPTY_LIMITS };
  for (const key of Object.keys(limits) as (keyof McpLimits)[]) if (num(lim[key]) > 0) limits[key] = num(lim[key]);
  const profiles = (Array.isArray(r.profiles) ? r.profiles : []).map(parseProfile).filter((p): p is McpProfile => p !== null);
  return { mode, reason: reason === 'sealing' || reason === 'store' ? reason : '', stopped: r.stopped === true, now: str(r.now), tiers, limits, profiles };
}

export interface McpGroups { pending: McpProfile[]; active: McpProfile[]; ended: McpProfile[] }

const byNewest = (a: McpProfile, b: McpProfile) => (a.createdAt < b.createdAt ? 1 : a.createdAt > b.createdAt ? -1 : a.id.localeCompare(b.id));

/** Waiting hosts first (oldest request first, so none is left at the bottom), then approved, then ended. */
export function groupProfiles(profiles: readonly McpProfile[]): McpGroups {
  const pending = profiles.filter((p) => p.state === 'pending').sort((a, b) => -byNewest(a, b));
  const active = profiles.filter((p) => p.state === 'active').sort(byNewest);
  const ended = profiles.filter((p) => p.state === 'revoked' || p.state === 'denied').sort(byNewest);
  return { pending, active, ended };
}

// ---- the approval form ---------------------------------------------------------------------------------------------------------------

export interface McpDraft {
  tier: McpTier;
  tools: string[];
  sources: string[];
  identifiers: boolean;
  identifiersMinutes: number;
  idleMinutes: number;
  acceptContested: boolean;
}

export const IDLE_CHOICES: readonly number[] = [60, 480, 1440, 10080, 43200];
export const IDENTIFIER_CHOICES: readonly number[] = [15, 60, 480, 1440, 10080];

export function toolsOf(tiers: readonly McpTierInfo[], tier: McpTier): string[] {
  return tiers.find((t) => t.id === tier)?.tools ?? [];
}

/** The form a card starts with: summaries only, every tool of that tier, every source connected now, no identifiers, 8 hours idle. */
export function defaultDraft(tiers: readonly McpTierInfo[], limits: McpLimits, sourceIds: readonly string[]): McpDraft {
  return {
    tier: 'aggregate', tools: toolsOf(tiers, 'aggregate'), sources: [...sourceIds], identifiers: false,
    identifiersMinutes: limits.defaultIdentifiersMinutes, idleMinutes: limits.defaultIdleMinutes, acceptContested: false
  };
}

/** Changing the tier keeps the tools that the new tier also has; going up to evidence adds its extra tools switched on. */
export function withTier(draft: McpDraft, tiers: readonly McpTierInfo[], tier: McpTier): McpDraft {
  const had = new Set(toolsOf(tiers, draft.tier));
  const next = toolsOf(tiers, tier);
  const tools = next.filter((t) => draft.tools.includes(t) || !had.has(t));
  return { ...draft, tier, tools };
}

export function toggled(list: readonly string[], item: string, on: boolean): string[] {
  const rest = list.filter((x) => x !== item);
  return on ? [...rest, item] : rest;
}

/** Why Approve cannot be pressed yet, in plain words ('' when it can). The server checks all of it again. */
export function draftProblem(p: McpProfile, d: McpDraft, text: { notConnected: string; noTools: string; noSources: string; contested: string; notWaiting: string }): string {
  if (p.state !== 'pending') return text.notWaiting;
  if (!p.enrolled) return text.notConnected;
  if (d.tools.length === 0) return text.noTools;
  if (d.sources.length === 0) return text.noSources;
  if (p.contested > 0 && !d.acceptContested) return text.contested;
  return '';
}

export interface ApproveBody {
  expectRev: number;
  tier: McpTier;
  tools: string[];
  sources: string[];
  identifiers: boolean;
  identifiersMinutes: number;
  idleMinutes: number;
  acceptContested: boolean;
}

/**
 * The body of an approval. It always carries the revision the card showed: without one this throws, so the screen cannot send an
 * approval that would bind to a host the person never saw (review N5 of PR 260, N-c of PR 263). The identifiers duration is sent only when
 * identifiers are on.
 */
export function approveBody(p: McpProfile, d: McpDraft, tiers: readonly McpTierInfo[]): ApproveBody {
  if (!Number.isInteger(p.rev) || p.rev <= 0) throw new Error('An approval needs the revision of the card.');
  const allowed = toolsOf(tiers, d.tier);
  return {
    expectRev: p.rev, tier: d.tier, tools: d.tools.filter((t) => allowed.includes(t)), sources: [...d.sources], identifiers: d.identifiers,
    identifiersMinutes: d.identifiers ? d.identifiersMinutes : 0, idleMinutes: d.idleMinutes, acceptContested: p.contested > 0 && d.acceptContested
  };
}

// ---- plain text from the data --------------------------------------------------------------------------------------------------------

/** "just now", "5 minutes ago", "3 hours ago", "2 days ago"; '' when there is no time. */
export function ago(nowIso: string, thenIso: string): string {
  const now = Date.parse(nowIso);
  const then = Date.parse(thenIso);
  if (!Number.isFinite(now) || !Number.isFinite(then)) return '';
  const minutes = Math.floor((now - then) / 60000);
  if (minutes < 1) return 'just now';
  if (minutes < 60) return minutes + (minutes === 1 ? ' minute ago' : ' minutes ago');
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return hours + (hours === 1 ? ' hour ago' : ' hours ago');
  const days = Math.floor(hours / 24);
  return days + ' days ago';
}

/** "in 5 minutes", "in 3 hours", "in 2 days", or "now" once passed; '' when there is no time. */
export function inFuture(nowIso: string, thenIso: string): string {
  const now = Date.parse(nowIso);
  const then = Date.parse(thenIso);
  if (!Number.isFinite(now) || !Number.isFinite(then)) return '';
  const minutes = Math.ceil((then - now) / 60000);
  if (minutes <= 0) return 'now';
  if (minutes < 60) return 'in ' + minutes + (minutes === 1 ? ' minute' : ' minutes');
  const hours = Math.round(minutes / 60);
  if (hours < 48) return 'in ' + hours + (hours === 1 ? ' hour' : ' hours');
  return 'in ' + Math.round(hours / 24) + ' days';
}

/** A length of time in the words of the choice lists: "15 minutes", "1 hour", "8 hours", "1 day", "7 days". */
export function span(minutes: number): string {
  if (minutes < 60) return minutes + (minutes === 1 ? ' minute' : ' minutes');
  if (minutes < 1440) { const h = Math.round(minutes / 60); return h + (h === 1 ? ' hour' : ' hours'); }
  const d = Math.round(minutes / 1440);
  return d + (d === 1 ? ' day' : ' days');
}

export type McpTone = 'wait' | 'ok' | 'bad' | 'quiet';

/** The state chip of a row: the words and a tone that picks the colour (never the only cue: the words say it). */
export function chipOf(p: McpProfile, stopped: boolean): { label: string; tone: McpTone } {
  if (p.state === 'pending') return p.enrolled ? { label: 'Waiting for you', tone: 'wait' } : { label: 'Not connected', tone: 'quiet' };
  if (p.state === 'revoked') return { label: 'Revoked', tone: 'bad' };
  if (p.state === 'denied') return { label: 'Denied', tone: 'bad' };
  if (stopped) return { label: 'Stopped', tone: 'bad' };
  if (p.expired) return { label: 'Expired', tone: 'wait' };
  if (!p.claimed) return { label: 'Approved, host not connected yet', tone: 'wait' };
  return { label: 'Active', tone: 'ok' };
}

/** The scope in one line: tier, how many tools, how many sources and whether identifiers are on. */
export function scopeLine(p: McpProfile): string {
  const tier = p.tier === 'evidence' ? 'Summaries and request rows' : 'Summaries only';
  const tools = p.tools.length + (p.tools.length === 1 ? ' tool' : ' tools');
  const sources = p.sources.length + (p.sources.length === 1 ? ' source' : ' sources');
  return [tier, tools, sources, p.identifiers ? 'user ids and addresses on' : 'no user ids or addresses'].join(', ');
}

/** Source labels for ids; an id that is not connected now is shown as it is (the grant still names it). */
export function sourceNames(ids: readonly string[], labels: ReadonlyMap<string, string>): string[] {
  return ids.map((id) => labels.get(id) ?? id);
}
