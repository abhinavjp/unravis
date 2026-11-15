# P-02 All new copy keys

model: sonnet, effort: medium. Reason: about 100 strings that must pass a causal-wording and reading-level lint.
risk: low. Review batch: RB-1. Wave: W0. Requirement: R-15 (BL-017).

## Outcome
Every string the Slow call screen needs exists in `DRILL_TEXT` / `DRILL_HELP`, is linted, and the copy test passes with the new counts. Later packets only read these keys.

## Write scope
- `web/src/lib/drill/drillCopy.ts`
- `web/tests/drill.copy.test.cjs`

## Must not change
- Any existing key or value in `DRILL_TEXT` / `DRILL_HELP` (the test pins many). The export names. `copylint.ts`, `deck.ts`.

## Changes
1. In `DRILL_TEXT`, before the closing `} as const;` (after `revealNote`), add the keys below, exactly. Functions keep the arrow form used in the file.
2. In `DRILL_HELP`, after `factDuration`, add the 8 help keys below.
3. In `SAMPLE` and in the `fns` list of `drillCopyEntries`, add one sample and one entry for every new function key (use the sample values given in the comments).
4. In the test: add the 8 keys to `HELP_KEYS`; change `18` to `26` in the `Object.keys(DRILL_HELP).length` assert; add one test `'slow call screen copy'` that asserts the exact output of: `crumbCall('10:14:07')`, `windowLine('09:59','10:29',15,15)`, `laneSlow(1)`, `laneSlow(3)`, `mapCaption(5)`, `dbSrcFile('11 Nov 2026')`, `tabNearbyCalls(12)`, `askingWider`.
5. Lint rules that reject text (`web/src/lib/copy/copylint.ts`): no `because`, `due to`, `caus*`, `wait for`, `waiting on`, `blocked by`, `led to`, `result in/from`, `responsible for`, `leak*`, `starv*`, `saturat*`, `proven`; grade ≤ 8.5 for strings of 8 words or more. If the lint reports `reading-level` on a string below, split it into shorter sentences without changing its meaning and list the final text in your report.

