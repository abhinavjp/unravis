# How other CI / release managers are structured (research for a small Go runner)

Date: 2026-11-25. Read-only research. Claims come from pages I fetched (Sources at the end).
"unverified" = I could not confirm it. Short paraphrase only. Note: the fetch tool returns a
model-made summary of each page, so exact wording was not checked.

Target: one owner, 1 to 3 Windows PCs, runs GitHub Actions workflow files, web UI inside the
existing Releases app, posts GitHub commit statuses.

## 1. TeamCity

- Pieces: server (UI, queue, config, history) plus agents. The agent opens the connection and
  polls the server ("unidirectional", polling protocol). One build per agent at a time, so agent
  count = parallelism. New agents must be authorized by an admin first.
- Queue: an agent is chosen only when the build starts; only agents meeting the configuration's
  requirements are candidates; among idle ones the fastest wins (history estimate, CPU rank as tie
  break). Priority changes queue position only, and waiting time slowly raises it.
- Triggers: a VCS trigger polls each VCS root (or reacts to a hook). A quiet period (default 60 s)
  waits after the last change, so several check-ins become one build. A branch filter limits
  which branches fire.
- Project config out of core: templates hold shared structure; each configuration fills in its own
  values through parameters.
- Agent pools: named agent groups; a project builds only on pools assigned to it.
- Reporting: Commit Status Publisher posts each result to the GitHub commit; can be a required check.
- REST API: HTTP over projects, builds, agents; JSON or XML. Whether it can trigger builds: not on
  the page I read (unverified).
- Overkill for us: pools, template inheritance, agent ranking, authorization flow, priority aging.
- Gap: the TeamCity concepts page returned 404; the server/agent split rests on the agent page.

## 2. Buildkite

- Pieces: hosted control plane (dispatch, status, UI) plus a self-run agent. The agent connects out
  over HTTPS and polls; no inbound ports. It runs the command, streams logs, reports exit status,
  uploads artifacts.
- Definition: YAML in the repo (.buildkite/pipeline.yml) or the web editor. Step types: command,
  wait, block, input, trigger, group. A step can run `pipeline upload` to add steps at run time.
- Routing: a step sets `agents: queue`. A self-hosted agent joins a queue by a `queue` tag at start;
  no tag means the default queue. Job goes to the first available agent in the queue (the one that
  most recently finished a job, favoring warm caches). Clusters group queues, agents, pipelines.
- Hooks: shell scripts at lifecycle points (environment, pre-checkout, checkout, post-checkout,
  pre-command, command, post-command, pre-artifact, post-artifact, pre-exit, agent-startup,
  agent-shutdown). Homes: the agent machine (all jobs), the repo's .buildkite/hooks, or a plugin.
- Plugins: add-ons hooking the job lifecycle, set on a command step; vendored (in repo) or fetched.
  How agents install them: not on that page (unverified).
- Take: pull agent, queue-name routing, hooks as the per-machine extension point, config in repo.
- Overkill: clusters, hosted queues, plugin ecosystem, dynamic upload.

## 3. Jenkins

- Pieces: controller (config, auth, UI; decides where and when work runs) plus agents (Java client
  on a node). Executors are slots, docs advise starting with one per node. On Windows the agent is
  agent.jar run with controller URL, secret and node name, optionally as a boot-time scheduled task.
- Definition: Jenkinsfile in source control, declarative or scripted (Groovy). `agent` required in
  declarative; stages contain steps; a failed stage stops later ones. Pipeline survives controller
  restarts. Plugins add steps; shared libraries hold reusable code.
- Labels: set on agents; jobs use label expressions; an agent can be limited to matching jobs only.
- Plugins: .hpi packages installed via UI, CLI, or by copying into JENKINS_HOME/plugins.
- Queue behavior: no fetched page covered it (unverified).
- Overkill: the plugin system, Groovy, executors, cloud agents.

## 4. GitHub Actions self-hosted runner (actions/runner)

- From the auth design doc: registration uses a short-lived token; the runner makes an RSA key pair
  and keeps the private key locally. The listener signs for an OAuth token, then long-polls a
  message queue over HTTP. When a workflow triggers, its labels are matched to a runner and an
  encrypted job message is queued. The listener starts a separate worker process and passes the job
  over IPC; a per-job token stays in memory and is masked in logs.
- Labels: set at configure time as a comma list, case-insensitive. How several `runs-on` labels
  combine: not on fetched pages (unverified here).
- Ephemeral / JIT: a runner takes one job then is removed (`--ephemeral`, or JIT config from the
  REST generate-jitconfig endpoints). Recommended for autoscaling. A job with no matching runner
  waits (fails after 24 h); a job not picked up within 60 s is re-queued.
- Security: GitHub advises against self-hosted runners for public repos (fork PRs can persist
  malware on a non-clean machine); private repos carry a smaller form of the same risk.
