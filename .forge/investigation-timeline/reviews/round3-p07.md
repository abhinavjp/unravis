# Round 3 review: Phase 07 background-run (scoped re-check)

Scope: round-2 fixes in `phases/07-background-run/` (new WP-74i, WP-74j; changed WP-74f/74g/74h/75b, 72a-c, 71d, 70b-d, 73b-d, 76a, 76b, 77), plan.md section 3 rows, round2-p07.md. Read-only. Spot-checked against `internal/setup/{proto,uiserver,runner,plan,layout,uninstall,fixes_b,txn,system,options,state}.go`, `cmd/unravis-setup/ui/assets/setup.js`, `README.md`, `MCP.md`, `docs/user/*`.

`check-plan.ps1`: `packets in plan: 79; packet files: 79; errors: 0; same-wave overlaps: 0; warnings: 0` then `OK`.

Verdict: the round-2 minors are all fixed, 76a/76b are intact, edges and waves are consistent. No BLOCKER. Three MAJOR findings, all in the new profile-link design (WP-74i/74f/74j); five MINOR.

## 1. Round-2 minors: all fixed

| Round-2 minor | Verdict |
|---|---|
| 71d missing constants | FIXED: `NIN_SELECT`, `WM_NULL`, `WM_RBUTTONUP`, `WM_TIMER` etc. now listed (line 17). |
| 71d retry-test flake | FIXED: `armRetryHook`/`disarmRetryHook` faked, no real timer (line 29). |
| 70d `ptr(...)` | FIXED: `windows.UTF16PtrFromString` text first, caption second; says there is no helper `ptr`. |
| 73c `reopener` shadow | FIXED: variable is `reo`, plus `TestMainUsesReoNotAShadowedType`. |
| 72c elevated wording | FIXED (line 13: elevated can signal normal; reverse is `ErrElevated`/another account). |
| 75c cross-account | FIXED (line 12: `ErrNoInstance`/`ErrAccessDenied` from a different account is expected, falls to `TerminateIf`); 75b Outcome says it too. |
| 76a README anchor | FIXED (names the "Start" section paragraphs). |
| 77 M9 / M11 notes | FIXED: M9 now says Desktop, Start-menu, Quit and Startup shortcuts exist after install, removed by uninstall, brought back by repair. |

## 2. WP-76a and WP-76b: complete

Both files have exactly the six headings in order (Outcome, Write scope, Must not change, Changes, Proof, Depends on), a header line with `risk/size/wave/model`, no non-ASCII bytes, no stray lines, a final newline. The Depends lists match plan.md rows (76a: 73e, 70e, 74g, 74j, 75b; 76b: 73e, 75b, 74g, 74j). Referenced files exist (`README.md`, `MCP.md` with the "How MCP finds the dashboard" section near line 77, `buildout/plan/installer-and-releases.md`, `docs/user/{install,upgrade-and-uninstall,troubleshooting}.md`; `NOT RUN` marker exists in `install.md` line 7). Proof greps for README/MCP terms are not vacuous (counts are 0 today for "administrator", "Quit Unravis", "windowless|console window"). One weak proof is listed under MINOR.

## 3. Edges, waves, write-scope chains: consistent

Plan.md rows equal the packet `Depends on` lines and waves (check-plan agrees). Chains:
- `runner.go`: 74i (w2) only.
- `plan.go`: 74f (w3) -> 74h (w4) -> 75b (w5). 74g (w4, `uninstall.go`, `uiserver.go`) and 74h (w4, `plan.go`) have disjoint scopes.
- `uiserver.go`: 74g (w4) -> 74j (w5). `proto.go`: 74j only. 74j and 75b are both w5 with disjoint scopes (74j: proto.go, uiserver.go, test; 75b: plan.go, test).
- `os_windows.go`: 75a (w1) -> 74e (w2) -> 75c (w3).
- `main.go`: 70c (w2) -> 70d (w3) -> 73a (w4) -> 73b (w5) -> 73c (w6) -> 73e (w7).
No invented edge. 74g and 74j consume 74i symbols (`linkIsOurs`, `WriteProfileLinks`, `RemoveProfileLinks`, `NoProfileLinks`) through 74f -> 74i only (transitive); accepted, as in round 2. Test counts in the Proof lines (74f 9, 74h 8, 74j 10, 75b 9, 73b 13, 73d 9, 72a 11) match the `-run` patterns against the named tests.

