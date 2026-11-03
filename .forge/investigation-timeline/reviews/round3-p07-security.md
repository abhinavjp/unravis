# Round 3 security review: Phase 07 (background run)

Scope: closure of round-2 findings (`reviews/round2-p07-security.md`) in `phases/07-background-run/`, with a spot check of `internal/setup` source (`runner.go`, `proto.go`, `uiserver.go` `LocalBackend.Run`, `layout.go`, `plan.go`, `uninstall.go`, `fixes_b.go`, `elevate_windows.go`, `log.go`) and `cmd/unravis-setup/main.go`. Review only; no code or plan file was changed.

Verdict: 0 BLOCKER, 2 MAJOR, 3 MINOR, 5 NIT. MAJOR-1 is closed for machine scope, which is the main case. It is still open for user scope when the elevated worker runs. A pre-existing elevated log write in the user's profile is in the same class and contradicts the phase's own claim.

## Closure table

| Round-2 item | Status | Evidence |
|---|---|---|
| MAJOR-1 elevated link write/delete in user profile | Closed for machine scope. Partly open for user scope (see R3-MAJOR-1). | WP-74i/74f/74g/74h/74j; proto.go `WorkerPrepare`; uiserver.go worker branch |
| MINOR-1 WM_ENDSESSION / WM_DESTROY / WM_QUIT | Closed | WP-71d: `systemEnding` = `GetSystemMetrics(SM_SHUTTINGDOWN)` only; `destroying` atomic gates WM_DESTROY; a foreign WM_QUIT is ignored in the loop; two tests |
| MINOR-2 Local\ name squatting | Closed (detected, not prevented, which is now worded honestly) | WP-72a `OwnedByCurrentUser` on the same handle; 72b Acquire; 72c Listen/Signal (no SetEvent on a foreign owner); 73b `ErrNameHeld`; 73c logs `ErrNameHeldByOther` |
| MINOR-3 unbounded RegisterSecret | Closed | WP-70b ring of 8; 70c `mintLaunchCode`; 73c `TestReopenManyTimesKeepsTheSecretListBounded` |
| MINOR-4 accepted-risk wording | Partly closed (see R3-MINOR-3) | WP-71d:38 and WP-77:24 are now identical, but still say "same integrity level" and leave out the telemetry side effect |
| NIT-1 TerminateIf has no owner check | Accepted, documented | WP-75a step 3 records the residual in the function comment; the caller passes only own-user PIDs from a fresh list |
| NIT-2 elevation before acquire | Closed | WP-73b: `elevated()` is checked first; test asserts names/acquire/signal are not called |
| NIT-3 AskQuit names from the worker token | Accepted, documented | WP-75c:12 cross-account gives `ErrNoInstance`, falling back to the verified `TerminateIf` |
| NIT-4 tray Enable overwrites a foreign Startup link | Closed | WP-74b `Enable`/`Disable` return `ErrNotOurs`, with tests for a link, garbage and a directory |
| NIT-5 rundll32/explorer by bare name | Closed | WP-73c `system32File("rundll32.exe")`; WP-73d `windowsFile("explorer.exe")`; text tests ban the bare name |

## What was confirmed safe in the MAJOR-1 split

