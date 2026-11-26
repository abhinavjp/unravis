# Whole-project review: fix plan

Date: 2026-11-26. Findings and evidence are in [FINDINGS.md](FINDINGS.md). Line numbers are for `develop` at `0d4fdbec`; find the code by the quoted names if lines have moved.

## How to use this plan

- Each work item is written so a fresh agent can do it from the item text alone. One item, one branch, one pull request to `develop`.
- Items are ordered by rating first and by dependency second. "Needs" names an item that must merge first. Items with no "Needs" can run side by side as long as they do not share files (the "Files" line shows this).
- **Model** says who implements. Haiku is for mechanical, fully specified changes. Sonnet is for normal work. **Opus review** is asked only where the item touches security, credentials, data loss, or release and setup deletion.
- Every item follows the repo rules: run `bash tools/ci/prepush.sh` before the push, push once, keep tests Windows-safe, never commit `web/dist`, `cmd/unravis-setup/ui` or `cmd/unravis-lite/ui`. Name the test that fails without the change in the pull request.
- **UI rule.** Any item marked "UI" changes what a person sees. Build a standalone HTML prototype first, design it with the Impeccable and slop-check skills, and get nachi's approval before changing the real screen. Check the wording for a first-time investigator. Desktop layout wins over phone.
- **Live data rule.** An item is done only when it works on the real data path. Tick the backlog line only then.
- Items marked "Confirm first" start by proving the problem with a failing test. If it cannot be proved, close the backlog line with a note instead of changing code.
- Nothing here needs a new collector or agent.

## Order at a glance

| Wave | Items | Why |
|---|---|---|
| 1 | W-01 to W-05 | The three High findings |
| 2 | W-06 to W-10 | Security, update and shutdown: Medium findings where a wrong fix is costly |
| 3 | W-11 to W-26 | The other Medium findings |
| 4 | W-27 to W-43 | Low findings that are confirmed |
| 5 | W-44 to W-52 | Low findings to confirm first |
| Owner | O-1 to O-5 | Need nachi's decision or hands |

---

## Wave 1: High

### W-01 Network failures become one plain-words error (RV-UI-1, part 1)

- **Model**: Sonnet. No Opus review.
- **Files**: `web/src/lib/api.ts`, a new `web/src/lib/copy/unreachableCopy.ts`, `web/src/lib/api.test.ts` (or the existing api test file).
- **Change**: In `request()` (`api.ts:109-122`), after the timeout branch, catch a failure that is not an `ApiError` and not an abort started by the caller. That is the browser's network failure (`TypeError`). Throw `new ApiError(UNREACHABLE_MESSAGE, 'unreachable', undefined, 0)` and call `reportError` the same way the timeout branch does. Keep abort errors untouched. Put the sentence in the new copy file: "Unravis could not be reached. It may have been closed or updated. Start Unravis again, then reload this page." Make `startupCopy.ts:36-40`, `caseWorkspace.ts:370` and `cases.ts:213-219` recognise the new code so their own wording still wins where it exists.
- **Test**: Unit test with `fetch` mocked to reject with `TypeError('Failed to fetch')`: `request` rejects with `ApiError`, code `unreachable`, status 0, and the message is the copy text. A caller abort still rejects with the abort error. Run `npm test` in `web`.
- **Risk**: Low. Callers that test `error instanceof ApiError && error.status === 0` now also see network failures. Search for `status === 0` and `TIMEOUT_CODE` and check each.
- **UI**: text only, but still under the UI rule. Show the sentence in the W-02 prototype.

### W-02 The page says when the app has stopped (RV-UI-1, part 2; RV-UI-2)

- **Model**: Sonnet. No Opus review.
- **Needs**: W-01.
- **Files**: new `web/src/lib/liveness.svelte.ts`, `web/src/App.svelte`, a new small banner component under `web/src/components/`, `web/src/lib/updateState.svelte.ts`.
- **Change**: Add a liveness store. It counts `unreachable` failures in a row from any request (hook it where W-01 calls `reportError`) and resets on any success. After 3 in a row, or a failed light poll (reuse an existing cheap GET such as the health or update-status call, every 20 seconds while the tab is visible), set `stopped = true`. `App.svelte` shows a banner across the top: what happened, "Start Unravis again", and a Reload button. When a later poll succeeds but answers 401 (a new app run has a new token), the banner says the page belongs to an earlier run and offers "Open Unravis again from the tray". In `updateState.svelte.ts:25-33` replace the empty catch with a call into the same store, so the update bar stops showing a stale "being applied".
- **Test**: Unit tests for the store (3 failures set stopped; a success clears it; hidden tab does not poll). Component test that the banner renders. Manual check on a real run: start the app, open the page, quit the app from the tray, wait, see the banner; start it again, see the "earlier run" text.
- **Risk**: Medium. A false "stopped" during a long import would alarm people. Timeouts (`TIMEOUT_CODE`) must not count, only `unreachable`.
- **UI**: yes. Prototype first.

### W-03 Blackbox keeps saying it is failing, and says when it recovers (RV-BB-7)

