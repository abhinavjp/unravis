/**
 * The words of the Suspects rail on the Investigation Timeline (the wireframe's right-hand panel). Fixed text and the catalogue's own
 * sentences live here so one test lints all of it; the engine in `lib/tracing/suspects.ts` only chooses which sentence applies.
 * Nothing here names a cause: a suspect is a place to look, and the rail says so.
 */
import type { LintEntry } from './copylint';

/** How many checks the full Unravis catalogue holds (internal/investigate/catalog.go). The rail lists the 26 it can show. */
export const CATALOGUE_TOTAL = 44;

export const SUSPECT_TEXT = {
  heading: 'Suspects',
  label: 'Suspects',
  lead: 'Suspects, not causes. Nothing here says why it happened.',
  scope: 'This list reads only what the timeline holds: event counts for each lane. It compares the first half of the window with the second half.',
  noRise: 'No meaningful rise was found in this window, so no suspect is ranked.',
  noRed: 'Red needs call times to show which part slowed first. The timeline holds counts only, so nothing is red here.',
  nothingToRead: 'No lane has events in this window, so no suspect can be tested. Add evidence or choose another window.',
  legendLabel: 'What the colours mean',
  red: 'Red',
  orange: 'Orange',
  green: 'Green',
  grey: 'Grey',
  meanRed: 'most likely (its tier clearly moved first).',
  meanOrange: 'likely.',
  meanGreen: 'ruled out by the data we have.',
  meanGrey: 'not sure. It could not be tested, or does not apply. Grey is never a pass.',
  seeAll: (n: number) => `See all (${n})`,
  showFewer: 'Show fewer',
  ruledOut: (n: number) => `Ruled out by the data (${n})`,
  notShown: (n: number) => `${n} more checks in the full Unravis catalogue are not shown in this view.`,
  summary: (total: number, red: number, orange: number, grey: number, green: number) =>
    `Checked ${total} suspects: ${red} red, ${orange} orange, ${grey} grey, ${green} ruled out (green).`,
  dataCheck: 'Data check',
  showOnChart: 'Show on chart',
  showOnChartLabel: (id: string) => `Show ${id} on the chart`,
  evidence: 'Evidence and checks',
  colSignal: 'Signal',
  colRole: 'Role',
  colReading: 'Reading',
  colWhy: 'Why',
  roleMust: 'Must',
  roleExpect: 'Expect',
  yes: 'Yes',
  no: 'No',
  unknown: 'Unknown',
  cheapest: 'Cheapest check:',
  overturn: 'What would overturn it:',
  needs: 'Needs:',
  moment: (lane: string, time: string) => `The ${lane} count first rose at ${time}.`,
  evidenceCaveat: 'This reading comes from the timeline counts only.'
} as const;

export const RESULT_TEXT = {
  consistent: 'Consistent',
  partly: 'Partly consistent',
  contradicted: 'Contradicted',
  cannot: 'Cannot test yet'
} as const;

/** The sentences a card shows for each way a result can come out. */
export const RESULT_WHY = {
  contradicted: (why: string) => `A reading it needs is absent. ${why}`,
  cannot: (why: string) => `Missing data. ${why}`,
  partlyUnknown: (why: string) => `A reading it needs is unknown. ${why}`,
  partlyExpect: (why: string) => `The main reading fits. One that goes with it does not. ${why}`,
  consistent: (why: string) => `Every reading it needs is present. ${why}`
} as const;

/** What a signal says when the timeline cannot read it. */
export const SIGNAL_UNKNOWN = 'The timeline holds counts only. This reading is not there yet.';

/** The sentences the four readings the timeline can make give back. Every figure is passed in; none is invented here. */
export const SIGNAL_WHY = {
  laneMissing: (lane: string) => `${lane} is not in this timeline.`,
  laneNoFile: (lane: string) => `${lane} has no file in this case.`,
  laneNotEvents: (lane: string, note: string) => `${lane}: ${note || 'not read in full'}`,
  laneNotFull: (lane: string) => `${lane} was not read in full, so its counts are not complete.`,
  laneZone: (lane: string) => `${lane} is switched off until its time zone is checked.`,
  tooFew: (lane: string, n: number) => `${lane} has only ${n} events in the window. A rise needs at least 30.`,
  tooShort: (lane: string) => `${lane} has too few bars in this window to compare two halves.`,
  rise: (lane: string, before: string, after: string, ratio: string) =>
    `${lane}: events per bar went from ${before} in the first half to ${after} in the second half (x${ratio}). A rise counts at x1.5.`,
  riseFromNone: (lane: string, after: string) => `${lane}: no events in the first half, ${after} per bar in the second half.`,
  steady: (lane: string, before: string, after: string, ratio: string) =>
    `${lane}: events per bar went from ${before} in the first half to ${after} in the second half (x${ratio}). A rise counts at x1.5.`,
  clockYes: (lanes: string) => `Switched off until the time zone is checked: ${lanes}.`,
  clockNo: 'No lane was switched off for its time zone. Calls that end before they start are not checked here.',
  clockNone: 'No lane could be read, so the time zones were not checked.',
  dipNeedBoth: 'Both the API audit and the web server (IIS) are needed to compare their counts.',
  dipYes: (before: string, after: string) =>
    `The audit holds ${before} audit calls per IIS request in the first half and ${after} in the second half.`,
  dipNo: (before: string, after: string) =>
    `The audit holds ${before} audit calls per IIS request in the first half and ${after} in the second half. The share held.`
} as const;

