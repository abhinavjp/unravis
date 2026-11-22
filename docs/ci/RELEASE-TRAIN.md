# Release train: develop, a fast gate per pull request, full CI once per version

Status: built, dormant until `develop` exists (see "Cutover"). Backlog: BL-462. Companion of the CI speed-up (BL-461).

## The idea in four lines

- Every pull request merges into one long-lived branch, `develop`, after a **fast gate** (`fast-gate.yml`, aim: under a minute or two, see the numbers below).
- The full CI (`ci.yml`: Windows tests, race, exe build, screenshots, docs, supply chain, desktop shell) runs on every push to `develop` (newest wins) and **once** on the pull request that promotes `develop` to `master`.
- When a version is ready, one script promotes `develop` (all of it, or up to a chosen commit) to `master`: `tools/release/Promote-Develop.ps1`.
- The release manager is unchanged. It cuts from `master` after the promotion.

## Numbers (plain minutes)

| | Before PR #123 (master runs, Nov 20 to 21) | After PR #123 (one pull request run) | Fast gate, per PR into develop |
|---|---|---|---|
| Per pull request, wall clock | about 11 to 17 min | about 6 min | **estimate: about 0.5 to 1 min for notes, small Go change about 1 to 1.5 min, web change about 2 to 3 min** |
| Runs per merged PR | full, each time | full, each time | fast, each time; full only on develop and on promotion |

The fast-gate row is an estimate until the first real pull request into `develop` has run. Runner start, checkout and cache restore alone probably cost 15 to 25 s (an estimate, not measured), so "under 30 s" is reachable only for notes-only or tiny changes; a typical Go change cannot honestly be 30 s. The measured numbers are written into this table when `develop` exists (checklist in BL-462).

## What the fast gate checks per pull request (and what it does not)

Checks (all required through the job `fast-build`): scope of the change (`tools/ci/fast-scope.mjs`: the changed packages plus every package that imports them, widened to everything when the path is unknown or `go.mod`, `go.sum`, `baseline-go-tests.txt` or `ci.yml` changed), gofmt of changed files, `go vet` on Linux and Windows cross-compile of those packages, their tests on Linux compared with the Linux failure baseline (the package graph is read for Linux and for Windows), `go mod tidy` when modules changed, the action-pin check, the self-tests of the CI tools; when `web/` or `docs/user/` changed: type check, tests, build and the committed-build check (BL-031). Tests that read files of another directory are covered by a short list in `fast-scope.mjs`: a change under `web/src/lib/copy/` also tests `internal/evidence` and `internal/server`; a change in `internal/setup` or `cmd/unravis-setup` also tests `cmd/audit-explorer`; a change under `internal/server`, `internal/blackbox`, `internal/calltrace` or `tools/e2e` also runs the web checks. A new test that reads across directories must be added to that list. golangci-lint runs on new code only and is advisory, as before.

**Not checked per pull request any more** (checked on the push to `develop` and on promotion): Windows tests, the race run, the exe build and its smoke, the screenshots, the docs check, govulncheck, osv, secret scan, the desktop shell, `desktop-sidecar`. Packages that do not import the changed ones are not tested per PR.

The trade, stated plainly: a Windows-only break, a race, a docs error or a vulnerable dependency can now merge into `develop` and show up in the push run on `develop` (red within the full-CI time, a few minutes) instead of on the pull request. It cannot reach `master`: the promotion pull request runs the same full `build` that every pull request ran before, and `master` keeps its protection. Nothing on `master` is weaker than today. What changed is where a break is found: in `develop`, minutes after the merge, not on the pull request.

## When develop goes red

1. The push run on `develop` fails. The merge sweep (or whoever merged last) sees it in the CI list on `develop`.
2. Fix forward with a new pull request into `develop` (the usual), or revert the bad commit by pull request. Both run the fast gate.
3. Do not promote while the newest push run on `develop` is red. `Promote-Develop.ps1 -UpTo <sha>` promotes up to an older commit, leaving the broken commit behind (the full run of an older commit may have been cancelled by a newer push: the promotion pull request runs the full CI on that older commit anyway, so it is checked there).
4. Two pull requests that each rebuilt `web/dist` can merge without a text conflict and leave `dist` stale. The push run on `develop` catches it (BL-031); the fix is a rebuild commit by pull request. For this reason `develop` does not require branches to be up to date (below).

