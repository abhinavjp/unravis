# Cutover runbook: from GitHub's gate to the local gate (BL-586)

Slice 7 of the local pipeline. Spec: `docs/ci/LOCAL-PIPELINE.md`, section 7 (on PR #277; it is not on develop yet). Releases stay on GitHub: `release.yml`, `verify-release.yml`, `Release.ps1` and the release lock are not touched by any step here.

**Nothing in this runbook has been done yet.** It was written on 2026-11-25. Lines marked "checked" were read from the real repository `abhinavjp/unravis` that day. Everything else is a plan.

## The short version

1. Phase 0 (now): the agent runs next to GitHub and posts `local/*` names that nothing requires. Count 20 agreeing pull requests in a row.
2. Phase 1: merge step 1 (adds the manual "Run workflow"), try the fallback once, switch the agent to the real name `fast-build`, then merge step 2 (GitHub stops starting the fast gate). Needs no change in GitHub settings.
3. Phase 2 (optional): nachi changes one setting on `master`. Read "Phase 2" first: it may not be worth doing.
4. Phase 3: clean up.

Rollback is always the same shape: revert the workflow PR (GitHub gates again at once), and for phase 2 put the setting back.

## What is required where (checked 2026-11-25, `gh api repos/abhinavjp/unravis/branches/<b>/protection`)

| | `develop` | `master` |
|---|---|---|
| Required check | `fast-build` | `build` |
| Source of that check | Any source (`app_id` null) | GitHub Actions (`app_id` 15368) |
| Up to date with the base before merging (`strict`) | no | yes |
| Admins bypass (`enforce_admins`) | no, so admins can bypass | no, so admins can bypass |
| Approving reviews | 0 | 0 |
| Conversation resolution | not required | required |
| Rulesets (`/rulesets`, `/rules/branches/master`) | none | none |

The default branch is `develop`. The one thing nachi has to change, and only in phase 2: on `master`, the source of `build` from **GitHub Actions** to **Any source**. Today it is `app_id` 15368.

Which workflow gates what (from the code, `cmd/unravis-ci/serve.go` and `status_cmds.go`):

- Pull request into `develop`: `fast-gate.yml`, name `fast-build`.
- Pull request into `master` (a promotion): `ci.yml`, name `build`.
- Push to `develop`: `fast-gate.yml` and `ci.yml`. Push to `master`: `ci.yml`.

## Who posts which name

| Phase | GitHub Actions posts | The agent (`unravis-ci serve`) posts |
|---|---|---|
| 0 Shadow | `fast-build`, `build` | `local/fast-build`, `local/build`, and one `local/<job>` per job. Nothing requires these. |
| 1 Develop | `fast-build` only until step 2 is merged; `build` always | `fast-build` (real, required). `build` stays `local/build`. Start: `serve --post-required --context ci.yml=local/build`. |
| 2 Master | `build` still (see Phase 2) | `build` too: `serve --post-required` without the `--context` part. |
| 3 Clean-up | `build` on master, releases | same as phase 2 |

Without `--post-required` the program refuses to post any name that does not start with `local/` (the "shadow guard"). `--post-required` alone posts BOTH real names, so in phase 1 always add `--context ci.yml=local/build`.

Not known, and to be tried on a throwaway draft pull request before relying on it: what GitHub does when a check run from GitHub Actions and a commit status from the agent both exist under the same required name. It happens for a short time in phase 1 (between "agent posts `fast-build`" and "step 2 merged"), and for good in phase 2.

## Before you start: read-only pre-flight

Nothing here writes. Run it from a clone of the branch that has `unravis-ci` (`go build -o unravis-ci.exe ./cmd/unravis-ci`). Replace `<N>` with an open pull request number.

```powershell
gh auth status                                              # logged in as a user that can read the repository
.\unravis-ci.exe version
.\unravis-ci.exe status                                     # agents: at least one, last seen under 30 s ago; queue 0 or small
.\unravis-ci.exe claims --repo abhinavjp/unravis            # PCs and claims (read only without --clean)
.\unravis-ci.exe compare --repo abhinavjp/unravis --limit 40 --save   # "pull requests in a row that agree: N of 20 needed"
gh api repos/abhinavjp/unravis/branches/develop/protection --jq '.required_status_checks.checks'
gh api repos/abhinavjp/unravis/branches/master/protection  --jq '.required_status_checks.checks'
gh api repos/abhinavjp/unravis/rulesets                     # expect []
Test-Path "$env:LOCALAPPDATA\Unravis\ci\store\PAUSE"        # expect False (True = the agent is paused on purpose)
.\unravis-ci.exe lint --workflows .github/workflows         # release.yml and verify-release.yml say "not supported": expected, they stay on GitHub
.\unravis-ci.exe fallback <N> --repo abhinavjp/unravis --dry-run   # starts nothing
go test -count=1 ./internal/localci ./cmd/unravis-ci -run "TestTrustedTable|TestTrustedBase|TestRunPRStrangerAndForkNeverRun|TestRunPRNonOwner|TestFallback"
```

Checked 2026-11-25 on the real repository and this PC: the two protection reads match the table above; rulesets are `[]`; `status` says "agents: none" and the store folder `%LOCALAPPDATA%\Unravis\ci\store` does not exist yet, so `compare` has nothing to read (streak 0 of 20); `fallback 284 --dry-run` is refused with "fast-gate.yml ... has no workflow_dispatch trigger" (this is the reason for step 1); the trust tests pass.

## Go / no-go

Every row must be "go" before the phase it guards. Measure it with the command, do not trust memory.

| # | Gate | How to measure | Go when | Today |
|---|---|---|---|---|
| G1 | 20 agreeing pull requests in a row | `compare --limit 40 --save`, last line | `20 of 20 needed` or more (it prints "enough") | 0, no data |
| G2 | No "disagree" unexplained | same output: `N disagree`, and the `disagree` rows | 0 disagree in the window, or each one has a written reason in `docs/ci/` | no data |
| G3 | Agent saw every pull request | same output: `github-only` count for the last 20 pull requests | 0 (a `github-only` row does not break the streak, so check it yourself) | no data |
| G4 | Agent alive | `status` | an agent is listed and was seen under 30 s ago (it beats every 10 s) | none running |
| G5 | Trust refusals tested | the `go test` line above, on the commit the agent is built from | all pass (fork and stranger never run; a non-owner only into a trusted base) | pass |
| G6 | Runner accepts a gate workflow without `pull_request`/`push` | `.\unravis-ci.exe run <step-2 fast-gate.yml> --event pull_request --dry-run` | exit code 0 | exit 2, "does not run on pull_request" (blocker R below) |
| G7 | Fallback tried once on a real pull request | after step 1 is on develop: `unravis-ci fallback <N>`, then `gh run list --repo abhinavjp/unravis --workflow fast-gate.yml --event workflow_dispatch --limit 3` and `gh pr checks <N>` | the run is green and the PR shows `fast-build` pass on its head | not possible before step 1 (refused) |
| G8 | Real name posts, on a throwaway draft pull request into develop | agent started with `--post-required --context ci.yml=local/build`; `gh api repos/abhinavjp/unravis/commits/<sha>/statuses --jq '.[]\|{context,state}'` | `fast-build` from the agent goes pending then success, `build` is not posted, and the pull request can be merged | never run |
| G9 | No release running | the release app shows none; the agent log shows no release pause | no release, no hold | not checked |

G1 to G3 are only valid in phase 0. After step 2 GitHub no longer posts `fast-build`, so `compare` would compare the agent with itself.

## Phase 0: shadow (now)

1. Build `unravis-ci.exe` from the cutover branch and start the agent: `unravis-ci serve --repo abhinavjp/unravis`. Without `--post-required` it can only post `local/*` names. Keep the PC awake while the queue is not empty (the agent does this itself).
2. Open pull requests as usual. GitHub still gates. For each head the agent posts `local/fast-build` (into develop) or `local/build` (into master).
3. Once a day: `unravis-ci compare --repo abhinavjp/unravis --limit 40 --save`. The Overview tab of the Releases app shows the same headline.
4. Stay in phase 0 until G1 to G5 are go. A "disagree" resets the streak: explain it (flaky test, Windows-only difference, a bug in the runner), fix the cause, and the count starts again from the next pull request.

## Phase 1: develop

Order matters. Steps 1 and 2 are two separate pull requests into develop (both are workflow-only; branches below).

1. **Step 1, `claude/local-ci-dispatch-ready`.** Adds `workflow_dispatch` to `fast-gate.yml` and a "merge develop into the checkout" step that only runs on a manual run. GitHub keeps starting the gate by itself, so nothing is weaker. It is needed first because GitHub dispatches a workflow only when the file is on the default branch (the reason G7 cannot be done earlier; this is the documented behaviour of GitHub, not tested here). Merge when the gate on that pull request is green.
2. **Try the fallback once (G7)** on a real open pull request, while GitHub still gates:
   `unravis-ci fallback <N>`; watch with the `gh run list` line it prints.
3. **Switch the agent (G8):** stop the shadow agent (`unravis-ci drain`), start `unravis-ci serve --repo abhinavjp/unravis --post-required --context ci.yml=local/build`. Open a throwaway draft pull request into develop and check the row G8. Also look at what GitHub shows when both a GitHub check and the agent post `fast-build`.
4. **Step 2, `claude/local-ci-trigger-removal`** (a draft pull request into develop; it contains step 1 as well, so after step 1 is merged with a merge commit it shows only the removal). Do not merge before G6 is go. When merged, GitHub stops starting the fast gate; the agent's `fast-build` is the only one. If the repository ever squash-merges step 1, rebase step 2 on develop first.
5. After the merge: open one small real pull request into develop and see `fast-build` go pending, then green, from the agent (`gh pr checks <N>`).

Rollback: revert the step 2 merge (GitHub gates again within minutes), then stop using `--post-required`.

### Blocker R: the runner must be changed first (not part of this slice)

The runner only runs a workflow for an event that is in the workflow's own `on:`: `internal/localci/prrun.go` (pull requests), `runref.go` (pushes) and `poller.go`. After step 2 the fast gate has no `pull_request` and no `push` in `on:`, so the agent would refuse it and no `fast-build` would ever be posted. The pull request of step 2 would also never get a `fast-build`. Proven on 2026-11-25: `unravis-ci run <step-2 file> --event pull_request --dry-run` exits 2 with "does not run on pull_request", and with `--event push` too; with `--event workflow_dispatch` it plans all jobs.

The fix belongs in the runner (a configured gate workflow, `--workflow-for` / `--push-workflow`, must not be filtered by `on:`), and it touches trust code, so it needs its own slice and review. It must also change the test `TestRunDryRunFastGate` in `cmd/unravis-ci/main_test.go`, which plans the real `fast-gate.yml` as a `pull_request` and fails against the step 2 file (shown on 2026-11-25). Step 1 does not have this problem: with step 1 the same tests pass.

### How a manual run stays as strict as a pull request

On GitHub a pull request is checked as its merge commit with develop, and the scope steps read `HEAD^1` as the develop tip. A manual run checks out the branch alone, where `HEAD^1` is an older commit of the same branch: the scope would say "nothing changed" and pass without testing (a rubber stamp). Step 1 builds the merge commit first, fails when the branch has nothing beyond develop, and fails when it does not merge. Shown on 2026-11-25 in a scratch clone with two commits (a Go file, then a docs file): the bare branch gave "Only notes changed: nothing to run"; the merge shape found the Go change and 391 affected packages; a branch equal to develop failed with a plain message.

The job keeps the name `fast-build` (the spec says to rename it `fast-build-github`; do not: only a job with the required name satisfies the required check, and the fallback must).

## Phase 2: master (optional, read this first)

What changes: only `build` on `master` may come from "Any source" instead of GitHub Actions. What does NOT change: `ci.yml` keeps its `pull_request` and `push` triggers, because the release tools read the GitHub runs of `ci.yml` and a local status cannot replace them:

- `tools/release/Promote-Develop.ps1` (line 245) waits up to 45 minutes for the `ci.yml` run with event `pull_request` on the promotion PR, job `build`.
- `tools/release/Release.ps1` (lines 184 and 347) and `ReleaseLib.ps1` (line 1172) read the `ci.yml` runs of master, and `release.yml` (line 106) skips tests only when the newest `ci.yml` push run is green.

So GitHub keeps running `build` on every promotion PR and every master push, and the agent's `build` is a second producer. The change makes sense only if you want a local result to be able to satisfy `build`. Taking the setting to "Any source" also lets anyone with a token that can write statuses on the repository post a passing `build` (today only the owner and one collaborator have write access; the status token is a fine-grained, statuses-only token kept out of steps). If you do not need that, stay in phase 1 and skip this phase.

Do it only when phase 1 has run clean for a while and G8 showed how GitHub treats two producers of one name.

**Save the current setting first** (also the rollback file):

```powershell
gh api repos/abhinavjp/unravis/branches/master/protection > "$env:LOCALAPPDATA\Unravis\ci\master-protection-before.json"
```

**Click path (nachi, repository admin).** Names may differ a little in the GitHub page; the `gh api` read below is the truth.
1. Open https://github.com/abhinavjp/unravis/settings/branches (Repository, Settings, Branches).
2. Under "Branch protection rules" open the rule for `master` (Edit).
3. In "Require status checks to pass before merging" find the check `build`. Next to its name is a small label or drop-down that says "GitHub Actions". Open it and choose "Any source".
4. Change nothing else on the page (not "Require branches to be up to date", not "Require conversation resolution"). Press "Save changes" at the bottom.

**Verify** (read only):

```powershell
gh api repos/abhinavjp/unravis/branches/master/protection --jq '.required_status_checks.checks, .required_status_checks.strict, .required_conversation_resolution.enabled'
```

Before: `[{"app_id":15368,"context":"build"}]`, `true`, `true`. After: `[{"app_id":null,"context":"build"}]` (the same shape `develop` has for `fast-build`), `true`, `true`. Anything else that changed is a mistake: restore it.

**Rollback**, either way:
- Same click path, choose "GitHub Actions" again for `build`, Save. Then run the verify line: `app_id` must be 15368.
- Or by API (written from the GitHub documentation, not run):
  ```powershell
  '{"strict":true,"checks":[{"context":"build","app_id":15368}]}' | gh api -X PATCH repos/abhinavjp/unravis/branches/master/protection/required_status_checks --input -
  ```

Try it first on a throwaway draft pull request into master that only touches a doc (it cannot be merged by accident because it is a draft), and watch that both the GitHub `build` and the agent's `build` appear and the pull request turns mergeable.

## Phase 3: clean-up

Only after phase 1 (and 2 if done) ran clean for a full release cycle:

- Look at the trigger table below once more; remove nothing that a release tool reads.
- `BL-586p` sub-items: the release manager's own wait for the `build` status (spec phase 3) is a separate, owner-approved release change and is not part of this runbook.
- Delete the merged workflow branches (`claude/local-ci-dispatch-ready`, `claude/local-ci-trigger-removal`).
- Keep the weekly `compare` for a month; the streak is not valid after step 2, but "agent alive" and "no github-only" still are.

## When the PC is asleep or offline

Nothing merges unverified. After step 2, a pull request into `develop` shows `fast-build` as "Expected: waiting for status" and cannot be merged. The agent keeps Windows awake while it works. When the PC wakes, it lists every open pull request head without a status and runs them newest first (nothing is lost, it is late). If a PC dies in the middle of a run, another PC may take the run over after the lease TTL of 5 minutes (`--claim-ttl`).

| Case | What to do |
|---|---|
| Pull request into `develop`, PC offline or asleep | Look at `unravis-ci status`. No fresh agent: do not wait, run `unravis-ci fallback <N>` from any PC where `gh` is logged in. It starts `fast-gate.yml` on the branch of the pull request on GitHub. A GitHub fast gate took 77 s to 307 s over the last 13 green runs (checked), so it is green in about 5 minutes and `fast-build` appears on the head commit. A fresh agent that is only busy: wait. |
| Max wait | Fresh agent: wait as long as the run shows Running. Agent seen but the pull request has no status for more than 10 minutes: run `fallback`. No fresh agent: run `fallback` at once. There is no built-in timeout; a pending pull request just stays pending. |
| Pull request into `master` (promotion) | No dependency on the PC: GitHub still runs `ci.yml` on it (see Phase 2). `Promote-Develop.ps1` waits up to 45 minutes (`-CiWaitMinutes`), and `Release.ps1` too. |
| Push to `develop` or `master` | `ci.yml` still runs on GitHub. The fast-gate push run on develop only warms the Go cache; if the PC is off the cache is simply cold for the next pull request. |
| Several PCs | One claim per commit and workflow (`claims` lists them); only one PC runs it. |
| The fallback itself fails | `fallback` refuses plainly (no `workflow_dispatch` in the file, branch not in this repository, no login). Read its message, nothing is half-done. As a last resort the owner may merge as admin (`enforce_admins` is off), only on purpose and never past a red check. |

## What the owner must NOT do

- Do not merge step 2 (`claude/local-ci-trigger-removal`) before G6, G7 and G8 are go. It leaves develop with no gate if the agent cannot run it.
- Do not use `--post-required` without `--context ci.yml=local/build` in phase 1: it would post the real `build` as well.
- Do not use `compare` as evidence after step 2. Its streak is only valid in phase 0.
- Do not merge past a pending or red check as admin to save time (`enforce_admins` is off, so GitHub lets you). A pending check with no agent is what `fallback` is for.
- Do not change anything else on the `master` rule when switching the source (`strict`, conversation resolution, reviews). Do not touch `develop`'s rule at all.
- Do not remove the `pull_request` or `push` trigger of `ci.yml`, `desktop-windows.yml` or `supply-chain.yml`, and do not touch `release.yml`, `verify-release.yml`, `Release.ps1`, `Promote-Develop.ps1` or the release lock for this cutover.
- Do not run a release (or `Promote-Develop.ps1`) while the shadow phase is being measured, and do not start the agent while a release holds the lock (it pauses itself; `--no-release-pause` turns that off, so do not use it).
- Do not paste the status token (statuses write, plus Contents write for claims) into a workflow, an issue or a log. Do not add outside collaborators with write access without moving to a GitHub App (the "Any source" weakness).
- Do not delete `refs/ci-claim/*` or `refs/ci-agent/*` by hand; use `unravis-ci claims --clean`.

## Workflow triggers: decision for every workflow

Step 2 touches `fast-gate.yml` only. Every other workflow is listed with its decision. The reason for "keep" is in the last column; nothing here is gated by the local agent today except the two workflows in the first rows.

| Workflow | Automatic triggers today | Decision | Reason |
|---|---|---|---|
| `fast-gate.yml` | pull_request, push to develop | **Remove** both, keep `workflow_dispatch` (new) | The local agent takes over `fast-build`; manual run is the fallback. Step 1 adds the dispatch, step 2 removes the others. |
| `ci.yml` | push to master and develop, pull_request into master, `workflow_dispatch` | **Keep all** | `Promote-Develop.ps1` waits for the `pull_request` run on the promotion; `Release.ps1`, `ReleaseLib.ps1` and `release.yml` read the push runs of master. Dropping `pull_request` needs a change in `Promote-Develop.ps1` first (out of scope). |
| `desktop-windows.yml` | pull_request into master, push to master | **Keep** | Not a required check and not gating develop; it needs a Windows runner the agent does not offer. |
| `supply-chain.yml` | pull_request into master, push to master | **Keep** | Security scans, not part of the gate. |
| `mutants.yml` | pull_request (paths of `desktop/ipc-core`) | **Keep** | Path-limited, not required. |
| `nightly-e2e.yml` | schedule, pull_request on its own files | **Keep** | Scheduled; the pull_request trigger only fires when the workflow's own files change. |
| `nightly-fuzz.yml` | schedule | **Keep** | Scheduled. |
| `mirror-feed.yml` | schedule | **Keep** | Scheduled. |
| `ui-audit.yml` | schedule, pull_request on its own files | **Keep** | Scheduled and path-limited. |
| `release.yml` | push of release tags | **Keep** | Releases stay on GitHub (owner rule). The runner cannot run it anyway ("uses actions/attest-build-provenance"). |
| `verify-release.yml` | `workflow_dispatch` only | **Keep** | Releases stay on GitHub. The runner cannot run it (`run-name`). |

`lint` inside the fast gate does not take part in `fast-build` (the gate only needs `go` and `web`), so a manual run, which skips `lint`, is as complete as a pull request run.

## Checked on what, and what is not

Verified on 2026-11-25 with real commands: the two branch-protection reads, rulesets `[]`, the real `fallback 284 --dry-run` refusal, the dispatch run of the new `fast-gate.yml` through the local interpreter in a scratch clone (merge step runs; scope is correct; a branch equal to develop is refused), plan-only runs of both versions for the three events, `lint` of all workflows (same result as before the change), the `tools/ci` tests and the action-pin check (the `test-promote-dist.mjs` test fails on this Windows PC with EBUSY before and after the change, a known problem), the `internal/localci` workflow tests with step 1, and the trust tests.

Not verified: any real run on GitHub of the new workflow (no pull request was opened); GitHub dispatching a workflow that is on a non-default branch only; GitHub with two producers of one required name; the click path labels; the phase 2 rollback by API; the whole local run of the `go` job on this Windows PC (it stops at the same `test-promote-dist.mjs` EBUSY, and the web tests have three CRLF checks that fail in the scratch clone; neither comes from the workflow change). No agent is running yet, so G1 to G4 have no data.
