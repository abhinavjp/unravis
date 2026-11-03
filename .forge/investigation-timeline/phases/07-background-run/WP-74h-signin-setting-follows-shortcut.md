# WP-74h Upgrade and repair keep the sign-in setting the user chose
risk: medium    size: S    wave: 4    model: sonnet
## Outcome
The tray toggle can switch start at sign-in on or off between installs, so the saved installer option can go stale. On upgrade and repair the installer now takes the truth from the Startup folder: the setting is "on" exactly when `Unravis.lnk` is there and points at the installed `unravis.exe`, and that value is what gets saved and re-applied. A user who turned it off in the tray keeps it off after an upgrade; a link that points at another program counts as off and is not overwritten. When setup runs as the user, the plan reads the link itself. In the elevated worker (`Runner.NoProfileLinks`, WP-74i) the plan never reads the user's Startup folder: it takes the value the setup window or silent mode already derived and sent in the request (`DeriveStartAtSignIn`, WP-74i, called by WP-74j); that function already falls back to the saved value when the link cannot be read.

## Write scope
- `internal/setup/plan.go`
- `internal/setup/plan_signin_test.go` (new)

## Must not change
- Fresh installs (the installer checkbox, default ON, still decides), uninstall, and every other file.
- The helpers `linkTarget`, `linkIsOurs` and `readLinkFile` from WP-74i are used as they are.

## Changes
1. In `NewPlan` (`plan.go`), directly after `p.Layout = NewLayout(p.Opts, r.Dirs)`, for `OpUpgrade` and `OpRepair` only:
   - When `r.NoProfileLinks`: `p.Opts.StartAtSignIn = opts.StartAtSignIn` (`opts` is the request's options, whose value `DeriveStartAtSignIn` set from the Startup link or, on a read error, from the saved state); the Startup folder is not read.
   - Otherwise: `if _, _, err := linkTarget(p.Layout.StartupLink); err == nil { _, ours := linkIsOurs(p.Layout.StartupLink, p.Layout.AppExe); p.Opts.StartAtSignIn = ours }`. A read error keeps the saved value. The seam is the package var `readLinkFile` (WP-74i): tests use real temp folders and swap it only to force a read error.
2. `saveState` already writes `p.Opts`; confirm that the derived value is what it persists and add nothing if it is.
3. `plan_signin_test.go`: `TestUpgradeKeepsStartAtSignInOffWhenTheShortcutWasRemoved` (saved option on, no shortcut -> option off and no Startup link written), `TestUpgradeKeepsStartAtSignInOnWhenTheShortcutExists`, `TestUpgradeTreatsALinkToAnotherProgramAsOff` (the foreign link is not overwritten), `TestUpgradeKeepsTheSavedValueWhenTheLinkCannotBeRead` (`readLinkFile` returns a permission error), `TestRepairRecreatesNothingForAUserWhoTurnedItOff`, `TestFreshInstallStillUsesTheCheckbox`, `TestSavedStateRecordsTheDerivedValue`, `TestWorkerPlanDoesNotReadTheStartupLink` (`NoProfileLinks` true: `readLinkFile` is replaced by a function that fails the test when called, and the plan takes the request's `StartAtSignIn`).

## Proof
- `go` is `.dev\toolchains\go\bin\go.exe` from the repo root with the environment of WP-58 step 3.
- `go test ./internal/setup -run "TestUpgradeKeepsStartAtSignIn|TestUpgradeTreatsALink|TestUpgradeKeepsTheSavedValue|TestRepairRecreatesNothing|TestFreshInstallStillUsesTheCheckbox|TestSavedStateRecordsTheDerived|TestWorkerPlanDoesNotRead" -count=1 -v` -> 8 tests `PASS`.
- `go test ./internal/setup -count=1` -> `ok`; `go vet ./internal/setup` -> no output.

## Depends on
WP-74f, WP-74i