- Take: listener/worker split, label matching, optional one-job-per-runner, job-scoped token.
- Overkill: RSA/OAuth identity, encrypted job messages, runner groups, autoscaling.

## 5. nektos/act

- Reads workflow files, orders jobs by dependencies, uses the Docker API to run a container per
  step image, mimicking GitHub env and filesystem.
- Each `runs-on` label maps to a Docker image (override with `-P label=image`). A label set to
  `-self-hosted` runs those jobs on the host with no Docker (suggested for Windows/macOS labels
  on the same OS).
- Not implemented (docs list): concurrency, run-name, step summaries, problem matchers,
  annotations, parts of the `github` context, cancelling run steps, job.permissions, timeout-minutes,
  continue-on-error, job.environment and environment secrets, OIDC URL. Docker context: not planned.
- Implication: a good model for the workflow reader and label mapping. It gives no run history,
  queue, UI or status posting. Whether it can be embedded as a Go library: unverified.

## 6. Woodpecker CI and Drone

Same family: server plus polling agents, container per step, config in repo.

- Woodpecker config: `.woodpecker.yaml` or `.woodpecker/`; steps run in order with a shared
  workspace; `when` filters (branch, event, path); services; plugins are images taking `settings`.
- Woodpecker agents: gRPC to the server with a shared or per-agent secret. Labels are declared by
  the agent (not trusted); filters are set server-side per agent and win on a key clash. Backends:
  docker, local, kubernetes, auto-detect.
- Woodpecker local backend: steps run directly on the host, no isolation, works on Windows, no
  services, steps can reach the agent's files; only for trusted code on a private setup. This is
  the closest match to our case.
- Woodpecker server: embedded SQLite by default; a config-extension endpoint lets an outside
  service supply pipeline config.
- Drone: runners are separate daemons polling the server; types listed: Docker, Exec, SSH, Digital
  Ocean, VM (RC), Macstadium (a Kubernetes runner is not confirmed: unverified). DB migrations run
  on upgrade. Extensions are external HTTP services (config, secret, conversion, validation,
  registry, admission, environment). Plugins are Docker images configured by environment variables.
- Take: server decides, agents poll; server-side agent filters; an exec/local backend; an HTTP seam.
- Overkill: gRPC, containers per step, services, forge OAuth, plugin images, DB server.

## 7. Dagger

- Pipelines are typed code run by one engine (sandbox, scheduler, container runtime, cache, tracing
  in one data model), local or on a shared remote engine; work is a lazy incremental graph so
  unchanged parts are reused. Modules wrap toolchains and read existing project config.
- SDK languages, cache keys, module format: not on fetched pages (unverified; caching and modules
  pages returned 404).
- It does not read GitHub workflow files, so it is a different layer. Overkill for us; only
  content-aware caching is an idea to note.

## 8. Gitea Actions runner (optional)

