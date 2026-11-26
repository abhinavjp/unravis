# Homestretch: build queue, machine rules and enterprise features (round 4)

Status: research and prototype notes. The prototype was approved by nachi on 2026-11-26; the real build follows `ROUND4-BUILD-PLAN.md`.
Prototype: `homestretch-redesign-prototype.html`. Useful links: `?tab=queue`, `?tab=machines`, `?tab=holds`, `?topo=brain&fleet=five`.

## 1. What the research found

Sources: TeamCity, Jenkins, GitHub Actions, Azure DevOps and Octopus docs, plus vendor release-management pages.

| Tool | Queue | Where a build may run | Worth copying |
|---|---|---|---|
| TeamCity | Queue page; reorder or move to top needs a permission; priority classes -100..100; a build waiting over 15 days is cancelled | Builds are not pre-assigned; the agent is picked at start; the build config shows how many agents are compatible | "N compatible machines" next to a project; one-click move to top; plain reason when nothing matches |
| Jenkins | Queue with a "why is this waiting" line | "Restrict where this project can be run" with a label expression; exclusive labels keep a machine for one job type; temporary offline and drain (general knowledge, not fetched) | Why-waiting line; reserved machines |
| GitHub Actions | `concurrency` groups; `cancel-in-progress` is off by default, so runs queue | Runner groups are a security boundary with repo and workflow access policy; groups and labels both filter; forks on self-hosted runners are a risk | Allow list per machine; fork-PR warning |
| Azure DevOps | Jobs wait for a matching agent | Demands versus agent capabilities; pipeline permissions say which pipelines may use a pool; one matching agent means a wait | Needs versus machine labels; "only 1 machine can run this" |
| Octopus | Deployments queue per environment | Approvals are evaluated when queued and cannot be skipped; manual intervention names responsible teams; lifecycle phases per environment | Approvals as a named gate; scope rules to project and environment |
| Enterprise release tools | Release calendar | Blackout and freeze windows; multi-level approvals and CAB; audit trail (who approved what, when, why) for SOC 2 and SOX; rehearsed rollback; DORA metrics | Freeze window; audit trail |

Evidence gaps, stated plainly: Jenkins drain, Octopus freeze windows and tenants were not found in fetched pages (general knowledge). The enterprise-tool row rests mostly on vendor pages, so treat it as weak.

## 2. Include now, leave out

Rule: include only what the existing engine can back or what removes a real failure (the PR 321 case: one machine, wrong labels, a job waits forever).

| Feature | Decision | Why |
|---|---|---|
| Queue view (queued, running, waiting, done) with Cancel, Retry, Move to top | Include | Engine has queue, cancel, rerun, priority. The UI never showed them. |
| Why-waiting line | Include | Computed from labels and policy. This is the TeamCity and Azure lesson. |
| Dedupe or superseded display | Include | Engine already supersedes older runs of the same ref. |
| Machine allow and deny per project | Include | Asked for by nachi. Small rule: Any, Only these, Any except these. |
| Drain and resume | Include | Drain is real; resume is a small new piece. |
| Fork PR rule | Include as one line | The GitHub self-hosted runner risk is real. |
| Hold (freeze window) | Include | One list, one effect: Promote is blocked with a named reason. |
| Activity (audit) | Include | Every action from UI, CLI or Claude is recorded with an actor. |
| CLI parity | Include | nachi's rule: every function has a command. |
| Runner pools as a separate object | Leave out | Labels plus allow lists already do it. A pool object is more to learn. |
| Priority classes -100..100 | Leave out | Three levels (PR, push, schedule) plus Move to top is enough. |
| Separate environments entity | Leave out | A step already has a target branch. |
| Notifications | Leave out | Needs mail or chat setup; no engine support. |
| Release calendar | Leave out | A hold list covers the real need. |
| DORA metrics | Leave out | Needs history beyond the run list. |
| CAB, multi-level approvals | Leave out | Branch rules and named approvers already exist. |

## 3. Models

### 3.1 Queue

States: `queued`, `running`, `done`, `cancelled`, `superseded` (engine names). The UI adds a derived sub-state for queued items: `waiting` when no machine can take it.

Why-waiting is computed, never typed. In order: a hold on the project; paused or released claiming; no machine has the required labels; machines that match are denied by policy; matching machines are busy; matching machines are draining or offline.

Priority is the engine's integer: PR 1, push 2, schedule 3 (lower runs first). Move to top sets it below the lowest queued value. Nobody sees raw numbers; the chip says Normal or First.

### 3.2 Machine policy

