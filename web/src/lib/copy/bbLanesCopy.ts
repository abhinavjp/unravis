import type { LintEntry } from './copylint';

/**
 * Fixed copy for the Blackbox lanes of the Investigation timeline and the Choose lanes picker. Lane names and meanings
 * are plain words for a beginner. Nothing here names a cause: a lane shows what the recorder read, nothing more.
 */
export const BBL_TEXT = {
  heading: 'Blackbox lanes',
  intro: 'The Blackbox is a small recorder on the server. It writes down many figures every 15 seconds. Each lane below is one figure on the same clock as your API calls. Times are in your chosen time zone.',
  summary: (shown: number, max: number): string => shown + (shown === 1 ? ' lane' : ' lanes') + ' shown, ' + max + ' at most',
  choose: 'Choose lanes',
  reset: 'Reset to the starting lanes',
  hide: (name: string): string => 'Hide the lane ' + name,
  hideTitle: 'Hide this lane',
  undo: 'Undo',
  undid: (name: string): string => 'Hid the lane ' + name + '.',
  empty: 'No lane is shown. Choose lanes to add some.',
  noFile: 'This case holds no Blackbox recording yet. Add the recording files to see these lanes.',
  loading: 'Reading the Blackbox recording.',
  failed: 'The Blackbox recording could not be read. Try again.',
  retry: 'Try again',
  partial: 'The read reached its limit, so some lanes may be incomplete.',
  noData: 'The recording has no figure for this lane in this window.',
  gapKey: 'The recorder was not running here, so there are no figures. This is not zero.',
  eventKey: 'A mark is an event, such as a restart, a deadlock or a dump file. The list below says what each one is.',
  scaleKey: 'Every lane starts at zero and ends at its own highest value, so shapes compare by when they change, not by height. A lane where low is the worry shows its lowest value instead.',
  scale: (top: string): string => 'scale 0 to ' + top,
  scaleLow: (low: string, top: string): string => 'lowest ' + low + ', scale 0 to ' + top,
  notRecorded: 'not recorded',
  apiLane: 'From the audit database',
  eventsHeading: 'Events in this window',
  eventsNone: 'The recorder wrote no events in this window.',
  firstHeading: 'Which lane moved first?',
  firstHelp: 'The shown lanes that left their own normal range, earliest first. This is a clue and not proof.',
  firstNone: 'No shown lane left its own normal range in this window.',
  firstAt: (name: string, time: string): string => name + ' at ' + time,
  pickerTitle: 'Choose lanes',
  searchLabel: 'Search lanes',
  searchHint: 'Search: cpu, deadlock, redis, queue',
  pickerAvail: (n: number): string => n + ' lanes are available.',
  pickerRule: (max: number): string => 'You can show ' + max + ' at once so each stays readable. Changes apply right away.',
  pickerFoot: (n: number, max: number): string => n + ' of ' + max + ' lanes shown',
  pickerFull: (max: number): string => max + ' lanes are shown. Hide one to show this.',
  pickerDone: 'Done',
  pickerNoMatch: 'Nothing matches that. Try cpu, queue or deadlock.',
  groupCount: (on: number, all: number): string => on + ' of ' + all + ' shown',
  counterLabel: (text: string): string => 'Counter: ' + text,
  groupHost: 'The server itself',
  groupSql: 'SQL Server',
  groupIis: 'IIS, HTTP and ASP.NET',
  groupRedis: 'Redis',
  groupMem: 'Memory pressure',
  groupNet: 'Network (TCP)',
  groupLog: 'Error log and crash dumps',
  evRecorderStart: 'The recorder started again (a new run)',
  evGap: 'The recorder was not running',
  evWin: 'Windows wrote an entry to its error log',
  evDump: 'A new crash dump file appeared',
  evDeadlock: 'SQL Server ended one query in a deadlock',
  evNonYield: 'SQL Server reported a scheduler that stopped yielding',
  evIoStall: 'SQL Server reported a slow disk',
  evSqlStart: 'SQL Server started',
  evSqlStop: 'SQL Server stopped',
  evSqlError: 'SQL Server wrote an error to its log',
  laneHost: 'The server itself'
} as const;