- Labels pick execution mode: `docker://image`, `kubernetes://image`, or `host` (runs as the
  runner's user, no isolation, trusted repos only). Runs without a config file. Relationship to
  nektos/act: not stated on that page (unverified). Nearest precedent for "workflow files + host mode".

## 9. GitHub commit status API (for our reporter)

POST /repos/{owner}/{repo}/statuses/{sha}: state (error, failure, pending, success), optional
target_url, description, context (default "default"). Needs push access. At most 1000 statuses
per SHA and context.

## 10. Synthesis

### Take now / Leave out on purpose

| Idea | Verdict | Why (source) |
|---|---|---|
| Pull-based agent (outbound poll) | Take | No inbound ports on PCs (Buildkite, TeamCity, GH runner) |
| Queue with label routing, job labels must be a subset of agent labels | Take | Smallest working scheduler (GH runner, Jenkins, Buildkite) |
| Server-side agent allow list instead of self-declared labels | Take, small | Woodpecker filters |
| Listener / worker process split | Take | A crashing job must not kill the agent (GH runner) |
| Read workflow from repo at the built commit | Take | All; here .github/workflows/*.yml |
| Quiet period + branch filter | Take | One push burst = one run (TeamCity) |
| Host execution with `runs-on` label mapping | Take | act -self-hosted, Woodpecker local, Gitea host |
| Commit status with context + target_url | Take | TeamCity publisher, GitHub API |
| Per-machine hooks dir | Take later | Buildkite; keeps PC specifics out of core |
| Fresh work dir per job (JIT idea) | Take as option | GH security guide |
| Trust rule: own repos only, no fork PRs | Take | GH security guide, Woodpecker local |
| Plugin marketplace / plugin install | Leave out | Jenkins, Buildkite, Drone; we ship one binary |
| Containers per step, service containers | Leave out | Windows host run; act and Woodpecker local drop services |
| DB server | Leave out | Woodpecker defaults to embedded SQLite; use SQLite or files |
| Pools, clusters, templates, priorities, agent ranking | Leave out | Scale features (TeamCity, Buildkite) |
| RSA/OAuth identity, encrypted job messages | Leave out | One shared secret over HTTPS, trusted network |
| Dynamic pipelines, Groovy, Dagger engine, autoscaling | Leave out | Static YAML, fixed PCs |
| act's gaps (timeouts, continue-on-error, concurrency, environments) | Decide per item | Cheap ones worth doing ourselves |

### Proposed modules (Go packages) and interface sketches

- `pipeline` (reader): `Load(repoDir, sha, event) ([]Workflow, error)`. Parses workflow YAML into
  jobs, needs-graph, steps, matrix, runs-on labels, `if`, env. Pure.
- `queue`: `Enqueue(Run)`, `Claim(agentID, labels) (*Job, bool)`, `Complete(jobID, Result)`,
  `Cancel(runID)`. Persists through `store`; holds the quiet-period merge of repeated pushes.
- `runner` (agent side): `Run(ctx, Job, Host) (Result, error)`. Polls `Claim`, spawns a worker child
  per job, streams log lines up, honors cancel and timeout.
- `host` (adapter): `Prepare(job) Workspace`, `Exec(ws, shell, script, env) (exit, error)`,
  `Cleanup(ws)`. Windows implementation (cmd, pwsh, bash if present); all per-PC differences live here.
- `store` (run store): `SaveRun`, `AppendLog(jobID, chunk)`, `GetRun`, `ListRuns(filter)`. SQLite
  or files; the Releases app reads it.
- `status` (reporter): `Report(sha, context, state, targetURL, desc) error`; retries, de-dupes
  repeats, respects the 1000-per-context cap.
- `trigger` (inbound): `OnPush(repo, branch, sha)` from webhook or poll; applies branch filter and
  quiet period, then `Enqueue`.
- `api`: JSON over HTTP for the Releases UI (list runs, view log, re-run, cancel, agent status);
  agents use the same API with a shared secret.
- `release` (separate module): `Publish(runID, artifacts) error`, tagging and notes. Reads run
  results from `store`; the core never imports it, so CI works without it.
- Project config stays out of core: a small per-repo file (secret names, label map, allowed
  branches) plus per-agent config (labels, work dir, hooks dir). This mirrors TeamCity parameters
  and Buildkite machine hooks without template machinery.

## Sources (all fetched)

- https://www.jetbrains.com/help/teamcity/build-queue.html
- https://www.jetbrains.com/help/teamcity/build-configuration-template.html
- https://www.jetbrains.com/help/teamcity/rest/teamcity-rest-api-documentation.html
- https://www.jetbrains.com/help/teamcity/configuring-agent-pools.html
- https://www.jetbrains.com/help/teamcity/configuring-vcs-triggers.html
- https://www.jetbrains.com/help/teamcity/build-agent.html
- https://www.jetbrains.com/help/teamcity/commit-status-publisher.html
- https://buildkite.com/docs/agent/v3
- https://buildkite.com/docs/pipelines/defining-steps
- https://buildkite.com/docs/pipelines/clusters
- https://buildkite.com/docs/agent/v3/hooks
- https://buildkite.com/docs/plugins
- https://buildkite.com/docs/agent/v3/queues
- https://www.jenkins.io/doc/book/managing/nodes/
- https://www.jenkins.io/doc/book/pipeline/jenkinsfile/
- https://www.jenkins.io/doc/book/managing/plugins/
- https://www.jenkins.io/doc/book/using/using-agents/
- https://www.jenkins.io/doc/book/pipeline/
- https://raw.githubusercontent.com/actions/runner/main/docs/design/auth.md
- https://raw.githubusercontent.com/actions/runner/main/README.md
- https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners/using-labels-with-self-hosted-runners
- https://docs.github.com/en/rest/actions/self-hosted-runners
- https://docs.github.com/en/actions/security-for-github-actions/security-guides/security-hardening-for-github-actions
- https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners/autoscaling-with-self-hosted-runners
- https://docs.github.com/en/rest/commits/statuses
- https://raw.githubusercontent.com/nektos/act/master/README.md
- https://nektosact.com/not_supported.html
- https://nektosact.com/usage/runners.html
- https://woodpecker-ci.org/docs/usage/workflow-syntax
- https://woodpecker-ci.org/docs/administration/configuration/agent
- https://woodpecker-ci.org/docs/administration/configuration/backends/local
- https://woodpecker-ci.org/docs/administration/configuration/server
- https://docs.drone.io/server/overview/
- https://docs.drone.io/runner/overview/
- https://docs.drone.io/extensions/overview/
- https://docs.drone.io/plugins/overview/
- https://docs.dagger.io/
- https://docs.gitea.com/usage/actions/act-runner
