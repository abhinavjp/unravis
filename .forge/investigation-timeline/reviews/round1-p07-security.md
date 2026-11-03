# Round 1 security review: Phase 07 background-run

Scope: every packet of `phases/07-background-run`, with emphasis on WP-72c, 73a, 73c, 74b and 75b, plus 72a/b/d, 74c, 74f/g/h and 75a/c. Spot checks of today's code: `internal/setup/plan.go`, `os_windows.go`, `fixes_b.go`, `uninstall.go`, `txn.go`, `layout.go`, `window_windows.go`, `internal/server/launch.go`, `internal/applog/sanitize.go`, `cmd/audit-explorer/main.go`.

Threat model: a local multi-user Windows machine; other processes of the same user, and processes of other users in the same interactive session (runas, CreateProcessWithLogonW); a user-writable install dir; an elevated setup worker.

Result: 0 BLOCKER, 2 MAJOR, 9 MINOR.

## Findings

[MAJOR] WP-75b — Terminate after the quit request can kill an unrelated process because the PID was reused. Evidence:
- `WP-75b-quit-on-upgrade.md:16` asks the app to quit, polls for up to 20 x 250 ms, then calls "`Terminate` for whatever of `mine` still runs". It does not say whether `mine` is the stale list from before the request or a fresh one, or how a process is matched.
- `internal/setup/os_windows.go:686-697` (`Terminate`) does `OpenProcess(PROCESS_TERMINATE|SYNCHRONIZE, pid)` and then `TerminateProcess`. It never checks the image path.
- Today `closeApp` (`plan.go:160-184`) terminates almost at once, so the race window is tiny. WP-75b makes the target process exit on purpose and then waits up to about 5 s, so a freed PID being taken by another process is now a realistic case.

Attack/accident scenario: Unravis PID 4120 quits cleanly on the signal. During the poll, the user's browser or editor (or a process an attacker spawns in a loop to grab PIDs) gets PID 4120. If the last poll round matched by PID only, or the stale `mine` list is used, setup (possibly the elevated worker) calls TerminateProcess on that process. Unsaved work is lost, or a security tool of the same user is killed.

Fix (packet text):
- Each poll round re-runs `p.runningApps()` and `splitApps`. A process counts as still running only when the (PID, Exe) pair from the latest round matches an entry of the original `mine`, using `samePath` on Exe.
- Terminate only the entries of the last round, never the stale list.
- Add a verified terminate, either `osProcesses.Terminate` itself or a new `TerminateIf(pid uint32, exe string)`. After `OpenProcess` it calls `QueryFullProcessImageName` on the opened handle and compares the result with `exe` using `samePath` before `TerminateProcess`; a mismatch returns `ErrNotFound`. This closes the TOCTOU because the handle pins the process.
- Add `os_windows.go` to the write scope of WP-75b, or to a new small packet that WP-75b depends on. WP-75c also writes that file, so give the two packets an edge.
- New tests:
  - `TestUpgradeDoesNotTerminateAReusedPID`: the fake list returns the same PID with a different Exe after AskQuit, and `Terminate` is not called.
  - `TestTerminateChecksTheImagePath`: a Windows test that starts a harmless child (for example `cmd /c pause` with a pipe), calls `TerminateIf(child.pid, "C:\\other\\unravis.exe")`, and asserts `ErrNotFound` and that the child is still alive.

[MAJOR] WP-71d / WP-73d — Window messages reach the tray window with no access check, so the event DACL in WP-72c is not the only way to quit or open. Evidence:
- `WP-71d-tray-window.md:19` maps the callback message (WM_APP+1 with `WM_LBUTTONUP` / `NIN_KEYSELECT`) to `OnOpen`.
- `WP-71d-tray-window.md:20` uses a posted `WM_CLOSE` as the stop path.
- `WP-71d-tray-window.md:28` sends `WM_ENDSESSION` with wparam 1 to `OnEndSession`, which WP-73d wires to `cancel`, so the app quits.
- A window has no DACL. Any process on the same desktop at the same or higher integrity level can post to it, including a process of another user started with runas in the same session. UIPI blocks only lower IL senders. The window can be found with `FindWindow("UnravisTrayWindow*")`, since the class name is fixed apart from a counter.