/** One plain label for each reading a suspect can need. */
export const SIGNAL_TEXT: Record<string, string> = {
  arrivalsUp: 'Calls per minute up 50% or more',
  auditUp: 'API slow-request time up meaningfully',
  scopeNarrow: 'The rise sits on a few routes (3 or fewer)',
  scopeAll: 'The rise is on nearly every route (80% or more)',
  p50Flat: 'The typical call (median) did not slow',
  outsideLarge: 'Time outside the application, between the web server and the audit, is large and up',
  queueBurst: 'HTTP.sys queue rejections (QueueFull, Timer_AppPool) in a burst',
  dotnetQueue: '.NET or ASP.NET request queue stays above zero',
  mongoSlow: 'Slow audit writes (MongoDB slow operations up)',
  insertUp: 'Audit-collection insert time up meaningfully',
  payloadUp: 'Payload recording time up meaningfully',
  filterReject: 'Request-filtering rejections (sub-status) up',
  appPoolFail: 'Application-pool unavailable or worker failure rows',
  recvTimeout: 'Header or body receive timeouts (HTTP.sys)',
  drain: 'Slow clients drop off, or outside time is up while the app is flat',
  sqlUp: 'Slow database calls (over 1 s) up 50% or more',
  blocking: 'Blocked-process reports rose',
  lockWait: 'The top server wait is a lock wait',
  deadlocks: 'Deadlock storm (5 or more reports, and twice the earlier rate)',
  memGrant: 'Queries held up for a memory grant',
  diskSlow: 'Disk latency at or over the bottleneck line (15 ms)',
  runQueue: 'Runnable queue on half the schedulers or more, 5 samples in a row',
  attention: 'Command timeouts or cancels (attention events) come in a burst',
  loginStorm: 'Login failures (error 18456) and login attempts in a burst, well above the earlier rate',
  mongoQueue: 'MongoDB operations queued',
  otherShare: 'Other databases’ share of file I/O up',
  spill: 'Operations that spilled to disk (usedDisk)',
  mongoOpUp: 'MongoDB operation time up meaningfully',
  connRejected: 'Incoming connections rejected',
  cpuHigh: 'CPU at 80% or more',
  cpuUp: 'CPU up 30% or more from the earlier part of the window',
  sharedHost: 'A second workload on the same host degrades too',
  allSlow: 'The API, the web server and the database all slowed',
  sameMoment: 'All three moved at the same moment',
  changesLead: 'A listed change sits just before the start',
  clockSkew: 'A lane is switched off until its time zone is checked',
  auditDip: 'The audit share of web requests fell'
};

/** The text of one catalogue entry. The structure (which readings it needs) is in `lib/tracing/suspects.ts`. */
export interface SuspectEntryText { title: string; check: string; overturn: string; needs: string }

