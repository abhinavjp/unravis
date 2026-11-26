# Whole-project review: findings

Date: 2026-11-26. Code reviewed: `develop` at `0d4fdbec` (merge of PR #330). All file and line references are to that commit.

This was a read-only review. No product code was changed. The fix plan is in [PLAN.md](PLAN.md).

## Summary

| Rating | Count |
|---|---|
| Critical | 0 |
| High | 3 |
| Medium | 26 |
| Low | 33 |
| Total | 62 |

Of the 62, 46 were confirmed in the code by the reviewer and 16 are unconfirmed in whole or in part. 9 are already in `docs/BACKLOG.md`, closely related to an item there, or a known design decision. The rest are new and were added to the backlog under "Whole-project review 2026-11-26".

The five that matter most:

1. **RV-BB-1** (High). Blackbox records nothing at all on a PC where the host read is always slower than 2 seconds, and says so only once.
2. **RV-UI-1** (High). When the app stops, the web page shows the browser's raw "Failed to fetch" text and never tells the person the app is gone.
3. **RV-REL-1** (High). The Releases dashboard keeps showing an old release snapshot as if it were current when a refresh fails.
4. **RV-SEC-1** (Medium, security). Any signed-in user on a shared PC can plant a saved SQL connection whose label names one server while it really points at another.
5. **RV-SUP-1** (Medium). If the installer dies without writing a result, the update bar stays on "being applied" and the automatic update never retries until the app restarts.

## How findings are rated

- **Critical**: data loss, another user's credentials exposed, or code execution. None found.
- **High**: a main feature silently fails or misleads in a normal situation, or a cross-user security impact with realistic preconditions.
- **Medium**: wrong or misleading in a less common situation, or recoverable by retry or restart.
- **Low**: cosmetic, rare, or hardening.

"Confirmed" means the reviewer read the cited code and it does what the finding says, or a test showed it. "Unconfirmed" means the code looks that way but the effect was not shown end to end. "Backlog" names the existing item when there is one, otherwise "new".

## Findings

### Blackbox (recorder, store, service)

| Id | Rating | What is wrong | Expected behaviour | Evidence | Backlog | Confirmed |
|---|---|---|---|---|---|---|
| RV-BB-1 | High | The host read (CPU, memory, disk, network, process list) has a fixed 2 second budget. When it is busy or over budget, `Collect` returns an error and the whole sample is thrown away, including the counter groups. Only the first failure raises an event. A PC where the read always takes over 2 seconds records nothing and shows one old event. | A slow host read still yields a sample (partial, or after a longer adaptive budget). The failure is repeated in events and shown in status while it lasts. | `internal/blackbox/groups.go:470` (`hostBudget = 2 * time.Second`), `:489-501`; `internal/blackbox/recorder.go:354-360` | new | Code confirmed. How often real PCs exceed 2 s is not measured. |
| RV-BB-2 | Medium | The free-space rule deletes the oldest recordings until free space is back above the limit or nothing is left. There is no minimum amount kept. On a disk filled by something else, Blackbox deletes its whole history. | A minimum-keep floor, plus a clear event when the floor stops deletion. | `internal/blackbox/housekeeping.go:117-133`; `internal/blackbox/config.go:35`, `:340`; `internal/blackbox/recorder.go:511` | By design (A37). Needs an owner ruling, see PLAN.md. | Confirmed |
| RV-BB-7 | Medium | Collect and write failures raise an event only on the first failure. A day-long outage shows one event from its start, and nothing says it is still failing or that it recovered. | A periodic repeat or summary event while failing, and a "recovered" event. | `internal/blackbox/recorder.go:354-360`, `:383-398` | new | Confirmed |
| RV-BB-9 | Medium | "Paused for low disk" is worked out by scanning the event list for the newest pause or resume event. The list keeps only the newest 200 events. After 200 later events the pause event is gone and status stops saying paused while recording is still paused. | The paused state is stored as its own field in the manifest. | `internal/blackbox/manifest.go:28` (`maxEvents = 200`); `internal/blackbox/store.go:338-352`; `internal/blackbox/state.go:216-227` | new | Confirmed |
| RV-BB-10 | Medium | The Blackbox service has no crash capture. A panic outside the one guarded worker leaves no record of why the service stopped. | The service writes a crash record the same way the app does. | No `crashlog` or `applog` import in `cmd/unravis-blackbox` or `internal/blackbox`; only `recover()` at `internal/blackbox/groups.go:422` | Related to BL-315 (app only) | Confirmed |
| RV-BB-6 | Low | The manifest write is retried up to 3 times with a 20 ms sleep while the store lock is held. Readers stall up to 40 ms. | Sleep outside the lock, or accept and document. | `internal/blackbox/store.go:605-617` | new | Confirmed |
| RV-BB-11 | Low | The crash-dump listing shares one visit budget (500 entries, depth 2, 200 listed) in directory order. In a large dump folder the newest dumps can be the ones left out. Status does say "truncated". | Newest first within the budget. | `internal/blackbox/dumps.go:25-29` | new | Confirmed |
| RV-BB-12 | Low | Diagnostic log rotation ignores Remove, Rename and Truncate errors. If all fail, the size cap stops working and the file grows. | Fall back to truncate in place and stop writing when that also fails. | `internal/blackbox/diag.go:156-166` | new | Partly confirmed (errors are ignored; unbounded growth not reproduced) |
| RV-BB-5 | Low | Self-update writes `pending.json` and marks the target version as skipped. If the helper is killed before the swap, that version may be skipped for good. | A pending update that did not finish is retried. | `internal/blackbox/selfupdate.go:48`, `:254`, `:277`, `:354`, `:360` | new | Unconfirmed |

### Server (local dashboard backend)

| Id | Rating | What is wrong | Expected behaviour | Evidence | Backlog | Confirmed |
|---|---|---|---|---|---|---|
| RV-SRV-1 | Medium | A reconnect failure during the start-up pass is cached for 2 minutes. When the person then presses Start tracing, the cached failure is replayed without dialling. The person fixes the cause, presses the button, and is told the old reason. | An attempt the person started always dials, or uses the short 20 second cooldown. | `internal/server/reconnect.go:30-31`, `:369`, `:375`, `:449-452`, `:476` | new | Confirmed |
| RV-SRV-2 | Medium | Archive and verify jobs run on a context made from `context.Background()`. At shutdown `CancelImports` stops discovery, samplers and imports only. Archive and verify jobs are neither cancelled nor waited for, so the process can exit while one is writing. | Shutdown cancels archive and verify jobs and waits a bounded time for them. | `internal/server/archive_routes.go:273`; `internal/server/archive_verify.go:182`; `internal/server/import_jobs.go:324-330`; `internal/server/archive_jobs.go:193` (only caller `masterkey.go:954`), `:232` (no non-test caller); `cmd/audit-explorer/main.go:384` | new | Confirmed |
| RV-SRV-3 | Low | The automatic-update loop goroutine is not awaited at shutdown. Close cancels only a running download. | Shutdown waits for the loop to end. | `cmd/audit-explorer/main.go:364-368`; `internal/server/updater_install.go:51-58` | new | Confirmed |
| RV-SRV-4 | Low | Cancel is not seen while an import upload is being spooled to disk. A large upload keeps copying after Cancel. | The spool copy checks the cancel signal. | `internal/server/import_jobs.go:412`, `:609-619`, `:332-339` | new | Confirmed |
| RV-SRV-5 | Low | A panic in the locate worker is swallowed and reported as a timeout. Nothing is logged. | Log the panic (scrubbed) and report an internal error. | `internal/server/discover_locate.go:499-517` | new | Confirmed |
| RV-SRV-6 | Low | The update-settings PUT reads, merges and writes without one lock. Two saves at once can lose one. | One lock around read-merge-write. | `internal/server/update_check.go:185-196` | new | Confirmed |
| RV-SRV-8 | Low | The "open the page again" limiter records the time before the new launch code is made. A failed attempt still starts the minimum gap. | Record the time only after success. | `cmd/audit-explorer/reopen.go:42-65` | new | Confirmed |
| RV-SRV-10 | Low | Reopening a case is saved first and the case is opened second. If the open fails the person sees an error although the reopen already happened. | Say that the case was reopened and could not be opened yet, or undo. | `internal/server/case_lifecycle.go:261-270` | new | Confirmed |
| RV-SRV-9 | Low | A retry entry is removed before the retry runs and put back on failure. If the put-back fails, the entry is lost. | Remove only after success. | `internal/server/case_lifecycle.go:516-526` | new | Unconfirmed |
| RV-SRV-11 | Low | The investigation paging loop stops on an empty page that still has a cursor, and does not mark the result truncated. | Mark it truncated. | `internal/server/investigation.go` near line 332 | new | Unconfirmed |

Nit: `internal/server/update_check.go:31` keeps `updateCheckStates` as a package-level map, so two servers in one test process share it.

### Data (case store, history, join)

| Id | Rating | What is wrong | Expected behaviour | Evidence | Backlog | Confirmed |
|---|---|---|---|---|---|---|
| RV-DAT-1 | Medium | The saved-connections file fails closed: one invalid record, unknown field or duplicate id makes the whole file unreadable. Delete reads first, so nothing can be removed either. The person is told "Saved connections could not be read. The saved file was left unchanged." and "Try again", which can never work. Failing closed is intended. The missing recovery path is the finding. | An offer to set the bad file aside (kept as a copy) and start with an empty list, with plain guidance. | `internal/history/history.go:334-376`, `:316-332`, `:217-244`; tests `internal/history/history_test.go:198`, `:308`; `internal/server/keeper.go:174-177`; `web/src/lib/copy/sourcesCopy.ts:34` | new | Confirmed |
| RV-DAT-2 | Medium | The interval join scans backward using a running maximum of end times. One long event early in the data keeps the maximum high, so every later lookup scans far back. The cost grows to intervals times events. | A structure whose lookup cost does not depend on one long event (for example an interval tree or a bounded scan with an index). | `internal/join/join.go:62-103` | new | Code confirmed. Effect at real data size not measured. |
| RV-DAT-3 | Low | The first query on an old large case can wait for the attribute index build. This is deliberate per the comments, but nothing tells the person why the first view is slow. | A "preparing this case" state while the index builds. | `internal/casestore/sqlite/case.go:1678-1736`, `:1235`, `:1785`; `internal/casestore/sqlite/sqlite.go:777` | new | Confirmed |
| RV-DAT-4 | Low | Event statistics run `SELECT kind, count(*) ... GROUP BY kind` over the whole events table once per import generation, under a lock. | Keep the counts as they are imported. | `internal/casestore/sqlite/scanplan.go:29-57` | new | Confirmed |
| RV-DAT-5 | Low | Saving a case file creates a 0-byte placeholder with exclusive create, then a temp file. Clean-up runs only in a defer. A crash leaves both behind, and the retry then fails with "file exists". | A left-over 0-byte placeholder is recognised and replaced. Stale temp files are removed. | `internal/casestore/casefile.go:180-203` | new | Confirmed |
| RV-DAT-8 | Low | The store sweep skips cases that are locked with their own key, so their expired data may stay until unlocked. | Document, or sweep on unlock. | `internal/casestore/sqlite/sqlite.go:1250-1274` | new | Unconfirmed |

Nit: `internal/casestore/recovery.go:40`, `:77-86` keeps a `locks` map that is never pruned.

### Setup, update and CLI

| Id | Rating | What is wrong | Expected behaviour | Evidence | Backlog | Confirmed |
|---|---|---|---|---|---|---|
| RV-SUP-1 | Medium | After the installer is started the update state is "launched". The automatic path does nothing in that state. If setup dies without writing a result (killed, crashed, PC put to sleep), the state stays "launched" until the app restarts. The bar keeps saying the update is being applied and the automatic update never retries. This is the same class as the recent "page lost the app after an update" bug. | A time limit on "launched" with no result. After it, the state returns to "ready" with a plain message and the automatic path may try again. | `internal/server/updater_auto.go:230-232`; `internal/server/updater_install.go:186-200`; `internal/server/updater.go:255-260`, `:392-400` | new | Confirmed |
| RV-SUP-7 | Low | Update health is written to a fixed `path + ".tmp"` with no fsync. Two writers race on the same temp name, and a power cut can leave an empty file. | Unique temp name and fsync before rename. | `internal/update/health.go:74-88` | new | Confirmed |
| RV-SUP-2 | Low | The setup transaction writes backup copies to `name.part` with exclusive create. A stale `.part` from a killed run could block the next attempt if the backup folder is reused. | Remove a stale `.part` before writing. | `internal/setup/txn.go:92-103`, `:137-155`, `:336` | new | Unconfirmed |
| RV-SUP-4 | Low | The CLI's legacy flag scan looks at every argument before `--`, including flag values. A value such as `--name --demo` could be read as the legacy `--demo` flag. | Scan only arguments in flag position. | `internal/clicore/cmdtree/run.go:85-150` | new | Unconfirmed |
| RV-SUP-5 | Low | `Retryable` is kept on error codes the owner process sent, so the CLI may retry something that will not change. | Retry only on transport errors. | `internal/clicore/attach.go` | Partly known (BACKLOG line 356) | Unconfirmed |
| RV-SUP-6 | Low | `Release.ps1` does not check the exit code of some `gh` calls, and `gh run watch` has no time limit. A failed call could be read as success, or the script could wait forever. Read only; the script was not run. | Check `$LASTEXITCODE` after each `gh` call and bound the watch. | `tools/release/Release.ps1` near lines 100 and 247 | new | Unconfirmed |

Nit: `internal/update/applyresult.go:29-31` cuts the message at 400 bytes, which can split a multi-byte character.

### Web UI (main app)

| Id | Rating | What is wrong | Expected behaviour | Evidence | Backlog | Confirmed |
|---|---|---|---|---|---|---|
| RV-UI-1 | High | `request()` turns only its own timeout into a plain-words error. Any other fetch failure is rethrown as is, and the screens print `cause.message`. When the app is closed, updated or crashed, the person sees the browser's "Failed to fetch". Plain wording exists only on the start screen and in case loading. Nothing checks that the app is still there, so the page looks alive with a dead app behind it. | One shared message such as "The app could not be reached", used everywhere, and an app-stopped state that says to start Unravis again and reload. | `web/src/lib/api.ts:109-122`; `web/src/lib/appState.svelte.ts:1072`; plain wording only at `web/src/lib/copy/startupCopy.ts:36-40`, `web/src/lib/caseWorkspace.ts:370`, `web/src/lib/cases.ts:213-219` | new | Confirmed |
| RV-UI-2 | Medium | The update bar polls every 10 minutes and ignores a failed poll. After the server stops or restarts, the bar keeps its last state, for example "being applied". | Count missed polls. After a few, show "reload this page". | `web/src/lib/updateState.svelte.ts:25-33`, `:125-130` | new | Confirmed |
| RV-UI-3 | Medium | Export shows "saved" as soon as the download link is clicked in code, although the browser may still block or cancel the download. If the confirm call fails, Save is enabled again on a preview the server has already used up, so the second press fails too. | Say "Download started". After a failed confirm, go back and make a new preview. | `web/src/components/ExportPanel.svelte:59-86` | new | Confirmed |
| RV-UI-4 | Medium | In the SQL Server form, removing a saved connection has no confirm and swallows errors: the row can stay with nothing said. In the drawer, removal shows an error but also has no confirm. A saved login is gone in one click. | Confirm or undo, and a visible failure message. | `web/src/components/wizard/SqlServerForm.svelte:101-104`; `web/src/lib/appState.svelte.ts:1127-1141`; `web/src/components/ConnectionsDrawer.svelte:14` | new | Confirmed |
| RV-UI-5 | Medium | Opening a case by its code answers "not found" for every failure: network down, server error, app locked. The person thinks the code is wrong. | A separate "could not check, try again" message for failures that are not "not found". | `web/src/lib/caseCode.ts:51-67` | new | Confirmed |
| RV-UI-6 | Medium | The update line in the help panel shows every error as "could not reach the update server", including errors from the local app. | Tell local failure apart from feed failure. | `web/src/components/Glossary.svelte:82` | new | Confirmed |
| RV-UI-9 | Medium | The tabs in the connections drawer are plain buttons with no tab roles or selected state. A screen reader cannot tell which tab is open. | `role="tablist"`, `role="tab"`, `aria-selected`, arrow keys. | `web/src/components/ConnectionsDrawer.svelte:10` | new | Confirmed |
| RV-UI-14 | Medium | When the server gives no message, people see "Request failed (500)". | A plain sentence. The status goes to the error log only. | `web/src/lib/api.ts:141` | new | Confirmed |
| RV-UI-8 | Medium | Several places catch an error and show nothing. | Each either shows a message or has a comment saying why silence is right. | `web/src/components/sources/AddAnotherDay.svelte:27`; `web/src/components/sourcewiz/AddSourcesFlow.svelte:140`; `web/src/lib/appState.svelte.ts:877`, `:886`, `:1059`; `web/src/components/sources/KeeperCard.svelte:70`, `:94`; `web/src/lib/components/CaseHome.svelte:294` | new | Unconfirmed (sites exist; whether each one hides a real failure was not checked one by one) |
| RV-UI-12 | Medium | When a refresh of the Blackbox panel fails, the list of PCs is replaced by an error instead of keeping the last good list with a notice. | Keep the last list and show the error above it. | `web/src/components/BlackboxPanel.svelte` `load()` | new | Unconfirmed |
| RV-UI-7 | Low | The AI access panel shows a fixed "could not stop" or "could not resume" text and drops the server's reason. | Show the server's reason when there is one. | `web/src/components/McpAccessPanel.svelte:118`, `:125` | new | Confirmed |
| RV-UI-10 | Low | Escape closes a drawer even while it is busy. | Callers that are saving block close, or the drawer takes a busy flag. | `web/src/components/Drawer.svelte:57` | new | Confirmed for the drawer. Callers not checked. |
| RV-UI-11 | Low | In the Blackbox panel, Apply is disabled with no visible reason. | A short reason next to the disabled button. | `web/src/components/BlackboxPanel.svelte:433`, `:488` | new | Confirmed |
| RV-UI-13 | Low | Submitting the master key box empty shows the "wrong key" message. | "Type the key first." | `web/src/components/MasterKeyUnlock.svelte` | new | Unconfirmed |

### Setup wizard, Releases dashboard and Lite

| Id | Rating | What is wrong | Expected behaviour | Evidence | Backlog | Confirmed |
|---|---|---|---|---|---|---|
| RV-REL-1 | High | When a refresh of the release snapshot fails, the Releases dashboard keeps the old snapshot and shows the error only if there is no snapshot at all. The freshness chip follows the fast poll only. Stale release, feed and verify state is shown as current. This is the screen used to decide whether a release went out. | A visible banner: "Last read at (time). Could not refresh: (reason)." | `cmd/unravis-releases/ui/data.js:58-63`; `cmd/unravis-releases/ui/app.js:442` | new | Confirmed |
| RV-UXS-1 | Medium | The setup wizard's progress watcher loops forever. Only "no connection" and 401 stop it. Any other error is retried every 250 ms with nothing shown, so the wizard can sit on a frozen progress list. | After a number of failures in a row, stop and show a message with what to do. | `web/setup/App.svelte:237-258` | new | Confirmed |
| RV-REL-2 | Medium | Clickable rows in the Releases dashboard react to Enter only, not Space. One place handles both. | Enter and Space everywhere. | `cmd/unravis-releases/ui/app.js:116`, `:480` (compare `:431`) | new | Confirmed |
| RV-REL-3 | Medium | Dialogs are written with `innerHTML`. Focus moves to the close button, but there is no focus trap, and focus is not returned when the dialog closes. | Trap focus while open and return it on close. | `cmd/unravis-releases/ui/app.js:493-499`, `:534-536` | new | Confirmed |
| RV-REL-4 | Medium | API calls have no time limit, so a hung request blocks that poll for good. Each change re-renders the whole main region, which drops keyboard focus and scroll. | A time limit on fetch, and focus kept across a render. | `cmd/unravis-releases/ui/data.js:13`; `cmd/unravis-releases/ui/app.js:555` | Server side known as SWP-1125-6 | Confirmed |
| RV-LITE-1 | Low | The Lite launch code lives 2 minutes. On a slow first start the browser can open after it has expired. | Longer life, or a new code on demand. | `cmd/unravis-lite/main.go:40` | new | Unconfirmed |

Desktop shell issues are already known as BL-306, BL-307, BL-309 and BL-314 and are not listed again.

### Performance, build and CI

| Id | Rating | What is wrong | Expected behaviour | Evidence | Backlog | Confirmed |
|---|---|---|---|---|---|---|
| RV-PERF-1 | Medium | The local CI agent never removes old run and work folders, so the host disk fills over time. The Go build cache part is already fixed (BL-586i). | Prune by age and total size, never touching a running job. | `internal/localci/agent.go:555` | Known: SWP-1125-7 | Confirmed |
| RV-CI-1 | Medium | Three CI jobs do not depend on the "what changed" job, so they run on every change, including docs-only ones. | Add `needs: changes` and the matching `if`. | `.github/workflows/ci.yml:461`, `:554`, `:705` | Known: BL-461 | Confirmed |
| RV-CI-2 | Medium | The lint job builds `golangci-lint` from source with `go install` on every run. | Cache the built binary by version, or use a pinned release binary with a checksum. | `.github/workflows/ci.yml:586` | new | Confirmed that the install runs each time. Whether the Go cache already makes it fast was not measured. |
| RV-CI-3 | Low | `TestSilentNoUAC` reads the real Windows service list, so it fails on a PC where Unravis services are installed. | The service check is injected so the test does not depend on the PC. | `internal/update/silentok.go:29`; listed in `docs/desktop/baseline-go-tests.windows.txt:47` | Known baseline entry | Confirmed (failed on the review PC) |

Also about performance, listed above: RV-DAT-2, RV-DAT-3, RV-DAT-4, RV-BB-6.

### Security

| Id | Rating | What is wrong | Expected behaviour | Evidence | Backlog | Confirmed |
|---|---|---|---|---|---|---|
| RV-SEC-1 | Medium | The keeper's `sql.import` call is open to every caller the pipe accepts (any interactive user on the PC). `Import` checks only the id, that a host is present, and that the password flag matches. It does not run the checks `Save` runs. A record can carry a label and host that name one server while its `Server` field, which is what gets dialled, points somewhere else. Abuse path: on a shared install, another signed-in user plants such a record. The victim picks "Payroll DB" from the saved list, types the SQL password, and the login goes to the attacker's server. | `Import` runs the same validation as `Save`, builds `Server` from host and port instead of trusting it, and checks the auth and encrypt values. Consider limiting `sql.import` to the migration path. | `internal/sqlconn/sqlconn.go:432-465`, `:306-320`, `:166`; `internal/keeper/backend.go:75`, `:210-219`; `internal/keeper/proto.go:101` | new | Confirmed by a throwaway test: the import returned 1 with no error and `Resolve` returned the attacker's server. The password capture step itself was not exercised. |
| RV-SEC-3 | Low | Local CI treats a pull request as the owner's when every commit's author or committer login equals the owner. It does not check that the commits are verified. A collaborator who sets a forged identity could have a PR run as trusted. Fork PRs never run, so the attacker must already have push rights. | Also require GitHub's verified flag, or the pusher's login. | `internal/localci/trust.go:41-58`; `internal/localci/prrun.go:247`; `internal/localci/isolate.go:23-24` | new | Code confirmed. Whether GitHub maps a forged commit email to the owner's login in the API answer was not tested. |
| RV-SEC-2 | Low | For Blackbox SQL setup the keeper writes a saved SQL password in clear text to a temp file for up to 5 minutes. Safety rests on the temp folder's access list. | Hand the login over the pipe or seal the file. At least confirm the folder's access list on a real install. | `internal/keeper/backend.go:78`; `internal/blackbox/sqllogin_file.go:33-55`; `cmd/unravis-keeper/main.go:131`; `internal/server/blackbox_sqlsetup.go:110-115` | Related: SWP-1123-1 | Unconfirmed (needs a look at the folder on the owner's PC) |
| RV-SEC-4 | Low | The Releases dashboard opens the browser with a bare `rundll32` name, so Windows search order decides which program runs. | Full path under the system folder. | `cmd/unravis-releases/main.go:145` | Known: SWP-1125-9 | Confirmed |
| RV-SEC-5 | Low | The client-log route's comment says unknown fields are refused, but the batch form uses a plain decode. The `screen`, `action`, `code` and `stage` fields are cut by length only and are not passed through the log scrubber. Only a token holder can call it. | Strict decode for the batch form and the scrubber on every text field. | `internal/server/clientlog.go:37-39`, `:76-81`, `:103-106`, `:126-133` | new | Confirmed |
| RV-SEC-6 | Low | A pipe peer that is refused is disconnected with no back-off, so a local process can keep the accept loop busy. | A short back-off after refusals. | `internal/pipesrv` | new | Unconfirmed |
| RV-SEC-7 | Low | Two small gaps. A failed write of the session file is only logged, so `--mcp` later fails with no hint. And no release check was found that proves the shipped binary lacks the test-only `UNRAVIS_E2E_ALLOW_IMAGE` switch (it sits behind the `e2e` build tag). | Surface the session-file failure. Add a release check that the string is absent from the shipped binaries. | `cmd/audit-explorer/main.go:570-584`; `internal/pipesec/testallow_e2e.go:39` | new | Unconfirmed |

## Threat model

This extends `docs/auto-updater/THREAT-MODEL.md`, which covers updates in detail. It does not repeat it.

### What is worth protecting

1. Saved SQL logins and connection strings (keeper store, sealed with DPAPI).
2. Case data: imported audit records, evidence, exports, the ledger.
3. The master key and per-case keys.
4. The session token of the running app, and the launch code that leads to it.
5. The update signing keys, the update feed and the installed program files.
6. AI access grants (MCP) and what they allow an AI client to read.
7. Blackbox recordings and the Blackbox read key.
8. The owner's GitHub token on the CI and release PC.

### Who might attack

- **A web page in the person's browser.** It can send requests to 127.0.0.1 but cannot read the token.
- **Another program running as the same Windows user.** It can read the user's files. This is outside what the app defends against, by decision.
- **Another signed-in user on a shared PC.** Can reach machine-wide pipes and shared folders.
- **Someone on the network.** Can reach the Blackbox read port if it is opened beyond loopback, and can tamper with update traffic.
- **A connected AI client.** Holds a grant and may try to go beyond it.
- **A GitHub collaborator or a compromised dependency.** Can open pull requests that CI runs.
- **Someone who hands over a file.** A case file, an import file or an offline update feed.

### Where input comes in

| Entry point | Guard | Checked how |
|---|---|---|
| Local HTTP server on 127.0.0.1 | Exact Host and Origin checks, `X-Audit-Token`, one-time launch code | Read `internal/server/server.go`, `security.go`. Sound. |
| Session proof route | HMAC over token, listening address and nonce | Read `internal/sessionproof/sessionproof.go`. Sound. |
| App pipe (CLI, desktop shell) | Access list limited to the logon session, first-instance flag, remote clients refused, peer image-path policy | Read `internal/clisrv/handler.go`, `internal/mcp/sessionfile.go`. Pipe layer per reviewer report. |
| Keeper pipe | Access list, remote clients refused, per-user rate limits, audit leak scan | Reviewer report plus own read of `backend.go`. One gap: RV-SEC-1. |
| Code mode and the JS worker sandbox | No production sandbox spawner exists, so every run is refused with `sandbox_unavailable` | `cmd/audit-explorer/codemode.go`, `internal/codemode`. Fails closed. |
| AI access (MCP) | First-claim enrolment, one-shot 10 minute claim, HMAC authentication, tier ceiling, budget, ledger write required, sources enforced, global stop, expiry | `internal/mcpsrv/dispatch.go:230-370`, `backend.go:114-127`, `alias.go:103`, `:126`; store sealed per user (`internal/mcpaudit/db.go:69`) |
| Update feed and download | Signed manifests (Ed25519, role bound to channel, rollback state), https-only allowed hosts, size and SHA-256, file held open from hash to start | `internal/update/manifest.go`, `fetch.go`, `download.go`, `launch_windows.go:37`, `:83-98` |
| Installer and elevated worker | Setup re-checks itself and passes expected hashes to the elevated worker; worker pipe checks PID and image; no downgrade from the page; link and owner checks on every path written | `cmd/unravis-setup/expect.go:42-80`, `main.go:328`, `:490`; `internal/setup/elevate_windows.go`, `os_windows.go`, `fixes_b.go`, `plan.go` |
| Setup wizard page | 127.0.0.1 on a random port, exact Host and Origin, constant-time key | `cmd/unravis-setup/uiserver.go` |
| Blackbox read port | GET only, refuses cross-site, read key, throttle, file reads through a rooted handle | `internal/blackbox/server.go:238` |
| Releases dashboard | GET and HEAD only, loopback Host, link token turned into a strict cookie, strict content policy, names checked | `internal/releasedash/server.go`, `steplog.go:102`, `ci.go:861-886` |
| Local CI agent | Fork PRs never run, collaborator PRs run with the base branch's definition, throwaway profile, placeholder token | `internal/localci/prrun.go:247-252`, `isolate.go`. One gap: RV-SEC-3. |
| GitHub workflows | Actions pinned by commit, no `pull_request_target`, feed mirror filters names and size | `.github/workflows/ci.yml`, `mirror-feed.yml` |
| Web page itself | No `eval`, no `new Function`, no `{@html}` | Search of `web/src` |
| Secrets in logs and exports | Layered scrubbers | `internal/secretscrub/scrub.go`, `internal/applog/sanitize.go`. One gap: RV-SEC-5. |
| Master key | Argon2id (3 passes, 64 MiB, 4 threads), shared unlock throttle | `internal/masterkey/masterkey.go:103`; `internal/server/masterkey.go:266-276` |

### Abuse paths tried against the code

| Path | Result |
|---|---|
| A web page calls the local API | Blocked by Origin and token checks. |
| A web page rebinding DNS to 127.0.0.1 | Blocked by the exact Host check. |
| A local process on another port poses as the dashboard to get the token | Blocked by the session proof, which is bound to the listening address. |
| An AI client asks for a source outside its grant, or above its tier | Refused in dispatch before any read. |
| An AI client runs code | Refused: no sandbox in production. |
| A tampered or replayed update manifest | Refused by signature, role and rollback checks. Offline feed replay is accepted by decision (`docs/auto-updater/OFFLINE.md`). |
| A swapped installer file between check and start | Blocked: the file is held open from hashing to start. |
| A planted link in a folder setup writes to | Refused by the no-follow open and link checks. |
| Another user plants a misleading saved connection through the keeper | **Works. RV-SEC-1.** |
| Another user reads a saved password from the keeper's temp file | Not settled. RV-SEC-2. |
| A collaborator's PR runs as owner-trusted in local CI | Possible in code. RV-SEC-3, GitHub side not tested. |
| A cross-site page reads Blackbox data | Refused by the guard. |
| A cross-site page drives the Releases dashboard | Nothing to drive: read-only routes, strict cookie. |

### Risks accepted by earlier decisions

These were seen again and are not findings.

- A program running as the same user can read `session.json` and pass the peer image check.
- CLI routes on the app pipe carry no token (BL-318 option B).
- The hello route gives the token to a peer whose image is `unravis-desktop.exe` in the install folder.
- The MCP bridge holds the full session token. The User-Agent guard on code mode is documented as not a boundary.
- Rate limiters are process-wide, not per client.
- Elevated setup builds system paths from `%SystemRoot%`.
- The keeper pipe's access list includes all interactive users.
- Blackbox traffic beyond loopback is clear text (warned in `config.go:302-315`).
- The update control file fails open on a network error (SWP-1021-5).
- A local CI step can still recover the owner's `gh` token (`docs/ci/LOCAL-PIPELINE.md:88`).
- `LaunchRecover` starts the installed setup after a link check but without a hash (BL-183).
- `govulncheck` and `osv` report but do not gate (SWP-1122-8).

## Checked and not a finding

- Blackbox Close waits 2 seconds for workers: intended per the comment.
- Reduced motion: handled widely across the web UI.
- Two candidate Blackbox findings and two candidate server and setup findings from the first pass could not be located in the code and were dropped.
- Case store "fail the open" behaviours flagged in the first pass are intended and tested.

## Not confirmed

RV-BB-5, RV-SRV-9, RV-SRV-11, RV-DAT-8, RV-SUP-2, RV-SUP-4, RV-SUP-5, RV-SUP-6, RV-UI-8, RV-UI-12, RV-UI-13, RV-LITE-1, RV-SEC-2, RV-SEC-6, RV-SEC-7 are unconfirmed. RV-SEC-3 is confirmed in code but its GitHub side is not.

Not measured: how often real PCs exceed the 2 second host budget (RV-BB-1), the join cost at real data sizes (RV-DAT-2), and the lint install time (RV-CI-2).

## Not reviewed

The review did not reach everything. These were not read, or only skimmed:

- **Blackbox**: `sqlservice*.go`, `server.go` after line 330, `resolve*.go`, `events.go`, the Windows counter reader, `cmd/unravis-blackbox/main.go` after line 140, `sqlrunner.go`, and the packages `bbsql`, `bbsaved`, `sampler`, `perfdata`, `resource`, `dbhealth`, `dotnetdiag`, `calltrace`, `collect`, `power`, `hosts`, `leakcheck`.
- **Server**: `case_file.go`, `archive_restore.go`, `lanes.go`, `import_check.go`, `health.go`, `tracing.go`, `case_key.go`, `case_workspace.go`, most `discover*.go`, and `gate.go`, `trayapp.go`, `power.go` under `cmd/audit-explorer`.
- **Data**: importer sub-packages, `store`, `audit`, `discover`, `investigate`, `evidence`, `timeline`, `casekey` (skimmed).
- **Setup and update**: `internal/setup` `plan.go`, `state.go`, `preflight.go`, `os_windows.go` (skimmed), `silent.go`, `log.go`; `internal/update` `control.go`, `offline.go`, `recover.go`, `keys.go`; `internal/sealedfile/platform_windows.go`; `instance`, `autostart`, `tray`, `shelllink`, `locator`, `appdirs`.
- **CLI**: `cliattach`, `clioffline`, `clicap`, `ownerlease`.
- **CI and releases**: `internal/localci` claim, run, poller and status; `internal/releasedash` ci, snapshot, active and machines; `tools/ci/*`; workflows other than `ci.yml` and `mirror-feed.yml`.
- **Web screens**: the Blackbox setup wizard steps, the place wizard, ImportPopup, RecordDetail, CaseHome, StartScreen, Landing, the case key page and card; `web/setup/logic.ts`; Releases `detail.js`, `legacy-core.js` and CSS; the desktop shell's Rust bodies.

## What was run

- `go test ./internal/history ./internal/join ./internal/sqlconn ./internal/update ./internal/keeper ./internal/sessionproof -count=1`. All passed except `TestSilentNoUAC` in `internal/update`, which is a known baseline entry (RV-CI-3).
- One throwaway test in `internal/sqlconn` to reproduce RV-SEC-1. It was deleted afterwards.
- `tools/uiaudit` and the full test suite were not run. Nothing under `tools/release` was run.
- No live install was inspected. Findings about a running install are marked unconfirmed.
