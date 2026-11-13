# Blackbox expansion: orchestration brief for Qoder

Written 2026-11-13. Read `docs/research/blackbox-expansion-handoff.md` first (rules, build order, checks). This file only adds how to plan, split, parallelise, review and deliver the work with Qoder's own tools and models.

## 0. Qoder facts to use (verify in the app; docs may have changed)

- **Model tiers** in the selector: Auto, Efficient (cheap, about 0.3x), Performance (about 1.1x), Ultimate (highest, about 1.6x). Lite is retired. Read the real rates in the selector and use those.
- **Custom subagents**: Markdown files in `.qoder/agents/<name>.md` (own context window, own tools, own system prompt). Run `/agents reload` after adding. If the file format has a model field, set the tier there; otherwise switch the tier in the selector before you dispatch.
- **Skills**: `.qoder/skills/<name>/SKILL.md` (`/skills reload`). **Rules and memory**: `AGENTS.md` at the repo root and `.qoder/rules/*.md`.
- **Quest mode** is for long, delegated work; Agent mode is for step-by-step work with checkpoints.
- The Claude-only skill `forge-plan` does not exist in Qoder. Section 2 below is its equivalent: do exactly that.

## 1. Model plan (map by task, not by habit)

| Role | Tier | Used for |
|---|---|---|
| Orchestrator (this session) | Ultimate | Plan, packet writing, dependency graph, dispatch, merging results, state file. Keeps its own context small: never reads implementation detail it can delegate. |
| Implementer, small | Efficient | Mechanical packets: types, config fields, plain table tests, doc and backlog edits, wiring. One file area, no concurrency, no security surface. |
| Implementer, standard | Performance | Packets with logic: counter engine, resolver, PDH or EventPipe readers, ring buffers, throttling, SQL reader. Also any packet an Efficient implementer failed once. |
| Batch reviewer, normal | Performance | Review of a finished batch of non-security packets. |
| Batch reviewer, high-risk | Ultimate | Review of anything touching SQL, credentials, service accounts, ACLs, HTTP endpoint, process access, or the footprint budget. Also the final whole-PR review. |

Each packet carries `model: efficient|performance` and a one-line reason (complexity, impact, risk). A human may overrule any choice by editing the packet header before dispatch.

## 2. Step 1: plan (no code)

Create `.forge/blackbox-expansion/PLAN.md` containing:

1. Goal and the non-negotiable rules (copy from handoff section 3, do not paraphrase).
2. **Technical dependencies**: Go version and modules in `go.mod`; Windows APIs (PDH, ETW or EventPipe, service control manager, ACLs, process snapshot); build tags and the `*_windows.go` / `*_other.go` pairs; existing Blackbox types the new code extends; test helpers and fakes that exist; things that need a real server and cannot be tested here.
3. **Tools and skills used** per step (which Qoder subagent, which skill, which command), so a human can see them.
4. Group order (handoff section 4), the unconfirmed items (handoff section 5) and which packets they block.
5. The risk table: packet id, impact, risk, tier chosen, reviewer tier.

## 3. Step 2: work packets

One file per packet in `.forge/blackbox-expansion/packets/BBX-<group>-<nn>-<slug>.md`. Make them **extremely small**: one concern, one to three files, about 150 changed lines at most, one test file. If a packet needs more, split it. Create packets for every backlog item BL-100 to BL-126 that can be written now; mark an item "blocked: <reason>" (for example "needs live PDH path check") instead of skipping it.

Packet template (all sections required, and the packet must make sense without any other document):

```
# BBX-1-03 <title>
Backlog: BL-xxx | Group: 1 | Model: efficient|performance | Reason: ...
Depends on: BBX-1-01, BBX-1-02 (or none) | Blocks: ...
## Goal (2 sentences)
## Context (only what this packet needs: exact file paths with line refs, the types/functions it uses, the rules from the handoff that apply)
## Do (numbered, concrete: file, function, signature, behaviour)
## Do not (the rules most likely to be broken here)
## Tests to write first (names and what each asserts, using fakes)
## Verification checklist (each item a command or an observable fact; all must be ticked)
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/blackbox/... -count=1 passes, new tests listed above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/blackbox/...)
- [ ] Footprint: no new goroutine or ticker without a stop path; no allocation in the 15 s loop beyond the cap stated here
- [ ] Missing reading is omitted, never 0 (test named ...)
- [ ] No text captured (list of fields written; nothing else)
- [ ] (packet-specific items)
## Live-check needed? (yes/no; if yes, the exact command a human runs on a real server and what number to expect)
## Report back (files changed, checklist with each item ticked or failed with output, anything unconfirmed)
```

The implementing subagent must tick each checklist item by actually running the command, paste failing output, and say "done" only when all are ticked. A packet is finished only when this is true.

## 4. Step 3: dependency graph