```ts
  // Slow call screen: header
  crumbTimeline: 'Timeline',
  crumbCall: (time: string): string => `Slow call ${time}`,            // '10:14:07'
  backToTimeline: 'Back to timeline',
  escHint: 'Esc',
  loadingLanes: 'Reading the three lanes for this window...',
  lanesFailed: 'The lanes could not be read.',
  // window control
  windowLabel: 'Time around this call',
  windowPreset: (m: number): string => `±${m} min`,                     // 15
  windowCustom: 'Custom',
  windowBefore: 'Minutes before',
  windowAfter: 'Minutes after',
  windowApply: 'Apply',
  windowRange: 'Type a number from 1 to 120.',
  windowLine: (from: string, to: string, before: number, after: number): string =>
    `Showing ${from} to ${to} (${before} min before, ${after} min after this call)`,   // '09:59','10:29',15,15
  windowLineMoved: (from: string, to: string): string => `Showing ${from} to ${to} (moved away from this call)`,
  windowWhole: 'The whole strip is shown',
  earlier: 'Earlier',
  later: 'Later',
  backToCall: 'Back to this call',
  sourcesLabel: 'Show these sources',
  // lanes
  timelineTitle: 'Timeline',
  laneApi: 'API calls',
  laneIis: 'Web server (IIS)',
  laneDb: 'Database',
  laneSource: (label: string): string => `Source: ${label}`,           // 'Query Store (live)'
  unitAtOnce: 'running at once',
  unitAtOnceAvg: 'running at once (average)',
  laneSlow: (n: number): string => (n === 1 ? '1 slow in this window' : `${n} slow in this window`),  // 3
  laneNothingSlow: 'nothing slow here',
  laneShape: (n: string): string => `Shape: 0 to ${n} at once`,        // '4.2'
  laneBlocks: (m: number): string => `saved in ${m}-minute blocks`,    // 15
  laneApiOnly: 'API calls only',
  laneEventsOnly: 'events only, no volumes',
  laneMoreBars: (n: number): string => `and ${n} more, see the list below`,  // 4
  legendWidth: 'Bar width = how long it took',
  legendShape: 'Faint shape = how many were running at once',
  legendHatched: 'Hatched = not recorded, which is unknown, not zero',
  legendCall: 'Red column = while this call was running',
  legendOutside: 'This call is outside the window you are looking at',
  // overview strip (minimap)
  mapHidden: 'The big chart already shows the whole strip, so there is nothing to move. Pick a shorter window above and the strip comes back here.',
  mapHow: 'How to read this',
  mapCaption: (minutes: number): string =>
    `The two hours around this call, one column per ${minutes === 1 ? 'minute' : minutes + ' minutes'}. The outlined box is what the big chart shows. The red line is the slow call.`,  // 5
  mapCaptionMore: 'Drag the box or use Earlier and Later to look at another time. Taller means more running at once. Each row is scaled to its own busiest column, so compare a row with itself, not with other rows. Hatched means no data was recorded, not zero.',
  mapPeak: (n: string, time: string): string => `peak ${n} · ${time}`,  // '4.2','10:14'
  mapNoData: 'no data',
  mapCell: (lane: string, n: string, time: string): string => `${lane}: ${n} running at once around ${time}`,   // 'API calls','4.2','10:14'
  mapCellNone: (lane: string, time: string): string => `${lane}: no data recorded for ${time}`,
  mapThisCall: (time: string): string => `this call ${time}`,
  mapDrag: 'drag',
  mapBoxLabel: 'The part of the strip the big chart shows. Use the left and right arrow keys to move it.',
  // readout under the hover line
  readTitle: 'Under the hover line',
  readHover: 'hover the lanes to read any moment, click to pin',
  readPinned: 'pinned; click the lanes to move it',
  readMoving: 'moving; the pinned line comes back when you leave',
  readThisCall: '(this call)',
  readApi: (n: string): string => `API calls running at once ${n}`,
  readIis: (n: string): string => `Web requests running at once ${n}`,
  readP95: (d: string): string => `slowest 5% (P95) ${d}`,            // '1.2 s'
  readRefused: (n: number): string => `refused or failed (4xx/5xx) ${n}`,
  readDb: (n: string): string => `Database running at once (average) ${n}`,
  readDbNone: 'Database not recorded',
  readEmpty: 'Nothing slow was recorded in this time step. The numbers above still count every call.',
  // inspector
  secThisCall: 'This call',
  secSelected: 'Selected',
  secWhile: 'While this call ran',
  secSources: 'Data sources',
  secNotShown: 'What this does not show',
  secHow: 'How to read the chart',
  selectedNone: 'Click a bar or a row to see it here.',
  notShownTime: 'The lanes are matched by time only. Two things in the same minute are not linked by the app.',
  notShownStart: 'A long call is counted in the time step it started in.',
  notShownWholeDb: 'The database lane covers the whole database, not only this API.',
  notShownIis: 'The web server log keeps whole seconds and the time a request ended.',
  coverageNote: 'Hatched means that source has nothing recorded for that time.',
  pretendLabel: 'Pretend the database is:',
  pretendNote: 'Demo only. This changes what the screen shows, not the data.',
  pretendReal: 'Real data',
  pretendQs: 'Query Store, live',
  pretendFile: 'Query Store file',
  pretendApi: 'Seen by the API',
  pretendOff: 'Query Store off',
  pretendRo: 'Read-only',
  pretendLate: 'Switched on later',
  pretendDenied: 'Permission denied',
  pretendEvents: 'Event markers only',
  pretendNotRead: 'Not read',
  // database source labels and state sentences
  dbSrcLive: 'Query Store (live)',
  dbSrcFile: (date: string): string => `Query Store file imported ${date}`,   // '11 Nov 2026'
  dbSrcApi: 'Seen by the API (time inside the database)',
  dbSrcNone: 'Not recorded',
  dbSrcLate: (time: string): string => `Query Store (live), nothing before ${time}`,   // '10:30'
  dbSrcReadOnly: 'Query Store (live), read-only',
  dbSrcEvents: 'SQL Server event files, markers only',
  dbMsgLive: (m: number): string => `Read from Query Store. It saves one summary per ${m}-minute block, so the database shape moves in steps and has no exact second.`,
  dbMsgFile: (date: string): string => `Read from a Query Store export file that was imported on ${date}. It ends where the export ended.`,
  dbMsgApi: "Query Store has no data for this period, so this lane shows the time this API's own calls spent inside the database. It leaves out other apps, so it can read lower than the real database load.",
  dbMsgOff: 'Query Store is switched off on this database, so it saved no history. Switching it on needs the database owner (the script is in Setup). Unravis never switches it on itself.',
  dbMsgReadOnly: 'Query Store is read-only, so it has stopped saving new history. This often happens when its storage is full. Older history is still shown.',
  dbMsgLate: (time: string): string => `Query Store has nothing before ${time}. It was switched on later, or the older history was cleaned up. The time before that is unknown, not zero.`,
  dbMsgDenied: 'The saved database login is not allowed to read Query Store. It needs the VIEW DATABASE STATE permission. Ask the database owner, or add a login that has it.',
  dbMsgEvents: "Only SQL Server's own event files are available. They show moments such as deadlocks and long blocking, but not how busy the database was. A marker shows when something happened, not why.",
  dbMsgNotRead: 'The database was not read for this time. Go back to the timeline and read it for a window that covers this call.',
  dbShortOff: 'Not recorded: Query Store is off',
  dbShortDenied: 'Not recorded: no permission',
  dbShortEvents: 'Not recorded: only events, no volumes',
  dbShortLate: 'Not recorded: Query Store has no history here',
  // lists
  tabNearbyCalls: (n: number): string => `Nearby calls (exact, API only · ${n})`,       // 12
  tabAllSources: (n: number): string => `All sources in time order (${n})`,
  nearbyHint: (from: string, to: string, n: number): string => `Calls from ${from} to ${to} · ${n} calls. The window is the one you set above.`,
  colTime: 'Time',
  colFromCall: 'From this call',
  colUser: 'User',
  colApi: 'API',
  colTook: 'Took',
  colDatabase: 'Database',
  colRedis: 'Redis',
  colWebCalls: 'Web calls',
  colSource: 'Source',
  colWhat: 'What',
  gapThis: 'this call',
  gapDuring: 'during',
  thisCallMark: '(this call)',
  askingWider: 'Asking the server for calls in this wider window...',
  windowCalls: (n: number, from: string, to: string): string => `${n} calls from ${from} to ${to}`,
  allHeading: (n: number): string => `What happened, in time order (${n})`,
  allEmpty: 'Nothing slow was recorded for these sources in this window.',
```

