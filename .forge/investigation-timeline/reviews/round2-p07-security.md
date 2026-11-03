# Round 2 security review: Phase 07 background-run

This review only reads; no plan or source files were edited. I read `reviews/round1-p07-security.md` and the fixed packets WP-70c, 71d, 72a-d, 73a-e, 74a-h, 75a-c, 76a/b and 77, `phase.md` and plan.md §3. I also spot-checked today's code: `internal/setup/layout.go` (`CheckUserDirs`), `fixes_b.go` (`refuseLink`, `splitApps`), `uninstall.go` (`removeShortcuts`), `txn.go` (`WriteFile`), `proto.go` (`WorkerPrepare`), `internal/applog/sanitize.go` (`RegisterSecret`, `Sanitize`), `internal/server/launch.go` (`launchState.take`) and `cmd/audit-explorer/main.go` (`startupLines`).

The threat model is the same as round 1: a local multi-user Windows machine; other processes of the same user, and processes of other accounts in the same interactive session (runas); a user-writable profile; and an elevated setup worker, which may run as a different administrator account (over-the-shoulder elevation, see `WorkerPrepare`).

Result: 0 BLOCKER, 1 MAJOR, 4 MINOR, 5 NIT. One round-1 MAJOR is only partly closed (the leftover is graded MINOR, with the reason given), and one round-1 MINOR is not closed.

## 1. Round-1 findings: closure check

| # | Round-1 finding | Status | Evidence in packet text |
|---|---|---|---|
| 1 | MAJOR PID reuse on terminate (75b) | **Closed** | WP-75a:20 `TerminateIf` opens with `PROCESS_TERMINATE|SYNCHRONIZE|PROCESS_QUERY_LIMITED_INFORMATION`, runs `QueryFullProcessImageName` on that handle and compares with `samePath` before `TerminateProcess`. The handle pins the process, so there is no TOCTOU between the check and the kill. WP-75b:18 rebuilds the list each round, matches (PID, Exe) against the original list, and terminates only `left`. Proof (e) bans `Proc.Terminate(`. Tests `TestUpgradeDoesNotTerminateAReusedPID` and `TestTerminateIfChecksTheImagePath` exist. A leftover owner gap is NIT-1. |
| 2 | MAJOR window messages bypass the event DACL (71d) | **Partly closed** | Closed: the foreign `WM_CLOSE` is now a no-op; `stopMsg` is gated on `stopping`; `ChangeWindowMessageFilter` is banned (WP-71d:13, test and Select-String); the accepted-risk note exists. Still open: a cross-thread `SendMessage(WM_ENDSESSION,1)` passes `systemEnding()`, and a posted `WM_DESTROY` or `WM_QUIT` still tears the tray down. See MINOR-1. |
| 3 | MINOR `Signal` leaks its handle (72c) | **Closed** | WP-72c:18 `defer windows.CloseHandle(h)`; `TestSignalDoesNotKeepTheEventAlive`. |
| 4 | MINOR another account squats the names (72a/b/c) | **Not closed** | WP-72a puts the SID in the name. A SID is public, so this does not stop another account from creating that exact name. The optional owner check was not adopted. See MINOR-2. |
| 5 | MINOR ACL test is a denylist (72c) | **Closed** | WP-72c:28 is now an allowlist (user, SYSTEM, Administrators) over the write-class bits, including `WRITE_DAC`/`WRITE_OWNER`. The logon-SID ACE in the default DACL is read/execute only, so it passes, which is correct. |
| 6 | MINOR elevated first copy locks out the user (73b/72b) | **Closed** | WP-73b:22/24 refuses the first copy only when the token's elevation type is Full; Default (UAC off, built-in Administrator) is not refused, so nobody is locked out. An elevated second copy still signals. The ACCESS_DENIED wording names "or as administrator" (73a/73b). Ordering nit: NIT-2. |
| 7 | MINOR `--background` code, and RegisterSecret | **Closed** | WP-73b:23/25 `startupCode` returns "" without minting under `--background`, `WithLaunchCode` is added only when a code exists, and no link line is printed. WP-70c:20 `mintLaunchCode` registers every code and is the only minting path (73b and 73c proofs ban `newLaunchCode(`). Tests: `TestStartupCodeIsEmptyInBackground`, `TestReopenRegistersTheCodeAsASecret`. New side effect: MINOR-3. |
| 8 | MINOR MessageBox shows the live link (73c) | **Closed** | WP-73c:18 box text holds only `base` and the tray hint, and `TestReopenWarnsWithoutTheLinkWhenTheBrowserFails` asserts there is no code and no `/launch`. WP-73c:13 bans the code from log, error, box and tooltip. WP-76b:17 documents the rundll32/4688 exposure. |
| 9 | MINOR uninstall deletes by path (74g) | **Closed for the stated case** | WP-74g:16 uses `linkIsOurs` for all four links and keeps-and-reports a link that points elsewhere; WP-74f:25 skips a foreign Desktop link; WP-76b:16 documents the leftover per-user Startup links. The check-then-delete runs elevated on a user-controlled path, though. See MAJOR-1. |
| 10 | MINOR junctioned Desktop (74f) | **Partly closed** | WP-74f:26 adds `refuseLinkedFolder` for the Desktop and Startup folders on install. The check can still race the write, and uninstall (74g) has no such check at all. See MAJOR-1. |
| 11 | MINOR autostart checks (74b/74c) | **Closed** | WP-74b:18 `Enabled` needs target == exe AND args == `--background` within 64 KiB. The temp file is under `%LOCALAPPDATA%\Unravis`, never in Startup, and is removed on every failure path. `Disable` removes only a provably-ours link. WP-74c:19 dropped the profile-root assertion. Leftover nit: NIT-4. |
| 12 | MINOR WP-75c dependency edges | **Closed** | plan.md:135 `WP-75c | 72a, 72c, 75a, 74e`. The `os_windows.go` writers are chained 75a -> 74e -> 75c. |

