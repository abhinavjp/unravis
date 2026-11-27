import type { LintEntry } from './copylint';

/** Words for the summary line and the "What it records" cards on a Blackbox status screen. Plain words, only what Unravis has read. */
export const BBS_TEXT = {
  stripLabel: 'Recording summary',
  usedOf: (used: string, cap: string): string => used + ' used of ' + cap,
  usedOnly: (used: string): string => used + ' recorded',
  since: (when: string): string => 'Recording since ' + when,
  kept: (days: number): string => days === 1 ? 'Kept for 1 day' : 'Kept for ' + days + ' days',
  cardsTitle: 'What it records',
  cardsIntro: 'What this Blackbox is set up to look at, and what Unravis could read about each part.',
  sqlTitle: 'SQL Server',
  sqlOn: 'collecting',
  sqlGrant: 'needs access',
  sqlOff: 'off',
  sqlWaiting: 'waiting',
  sqlStopped: 'stopped',
  sqlTurnedOff: 'turned itself off',
  sqlNoteOn: 'The SQL reader had a good read.',
  sqlNoteGrant: 'SQL refused the login. Use Collect SQL data below to check access and get the grant.',
  sqlNoteOff: 'The SQL service is not set up on this PC. Collect SQL data below turns it on.',
  sqlNoteStopped: 'The SQL service is set up and stopped. Collect SQL data below turns it on.',
  sqlNoteWaiting: 'The SQL service runs but has not had a good read yet.',
  sqlNoteTurnedOff: 'The SQL reader turned itself off after repeated failures. Check access below, then turn it on again.',
  sqlLastOk: (when: string): string => 'Last good read ' + when,
  redisTitle: 'Redis',
  redisSet: 'set to record',
  redisOff: 'switched off',
  redisSub: (port: number, set: string): string => 'Port ' + port + ', ' + set + ' set',
  redisNote: 'It only sends read commands to Redis. Whether Redis answered is not shown here yet.',
  redisNoteOff: 'You switched Redis recording off in the settings.',
  sitesTitle: 'Sites and programs',
  sitesNone: 'whole machine only',
  sitesPicked: 'watched closely',
  sitesNoteNone: 'Nothing is picked for per-site detail. The whole server is recorded together.',
  sitesNotePicked: (pools: number, services: number): string => {
    const parts: string[] = [];
    if (pools) parts.push(pools === 1 ? '1 app pool' : pools + ' app pools');
    if (services) parts.push(services === 1 ? '1 service' : services + ' services');
    return parts.join(' and ') + ' get their own processor and memory figures.';
  }
};

/** One sample of every string, for the copy lint. */
export function blackboxStatusCopyEntries(): LintEntry[] {
  const entries: LintEntry[] = [];
  for (const [id, value] of Object.entries(BBS_TEXT)) {
    if (typeof value === 'string') { entries.push({ id: 'status.' + id, text: value }); continue; }
    const sample = id === 'sitesNotePicked' ? BBS_TEXT.sitesNotePicked(2, 1)
      : id === 'usedOf' ? BBS_TEXT.usedOf('412 MB', '5 GB')
      : id === 'usedOnly' ? BBS_TEXT.usedOnly('412 MB')
      : id === 'since' ? BBS_TEXT.since('26 Nov 18:55')
      : id === 'kept' ? BBS_TEXT.kept(14)
      : id === 'sqlLastOk' ? BBS_TEXT.sqlLastOk('18:55')
      : BBS_TEXT.redisSub(6379, 'Standard');
    entries.push({ id: 'status.' + id, text: sample });
  }
  return entries;
}