- **No new privilege boundary on the window side.** `WriteProfileLinks` and `RemoveProfileLinks` run in the non-elevated window, as the profile owner. A junction or symlink the user plants there only redirects the user's own writes. `refuseLinkedFolder` and `linkIsOurs` (Lstat, regular file, 64 KiB cap) are hygiene here, not a boundary.
- **The worker cannot be talked out of `NoProfileLinks`.** `ServeWorker` copies the runner (`w := *r`) and calls `prepare(&req, &w)`. `WorkerPrepare` sets the folders from the server-token SID and `ProfileOf`, then `NoProfileLinks = true`. `WorkRequest` has only `Op`, `Options`, `Uninstall`, `AllowDowngrade`, `Silent` and `User`; none of them maps to a runner flag. `readRequest` uses `DisallowUnknownFields` and clears `Silent`/`ReadKeyFile`.
- **The protocol carries no paths back to the window.** After the worker, the window builds the layout from `b.R.Dirs` (its own token) and `o` (the old state or the request options), never from the worker's `Result`. Both ends of the pipe are pinned: the window checks the client PID equals the worker PID, and the worker checks the server PID equals its parent plus the image path. A malicious worker message therefore cannot make the window delete an arbitrary path. The window only touches `<Desktop>\Unravis.lnk` and `<Startup>\Unravis.lnk`, and only `os.Remove`s one that `linkIsOurs` proves points at the layout's `unravis.exe`.
- **The sign-in value sent to the worker is a plain bool.** It cannot carry a path or argument. Its only effect in the worker is the saved `StartAtSignIn` in the state file (WP-74h `NoProfileLinks` branch). Since the worker no longer writes the Startup link, a forged `true` does nothing except change a saved preference the same user could change in the tray. No further validation is needed.
- **Rollback.** A worker failure (rolled back, preflight stop, cancelled) triggers no window step (`TestFailedWorkerRunWritesNoLinks`). A window-step failure after commit leaves a complete install with a listed missing link, not a half install. Each of the two links is independent and the Desktop/Startup links are not part of the install contract, so treating them as best effort is correct.

## MAJOR

### R3-MAJOR-1: user-scope elevated worker still writes and deletes Start-menu and Quit links in the user's profile

**Evidence.**
- `layout.go` `NeedsAdmin(o)` is `Scope == Machine || Blackbox != nil`, and `NeedsAdminFor` is also true when a leftover service exists. A **user-scope** install with the Blackbox option, or a user-scope uninstall or repair with a leftover service, therefore runs in the elevated worker.
- For user scope `NewLayout` sets `StartMenuLink` under `d.Programs`, that is `%APPDATA%\Microsoft\Windows\Start Menu\Programs`, which is in the user's profile.
- WP-74d puts `QuitLink` next to it.
- WP-74f writes `StartMenuLink` and `QuitLink` "always", with path-based `t.MkdirAll` and `t.WriteFile` (keepOld rename plus write). WP-74g `removeShortcuts` handles both "always" with `os.Remove`.
- `refuseLinkedFolder` is called only for the Desktop and Startup folders.
- WP-74i's justification ("in the Start-menu folder of the install scope, which only an administrator can write") is true only for machine scope.
- `TestWorkerNeverWritesIntoTheUserProfile` uses machine scope and a spy limited to Desktop and Startup, so it cannot catch this.

**Scenario.** A standard user (or malware running as that user) starts a user-scope install with Blackbox, and an administrator approves the UAC prompt (over-the-shoulder, or the same account with split token).
1. Before the shortcuts step, the user replaces `...\Start Menu\Programs\Unravis` with a junction to `\RPC Control`.
2. The user creates an object-manager symlink `Unravis.lnk` pointing to `C:\Windows\System32\<target>`.
3. The elevated `keepOld` rename then moves a protected file, and the write creates attacker-shaped bytes in a protected folder. On uninstall or rollback, `os.Remove` deletes it.

The result is an arbitrary file move, create or delete as administrator, which is the standard local privilege escalation primitive. It is the same class as round-2 MAJOR-1, only through two other links.

**Concrete fix.**
1. Classify by location, not by name. In the worker, any link whose path is inside `r.Dirs.UserProfile` (`insideFolder`) is a profile link. With `NoProfileLinks` the worker skips it, and the window writes or removes it.
   - Generalise WP-74i into `profileLinks(l Layout, dirs Dirs) []string`, which returns every layout link under the profile.
   - For user scope this list is Start menu, Quit, Desktop and Startup. For machine scope it is Desktop and Startup.
   - Use that list in WP-74f, 74g and 74j.
2. Alternatively (simpler and stronger), refuse user scope in the worker. `WorkerPrepare`, or `NewPlan` when `NoProfileLinks`, returns an error when `o.Scope == ScopeUser` and any plan step targets a path inside `UserProfile`. Blackbox then requires machine scope, a rule the page can state.
   - This also covers the wider pre-existing exposure: a user-scope elevated run also writes the program files and the state file into, and deletes `UserData` from, the user's profile.