## 2. Findings (new or residual)

[MAJOR-1] WP-74f / WP-74g: the elevated worker writes and deletes links in the user's profile through a path the profile owner controls. The new junction guard can be raced, and uninstall has no guard at all.

Evidence:
- `WorkerPrepare` (`proto.go:183-199`) has the worker act for the window's account and the folders of that account's profile, and the worker may be another administrator account.
- Install: WP-74f:26 checks each folder with `refuseLink` (Lstat plus the reparse flag) and only then writes. `Txn.WriteFile` (`txn.go:158-163`) then runs `keepOld(path)` and `os.WriteFile(path)` by path, which follows any reparse point created after the check.
- Uninstall: WP-74g:16 does `linkIsOurs(link)` (an `os.ReadFile` by path), then `os.Remove(link)` by path, and checks no folder at all. Today's `removeShortcuts` (`uninstall.go:148-155`) already does this for `StartupLink`; WP-74g extends it to `DesktopLink`.

Attack scenario (machine-scope install, a standard user, an administrator typing credentials at the UAC prompt to uninstall or repair):
1. The user replaces `C:\Users\u\Desktop` (or `...\Startup`) with a mount point to `\RPC Control`. In that object directory they create the object-manager symlink `Unravis.lnk` pointing to `C:\Users\u\bait.lnk`, a hand-made .lnk whose target is `C:\Program Files\Unravis\unravis.exe`. They also set an oplock on `bait.lnk`.
2. The worker runs `linkIsOurs`, opens `bait.lnk`, and the oplock fires.
3. The user re-points the symlink to `C:\Config.Msi`, or to any file, and releases the oplock. The read finishes, and the link counts as "ours".
4. `os.Remove("C:\Users\u\Desktop\Unravis.lnk")` now deletes the chosen file as administrator. Arbitrary delete is a well-known path to local privilege escalation (the MSI rollback technique).

On install, the same swap after `refuseLinkedFolder` turns `keepOld` (a move into the backup folder) plus `os.WriteFile` into an arbitrary file move or delete and an overwrite with .lnk bytes. Round 1 rated the write side low because the content is fixed. The move/delete half, and the uninstall side, were not covered.

