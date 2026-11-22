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

1. Pull requests merge into `develop` after the fast gate. The fast gate type checks, tests and builds the web (so a broken web build is found early) and runs a guard that fails a pull request that changes the three folders. The one exception is a back-merge of master into develop: it passes when, after the merge, the three folders are byte for byte master's (the guard fetches master and compares). The rule is about content, so no branch name or author gets a pull request through with other files in those folders.
2. Someone runs `pwsh tools/release/Promote-Develop.ps1`. It needs **Node 22.23.5**, git and npm on that PC (the same Node version CI uses). A dry run (`-DryRun`) checks this first.
3. The script makes the branch `promote/<sha7>` on GitHub and merges `master` into it when master moved (as before).
4. The script runs `tools/release/promote-dist.mjs`. It clones the promote branch into a temporary folder (your own checkout is never touched), runs `npm ci` and `npm run build` in `web`, and runs `web/scripts/check-dist.cjs`.
   - If the committed files already equal the build: nothing is committed.
   - If they differ: it commits only the three folders, with the subject `Rebuild web files for promotion <sha7>`.
5. It deletes `web/node_modules`, runs `npm ci` again, builds a second time and runs `check-dist` again. If the bytes moved, the build is not stable here: it stops and pushes nothing. This is a same-PC, clean-install check; the proof across machines is `check-dist` on the Windows runner (step 7).
6. It pushes the commit to the promote branch (a plain push, never a force). `Promote-Develop.ps1` reads the script's last line (`DIST_COMMIT=none` or `DIST_COMMIT=<sha>`) and stops if it is missing, so a rebuild that silently did nothing cannot lead to a promotion pull request. Only then does it open the promotion pull request, so the full CI starts once, on the final head.
   - A run that already has the generated commit on the branch (a re-run after `-NoMerge`, a slow or failed CI, or a refused merge) is accepted by the branch check: the commit is recognised by its fixed subject, a single parent, and only files under the three folders. The re-run builds again and commits nothing new if the files are equal. The branch is not deleted and the open pull request is kept.
7. The full CI on the promotion pull request runs `check-dist` on a Windows runner (`ci.yml`). Green means the committed files equal a fresh build there too. The script merges as before.
8. After the merge, master holds the rebuilt files. `develop` does not get that commit back. The next promotion merges `master` into its branch first (no conflict: only master changed those files), then rebuilds on top.
9. The release (`Release.ps1`, then `release.yml` on the tag) runs `check-dist` again and stops before building or signing if the files are stale.

The release manager checks that every develop commit in a promotion is a pull request merged into `develop`. It accepts generated commits as an exception, and only when the subject is the fixed one, the commit changes nothing outside the three folders (compared case sensitive, so `UI` is not `ui`), and it sits at the tip end of the promote branch (only other generated commits and clean merges of master may come after it; any other commit above it, a squash commit or a `Merge pull request` merge commit of develop, ends the tip end, so a generated-looking commit under a develop pull request is refused, whichever way that pull request was merged). It names each accepted one in its log. The rule is `Get-PromotionSideCommits` in `tools/release/ReleaseLib.ps1`. That the bytes are right is proved by `check-dist`, not by the release manager. It cannot tell the promotion's generated commit from a direct push to develop of the same shape; branch protection stops that push, and `check-dist` still proves the bytes.

## What else in CI changed

- `ci.yml`, `web` job: the `check-dist` step is skipped on pushes to `develop`.
- `ci.yml`, `package` job: unchanged (`-SkipWeb`). On develop the exe and the screenshots therefore come from the last promoted files; the screenshot compare only warns until `docs/user/screenshots/RUNNER-BASELINES.txt` exists. No second web build runs per develop push (it would cost Windows minutes). A "Regenerate screenshot baselines" run on `develop` is refused for the same reason (it would photograph the old UI); run it on a `promote/*` branch or on `master`. If that marker file is ever committed, the develop run must start building the web again (build it once in the `web` job and upload it, rather than building twice).
- `ui-audit.yml`: builds the web files from source before the audit, because the audit's demo exe embeds them.
- `fast-gate.yml`: no `check-dist`; new guard step; self-test `tools/ci/test-promote-dist.mjs`.
- `release.yml`: unchanged. `check-dist` stays blocking there.

Tests that run on a pull request use the last promoted files inside the Go program. A Go test that needs a new string from a changed `web/src` file would not see it before the next promotion. No such test exists today: `internal/server/secfix_ack_test.go` and `internal/evidence/wording_test.go` read `web/src`, and the tests that use `explorer.Assets()` assert only generic structure. The rule is in `CLAUDE.md`: never assert new UI text against the embedded copy; read the source.

## Failure cases

**A pull request author commits the three folders anyway.** The guard step in the fast gate fails with the file list and the fix: `git restore --source=origin/develop --staged --worktree -- web/dist cmd/unravis-setup/ui cmd/unravis-lite/ui`, then `git clean -fd -- web/dist cmd/unravis-setup/ui cmd/unravis-lite/ui` for files git did not track yet (look at `git status` first), commit, push. Use `git restore`, not `git checkout origin/develop -- ...`: a checkout only brings back files that exist in develop and leaves the new hashed files your build added, so the guard would fail again. The guard matches the folder names without regard to letter case (a Windows checkout would put `cmd/unravis-setup/UI/x.js` inside the embedded folder). This also applies to open pull requests that were made under the old rule. A back-merge of master into develop (a hotfix, or the fix for the conflict below) is the one allowed change: the guard fetches master and passes it only when the three folders end up identical to master's (`git restore --source=origin/master --staged --worktree -- web/dist cmd/unravis-setup/ui cmd/unravis-lite/ui` after the merge; it also removes develop's stale hashed files that master does not have; names and letter case must match master's exactly). Without the guard, the promotion would hit a merge conflict when it merges `master` into the promote branch, because both sides changed the hashed asset files.

