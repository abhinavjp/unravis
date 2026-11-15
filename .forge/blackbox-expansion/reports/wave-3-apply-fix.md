# W3-01 fix: Apply now persists the new settings to the installed config

Date: 2026-11-15. Branch: feat/blackbox-expansion. Scope: only
`internal/blackbox/service.go`, `internal/blackbox/service_test.go`,
`internal/blackbox/thispc.go`, `internal/blackbox/thispc_test.go`,
`cmd/unravis-blackbox/main.go`, `cmd/unravis-blackbox/main_test.go`.
No commit/push. No live service/SCM operations, no ACL/config changes on this
machine. All tests use the existing fake SCM/env/elevator seams and real
temporary files.

## Confirmed trace (before the fix)

1. Web submits the whole config; `internal/server/blackbox.go` (`bbApply`,
   ~line 1100) validates and forwards it unchanged to `ThisPC.Apply`.
2. `ThisPC.do` (thispc.go ~213-231) marshals cfg into
   `%TEMP%\unravis-bb-*\blackbox.json` and `defer os.RemoveAll(dir)` deletes it
   after the helper call. `HelperArgs` passes `apply --json --config <temp>`.
3. `cmd/unravis-blackbox/main.go` (~line 254) honors the explicit file and
   calls `Manager.Apply(paths.ConfigPath)`.
4. `Manager.Apply` (service.go, old lines 472-508) only `LoadConfig`s that file
   and stops/starts the service. No save anywhere.
5. Install registers a FIXED config path: `filepath.Join(progDir,
   ConfigFileName)` in the service args (old service.go:307,336), and the
   service reloads exactly that file at startup
   (`cmd/unravis-blackbox/service_windows.go:64-75`).
6. Result: removing target A and selecting B restarted the recorder with the
   OLD installed list; the temp file was gone. A stopped recorder returned
   success with no save at all (old `case SvcStopped: return res, nil`).

## What changed

- `installedPaths(progDir)` (service.go) is now the single source of truth for
  the installed config/key paths. Install uses it (replacing its inline
  `Paths{...}` literal) and Apply derives the save target from it via
  `m.Env.ProgramDir()` — the same way installation does. No second hardcoded
  path.
- `Manager.Apply` now: admin check → `LoadConfig` (validate; invalid refused
  before anything is saved or stopped) → service state check (`not-installed`
  refused) → `SaveConfig(installed, cfg, true)` (atomic temp+rename, keeps
  `blackbox.json.bak` of the previous settings) → only then stop/start. A
  stopped recorder keeps the saved settings for its next start and returns
  `Restarted=false` honestly.
- New fixed reasons and error type, following the existing
  `InstallError`/`SnapshotError` pattern: `ApplyError{Reason, Message, Cause}`
  with `ReasonSaveFailed = "save-failed"` and
  `ReasonRestartFailed = "restart-failed"`. Messages are fixed plain sentences;
  the raw OS error stays in `Cause` (for logs), never in the machine answer.
  `answer.fail` in main.go maps `*ApplyError` to its reason; `FailureWords`
  (thispc.go) gained plain what/next words for both reasons, so the existing
  server/UI failure path (`bbPCFailure` → `FailureWords`) works unchanged.
- Helper protocol UNCHANGED: still `apply --json --config <temp>`. No new flag
  was needed because the helper derives the installed path itself from
  `Env.ProgramDir()`, exactly as install does. Old and new helper binaries
  parse the same arguments; the temp file is only the carrier of the new
  settings. (An old helper binary still has the old no-save behavior — the fix
  ships with the helper executable, as with any helper change.)
- main.go usage text for `apply` corrected; the existing success texts
  ("Settings saved. The Blackbox is stopped: they apply at its next start." /
  "Settings applied. The Blackbox restarted.") are now truthful.

## Rollback / error semantics

- Invalid source config → refused before any save or stop (`invalid-config`
  via `ErrInvalidConfig`, unchanged).
- Save fails (including `ProgramDir` failure) → nothing is stopped, previous
  installed settings untouched, reason `save-failed`.
- Restart fails after a successful save (stop error, stop timeout, start
  error, start timeout) → the Install undo pattern is reused: `undoList` +
  `fileMemory(installed)` + `fileMemory(installed + ".bak")`, run on error, so
  the previous config and its previous backup state are put back byte for
  byte. Reason `restart-failed`, `Restarted=false`. The machine is left as it
  was; the distinct reason says the recorder did not restart and the previous
  settings were put back. A test observes the installed file at the moment
  Start is attempted to prove save-before-restart ordering.
- Success (running) → installed file holds the new settings, `.bak` holds the
  previous ones, service restarted, `Restarted=true`.
- Success (stopped) → installed file holds the new settings, no start/stop
  calls, `Restarted=false`; next start loads them.