export const SUSPECT_ENTRY_TEXT: Record<string, SuspectEntryText> = {
  Q1: { title: 'Clock, time-zone or daylight-saving error', check: 'Compare the time-zone rule of each import, and count calls that end before they start.', overturn: 'No call ends before it starts, and every import uses the same time-zone rule.', needs: 'Start and end times of the audit, IIS and SQL rows, with the time-zone rule of each import.' },
  Q2: { title: 'Audit calls missing from the record', check: 'Audit calls per minute divided by the IIS request count for the same paths.', overturn: 'Audit and IIS counts agree.', needs: 'IIS log rows with enough requests in the window.' },
  L1: { title: 'Traffic surge', check: 'Calls per minute against the earlier part of the window. A call time that did not move early points to a surge, not a slowdown.', overturn: 'Calls per second flat or down.', needs: 'API audit rows with enough calls in the window.' },
  L2: { title: 'Mix shift: one route or client dominates', check: 'Group the slow calls by route, client and body size.', overturn: 'The rise is spread across routes.', needs: 'Call times per route from the API audit.' },
  S1: { title: 'HTTP.sys or IIS queue backup', check: 'Count HTTPERR rows with QueueFull, Timer_AppPool or AppPoolTimer per 5 minutes. Then compare the queue-length counters with the audit time.', overturn: 'No such HTTPERR rows, and the queue length stays at zero.', needs: 'HTTPERR log files for the window.' },
  S2: { title: 'Thread-pool queue backup in the .NET host', check: 'Thread-pool queue length and thread count, and the time of a trivial route.', overturn: 'Thread-pool queue length flat at zero.', needs: 'IIS log rows, and .NET counters for a direct reading.' },
  S5: { title: 'Audit-write coupling', check: 'Server-side time of inserts on the audit collection only. Confirm the write is awaited inside the request.', overturn: 'Audit-collection inserts did not slow.', needs: 'MongoDB profiler switched on, so the insert on the audit collection can be timed on its own.' },
  S8: { title: 'Payload capture or response-body slowdown', check: 'Payload recording time by route against the earlier part of the window. Then body size on the slowest routes.', overturn: 'Payload recording time flat while the audit time rose.', needs: 'The API audit with payload recording time.' },
  S9: { title: 'IIS request-filtering rejection', check: 'Count W3C rows by sc-status.substatus (404.5 to 404.8, 404.11, 404.12, 404.14, 404.15, 404.18, 404.19, 413.1, 431).', overturn: 'Those sub-status counts are unchanged.', needs: 'IIS logs that record sc-substatus.' },
  S10: { title: 'Application pool unavailable or worker failure', check: 'Count HTTPERR rows with AppOffline, AppShutdown or AppPoolTimer. Compare them with the pool events of the host.', overturn: 'None of those reasons appear in a complete HTTPERR log.', needs: 'HTTPERR log files for the window.' },
  S11: { title: 'HTTP.sys header or body receive timeout', check: 'Count HTTPERR rows with Timer_HeaderWait or Timer_EntityBody per 5 minutes. Group them by client.', overturn: 'Those rows are rare, or no higher than earlier in the window.', needs: 'HTTPERR log files for the window.' },
  S12: { title: 'Slow client or proxy: response drain or disconnect', check: 'Count HTTPERR rows with Timer_MinBytesPerSecond or Connection_Dropped per 5 minutes. Or take IIS time minus the audit time.', overturn: 'No such rows, and time outside the application did not rise.', needs: 'HTTPERR log files and IIS log rows for the window.' },
  D1: { title: 'Blocking chain (head blocker or open transaction)', check: 'Head blocker in the blocking snapshot or blocked-process report.', overturn: 'No lock waits.', needs: 'SQL Server wait, blocking or Query Store snapshots for the window.' },
  D2: { title: 'Deadlock storm and retries', check: 'The deadlock events kept by system_health.', overturn: 'No deadlocks logged.', needs: 'SQL Server wait, blocking or Query Store snapshots for the window.' },
  D6: { title: 'Memory pressure or grant waits', check: 'Memory grants with a null grant time.', overturn: 'No grant waits.', needs: 'SQL Server wait, blocking or Query Store snapshots for the window.' },
  D9: { title: 'Disk IO latency', check: 'Per-file IO stall changes.', overturn: 'Normal IO latency.', needs: 'SQL Server wait, blocking or Query Store snapshots for the window.' },
  D10: { title: 'SQL scheduler pressure', check: 'Runnable queue per scheduler, then SQL CPU against OS CPU.', overturn: 'No runnable queue.', needs: 'SQL Server wait, blocking or Query Store snapshots for the window.' },
  D16: { title: 'SQL command timeout or cancellation burst', check: 'Count attention events per 5 minutes. List the statements they cancelled.', overturn: 'Fewer than 3 attention events in any 5 minutes, and under 2% of calls.', needs: 'Stored SQL Server events that include attention events for the window.' },
  D17: { title: 'SQL login or authentication storm', check: 'Error 18456 and login attempts per 5 minutes, by client and state.', overturn: 'Login attempts are near the earlier rate.', needs: 'Stored SQL Server events that include login events for the window.' },
  M5: { title: 'Ticket or lock queue', check: 'Duration minus working time, and the global lock queue.', overturn: 'Both equal.', needs: 'Slow lines from the MongoDB log, or its status.' },
  M10: { title: 'Shared mongod: another tenant or maintenance job', check: 'Group slow lines by database. Group current operations by namespace.', overturn: 'Other databases flat while this one is slow.', needs: 'Slow lines from the MongoDB log, or its status.' },
  M11: { title: 'MongoDB spill to disk', check: 'Profiler entries with usedDisk set, by query shape.', overturn: 'Fewer than 3 operations used disk, or operation time is flat.', needs: 'MongoDB profiler switched on, so usedDisk is kept.' },
  M12: { title: 'MongoDB incoming-connection rejection', check: 'Compare connections.rejected between samples. Note the limit.', overturn: 'connections.rejected did not move.', needs: 'MongoDB 6.3 or later with server status kept (connections.rejected).' },
  H1: { title: 'Host CPU contention', check: 'CPU by process.', overturn: 'Process CPU accounts for it.', needs: 'Host performance counters for the window.' },
  H6: { title: 'Shared effect on every tier (VM, storage, network)', check: 'Do small, unrelated tasks slow at the same moment on every tier?', overturn: 'Only one tier is slow.', needs: 'Host performance counters for the window.' },
  C1: { title: 'Deploy, configuration or infrastructure change', check: 'Change calendar against the start of the rise: a lead, not an overlap.', overturn: 'No change listed within 30 minutes before the start.', needs: 'The list of releases and changes, typed in.' }
};