**The promotion hits a merge conflict when master is merged into the promote branch** (master changed the generated files, for example a hotfix, and develop changed them too). The script stops with a message. Open a back-merge pull request from master into develop with a merge commit, resolve the three folders by taking master's files (the guard accepts exactly that), merge it, run the script again. If the promote branch already holds a generated commit and master's hotfix changed those files after it, delete the `promote/<sha7>` branch on GitHub first (this closes its pull request; the next run starts clean). 

**The author forgets to build or test the web.** The fast gate runs type check, tests and build whenever `web/` changes, so a broken web build fails the pull request. Only the comparison with committed files is gone.

**The build is not stable on the promoting PC.** The second build (after deleting `web/node_modules` and running `npm ci` again) differs from the commit; the script says so and stops. Nothing is pushed and no pull request is opened. Check the Node version (`node --version` must be `v22.23.5`), a dependency that changed, or a build step that writes a timestamp or random id. The branch is reused by the next run. This catches instability on one PC. A different OS or toolchain is caught only by `check-dist` on the Windows runner (next case).

**The build is reproducible on the PC but differs on the CI runner.** The promotion pull request fails at `Web - built files match what is committed` and lists the files. Do not merge it. Compare a Windows build against the commit (`node web/scripts/check-dist.cjs` after `npm run build`), look for line endings (`.gitattributes`) or a lock file that changed. Fix the cause by pull request into `develop` and run the promotion again. A promotion made from a Windows PC should match the Windows runner; the same Node and `npm ci` are required either way.

**Node is missing or the wrong version, or git, npm or the GitHub token is missing.** `Promote-Develop.ps1` stops before it creates anything (dry run too), with the version it found.

**Dependency code runs during the web build on the promoting PC.** `npm ci` and `npm run build` run in their own clone (`build`), in a scrubbed environment: only listed variables, a private empty HOME and gh config folder, no git credential helper, and a `gh` that only fails. Registry and certificate settings (`npm_config_registry`, `npm_config_cafile`, `HTTPS_PROXY`, `NODE_EXTRA_CA_CERTS`, `SSL_CERT_FILE`) are passed on when they carry no password; `~/.npmrc` is not read. Only the three generated folders are then copied (plain files only; a link or a `.git` entry stops the run) into a second clone (`repo`) that build code never touched, and every git command that carries the token (clone, commit, push) runs there with hooks and `core.fsmonitor` off. This lowers the risk; it is not a security boundary: code running as the same user can still read the parent process's environment (`/proc/<pid>/environ`, `ReadProcessMemory`), call a program by its absolute path, or ask the OS credential store. The lock file pins the dependencies and check-dist compares the bytes. `tools/ci/test-promote-dist.mjs` proves the environment and the planted-hook case.

**The push of the generated commit fails** (no rights, network). The script stops; the promote branch exists without the commit; run again, it reuses the branch and builds again.

**The pull request into `master` is already open when a new generated commit is pushed** (a second run after master moved or the source changed). The new head starts a new CI run, which is the correct one to wait for. A re-run with nothing to change pushes nothing and costs no run.

**The rebuild step does nothing.** The caller stops: it requires the final `DIST_COMMIT=...` line. The script compares real paths when it decides whether it was started directly, so a path through a symlink, junction or `subst` drive still runs (a test starts it through a symlink).

**A hotfix goes straight into `master`.** A hotfix pull request into `master` that changes the web inputs must run `npm run build` in `web` and commit the three folders itself; `ci.yml` runs `check-dist` on it. Then merge `master` into `develop` by a pull request with a merge commit (the guard accepts it because the three folders end up equal to master's).

**Somebody squashes the promotion pull request.** Not allowed (see `docs/ci/RELEASE-TRAIN.md`): the generated commit and every develop commit must stay in master's history.

## Quota note

The point of the change is fewer wasted runs: no conflict rebuilds, no repeated pushes only to refresh generated files, and the comparison with committed files runs once per promotion on the promotion pull request instead of on every web pull request. The rebuild itself runs on the promoting PC, not on GitHub. There is no extra build on GitHub per develop push. Honest size: the check-dist step removed from the fast gate was a few seconds of Linux, so the saving is mostly the reruns that conflicts caused, not a per-push saving. Also: `ci.yml` treats every push to `develop` as code (the notes-only filter applies to pull requests only), so a docs-only merge still runs the full CI once.

## Files

- `tools/release/promote-dist.mjs` and its test `tools/ci/test-promote-dist.mjs` (also checks that the Node version and the commit subject agree with the workflows and `ReleaseLib.ps1`)
- `tools/release/Promote-Develop.ps1` (preflight and the call), `tools/release/ReleaseLib.ps1` (release manager rule), `tools/release/Test-ReleaseManager.ps1`
- `web/scripts/check-dist.cjs`
- `.github/workflows/fast-gate.yml`, `ci.yml`, `ui-audit.yml`, `release.yml`