Help keys (each under 160 characters):
```ts
  laneApi: 'Calls your API handled, taken from its own audit log.',
  laneIis: 'Requests the web server logged. Its log keeps whole seconds only.',
  laneDb: 'Work inside the database. Query Store saves it in blocks, so this lane has no exact second.',
  atOnce: 'How many were in progress at the same moment, on average. It is worked out from how many started and how long they took.',
  minimap: 'A small picture of the two hours around the call. The box marks the part shown in the big chart.',
  windowControl: 'How much time before and after the call the screen shows. One control drives the chart and both lists.',
  nearbyCalls: 'Other API calls close in time to this one. They are exact records, one row per call.',
  allSources: 'Slow items from every source in one list, ordered by time. They are matched by time only.'
```

## Proof
- [ ] `cd web && node --test tests/drill.copy.test.cjs` → all pass, 0 fail (now 7 tests).
- [ ] `cd web && npx svelte-check --threshold error` → 0 errors.
- [ ] Every new function key has a `SAMPLE` entry and a `fns` entry (the test `lint entries use drill ids and cover every key` passes).
- [ ] `lintScreenStrings(drillCopyEntries(), deck.terms)` is `[]`; list any string you had to reword, old and new.
- [ ] `git diff --stat` shows only the two files in Write scope from this packet.

## Depends on
Nothing.