Attack scenario:
- A process of a second account in the same session (or any same-IL process) posts `WM_ENDSESSION(1)`: Unravis quits and its mutex is released, so the attacker can start its own copy and squat the names, or simply deny service.
- Posting `WM_APP+1/WM_LBUTTONUP` in a loop mints a launch code and opens a browser tab every 2 s (throttled).
- Posting `WM_CLOSE` removes the tray icon while the server keeps running, so the user can no longer quit from the tray.

Fix (packet text, WP-71d):
- Act on `WM_ENDSESSION` only when the system is really ending the session: `InSendMessageEx(0)&ISMEX_SEND != 0` (a posted message fails this test) or `GetSystemMetrics(SM_SHUTTINGDOWN) != 0`, behind an injectable `endingSession func() bool` so the test can fake it.
- Use a private stop message (`WM_APP+2`) that the ctx goroutine posts, and handle `WM_CLOSE` as a no-op (or as stop only when the private flag set by the ctx goroutine is set).
- Add a Select-String proof that `ChangeWindowMessageFilter` / `ChangeWindowMessageFilterEx` never appear in `internal/tray` (keeps the UIPI wall against low-IL senders).
- Document in WP-71d and in the WP-77 manual rows that "open" through the tray callback is reachable by same-desktop same-IL processes. It is accepted because it only opens the user's own browser with a fresh single-use code.
- New tests:
  - `TestTrayPostedEndSessionIsIgnored`: the fake `endingSession` returns false, and `OnEndSession` is not called.
  - `TestTrayIgnoresForeignClose`.

[MINOR] WP-72c — `Signal` does not close its event handle. Evidence: `WP-72c-instance-signal.md:18` says "OpenEvent ... then SetEvent" and says nothing about `CloseHandle`. Scenario: setup's in-process `AskQuit` (WP-75c), or any long-lived sender, keeps the named event alive after the app exits. The next instance's `Listen` then finds the name existing and fails with `ErrEventsTaken`. This breaks exactly the upgrade-then-restart flow. Fix: `defer windows.CloseHandle(h)` in `Signal`, and add `TestSignalDoesNotKeepTheEventAlive` (Signal, stop the listener, then a new Listen on the same names succeeds).

