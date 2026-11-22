# BL-031 gate proof (stale web build must fail)

A gate that never fails is not proven. This is the procedure that shows `web/scripts/check-dist.cjs` really stops a stale build. Run A once now that the step is blocking in `ci.yml`, and B once for `release.yml`.

## A. ci.yml (pull request or branch push)

1. Branch from master: `git switch -c claude/bl-031-stale-proof origin/master`.
2. Change one string literal in a file under `web/src` (for example a visible label in `web/src/lib/drill/drillCopy.ts`). Not a comment: a comment may not change the built output.
3. Do NOT run `npm run build`. Commit and push the branch only.
4. Expected: job `build` fails at the step "Web - built files match what is committed", with the message telling you to run `npm run build` in `web` and commit the three folders (`web/dist`, `cmd/unravis-setup/ui`, `cmd/unravis-lite/ui`). Later steps do not run.
5. Record the run link in the BL-031 line of `docs/BACKLOG.md`.
6. Close the pull request and delete the branch. Never merge it.

Local rehearsal (no push): make the same edit, run `cd web && npm run build`, restore the three built folders with `git checkout -- web/dist cmd/unravis-setup/ui cmd/unravis-lite/ui` (so source is new and dist is old), then run `node scripts/check-dist.cjs`. It must exit non-zero and list the changed files.

## B. release.yml (not testable on a pull request)

The release workflow runs on a tag push or `workflow_dispatch`, never on a pull request.

1. On the scratch branch from A (stale dist committed), push a pre-release tag such as `v0.0.0-gateproof.1` on that commit, or run the workflow by hand against it. Only with the owner's go.
2. Expected: job `build` fails at the same step, before "Build the programs and the installer", before signing and before any asset is uploaded. No draft release is created.
3. Delete the tag and the branch afterwards (`git push origin :refs/tags/<tag>`).

Until B has been run, the blocking release step is NOT verified on a real release run. Do not tick BL-031 on a merge alone.

## C. What a pass looks like

- A fails as described, and the same branch with a rebuilt dist (`npm run build`, commit the three folders) goes green.
- B fails at the dist step on the stale commit.