## Tests added (existing seams: fakeSvc/fakeEnv/svcRig, fakeElev, memSvc/memEnv/rig)

service_test.go:
- `TestApplySavesTheNewTargetsToTheInstalledConfig` — install A, apply a
  different temp file selecting B; installed file holds B not A; service args
  still name the installed file (never the temp path); next-start config
  (loaded from the registered `--config` path) is B; `.bak` holds A; no
  re-register; stop/start counts.
- `TestApplyOnAStoppedRecorderSavesForTheNextStart` — stopped: saved, no
  start/stop, `Restarted=false`, next start loads B.
- `TestApplySaveFailureStopsNothingAndKeepsTheOldSettings` — a folder placed
  at `blackbox.json.bak` makes the save fail: `save-failed`, nothing stopped,
  old settings kept, message carries no path.
- `TestApplyRestartFailurePutsThePreviousSettingsBack` — start refused /
  never-runs subtests: `restart-failed`, save observed before the start
  attempt, previous settings restored, no leftover `.bak`, fixed message.
- `TestApplyRefusesAnInvalidSourceBeforeSavingOrStopping`.

thispc_test.go:
- `helperElev` — fake elevator that runs the REAL Manager with the args ThisPC
  builds (fake SCM/env, real temp files), answering in the helper JSON
  protocol.
- `TestApplyThroughTheHelperSavesTheInstalledConfigNotTheTemporaryFile` —
  running + stopped subtests: install A via `ThisPC.Install`, apply B via
  `ThisPC.Apply`; installed config holds B not A; the temp `--config` file is
  deleted afterwards, differs from the installed path, and is never in the
  service args.
- `TestApplySaveFailureThroughTheHelperKeepsTheRecorderRunning` — fixed
  `save-failed` reason end to end, recorder still running, old settings kept.
- `TestApplyFailureReasonsComeThroughInPlainWords` — both new reasons have
  `FailureWords`.

main_test.go (memSvc now records the registered spec and can fail Start):
- `TestApplyFromATemporaryFileSavesTheInstalledCopy` — CLI install A, apply
  `--config <different temp file>` B: installed copy holds B not A, service
  args name the installed copy, next `run` (same loader as the service) sees B.
- `TestApplyWhileStoppedSavesForTheNextStart` — `"restarted":false`, saved,
  next start/run uses B.
- `TestApplySaveFailureIsAFixedReasonAndStopsNothing` — JSON reason
  `save-failed`, still running, old settings, no path in the message.
- `TestApplyRestartFailurePutsThePreviousSettingsBack` — JSON reason
  `restart-failed`, previous settings restored.

## Exact commands and results

Go: `C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/.dev/toolchains/go/bin/go.exe`
(go1.27.3 windows/amd64), added to PATH. Run from the repo root.

- `go build ./...` → OK (no output).
- `go vet ./internal/blackbox/ ./cmd/unravis-blackbox/` → OK.
- `GOOS=linux go vet ./internal/blackbox/ ./cmd/unravis-blackbox/` → OK.
- `go test ./internal/blackbox/ -count=1 -run 'Apply|Install|ThisPC|Service|Helper|Scan' -timeout 120s`
  → `ok api-audit-explorer/internal/blackbox 4.326s` (all PASS, including every
  new test above; verbose run confirmed each).
- `go test ./cmd/unravis-blackbox/ -count=1 -timeout 120s`
  → `ok api-audit-explorer/cmd/unravis-blackbox 3.510s` (full package suite).

Notes: concurrent workers editing resolve*/host/groups files briefly broke the
package build twice mid-run (`r.inventory undefined`, `gate.view undefined` —
not my files); verification was re-run once their edits settled and everything
above passed on the final consistent tree. The whole-repo suite was NOT run
(per instruction). gofmt: all six files gofmt-clean (verified on LF copies;
`gofmt -l` flags every CRLF file in this repo, including untouched ones).
Working-tree line endings preserved as CRLF (core.autocrlf=true); this report
is a new file with LF.

## What still needs a real elevated install to prove

- That the elevated helper, running as SYSTEM/admin on a real machine, writes
  `%ProgramFiles%\Unravis Blackbox\blackbox.json` (ACL-locked folder) and the
  SCM restart then loads the new targets — the fake SCM cannot prove real
  SCM/ACL behavior.
- Real stop/start timing (30 s / 15 s waits) against the actual service, and
  that a real restart failure (e.g. port taken by another program after the
  settings change) rolls back and reports `restart-failed` in the UI.
- End-to-end UI evidence: apply B in the wizard, reload settings, and see B
  collected in recordings (owner-authorized live run; no live operations were
  performed here).
- The `.bak` recovery path on a real locked folder (backup write under the
  program-folder ACL).