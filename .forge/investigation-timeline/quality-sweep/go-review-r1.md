# Go quality sweep, round 1 (read-only review)

Scope: `internal/` and `cmd/` code added or changed on feat/investigation-timeline since 94ea98e (tracing API, casestore buckets/revision, applog, instance, tray, autostart, shelllink, setup, cmd/audit-explorer).
Method: read the code; no Go commands run, nothing edited. Every row cites a line read. "Existing tests" says what I could see or could not confirm; "unverified" means I did not open the test.
Test-quality check: only 5 scoped test files use `time.Sleep` (cmd/audit-explorer lifecycle_test.go, reopen_test.go; cmd/unravis-setup main_test.go; internal/casestore series_test.go; internal/tray tray_windows_test.go, one each) and none uses `time.Since` or a duration comparison. No hard timing assertions found. Failure-path gaps are listed per row.

Fix units are grouped by package; take one package section per fixer. Rows inside a package are ranked by value.

## Package: internal/applog

| ID | Category | Sev | file:line | Problem | Minimal safe fix | Fix risk | Existing tests |
|---|---|---|---|---|---|---|---|
| GO-R1-01 | security | medium | internal/applog/applog.go:188-209 | `sanitizeAttr` only cleans `KindString`, groups, and `KindAny` holding a `string` or `error`. Any other `KindAny` value (a `fmt.Stringer`, `url.URL`, `[]string`, `map`, struct, `*http.Request`) is passed through and the TextHandler formats it unsanitized. A credential URL or token in such a value reaches the hand-over log. The package doc promises "nothing secret is ever written". | In the `KindAny` default branch, render with `fmt.Sprint(v)`, run `Sanitize`, return as a string attr. Also handle `KindAny` holding `[]byte`. | low | Unverified; string and error cases likely tested, no test seen for other types. Add a table test with Stringer, `url.URL`, slice, map. |
| GO-R1-02 | perf | medium | internal/applog/sanitize.go:86-161 | Every `Sanitize` call (every log line, plus every stdlib `log` write through `sanitizeWriter`) rebuilds `profileDirs()` (UserHomeDir, 2 Getenv, a map, a selection sort, slash variants) and then runs 7 regexes plus one `strings.ReplaceAll` per registered secret, even for a short plain line. The request-line logger is on the hot path. | Cache the sorted profile dirs once (`sync.Once`; env does not change at run time, tests can reset via a package var). Add a cheap pre-check: skip the credential regexes when the text has none of `://`, `@`, `=`, `:`, `bearer`, and no 25-char run. | low (cache) / medium (pre-check, must not skip a real secret) | Sanitize has table tests (unverified count). Keep them as the safety net; add a benchmark. |
| GO-R1-03 | scale | medium | internal/applog/rotate.go:73-89 | `rotate()` ignores every `Remove`/`Rename` error. If `Rename(path, backup(1))` fails (antivirus or a viewer holds the file on Windows), the older backups were already shifted and the oldest was already deleted, then `open()` reopens the same oversized file. `w.size` stays above `maxSize`, so the next write rotates again, each time shifting and deleting more history. One locked file can erase the whole retained log in a few writes. | Rename the current file first; if that fails, skip the shift and the delete, keep appending, and retry after a backoff (for example only when size exceeds 2x `maxSize`). Return or report the error instead of discarding it. | low | No failure-path test seen for rotation. Add one with a held-open backup on Windows. |

## Package: internal/server