[MINOR] WP-72b / WP-72c — Another account in the same session can squat the names. Evidence: the names live in `Local\`, which is per session, not per user. `WP-72b-instance-guard.md:16` treats `ERROR_ALREADY_EXISTS` as "another copy runs". `WP-72c-instance-signal.md:18` signals whatever object carries the name.

Scenario: a second account (runas in the same session) creates `Local\<app>-mutex` and the events first. Results:
- The user's real start gets `first=false` and fails to signal (no listener), which is a silent denial of service. Or it gets ACCESS_DENIED with the wording "another account".
- `--quit` reports success against the squatter's event.

Fix:
- Put the user SID in the names in WP-72a (`Local\Unravis-<SID>-...`), or create a private namespace bound to the user SID. Either makes squatting impossible across accounts.
- Optionally also, in `Signal`, open with `EVENT_MODIFY_STATE|READ_CONTROL` and refuse (`ErrRunningForAnotherAccount`) when the owner SID of the object is not the current token user. In `Acquire`, check the mutex owner the same way on `ERROR_ALREADY_EXISTS`.
- Add `TestNamesContainTheUserSID` to WP-72a.

[MINOR] WP-72c — `TestEventsGrantNothingToEveryone` is a denylist. Evidence: `WP-72c-instance-signal.md:27` checks only S-1-1-0, S-1-5-11 and S-1-5-32-545. The default DACL also grants the logon SID (S-1-5-5-x-y), and a non-default DACL set by policy or by an elevated parent would pass this test while still granting others. Fix: make it an allowlist. Every ACE that grants `EVENT_MODIFY_STATE` (or GENERIC_WRITE / GENERIC_ALL) must belong to the current user SID, SYSTEM (S-1-5-18) or Administrators (S-1-5-32-544); fail on any other SID.

[MINOR] WP-73b / WP-72b — An elevated first copy locks out the non-elevated user. Evidence: `window_windows.go:54` `StartProgram` runs `exec.Command(exe)` from the setup window. If setup itself was started with "Run as administrator", or the user once ran `unravis.exe` elevated, the mutex and events get the Administrators-only default DACL and a High label. Later non-elevated starts get ACCESS_DENIED, and `WP-73b-single-instance-gate.md:18` maps that to `ErrRunningForAnotherAccount`, which is misleading. `--quit` cannot reach it either. Fix:
- In the gate, read `TokenElevation`. When the process is elevated, log a warning and do not take the guard, or refuse with a clear message ("start Unravis without administrator rights").
- Make the ACCESS_DENIED text say "under another Windows account or as administrator".
- Add `TestGateRefusesOrWarnsWhenElevated` with an `elevated func() bool` seam.

[MINOR] WP-73b / WP-73c — The `--background` start still mints a long-lived code and prints the link, and minted codes are not registered with the log sanitizer. Evidence:
- `WP-73b-single-instance-gate.md:16`: "with `--background` the launch link is still created".
- `cmd/audit-explorer/main.go:221-230`: `codeLife` is 10 minutes when no browser is opened.
- `main.go:267` writes the startup lines; WP-70c keeps the link out of the log but prints it to the console.
- `applog.RegisterSecret` (`internal/applog/sanitize.go:41`) is called today for the session token only, not for the launch code.

Scenario: a sign-in start (Startup link with `--background`) arms a code that nobody uses for 10 minutes. If stdout is captured (PowerShell transcription, a redirected launcher, a later AttachConsole), the link is readable by anyone who can read that capture. Fix:
- With `--background`, do not arm an initial code (arm with an expiry in the past, or skip `launchState`) and do not print the link line. The tray Open (WP-73c) mints a fresh code when needed.
- Call `applog.RegisterSecret(code)` for every code minted at start-up and in `reopener.Reopen`, as defense in depth for INV-3.
- Add `TestBackgroundStartArmsNoCode` and `TestReopenRegistersTheCodeAsASecret`.

[MINOR] WP-73c — The MessageBox fallback shows a link that is live and holds the code. Evidence: `WP-73c-reopen-quit-wiring.md:23` (`TestReopenWarnsWithTheLinkWhenTheBrowserFails`: warn text contains the link). The code is valid for 2 minutes. While the box is open, any same-desktop same-IL process, including another account through runas, can read the static text with `WM_GETTEXT`, redeem the code first, and receive the session token in the redirect fragment. Fix:
- Do not put the link in the box. Say "Could not open your browser. Choose Open in the tray menu to try again", or offer a "Copy link" button that puts it on the clipboard only on click.
- If the owner keeps the link in the box, record it as an accepted residual risk in plan.md §10.
- Either way, the rundll32 command line (`main.go:265`) exposes the code to process-creation auditing (4688, Sysmon, EDR) on every reopen. That is pre-existing and accepted with the 2-minute single-use life; note it in WP-76b troubleshooting.

[MINOR] WP-74g — Uninstall deletes shortcuts by path only. Evidence: `WP-74g-uninstall-shortcuts.md:17` uses "the same `os.Remove`" for `DesktopLink` and `QuitLink`, and `uninstall.go:148` does this today for Start-menu and Startup links. Scenarios:
- The user already had their own `Desktop\Unravis.lnk` (for example to a portable copy, or a shortcut to a different tool named Unravis). WP-74f overwrites it on install and WP-74g deletes it on uninstall.
- Per-user Startup links that the tray toggle created for other users of a machine install are left behind and point to a removed exe.

Fix:
- In `removeShortcuts`, read each link with `shelllink.ReadShortcutTarget` and remove it only when the target matches `Layout.AppExe` (`samePath`). Otherwise report "kept: points elsewhere".
- In WP-74f, skip writing the Desktop link when one exists that points elsewhere.
- Document the leftover per-user Startup links in WP-76b. The app already fails harmlessly when the exe is gone.
- Add `TestUninstallKeepsADesktopLinkThatPointsElsewhere`.

[MINOR] WP-74f — The elevated worker writes into the user's Desktop through a path that a junction can redirect. Evidence: `WP-74f-install-shortcuts.md:15-16` writes `DesktopLink` with `t.WriteFile`, and `txn.go:158` uses `os.WriteFile`, which follows reparse points. `CheckUserDirs` (`layout.go:51`) checks only the path suffix. Scenario: in a machine-scope install with over-the-shoulder elevation, the profile owner (a standard user) turns `Desktop` into a junction to a folder they cannot write, for example `C:\ProgramData\Microsoft\Windows\Start Menu\Programs\StartUp`. The elevated worker then drops `Unravis.lnk` there. The content is fixed (target is the installed exe), so impact is low: a machine-wide autostart for every user. The Startup link already has the same pattern. Fix: before writing into any user-profile folder from the elevated worker, reject the folder when it, or any parent up to the profile root, is a reparse point (the existing `fileid_windows.go` helper exposes the reparse flag). Alternatively, let the non-elevated window process write the Desktop and Startup links. Add `TestInstallRefusesAJunctionedDesktop` (Windows only).

[MINOR] WP-74b / WP-74c — Autostart checks are incomplete. Evidence:
- `WP-74b-autostart-package.md:17`: `Enabled` only checks that the file exists, so a planted or stale `Unravis.lnk` that points elsewhere shows as ticked.
- `WP-74b-autostart-package.md:19`: `Enable` writes its temp file inside the Startup folder. A crash between write and rename leaves a file that Explorer tries to open at every sign-in.
- `WP-74c-startup-folder.md:19` asserts the folder is inside `os.UserHomeDir`, which fails on machines with folder redirection.

Fix:
- `Enabled` returns true only when `ReadShortcutTarget` matches `exe` and the args equal `--background`. A mismatch shows the item unticked, and toggling it rewrites the link.
- Create the temp file with `FILE_ATTRIBUTE_HIDDEN` and a non-launchable name (for example `.Unravis.lnk.partial`), or write it in `%LOCALAPPDATA%\Unravis` and rename only when that folder is on the same volume (fall back otherwise).
- Change the WP-74c test to require "absolute, exists, base name `Startup`" and drop the profile-root assertion, or skip it when `FOLDERID_Startup` is redirected.

[MINOR] plan.md §3 — Dependency edges are missing for WP-75c. Evidence: `plan.md:135` lists WP-75c with deps `75a, 74e` only. WP-75c calls `instance.Signal` and `instance.NewNames` / `DefaultApp` from WP-72a and WP-72c (`WP-75c-real-askquit.md:14`). The build works today only because WP-72c is in wave 2 and WP-75c is in wave 3. Fix: add `72a, 72c` to WP-75c in §3 and in its `## Depends on`. If the verified terminate from the first MAJOR goes into `os_windows.go`, add an edge between that packet and WP-75c, since both write `os_windows.go`.

