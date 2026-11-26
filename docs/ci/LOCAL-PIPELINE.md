# Local pipeline: CI on a PC instead of GitHub ("TeamCity Lite")

Status: spec, written 2026-11-25 (BL-TBD, see BACKLOG). Owner decision (nachi, 2026-11-25): **replace** the GitHub CI gate and use only the local PC. GitHub Actions stops being the gate once the local pipeline is proven. Companion of [RELEASE-TRAIN.md](RELEASE-TRAIN.md) (develop, fast gate, promotion) and of the release manager ([../auto-updater/RELEASE-MANAGER.md](../auto-updater/RELEASE-MANAGER.md), [RELEASE-COORDINATION.md](../auto-updater/RELEASE-COORDINATION.md), lock on branch `release-lock`).

## Reading used (what was asked, what is built)

"Run the whole CI pipeline locally on any PC, like TeamCity Lite, by upgrading the Release Manager, and use only the local PC instead of GitHub." Read as:

1. A **local pipeline runner** executes the same stages as the GitHub workflows (fast gate, full CI, supply-chain checks, UI audit, nightly e2e and fuzz) on a Windows PC.
2. **No second pipeline definition.** The files in `.github/workflows/` stay the single source; the runner reads them.
3. Build **agents and a queue** (several PCs may serve one queue), per-stage logs and results, shown in the **Releases app** (`unravis-releases`).
4. The runner posts **commit statuses** to GitHub, so branch protection (`fast-build` on develop, `build` on master) is satisfied by the PC. GitHub workflows then stop running on pushes and pull requests.
5. **Scope is CI only** (nachi, 2026-11-25). Releases stay on GitHub and are not touched in this work: `Release.ps1`, `release.yml`, `verify-release.yml` and the release lock keep working exactly as today. Making releases an abstraction too is **future work** (see the end of this document). **Not replaced**: `release.yml` (build with build attestation needs GitHub's own signer) and `verify-release.yml` (a clean GitHub machine proves the update path). They are release steps, not CI gates; the release manager keeps driving them. A release still needs GitHub. Everything that gates a merge or a promotion moves to the PC.

## What moves where

| Workflow | Today | After cutover |
|---|---|---|
| `fast-gate.yml` (job `fast-build`) | PR and push on develop | Local, posts status `fast-build` |
| `ci.yml` (job `build`, shards, race, screenshots, docs) | push develop and master, PR into master | Local, posts status `build` |
| `supply-chain.yml`, `desktop-windows.yml` | PR into master, push master | Local (jobs that need a tool the PC lacks are listed as "not run locally", never as passed) |
| `ui-audit.yml`, `nightly-e2e.yml`, `nightly-fuzz.yml`, `mutants.yml`, `mirror-feed.yml` | schedules and path filters | Local scheduler (cron from the workflow `on.schedule`), results only in the Releases app; no status |
| `release.yml`, `verify-release.yml` | tag, dispatch | Unchanged (GitHub) |

After cutover each moved workflow keeps its jobs and steps and loses only its automatic triggers (`on:` shrinks to `workflow_dispatch`). That is the **fallback**: when the PC is gone, "Run workflow" on the branch runs the old pipeline unchanged on GitHub.

## Design

### 1. The runner reads the workflows (no copy)

New Go package `internal/localci` and command `cmd/unravis-ci` (not shipped in the Unravis exe; built with the releases app tools). It parses a workflow file and runs it with a small, strict subset of GitHub Actions:

- **Supported**: `on` (push, pull_request, schedule, workflow_dispatch), `jobs.<id>` with `needs`, `if`, `strategy.matrix` (lists, `fail-fast`), `env`, `steps` with `run` (bash, pwsh, powershell, cmd), `shell`, `working-directory`, `id`, `if`, `continue-on-error`, `timeout-minutes`, `$GITHUB_OUTPUT`, `$GITHUB_ENV`, `$GITHUB_PATH`, `$GITHUB_STEP_SUMMARY`, contexts `github`, `runner`, `env`, `matrix`, `needs`, `steps`, `inputs`, `vars`; expression functions actually used by our files: `success()`, `failure()`, `always()`, `cancelled()`, `hashFiles()`, plus `==`, `!=`, `&&`, `||`, `!`, string literals.
- **`uses:` shims** (each a few lines, listed in one table in code): `actions/checkout` (the run's own checkout, below), `actions/setup-go` and `actions/setup-node` (check the installed version satisfies `go.mod` / `node-version`, else the job fails with a clear line; no download), `actions/cache/*` (no-op, the PC keeps its own persistent Go and npm caches), `actions/upload-artifact` and `download-artifact` (copy to and from `ci\artifacts\<run>\`), `trufflesecurity/trufflehog` (runs the installed `trufflehog` binary with the same args). Anything else (for example `attest-build-provenance`) is **unsupported**.
- **Unsupported means loud.** Any key, function or `uses:` the runner does not know fails the job with `unsupported: <thing> at <file>:<line>`; it is never skipped silently. A test walks every workflow and fails when a moved workflow contains something unsupported. That keeps "single source" honest: if someone adds a feature the runner lacks, the test says so before the gate changes.
- **`runs-on`** is mapped by one small file, `.github/local-ci.yml` (overrides only, no steps): `windows-*` runs on the host; `ubuntu-*` runs on the host through Git Bash (Go, Node and the CI scripts are cross-platform; the Linux failure baseline is a list of Windows-only failures, so a Windows run passes it); a job can be marked `skip: <reason>` (macOS entries of the desktop matrix) and is then reported as **not run locally**, and the gate job counts it as failure unless the file lists it as `accepted`. A job can name an `exclusive:` resource of the PC (for example the desktop app's per-user first-instance name): instances that name the same resource run one at a time, across the legs of a matrix and across the runs one agent has going. `step-exclusive:` does the same for single steps (step name -> resource): only that step holds the resource, so the other steps of the job still run in parallel; it is for the `go test` steps that start the desktop app. Your own pre-push run takes the same lock: `unravis-ci exclusive desktop-first-instance -- <command>` waits for the resource, runs the command and returns its exit code (`tools/ci/prepush.sh` wraps its two Windows `go test` steps in it), so a local run and an agent run never fight over the first-instance name. Docker or WSL is not required, and not assumed (Docker Desktop is stopped on the owner's PC).
- Own jobs that gate (`fast-build`, `build`) stay what they are: ordinary jobs in the workflow that read `needs.*.result`. The runner derives the commit status from the gate job's result, so the definition of "green" stays in the workflow file.

### 2. A run: checkout, scope, work area

- A **run** is (workflow, event, commit). For a PR the commit is GitHub's merge ref `refs/pull/N/merge` (what Actions checks out), fetched into a throw-away worktree under `%LOCALAPPDATA%\Unravis\ci\work\<run>`; first parent is the base tip, as `fast-scope.mjs` expects (`fetch-depth: 2` behaviour). Worktrees are removed after the run (kept on failure for 24 h so a log line can be reproduced).
- Environment per step: `CI=true`, `GITHUB_*` variables the scripts read, a **scrubbed** parent environment: no `GH_TOKEN`, no Proton Pass, no `D:\secret` access, no release variables. Steps never see the status-posting token (it lives only in the agent process).
- Persistent caches (`GOCACHE`, `GOMODCACHE`, npm cache) live in a shared folder per agent, so a run starts warm without the `actions/cache` round trip.

### 3. Agents, queue, triggers

- **Triggers by polling** (the PC has no public address): the agent polls GitHub every 30 s with ETags (open PRs into develop and master, pushes to develop and master, and `on.schedule` cron entries locally). A new head sha of a PR cancels its older queued or running run (same rule as the workflow `concurrency` groups, read from the workflow).
- **Queue** is a local store (`ci\queue\`, JSON files, one per run, moved atomically). **Agent** = the `unravis-ci serve` process on a PC with `slots` (default 2 jobs at once; cores and RAM are plentiful on the owner's PC, 32 cores and 64 GB). Priority: PR gates first, then push runs, then scheduled work.
- **Many PCs, one queue** (slice 6): a run is claimed with an atomic GitHub operation, exactly like the release lock: create the ref `refs/ci-claim/<sha>-<workflow>` (creating an existing ref fails), release by deleting it, a lease with a heartbeat for crashes. No server, no database. Runs from a PC that is not the owner's are shown with its opaque id only.
- **Never run the release and CI at the same time on one PC when they compete for the machine**: the agent pauses taking new jobs while `release.lock` of the release manager is held on this PC (a release is rare and always wins).
- **Sleep and offline**: while the queue is non-empty or a job is running, the agent keeps Windows awake (`SetThreadExecutionState`, system-required only; the screen may sleep). When the PC was off, on start the agent lists every open PR head and tracked branch tip without a status from this agent and runs them newest first. Nothing is lost, it is late.

### 4. Commit statuses (what GitHub sees)

- On queue: `pending` "queued". On start: `pending` "running, job 3 of 7". On finish: `success` or `failure` with the time. Context names: `fast-build` for pull requests into develop, `build` for pull requests into master and for the push to master and develop, plus one non-required `local/<job>` status per job so the PR page shows which one failed. `target_url` stays empty until the Releases app can be reached from GitHub (it cannot: loopback only); the description carries the failing job and step name.
- Posted with the signed-in `gh` token of the agent (`repo:status` is enough). Posting is the only network write the runner does.
- **Fork pull requests never run** (trust, section 6). They get a `failure` status `fast-build` "fork pull request: a maintainer pushes it to a branch of this repository".

### 5. Releases app: a Pipelines page

The Releases app (`cmd/unravis-releases`, `internal/releasedash`) gets a **Pipelines** area next to the release list: runs (queued, running, passed, failed, not run locally), per-job and per-step timings, live log tail with the existing search/copy/jump (same `redact.go`, same loopback and one-time-code rules), the queue and each agent (name, slots, last seen, current jobs), and the last status posted per commit. It reads the runner's store files; it stays **read-only** (GET and HEAD only) in the first slices. Actions (re-run, cancel, drain) are CLI commands first (`unravis-ci rerun`, `cancel`, `status`, `drain`); buttons come last and need their own POST rule and security review (slice 7).

### 5a. One UI for runs and releases (decision from [research-ci-ui.md](research-ci-ui.md))

Layout A: header with a **project switcher** (shown only when more than one project is configured), then top tabs **Overview | Runs | Releases | Machines** (Machines hidden until an agent reports). Other tools keep runs and releases as two linked lists (GitHub Actions and Releases, GitLab pipelines and environments, Vercel deployments), so we do the same and join them by commit: a run shows "This commit was released as 1.4.0-rc2", a release links to its run.
- **Releases tab** = today's screen unchanged (list left, detail right, lock banner, logs), scoped by project. Releases keep working exactly as now.
- **Runs tab** = the same two-pane shell: rows with status word, branch, short commit and message, trigger, start, duration, a mini step strip; detail tabs Steps / Tests / Files / Changes / Log reusing the current panels and log viewer (search, live tail, failing line marked; a failed step opens by itself).
- **Overview** answers "is anything wrong right now?": what is running, latest result per branch, live versions per channel, last 20 runs, "Needs attention".
- The lock banner stays visible on every tab. Hash routes (`#/unravis/runs/212`) give deep links and a working Back button. New routes are read-only (`/api/projects`, `/api/runs`, `/api/run`, `/api/run/tests`, `/api/run/changes`); the server stays GET-only, loopback, strict CSP.
- Project-agnostic: a project is a small config record (id, name, repo, branches, step names and help text, whether releases exist, channel names). The "rc", "stable" and the 7 release steps now hard-coded in the app move into that record; a project without releases hides the Releases tab. Deferred: card grid across projects, notifications, write actions, parallel-job graph.
- Copy rules (beginner-friendly): one status set (Passed, Failed, Running, Waiting, Stopped, Not recorded here); "Run" = one automatic check of the code, "Release" = a version people can install, defined in tooltips and empty states; every empty state says why, what was checked and the next action; relative times with exact on hover.

### 6. Trust and secrets (the part that needs the most care)

The repository is public and the runner executes code from branches on a personal PC. So:

1. Only commits of **branches in this repository** by an allow-list of authors (the repository's collaborators, read from GitHub) run. Forks never run. A PR whose workflow, `tools/ci` or `.github/local-ci.yml` changed runs those files from the **base** (develop) when the author is not the owner, so a PR cannot widen its own sandbox; the PR's changes are tested, the runner definition is not theirs.
2. The agent runs as its own Windows user (recommended: a standard account `unravis-ci`; documented, not forced), whose profile holds no keys, no Proton Pass session, no `gh` token for the owner; the status token is a fine-grained token (statuses write on this repository only) readable by the agent process and by nobody else. This is the **honest limit**: without a VM the code runs with that user's rights. Windows Sandbox or a VM is the next step if the repository ever takes outside collaborators.
3. Job Object per step: the whole process tree is killed on timeout or cancel; output is redacted as it is written (`Protect-LogLine` rules, same patterns as the release logs).
4. The release signing key, passphrase and the release lock are not reachable from a CI user. The runner never calls `Release.ps1`.
5. The runner has no inbound listener. The Releases app stays loopback-only.

What the runner does about it (slice 2, `internal/localci`):

- **Who runs.** Collaborators are only users with push permission. A pull request of the owner runs with its own workflow files only when every commit in it was written and committed by the owner (GitHub's web editor committer counts); one foreign, unknown or truncated commit makes the run use the runner definition of the base, like a collaborator's. A non-owner pull request runs only into a trusted base (the bases of the workflow map, plus `--trusted-base`), never into a branch somebody with push access could have just created.
- **Git is sealed.** The runner's own git ignores the user's and the system's config, hooks, fsmonitor and replace objects, never follows symlinks and never signs. The mirror's `config` is rewritten from a fixed template and its hooks are emptied before every run; stale worktrees are pruned. Only the fetch sees the user's credential helper.
- **Steps get a profile of their own.** Each run gets a throw-away folder as `HOME`, `USERPROFILE`, `APPDATA`, `LOCALAPPDATA`, `GH_CONFIG_DIR` and `XDG_*`, git with no global or system config and an empty `credential.helper`, no ssh agent, no askpass; `GH_TOKEN` and `GITHUB_TOKEN` hold a placeholder that is not a credential (so a plain `gh` call fails instead of using the owner's login), Go and npm settings and caches come from the store, and a run of a pull request whose definition is not the owner's gets caches apart from the owner's. The environment is an allow-list (`PATH`, `SystemRoot`, ... plus `--allow-env NAME`), and every secret-looking value, plus the `gh auth token` value, is masked in every sink.
- **This stops casual access only.** A step still runs as the runner's Windows user: it can call the Windows Credential Manager API, start a credential helper by its full path, or read files elsewhere in that user's profile by absolute path. A step that unsets `GH_TOKEN` and runs `gh auth token` gets the owner's `gh` token, because `gh` keeps it in the Credential Manager (verified live). Pointing `HOME` somewhere else closes none of that. **A separate Windows user for the CI runner (point 2) therefore stays the recommendation**, and is the only thing that closes it; until then, treat the collaborator allow-list as the real boundary.

### 7. Cutover: replacing the GitHub gate safely

Branch protection today: `develop` requires `fast-build`, source **any** (`app_id` null); `master` requires `build`, source **GitHub Actions** (`app_id` 15368), strict. Both stay as they are, with one change on master, below.

| Phase | What happens | Branch protection |
|---|---|---|
| 0. Shadow | The agent runs fast gate and full CI on every PR and push and posts `local/fast-build` and `local/build` (not required). GitHub still gates. A page compares the two answers per commit. | none |
| 1. Develop | After at least 20 consecutive PRs where local and GitHub agree (and every disagreement explained), a PR changes `fast-gate.yml`: job renamed `fast-build-github`, triggers reduced to `workflow_dispatch`. The agent posts `fast-build`. | **none** (`fast-build` already accepts any source) |
| 2. Master | Same for `ci.yml` and the master workflows: agent posts `build`. | **nachi changes one thing**: required check `build` on `master`: source from "GitHub Actions" to "Any source". |
| 3. Clean-up | `pull_request` / `schedule` triggers removed from the moved workflows. **`ci.yml` keeps its `push` trigger on `master`**: `Release.ps1` and `release.yml` read that GitHub run as release evidence and are not changed (owner scope, 2026-11-25: CI only). | none |

Rollback at any phase: revert the trigger PR (the workflows are intact); phase 2 rollback sets the source back to GitHub Actions.

**Offline or asleep PC.** No status arrives, the PR shows "Expected: waiting for status" and cannot merge; nothing merges unverified. Options in order: wait (the agent catches up when it wakes); run the fallback on GitHub (`gh workflow run fast-gate.yml --ref <branch>`, wrapped as `unravis-ci fallback <pr>`: the dispatched run posts the same context); the owner merges as admin (`enforce_admins` is off) only on purpose. Promotion and release already need the owner's PC, so nothing new is lost there.

**The weakness that comes with "Any source".** Any token with `repo:status` on the repository can post `fast-build`. Today only the owner and one collaborator have push access; the status token is fine-grained and kept out of steps. If outside collaborators are added, switch the source to a GitHub App that only the agent holds.

## Modules, config and extraction (owner direction 2026-11-25: modular, reusable for other projects)

Research behind this: [research-ci-structure.md](research-ci-structure.md) (TeamCity, Buildkite, Jenkins, the GitHub runner, act, Woodpecker/Drone, Dagger, Gitea; sources listed there, gaps marked "unverified"). What we take and what we leave out on purpose is the table in its section 10; in short: take pull-based agents with outbound-only traffic, label routing, repo-side workflow files, quiet period plus branch filter, commit statuses, host execution, own-repos-only; leave out plugin systems, containers per step, a database server, pools, templates, priorities, dynamic pipelines. One deliberate difference: our "server" is GitHub itself (claims by ref, statuses by API), so one PC needs no daemon besides the agent and a second PC needs no new server.

**Two products in one Releases app, side by side.** The Releases app keeps showing releases exactly as today (release list, detail, lock, logs). The Pipelines area is added next to it and shares only the shell (loopback, one-time code, `redact.go`). Neither imports the other; both read their own store.

**Core (no project names, extractable):** each module is a Go package with one small interface; the Unravis-specific parts are config or adapters.

| Module | Interface (sketch) | Unravis-specific part (config, not code) |
|---|---|---|
| `localci` pipeline reader and runner | `Load(workflowFile)`, `Run(ctx, workflow, RunOptions)` | `.github/local-ci.yml` (`runs-on` map, per-job `skip`/`accepted`/`exclusive`, per-step `step-env`), scrub-list names |
| checkout and run store | `Checkout(ref) Workspace`, `SaveRun`, `AppendLog`, `ListRuns` | repo slug, state dir |
| host adapter | `Host` (pull request, head sha, author, collaborators) with a `gh` implementation | which repo, who is trusted |
| agent and queue (slice 3) | `Enqueue`, `Claim(agent, labels)`, `Complete`, `Cancel`; quiet period; cron | branch filter, labels, which workflow serves which base branch |
| status reporter (slice 4) | `Report(sha, context, state, desc)` | context names `fast-build`, `build`, `local/<job>` |
| pipelines UI (slice 5) | JSON over the store, read-only first | page text, tooltips |
| `release` (separate, future) | `Publish(run, artifacts)`; reads run results from the store; the core never imports it | everything about `Release.ps1`, signing, `release-lock` |

Rule for every slice: the core packages must not contain the words Unravis, Brain Payroll, `D:\secret`, or hard-coded context or branch names; a test greps the core packages for them. Process split (from the GitHub runner): the agent only schedules; each job runs in a child process, so a crash or kill of a job never takes the agent down.

**Extraction plan (when a second project, such as the small side project, wants it).**
- Moves to a new repository as is: `internal/localci` (reader, runner, shims), `cmd/unravis-ci` (renamed), the store and checkout, the queue and agent, the status reporter, the host interface with its `gh` implementation, the Windows process and Job Object code, the Pipelines UI components.
- Stays in this repository: `.github/local-ci.yml` and the label map (they are config), the Releases app shell and release module (`tools/release`, `cmd/unravis-releases`), Unravis scrub-list and trust lists, the status context names.
- Needs change at extraction: the Go module path and the binary and package names; the scrub list and secret-name patterns read from config instead of defaults; the state directory and port defaults; the `gh` host replaced or complemented (another forge) behind the same interface; the UI shell taken from a small shared package instead of the Releases app; docs moved with it.
- Cost control now: no abstraction is built before a second project exists beyond the interfaces above; the grep test and the config file are the whole price.

## Slices (each: tests, Sonnet review, Opus only for section 6, UI audit for screens)

1. **Runner core**: workflow parser, expression subset, job graph and matrix, step runner, outputs, `uses:` shims, unsupported-is-loud test over every workflow, `unravis-ci run <workflow> --event ... --local <dir>`. Verified on real data by running `fast-gate.yml` on this repository.
2. **Checkout and store**: PR merge-ref worktrees, scrubbed environment, per-step logs and run records, caches, cancel by Job Object.
3. **Agent and queue**: poller (PRs, pushes, cron), queue, supersede rules, keep-awake, release-lock pause, catch-up after sleep; `status`, `cancel`, `rerun`, `drain`.
4. **Statuses and shadow phase**: status poster, `local/*` contexts, comparison page data.
5. **Releases app: Runs and Overview tabs** (layout in 5a): project config record, tab bar and hash routes, read-only run routes, Runs list and detail on the existing shell, Overview, commit links between runs and releases; Releases tab unchanged; UI audit of every screen.
6. **Many PCs**: ref-based claim, lease, agent list.
7. **Cutover pieces** (CI only): workflow trigger PRs (`ci.yml` keeps its `push` trigger on master for the release manager), fallback command, optional buttons in the app.

Backlog items and the checklist live in [../BACKLOG.md](../BACKLOG.md); an item is ticked only when it works on the real data path (a real PR of this repository ran, was posted and was read back).

## Open points (decide in shadow phase, not now)

- Windows-host runs of `ubuntu` jobs: the `go-race` job needs a C compiler on Windows; if the PC has none, that job is `skip` with the reason shown until one is installed.
- macOS entries of `desktop-sidecar` cannot run on a Windows PC; they stay on GitHub as a manual job and are listed as not run locally.
- Disk: worktree plus caches about 3 to 6 GB per active run; the agent prunes work areas older than 24 h and caches over a size limit.
  Housekeeping on the developer PC (session scratch, merged worktrees, Temp probes, Go cache) is separate: `tools/maint/Housekeeping.ps1` (CLAUDE.md, BL-597). It never touches the agent's work areas or caches.

## Future work (not in scope now)

Releases on an abstraction, like CI: `Release.ps1` waiting for the commit status `build` of the pinned commit instead of the `ci.yml` run on master; `release.yml` skipping its tests from that status; then dropping the `push` trigger of `ci.yml` on master; a local equivalent of `verify-release.yml` and of build attestation. Only on the owner's go, as a separate spec.