Per project, one of: **Any machine** (default), **Only these**, **Any except these**. A machine can also be set to **Drain**: finish current work, take nothing new, then show Drained until Resume. Labels still decide fit; policy decides permission. A job runs where labels fit and policy allows. Every edit shows an impact line first: "3 queued runs would have no machine" and the edit is refused when it would strand a run, unless the user confirms.

Fork PRs: a line on each machine that runs untrusted code: "Pull requests from forks never run here." This is a default, shown plainly, not a setting in this phase.

### 3.3 Holds and audit

A hold has a project (or all), a reason, an owner, a start and an end. While active, Promote is blocked and the Overview says who and until when. Lifting early records the lifter.

Audit entry: time, actor (`ui`, `cli`, `claude`), person, action, target, reason. Read-only, filterable. Admin bypass of a rule is not a button; it is never offered.

## 4. CLI parity

Every UI action has the same operation as a command. Same safety stops, same audit entry (actor `cli`). Output is text by default and `--json` for scripts. Exit codes: 0 done, 1 workflow failed, 2 usage or I/O error, 3 unsupported, 4 stopped by a stop state, 5 held by a freeze.

Name: the binary is `homestretch`, with the short alias `hs`. Engine commands move under `homestretch ci` and keep working through shims.

| UI action | CLI | Backing today |
|---|---|---|
| Look at a project | `homestretch status [--project P] [--json]` | Real (`status`) |
| Promote one step | `homestretch promote <step> [--project P]` | Real (Promote-Develop.ps1) |
| Cut rc or stable | `homestretch release <rc\|stable>` | Real (Release.ps1) |
| Roll back a step | `homestretch rollback <step>` | New |
| List queue | `homestretch queue [--state S] [--json]` | Real (`runs`, `status`) |
| Cancel a queued or running item | `homestretch queue cancel <id\|pr> [--reason R]` | Real (`cancel`) |
| Retry | `homestretch queue retry <runid\|pr>` | Real (`rerun`) |
| Move to top | `homestretch queue top <id>` | Priority field is real; command is new |
| Pause or resume claiming | `homestretch queue pause\|resume` | Real (the `PAUSE` file); command is new |
| Run a PR on this PC | `homestretch queue fallback <pr>` | Real (`fallback`) |
| List machines | `homestretch machines [--json]` | Real (agent heartbeats) |
| Drain a machine | `homestretch machines drain <name>` | Real (`drain`) |
| Resume a machine | `homestretch machines resume <name>` | New |
| Add a label | `homestretch machines label <name> <label>` | Real (`serve --label`) |
| Allow or deny a project | `homestretch machines allow\|deny <name> --project P` | New |
| Set a project's policy | `homestretch policy set <project> --any\|--only m1,m2\|--except m1` | New |
| Show a project's policy | `homestretch policy show <project> [--json]` | New |
| Enrol a machine | `homestretch machines enrol --name N --code C` | Real (`agent enrol`) |
| Claim a failing PR | `homestretch claim <pr> --by NAME` | Real (labels `hs:claimed`) |
| Mark a fix done | `homestretch claim <pr> --fixed` | Real (`hs:fixed`) |
| Copy the brief | `homestretch brief <pr>` | New |
| Add a hold | `homestretch hold add --project P --until T --reason R` | New |
| Lift a hold | `homestretch hold lift <id>` | New |
| List holds | `homestretch hold list [--json]` | New |
| Read the activity log | `homestretch activity [--actor A] [--json]` | New |
| Compare runs | `homestretch compare <a> <b>` | Real (`compare`) |
| Version | `homestretch version` | Real |

Parity by construction: every UI action is one entry in a single action registry (id, arguments, safety checks, audit text). The UI buttons and the CLI parser both read it, and a table test fails the build when an action has no command.

## 5. Rename (its own work item)

- Binary `homestretch`, alias `hs`. `unravis-releases` and `unravis-ci` stay as shims that print the new name and forward, for a limited period.
- Opinion: keep `homestretch`. It is already the product name, so UI, CLI and docs say one word. `hs` is short enough for daily use; the only clash found is Hammerspoon on macOS, and Windows is the target. A more neutral name such as `relman` would lose the link with the UI name.
- Work item: rename binaries and flags, add shims, move engine commands under `homestretch ci`, update Release.ps1 and the launcher, add the action registry and the table test. Small PRs, rename last.

## 6. What is real and what is simulated

- Real in the engine today: queue, cancel, rerun, drain, labels, priority, supersede, pause file, run list.
- New (proposals): allow and deny policy, resume, holds, activity log, `--json`, exit codes 4 and 5, the unified `homestretch` binary, the action registry.
- In the prototype: the run list on Runs is the real snapshot; queue rows, machine states beyond this PC, policy, holds and activity are simulated and labelled so.