- **Model**: Sonnet. No Opus review.
- **Files**: `internal/blackbox/recorder.go`, `internal/blackbox/manifest.go` (event kinds), `internal/blackbox/recorder_test.go`.
- **Change**: Today `r.collectOK` and `r.writeOK` gate one event at the first failure (`recorder.go:354-360`, `:383-398`). Keep that first event. Add per-kind counters and a `lastRepeat` time. While failing, raise a repeat event at most once every 15 minutes carrying the count of failed rounds since the last event and the newest code. When a round succeeds after failures, raise `EventCollectRecovered` or `EventWriteRecovered` with the total count and how long it lasted. Do not raise more than one event per 15 minutes per kind, so RV-BB-9's 200-event window is not flooded.
- **Test**: Table test with a fake collector that fails N rounds over a fake clock: 1 first event, repeats at the 15 minute marks, 1 recovered event with the right count. Same for a failing store.
- **Risk**: Low. Event kinds are read by the web panel; unknown kinds must render as plain text. Check `web/src` for a switch over event kinds and add wording for the new ones (that part is UI, text only).

### W-04 A slow host read no longer costs the whole sample (RV-BB-1)

- **Model**: Sonnet. No Opus review.
- **Needs**: W-03 (uses its repeat events).
- **Files**: `internal/blackbox/groups.go`, `internal/blackbox/groups_test.go`, `internal/blackbox/state.go` (status word).
- **Change**: In `groupCollector.Collect` (`groups.go:489-501`), `errHostBusy` and `errHostSlow` return an empty sample. Change two things. (1) Make the budget adaptive: keep 2 seconds as the start; after 3 slow rounds in a row raise it in steps (4 s, then 8 s), never above half the configured interval; lower it one step after 20 rounds in a row under half the current budget. (2) When the host read is still busy or slow at the current budget, do not return an error: return a sample with the counter groups filled and the host fields absent, and mark it (`s.HostMissing = true` or the existing per-group timing list `c.gt` with a "host" entry). Check how readers treat missing host fields before choosing the marker; the charts must show a gap, not zeros. Add a status reason "host read is slow on this PC" when more than half of the last 20 samples lack host data.
- **Test**: Fake inner collector that sleeps past the budget: the first rounds yield partial samples (groups present), the budget steps up, and a round under the new budget yields a full sample. A fake that is fast again steps the budget down. A reader test that a partial sample is drawn as a gap.
- **Risk**: Medium. The sample format is read by the store, the series routes and the web charts. A partial sample must not be mistaken for zero CPU. Read `internal/blackbox/series*.go` and the web chart code for nil handling before merging.
- **Live check**: on a real PC, run with `hostBudget` forced to 1 ms in a test build and confirm partial samples appear in the panel as gaps with the reason text.

### W-05 Releases dashboard says when its data is old (RV-REL-1)

- **Model**: Sonnet. No Opus review.
- **Files**: `cmd/unravis-releases/ui/data.js`, `cmd/unravis-releases/ui/app.js`, the UI's CSS file, the UI tests under `cmd/unravis-releases/ui` if present.
- **Change**: In `loadSlow` (`data.js:58-63`) record `D.snapshotAt = Date.now()` on success and keep `D.snapshotErr` on failure (it does this already). In `loadFast`, on failure set `D.err` and keep `D.at`. In `app.js`, `viewReleases` (`:440-442`) and the shared header: when `D.snapshotErr` is set and a snapshot exists, show a banner above the content: "Showing releases as read at (time). Could not refresh: (reason)." Do the same for the fast data with `D.err`. Make the freshness chip show the older of the two times, and turn it to the warning style when either is older than 3 poll periods.
- **Test**: Unit test of `loadSlow` with `fetch` mocked to fail after one success: `D.snapshot` kept, `D.snapshotErr` set, `D.snapshotAt` unchanged. Render test that the banner text appears. Manual: run `unravis-releases`, disconnect the network, wait for a slow poll, see the banner.
- **Risk**: Low.
- **UI**: yes. Prototype first.

---

## Wave 2: security, update and shutdown

### W-06 Imported saved connections get the same checks as saved ones (RV-SEC-1)

- **Model**: Sonnet. **Opus review required** (credentials, cross-user).
- **Files**: `internal/sqlconn/sqlconn.go`, `internal/sqlconn/sqlconn_test.go`, `internal/keeper/backend.go` (only if the call is narrowed).
- **Change**: In `Service.Import` (`sqlconn.go:432-465`) each record is accepted with only an id, host and password-flag check. For each record, build an `Input` from its fields and run `validate()` (`sqlconn.go:166`), the same function `Save` uses. Do not trust `rec.Server`: set it from the validated result (host and port), the way `Save` does. Check `Auth` and `Encrypt` against the allowed values. Keep the id and save times from the record as today. If a record fails, return `ErrInvalid` and write nothing, as today. Then look at who calls `sql.import` (`internal/keeper/backend.go:75`, `:210-219`): if only the one-time migration needs it, refuse it once the migration marker is set.
- **Test**: New test: import `{ID:"abc", Label:"Payroll DB", Server:"attacker.example,1433", Host:"payroll-db", Port:1433, Auth:"sql", ...}` then `Resolve("abc")` returns a server built from `payroll-db` and 1433, never `attacker.example`. Import with `Auth:"bogus-auth"` or `Encrypt:"whatever"` returns `ErrInvalid` and the store is unchanged. The existing migration test still passes with real old records.
- **Risk**: Medium. Old records made by earlier versions might fail the stricter check and be dropped at migration. Run the migration test fixtures, and prefer "skip the bad record and count it" over "fail the whole import" only if a real old shape needs it. Say which in the pull request.

### W-07 "Launched" with no result times out (RV-SUP-1)

