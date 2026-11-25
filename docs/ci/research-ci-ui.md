# Research: one UI for CI runs and releases (information architecture)

Date 2026-11-25. Scope: how other tools lay out projects, runs, logs and releases; the Releases app today; the layout
to build. Where a fetched page did not say something it is marked NOT STATED (not filled from memory).

## 1. How other tools present it (fetched pages only)
Buildkite (dashboard walkthrough page)
- Landing: Pipelines dashboard. One card per pipeline: status icon of the latest default-branch build, bar chart of the
  last 30 builds (bar height = run time, colour = status), average speed, 30-day reliability, builds per week.
- Many projects: search ("/" focuses it), bookmark to pin, active/archived switch, team filter, tags.
- Pipeline page = build list with Filter (branch, state, mine), "Show steps" per row, Cancel on running rows.
- Build page = job list, trigger, Rebuild / Retry failures / New build (main button changes with state).
- NOT STATED on that page: top-level nav, row contents, step graph, log tail/search.

GitHub Actions (log docs, run history docs, releases docs)
- Left sidebar lists workflows; click one for its runs; click a run for the summary: state, result (success, failure,
  canceled, neutral), jobs list plus a graph (details NOT STATED), pick a job for its log, re-run.
- Logs: "Search logs" box (only searches expanded steps), failed steps auto-expand, each step shows its duration,
  line-number permalinks, download log archive. Live tail: NOT STATED.
- Releases are a separate feed reached by a "Releases" link beside the file list. A release hangs on a tag. Pre-release
  and "Latest" labels exist. They are not in the Actions run list.

TeamCity (build results page, build log page)
- Build page tabs: Overview (duration, trigger, agent, change summary, diagnostics), Changes, Build Log, Artifacts,
  Parameters, Dependencies, Tests; extra tabs appear only when relevant (Issues, Coverage...).
- Build Log: stage timeline (click jumps to the line); filter All / Errors / Important / Verbose; Expand/Collapse all;
  absolute or relative timestamps; download. Live updates: NOT STATED.
- Project overview, queue and agents pages: fetch failed (404), NOT VERIFIED.

