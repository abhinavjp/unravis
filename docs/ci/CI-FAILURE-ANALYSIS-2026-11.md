# Why so many CI runs fail, and how a thread stops it (analysis of 29 Oct to 26 Nov 2026)

Question from nachi (26 Nov): why are there so many failed CI results, and how can a thread prevent them while it builds and reviews, so every pull request is bulletproof?

This is a count, not a guess. Sources: every GitHub Actions run since 29 Oct (2,157 runs; the 277 failed ones were downloaded and their logs classified), the 324 pull requests of the same period, and the 116 runs in the Homestretch store on the owner's PC (`%LOCALAPPDATA%\Unravis\ci\store\runs`). Real density starts about 13 Nov (release train and local CI); 21 to 25 Nov is the peak.

## The short answer

Most red is not "bad code reaches review". Four things make it:

1. **Windows-only test failures that the Linux fast gate cannot see.** They merge green, then turn `develop` red for days.
2. **The race-detector job**, which only runs on the full CI after the merge.
3. **Lint** on the pull request (advisory, but red).
4. **Tests that fail only on the PC, or only sometimes** (host state, timing), which no one can tell from a real regression without re-running.

Merge conflicts and review-conversation blockers are **not** a cause (see the end).

## Numbers

| Workflow | Success | Cancelled | Failure |
|---|---|---|---|
| Fast gate (per PR) | 481 | 114 | 92 |
| CI (full) | 407 | 261 | 147 |
| Supply chain | 153 | 22 | 7 |
| UI audit | 55 | 38 | 7 |
| desktop-windows | 118 | 27 | 3 |
| Nightly e2e | 22 | 18 | 15 |
| Release | 31 | 0 | 4 |

- 277 failed runs: 178 on pull requests, 95 on pushes, 2 dispatch, 2 dependabot.
- Full CI failures: 79 on pushes to `develop`, 56 on pull requests, 12 on pushes to `master`. All 92 Fast gate failures are on pull requests.
- **`develop` push runs: 79 failed, 86 cancelled, 21 passed (13% green).** `develop` was red almost without a break from 22 to 25 Nov; the first green was about 25 Nov 21:00 UTC.
- Fast gate per branch (158 branches): 121 passed the first time they ran to the end, 37 failed first. 111 branches never failed; 29 failed once, 8 twice, 6 three times, 1 four, 2 five, 1 fifteen times (a lint loop).
- Pull requests: 291 merged, 28 closed, 5 open. Only 3 of the last 60 closed ones were closed unmerged.
- Cancelled runs (261 CI, 114 Fast gate, 86 on `develop`) come mostly from `cancel-in-progress` when a branch is pushed again before the last run ends. 15 runs were re-attempts.

## Causes, ranked

### 1. Windows-only failures, seen only after the merge
The Fast gate runs on Linux. The Windows job (`go-tests`, 3 shards) belongs to `ci.yml`, which runs on a push to `develop`. So a test that passes on Linux and fails on Windows is first seen **after** it merged, and then every pull request that merges after it inherits a red `develop`.

Tests that failed this way (number of failed runs): `TestInstallDir`, `TestAppProgramOnDisk`, `TestInstallDirRule`, `TestDoctorWithTheAppRunning` (about 40 each); `TestLegacyOptionsAnywhere` (44); `TestSQLLog*` and `TestReadErrorLog*` (31); `TestLockWaitHonoursContext` (25); file-mode tests (about 23); `TestGoldenFixture` and `TestGoldenDts` (22, CRLF in the fixture); `TestSetProcessInformationReal` (20); `TestWindowsReadDeadline`; `TestMaskLongNamesAreLinear` (timing, 12). The `desktop-sidecar` "Pipe security tests" step failed in 30 of 30 local runs.

Typical shapes: a Unix path literal, a file-mode check (Windows reports 0666 or 0777), `os.Chmod` as "read only", `HOME` instead of `USERPROFILE`, a fixture compared byte for byte, a drive letter, a short sleep or a wall-clock budget.

### 2. Race detector
About 41 failing job rows; at least 15 runs failed on this job alone; 13 logs report "N data race(s)". It runs only in the full CI, so a data race merges. 30 of 30 failing local CI runs had it red.

### 3. Fast gate lint (advisory, red)
62 runs on about 31 branches. Rules hit: staticcheck 31, gosec G304 20, errcheck 17, G115 11, nolintlint 10, G703 8. Typical: an unchecked `Close` in a test, `ReadFile` or `WriteFile` with a variable path or loose mode in a test, `//nolint` without a reason, QF1001. One branch looped 15 times on lint alone.