- **Model**: Sonnet. **Opus review required** (update and setup).
- **Files**: `internal/server/updater.go`, `internal/server/updater_auto.go`, `internal/server/updater_install.go`, their tests.
- **Change**: `u.inst.launchedAt` already records the start (`updater.go:392-400`). `Status()` already turns "launched" into "failed" when a bad result is on disk (`updater.go:255-260`). Add the missing case: state "launched", `launchedAt` older than a limit (20 minutes), no result written after `launchedAt`, and no setup process running. Check for the process with the existing single-instance or process lookup used by setup; if none exists, use the limit alone. In that case set the state back to "ready" with a plain note: "The update did not finish. You can try again." In `autoStep` (`updater_auto.go:230-232`) the `default:` branch returns for "launched": after the time-out the state is "ready", so the normal path runs again. Count this as one automatic failure so the existing back-off applies and it cannot loop.
- **Test**: With a fake clock: launch, advance 21 minutes with no result, `Status()` shows "ready" with the note and the automatic step may start again after back-off. With a result file written after `launchedAt`, the existing behaviour is unchanged. With setup still running, no time-out.
- **Risk**: Medium. Starting a second installer while the first is still working would be worse than the bug. The running-process check or a generous limit must hold. Setup's own single-instance lock is the last guard: confirm it exists and say so in the pull request.

### W-08 Archive and verify jobs stop at shutdown (RV-SRV-2)

- **Model**: Sonnet. **Opus review required** (data loss: a half-written archive).
- **Files**: `internal/server/import_jobs.go` (`CancelImports`), `internal/server/archive_jobs.go`, `internal/server/archive_routes.go`, `internal/server/archive_verify.go`, tests.
- **Change**: `CancelImports` (`import_jobs.go:324-330`) stops discovery, samplers and imports. Add the file jobs: call a new `fileJobs.shutdown(d time.Duration)` that cancels every running job with a "shutting down" reason (not the key-lock reason `stopAll` uses, so the next start does not tell the person a key was locked) and waits up to `d` (5 seconds) for their `done` channels, the way `stopCase` waits (`archive_jobs.go:207-229`). Confirm that a cancelled archive removes its partial output file; if it does not, make it.
- **Test**: Start an archive job on a fake slow writer, call `CancelImports`, assert the job's context is cancelled, `done` closes within the limit, and no partial file is left. Run with `-race`.
- **Risk**: Medium. Shutdown must not hang: the wait is bounded and the process exits after it either way.

### W-09 A person's reconnect always dials (RV-SRV-1)

- **Model**: Sonnet. No Opus review.
- **Files**: `internal/server/reconnect.go`, `internal/server/reconnect_test.go`.
- **Change**: The loop at `reconnect.go:449-452` replays a cached failure via `h.recentFailure(key)`. The start-up pass stores failures with `startupCooldown` (2 minutes, `:30-31`). Record with each cached failure whether the start-up pass or a person made it. When a person starts the attempt (the route behind Start tracing and the per-lane Connect), ignore failures cached by the start-up pass and honour only the short `reconnectCooldown` (20 seconds) set by a person's own attempt. The start-up pass keeps its behaviour.
- **Test**: Fake dialer that fails once then succeeds: start-up pass fails and caches; a person's call 5 seconds later dials and succeeds. A second person's call within 20 seconds of a person's failure is still answered from the cache.
- **Risk**: Low.

### W-10 Local CI trusts only verified owner commits (RV-SEC-3)

- **Model**: Sonnet. **Opus review required** (security).
- **Needs**: O-4 answered first (it decides whether this is code or a repository setting).
- **Files**: `internal/localci/trust.go`, `internal/localci/trust_test.go`, the GitHub client code that fills `AuthorLogin` and `CommitterLogin`.
- **Change**: `CommitsByOwner` (`trust.go:41-58`) compares logins only. Read GitHub's `commit.verification.verified` for each commit and require it to be true, or (simpler and stronger) trust a PR only when the pusher of the head commit is the owner, taken from the events API. Unverified commits fall to the untrusted path, which already exists (`isolate.go:23-24`).
- **Test**: Table test: owner login + verified true is trusted; owner login + verified false is untrusted; `web-flow` committer with verified true is trusted.
- **Risk**: Medium. If the owner's commits are not signed today, every owner PR would drop to the untrusted path and CI would slow down. That is why O-4 comes first.

---

## Wave 3: other Medium

### W-11 Blackbox "paused" is stored, not guessed from events (RV-BB-9)

- **Model**: Sonnet. No Opus review.
- **Files**: `internal/blackbox/manifest.go`, `internal/blackbox/store.go`, `internal/blackbox/state.go`, `internal/blackbox/recorder.go`, tests.
- **Change**: Add `PausedDiskLow bool` and `PausedSince time.Time` to the manifest. Set them where `EventPausedDiskLow` is raised and clear them where `EventResumed` is raised. `pausedForDisk` (`state.go:216-227`) reads the field. For a manifest written by an older version (field absent), fall back to the event scan once and store the result.
- **Test**: Pause, add 250 unrelated events, assert status still says paused. Old-manifest fixture still reads correctly.
- **Risk**: Low. The manifest is read by older service versions during an update: an added field must be ignored by them. Check the decoder is not strict.

### W-12 Crash capture for the Blackbox service (RV-BB-10)

- **Model**: Sonnet. No Opus review.
- **Files**: `cmd/unravis-blackbox/main.go`, `internal/crashlog` (reuse), `internal/blackbox/diag.go`.
- **Change**: Use `internal/crashlog` the way `cmd/audit-explorer` does: a deferred recover at the top of `main` and of each long-lived goroutine the service starts, writing a scrubbed crash record into the Blackbox data folder, then exiting non-zero so the service manager restarts it. On the next start, raise one event "the recorder stopped unexpectedly at (time)". Do not log sample contents.
- **Test**: Unit test that a panicking goroutine wrapper writes a crash file with the stack and no secrets (run the scrubber test strings through it). Start-up test that a crash file produces one event and is then marked as reported.
- **Risk**: Low. Check the service account can write to the chosen folder.