3. Tests:
   - Extend `TestWorkerNeverWritesIntoTheUserProfile` to a table with machine scope and user scope plus Blackbox. The spy records every `Sys` write, rename and remove, and the test asserts that none is under `UserProfile`.
   - Add `TestWorkerRefusesUserScope` if option 2 is chosen.
4. Correct the WP-74i sentence to say "machine scope only".

### R3-MAJOR-2 (pre-existing, same class): the elevated worker opens its log in the user's LocalAppData and follows links

**Evidence.**
- `cmd/unravis-setup/main.go` `runWorker` (line ~345) calls `openLog(o, e, filepath.Join(wp.Profile, "AppData", "Local"), true)`.
- `internal/setup/log.go` `DefaultLogPath` gives `<LocalAppData>\Unravis\setup-logs\setup-YYYYMMDD-HHMMSS-admin.log`.
- `OpenFileLog` calls `os.MkdirAll` and then `os.OpenFile(path, O_CREATE|O_APPEND|O_WRONLY)`. Both follow junctions and symlinks, and there is no `refuseLink`.

The file name is predictable to the second and the folder is user-owned. This is not new in Phase 07, but it directly contradicts the phase's claim that the worker never touches the user's profile, and it is a simpler primitive than the link writes.

**Scenario.** The user pre-creates `setup-logs` as a junction to `\RPC Control` with object-manager symlinks for the next few seconds' names, each pointing to `C:\Windows\System32\<dll or config>`. The admin approves UAC. The worker creates or appends to a protected file as administrator, with partly attacker-influenced content (for example paths and option values that end up in log lines). That can give a DoS by file creation at minimum, and code execution where a created file is loaded.

**Concrete fix (pick one).**
1. Preferred: the worker writes no log file of its own. Every line already goes to the window over `MsgLog`, and the window, as the user, writes the one log.
2. Or write the worker log under `MachineJournalDir` (ProgramData, admin-only ACL, created by `prepareJournalDir`, which already refuses links).
3. Or keep the path but open it handle-safely: `refuseLinkedFolder` on every level, then `CreateFile` with `CREATE_NEW | FILE_FLAG_OPEN_REPARSE_POINT`, then `GetFinalPathNameByHandle` must equal the intended path, otherwise close and fail.

Add `TestWorkerLogIsNotUnderTheUserProfile` (`runWorker` with a fake profile asserts that the log path is outside `wp.Profile`).

## MINOR

### R3-MINOR-1: the silent worker path has no profile-link step (orphan or missing links)

**Evidence.** `cmd/unravis-setup/main.go` lines ~270-284 (silent mode) call `setup.RunWorker` when `NeedsAdminFor && !IsAdmin`, then exit. WP-74j adds the after-worker step only inside `LocalBackend.Run`.

**Scenario.**
- A silent uninstall through `QuietUninstallString` or winget, by a standard user of a machine-scope install, removes Program Files but leaves `Desktop\Unravis.lnk` and `Startup\Unravis.lnk`.
- The Startup link then fires at every sign-in against a missing exe, which shows an error dialog. If the folder is later re-created by anyone able to write there, it starts whatever `unravis.exe` is found at that path, though Program Files is admin-only.
- A silent install or repair writes neither link, so the sign-in option is silently lost.

**Fix.**
- Move the after-worker block into one exported function, for example `func (r *Runner) ApplyProfileLinks(op Op, opts Options, res *Result, onLog func(string))`, and call it from `LocalBackend.Run` and from silent `main`.
- Do the same for the WP-74h pre-worker derivation (`DeriveStartAtSignIn`).
- Add `TestSilentWorkerUninstallRemovesOurProfileLinks` and `TestSilentWorkerInstallWritesProfileLinks`.

### R3-MINOR-2: the window step derives its inputs loosely