| ID | Category | Sev | file:line | Problem | Minimal safe fix | Fix risk | Existing tests |
|---|---|---|---|---|---|---|---|
| GO-R1-04 | maintain | medium | internal/server/tracing.go:471-686 | `tracingSQLIISLanes` is a 215-line function that resolves sources, reads events, buckets, builds both lanes, caps and warns. Hard to test per lane and the most likely place for the next regression. | Extract `readLaneEvents`, `bucketLane` and `buildLane` helpers; keep the signature and output byte-identical. | medium | Tracing handler tests exist (unverified list). Capture golden JSON for the current output before splitting. |
| GO-R1-05 | scale | medium | internal/server/tracing.go:572-580 | The lanes load up to `casestore.MaxEventQuery` (50,000) full events, attrs included, per POST, then bucket in Go. The store already exposes `BucketCounter` (sqlite GROUP BY over indexed `ev_ts`), so counts need no event bodies. A UI that re-posts on zoom or brush repeats a 50k-event read each time. | Use `CountBuckets` for the count series; read events only for the parts that need bodies (markers, latency line), with a smaller limit. | medium | Bucket tests exist in casestore; no handler-level test of the large-case path seen. |
| GO-R1-06 | maintain | low | internal/server/tracing.go:765-792 | `resp.Warnings` is set at ~767 and then assigned again at ~779, so the first set is lost. `time.Now()` is called twice (765, 792), so two timestamps can disagree in one response. | One `now := time.Now().UTC()` at handler start; build warnings with `append`. | low | Unverified. Add a test that provokes a lane warning plus a cap warning and checks both. |
| GO-R1-07 | scale | low | internal/server/tracing.go (handler `caseTracing`, 722-800) | One request timeout covers all lanes in sequence. A slow SQL lane can leave the audit lane with no time, and the whole answer fails rather than returning the finished lanes. | Give each lane its own `context.WithTimeout` slice, or run lanes concurrently and mark a timed-out lane as a warning. | medium | No timeout-path test seen. |
| GO-R1-08 | maintain | low | internal/server/export.go (`caseExportConfirm`) and case_views.go (`caseRevisionOf`) | The stale-preview guard relies on `caseRevisionOf`, which returns 0 for a store that keeps no revision (the memory store). The preview id then never changes and the before/after revision check is vacuous, so a changed memory case confirms without a 409. Confirm also rebuilds the whole export (a second full read and redaction pass). | Give the memory store a revision counter, or document and test the limitation. | low | Snapshot hook tests exist for the sqlite path (unverified); none seen for the memory store. |
| GO-R1-09 | kiss | low | internal/server/tracing_audit.go:282-293 | `auditSourceIDs` is used only by tests: dead code in the production file. | Move to a `_test.go` file or delete. | low | Compiles only because a test calls it. |
| GO-R1-10 | dry | low | internal/server/tracing_axis.go:19-25 | `allRungs` is a hand copy of `shortRungs` followed by `longRungs`. Editing one list without the other silently breaks the ladder. | `var allRungs = append(append([]int64{}, shortRungs...), longRungs...)`. | low | Axis tests exist (unverified); they would catch a mismatch. |

## Package: internal/casestore (sqlite)

| ID | Category | Sev | file:line | Problem | Minimal safe fix | Fix risk | Existing tests |
|---|---|---|---|---|---|---|---|
| GO-R1-11 | maintain | medium | internal/casestore/sqlite/case.go:770, tags.go:86,160,222, cookiepolicy.go:133 versus case.go:925, plan.go, retries.go, health.go, workspaceblob.go, ledgerblob.go | `bumpCaseRevision` is called only in the listed mutators. Other mutators write without it (quarantine at case.go:925, saved views/checklist, retries, health, blobs). Whether they change what the case export reads (events, imports) is unverified; quarantine is the one to check first. If it changes visible events, the 409 guard cannot see it. | Decide per mutator, then add the bump inside the same transaction, or add a test that enumerates every mutating method and asserts the revision moved (or documents why not). | low | A revision test exists for the bumped paths (unverified). The missing piece is a mutator-completeness test. |
| GO-R1-12 | perf | low | internal/casestore/buckets.go (`CountBucketsScan`) | The fallback scan walks every event of the case through `Case.Each` and buckets in Go. Fine for the memory store, but any store without `BucketCounter` pays O(n) per request. | Keep; note the cost in the doc comment and cap the scan with the query window up front. | low | Scan and sqlite results compared in tests (unverified). |

## Package: internal/instance

| ID | Category | Sev | file:line | Problem | Minimal safe fix | Fix risk | Existing tests |
|---|---|---|---|---|---|---|---|
| GO-R1-13 | maintain | medium | internal/instance/signal_windows.go:86-92 | `callSafely` recovers a panic from a callback and drops it with no log. A bug in an Open or Quit handler would make "open Unravis" silently do nothing. | Pass a log function (or use `applog`) and log the panic value and stack once; keep the recover. | low | Unverified; add a test that the panic is reported. |
| GO-R1-14 | dry | low | internal/instance/guard_windows.go:50; signal_windows.go:138,143,209 | The same `ownerCheck` then "error or not ok" refusal block is repeated four times, each with its own wording. | One `func requireOwner(h) error` used by all four. | low | Owner-check seam is injectable (names_windows.go:74); tests use it. |

## Package: cmd/audit-explorer