### 4. Go tests of the affected packages, compared with the baseline
About 17 runs on GitHub and 17 locally. New failures were a mix of real regressions and flakes: `TestBucketCountsReadOneSnapshot`, the releasedash route test, setup uninstall tests, `TestEmbeddedDemoHTTPWorkflow`, `setup.TestInstallPlacesCLIAtBin`, `unravis-ci` tests, `localci.TestCorePurity`, the leakcheck ticker, the casestore memory-contract timing, the CLI broker tests. Several cluster at 25 Nov 13:51 to 14:20, which is load or timing.

On the owner's PC there is a second source: the Linux baseline is compared on a Windows host with its own state (a full case disk, a running app, a modified profile), so setup, mcpsrv modes, cmdtree, autostart and pipesec tests show as NEW although the change did not touch them. PR #293 added a Windows baseline; host state is still not solved (a dry run of the new gate on 26 Nov showed 20 "new" failures from a change to a one-file package: "case cap exceeded: free disk space", "server never listened").

### 5. gofmt of changed files
About 18 runs (Fast gate 7, CI 4 to 6, others) and 3 local. Always avoidable by running `gofmt -l` on the files of the change.

### 6. The rest
- Web tests: about 27 runs, 43 mentions of the "keep ranges are the server ranges in config.go" test (a Go constant that the web test reads).
- Web built files differ from the committed ones: 3 CI runs. "Generated web files are not part of this PR": 1 Fast gate run, 1 local. `go mod tidy`: 2 Fast gate, 2 CI.
- Nightly e2e compare: 15 runs, mostly 23 Nov. UI audit 7. Supply chain 7 (osv lockfile 3, trufflehog "BASE and HEAD the same" on promote PRs 2, npm policy 2, cargo-deny 1). desktop-windows 3. Release `Go - tests` 4. Dependabot cargo path 2. Release manager self-test 3. Local `_checkout` 2.

### 7. Not causes
Merge conflicts and review blockers: branch protection on `develop` requires only `fast-build`, strict is off, there is no conversation rule and no required approvals, and 3 of the last 60 closed pull requests were unmerged. Runner bugs show only as the local-store `_checkout` failures (2) and PR #324's workspace fix, already merged.

## What already exists (do not duplicate)
PR #293 (Windows baseline), #318 and #319 (flaky status and race-grant tests), #292 (shadow-status determinism), #289 (Fast gate manual only, draft), #324 (per-job workspace). The factory thread owns runner capacity; the local-CI thread owns Homestretch.

## What this change adds
| Cause | Fix in this change |
|---|---|
| 1 Windows-only | `tools/ci/windows-risk.mjs` (report on the Go lines a change adds; nine rules, each from a real failure; `// winok: <reason>` to opt a line out) and a cross-`go vet` for Windows and Linux in the pre-push gate. Run in the Fast gate's always-on step as a self-test; as a report in `prepush.sh`. |
| 2 Race | Rule in `THREAD-GATE.md`: run `go test -race` on the packages you touched when you touch goroutines, locks, channels or shared state (it needs a C compiler; a thread without one says so in the PR). |
| 3 Lint | `prepush.sh` runs `golangci-lint --new-from-rev` like the PR's lint job. |
| 4 Baseline compare | `prepush.sh` runs the same compare as the Fast gate; the flaky policy says what to do with a failure that is not yours. |
| 5 gofmt | Same check as the Fast gate, on the committed text. |
| 6 Generated files, tidy, web | Same guard as the Fast gate (before the push, not after); `go mod tidy` check; web type check and tests when web changed. |
| Cancelled runs | Rule: merge or rebase develop before the push, run the gate, then push once. |
| All | `.github/PULL_REQUEST_TEMPLATE.md` (checklist), reviewer checklist, thread-instruction block in `docs/ci/THREAD-GATE.md`. |

## Proposed for other threads (not done here)
1. **Local-CI (Homestretch):** run the Windows `go-tests` shard for the pull request, not only after the merge to `develop`. This is the one change that would remove cause 1 completely; the windows-risk scan only narrows it. Also give Homestretch a clean-profile host (empty case folder, no app running) so "NEW" failures mean what they say.
2. **Factory:** keep `fast-build` required; add the quarantine list below to the Fast gate compare only after the local-CI thread agrees on the file format.
3. **Flaky quarantine list** (`docs/ci/flaky-tests.txt`) read by `compare-go-failures.mjs`: not built here, because the two-PC test that nachi named did not appear in the logs under that name, and a quarantine file without a reader would only be a second baseline. The policy in `THREAD-GATE.md` works with the existing baseline files until then.

Backlog: BL-590 to BL-594 in `docs/BACKLOG.md`.
