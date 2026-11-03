# WP-74j The setup window and silent mode write the profile links after the elevated worker commits
risk: high    size: M    wave: 5    model: sonnet
## Outcome
Carries out the approach stated in WP-74i. In the elevated worker `Runner.NoProfileLinks` is true, so the plan never reads, writes or removes a link inside the user's profile (Desktop, Startup, and for a just-for-me install the Start-menu and Quit links). After the worker returns a successful result, the non-elevated caller creates (install, upgrade, repair) or removes (uninstall) those links itself, as the user, through the two shared functions of WP-74i: `DeriveStartAtSignIn` before the request is sent and `ApplyProfileLinks` after. The setup window (`LocalBackend.Run`) and silent mode (`cmd/unravis-setup` `main.go`) both call the same two functions, so a silent uninstall leaves no orphan Startup link and a silent install keeps the sign-in option. The sign-in value is derived from the RESOLVED operation (`ResolveOp`), so an install request over an existing install behaves like the upgrade or repair it becomes, and a link that cannot be read keeps the SAVED value. The elevated worker also writes no log file of its own: its lines already travel to the window over the pipe (`MsgLog`), and the window writes the one log as the user. A failure of the best-effort links is logged and listed and never turns a committed install into a failure. When no elevation is needed, nothing changes: the in-process run keeps the links in its transaction (WP-74f).

## Write scope
- `internal/setup/proto.go`
- `internal/setup/uiserver.go`
- `internal/setup/worker_profile_links_test.go` (new)
- `cmd/unravis-setup/main.go`
- `cmd/unravis-setup/main_test.go`
- `cmd/unravis-setup/profile_links_test.go` (new)

## Must not change
- The wire format of `WorkRequest` (no new field: the sign-in value travels in the existing `Options.StartAtSignIn`), the consent and elevation rules, the `DescribeChanges` text (WP-74g) and every other file.
- The worker still takes its folders and SID from `WorkerPrepare`; nothing the page sends can turn `NoProfileLinks` off in the worker.
- `cmd/unravis-setup/main_test.go`: only the one expectation about the worker's own log (step 4) changes; never weaken another assertion.

