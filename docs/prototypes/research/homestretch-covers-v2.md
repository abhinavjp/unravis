# How CI and merge-queue tools show batched / covered merges (v2)

Researched 2026-11-27 from official docs and project source. Anything not confirmed is marked "unverified". The doc pages were read through a summarising fetcher, so quoted wording is as reported, not eyeballed from screenshots. No tool's real screenshots were seen.

## Takeaway

1. Nobody shows "N merges covered by one run" as a headline. The coverage is stated per item (a PR, an MR or a change shows its own position and result) or buried in a "Changes" list on the run.
2. The best patterns give the merged person a status on their own item that links to the shared run: GitLab "2 of 3 in queue" plus "View merge train", bors "included in a batch" plus a batch page, GitHub PR timeline plus a "merge queue" link.
3. When a shared run fails, tools either name the culprit by removing it from the queue (GitHub, GitLab, Zuul), or split and retry (bors bisects), or say nothing about cause (TeamCity, Jenkins, Buildkite: you read the change list yourself).
4. Failure text is plain and states the consequence for that item: "removed from the queue", "will be automatically retried", "will not be retried". The reason sits on the item's own timeline or comment.
5. Coalescing (TeamCity, Jenkins, Buildkite skip) is mostly silent in docs: a newer run replaces or absorbs older ones. Verified UI wording for the skipped or absorbed entries is thin, so a local tool has to design that wording itself.

## Table

| Tool | Where shown | Inline vs collapsed | Wording | Failure handling | How reached |
|---|---|---|---|---|---|
| TeamCity | Queue page lists queued builds (position, branch, config path, time to start, trigger reason). Build Results > Changes tab lists changes of a build; "changes are available while the build waits in the queue". | Changes tab lists all, filterable by author, comment, path and revision; "Show files" is a checkbox. Count wording: unverified. | Docs: "TeamCity will add only one build to the queue with all of these changes"; log wording "substituted by build with more changes" (support forum, second-hand); "Trigger a build on each check-in" is the opt-out. A literal "includes N changes" label: unverified. | No culprit named. Docs say per-check-in builds make it "easier to tell whose change caused a failure". Otherwise the user inspects the Changes tab. | Top-bar Queue page, then build, then Changes tab. Pending Changes from Projects or config page (old docs). |
| Buildkite | Pipeline build list; setting "Skip Intermediate Builds" (queued) and "Cancel Intermediate Builds" (running). | A skipped or cancelled build is its own row. How the row looks: unverified. Cancel reason is `build_skipping` (despite the name, set by Cancel Intermediate). | "Skip Intermediate Builds", "Cancel Intermediate Builds". A skipped build reports as `error` in GitHub commit status (changelog 97, was "success"). | A newer build covers the skipped commits. The failure is on the newer build. No automatic naming of the culprit (unverified). | Settings > Builds. Cancel reason visible in REST or GraphQL; UI path unverified. |
| GitHub merge queue | PR page: "Merge when ready" button, "Remove from queue" in the queue message at the bottom of the PR, and a "merge queue" link in the timeline. Branches page link such as "33 pull requests queued to merge". Queue view lists queued PRs with your PRs "clearly marked". | Queue view is a list, one row per PR. A group is a temporary branch (`gh-readonly-queue/{base}/...`); the queue UI does not describe a group row (unverified). | "merge queue", "merge group", "Merge when ready", "Remove from queue". Settings: build concurrency, merge limits (min, max, wait time). | A failing PR or a conflict gets removed; "The pull request timeline will display the reason". Reasons: CI reports test failures for a merge group; timed out; user removal; unresolvable branch protection. The queue rebuilds the rest without it. | From the PR (timeline link), or the Branches page. Notification channel for removal: unverified. |
| bors-ng | Comments on each PR, commit status on the batch commit linking to a batch details page (`/batches/<n>`). | One status per PR, the batch page lists the PRs. Statuses: "Waiting in queue", "Running", "Delayed for higher-priority pull requests", "Build succeeded", "Build failed", "Timed out", "Merge conflict", "Canceled". | "included in a batch", "Build succeeded:", "Build failed (retrying...):". Batching: "bors single on/off", `max_batch_size`; commit message "Merge ${PR_REFS}" with one line per PR. | On failure with 2+ PRs the batch is bisected into two batches and retried; a single failing PR is blocked. Comments: "This PR was included in a batch that timed out, it will be automatically retried" and "...successfully built, but then failed to merge into <branch>... It will not be retried." | From the PR comment or status "Details" link to the batch page. |
| Zuul | Status page: pipelines, then queues, then changes, then jobs with progress bars. Gate (dependent) pipeline keeps one queue ordered by approval. | A window limits how many head changes run jobs; the others show an hourglass-style waiting marker (from docs and a userscript, second-hand). Spec calls the page "overwhelming", poor at finding "my change". | "gate", "dependent pipeline", "window", "queue". | If a change fails, Zuul reports it, drops it, discards builds behind it and restarts them without it. Window grows by one on merge, halves on failure. This identifies the culprit by construction. | Gerrit or GitHub comment links to status or build. A per-change status route exists in code (`/{tenant}/status`). Status Page 2.0 per-change view is only proposed. |
| GitLab merge trains | Merge request widget: "This merge request is 2 of 3 in queue."; first MR: "A new merge train has started and this merge request is first in the queue. View merge train." Merge trains list (Code > Merge requests > Merge trains). | One row per MR in the train list, filter by target branch. Each MR's pipeline includes all MRs ahead of it. | "merge train", "View merge train", "Remove from merge train". | Failed MR is removed and not merged. System note: "User removed this merge request from the merge train because ...". Later MRs get new pipelines. Notifications: not described. | MR widget link, the MR system note "merge train" link, pipeline page "View merge train details", or the Merge trains tab. |
| Jenkins | Build queue panel; a build page "Changes" and "Started by ..." causes. | Core merges a new request into an existing queue item when the task matches (javadoc: "merged into existing items in the queue"). Match rules and the UI trace of merged triggers: unverified. | "quiet period" is a real setting. A literal "Changes: N" label: unverified. | No culprit logic in core. User reads the Changes list. | Queue panel, then build page, then Changes. |