## Promotion

```powershell
pwsh tools/release/Promote-Develop.ps1 -DryRun        # what would go, and the lock check
pwsh tools/release/Promote-Develop.ps1                # promote all of develop
pwsh tools/release/Promote-Develop.ps1 -UpTo 1a2b3c4  # promote up to a commit
```

It reads the release lock (never writes it; a failed read stops it, it never counts as free) and stops when a release holds it with merges on hold. It refuses when any commit to promote is not the merge of a pull request into `develop` (a direct push, an admin bypass); the release manager checks the same on master (BL-470, PR 130). It makes `promote/<sha7>` on GitHub, merges `master` into it when master moved, opens the pull request into `master`, waits for the `build` job of **that pull request's own `ci.yml` run** (not other checks on the same commit), re-reads the lock, and merges with a **merge commit** pinned to the checked head commit so every `develop` commit stays in `master` history. The release manager reads the merged pull request of each first-parent commit, so the promotion pull request is what it sees; the sensitive-paths list names that pull request, and release notes still list every commit (they use `git log`). After the merge, `ci.yml` runs on the new `master` commit (per-sha group, never cancelled) and the release manager waits for that run as before.

Only one promotion at a time: it is a merge to `master`, so the same release-lock rule applies as for any other pull request into `master`.

## Rules for every thread (copy to threads that act on pull requests)

- **All pull requests target `develop`**, not `master`. This includes the merge sweep, dependabot, docs and notes-only pull requests. Open with `--base develop`.
- The merge gate on `develop` is the job `fast-build` of the Fast gate. Merge on green as before; squash merge as before.
- **The release lock applies to merges into `master`**, that is, to promotions only. Merging into `develop` does not read the lock and never waits for a release (a release cuts from `master`).
- Nothing else merges into `master`. Hotfix: a pull request into `master` (full CI) and then merge `master` into `develop` by pull request, **with a merge commit, not a squash** (a squash would leave master out of develop's history and the next promotion would conflict on the hotfix again). That is why `develop` allows both merge types. Promotion merges `master` in automatically when `master` moved.
- Do not push to `develop` or `master` directly. Do not merge a promotion while the newest push run on `develop` is red.
- A failing `develop` push run is everyone's priority: fix forward or revert before more merges pile on.
- `docs/BACKLOG.md` items are ticked when the change reaches `master` and is verified on the real data path, not at the merge into `develop`.

## Cutover (needs nachi's go; I create nothing until then)

1. Merge the pull request that adds this train. It is dormant: `fast-gate.yml` triggers only on `develop`, and the `ci.yml` triggers still cover `master`.
2. Create `develop` from `master` (one command, or the GitHub branch dialog).
3. **Branch protection (nachi, in GitHub: Settings > Branches > Add branch ruleset, or classic rule), for `develop`:** require a pull request before merging; require status check `fast-build` (the Fast gate job; app GitHub Actions); require conversation resolution; block force pushes and deletion; **do not** require branches to be up to date (strict off), so merged pull requests do not each wait for a rebase; allow squash merge and merge commits (merge commits are for the master-into-develop sync and for promotion). Keep `master`'s rule exactly as it is (required `build`, strict, pull request required). The two gates have different names on purpose: `fast-build` on `develop`, `build` (the full `ci.yml`) on `master`. Also turn on **Allow merge commits** in Settings > General (promotion needs it), and set `develop` as the default branch only if you want new pull requests to open against it (optional).
4. Retarget open pull requests: `gh pr edit <n> --base develop` (the sweep thread does it).
5. A small pull request updates `CLAUDE.md` (rule line) and `dependabot.yml` (`target-branch: develop`).
6. First real pull request into `develop`: measure the fast gate and write the seconds into the table above; tick BL-462.

## Rollback

Stop using `develop`: set pull requests back to `master` (`gh pr edit --base master`). The `ci.yml` triggers still run on `master` pull requests, so nothing else to undo. Delete `develop` when empty.