### W-13 Saved connections: a way out when the file is unreadable (RV-DAT-1)

- **Model**: Sonnet. **Opus review required** (saved credentials, data loss).
- **Files**: `internal/history/history.go`, `internal/history/history_test.go`, `internal/server/keeper.go`, the keeper call list if the history is held there, `web/src/lib/copy/sourcesCopy.ts`, the connections drawer component.
- **Change**: Keep failing closed. Add `SetAside()`: under the file lock, rename the unreadable file to `name.unreadable-YYYYMMDD-HHMMSS` in the same folder (never delete it) and start with an empty list. Keep at most 3 set-aside copies; do not remove older ones automatically, just refuse a fourth with a message. Add a route that calls it, allowed only when the last read returned `ErrCorrupt`. In the web UI, when the read error is "corrupt", replace "Try again" with: "The saved connections file is damaged and cannot be read. You can set it aside and start a new list. The old file is kept." and one button.
- **Test**: Corrupt fixture: read returns `ErrCorrupt`; `SetAside` renames the file byte for byte and the next read returns an empty list; `SetAside` on a healthy file is refused. Route test for the guard.
- **Risk**: Medium. This removes the person's saved logins from view. The old file must be kept untouched, and the action must not be reachable when the file is healthy.
- **UI**: yes. Prototype first.

### W-14 Join cost with one long event (RV-DAT-2)

- **Model**: Sonnet. No Opus review.
- **Files**: `internal/join/join.go`, `internal/join/join_test.go`, a new benchmark file.
- **Change**: First write a benchmark: 200,000 short events plus one event that spans the whole range, against 20,000 intervals. Record the time. If it is not clearly worse than the same data without the long event, close the item with the numbers. Otherwise replace the backward scan bounded by `prefixMaxEnd` (`join.go:62-103`) with a structure whose lookup does not depend on one long event: sort by start, build an implicit interval tree (max-end per subtree over the sorted array), and query by overlap.
- **Test**: The existing join tests must pass unchanged. Add a property test comparing the new result with a brute-force overlap on random data. The benchmark shows the gain.
- **Risk**: Low. Result order must stay the same; assert it in the property test.

### W-15 Export says "Download started" and recovers from a failed save (RV-UI-3)

- **Model**: Sonnet. No Opus review.
- **Files**: `web/src/components/ExportPanel.svelte`, its copy file (`EXPORT_TEXT`), its test.
- **Change**: In `save()` (`ExportPanel.svelte:59-86`) the catch puts the phase back to `preview` although the server has used up that preview. On a failed confirm set `phase = { kind: 'idle' }` and show: "The file could not be saved. Make the preview again." (keep the old behaviour only when the failure is `unreachable` before the request left, which cannot be known, so always go to idle). Change the success wording from "saved" to "Download started. Check your browser's downloads." and keep the file name and hash on screen.
- **Test**: Component test: confirm rejects, Save is no longer offered and Preview is. Confirm resolves, the new wording shows.
- **Risk**: Low.
- **UI**: yes. Prototype first.

### W-16 Removing a saved connection asks first and reports failure (RV-UI-4)

- **Model**: Sonnet. No Opus review.
- **Files**: `web/src/components/wizard/SqlServerForm.svelte`, `web/src/components/ConnectionsDrawer.svelte`, `web/src/lib/appState.svelte.ts`.
- **Change**: `forget(id)` in `SqlServerForm.svelte:101-104` swallows errors. Route both places through `forgetRecent` in `appState.svelte.ts:1127-1141`, which already has error text. Add an inline confirm in both ("Remove this saved connection? Its saved password is removed too." with Remove and Keep), not a browser `confirm()`. Show the error next to the row when removal fails.
- **Test**: Component tests: first click shows the confirm and removes nothing; Remove calls the API; an API failure shows the message and keeps the row.
- **Risk**: Low.
- **UI**: yes. Prototype first.

### W-17 "Could not check" is not "not found" (RV-UI-5, RV-UI-6)

- **Model**: Haiku. No Opus review.
- **Needs**: W-01.
- **Files**: `web/src/lib/caseCode.ts`, `web/src/lib/caseCode.test.ts`, `web/src/components/Glossary.svelte`.
- **Change**: In `resolveCode` (`caseCode.ts:51-67`) the last line returns `CODE_NOT_FOUND` for every other error. Return `CODE_NOT_FOUND` only when the error is an `ApiError` with status 404 or 400. For code `unreachable` or status 0 return the W-01 message. For status 423 return "Unlock Unravis first, then try the code again." For anything else return a new `CODE_CHECK_FAILED`: "The code could not be checked just now. Try again." In `Glossary.svelte:82` use `unreachableReply()` only when the error says the update feed was unreachable; for a local failure show the W-01 message.
- **Test**: Unit tests for each branch of `resolveCode`.
- **Risk**: Low.
- **UI**: text only; include the sentences in the W-02 prototype for approval.

### W-18 No "Request failed (500)" on screen; AI access panel shows the reason (RV-UI-14, RV-UI-7)

