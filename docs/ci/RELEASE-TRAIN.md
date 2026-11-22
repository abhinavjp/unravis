# Release train: develop, a fast gate per pull request, full CI once per version

Status: live since 2026-11-22. `develop` exists and is the default branch (new pull requests open against it), its protection is set (required `fast-build`, strict off, merge commits allowed; conversation resolution not required). Backlog: BL-462. Companion of the CI speed-up (BL-461).

## The idea in four lines

- Every pull request merges into one long-lived branch, `develop`, after a **fast gate** (`fast-gate.yml`, aim: under a minute or two, see the numbers below).
- The full CI (`ci.yml`: Windows tests, race, exe build, screenshots, docs) runs on every push to `develop` (newest wins) and **once** on the pull request that promotes `develop` to `master`. The two heavy side workflows, `supply-chain.yml` and `desktop-windows.yml`, run only on that promotion pull request and on the push to `master` (not on every push to `develop`; "Run workflow" starts them by hand on `develop`). Why: the repository is public, so runner minutes are free, but a Free plan allows 20 jobs at once; on 2026-11-22 those two workflows plus `ci.yml` filled the queue and made the fast gate wait about 4 minutes.
- When a version is ready, one script promotes `develop` (all of it, or up to a chosen commit) to `master`: `tools/release/Promote-Develop.ps1`.
- The release manager is unchanged. It cuts from `master` after the promotion.

## Numbers (plain minutes)

| | Before PR #123 (master runs, Nov 20 to 21) | After PR #123 (one pull request run) | Fast gate, per PR into develop |
|---|---|---|---|
| Per pull request, wall clock | about 11 to 17 min | about 6 min | **measured once (PR 134, notes and config only, 2026-11-22): 51 s from the first job start to a green `fast-build` (go 25 s, web 12 s, gate 5 s), after about 4 min waiting for a free runner while the queue was full. Estimates for the rest: small Go change about 1 to 1.5 min, web change about 2 to 3 min** |
| Runs per merged PR | full, each time | full, each time | fast, each time; full only on develop and on promotion |

Measured gate times so far (2026-11-22, GitHub-hosted runners): a script and docs change took 28 s of Go and 10 s of web work (gate 3 s); a notes and config change 51 s of work; a Go plus web change on a busy runner queue took 3 min 12 s for Go (scoped tests), 1 min 51 s for web and 2 min 8 s for lint (advisory), gate 4 s. A second run of the same kind took 3 min 58 s for Go, 6 s for web (cache hit), 1 min 58 s for lint. Waiting for a free runner added up to about 4 min on a busy queue and is not in these figures. A full CI run (dispatch on the branch, race job included) took 9 min wall clock; the race job alone about 5 min, in parallel with the rest. Runner start, checkout and cache restore alone probably cost 15 to 25 s (an estimate, not measured), so "under 30 s" is reachable only for notes-only or tiny changes; a typical Go change cannot honestly be 30 s. The measured numbers are written into this table when `develop` exists (checklist in BL-462).

## What the fast gate checks per pull request (and what it does not)

