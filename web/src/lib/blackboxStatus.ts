import { shownBytes, type BlackboxItem, type Settings, type ThisPc, type Tone } from './blackbox';
import { BBS_TEXT as T } from './copy/blackboxStatusCopy';

/** One part of the machine the Blackbox looks at, with what Unravis has read about it. Nothing here is guessed. */
export interface TechCard { id: 'sql' | 'redis' | 'sites'; title: string; tone: Tone; word: string; sub: string; note: string }

/** "412 MB used of 5 GB". Only the parts that were read: no cap means no "of", no size means no line. */
export function usedLine(item: BlackboxItem): string {
  const f = item.facts;
  if (!f || !f.bytes) return '';
  const used = shownBytes(f.bytes);
  return f.maxGB ? T.usedOf(used, shownGB(f.maxGB)) : T.usedOnly(used);
}

function shownGB(gb: number): string {
  return String(Number(gb.toFixed(2))) + ' GB';
}

/** The summary parts, in order, for the line under the headline. Each part is left out when it was not read. */
export function stripParts(item: BlackboxItem, since: string): string[] {
  const parts: string[] = [];
  const used = usedLine(item);
  if (used) parts.push(used);
  if (since) parts.push(T.since(since));
  if (item.facts?.keepDays) parts.push(T.kept(item.facts.keepDays));
  return parts;
}

/** The SQL collector, from the recorder's own status page and the service manager. A Blackbox on another server shows the status page only. */
function sqlCard(item: BlackboxItem, pc: ThisPc, when: (iso: string) => string): TechCard | null {
  const q = item.facts?.sql;
  if (!q && !(item.thisPc && pc.installed)) return null;
  const base = { id: 'sql' as const, title: T.sqlTitle, sub: '' };
  // The service manager is only read for the recorder on this PC.
  if (item.thisPc && pc.installed && !pc.sqlInstalled) return { ...base, tone: 'idle', word: T.sqlOff, note: T.sqlNoteOff };
  if (item.thisPc && pc.installed && pc.sqlInstalled && !pc.sqlRunning) return { ...base, tone: 'warn', word: T.sqlStopped, note: T.sqlNoteStopped };
  if (!q) return null;
  // "Collect SQL data below" exists only for the recorder on this PC, so a recorder elsewhere gets a note without it.
  const local = item.thisPc && pc.installed;
  if (q.state === 'denied') return { ...base, tone: 'warn', word: T.sqlGrant, note: local ? T.sqlNoteGrant : T.sqlNoteGrantRemote };
  if (q.state === 'auto-off') return { ...base, tone: 'bad', word: T.sqlTurnedOff, note: local ? T.sqlNoteTurnedOff : T.sqlNoteTurnedOffRemote };
  if (q.enabled && q.state === 'on' && q.lastOK) return { ...base, tone: 'good', word: T.sqlOn, sub: T.sqlLastOk(when(q.lastOK)), note: T.sqlNoteOn };
  if (q.state === 'backing-off') return { ...base, tone: 'warn', word: T.sqlRetrying, sub: q.lastOK ? T.sqlLastOk(when(q.lastOK)) : '', note: T.sqlNoteRetrying(q.failures ?? 0) };
  if (!q.enabled && q.state === 'off') {
    // On this PC the service manager already said it is installed and running, so "not set up" would contradict it.
    if (local && pc.sqlInstalled && pc.sqlRunning) return { ...base, tone: 'idle', word: T.sqlWaiting, note: T.sqlNoteRunsOff };
    return { ...base, tone: 'idle', word: T.sqlOff, note: local ? T.sqlNoteOff : T.sqlNoteOffRemote };
  }
  return { ...base, tone: 'idle', word: T.sqlWaiting, note: T.sqlNoteWaiting };
}

/** Redis is read from the settings only: the recorder does not report whether Redis answered, and the card says so. */
function redisCard(s: Settings | undefined): TechCard | null {
  if (!s?.redis) return null;
  const off = s.collectors?.['redis.info']?.on === false;
  const set = s.redis.profile ? s.redis.profile.charAt(0).toUpperCase() + s.redis.profile.slice(1) : 'Standard';
  return off
    ? { id: 'redis', title: T.redisTitle, tone: 'idle', word: T.redisOff, sub: '', note: T.redisNoteOff }
    : { id: 'redis', title: T.redisTitle, tone: 'idle', word: T.redisSet, sub: T.redisSub(s.redis.port ?? 6379, set), note: T.redisNote };
}

function sitesCard(s: Settings | undefined): TechCard | null {
  if (!s) return null;
  const targets = s.targets ?? [];
  const pools = targets.filter((t) => t.kind === 'appPool').length;
  const services = targets.length - pools;
  return targets.length === 0
    ? { id: 'sites', title: T.sitesTitle, tone: 'idle', word: T.sitesNone, sub: '', note: T.sitesNoteNone }
    : { id: 'sites', title: T.sitesTitle, tone: 'good', word: T.sitesPicked, sub: '', note: T.sitesNotePicked(pools, services) };
}

/** The cards for one Blackbox. Only parts with something read are listed: no card for MongoDB or the Salt minion, nothing Unravis cannot see. */
export function techCards(item: BlackboxItem, pc: ThisPc, when: (iso: string) => string): TechCard[] {
  const settings = item.thisPc ? pc.settings : undefined;
  return [sqlCard(item, pc, when), redisCard(settings), sitesCard(settings)].filter((c): c is TechCard => c !== null);
}