- **Model**: Haiku. No Opus review.
- **Files**: `web/src/lib/api.ts`, `web/src/components/McpAccessPanel.svelte`, tests.
- **Change**: At `api.ts:141` the fallback message is `'Request failed (' + response.status + ')'`. Replace with "Something went wrong in Unravis. Try again. If it keeps happening, open Help and copy the error log." Keep the status in the `reportError` entry. In `McpAccessPanel.svelte:118` and `:125`, when the caught error is an `ApiError` with a message, show `T.stopFailed + ' ' + error.message` (and the same for resume).
- **Test**: Unit test: a 500 with an empty body yields the new sentence and the error log entry carries 500.
- **Risk**: Low. Search tests for the old string.
- **UI**: text only; get the sentence approved with W-02.

### W-19 Connections drawer tabs work with a screen reader (RV-UI-9)

- **Model**: Haiku. No Opus review.
- **Files**: `web/src/components/ConnectionsDrawer.svelte`, its test.
- **Change**: Wrap the tab buttons (line 10) in an element with `role="tablist"` and an `aria-label`. Each button gets `role="tab"`, `aria-selected`, `aria-controls`, and `tabindex` 0 for the selected tab and -1 for the others. Each panel gets `role="tabpanel"` and `aria-labelledby`. Left and Right arrow keys move between tabs; Home and End go to the first and last.
- **Test**: Component test for roles, `aria-selected` and arrow keys. Run `tools/uiaudit` on the drawer.
- **Risk**: Low. No visual change, so no prototype is needed beyond noting that in the pull request.

### W-20 Silent catches: check each, fix the real ones (RV-UI-8, RV-UI-12, RV-UI-13, RV-UI-10, RV-UI-11)

- **Model**: Sonnet. No Opus review. Confirm first.
- **Files**: `web/src/components/sources/AddAnotherDay.svelte:27`, `web/src/components/sourcewiz/AddSourcesFlow.svelte:140`, `web/src/lib/appState.svelte.ts:877`, `:886`, `:1059`, `web/src/components/sources/KeeperCard.svelte:70`, `:94`, `web/src/lib/components/CaseHome.svelte:294`, `web/src/components/BlackboxPanel.svelte` (`load()`, and the Apply buttons at `:433`, `:488`), `web/src/components/MasterKeyUnlock.svelte`, `web/src/components/Drawer.svelte:57`.
- **Change**: For each catch listed, decide: does a failure here leave the person looking at something wrong or stuck? If yes, show a message near the control. If no, add a one-line comment saying why silence is right. Then: BlackboxPanel `load()` keeps the last good list and shows the error above it. MasterKeyUnlock with an empty box says "Type the key first." Apply in BlackboxPanel shows a short reason while disabled ("Loading this PC's settings" or "Another change is being applied"). `Drawer.svelte` takes a `busy` prop; while it is true, Escape and the backdrop do not close.
- **Test**: One component test per behaviour changed. List in the pull request which catches were left silent and why.
- **Risk**: Low.
- **UI**: yes for the new messages. Prototype first; one prototype can cover all of them.

### W-21 Setup wizard stops waiting when progress cannot be read (RV-UXS-1)

- **Model**: Sonnet. **Opus review required** (setup).
- **Files**: `web/setup/App.svelte`, `web/setup/logic.ts`, tests under `web/setup`.
- **Change**: In `watch()` (`App.svelte:237-258`) the catch ignores every error except status 0 and 401. Count failures in a row. Reset on success. After 20 in a row (about 5 seconds with back-off to 1 second), stop and set `fatal` to: "Setup could not read its progress. The install may still be running. Wait a minute, then open Unravis Setup again to see the result." Do not say the install failed, because it may not have.
- **Test**: Move the loop's decision into `logic.ts` as a pure function and unit test it: 19 failures keep going, 20 stop, a success in between resets.
- **Risk**: Medium. The message must not lead the person to start a second install on top of a running one.
- **UI**: yes. Prototype first.

### W-22 Releases dashboard: keyboard and dialogs (RV-REL-2, RV-REL-3)

- **Model**: Sonnet. No Opus review.
- **Files**: `cmd/unravis-releases/ui/app.js`.
- **Change**: Use one helper for "activate on Enter or Space" (the code at `:431` already does both) at `:116` and `:480`; call `preventDefault` on Space so the page does not scroll. For dialogs (`:493-499`, `:534-536`): remember `document.activeElement` when opening, keep Tab and Shift+Tab inside the dialog, close on Escape, and return focus to the remembered element on close. Give the dialog `role="dialog"`, `aria-modal="true"` and `aria-labelledby`.
- **Test**: DOM tests for Space on a row, focus trap and focus return.
- **Risk**: Low. No visual change.

### W-23 Releases dashboard: fetch time limit and focus kept on refresh (RV-REL-4)

- **Model**: Sonnet. No Opus review.
- **Needs**: W-05 (same files).
- **Files**: `cmd/unravis-releases/ui/data.js`, `cmd/unravis-releases/ui/app.js`.
- **Change**: In `api()` (`data.js:13`) add an `AbortController` with a 20 second limit (60 for `/api/snapshot`) and throw "The server did not answer in time." Before `main.innerHTML = banner + html` (`app.js:555`) record the focused element's id or `data-key` and the scroll position; restore both after the render. Skip the render when the HTML string is unchanged.
- **Test**: Unit test for the time limit with a never-resolving fetch and fake timers. DOM test that focus stays on the same row after a render.
- **Risk**: Low.

### W-24 Local CI removes old run folders (RV-PERF-1, SWP-1125-7)

