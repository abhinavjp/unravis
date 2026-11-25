# Local CI pipeline, slice 4: commit statuses and the shadow phase (BL-583)

Branch `claude/local-ci-status`, stacked on slice 3 (`claude/local-ci-agent`, PR #282). Spec: `docs/ci/LOCAL-PIPELINE.md` section 4 and cutover phase 0 (Shadow). This note lists what was built, how it was checked, and where it differs from the spec or the brief.

## What it does

- `internal/localci/status.go`: `StatusReporter` interface, `GHReporter` (posts with `gh api repos/<repo>/statuses/<sha>` and the agent's own token), `FakeReporter`, retry with backoff, a dedup ledger (`<state>/status/ledger.json`, survives a restart), `ShadowGuard` (refuses any name that does not start with `local/` unless the run is in post-required mode). The description is redacted and cut to 140 characters. No `target_url` is sent.
- `internal/localci/status_service.go`, `progress.go`: turns queue and run events into statuses. The agent posts everything. The child process only writes `queue/progress/<id>.json` and the agent reads it on each tick. A posting failure is logged and counted, never fails the run.
- `internal/localci/compare.go`: `Compare` and the table / JSON report with the streak toward the 20-in-a-row gate. `fallback.go`: `Fallback` and the `Dispatcher` interface. Both read GitHub through the separate `CompareSource` and `Dispatcher` interfaces, not through `Host`.
- `cmd/unravis-ci/status_cmds.go`: the project defaults (`fast-gate.yml` is `fast-build`, `ci.yml` is `build`), `compare`, `fallback`, the startup banners. `serve` gets `--post-required`, `--no-status` and `--context file.yml=name`.

Names in shadow mode (the default, the zero value of `PostRequired`): `local/fast-build` for a PR into develop, `local/build` for a PR into master and pushes to master and develop, and `local/<job>` for each job. The real names are posted only with `--post-required`, which prints a loud banner at start. `unravis-ci status` shows per agent: mode, posted, failed, last posting problem.

State to description: pending "queued"; pending "running" then "running, job k of N"; success "passed in 3m12s (4 jobs)"; failure "failed in 2m2s: job go, step 6 <name>"; error "cancelled", or error "error after 40s: <reason>" for a crashed child; superseded runs post nothing; a request with no matching gate rule gets no statuses.

## Verified live (2026-11-25, `abhinavjp/unravis`, throwaway store `D:\Projects\ci-live-store-4`)

Real `serve` in shadow mode (`--no-push --workflow-for claude/local-ci-store=fast-gate.yml`, so only PR #282 was eligible) on head `60df60d3`. On that commit `gh api .../statuses` showed, in order: `local/fast-build` pending "queued", pending "running", pending "running, job 2 of 4", pending "running, job 3 of 4", `local/go` failure "failed in 43s: step 6 Checks that always run (tests of the CI tools, action pins)", `local/web` success "passed in 43s", `local/lint` success "passed in 1m46s", `local/fast-build` failure "failed in 2m2s: job go, step 6 ...". No `fast-build` or `build` status was posted (checked by name: 0). No `target_url`. The startup banner said SHADOW.

`compare` ran against real GitHub, table and `--json`: a stacked PR with no real check reads `local-only`; the older PR #278 run (local fail vs GitHub pass) reads `disagree`; commits without a local run read `github-only`; the streak line read "0 of 20 needed".

The local failure is the known Windows EBUSY in `tools/ci/test-promote-dist.mjs` (rmdir of a temp build folder), not a posting problem. It is also the reason `compare` shows a real disagreement on #278.

## Tests only, NOT live

- `--post-required` (fake reporter only; the real required names were never posted).
- `fallback` real dispatch (fake dispatcher; never run against the real repo). `fallback --dry-run` was not run live either.
- The success path of the gate context (the live run failed; per-job success was seen live).
- Cancelled, crash and superseded statuses, restart dedup, retry on a real 5xx, the streak counting past 20 (tests with fakes).

## Surprises

1. A failing non-required context still changes the PR. `gh pr view 282` reports `mergeStateStatus: UNSTABLE`, and the combined commit status is `failure`. The PR stays mergeable (the only required context on develop is `fast-build`), but the PR page shows red marks for `local/go` and `local/fast-build`. `gh pr checks` lists the `local/...` contexts next to the real ones. Shadow mode is therefore visible, not silent. Each job context adds a row, so four rows per PR.
2. Per-job names sit in the same namespace as the gate (`local/go`, `local/lint`, `local/web`). When two workflows gate the same branch, the job name is qualified `local/<workflow>/<job>` to avoid collisions.
3. `compare --limit 30` (the default) takes over 2 minutes against real GitHub (about one request per PR and per commit); `--limit 5` takes 8 seconds.
4. The first "running" status has no job count; "job k of N" appears once the child reports its first job.

## Deviations from the spec or the brief

1. The dedup key includes the description, not only (sha, context, state), because "queued" and "running, job k of N" are both pending.
2. Gate rules are keyed by (kind, branch, workflow). A per-job context equal to the gate context is skipped. Per-job posts are terminal states only; skipped and not-run jobs are not posted.
3. Compare: a local error counts as a disagreement when GitHub has a result. Cancelled and superseded local runs are skipped. A GitHub result that is pending or missing reads `local-only`. The streak counts only PR heads, newest first by local end time; a disagreement breaks it; `local-only` and `github-only` rows are skipped. A check run and a commit status with the same name are both shown (fail wins, then pending, then pass). "Every disagreement explained" is manual: the report lists the disagreements. `local/<name>` statuses are never read as the real name.
4. `fallback` refuses a fork, a PR that is not open, a branch name starting with "-", a base with no gate workflow, and a workflow without a `workflow_dispatch` trigger.

## Open questions for the owner

- `fast-gate.yml` has no `workflow_dispatch` trigger, so `unravis-ci fallback <pr>` refuses it today. Add `workflow_dispatch` to `fast-gate.yml` (a workflow change, so not done here), or choose another fallback path for `fast-build`. `ci.yml` is not affected by the check.
- Does the owner want the per-job `local/<job>` rows on the PR page, or only the gate name `local/fast-build` during shadow (a small change in the naming rules)?
- Phase 1 (post the real names) needs the EBUSY in `test-promote-dist.mjs` fixed first; until then every local run of a PR that touches the dist promotion step fails where GitHub passes.