## BLOCKER

none

## MAJOR

[MAJOR] WP-74i `refuseLinkedFolder` / WP-74f step 3 — the junction guard walks from the Desktop or Startup folder up to the profile and calls `refuseLink` on every folder; `refuseLink` (`fixes_b.go:32-54`) rejects any folder with `ModeSymlink|ModeIrregular` or `fs.Reparse`. A OneDrive folder is a reparse point: on this machine `(Get-Item $env:OneDrive).Attributes` is `ReadOnly, Directory, Archive, ReparsePoint`. The common OneDrive "Known Folder Move" Desktop is `C:\Users\<u>\OneDrive\Desktop`, which WP-74e keeps (inside the profile), so the walk hits the `OneDrive` folder and returns "is a link to another place". In the in-process path (WP-74f) that error fails the `shortcuts` step and rolls the whole install back for every OneDrive-Desktop user; in the window path (WP-74j) the Desktop link is never created. The guard also has no security value here any more: after WP-74i the elevated worker never writes these links, and the user's own process may write anywhere in its own profile — Fix: in WP-74i make `refuseLinkedFolder` check only the leaf folder `dir` (no parent walk), and in WP-74f/74i treat a refusal as "skip this link and log `shortcut skipped: <error>`" instead of returning the error from the step; keep `TestWriteProfileLinksRefusesAJunctionedDesktop` (junction leaf is skipped, nothing written into the target) and change `TestInstallRefusesAJunctionedDesktop` to "install succeeds, no Desktop link, other folder empty"; add `TestWriteProfileLinksAcceptsADesktopWhoseParentIsAReparsePoint` (use a fake `Sys.Sec` with `Reparse` true for the parent only). Note that a OneDrive placeholder leaf could still be flagged; skipping (not failing) covers that.

[MAJOR] WP-74j step 2 — "a read error keeps the saved value" is false as written: for upgrade/repair the worker plan (WP-74h worker branch) sets `p.Opts.StartAtSignIn = opts.StartAtSignIn`, the REQUEST value, which is whatever the page sent (the installer default or checkbox), not the saved state (`plan.go:79` `p.Opts = old.State.Options()` is overwritten by 74h). So when `linkTarget` returns an error the setting is silently replaced by the page's value. Second gap: the condition is on `req.Op` being `OpUpgrade`/`OpRepair`, but `Perform` runs `ResolveOp(req.Op, ...)` (`proto.go:74`), so an `OpInstall` request over an existing install that resolves to upgrade or repair reaches the worker with the page's value and skips the probe — Fix: in `Run`, for every request where `ResolveOp(req.Op, old, b.version())` is `OpUpgrade` or `OpRepair` (use that, not `req.Op`), first set `req.Options.StartAtSignIn = old.State.StartAtSignIn` (equivalently `o.StartAtSignIn`, `o` is already `old.State.Options()` there), then run the probe and overwrite only when `linkTarget` returned no error; add test `TestUpgradeKeepsTheSavedSignInValueWhenTheLinkCannotBeRead` (swap `readLinkFile` to fail; saved option on; the request the worker receives has `StartAtSignIn == true` even though the page sent false) and `TestInstallRequestOverAnInstallIsTreatedAsUpgrade`.