- **Model**: Sonnet. **Opus review required** (deletion on the build PC).
- **Files**: `internal/localci/agent.go` (`prune`, near `:555`), `internal/localci/cachetrim.go` (pattern to follow), tests.
- **Change**: Extend pruning to run and work folders. Remove folders of finished runs older than 7 days, then the oldest finished runs until the total is under a size limit (default 10 GB, configurable). Never touch a folder whose run is claimed or running: take the list of active runs under the same lock the agent uses to claim. Resolve each path and confirm it is inside the agent's own root and is not a link before deleting. Log what was removed and how much space was freed.
- **Test**: Temp-folder test with finished old, finished new and running runs: only finished old ones go; a link inside the root is not followed; a path outside the root is refused.
- **Risk**: Medium. A wrong path check deletes someone's work. The inside-root and not-a-link checks are the point of the review.

### W-25 Three CI jobs skip when nothing they cover changed (RV-CI-1, BL-461)

- **Model**: Haiku. No Opus review.
- **Files**: `.github/workflows/ci.yml`.
- **Change**: Read BL-461 in `docs/BACKLOG.md` first and follow it if it says more. Give the jobs at `:461` (desktop-sidecar), `:554` (linux-gates) and `:705` (powershell-syntax) `needs: changes` and an `if:` on the matching output of the `changes` job, copying the form the other gated jobs use. If the required-checks list on the branch names these jobs, a skipped job must still count as passed: use the same pattern the already gated jobs use for that.
- **Test**: Open a docs-only pull request and confirm the three jobs are skipped and the merge is not blocked. Open one touching a `.ps1` file and confirm `powershell-syntax` runs.
- **Risk**: Medium. A wrong path filter silently stops a gate from running. List the filters in the pull request.

### W-26 Lint tool is not rebuilt on every CI run (RV-CI-2)

- **Model**: Haiku. No Opus review.
- **Files**: `.github/workflows/ci.yml`.
- **Change**: First measure: look at the step time of "Go - install golangci-lint" (`:582-586`) in five recent runs. If it is under 20 seconds, close the item with the numbers. Otherwise add an `actions/cache` step (pinned by commit like the others) for `~/go/bin/golangci-lint` keyed by the tool version and the Go version, and run `go install` only on a cache miss. Keep building from source with the repo's Go version; the comment at `:584` explains why a prebuilt binary does not work.
- **Test**: Two runs: the second shows a cache hit and no install.
- **Risk**: Low.

---

## Wave 4: Low, confirmed

Each of these is small. Haiku unless stated. No Opus review unless stated.

### W-27 Await the automatic-update loop at shutdown (RV-SRV-3)
- **Files**: `cmd/audit-explorer/main.go:364-368`, `internal/server/updater_install.go:51-58`.
- **Change**: Start `RunAuto` with a done channel (or a `sync.WaitGroup`). In `Close`, after cancelling, wait up to 3 seconds for it.
- **Test**: Close returns only after the loop has exited; `-race` clean.
- **Risk**: Low.

### W-28 Cancel is seen while an upload spools (RV-SRV-4)
- **Model**: Sonnet.
- **Files**: `internal/server/import_jobs.go:412`, `:609-619`.
- **Change**: Wrap the reader passed to `io.Copy` in a reader that returns the job context's error once it is cancelled. Remove the partial spool file on cancel.
- **Test**: Slow reader, cancel mid-copy: copy stops within one read and the spool file is gone.
- **Risk**: Low.

### W-29 A panic in locate is logged, not called a timeout (RV-SRV-5)
- **Files**: `internal/server/discover_locate.go:499-517`.
- **Change**: In the deferred recover, log the panic value and stack through `applog` (scrubbed) and return an internal-error result instead of the timeout result.
- **Test**: A locate function that panics yields the internal-error result and one log entry.
- **Risk**: Low.

### W-30 Update settings save under one lock (RV-SRV-6)
- **Files**: `internal/server/update_check.go:185-196`.
- **Change**: Hold one mutex across the read, merge and write of the settings PUT.
- **Test**: Two concurrent PUTs changing different fields: both changes are present afterwards; `-race` clean.
- **Risk**: Low.

### W-31 Reopen limiter counts only successes (RV-SRV-8)
- **Files**: `cmd/audit-explorer/reopen.go:42-65`.
- **Change**: Set `r.last = t` after `newCode` and `rearm` succeed, not before.
- **Test**: A failing `rearm` followed at once by a second call: the second call is attempted, not refused by the gap.
- **Risk**: Low.

### W-32 Clear message when a reopened case cannot be opened (RV-SRV-10)
- **Model**: Sonnet.
- **Files**: `internal/server/case_lifecycle.go:261-270`, the web copy for that error.
- **Change**: When the reopen is saved but `h.cases.Open` fails, answer with a distinct reason ("reopened-not-opened") and the text "The case was reopened but could not be opened yet. Open it from the list."
- **Test**: Handler test with a store whose Open fails.
- **Risk**: Low. UI text: include in a prototype batch.

### W-33 Case file save recovers from a crash (RV-DAT-5)
- **Model**: Sonnet. **Opus review required** (overwrites a file).
- **Files**: `internal/casestore/casefile.go:180-203`, its test.
- **Change**: When the target exists, has size 0 and was made by a previous attempt (same folder holds a matching `unravis-case-*.tmp`, or the placeholder is older than 1 minute and empty), treat it as a left-over: remove it and continue. Never replace a file with content. On start of a save, remove `unravis-case-*.tmp` files older than 1 day in the target folder.
- **Test**: Leave a 0-byte target and a tmp file, save again: succeeds. A non-empty target still fails with `ErrCaseFileExists`.
- **Risk**: Medium. Must never overwrite a real file.

