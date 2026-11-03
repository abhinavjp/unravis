# WP-74b Start at sign-in shortcut logic
risk: high    size: M    wave: 2    model: sonnet
## Outcome
A new package `internal/autostart` turns "start at sign-in" on and off at run time by creating or removing one shortcut, `Unravis.lnk`, in the user's Startup folder. The shortcut is the single source of truth, and it counts as "on" only when it points at this installed `unravis.exe` with the argument `--background` (a planted or stale link that points elsewhere shows as off). It will only create a shortcut that points at an installed copy of `unravis.exe`, and it only replaces or removes a shortcut it can prove is its own (its target is this `exe`); a foreign `Unravis.lnk` is left alone and reported as `ErrNotOurs`. The temp file of the atomic write never sits in the Startup folder.

## Write scope
- `internal/autostart/autostart.go` (new)
- `internal/autostart/autostart_test.go` (new)

## Must not change
- Every file outside `internal/autostart`. The Startup folder path itself comes from WP-74c; here it is always passed in as a parameter.

## Changes
1. `autostart.go` (package `autostart`, no build tags so it tests on any OS; imports `internal/shelllink`):
   - `const LinkName = "Unravis.lnk"`, `const Arg = "--background"`, `var ErrNotInstalled = errors.New("autostart: this copy of Unravis is not installed")`, `var ErrNotOurs = errors.New("autostart: the Startup shortcut belongs to something else")`.
   - Seams (package vars, tests swap them with `t.Cleanup`): `var renameFile = os.Rename` and `var tempDir = func() string { ... }` returning `%LOCALAPPDATA%\Unravis` (`os.Getenv("LOCALAPPDATA")` joined with `Unravis`), or `os.TempDir()` when that variable is empty.
   - `func LinkPath(startupDir string) string` = `filepath.Join(startupDir, LinkName)`.
   - `func Enabled(startupDir, exe string) bool`: `Unravis.lnk` is a regular file of at most 64 KiB, `shelllink.ReadShortcutTarget` equals `exe` (case-insensitive compare of the cleaned paths) and `shelllink.ReadShortcutArgs` equals `Arg`. Any read or parse error gives false.
   - `func Allowed(exe string) bool`: `exe` is absolute, its base name is `unravis.exe` (case-insensitive), it exists as a regular file, and a regular file `setup-state.json` exists in the same folder (that is how an installed copy is told apart from a build in a dev folder).
   - `func Enable(startupDir, exe string) error`: `Allowed(exe)` false -> `ErrNotInstalled`; `startupDir` must be absolute and an existing directory. If `Unravis.lnk` already exists it must be provably ours by the same rule as `Disable` (a regular file of at most 64 KiB, not a link or directory, whose `shelllink.ReadShortcutTarget` equals `exe`); otherwise return `ErrNotOurs` and change nothing. Build a `shelllink.Shortcut` (Target `exe`, Args `Arg`, WorkDir the exe's folder, Description "Unravis", Icon `exe`). Write the bytes to a temp file made with `os.CreateTemp(tempDir(), "unravis-lnk-*.partial")` (the folder is made with `os.MkdirAll(..., 0o700)`), then `renameFile(temp, LinkPath(startupDir))`, which replaces the older `Unravis.lnk` of ours. If the rename fails (for example another volume), remove the temp file and write the bytes straight to `LinkPath(startupDir)` with `os.WriteFile`. The temp file is removed on every failure path.
   - `func Disable(startupDir, exe string) error`: no `Unravis.lnk` -> nil; the file exists and its target equals `exe` -> `os.Remove` of that one path; it exists but the target differs or cannot be read -> `ErrNotOurs` and the file stays. It never lists the directory and never removes anything else.
2. Tests in `autostart_test.go` (temp dirs; a fake installed folder with `unravis.exe` and `setup-state.json` made by the test; `tempDir` is pointed at a second temp dir):
   - `TestEnableCreatesTheShortcutWithBackgroundArg` (read back with `shelllink.ReadShortcutTarget` and `ReadShortcutArgs`).
   - `TestEnableReplacesAnOlderShortcutOfOurs` (an old link to this exe without args is replaced by one with `--background`).
   - `TestEnableRefusesAForeignShortcut` (a link to another program, garbage bytes and a directory named `Unravis.lnk` each give `ErrNotOurs`; the file or directory is unchanged and no temp file is left).
   - `TestEnableRefusesAnExeThatIsNotInstalled` (no `setup-state.json` -> `ErrNotInstalled`, nothing written).
   - `TestEnableRefusesRelativeAndWrongNamedExe` (table: relative path, `other.exe`, `unravis.exe` that does not exist).
   - `TestEnableWritesItsTempFileOutsideTheStartupFolder` (a fake `renameFile` records the source path: its folder is not `startupDir`; it then delegates to `os.Rename`).
   - `TestEnableRemovesItsTempFileWhenTheRenameFails` (fake rename error: the link still exists through the direct write and the temp folder is empty).
   - `TestEnabledNeedsTheShortcutToPointAtThisExeWithTheArg` (table: missing, ours, other target, no args, garbage bytes, 100 KiB file -> only "ours" is true).
   - `TestDisableRemovesOnlyTheUnravisShortcut` (a neighbour `Other.lnk` survives).
   - `TestDisableKeepsAShortcutThatPointsElsewhere` (`ErrNotOurs`, the file is still there).
   - `TestDisableWhenMissingIsNotAnError`.
   - `TestAllowedNeedsTheStateFile`.

## Proof
- `go` is `.dev\toolchains\go\bin\go.exe` from the repo root with the environment of WP-58 step 3.
- `go test ./internal/autostart -count=1 -v` -> 12 tests `PASS`.
- `go vet ./internal/autostart` -> no output; `$env:GOOS='linux'; go vet ./internal/autostart; Remove-Item Env:GOOS` -> no output.
- Security acceptance: (a) `TestEnableRefusesAnExeThatIsNotInstalled` and `TestEnableRefusesRelativeAndWrongNamedExe` pass, so the shortcut can only point at an installed `unravis.exe`; (b) `Select-String -Path internal/autostart/autostart.go -Pattern "os\.RemoveAll|filepath\.Glob|os\.ReadDir"` -> no output, so `Disable` can remove only the one named file; (c) `Select-String -Path internal/autostart/autostart.go -Pattern "os/exec"` -> no output; (d) the target and arguments are fixed by the code, not taken from a caller string other than the exe path that `Allowed` has just checked; (e) `TestEnableWritesItsTempFileOutsideTheStartupFolder` passes, so a crash cannot leave a launchable half file in the Startup folder.

## Depends on
WP-74a