[MAJOR] WP-74j test `TestWorkerNeverWritesIntoTheUserProfile` (also the security acceptance (a)) — it asks for "a spy `Sys` records every path it writes". No such seam exists: `Txn.WriteFile`/`MkdirAll` call `os.WriteFile`/`os.Lstat` directly (`txn.go:55-163`) and `System` (`system.go:176`) has no write recorder, so a blind model must invent one or write a test that cannot fail. Also, going through `LocalBackend.Run` includes the window's own later writes, so the folders are not empty at the end — Fix: reword the test: "drive `RunWorker` with the `pipeElevator` of `uiserver_test.go` directly (not `LocalBackend.Run`), set `Runner.BeforeCommit` to list the Desktop and Startup folders into a variable, and assert both listings are empty (and that the folders do not exist if they did not before) after the worker returns `OutcomeDone`". Give `TestWorkerLeavesAPlantedDesktopLinkAlone` the same shape for its worker half (planted bytes unchanged after `RunWorker`), and keep its window half on `LocalBackend.Run`.

## MINOR

[MINOR] WP-74j step 2 — the post-worker layout is `NewLayout(opts, b.R.Dirs)` built from the page's raw `Options.Location` on a fresh install, while the worker canonicalises it (`Validated`, `plan.go:70`); the Desktop link target should be the exact exe the worker installed — Fix: after `OutcomeDone`, set `l.AppExe = res.AppExe` and `l.InstallDir = filepath.Dir(res.AppExe)` when `res.AppExe != ""` (`Result.AppExe` is set from `p.Layout.AppExe`, `runner.go:184`) before `WriteProfileLinks`/`RemoveProfileLinks`.

[MINOR] WP-74i step 2 `refuseLinkedFolder` — "until the path equals `UserProfile` or leaves it" does not say whether the first `dir` is checked when it is already outside the profile or when `UserProfile` is empty (`insideFolder` returns false for empty); two models will do different things — Fix: (with the MAJOR fix above) state "check `dir` itself with `r.refuseLink(dir)` and return its error; nothing else".

[MINOR] WP-74i `linkTarget` — the 64 KiB limit is applied after `os.ReadFile` has read the whole file, so a huge planted `Unravis.lnk` is read into memory; WP-74b checks the size first — Fix: "`Lstat` first; not a regular file or size over 65536 -> error; then read through `readLinkFile`".

[MINOR] WP-74i Proof — "12 tests `PASS`" includes `TestRemoveProfileLinksDoesNotFollowASymlinkedLink`, which the packet says may skip without symlink rights — Fix: "12 tests `PASS` (the symlink test may show `SKIP`)".

[MINOR] WP-76a Proof — `Select-String ... "SID"` in `installer-and-releases.md` already matches today (1 existing match), so it proves nothing about this packet — Fix: use `-Pattern "UnravisApp\."` (at least one match) instead.

## Symbol spot-check (WP-74i/74j and neighbours)

All real: `Runner` struct (`runner.go:93`), `Dirs.UserProfile`/`User()`/`WithUser` (`layout.go`), `samePath`/`insideFolder` (`options.go:337,346`), `Runner.refuseLink(path string) error` (`fixes_b.go:32`), `ReadShortcutTarget(data []byte)` (`lnk.go`, becomes an alias in 74a), `WorkerPrepare(sid, profile) func(*WorkRequest, *Runner) error` (`proto.go:183`; the runner argument is the worker's copy `w`, so `r.NoProfileLinks = true` stays local), `LocalBackend.Run` (`uiserver.go:477`, the elevated branch is `NeedsAdminFor && !b.IsAdmin`; `res`, `onLog`, `b.R.Dirs`, `OutcomeLeftovers`, `Result.Left` all exist), `removeShortcuts` loop variable `l` and `leftf` format `"Shortcut %s (%v)"` (`uninstall.go:148-153`, matches the 74g Proof pattern `os\.Remove\(l\)`), `NewPlan` and the `p.Layout = NewLayout(p.Opts, r.Dirs)` anchor (`plan.go:99`), `closeApp` as 75b describes (`plan.go:160-184`), the single `shortcuts` step (`plan.go:625-644`). The setup UI shows `Result.Left` on done screens (`setup.js`), so appending a line without changing the outcome (74j) is visible.

## Size and blind-implementability

All packets S or M. WP-74j is the heaviest (two source files plus ten tests with a fake elevator): acceptable as one M because the production change is about 25 lines; if the implementer struggles, split the tests into `worker_profile_links_test.go` (first five) and a second file. No packet tells the implementer to read the spec or large files.