Write `.forge/blackbox-expansion/GRAPH.md`: a table (packet, depends on, files touched, wave) and a Mermaid graph. Rules:

- A packet is in wave N when all its dependencies are in earlier waves.
- Packets in the same wave run in parallel **only if they touch disjoint files**. If two touch the same file, serialise them. Use separate git worktrees for parallel implementers; merge each wave into the PR branch before the next starts.
- Mark the critical path and the packets that are blocked on a live-server check.

## 5. Step 4: execute in waves

Keep `.forge/blackbox-expansion/STATE.md` (packet, status, tier, wave, review result, commit). It is the orchestrator's memory; update it after every step so a fresh session can resume.

**Context engineering rules for every dispatch:**
- Give the subagent only: its packet file, the files the packet names, and the handoff section 3 rules. Not the plan, not the other packets, not the chat history.
- State the end condition and the checklist in the first lines of the dispatch. Tell it what to return (the report format in the packet) and nothing more.
- Return values to the orchestrator are short: status, checklist result, files changed, open questions. Full command output stays in the packet's report file under `.forge/blackbox-expansion/reports/`.
- When a subagent fails once, give the fresh retry the packet, its report and the failure output, and move it up one tier. Never loop more than twice.

**Batch review (not per packet):**
1. After a wave, collect all its packets (and merge them into the PR branch).
2. One reviewer per batch reads the diff for the wave plus the packet files and reports only real problems, ranked: file, line, the rule or checklist item broken, a concrete failing scenario. No style notes. Checklist: handoff section 7.
3. Choose the reviewer tier per section 1: any security-relevant packet in the batch makes it Ultimate; otherwise Performance.
4. The orchestrator splits the findings by packet and sends each finding back to a fresh implementer for that packet (packet + report + the findings, nothing else). One fix round per batch.
5. Re-review only findings that were unclear or high severity, with the cheapest tier that can judge them. Then commit the wave.
6. After each wave, a short "break the app" pass: stopped target, recycled pool, PID reuse, missing counter, counters reset, permission denied, disk full.

## 6. Delivery

- Work on branch `feat/blackbox-expansion` (created from `feat/investigation-timeline`). One PR per group into `feat/investigation-timeline`, never `master`. Commit per wave with a clear message.
- Each PR body: what changed, the packets and their checklist results, what was verified on a real machine (usually nothing, say so), what was only tested with fakes, the exact human test steps, and open questions.
- **Never merge, never enable auto-merge, never push to another branch.** The owner tests on a real server and approves in writing first.
- In `docs/BACKLOG.md`, tick an item only when verified on the real data path. Fake-tested items stay unticked with a note "built, awaiting live check".
- Group 5 (SQL) starts only after BL-123 and BL-124 are built and reviewed at Ultimate tier. Group 6 is last.
- Keep the owner's rules: no dumps, nothing under "deferred" built, no new collector or agent beyond the Blackbox, no `sql/` folder or customer data in the repo (the repo is public).

## 7. Prompt to paste into Qoder

> Clone https://github.com/abhinavjp/unravis and check out `feat/investigation-timeline`:
> `git clone -b feat/investigation-timeline https://github.com/abhinavjp/unravis.git`
> Open the cloned `unravis` folder and work only inside it. Create your branch: `git switch -c feat/blackbox-expansion`. Never push to `feat/investigation-timeline` or `master`.
>
> You are the orchestrator. Use the highest model tier (Ultimate) for yourself. Read, in this order: `CLAUDE.md`, `docs/research/blackbox-expansion-handoff.md`, `docs/research/blackbox-qoder-orchestration.md` (follow it exactly), `docs/BACKLOG.md` (Blackbox expansion, BL-100 to BL-126), `docs/research/blackbox-expansion-prioritised.md`, `docs/research/blackbox-sql-security-review.md`.
>
> Do these in order and write each result to `.forge/blackbox-expansion/`: (1) PLAN.md with technical dependencies, tools and skills used, and the risk table; (2) very small work packets with verification checklists, for every backlog item that can be written; (3) GRAPH.md with waves and what can run in parallel; (4) create the subagents in `.qoder/agents/` (implementer-efficient, implementer-performance, reviewer-performance, reviewer-ultimate) and run `/agents reload`; (5) execute wave by wave: Efficient or Performance implementers per packet (as marked), one batch review per wave (Performance, or Ultimate when a packet touches SQL, credentials, ACLs, the HTTP endpoint or the footprint budget), send findings back to the packet's implementer once, then commit; (6) open one PR per group into `feat/investigation-timeline`.
>
> Give each subagent only its packet and the files it names, never the whole history. Keep STATE.md up to date. Do not tick `docs/BACKLOG.md` items unless verified on real data. Never merge a PR or enable auto-merge: I will test on a real server and approve in writing. If something cannot be checked without a real server, say so in the packet and the PR.