### W-34 First view of an old large case says it is preparing (RV-DAT-3) and event counts are kept (RV-DAT-4)
- **Model**: Sonnet.
- **Files**: `internal/casestore/sqlite/case.go:1678-1736`, `internal/casestore/sqlite/scanplan.go:29-57`, the case workspace web state.
- **Change**: Measure first on a case of 5 million events or more: time of the first query during the index build and time of `eventStats`. If either is over 2 seconds: expose an "indexing" flag in the case status so the web page can show "Preparing this case. This happens once." and keep per-kind counts in a small table updated in the import transaction, read by `eventStats`.
- **Test**: Benchmarks before and after; a test that counts match `GROUP BY` after several imports and a delete.
- **Risk**: Low. UI part: prototype first.

### W-35 Manifest write retry sleeps outside the lock (RV-BB-6)
- **Model**: Sonnet.
- **Files**: `internal/blackbox/store.go:605-617`.
- **Change**: Release `s.mu` around the 20 ms sleep and re-check state after taking it again, or keep the lock and cut the retry to one. Choose the simpler one that keeps the tests green.
- **Test**: Existing store tests with `-race`; a test that a reader is not blocked for the whole retry.
- **Risk**: Low.

### W-36 Newest crash dumps are listed first (RV-BB-11)
- **Files**: `internal/blackbox/dumps.go:25-29` and the walk below it.
- **Change**: Within each folder read the entries, sort by modified time newest first, then apply the visit and list limits.
- **Test**: A folder with 600 files where the newest is last in name order: it is in the listing.
- **Risk**: Low.

### W-37 Diagnostic log cannot grow without limit (RV-BB-12)
- **Files**: `internal/blackbox/diag.go:156-166`.
- **Change**: Check the errors in `rotateLocked`. If rename fails, truncate in place. If that fails too, stop writing to the file until the next successful rotation and raise one event.
- **Test**: Fake file system where rename fails: the file is truncated. Where both fail: writes stop.
- **Risk**: Low.

### W-38 Update health file written safely (RV-SUP-7 and the message-cut nit)
- **Files**: `internal/update/health.go:74-88`, `internal/update/applyresult.go:29-31`.
- **Change**: Use `os.CreateTemp` in the same folder, write, `Sync`, close, rename. In `applyresult.go` cut the message at a rune boundary (`utf8.RuneStart` walk back).
- **Test**: Two concurrent writes leave a valid file; a 401-byte message ending in a 2-byte character is cut cleanly.
- **Risk**: Low.

### W-39 Releases dashboard opens the browser by full path (RV-SEC-4, SWP-1125-9)
- **Opus review required** (security; small).
- **Files**: `cmd/unravis-releases/main.go:145`.
- **Change**: Build the path from the Windows system directory (`windows.GetSystemDirectory`) joined with `rundll32.exe`, as the main app does for its browser open. Search the repo for other bare `exec.Command("rundll32"` or `"cmd"` names and fix them the same way.
- **Test**: Unit test that the command path is absolute and under the system directory.
- **Risk**: Low.

### W-40 Client log route: strict decode and scrub every field (RV-SEC-5)
- **Model**: Sonnet. **Opus review required** (secret scrubbing).
- **Files**: `internal/server/clientlog.go:37-39`, `:76-81`, `:103-106`, `:126-133`, its test.
- **Change**: Decode the batch form with `DisallowUnknownFields`, as the single form does. Pass `screen`, `action`, `code` and `stage` through `applog.Sanitize` after the length cap.
- **Test**: A batch with an unknown field is refused. A `screen` value holding a connection string with a password is written scrubbed.
- **Risk**: Low. The web client must not send extra fields: check `web/src/lib` where the batch is built.

### W-41 Shipped binaries are checked for the test-only switch; session file failure is surfaced (RV-SEC-7)
- **Model**: Sonnet. **Opus review required** (release check).
- **Files**: `tools/ci/` (a new check script and its call in the release-verify workflow), `cmd/audit-explorer/main.go:570-584`.
- **Change**: Add a CI check that fails when any release binary contains the string `UNRAVIS_E2E_ALLOW_IMAGE`. Wire it where release artefacts are verified. Do not change anything under `tools/release`. In `main.go`, when `writeSessionFile` fails, keep running but record the reason so `unravis --mcp` and the AI access panel can say "The session file could not be written: (reason)".
- **Test**: The check passes on a normal build and fails on a build with `-tags e2e`.
- **Risk**: Low.

### W-42 `TestSilentNoUAC` no longer depends on the PC (RV-CI-3)
- **Files**: `internal/update/silentok.go:29`, its test, `docs/desktop/baseline-go-tests.windows.txt:47`.
- **Change**: Make the service lookup a package variable (`var anyServiceInstalled = ...`) the test replaces with a fake. Remove the baseline line once the test passes on a PC with the services installed.
- **Test**: The test passes with the services installed and without.
- **Risk**: Low.

### W-43 Small clean-ups (nits)
- **Files**: `internal/server/update_check.go:31`, `internal/casestore/recovery.go:40`, `:77-86`.
- **Change**: Move `updateCheckStates` onto the handler. Prune the `locks` map entry when its case is closed or deleted.
- **Test**: Existing tests with `-race`.
- **Risk**: Low.

---

## Wave 5: Low, confirm first

