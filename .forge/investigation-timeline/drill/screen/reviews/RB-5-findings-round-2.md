# RB-5 round 2: Screen components (re-review of fixes)
Scope: lines fixing F-01, F-02, F-03, F-04, F-07, F-08 plus the Query Store block length fallback. Tests run: slowcall.screen 15/15, slowcall.controller 20/20, slowcall.nearbyui 7/7 pass.

F-01 | FIXED | web/src/components/SlowCallScreen.svelte:171,179-185 | allSources now gets only kind 'bar' IIS bars; zero-length IIS bars go in as marker items (endMs null, tookMs null, marker true, key `iis:<id>` same as the lane bar key) and reach CallLanes through `markers`; CallLanes draws only kind 'bar' bars (CallLanes.svelte:137), so no double draw.
F-02 | FIXED | web/src/components/SlowCallScreen.svelte:161 | a null or non-finite duration is skipped for the API lane packing instead of becoming 0 ms; the row stays in All sources with tookMs null (callReadout.ts:88) and the selected-row rebuild keeps null (line 289-296).
F-03 | FIXED | web/src/lib/drill/callController.ts:159-171, SlowCallScreen.svelte:378 | flag cleared at the start of each read and on a good one, set only on a non-aborted, non-stale failure (the aborted/stale/disposed guard returns first); the API lane area shows the note; test at slowcall.controller.test.cjs:323-342 covers set, clear and aborted.
F-04 | FIXED | web/src/components/SlowCallScreen.svelte:392,447 | overviewFailed note sits under the minimap and eventsTruncated note sits above All sources; both render only when the flag is true, so the normal-state lane height and "no inner scroll" rule are untouched.
F-07 | FIXED | web/src/components/CallNearby.svelte:44-45 | disabled user and endpoint chips use chipUserNone / chipApiNone ("Same user: not recorded", "Same endpoint: not recorded"); enabled-chip labels unchanged.
F-08 | FIXED | web/src/components/SlowCallScreen.svelte:188,200 | `pretendNow = demo ? pretend : null` is the only value passed to dbState and the drawn-lane override; the inspector switch also renders only under `demo`.
QS block length | FIXED | web/src/components/SlowCallScreen.svelte:110-122 | `lane.blockSeconds ?? qsBlockSeconds(lane.key)` applies to the sql key only, requires a finite number above 0, and returns undefined otherwise (null stays unset); used by the visible window, the overview strip and the dbState input.

Copy check: chipUserNone, chipApiNone, slowRowsFailed, overviewFailed, eventsTruncated (drillCopy.ts:201-205) are plain words, no banned word present.
Test check: each added test pins the fixed source text or controller behaviour and would fail on the round 1 code (F-01 regex rejects `iisBars: lanes.iis.bars,`; F-08 pins `demo ? pretend : null`; F-03 asserts the flag). Screen/nearby tests are source-text pins by the project's convention, so the QS fallback and marker mapping are not exercised at run time.

No new blocker or major found.
N-01 | P-32 | nit | confidence medium | web/src/lib/drill/drillCopy.ts:203 | slowRowsFailed ends "Try again." but no retry button sits next to the note (the retry button belongs to loadFailed only); the next window move or commit does re-read | say "Move the window to try again." or add a retry link that calls controller.retry()

UNCLEAR: (1) IIS lane events (tracing answer) and iis-* items from /events could be the same record with two different keys; if so a zero-length IIS item shows once from the lane (`iis:<id>`) and once from events (`iis:<source>:<evidence>`) in All sources. Same overlap existed for timed bars before; needs one live look. (2) F-05 (540 px lane height) still needs a browser measurement; not in this round's scope.
VERDICT: SHIP