## Checked and clean

- Shortcut argument injection: `.lnk` stores target and arguments separately, so paths with spaces cannot inject. The arguments are fixed literals (`--background`, `--quit`). A user editing their own link crosses no boundary.
- Elevated setup never runs the user-writable exe to ask it to quit: `AskQuit func() error` takes no path (WP-75a), and WP-75c signals in-process. The Select-String proofs cover this.
- Consent comes before AskQuit (`TestUpgradeDoesNotAskWithoutConsent`), and other users' processes are never touched (`splitApps`).
- Low-integrity and AppContainer processes cannot open the events: NO_WRITE_UP blocks `EVENT_MODIFY_STATE`, and AppContainers have their own namespace. `Global\` is not used.
- Tray menu: `TPM_RETURNCMD|TPM_NONOTIFY` (WP-71c) means a posted `WM_COMMAND` cannot pick Quit or Start at sign-in.
- Log contents: WP-70c and WP-73c keep the link and the code out of `log` and `Logf`, with canary tests. Apart from the RegisterSecret hardening above, no cookie, query string or user agent is newly written (INV-3).
- Uninstall of the Quit link: the path is fixed under the Start menu folder the installer created.
- INV-1..8: no violations found. INV-5 holds: retries and polls use fake sleeps and nothing asserts elapsed time.
- R6-02 and R6-03 were not reviewed (owner decisions).
