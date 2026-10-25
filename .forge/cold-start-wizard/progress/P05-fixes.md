# P05 fixes (F1 to F6)

Status: all six findings fixed. Web: `npm test` 1302 tests, 1302 pass, 0 fail (1297 before plus 5 new). `npm run check`: 0 errors, 0 warnings. No Go change, so Go was not run. Nothing committed.

## Files changed (all CRLF)

- web/src/lib/wizardCreate.svelte.ts: `scan-busy` blocker, `scanMissing()`, titles refreshed after a failed create.
- web/src/lib/wizardSubmit.ts: `reasonOf` and `isLocked` understand `ApiError`; `no-scan` reason; optional `findRecentCase` on `SubmitApi`, used before a retry of the case step.
- web/src/lib/wizardSubmitApi.ts: real `findRecentCase` (lists cases, same trimmed title, created at or after the failed try).
- web/src/lib/wizardSources.ts: `hostLabel`, `classifyDatabase`, new `bad-name` database problem.
- web/src/lib/appState.svelte.ts: `lockCases` keeps `create` as the unlock origin.
- web/src/lib/copy/wizardCreateCopy.ts: `scanBusy`, `no-scan`, `rate-limited`, `bad-request` lines (copy lint passes).
- web/src/lib/copy/wizardSourcesCopy.ts: `bad-name` line.
- web/src/components/wizard/ReviewStep.svelte: shows the `scanBusy` line (role="alert") when the blocker is set.
- web/tests/p05.clumsy.test.cjs: seven `bug:` tests renamed `fixed:`, assertions unchanged; five small `p05:` tests added; fake server got a `findRecentCase` list route (see deviations).

## Per finding

- F1: `blockers()` adds `scan-busy` when scan sources are chosen and the phase is not `done` or `scanId` is empty. Review shows its hint. A missing scan inside the run is now `no-scan` ("The scan of this computer was not ready...") instead of `no-window`.
- F2: before a retry of the case step, the run asks `findRecentCase(title, triedAt - 5s)` and reuses the case if found; a failed lookup just falls through to creating. `titles` is re-read after a failed create (so the repeat warning is true).
- F3: `ApiError` 429 -> `rate-limited`, 400/422 -> `bad-request`, 423 -> locked (run stops, resumes after unlock), other statuses -> `failed`, no status or a network error -> `unreachable`. Server text is never kept.
- F4: `unlockOrigin = screen === 'running' || screen === 'create' ? screen : 'start'`.
- F5: text is cut at the first `?` or `#`, the last `@` before that is the user-info end, the host ends at the first `/` after it. A password with an unencoded `/` still works; an `@` in options or fragment is ignored.
- F6: a Mongo database label is the tested name exactly. A name that looks like a secret or is over 80 characters is refused with a plain line under that database ("This database name is too long, or looks like a password..."), as `bad-name`.

## Deviations and notes

- The test's fake API had no way to list cases, and the real `listCases` cannot run under node, so the F2 test could not pass by any fix. I added a `findRecentCase` method to `fakeApi` (test infrastructure only; no assertion changed). If the real server's list route differs, the real adapter is the only untested part.
- F2 reuses a case created since the failed try began (minus 5 s clock slack), not a fixed "last minute". It is stricter against reusing an unrelated case and also covers a slow retry.
- F1: I did not add the "taken off your list" note to Review. The new blocker means Create cannot run while the scan is gone, and the Sources step still shows that note. The Review step shows the shorter sources list once the rescan ends.
- The `scanBusy` copy avoids the words the copy lint reads as a cause ("wait for").