| ID | Category | Sev | file:line | Problem | Minimal safe fix | Fix risk | Existing tests |
|---|---|---|---|---|---|---|---|
| GO-R1-15 | solid | medium | cmd/audit-explorer/main.go:144-402 | `run()` is about 260 lines: flags, gate, data dir, secrets, server, listener, tray, shutdown. Mixed responsibilities; most of it cannot be unit tested. | Extract `setupLogging`, `startServer`, `startTray` returning a small struct with `Close`; keep order. | medium | Only helpers (gate, lifecycle, reopen) have tests; `run()` itself is not covered. |
| GO-R1-16 | maintain | low | cmd/audit-explorer/lifecycle.go:20-23; main.go:61,347,351 | Package-level `runDone` and `endingSession` are shared mutable state; the tray wiring needs them passed as arguments in places and global in others. | Put both in a small `lifecycle` struct created in `run()` and passed to the tray and the end-session handler. | low | lifecycle_test.go covers the handlers. |
| GO-R1-17 | dry | low | cmd/audit-explorer/reopen.go:22-28 and trayapp.go:65-71 | `system32File` and `windowsFile` repeat the `SystemRoot` lookup and the `C:\Windows` fallback. | `windowsFile(parts ...string)`; `system32File(name)` calls it. | low | reopen_test.go (unverified). |
| GO-R1-18 | perf | low | cmd/audit-explorer/trayapp.go:104,108,112 | Each tray menu action calls `startup()` again on the tray (UI) thread. If it touches the disk or registry, the menu stalls. | Compute once at tray creation, or cache for the session. | low | Unverified. |

## Package: internal/autostart, internal/shelllink, internal/setup

| ID | Category | Sev | file:line | Problem | Minimal safe fix | Fix risk | Existing tests |
|---|---|---|---|---|---|---|---|
| GO-R1-19 | dry | medium | internal/autostart/autostart.go:20-75 versus internal/setup/profilelinks.go:18-70 | The shortcut size cap (64 KiB, written as `64 << 10` and `65536`), the guarded read, and the "is this link ours" test are written twice (`readLink`/`isOurs` and `linkTarget`/`linkIsOurs`), and the `--background` argument is a second literal (`autostart.Arg` and `startupLinkArg`). A fix to one (size, regular-file check) will miss the other. | Export one reader and one ours-check from `internal/shelllink` (or `autostart`) and have setup call them; `startupLinkArg = autostart.Arg`. | medium | Both copies have tests (unverified); keep both sets running against the shared code. |
| GO-R1-20 | pattern | low | internal/setup/profilelinks.go:141-143 | The Startup link is written without the exists-and-ours check the Desktop link gets at 137-139, so a user shortcut of the same name in the Startup folder is overwritten. | Apply the same `linkIsOurs` guard. | low | Unverified; add a test with a foreign link present. |
| GO-R1-21 | kiss | low | internal/setup/profilelinks.go:70-73 | `refuseLinkedFolder` is a one-line alias of `refuseLink`, adding a second name for the same check (used at profilelinks.go:115 and plan.go:688). | Call `refuseLink` directly and remove the alias. | low | Covered through callers. |
| GO-R1-22 | scale | low | internal/autostart/autostart.go (rename fallback) | When the rename over an existing link fails, the fallback writes in place, so a crash mid-write leaves a truncated link in the Startup folder. | Keep the rename path, and on fallback write to a temp name first and retry the rename once. | low | No failure-path test seen. |

## Counts

Total 22 findings. By severity: high 0; medium 9 (01, 02, 03, 04, 05, 11, 13, 15, 19); low 13 (06, 07, 08, 09, 10, 12, 14, 16, 17, 18, 20, 21, 22).

By category: security 1; perf 3 (02, 12, 18); scale 4 (03, 05, 07, 22); maintain 6 (04, 06, 08, 11, 13, 16); dry 4 (10, 14, 17, 19); kiss 2 (09, 21); solid 1 (15); pattern 1 (20); test 0 (test gaps are folded into the "Existing tests" column of each row).

## Not verified / skipped

- Skim not done: setup uiserver.go and uninstall.go, calltrace lanes.go, evidence caseexport files, importer iis changes, console_windows.go, fatal_windows.go, tray menu and notifyicon files.
- Security checks that came out clean: tray `wndProc` guards, named-object owner SID checks (no TOCTOU seen), `Rearm` length bounds and secret handling, `includeSensitive` opt-in only on exact `1`.