**Evidence.** In the WP-74j wording and the `uiserver.go` structure:
- (a) The pre-worker derivation is gated on `req.Op`. But `Run` resolves an install over an existing install into an upgrade or repair (`ResolveOp`), and `o` is then the old state, so that path skips the derivation and the page checkbox wins over the tray choice. This is the same defect WP-74h fixes in process.
- (b) A link read error keeps `req.Options.StartAtSignIn`, which is the page value, not the saved `o.StartAtSignIn` that WP-74h promises ("a read error keeps the saved value").
- (c) For a fresh install the layout is built from the raw page options, not from the canonical `opts.Validated` path the worker used. `AppExe` and `InstallDir` could then differ in case or form from what was installed, so `linkIsOurs` would differ and a Desktop link could get a non-canonical target.
- (d) After the worker it should use `res.Op` (what actually ran), not `req.Op`.

None of these gives escalation, because the window acts as the user. They make links and the saved setting disagree, and that is how orphan Startup links appear.

**Fix.**
- Compute `op := ResolveOp(req.Op, old, version)` once.
- Before the derivation set `req.Options.StartAtSignIn = o.StartAtSignIn`, then derive for `op` in {upgrade, repair}.
- After `OutcomeDone` or `OutcomeLeftovers`, re-run `Detect` (registry plus state the worker just wrote) and build the layout from that canonical state. For uninstall, use the pre-run `old` state.
- Add `TestInstallOverExistingKeepsTheTraySignInChoice`, `TestLinkReadErrorKeepsTheSavedValueNotThePage` and `TestWindowLinksUseTheCanonicalInstalledPath`.

### R3-MINOR-3: the MINOR-4 wording is still incomplete

**Evidence.** WP-71d:38 and WP-77:24 now carry one identical sentence, but it says "same integrity level". Round 2 asked for:
- "same or higher integrity level";
- that a send can make the previous link stop working (re-arm);
- that each send adds a `rundll32` process-creation event (EDR or telemetry noise).

**Fix.** Replace the sentence in both packets with: "Any process of the same user at the same or higher integrity level can send the open or quit signal. Open re-arms a new launch code, so the previous link stops working, and each accepted open starts `rundll32.exe`, which adds a process-creation event. Quit ends the app. This is accepted: such a process already controls the user's session." Keep the two copies word for word, and add the wording check to the WP-77 proof.

## NIT

- **R3-NIT-1:** `ServeWorker` should set `w.NoProfileLinks = true` itself, before calling `prepare`. This is defence in depth: today a nil `prepare` appears only in tests, but the flag should not depend on the callback. Add the assignment, and keep the WP-74j proof's "one match" check by counting `proto.go` matches as two, or by asserting it in the test.
- **R3-NIT-2:** `WriteProfileLinks` should remove `<link>.tmp` on every failure path (WP-74b already does this for the tray). Its doc should also say that a `Done`-with-`Left` result is shown on the page, so the user sees the missing link.
- **R3-NIT-3:** If the window crashes or is closed between the worker commit and `RemoveProfileLinks` on uninstall, an orphan Startup link to a deleted Program Files exe is left. The impact is low (error at sign-in; Program Files is admin-only). Record it in WP-76a's installer plan as a known limit, or have the app's `Allowed` check at the next start of a reinstall clean it up.
- **R3-NIT-4:** In a same-account elevated in-process run (the setup window started as administrator, or silent from an elevated console), `IsAdmin` is true, so the in-process plan writes Desktop and Startup links path-based, and `refuseLinkedFolder` has a check-then-use race. UAC is not a security boundary for the same account, so record this as accepted in WP-74i instead of fixing it.
- **R3-NIT-5:** WP-71d's message loop should treat a `GetMessage` return of -1 as fatal (log and leave the loop) rather than continue, to avoid a busy spin on an invalid window handle after `destroying`.

## New issues introduced by the changes

Only R3-MINOR-1 and R3-MINOR-2 are new. R3-MAJOR-1 is the original MAJOR-1 surviving through the Start-menu and Quit links, which WP-74i kept in the worker on a premise that is false for user scope. R3-MAJOR-2 predates Phase 07. No new elevated path, pipe message or request field was added. The window-side link code crosses no privilege boundary, and the single-instance, tray and launch-code changes reviewed here add no new surface beyond what the table above records.