Fix (packet text):
- Preferred: the elevated worker never touches a path inside the user's profile. Move writing and removing `DesktopLink` and `StartupLink` (and the 74h read of `StartupLink`) into the non-elevated window process, which runs as the profile owner, so no privilege boundary is crossed. Do it as a small step after the worker's transaction commits, or before it on uninstall. The worker keeps only `StartMenuLink` and `QuitLink`, which for machine scope sit under the common Start menu that only administrators can write. Add a text test, `TestWorkerNeverWritesIntoTheUserProfile`, asserting that the worker-side step list does not reference `DesktopLink` or `StartupLink`.
- If the work must stay in the worker:
  - Call `SetProcessMitigationPolicy(ProcessRedirectionTrustPolicy, EnforceRedirectionTrust)` at worker start (Windows 11 22H2+). The worker then ignores junctions created by non-administrators.
  - Act on handles, not paths. Open the link with `FILE_FLAG_OPEN_REPARSE_POINT` and `DELETE|FILE_READ_DATA`, check that `GetFinalPathNameByHandle` lies inside the profile and that no component is a reparse point, parse it from the same handle, then delete it with `SetFileInformationByHandle(FileDispositionInfo)` on that handle.
  - On install, create the file with `CREATE_NEW` on a verified parent and check the final path of the created handle before writing.
  - Add `TestUninstallChecksAndDeletesTheSameFile` (Windows: a seam swaps the Desktop folder for a junction between the read and the delete; the file in the other folder survives) and `TestUninstallRefusesAJunctionedDesktop`.
- Also: WP-74f:26 says "call `refuseLink(folder)` and return its error", which an implementer can read as checking only the first level. Write "check every level and return the first non-nil error".

[MINOR-1] WP-71d: round-1 MAJOR 2 is only partly closed. Three window-message paths still quit the app or tear down the tray.

Evidence:
- (a) `systemEnding()` (WP-71d:20) is `InSendMessage() != 0 || GetSystemMetrics(SM_SHUTTINGDOWN) != 0`. `InSendMessage` is non-zero for any message that another thread or process delivered with `SendMessage`, not just for the system's end-session broadcast. `SendMessage(hwnd, WM_ENDSESSION, 1, 0)` from another process therefore calls `OnEndSession`, which cancels the app (WP-73e).
- (b) WP-71d:21: `WM_DESTROY` -> `deleteIconHook`, `PostQuitMessage(0)` runs unconditionally. A posted `WM_DESTROY` deletes the icon and ends the loop.
- (c) `PostMessage(hwnd, WM_QUIT, ...)` makes `GetMessage` return 0, so the loop ends ("The GetMessage loop ends on quit", 71d:22). `Run` returns nil while the app keeps running without an icon. `startTray` only closes `trayDone`.
- `TestTrayPostedEndSessionIsIgnored` replaces `endingSession` with a fake, so the real predicate is never tested. `TestTrayIgnoresForeignClose` covers only `WM_CLOSE` and `stopMsg`.

Scenario: a process on the same desktop at the same or higher integrity level (including another account started with runas) finds `UnravisTrayWindow*`. It then either quits Unravis (a) or removes the icon (b, c). The user can still quit with `--quit` or from the Start menu.

Why MINOR and not MAJOR: the impact is denial of service only, and every sender that can do this can also drive the tray with `SendInput`, which UIPI checks by integrity level, not by account. The packet text, though, now claims that "only a private stop message and a genuine system end-of-session can quit it", which is false, and that should not ship.

Fix (WP-71d):
- `systemEnding()` returns `GetSystemMetrics(SM_SHUTTINGDOWN) != 0` only. Drop the `InSendMessage` OR. Keep manual row M2 / the 73e "signing out leaves no session.json" row as the check that the metric is set on logoff. If it is not, record that and accept a leftover `session.json` on logoff rather than reopening the hole.
- Add `destroying atomic.Bool`, set in the `stopMsg` handler right before `DestroyWindow`. `WM_DESTROY` acts only when it is set and otherwise returns 0.
- When `GetMessage` returns 0 and `!stopping.Load()`, log it once and keep looping (ignore a foreign `WM_QUIT`).
- Tests:
  - `TestTrayIgnoresForeignDestroyAndQuit`: `PostMessage(WM_DESTROY)` and `PostMessage(WM_QUIT)` leave `IsWindow` true, the delete hook uncalled and `Run` still running; a later cancel still stops it.
  - `TestRealSystemEndingRejectsASentEndSession`: leave `endingSession` as the real `systemEnding`, `SendMessage(WM_ENDSESSION,1)` from the test goroutine, and assert `OnEndSession` is not called. Skip when `SM_SHUTTINGDOWN` is set.

[MINOR-2] WP-72a / 72b / 72c / 76a: a SID in the object name does not stop squatting. Round-1 MINOR 4 is not closed, and four packets now state otherwise.

