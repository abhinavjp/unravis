# Which machine and which slot: how CI tools show it

Research 2026-11-27. "Verified" = read on a doc page fetched today. "Unverified" = known from product use or only secondary sources; the docs fetched do not quote it. Treat unverified wording as a lead, not a quote.

| Tool | Place | What it shows / wording | Status |
|---|---|---|---|
| Jenkins | Build queue | Clock icon on a waiting item. Tooltip on the job-name link "should tell you exactly why". Documented causes: "Agents are offline", "Waiting for an available executor on an agent", "Waiting for an available executor on a label". [1] | verified (causes); tooltip text unverified |
| Jenkins | Executor widget | Node name with busy/total executors after it; idle executors hidden; executor numbers dropped for regular executors; offline/launching shown as icons. [2] | verified |
| Jenkins | Node config | Executor = "a slot for the execution of tasks" (a thread); count set per node; 0 on built-in node disables it. [3] | verified |
| TeamCity | Queue | "Time to start" per build; hover shows expected start/finish and a link to the planned agent; shows how many agents are compatible, with a drop-down list. [4] | verified |
| TeamCity | Queue (paused) | Whole queue can pause (manual, or low disk); a message on each page gives the reason. Builds with no compatible agent auto-cancel after 15 days. [4] | verified |
| TeamCity | Wait reason | "There are no compatible agents which can run this build"; "Waiting for the build queue distribution process". [5][6] | secondary sources |
| TeamCity | Agent state | Three independent pairs: Connected/Disconnected, Authorized/Unauthorized, Enabled/Disabled. Runs builds only when all three are good. [7] | verified |
| Buildkite | Build page | Each job shows its agent name, linked to agent details. While waiting, a link to its queue; agent shown beside the queue once assigned. [8] | verified |
| Buildkite | Waiting job | State `scheduled` = no matching agent (queue or tag mismatch). Jobs match a queue plus every requested tag. [9][10] | secondary |
| GitHub Actions | Runner status | Idle: "connected to GitHub and is ready to execute jobs". Active: "currently executing a job". Offline: "not connected to GitHub". [11] | verified |
| GitHub Actions | Waiting job | Queued until an online, idle runner matches ALL `runs-on` labels (and the group, if set); fails after 24h on GHES. Log text "Waiting for a runner to pick up this job" is common in forums. [12][13] | partly verified |
| GitLab CI | Runner list | Status words `online` / `offline` / `stale` / `never_contacted`, with a 2h and 7d rule; description, tags, pause/resume ("Pause from accepting jobs"). [14] | verified |
| GitLab CI | Stuck job | "This job is stuck because you don't have any active runners online or available with any of these tags assigned to them". Variant also names protected branches. [15][16] | secondary |
| GitLab CI | Multi-slot | Runner config `concurrent` (all runners) and per-runner `limit` set jobs at once. These are config, not a UI display. [17] | verified |
| CircleCI | Self-hosted | Resource class `maxConcurrentTasks` (default 20) caps jobs; extra jobs queue even if runners look idle. Inventory shows "No runners" / offline. [18] | secondary |
| Azure Pipelines | Queued job | "The agent request is not running because all potential agents are running other requests. Current position in queue: 1". Also "No agent found in pool X which satisfies the specified demands: ...". [19][20] | verified |
| Azure Pipelines | Pool page | Agents tab lists every agent online/offline and in use; per-pool "Currently running X/X jobs". [19] | verified |
| Zuul | Node | A job waits on a Nodepool node request; nodes come from a nodeset; no documented per-node display on the status page (the status-page spec is not shipped). [21] | thin |

## Design takeaways for a small local dashboard (machines with N slots)

1. Name the machine on every row that shows a run: queue, running, finished, run detail, job list. Buildkite and Jenkins agree that the machine is a link, not just text. Link it to the machine's page.
2. Show the slot as "machine, slot 2 of 3", not a bare number. Jenkins dropped slot numbers because they confuse; busy/total beside the machine name (Jenkins) reads better than a slot id alone.
3. Show capacity as busy/total on the machine, for example "2 of 3 slots busy". GitLab and CircleCI hide this in config; surface it, since it answers "why is nothing starting?".
4. A waiting job must say why, in one plain sentence, with a cause named: "waiting for a free slot on Machine A (2 of 2 busy)". Use the Jenkins three-way split: machine offline, machine full, every machine with the needed label full. Add Azure's "position in queue: 1".
5. Say "no machine can ever run this" differently from "all busy right now". GitHub, GitLab, TeamCity and Azure all separate demand/tag mismatch from busy; the first needs a human, the second just needs time.
6. Keep machine state in separate axes, as TeamCity does (connected, enabled, busy). Offline, paused ("accepting jobs: no") and full are three different reasons; do not collapse them into one red dot.
7. Time the offline rule openly, as GitLab does (offline after 2h, stale after 7d). For a local tool, show "last seen 4 min ago" next to Offline.
8. Hide idle noise but keep it findable. Jenkins removed idle executors from its sidebar widget; show idle slots as a quiet count on the machines page, and only busy slots on the overview.

## Sources

1 https://www.jenkins.io/doc/book/using/executor-starvation/ ; 2 https://github.com/jenkinsci/jenkins/pull/9177 ; 3 https://www.jenkins.io/doc/book/managing/nodes/ ; 4 https://www.jetbrains.com/help/teamcity/build-queue.html ; 5 https://teamcity-support.jetbrains.com/hc/en-us/community/posts/360000150004-Wait-reason-There-are-no-compatible-agents-which-can-run-this-build ; 6 https://blog.jetbrains.com/teamcity/2022/05/solving-the-build-queue-bottleneck-problem/ ; 7 https://www.jetbrains.com/help/teamcity/build-agent.html ; 8 https://buildkite.com/changelog/223-linking-to-agents-from-jobs ; 9 https://buildkite.com/docs/agent/v3/queues ; 10 https://oneuptime.com/blog/post/2026-09-11-debug-buildkite-waiting-for-agent-queues-tags/markdown ; 11 https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners/monitoring-and-troubleshooting-self-hosted-runners ; 12 https://docs.github.com/en/actions/how-tos/manage-runners/self-hosted-runners/use-in-a-workflow ; 13 https://docs.github.com/en/enterprise-server@3.9/actions/hosting-your-own-runners/managing-self-hosted-runners/using-self-hosted-runners-in-a-workflow ; 14 https://docs.gitlab.com/ci/runners/runners_scope/ ; 15 https://forum.gitlab.com/t/this-job-is-stuck-because-you-don-t-have-any-active-runners-online-with-any-of-these-tags-assigned/28205 ; 16 https://gitlab.com/gitlab-org/gitlab/-/issues/463014 ; 17 https://docs.gitlab.com/runner/configuration/advanced-configuration/ ; 18 https://support.circleci.com/hc/en-us/articles/360055741771-Build-Not-Running-Due-to-Concurrency-Limit-but-No-Other-Job-Is-Running ; 19 https://learn.microsoft.com/azure/devops/pipelines/troubleshooting/troubleshoot-start ; 20 https://learn.microsoft.com/en-us/answers/questions/653833/azure-devops-agent-demands-failed ; 21 https://zuul-ci.org/docs/zuul/11.0.1/developer/specs/status-page-2.0.html

Gaps: exact UI strings for Jenkins queue tooltip, GitHub "Waiting for a runner", GitLab "Running on runner #id", CircleCI "waiting for executor" and Zuul's node display were not found on fetched pages. Confirm on a live instance before copying wording.