/** Every fixed sentence, with sample figures where a sentence takes them, for the screen-text lint. */
export const suspectsCopyEntries = (): LintEntry[] => {
  const fixed = Object.entries(SUSPECT_TEXT)
    .filter(([, value]) => typeof value === 'string')
    .map(([key, value]) => ({ id: 'suspects.' + key, text: value as string }));
  const sampled: LintEntry[] = [
    { id: 'suspects.seeAll', text: SUSPECT_TEXT.seeAll(26) },
    { id: 'suspects.ruledOut', text: SUSPECT_TEXT.ruledOut(3) },
    { id: 'suspects.notShown', text: SUSPECT_TEXT.notShown(18) },
    { id: 'suspects.summary', text: SUSPECT_TEXT.summary(26, 0, 1, 22, 3) },
    { id: 'suspects.showOnChartLabel', text: SUSPECT_TEXT.showOnChartLabel('L1') },
    { id: 'suspects.moment', text: SUSPECT_TEXT.moment('API requests (audit log)', '20 Oct 09:42') }
  ];
  const why = [
    RESULT_WHY.contradicted('Slow database calls did not rise.'), RESULT_WHY.cannot(SIGNAL_UNKNOWN), RESULT_WHY.partlyUnknown(SIGNAL_UNKNOWN),
    RESULT_WHY.partlyExpect(SIGNAL_UNKNOWN), RESULT_WHY.consistent('Calls per minute rose.')
  ].map((text, i) => ({ id: 'suspects.resultWhy.' + i, text }));
  const lane = 'API requests (audit log)';
  const signals: LintEntry[] = [
    SIGNAL_UNKNOWN, SIGNAL_WHY.laneMissing(lane), SIGNAL_WHY.laneNoFile(lane), SIGNAL_WHY.laneNotEvents(lane, 'Files do not cover this time'),
    SIGNAL_WHY.laneNotFull(lane), SIGNAL_WHY.laneZone(lane), SIGNAL_WHY.tooFew(lane, 12), SIGNAL_WHY.tooShort(lane),
    SIGNAL_WHY.rise(lane, '12.5', '31.0', '2.5'), SIGNAL_WHY.riseFromNone(lane, '31.0'), SIGNAL_WHY.steady(lane, '12.5', '13.0', '1.0'),
    SIGNAL_WHY.clockYes(lane), SIGNAL_WHY.clockNo, SIGNAL_WHY.clockNone, SIGNAL_WHY.dipNeedBoth, SIGNAL_WHY.dipYes('0.80', '0.40'),
    SIGNAL_WHY.dipNo('0.80', '0.78')
  ].map((text, i) => ({ id: 'suspects.signalWhy.' + i, text }));
  const labels = Object.entries(SIGNAL_TEXT).map(([key, text]) => ({ id: 'suspects.signal.' + key, text }));
  const results = Object.entries(RESULT_TEXT).map(([key, text]) => ({ id: 'suspects.result.' + key, text }));
  const catalogue = Object.entries(SUSPECT_ENTRY_TEXT).flatMap(([id, entry]) => [
    { id: `suspects.${id}.title`, text: entry.title }, { id: `suspects.${id}.check`, text: entry.check },
    { id: `suspects.${id}.overturn`, text: entry.overturn }, { id: `suspects.${id}.needs`, text: entry.needs }
  ]);
  return [...fixed, ...sampled, ...why, ...signals, ...labels, ...results, ...catalogue];
};