## Changes
1. `proto.go`: in `ServeWorker`, directly after the runner is copied (`w := *r`) and BEFORE `prepare` is called, `w.NoProfileLinks = true` (so the flag never depends on the callback, even when `prepare` is nil); and in the function returned by `WorkerPrepare`, after the folders are set, `r.NoProfileLinks = true`.
2. `uiserver.go`, `LocalBackend.Run`: compute `op := ResolveOp(req.Op, old, b.version())` once, next to `o`. In the branch that sends the request to the worker (`NeedsAdminFor && !b.IsAdmin`):
   - Before `RunWorker`: `r := *b.R` then `r.DeriveStartAtSignIn(op, o, &req.Options)` (WP-74i; `o` is the existing install's options for upgrade, repair, uninstall and an install over an install, so the saved value is the fallback). Use `op`, never `req.Op`.
   - After `RunWorker` returns `res`: `opts := o; opts.StartAtSignIn = req.Options.StartAtSignIn` then `r.ApplyProfileLinks(opts, &res, onLog)` (WP-74i does the outcome check, the `res.AppExe` layout fix, the uninstall removal and the best-effort listing; a rolled-back, preflight-stopped or cancelled result triggers nothing).
3. `cmd/unravis-setup/main.go`, silent mode (the block where `NeedsAdminFor && !IsAdmin` sends the request to `setup.RunWorker`): the runner `r` there is the non-elevated one. Before `RunWorker`: `r.DeriveStartAtSignIn(setup.ResolveOp(req.Op, old, <the version value Perform uses for ResolveOp in proto.go>), opts, &req.Options)`. After it: `o2 := opts; o2.StartAtSignIn = req.Options.StartAtSignIn; r.ApplyProfileLinks(o2, &res, func(s string) { l.Line("links: " + s) })`. Call nothing else of its own: the in-process branch (`r.Perform`) is unchanged.
4. `cmd/unravis-setup/main.go`, `runWorker`: remove the `openLog(...)` call and its `defer closeLog` (the worker must not create or append to any file under the user's profile), set `r.Log = nil` (`ServeWorker` already forwards each line to the window as `MsgLog`, and the window and silent mode log them as `worker: ...`), and drop the two `l.Line` calls. Errors that happen before the pipe is dialled still go to `e.stderr`. In `main_test.go` the existing worker test that globs `*-admin.log` (near line 437) now expects ZERO files; change only that expectation and list it in the packet note.
5. `worker_profile_links_test.go` (package `setup`; fake elevator `pipeElevator` of `uiserver_test.go`, fake system, real temp folders). Tests that must show what the WORKER wrote call `RunWorker` with the `pipeElevator` directly (not `LocalBackend.Run`) and set `Runner.BeforeCommit` to list the folders into a variable, because the window's own later writes would otherwise be in the picture:
   - `TestWorkerNeverWritesIntoTheUserProfile`: a table of two cases, (1) machine-scope install, (2) user-scope install with the Blackbox option (so it needs the worker). In `BeforeCommit` (after the `shortcuts` step) list the user's Desktop folder, Startup folder and, for user scope, the Start-menu link and the Quit link paths. After `RunWorker` returns `OutcomeDone`, assert every listing is empty, that a folder which did not exist before still does not exist, and that neither profile Start-menu link nor Quit link exists in case (2); in case (1) the machine-scope Start-menu and Quit links DO exist.
   - `TestWorkerLeavesAPlantedDesktopLinkAlone`: worker half: a foreign `Unravis.lnk` planted on the Desktop is byte for byte unchanged after `RunWorker` (same `BeforeCommit` shape). Window half on `LocalBackend.Run`: after the full run it is still unchanged (the window skips a foreign Desktop link).
   - `TestWindowWritesTheDesktopAndStartupLinksAfterTheWorker` (Desktop link and the `--background` Startup link exist after a successful worker install; the Desktop link targets the exe in `res.AppExe`).
   - `TestWindowWritesStartMenuAndQuitLinksForUserScopeAfterTheWorker` (user scope with Blackbox: both exist after the run, written by the window; Quit has `--quit`).
   - `TestWindowWritesNoStartupLinkWhenTheOptionIsOff`.
   - `TestWorkerUninstallLeavesTheLinksAndTheWindowRemovesOnlyOurs` (machine scope; during the worker the links are untouched; after the run our links are gone, a foreign Desktop link is kept and listed in `res.Left`).
   - `TestWorkerUninstallLeavesUserScopeStartMenuAndQuitLinks` (user scope with Blackbox: in `BeforeCommit` of the uninstall both profile links still exist; after the run the window has removed them).
   - `TestFailedWorkerRunWritesNoLinks` (rolled-back result: no link in the profile).
   - `TestLinkWriteFailureIsListedAndDoesNotFailTheInstall` (the Desktop folder cannot be written: outcome stays `OutcomeDone`, `res.Left` has one line).
   - `TestUpgradeSendsTheSignInValueReadFromTheStartupLink` (no Startup link, saved option on: the request the worker receives has `StartAtSignIn == false`).
   - `TestUpgradeKeepsTheSavedSignInValueWhenTheLinkCannotBeRead` (swap `readLinkFile` to fail; saved option on; the page sent false; the request the worker receives has `StartAtSignIn == true`).
   - `TestInstallRequestOverAnInstallIsTreatedAsUpgrade` (`req.Op == OpInstall` over an existing install with the option on and no Startup link: the worker receives false, from the link, not the page's value).
   - `TestWorkerPrepareSetsNoProfileLinks` (the runner after the prepare function has `NoProfileLinks == true`, and a request cannot change it).
   - `TestServeWorkerSetsNoProfileLinksEvenWithNilPrepare` (`ServeWorker` with a nil prepare: the runner that performs the plan has the flag).
   - `TestInProcessRunKeepsTheLinksInItsTransaction` (no elevation needed: the links exist after the run and a fault after the `shortcuts` step removes them).
6. `cmd/unravis-setup/profile_links_test.go` (use the rig `newRig` and the `connElevator` style of `main_test.go`; the elevator runs `runWorker` over `net.Pipe`):
   - `TestWorkerLogIsNotWrittenAnywhere` (`runWorker` with a fake profile: no `setup-logs` folder and no `*-admin.log` exists under the fake `wp.Profile`, and the worker still returns exit 0).
   - `TestSilentWorkerInstallWritesProfileLinks` (silent machine-scope install through the worker: Desktop link and Startup link exist afterwards).
   - `TestSilentWorkerUninstallRemovesOurProfileLinks` (silent uninstall of a machine-scope install: our Desktop and Startup links are gone, a foreign Desktop link is kept).
   - `TestSilentUpgradeKeepsTheTraySignInChoice` (saved option on, user removed the Startup link: after a silent upgrade through the worker the option stays off and no Startup link is written).

## Proof
- `go` is `.dev\toolchains\go\bin\go.exe` from the repo root with the environment of WP-58 step 3.
- `go test ./internal/setup -run "TestWorkerNeverWrites|TestWorkerLeavesAPlanted|TestWindowWrites|TestWorkerUninstallLeaves|TestFailedWorkerRunWritesNoLinks|TestLinkWriteFailure|TestUpgradeSendsTheSignIn|TestUpgradeKeepsTheSavedSignIn|TestInstallRequestOverAnInstall|TestWorkerPrepareSetsNoProfileLinks|TestServeWorkerSetsNoProfileLinks|TestInProcessRunKeeps" -count=1 -v` -> 15 tests `PASS`.
- `go test ./cmd/unravis-setup -run "TestWorkerLogIsNotWritten|TestSilentWorker|TestSilentUpgradeKeeps" -count=1 -v` -> 4 tests `PASS`.
- `go test ./internal/setup ./cmd/unravis-setup -count=1` -> `ok` for both (list the one changed expectation in `main_test.go`); `go vet ./internal/setup ./cmd/unravis-setup` -> no output; `go build ./...` -> no output.
- Security acceptance: (a) `TestWorkerNeverWritesIntoTheUserProfile` passes for machine scope and for user scope with Blackbox (no elevated write of any link into the user's profile); (b) `TestWorkerPrepareSetsNoProfileLinks` and `TestServeWorkerSetsNoProfileLinksEvenWithNilPrepare` pass; (c) `Select-String -Path internal/setup/proto.go -Pattern "NoProfileLinks"` -> two matches, both assignments `= true`; (d) `TestWorkerLogIsNotWrittenAnywhere` passes and `Select-String -Path cmd/unravis-setup/main.go -Pattern "openLog"` shows no call inside `runWorker`.

## Depends on
WP-74f, WP-74g, WP-74h