/** Lane names and meanings, one plain pair per lane. Kept apart from the chrome copy so the lane table stays one place. */
export const BBL_LANE_TEXT: Record<string, { name: string; what: string }> = {
  cpu: { name: 'Host CPU busy', what: 'How busy the processors are, all together.' },
  mem: { name: 'Memory available', what: 'Memory nobody is using. Low is the worry.' },
  disk: { name: 'Disk time per transfer', what: 'How long one read or write to disk takes.' },
  dq: { name: 'Disk queue length', what: 'Reads and writes lined up for the disk.' },
  free: { name: 'Free space (recordings drive)', what: 'Free space on the drive the Blackbox saves to. Low is the worry.' },
  blk: { name: 'SQL queries held up', what: 'Queries stopped by another query that holds a lock.' },
  lwt: { name: 'SQL lock wait time', what: 'How long queries sit and wait on locks.' },
  dl: { name: 'SQL deadlocks', what: 'Two queries stuck on each other. SQL Server ended one.' },
  gp: { name: 'SQL memory grants pending', what: 'Queries that cannot start until memory is free.' },
  ple: { name: 'SQL page life expectancy', what: 'How long data stays in memory before it is pushed out. Low is the worry.' },
  bat: { name: 'SQL batch requests', what: 'How many requests SQL Server gets each second.' },
  hq: { name: 'Web requests queued', what: 'Requests held in the HTTP.sys queue before IIS picks them up.' },
  rej: { name: 'Web requests turned away', what: 'Requests refused when the queue was full.' },
  wr: { name: 'Website requests running', what: 'Requests the website process is working on right now.' },
  ping: { name: 'Website response check', what: 'How long the website process takes to answer a ping from the Blackbox.' },
  aq: { name: 'ASP.NET requests queued', what: 'Requests held inside the ASP.NET application.' },
  rmem: { name: 'Redis memory used', what: 'Memory the Redis cache holds.' },
  rcl: { name: 'Redis blocked clients', what: 'Clients stuck on a blocking Redis command.' },
  rops: { name: 'Redis commands per second', what: 'How many commands Redis runs each second.' },
  pgs: { name: 'Paging traffic', what: 'Data moved between memory and disk. High means memory is short.' },
  pp: { name: 'Windows kernel memory', what: 'Memory the Windows kernel itself holds (paged pool).' },
  tcpr: { name: 'TCP packets resent', what: 'Network packets that had to be sent again.' },
  evt: { name: 'Windows error log entries', what: 'Entries Windows wrote to its System or Application log.' },
  dmp: { name: 'New crash dump files', what: 'A program crashed and Windows saved a memory dump.' }
};

/** Every fixed string and a sample of each built string, for the copy lint. */
export function bbLanesCopyEntries(): LintEntry[] {
  const samples: Record<string, string> = {
    summary: BBL_TEXT.summary(8, 10),
    hide: BBL_TEXT.hide('Host CPU busy'),
    undid: BBL_TEXT.undid('Host CPU busy'),
    scale: BBL_TEXT.scale('83 %'),
    scaleLow: BBL_TEXT.scaleLow('1.09 GB', '6.91 GB'),
    firstAt: BBL_TEXT.firstAt('Host CPU busy', '15:21'),
    pickerAvail: BBL_TEXT.pickerAvail(24),
    pickerRule: BBL_TEXT.pickerRule(10),
    pickerFoot: BBL_TEXT.pickerFoot(8, 10),
    pickerFull: BBL_TEXT.pickerFull(10),
    groupCount: BBL_TEXT.groupCount(2, 5),
    counterLabel: BBL_TEXT.counterLabel('Memory\\Available Bytes')
  };
  const out: LintEntry[] = [];
  for (const [id, value] of Object.entries(BBL_TEXT)) out.push({ id: 'bbLanes.' + id, text: typeof value === 'function' ? samples[id] : value });
  for (const [id, v] of Object.entries(BBL_LANE_TEXT)) {
    out.push({ id: 'bbLanes.lane.' + id + '.name', text: v.name }, { id: 'bbLanes.lane.' + id + '.what', text: v.what });
  }
  return out;
}