Evidence:
- WP-72a:4 says "another account in the same sign-in session cannot create the objects first", and WP-72b:13 says "another account cannot pre-create it". WP-72c:13 and WP-76a:17 repeat the claim.
- A user's SID is public (`LookupAccountName`, `whoami /user` run by an admin, the profile path's ACL). Any account in the session can create objects in `\Sessions\<n>\BaseNamedObjects`.
- `Acquire` treats `ERROR_ALREADY_EXISTS` as "not first" (72b:16), and `Signal` (72c:18) sets whatever event carries the name.

Scenario: another account in the session creates `Local\UnravisApp.<victimSID>` (mutex plus both events) with an Everyone-allow DACL and waits on the events. Every start by the victim, normal or `--background`, then signals Open into the squatter and exits 0 silently, so Unravis never runs. `--quit` and the upgrade `AskQuit` report success against the squatter. The impact is denial of service only: the signals carry no data, and no code or token leaks.

Fix:
- In `Acquire`, on `ERROR_ALREADY_EXISTS`, read the owner of the returned handle (`GetSecurityInfo(h, SE_KERNEL_OBJECT, OWNER_SECURITY_INFORMATION)`). Accept it only when it equals the token user SID or the token's default owner (`TokenOwner`; this is Administrators for a UAC-off admin). Otherwise return `ErrAccessDenied`, which the gate already words as "another Windows account or as administrator". Do the same in `Signal` with `OpenEvent(EVENT_MODIFY_STATE|READ_CONTROL)`.
- A stronger alternative is a private namespace (`CreateBoundaryDescriptor` plus `AddSIDToBoundaryDescriptor(userSID)`, `CreatePrivateNamespace`). Only a token holding that SID can create it.
- Tests: `TestAcquireRefusesAMutexOwnedBySomeoneElse` and `TestSignalRefusesAnEventOwnedBySomeoneElse`. Set a foreign owner such as `S-1-5-18`, which needs an elevated test, so mark them skip-unless-elevated and also add them as a manual row next to M11.
- Correct the four sentences: "the SID keeps accounts apart; the owner check stops squatting".

[MINOR-3] WP-73c / WP-70c: every Reopen registers a new secret, and `RegisterSecret` keeps all of them forever. Log cost grows without bound.

Evidence: `sanitize.go:41-53` appends to `secrets` after an O(n) duplicate scan, and `Sanitize` (`:56-65`) runs `strings.ReplaceAll` once per secret on every log line. WP-73c registers a new code on every Open. The app now runs for days or weeks.

Scenario: any sender of Open (a second launch, a script repeating `unravis.exe`, or the same-desktop tray callback from the accepted risk) can add a secret every 2 seconds, about 43,000 a day. Each log line then costs tens of thousands of `ReplaceAll` passes, which slowly exhausts CPU and memory. A code replaced by `Rearm` is already dead, so keeping it buys nothing.

Fix: add `applog.RegisterEphemeralSecret(value)`, which keeps the last N (for example 8) launch codes in a ring separate from the permanent secrets. Use it from `mintLaunchCode`. Add `TestEphemeralSecretsAreBounded` (register 100 and the list holds 8) and keep `TestMintLaunchCodeRegistersTheSecret` and `TestReopenRegistersTheCodeAsASecret` as they are.

[MINOR-4] WP-71d:36 / WP-77:24: the accepted-risk note is narrower than the real exposure, and the two packets disagree with each other.

Evidence: WP-71d says "same or higher integrity level"; WP-77 says "other processes of the same user ... at the same integrity level". Neither mentions other accounts in the same session, which reach the window too, because UIPI is checked by integrity level and windows have no DACL.

The stated rationale is otherwise accurate:
- The code goes only to the user's default browser.
- Another account cannot read rundll32's command line without `PROCESS_VM_READ` on the user's process, which a runas account has no logon-SID ACE for.
- The code is single-use with a 2-minute life, and Reopen is throttled to once every 2 s.

Two side effects are not recorded:
- (i) Every forced Open re-arms, so an attacker can keep invalidating the user's own in-flight link, which shows the user a "link gone" page.
- (ii) Every forced Open puts a fresh code into process-creation telemetry (4688, Sysmon) and into the secret list (MINOR-3).

Fix: one identical sentence in both packets, for example: "Any process on the same desktop at the same or higher integrity level, including another Windows account started in this session, can trigger Open through the tray callback. It only opens this user's browser with a fresh single-use 2-minute code (throttled to one per 2 s). It can make the user's previous link stop working and adds a rundll32 process-creation event each time. Accepted, because such a process can already send input to the user's desktop." With that wording the risk is accurate and acceptable.