For each: write the failing test described. If it fails, fix as described. If it cannot be made to fail, tick nothing, and add a note to the backlog line saying it was checked and how.

### W-44 Blackbox self-update does not skip a version for good (RV-BB-5)
- **Model**: Sonnet. **Opus review required** (update).
- **Files**: `internal/blackbox/selfupdate.go:48`, `:254`, `:277`, `:354`, `:360`.
- **Test to write**: Write `pending.json` for version X, simulate the helper dying before the swap, start the updater again: is X offered again or skipped?
- **Fix if real**: Call `tracker().Skip` only after a recorded failed swap, not when the pending file is merely stale. A stale pending file older than 30 minutes with no result is cleared and the version is retried once.

### W-45 Retry entry is not lost when the put-back fails (RV-SRV-9)
- **Model**: Sonnet.
- **Files**: `internal/server/case_lifecycle.go:516-526`.
- **Test to write**: Store whose put-back fails after a failed retry: is the entry still listed?
- **Fix if real**: Remove the entry only after the retry succeeds.

### W-46 Investigation paging marks a cut result (RV-SRV-11)
- **Files**: `internal/server/investigation.go` near line 332.
- **Test to write**: A source that returns an empty page with a cursor: is `Truncated` true?
- **Fix if real**: Set `Truncated = true` when the loop stops with a cursor still present.

### W-47 Locked cases and the sweep (RV-DAT-8)
- **Model**: Sonnet.
- **Files**: `internal/casestore/sqlite/sqlite.go:1250-1274`.
- **Test to write**: A locked own-key case with expired data: does the sweep remove it after unlock?
- **Fix if real**: Run the sweep for that case on unlock. If it already does, add a comment.

### W-48 Stale backup part file does not block setup (RV-SUP-2)
- **Model**: Sonnet. **Opus review required** (setup).
- **Files**: `internal/setup/txn.go:92-103`, `:137-155`, `:336`.
- **Test to write**: Leave `x.part` in the backup folder and run `keepCopy` for `x`: does it fail?
- **Fix if real**: Remove a stale `.part` (after a not-a-link check) before the exclusive create.

### W-49 CLI legacy flag scan ignores flag values (RV-SUP-4)
- **Files**: `internal/clicore/cmdtree/run.go:85-150`.
- **Test to write**: `unravis case open --name --demo`: is the legacy demo path taken?
- **Fix if real**: Only treat an argument as a legacy flag when it is not the value of the flag before it.

### W-50 CLI retries only what can change (RV-SUP-5)
- **Files**: `internal/clicore/attach.go`; read BACKLOG line 356 first.
- **Test to write**: The owner answers a refusal code: does the CLI retry?
- **Fix if real**: Clear `Retryable` on codes the owner sent.

### W-51 Refused pipe peers back off (RV-SEC-6)
- **Model**: Sonnet. **Opus review required** (security).
- **Files**: `internal/pipesrv`.
- **Test to write**: 1,000 refused connects in a loop: does a legitimate client still get served within 1 second?
- **Fix if real**: After N refusals in a second, sleep briefly before the next accept.

### W-52 Lite launch code life (RV-LITE-1)
- **Files**: `cmd/unravis-lite/main.go:40`.
- **Test to write**: None needed. Measure a cold start on a slow PC from process start to the browser's first request.
- **Fix if real**: Raise `codeLife` to 5 minutes, or issue a new code when the old one has expired and the request comes from the first browser open.

---

## For nachi only

These need a decision or hands on the PC. No agent item exists for them.

- **O-1 Blackbox keep floor (RV-BB-2).** Today the free-space rule may delete every recording. Should Blackbox always keep a minimum, for example the newest 24 hours or 200 MB, even when the disk stays under the limit? This reverses part of design A37. Recommended: yes, keep the newest 24 hours. Once decided it is a small Sonnet item in `internal/blackbox/housekeeping.go:117-133` with Opus review (it changes what gets deleted).
- **O-2 Keeper temp login file (RV-SEC-2).** On the installed PC, while a Blackbox SQL setup is running, look at the access list of `%TEMP%\unravis-sqllogin-*` under the keeper's account and confirm other users cannot read it. If they can, this becomes High. Separately decide whether to replace the file with a hand-over through the pipe (recommended; Sonnet item with Opus review).
- **O-3 Blackbox host budget on real PCs (RV-BB-1).** After W-03 and W-04 are installed, check the Blackbox event list on the client PCs for "host read slow" events to learn how common it is.
- **O-4 Signed commits (RV-SEC-3).** Decide between a GitHub setting ("require signed commits" on `develop` and `master`, which needs commit signing set up on the PC and may need a passphrase) and the code check in W-10. Recommended: the pusher check in W-10, which needs no signing.
- **O-5 Release script exit codes (RV-SUP-6).** `tools/release/Release.ps1` signs and publishes, so no agent should run it. A Sonnet agent can add `$LASTEXITCODE` checks after each `gh` call and a time limit on `gh run watch`, with Opus review, but the first run after that change needs nachi watching.
- **Required checks.** If W-25 changes which CI jobs can be skipped, the required-checks list on `master` stays nachi's to change.

## Not planned

- Accepted risks listed in FINDINGS.md under "Risks accepted by earlier decisions".
- Desktop shell items already tracked as BL-306, BL-307, BL-309, BL-314.
- Areas listed under "Not reviewed" in FINDINGS.md. A second review pass over those is worth its own thread, starting with the Blackbox SQL service, the importer and `internal/setup/plan.go`.
