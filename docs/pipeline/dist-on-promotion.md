# Generated web files are committed once per promotion

Status: new (2026-11-22, backlog BL-491). Owner request: pull requests no longer commit rebuilt web files, because they conflicted on nearly every merge and every conflict costs a CI run (GitHub Free has a limited quota).

## The three folders

`npm run build` in `web` writes three folders. They are committed, and the Go program embeds them (`go:embed`):

- `web/dist` (the main app, `assets.go`)
- `cmd/unravis-setup/ui` (the setup program)
- `cmd/unravis-lite/ui` (Unravis Lite)

## The old rule and the new rule

Old: every pull request that changed `web/src` (or the other inputs) had to commit the rebuilt folders, and the CI step `check-dist` failed the pull request when they did not match a fresh build byte for byte. Two pull requests that both rebuilt the folders conflicted.

New:

- A pull request into `develop` changes the source only (`web/src`, `web/index.html`, `docs/user/*.md`, and so on). It does not touch the three folders.
- `develop` keeps the folders from the last promotion. They are old compared with the source, and that is fine: `go build` and the Go tests still work, because the embedded files exist.
- When `Promote-Develop.ps1` promotes `develop` to `master`, it rebuilds the folders once and adds one commit to the promote branch.
- `check-dist` is a release gate. It runs on the promotion pull request, on every push to `master`, and in `release.yml` (the tag). It does not run on pull requests into `develop` or on pushes to `develop`.

## The flow, step by step

1. Pull requests merge into `develop` after the fast gate. The fast gate type checks, tests and builds the web (so a broken web build is found early) and runs a guard that fails a pull request that changes the three folders.
2. Someone runs `pwsh tools/release/Promote-Develop.ps1`. It needs **Node 22.23.5**, git and npm on that PC (the same Node version CI uses). A dry run (`-DryRun`) checks this first.
3. The script makes the branch `promote/<sha7>` on GitHub and merges `master` into it when master moved (as before).
4. The script runs `tools/release/promote-dist.mjs`. It clones the promote branch into a temporary folder (your own checkout is never touched), runs `npm ci` and `npm run build` in `web`, and runs `web/scripts/check-dist.cjs`.
   - If the committed files already equal the build: nothing is committed.
   - If they differ: it commits only the three folders, with the subject `Rebuild web files for promotion <sha7>`.
5. It builds a second time on the same files and runs `check-dist` again. If the bytes moved, the build is not reproducible here: it stops and pushes nothing.
6. It pushes the commit to the promote branch (a plain push, never a force). Only then does it open the promotion pull request, so the full CI starts once, on the final head.
7. The full CI on the promotion pull request runs `check-dist` on a Windows runner (`ci.yml`). Green means the committed files equal a fresh build there too. The script merges as before.
8. After the merge, master holds the rebuilt files. `develop` does not get that commit back. The next promotion merges `master` into its branch first (no conflict: only master changed those files), then rebuilds on top.
9. The release (`Release.ps1`, then `release.yml` on the tag) runs `check-dist` again and stops before building or signing if the files are stale.

The release manager checks that every develop commit in a promotion is a pull request merged into `develop`. It accepts the one generated commit as an exception, and only when its subject is the fixed one and it changes nothing outside the three folders (`Get-PromotionSideCommits` in `tools/release/ReleaseLib.ps1`). That the bytes are right is proved by `check-dist`, not by the release manager.

## What else in CI changed

- `ci.yml`, `web` job: the `check-dist` step is skipped on pushes to `develop`.
- `ci.yml`, `package` job: on pushes to `develop` the exe is built with the web build included (it used `-SkipWeb`), so the develop run tests the current source and not the last promoted files. On master and on the promotion it still uses the committed files.
- `ui-audit.yml`: builds the web files from source before the audit, because the audit's demo exe embeds them.
- `fast-gate.yml`: no `check-dist`; new guard step; self-test `tools/ci/test-promote-dist.mjs`.
- `release.yml`: unchanged. `check-dist` stays blocking there.

Tests that run on a pull request use the last promoted files inside the Go program. A Go test that needs a new string from a changed `web/src` file would not see it before the next promotion. No such test exists today (the web tests read the source). If you write one, read the source file, not the embedded copy.

## Failure cases

**A pull request author commits the three folders anyway.** The guard step in the fast gate fails with the file list and the fix: `git checkout origin/develop -- web/dist cmd/unravis-setup/ui cmd/unravis-lite/ui`, commit, push. This also applies to open pull requests that were made under the old rule. Branches named `sync/*` (the merge of a hotfix from `master` back into `develop`) are exempt, because that merge carries master's rebuilt files on purpose. If the guard did not exist, the promotion would hit a merge conflict when it merges `master` into the promote branch (both sides changed the files). The script stops with the existing message in that case; the fix is a pull request into `develop` that restores the folders.

**The author forgets to build or test the web.** The fast gate runs type check, tests and build whenever `web/` changes, so a broken web build fails the pull request. Only the comparison with committed files is gone.

**The build is not reproducible on the promoting PC.** The second build in `promote-dist.mjs` differs from the commit, the script says so and stops. Nothing is pushed and no pull request is opened. Check the Node version (`node --version` must be `v22.23.5`), a leftover `web/node_modules` from another Node, or a changed dependency. Run `npm ci` fresh. The branch is reused by the next run.

**The build is reproducible on the PC but differs on the CI runner.** The promotion pull request fails at `Web - built files match what is committed` and lists the files. Do not merge it. Compare a Windows build against the commit (`node web/scripts/check-dist.cjs` after `npm run build`), look for line endings (`.gitattributes`) or a lock file that changed. Fix the cause by pull request into `develop` and run the promotion again. A promotion made from a Windows PC should match the Windows runner; the same Node and `npm ci` are required either way.

**Node is missing or the wrong version, or git, npm or the GitHub token is missing.** `Promote-Develop.ps1` stops before it creates anything (dry run too), with the version it found.

**The push of the generated commit fails** (no rights, network). The script stops; the promote branch exists without the commit; run again, it reuses the branch and builds again.

**The pull request into `master` is already open when the generated commit is pushed** (a second run on the same branch). The new head starts a new CI run, which is the correct one to wait for. This is the only case that costs a second full run.

**A hotfix goes straight into `master`.** A hotfix pull request into `master` that changes the web inputs must run `npm run build` in `web` and commit the three folders itself; `ci.yml` runs `check-dist` on it. Then merge `master` into `develop` by a pull request from a `sync/...` branch, with a merge commit.

**Somebody squashes the promotion pull request.** Not allowed (see `docs/ci/RELEASE-TRAIN.md`): the generated commit and every develop commit must stay in master's history.

## Quota note

The point of the change is fewer wasted runs: no conflict rebuilds, no repeated pushes only to refresh generated files, and the comparison with committed files runs once per promotion on the promotion pull request instead of on every web pull request. The rebuild itself runs on the promoting PC, not on GitHub.

## Files

- `tools/release/promote-dist.mjs` and its test `tools/ci/test-promote-dist.mjs` (also checks that the Node version and the commit subject agree with the workflows and `ReleaseLib.ps1`)
- `tools/release/Promote-Develop.ps1` (preflight and the call), `tools/release/ReleaseLib.ps1` (release manager rule), `tools/release/Test-ReleaseManager.ps1`
- `web/scripts/check-dist.cjs`
- `.github/workflows/fast-gate.yml`, `ci.yml`, `ui-audit.yml`, `release.yml`