[NIT-1] WP-75a: `TerminateIf` checks the image path but not the owner. In a machine-scope install, every user's `unravis.exe` has the same path. If a PID in `left` exits and is reused by another user's Unravis within microseconds, between the re-list and the open, the elevated worker kills that user's copy. Fix: on the same handle, `OpenProcessToken(TOKEN_QUERY)` and compare the token user with `r.UserSID` (pass it as a third argument), or compare the process creation time recorded at list time.

[NIT-2] WP-73b:22: elevation is checked after `acquire`. While the elevated copy briefly owns the mutex, a normal start spins for 5 s and reports `ErrAlreadyRunning`. Fix: call `d.elevated()` before `d.names`/`d.acquire` and return `ErrElevated` without taking the mutex. Update `TestGateRefusesWhenElevated` to assert that `acquire` was never called.

[NIT-3] WP-75c: `AskQuit` builds its names from the worker's token. With over-the-shoulder elevation that is the administrator's SID. Even with the right names, the standard user's default DACL grants nothing to Administrators. Either way it fails safe (`ErrNoInstance` / `ErrAccessDenied`, then the verified terminate). Record in WP-75c that a graceful quit only happens when the window and the worker are the same account.

[NIT-4] WP-74b:24 (`TestEnableReplacesAnOlderShortcut`): the tray toggle overwrites a foreign `Startup\Unravis.lnk` (a link to another program) without asking. The installer keeps such a link (74f, 74h). This is user-initiated and low impact. Either refuse with `ErrNotOurs` when the existing link's target is not named `unravis.exe`, or note the difference in WP-76b.

[NIT-5] WP-73c:21 and WP-73d:20 start `rundll32.exe` and `explorer.exe` by bare name, so they are found through PATH. Use `filepath.Join(os.Getenv("SystemRoot"), "System32", "rundll32.exe")` and `...\explorer.exe` (falling back to `C:\Windows`) as hardening.

## 3. Requested confirmations

- **TOCTOU between the image-path check and the terminate**: none. The check and `TerminateProcess` use one handle (WP-75a:20). The only leftover is the owner gap in NIT-1.
- **SID name derivation**: it uses the process token user (`OpenCurrentProcessToken`, WP-72a:21), so impersonation does not apply. Service and system SIDs (`S-1-5-18`, `S-1-5-19/20`) pass the regex. Names stay well under MAX_PATH (32 + 184 + prefix). Two sessions of the same user each get their own copy, because `Local\` is per session; that is by design and not a security issue. The squatting claim is wrong (MINOR-2).
- **Elevated refusal**: it cannot be bypassed in a way that locks the user out. `--no-browser` and `--mcp` skip the gate but take no mutex. A UAC-off or built-in Administrator token (type Default) is allowed, and all of that account's processes are elevated anyway. An elevated second copy only signals. The only rough edge is the ordering in NIT-2. A setup window started with "Run as administrator" will now get the refusal box from its post-install launch; WP-75b:13 records this.
- **Shortcut target and args validation in `autostart.Enabled`**: correct. It requires a regular file of at most 64 KiB, target == exe (case-insensitive, cleaned) and args == `--background`, and any parse error gives false. `Enable` writes only fixed fields plus an exe that `Allowed` has just checked. Shortcut fields are stored separately, so they cannot be injected.
- **Temp-file location**: `%LOCALAPPDATA%\Unravis\unravis-lnk-*.partial` belongs to the same user, so there is no cross-account race. A crash cannot leave a launchable half file in Startup. The cross-volume fallback writes in place, and a torn `.lnk` there would only make Explorer show an error at sign-in. Acceptable.
- **Accepted risk for the tray Open**: acceptable, but the wording needs correcting (MINOR-4).
- **INV-3**: holds. New log lines are the listening/tray line (`localURL` only, no query), "Tray icon unavailable: %v", "Second launches cannot reach this copy: %v", tray `Logf` for icon-add failures, and `t.Logf` of the `AskQuit` error. None carries a cookie, a query string, a user agent or a code. The link reaches the console only when the browser failed (WP-70c:19), and every minted code is a registered secret.
- **Launch code stays one-time**: it holds. `take` spends the code on every match (`launch.go:50`); `Rearm` replaces it under the same mutex and rejects codes outside 16-256 characters; a `--background` start arms none; a second copy exits in the gate before any code is minted; the box, tooltip and logs never carry it. The only exposure is the rundll32 command line, which is pre-existing and documented in WP-76b.
