# Thread gate: what every thread does before a push, and what every reviewer checks

Why: `docs/ci/CI-FAILURE-ANALYSIS-2026-11.md` (277 failed runs in 4 weeks; `develop` green on 13% of pushes). The causes are few and repeat. This page is the part a thread can act on. It is short on purpose.

## 1. The pre-push gate (builder)

Before `git push`, from inside the clone:

```bash
bash tools/ci/prepush.sh
```

It works in Git Bash on the PC and in a Linux container. It repeats what GitHub will say about the pull request, in this order:

1. The branch contains the tip of `origin/develop` (the Fast gate judges the merge with develop).
2. No generated web files (`web/dist`, `cmd/unravis-setup/ui`, `cmd/unravis-lite/ui`) in the change.
3. The tests of the CI tools.
4. `gofmt` of the changed Go files, on the committed text.
5. The Windows-risk scan of the Go lines the change adds (`tools/ci/windows-risk.mjs`).
6. `go vet` for this OS, `GOOS=windows` and `GOOS=linux`; the `tauri_sidecar` build.
7. Go tests of the affected packages, compared with the baseline, like `fast-build`.
8. `go mod tidy` when `go.mod` or `go.sum` changed.
9. `golangci-lint --new-from-rev` (the pull request's lint job).
10. Web type check and tests when `web/` or a file the web tests read changed (needs `cd web && npm ci` once). It never runs `npm run build`: that rewrites the generated folders.

Options: `--skip-tests` (the slow step; only when the change is docs or tooling and the scope says Go is not affected anyway), `--skip-web`, `--no-fetch`, `--base <ref>`. Exit 0 means "ready to push".

Also run (not numbered above): the scope (`fast-scope.mjs`), the action-pin check, and `go test -tags tauri_sidecar .` like the Fast gate.

What it cannot do, so the rules below fill the gap:
- It does not run the web build (it rewrites the generated folders); the Fast gate does.
- It does not run the Windows test shards or the race job (they are in the full CI). Rules 3 and 4 below cover that.
- On the owner's PC, the baseline compare can show "new" failures that come from the PC (a full case folder, a running app). See the flaky policy; do not fix them in your change.

## 2. Rules

1. **Merge or rebase `develop` first, run the gate, push once.** Each push cancels the run in progress (261 cancelled CI runs, 114 cancelled Fast gate runs in 4 weeks) and a push that fixes only the last red one usually breaks the next check. Batch fixes: reproduce all failures locally, fix them together, run the gate, push.
2. **A red pull request is fixed from the log, not by guessing.** Read the failing step of the run, reproduce it with the command in the log, then fix. Do not re-run a failing job hoping for green (see the flaky policy).
3. **Windows is a target.** Tests run on a Windows PC and on Windows CI, not only on Linux. In a new or changed test: build paths with `t.TempDir()` and `filepath.Join`; do not check file modes (Windows says 0666) unless the test is a `_windows_test.go`; do not use `HOME`, `/tmp`, `sh`, `syscall.Kill`; compare text fixtures after normalising `\r\n`; do not use a wall-clock budget or a short sleep to prove an ordering (wait on a channel or a condition with a generous timeout). `windows-risk.mjs` flags these; a line that is fine ends with `// winok: <reason>`.
4. **Concurrency is a race until shown otherwise.** If the change adds a goroutine, lock, channel, timer or shared variable, run `go test -race` on those packages before the push (it needs a C compiler: `gcc` on the PC; Linux containers have one). Say in the pull request which packages you ran. Never put a real race in a baseline.
5. **Lint fixes are real fixes.** A `//nolint` needs the rule name and a reason (`//nolint:gosec // G304: path comes from t.TempDir()`). Test files: check the error of `Close`, `Remove`, `WriteFile` or say why not; use `0o600` and `0o750` unless the test is about modes.
6. **Never commit generated files.** If `git status` shows `web/dist/...`, restore them (the command is in the gate output). A build run in place rewrites them.
7. **Tests belong in the same change** as the code they cover, and the test name says what breaks, so the next reader of a red log knows.
8. **Evidence in the pull request.** The pull request body says which gate steps ran and the result (the template has the lines). If a step could not run, the body says which and why. A pull request that says "checked" with no evidence is sent back.

## 3. Flaky and PC-state tests: quarantine and fix policy

A test is **flaky** when it fails on one run and passes on the next without a code change; it is **PC-state** when it fails on the owner's PC and passes on GitHub (or the reverse) because of the machine (disk, a running app, a profile).

- **Do not "fix" a failure that is not yours inside your change.** Run the failing test three times on a clean `develop`. If it fails on `develop` too, it is not yours: leave it, name it in the pull request, and open or find the backlog item for it.
- **Do not re-run until green** and do not merge on a re-run you did not understand. A green after a red re-run is a flaky test, not a pass.
- **Quarantine = an entry in the baseline file with a backlog id and a reason** (`docs/desktop/baseline-go-tests.txt` for Linux, `.windows.txt` for Windows, `baseline-go-race.txt` for the race job). Allowed only for a test that is flaky or host-dependent, never for a real race or a real regression. The line names the backlog item; the item names an owner and a date.
- **A quarantined test is fixed within one release.** A thread that sees the same test fail in two unrelated pull requests opens the item (title: the test name) in `docs/BACKLOG.md` with the two run links. Make the test deterministic: inject a clock, wait on a condition, use `t.TempDir()`, remove the host dependency. Never raise a timeout as the fix.
- The analysis lists the first suspects: `TestMaskLongNamesAreLinear`, `TestLockWaitHonoursContext`, `TestEventsTimeBudgetAndCancel` (casestore), the leakcheck ticker test, the CLI broker tests, `TestBucketCountsReadOneSnapshot`.

## 4. Reviewer checklist (fresh reviewer, one pass)

Check these first; they are where the 277 failures came from. Read the pull request body for the gate evidence, then the diff:

- [ ] Branch is current with `develop` (the diff shows only this change; no unrelated files).
- [ ] No generated files (`web/dist`, `cmd/unravis-setup/ui`, `cmd/unravis-lite/ui`).
- [ ] Every new or changed test would pass on **Windows**: paths, modes, `HOME`/`/tmp`, shell programs, CRLF fixtures, drive letters, sleeps and wall-clock budgets. Run `node tools/ci/windows-risk.mjs` yourself and read each line it prints.
- [ ] New goroutines, locks, channels, timers or shared state: a `-race` run is reported, and every goroutine has a stop and a wait (no leak, no test that leaves one running).
- [ ] Lint-prone code: unchecked errors in tests, `ReadFile` or `WriteFile` with a variable path, loose modes, `//nolint` without rule and reason, integer conversions (G115).
- [ ] The change has a test that fails without the fix. For a bug: the test name says the bug.
- [ ] Nothing was added to a baseline or a known-failure file without a backlog id and a reason. A real race or regression is never baselined.
- [ ] No timing test: no `time.Sleep` to order events, no `time.Since` budget under about 1 s.
- [ ] `go.mod` and `go.sum` changed together, and tidy.
- [ ] The pull request targets `develop`, and the pull request body has the gate evidence.

Findings are labelled MUST (red on CI or breaks Windows) or SHOULD. A reviewer does not re-run the whole gate; one `windows-risk.mjs` run and the evidence lines are enough.

## 5. Block to paste into a thread's instructions

```text
CI gate (docs/ci/THREAD-GATE.md): before every push merge origin/develop into the branch, run `bash tools/ci/prepush.sh`, fix everything it reports in one batch, and push once. Never commit web/dist, cmd/unravis-setup/ui or cmd/unravis-lite/ui. Tests must pass on Windows (t.TempDir and filepath.Join, no mode checks, no HOME or /tmp or sh, no sleeps or wall-clock budgets, normalise CRLF); add `// winok: <reason>` to a line that is fine. Concurrency changes: run go test -race on those packages. Do not re-run a red job hoping for green and do not fix a failure that fails on develop too: name it in the PR and open a backlog item. Fill in the PR template with the gate evidence.
```

Project-memory line: "Before any push: merge develop, run `bash tools/ci/prepush.sh`, push once; tests must be Windows-safe; flaky tests are quarantined with a backlog id (docs/ci/THREAD-GATE.md)."

## 6. What the gate costs
A change to a package that many others import (for example `internal/clock`) tests every dependent package: about 7 minutes on the owner's PC in the dry run. A change to one leaf package takes seconds to a minute. That is the price of one red run on GitHub, which is 10 to 20 minutes of CI plus a fix push that cancels the next run.