Jenkins Blue Ocean (run details page; page says it is deprecated)
- Header bar coloured by status, with run number, branch, commit id, duration, authors, Re-run. Tabs: Pipeline (default;
  stages, steps, logs; a failed step's log opens by itself), Changes, Tests, Artifacts. Live log: NOT STATED.

GitLab (pipelines page, environments page)
- Path: project, left sidebar Build > Pipelines. List with filters (author, branch, status, tag, source).
- Pipeline page = graph, jobs in columns by stage, or grouped by job dependencies. "Mini graph" (a row of stage dots)
  is reused wherever a commit or pipeline appears.
- Deployments live under Operate > Environments: one row per environment (available, stopping, stopped), a deployment
  list per environment with retry and roll back. Deployments exist only after a CI deployment job made them.
  So: separate area, linked from the pipeline that made it.

Woodpecker / Drone: Woodpecker has a repo list, per-repo Pipelines, step list (log layout NOT STATED). Drone page had no UI text, NOT VERIFIED.

Vercel and Netlify (multi-project, deployments as the main list)
- Vercel: one dashboard lists a team's projects. Project Overview shows the latest production deployment with URL and
  commit. Deployments section: Redeploy, Inspect logs, Promote to Production, rollback. Production vs Preview
  environments; each commit or PR makes a deployment.
- Netlify: each deploy has a detail page (summary above the log, log-line permalinks). The "published deploy" is the
  live one; previews and branch deploys are labelled apart. Deploys-list columns and rollback: NOT STATED.

Patterns that repeat in the fetched sources
1. Project list -> runs list -> one run -> steps -> log. Nobody lands on the log.
2. A failed step opens by itself (GitHub, Blue Ocean); logs have search; download is common.
3. Releases/deployments are a second list (GitHub Releases, GitLab Environments, Vercel Deployments) joined to runs by
   tag or commit, never mixed into the run list as equals.
4. Many-project overviews: one small card per project, latest status plus a history strip (Buildkite).
5. Buttons change with state (Buildkite Retry failures vs Rebuild vs New build).

## 2. The Releases app today (D:\Projects\unravis-ci-local, read this session)
- One page, no URLs or routes: index.html with header, banner, list on the left, detail on the right, footer.
- Files: cmd/unravis-releases/ui/{index.html,app.js,detail.js,app.css}; Go server internal/releasedash (GET only,
  loopback check, token cookie, strict CSP: self-only scripts and styles).
- Routes: /api/snapshot, /api/release, /api/release/{tests,artifacts,checks,changes}, /api/log, /api/ping, /api/bye.
- Header: rc and stable boxes (signed feed version, age, paused, rollout, withdrawn count); Refresh.
- Banner (#run): "Release X is running" (links to it) or "stopped ...". It reads the release lock (live.go); lock state
  is never a separate screen.
- Left list: one row per release: version, status word (Live verified, Verify failed, Stopped at <step>, Withdrawn...),
  date and channel. A "Next release" row appears while a run has no version yet.
- Detail tabs: Steps (7 steps Preflight to Verify with help text, per-step log slice, Facts), Tests, CI checks, Files,
  Changes, All logs.
- Log viewer: search box, line count, Copy, Jump to end, live tail, failing line marked and scrolled to, paged loading.
- Footer: Read only, source health (GitHub, Feed, Release log), "Updated N min ago". Keys: R, j/k. Poll 5 s running, 30 s idle.
- Empty state lists each data source and what to do. Selection and tab kept in sessionStorage only.
- Gaps: no project concept, no CI-run list (runs show only as a release's "CI checks"), no branch view, no deep
  links, no machines/queue view.

## 3. Synthesis

### 3a. Three candidate layouts

A. Project tabs: Overview / Runs / Releases (later Machines), with a project switcher in the header.
```
 Unravis CI+Releases   [ Project: unravis v ]                       [Refresh]  Updated 2 min ago
 +------------------------------------------------------------------------------------------+
 | Overview | Runs | Releases | Machines                                                     |
 +------------------------------------------------------------------------------------------+
 | NOW   Release 1.4.0-rc3 is running: step Build (4 of 7), started 6 min ago   [Open]      |
 | Latest check on master: PASSED 12 min ago  (commit a1b2c3d "Fix export")   [Open]        |
 | Live versions    rc: 1.4.0-rc2 (3 days ago)      stable: 1.3.2 (2 weeks ago)             |
 | Last 20 runs:  ## # ## ## #### ## (green/red strip, hover = commit)                      |
 | Needs attention: 1 release withdrawn, 1 check failed on branch fix/log-view             |
 +------------------------------------------------------------------------------------------+
```
- Pros: familiar split; each tab answers one question; the release screen drops in unchanged; calm first screen.
- Cons: a new user must learn "Runs" vs "Releases" (tab tooltips fix this); two lists show the same commit.
- Cost: LOW to MEDIUM. Reuse list+detail+log viewer; add hash routing, /api/projects, /api/runs, /api/run, Overview.

B. One commit-centred timeline mixing runs and releases.
```
 [ unravis v ]  Filter: [All v] [master v]
 Today
  a1b2c3d Fix export       check PASSED 12 min      -
  9f8e7d6 Add log search   check FAILED 1 h         -
 Nov 22
  44aa11c Release 1.4.0-rc2   check PASSED  >  RELEASED rc  >  VERIFIED     [Open release]
```
- Pros: one story ("what happened to my change?"); the commit is the natural join key.
- Cons: a release spans several runs and a feed, so the objects differ; noisy list; unclear where release steps live;
  hard to explain to a first-time user; filters needed on day one.
- Cost: HIGH. New data model (commit as root); the release list and detail must be rebuilt.

C. Project switcher plus left nav (Overview, Runs, Releases, Machines, Settings), page area on the right.
```
 +----------+-------------------------------------------------------+
 | unravis v| Overview                                              |
 | Overview | (same cards as layout A)                              |
 | Runs     |                                                       |
 | Releases |                                                       |
 | Machines |                                                       |
 +----------+-------------------------------------------------------+
```
- Pros: scales to many sections; GitLab uses a left sidebar.
- Cons: eats width the list+detail panes need (two panes inside two panes); heavy for 3 or 4 sections.
- Cost: MEDIUM. Screens as in A plus a new shell and nested-pane layout work.

### 3b. Recommendation

Choose A (top tabs Overview / Runs / Releases / Machines) with a project switcher in the header. Mature tools keep
runs and releases as two lists joined by commit or tag (pattern 3), so it feels familiar; the current "list left,
detail right with tabs" screen becomes the body of Runs and Releases; Overview answers "is anything wrong right now?";
it is the cheapest. Borrow from B only the link: runs and releases show their short commit and link to each other
("This commit was released as 1.4.0-rc2"). Borrow from C only the switcher. Machines comes late; hide it until the
runner reports data.

### 3c. What changes in the current app
1. Header: product name plus project switcher; move the rc/stable boxes into Overview and Releases; keep Refresh.
2. Add a tab bar (reuse the TABS/aria code in detail.js) and hash routes such as #/unravis/runs/212 and
   #/unravis/releases/1.4.0-rc2: deep links and a working Back button (none today).
3. Releases tab = today's list + detail, unchanged, scoped by project.
4. Runs tab = same two-pane shell: list rows (status word, branch, short commit and message, trigger, started, duration,
   mini step strip), detail tabs Steps / Tests / Files / Changes / Log, reusing panelSteps, panelTests, panelChanges
   and the log viewer. Add a Filter (branch, state).
5. Overview tab: NOW strip (running run or release, from the lock), latest result per branch, live versions per
   channel, last-20-runs strip, "Needs attention" list.
6. Banner: keep, but make it project-wide and visible on every tab (so lock state is never hidden behind a tab).
7. New read-only routes: /api/projects, /api/runs, /api/run, /api/run/tests, /api/run/changes. Keep GET only, the
   loopback host check and CSP; do not add inline scripts or styles.
8. Footer: keep the source-health line, per project. 9. Commit chips link runs and releases to each other.
10. Empty states per tab (see 3e). Keep last project and tab in sessionStorage as today.

### 3d. What makes it project-agnostic, and what to defer
Project-agnostic from day one:
- A project is a small config record: id, display name, repo URL, branches to show, pipeline step names and help text,
  whether releases exist (yes/no), channel names, optional machine names. The UI reads labels from it; no hard-coded
  "rc", "stable", "Release.ps1" strings in app.js (they are hard-coded today).
- Run steps are data, not code (today the 7 release steps are a constant in app.js; make that the project's list).
- If a project has no releases (the small side project), the Releases tab and the channel boxes are simply hidden.
- Project switcher shows only when more than one project is configured. One project means no extra clutter.
- Every list row uses the same fields: status word, title, commit, when, how long. Different projects fill them differently.
Defer: Machines/queue screen, cross-project card grid (Buildkite style), notifications, write actions (Re-run, Cancel;
the server is GET-only on purpose), parallel-job graph, search across all logs, pinning, themes.

### 3e. Beginner-friendly copy rules
Rules
1. One status word per item, always the same set: Passed, Failed, Running, Waiting, Stopped, Not recorded here.
   Colour and icon only repeat the word. Never show a bare icon.
2. Say what happened and what to do next in the same sentence pair ("Failed at Build. Open the log to see why.").
3. Name things by what they mean: "Run" = one automatic check of the code; "Release" = a version people can install.
   Define both once in a tooltip on the tab and in the empty state, then never mix the words.
4. Tooltips explain jargon in one short sentence: commit, branch, channel, signed, feed, verify, lock, machine.
5. Every empty state says why it is empty, which source was checked, and the one next action.
6. Times are relative first ("12 min ago"), exact on hover; durations in words ("2 min 05 s").
7. A failed step opens by itself with the failing line marked (as GitHub and Blue Ocean do; the app already marks it).
8. Buttons say what they do ("Show the log", "Read again"), not generic verbs; read-only screens say so in the footer.
Eight example strings
1. Tab tooltip, Runs: "A run is one automatic check of your code: build, then tests. It starts when code is saved."
2. Tab tooltip, Releases: "A release is a version people can install. It is made from a run that passed."
3. Status chip: "Failed at Build" with tooltip "The build did not finish, so the tests and the release never started."
4. Overview, nothing wrong: "All good. The latest check passed 12 min ago and nothing is running."
5. Overview, running: "Release 1.4.0-rc3 is running. Step 4 of 7: Build. Started 6 min ago."
6. Runs empty state: "No runs yet. Runs appear here after you push code to this project. Checked: local CI log (found), GitHub (not reachable)."
7. Commit chip tooltip: "A commit is one saved change to the code. The short code identifies it."
8. Lock/banner: "A release is already running on this PC, so a second one cannot start. It began 6 min ago."

## Sources (all fetched this session; a failed fetch is listed separately)
- https://buildkite.com/docs/pipelines/dashboard-walkthrough
- https://docs.github.com/en/actions/monitoring-and-troubleshooting-workflows/using-workflow-run-logs
- https://docs.github.com/en/actions/how-tos/monitor-workflows/view-workflow-run-history and .../use-workflow-run-logs
- https://docs.github.com/en/repositories/releasing-projects-on-github/managing-releases-in-a-repository
- https://www.jetbrains.com/help/teamcity/build-results-page.html and .../build-log.html
- https://docs.gitlab.com/ci/pipelines/ and https://docs.gitlab.com/ci/environments/
- https://woodpecker-ci.org/docs/usage/intro
- https://docs.drone.io/pipeline/overview/ (no UI description)
- https://www.jenkins.io/doc/book/blueocean/pipeline-run-details/
- https://vercel.com/docs/deployments/overview and https://vercel.com/docs/projects/overview
- https://docs.netlify.com/deploy/deploy-overview/
- No UI text found (Buildkite command-step page, Argo CD web-terminal page): not used.
- Failed fetches (404): GitHub about-workflows page, TeamCity viewing-project-and-build-configuration-details, TeamCity teamcity-explorer.