## Design lessons for a small local release manager (queue rows plus a Run page)

1. Put the status on the merge's own row, not just on the run. Every merged item needs its own final status (GitLab "2 of 3 in queue", bors per-PR status). The row is what the person who merged looks at.
2. Make the row link to the shared Run page, and make the Run page list exactly which rows it covers. This mirrors GitLab's "View merge train" and bors's batch page. Use one noun for the shared run. Plain candidates: "covered by Run 14".
3. Show a short list inline and put the rest behind "show all N". Real tools do this at the data layer (TeamCity's Changes tab is filterable, GitHub's queue view is a plain list). Show the count in the heading, three or so rows inline, and a "show all" control.
4. When a covering run fails, tell each covered row its own consequence in one sentence, as bors and GitHub do: "removed from the queue", "will be retried", "will not be retried". Do not just say the run failed. If you cannot say which row broke it, say so and offer the next step (split and retry, as bors bisects, or run the rows singly).
5. Name the culprit only when you can prove it. Zuul and GitLab can because the failing item is the one at the head. A batch run cannot. Show "cannot tell which of these N broke it" rather than guessing, and offer a split run.
6. Say what happened to older queued or running runs that a newer run replaced. TeamCity and Buildkite do this almost silently, which is the confusing part. Mark the replaced row as "skipped, covered by Run N" with a link, and never report a replaced run as success (Buildkite changed skipped from success to error because it looked like tests had passed).

## Not verified

- Literal strings "includes N changes" (TeamCity) and "Changes: N" (Jenkins) were not found in any doc; they may exist in the live UI only.
- How Buildkite renders skipped builds in its build list.
- Whether GitHub's queue page shows a group as one row, and how removal notifications are delivered.
- bors comment wording for the bisect step itself (source shows split logic, text for "split into two" not seen). The hosted bors-ng is being retired in favour of GitHub merge queue; repo archived.
- Current Zuul status page icons and layout (only the spec and old commits were seen).

## Sources

- TeamCity queue and optimisation: https://www.jetbrains.com/help/teamcity/build-queue.html
- TeamCity build results page (Changes tab): https://www.jetbrains.com/help/teamcity/build-results-page.html
- TeamCity VCS triggers (2026.2): https://www.jetbrains.com/help/teamcity/2026.2/configuring-vcs-triggers.html
- TeamCity queue optimizer forum thread (second-hand, page blocked): https://teamcity-support.jetbrains.com/hc/en-us/community/posts/206811835/comments/207404725
- Buildkite canceling builds: https://buildkite.com/docs/pipelines/configure/canceling-builds
- Buildkite skipping: https://buildkite.com/docs/pipelines/skipping
- Buildkite changelog 97 (skipped is error): https://buildkite.com/changelog/97-skipped-builds-now-use-error-for-github-commit-statuses
- GitHub, managing a merge queue: https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/configuring-pull-request-merges/managing-a-merge-queue
- GitHub, merging a PR with a merge queue: https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/incorporating-changes-from-a-pull-request/merging-a-pull-request-with-a-merge-queue
- bors-ng documentation: https://bors.tech/documentation/
- bors-ng batcher messages source: https://github.com/bors-ng/bors-ng/blob/master/lib/worker/batcher/message.ex
- bors-ng batcher source: https://github.com/bors-ng/bors-ng/blob/master/lib/worker/batcher.ex
- Zuul project gating: https://zuul-ci.org/docs/zuul/latest/gating.html
- Zuul status page 2.0 spec: https://zuul-ci.org/docs/zuul/11.3.0/developer/specs/status-page-2.0.html
- GitLab merge trains: https://docs.gitlab.com/ci/pipelines/merge_trains/
- Jenkins Queue javadoc: https://javadoc.jenkins.io/hudson/model/Queue.html
- Jenkins queue merging, community reports: https://issues.jenkins.io/browse/JENKINS-59833