Checks (all required through the job `fast-build`): scope of the change (`tools/ci/fast-scope.mjs`: the changed packages plus every package that imports them, widened to everything when the path is unknown or `go.mod`, `go.sum`, `baseline-go-tests.txt` or `ci.yml` changed), gofmt of changed files, `go vet` on Linux and Windows cross-compile of those packages, their tests on Linux compared with the Linux failure baseline (the package graph is read for Linux and for Windows), `go mod tidy` when modules changed, the action-pin check, the self-tests of the CI tools; when `web/`, `docs/user/` or a file the web tests read changed (the list is `tools/ci/web-scope.mjs`, checked against the tests by `tools/ci/test-web-scope.mjs`): type check, tests and build (the committed-build check, BL-031, is not run here any more: pull requests do not commit the generated web files, see `docs/pipeline/dist-on-promotion.md`); for every pull request, a guard that fails one that changes `web/dist`, `cmd/unravis-setup/ui` or `cmd/unravis-lite/ui` (a back-merge of master that leaves those folders identical to master's is allowed). Tests that read files of another directory are covered by a short list in `fast-scope.mjs`: a change under `web/src/lib/copy/` also tests `internal/evidence` and `internal/server`; a change in `internal/setup` or `cmd/unravis-setup` also tests `cmd/audit-explorer`; a change under `internal/server`, `internal/blackbox`, `internal/calltrace` or `tools/e2e` also runs the web checks. A new test that reads across directories must be added to that list. golangci-lint runs on new code only and is advisory, as before.

**Not checked per pull request any more** (checked on the push to `develop` for Windows tests, the race run, the exe build and its smoke, the screenshots and the docs check; on promotion only for govulncheck, osv, secret scan, the desktop shell and `desktop-sidecar`): all of these. Packages that do not import the changed ones are not tested per PR.

The trade, stated plainly: a Windows-only break, a race, a docs error or a vulnerable dependency can now merge into `develop` and show up in the push run on `develop` (red within the full-CI time, a few minutes) instead of on the pull request. It cannot reach `master`: the promotion pull request runs the same full `build` that every pull request ran before, and `master` keeps its protection. Nothing on `master` is weaker than today. What changed is where a break is found: in `develop`, minutes after the merge, not on the pull request.

## When develop goes red

1. The push run on `develop` fails. The merge sweep (or whoever merged last) sees it in the CI list on `develop`.
2. Fix forward with a new pull request into `develop` (the usual), or revert the bad commit by pull request. Both run the fast gate.
3. Do not promote while the newest push run on `develop` is red. `Promote-Develop.ps1 -UpTo <sha>` promotes up to an older commit, leaving the broken commit behind (the full run of an older commit may have been cancelled by a newer push: the promotion pull request runs the full CI on that older commit anyway, so it is checked there).
4. The generated web files (`web/dist` and the two `ui` folders) are not rebuilt by pull requests into `develop`: develop holds the last promoted ones, `go:embed` keeps compiling, and the promotion rebuilds them once (`docs/pipeline/dist-on-promotion.md`). Nothing to fix on develop for stale generated files.

## Promotion

```powershell
pwsh tools/release/Promote-Develop.ps1 -DryRun        # what would go, and the lock check
pwsh tools/release/Promote-Develop.ps1                # promote all of develop
pwsh tools/release/Promote-Develop.ps1 -UpTo 1a2b3c4  # promote up to a commit
```

Before it opens the pull request it rebuilds the generated web files on this PC (Node 22.23.5, a temporary clone of the promote branch, `tools/release/promote-dist.mjs`) and pushes one commit "Rebuild web files for promotion ..." when they differ, so the full CI run starts once on the final head and its `check-dist` proves the files equal a fresh build. It needs Node 22.23.5, git and npm on the PC that promotes. The release manager accepts those commits on the develop side of a promotion (subject fixed, only the three generated folders touched, at the tip end of the branch); a re-run of the script reuses the branch and its generated commit; anything else that is not a merged pull request is still refused.

It reads the release lock (never writes it; a failed read stops it, it never counts as free) and stops when a release holds it with merges on hold. It refuses when a first-parent commit of `develop` (a pull request merge; merges of master into develop and merges inside a pull request branch are not judged) is not the merge of a pull request into `develop` (a direct push, an admin bypass); the release manager checks the same on master (BL-470, PR 130). It makes `promote/<sha7>` on GitHub, merges `master` into it when master moved, opens the pull request into `master`, waits for the `build` job of **that pull request's own `ci.yml` run** (not other checks on the same commit), re-reads the lock, and merges with a **merge commit** pinned to the checked head commit so every `develop` commit stays in `master` history. The release manager reads the merged pull request of each first-parent commit, so the promotion pull request is what it sees; the sensitive-paths list names that pull request, and release notes still list every commit (they use `git log`). After the merge, `ci.yml` runs on the new `master` commit (per-sha group, never cancelled) and the release manager waits for that run as before.

Only one promotion at a time: it is a merge to `master`, so the same release-lock rule applies as for any other pull request into `master`.

## Rules for every thread (copy to threads that act on pull requests)

- **All pull requests target `develop`**, not `master`. This includes the merge sweep, dependabot, docs and notes-only pull requests. Open with `--base develop`.
- The merge gate on `develop` is the job `fast-build` of the Fast gate. Merge on green as before; squash merge as before.
- **The release lock applies to merges into `master`**, that is, to promotions only. Merging into `develop` does not read the lock and never waits for a release (a release cuts from `master`).
- Nothing else merges into `master`. Hotfix: a pull request into `master` (full CI) and then merge `master` into `develop` by pull request, **with a merge commit, not a squash** (a squash would leave master out of develop's history and the next promotion would conflict on the hotfix again). That is why `develop` allows both merge types. Promotion merges `master` in automatically when `master` moved.
- Do not push to `develop` or `master` directly. Do not merge a promotion while the newest push run on `develop` is red.
- A failing `develop` push run is everyone's priority: fix forward or revert before more merges pile on.
- `docs/BACKLOG.md` items are ticked when the change reaches `master` and is verified on the real data path, not at the merge into `develop`.

## Cutover (all steps done 2026-11-22 except the measured seconds, step 6)

1. Merge the pull request that adds this train. It is dormant: `fast-gate.yml` triggers only on `develop`, and the `ci.yml` triggers still cover `master`.
2. Create `develop` from `master` (one command, or the GitHub branch dialog).
3. **Branch protection (nachi, in GitHub: Settings > Branches > Add branch ruleset, or classic rule), for `develop`:** require a pull request before merging; require status check `fast-build` (the Fast gate job; app GitHub Actions); require conversation resolution; block force pushes and deletion; **do not** require branches to be up to date (strict off), so merged pull requests do not each wait for a rebase; allow squash merge and merge commits (merge commits are for the master-into-develop sync and for promotion). Keep `master`'s rule exactly as it is (required `build`, strict, pull request required). The two gates have different names on purpose: `fast-build` on `develop`, `build` (the full `ci.yml`) on `master`. Also turn on **Allow merge commits** in Settings > General (promotion needs it), and set `develop` as the default branch only if you want new pull requests to open against it (optional).
4. Retarget open pull requests: `gh pr edit <n> --base develop`, then close and reopen each one (a retarget alone does not start the fast gate). Done for all open pull requests.
5. A small pull request updates `CLAUDE.md` (rule line) and `dependabot.yml` (`target-branch: develop`).
6. First real pull requests into `develop`: measured (table above). First promotion merged 2026-11-22 (PR 143, 20 commits, master 2f12bf5) with a green full `build`. BL-462 is ticked only when master's own run on that commit is green too.
7. Race job: blocking part of `build` since this change (fails on any `WARNING: DATA RACE` or on any test failure not in `docs/desktop/baseline-go-tests.txt` or `docs/desktop/baseline-go-race.txt`). A dispatch run on the branch reported 0 races and 0 new failures, so the race-only baseline starts empty. A test that is only too slow under `-race` goes into that file; a real race is fixed, never baselined.

## Rollback

Stop using `develop`: set pull requests back to `master` (`gh pr edit --base master`). Changing the base does not start `ci.yml` by itself (a retarget is an `edited` event, which `ci.yml` does not listen to): push a commit to each retargeted pull request, or close and reopen it, so the full `build` runs and master's required check can pass. The `ci.yml` triggers still cover `master` pull requests, so nothing else to undo. Delete `develop` when empty.

A promotion pull request must be merged with a merge commit (`Promote-Develop.ps1` does). Nothing stops a person from squashing it on the GitHub button: master would then hold the changes without develop's commits, and the next promotion would conflict. Do not squash a `promote/*` pull request.

## develop is the default branch

- Pull requests open against `develop` without `--base`. Scheduled workflows (`mirror-feed.yml`, `ui-audit.yml`) and a manual "Run workflow" use the workflow file on `develop`, not on `master`.
- Releases are unaffected: `Release.ps1` names `master` explicitly (`-Branch master`, `origin/master`), `Promote-Develop.ps1` names `--base master` and `develop` explicitly, `release.yml` runs on the pushed tag, and the lock lives on its own branch `release-lock`.
- Closing keywords ("Closes #123") act when a pull request merges into the default branch, so they fire at the merge into `develop`.
