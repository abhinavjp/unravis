# CI audit (BL-440, static part) and BL-441 findings

Date 2026-11-21. Method: read `.github/workflows/*`, and ran the Linux gates in a Linux container (Go 1.27.3, root, no pwsh, no network access to the repo's Actions history). Nothing here comes from a live GitHub run.

## What was read from the repo

- `ci.yml`: jobs `build` (windows-2022, 45 min) and `powershell-syntax` (windows-2022, 10 min). `build` runs `go vet`, a gofmt check on changed files, `go test -p 2 -timeout 25m ./...`, web check/test/build, screenshots. `permissions: contents: read`, concurrency cancel, actions pinned by full SHA (no unpinned `uses:` in any workflow).
- Other workflows: `release.yml`, `verify-release.yml` (windows-2022), `mirror-feed.yml` (ubuntu-24.04). No scheduled lint, no Dependabot, no Linux job before BL-441.
- No `.golangci.yml`, no `.github/dependabot.yml`, no `docs/adr` before BL-441.

## Not done (needs the live repo)

- Repo visibility, billable minutes of the last 20 runs, cache sizes, branch protection: `gh repo view` is blocked from this session (GraphQL not available). `tools/ci/Get-CiBaseline.ps1` (spec 08) is not written; BL-440 stays open.

## Findings that change the plan

1. **The Linux failure count is not 9.** `go test ./...` on Linux fails 382 tests plus two crashing packages (see `docs/desktop/baseline-go-tests.txt`). By package: `internal/blackbox` 176, `internal/setup` 136, `cmd/unravis-blackbox` 33, `cmd/unravis-setup` 11, `internal/autostart` 10, `internal/server` 9 (these are the 9 the plan counted), `internal/casestore/sqlite` 4, `internal/shelllink` 1, plus `pkg.internal/server` and `pkg.internal/setup` (a test panics, so the later tests of that package never run on Linux; their real count is unknown). The plan and specs that say "9" or "19" should cite the file instead. Causes seen: Windows paths (`D:\...`), shortcuts, services, free-disk floor.
2. **`go vet ./...` and `go build ./...` failed on Linux** because `cmd/unravis-blackbox` used `blackbox.AnswerFileFlag`, declared only in a `_windows.go` file. Fixed in this change (the const moved to untagged `internal/blackbox/elevate_guard.go`; `GOOS=windows go build ./...` and `go vet` of both packages still pass).
3. **Two tests hang on Linux until the timeout**: `TestRunHostKeepsRecordingWhenThePortIsBusyAndTakesItLater`, `TestRunHostStopWhileWaitingForAPortReturns` (`internal/blackbox`). A hang costs the whole 25 min. The Linux job skips exactly these two with `-skip`; they are not in the baseline because they do not run.
4. **Under `-race`, 4 more tests fail** (in the baseline, since the CI job runs with `-race`): two real data races in test code, `internal/store` `TestActivityQueriesAllSourcesOnceWithOverlapAndNoPayload` and `TestSlowestReadsEverySourceOnceAndRanksGlobally` (shared state from `profiles_test.go:40`); and two time-budget tests that exceed their budget under the race detector (`internal/casestore` `TestSeriesThreeMillionEventsGiveExactBucketStatistics`, `internal/importer/schedule` `TestSecondCronRowsHitWorkBudget`, 2m15s). A plain `go test` passes these four. The two data races look like real bugs worth a separate item.
5. **The race run takes about 9 min** on this container (plain run: about 1.5 min). The job timeout is 45 min.
6. **golangci-lint v2.15.0 (latest) released binaries are built with Go 1.26** and refuse this module (`go 1.27.3`): "the Go language version used to build golangci-lint is lower than the targeted Go version". So the job does `go install ...golangci-lint/v2/cmd/golangci-lint@v2.15.0` with the Go from `go.mod` instead of the action. Verified locally that this build loads `.golangci.yml` and flags a deliberate `os.Remove("x")` in `internal/server` as errcheck.
7. A whole-repo `golangci-lint run` was clean (0 issues on new code) after the fix in 2; before it, one typecheck error in `cmd/unravis-blackbox` stopped the other linters.

## Deviations from spec 08 (BL-441)

- `Compare-GoFailures.ps1` and its test are Node scripts (`tools/ci/compare-go-failures.mjs`, `tools/ci/test-compare.mjs`) because there is no pwsh here to run a script against; the Node ones were run. Other specs name the .ps1; rename or add a wrapper when pwsh is at hand.
- `pkg.<package>` entries may be in the baseline (spec: always new). Two packages panic on Linux today; without this the gate would be red from day one.
- `Test-PathFilters.ps1` not added: it checks `desktop-windows.yml`, which does not exist until BL-442.
- `all-green` is not part of BL-441 (spec lists it only in the workflow-name section); not added.
- Go 1.27.4 bump (D16): not done; no way to check `go.dev/dl` for 1.27.4 from here and the 1.27.3 toolchain is what ran.
- npm `tools/e2e` has no lockfile; Dependabot still reads its `package.json`.
