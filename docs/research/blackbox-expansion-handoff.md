# Blackbox expansion: how to start development and review (handoff for another harness)

Written 2026-11-13. Status: design and reviews done, **nothing built**. Start with the foundation group once the owner says go.

## 1. Read first, in this order

1. `CLAUDE.md` (project rules: finish on live data, no demo-only features).
2. `docs/BACKLOG.md`, section "Blackbox expansion" (BL-100 to BL-126). This is the checklist. Tick an item only when it works on the real data path.
3. `docs/research/blackbox-expansion-prioritised.md` (conditions every item must meet, build order, decisions, unconfirmed items).
4. `docs/research/blackbox-sql-security-review.md` (SQL verdicts, credential design, controls C1-C14, existing code to change first).
5. Raw research, only if you need the source: `docs/research/blackbox-expansion-research-prompt.md` and Google's answer (not in the repo; owner has it as `unravis-blackbox-google-argon-research.txt`). Counter paths in it are **unverified**.
6. Existing code: `internal/blackbox/` (`recorder.go`, `sample.go`, `host.go`, `config.go`, `logs.go`, `server.go`, `service*.go`, `pack.go`).

## 2. What the Blackbox is

A Windows service (SYSTEM) that samples its own machine every 15 s into hourly NDJSON files plus a manifest. A read-only status server sits behind an `X-Blackbox-Key` header. Housekeeping is always on (keepDays 7, maxGB 2, minFreePercent 10). A reading that cannot be taken is left out, never written as 0.

## 3. Rules that cannot be broken

- **Light**: under 0.5% of one core on average, under 50 MB, each counter group within 1 s, below-normal priority, self-throttle (BL-122). A group that breaks the budget ships off or slower.
- **Never pause, restart, attach to or dump a target.** No environment variables, no IIS or service restarts.
- **Owner-selected targets only** for per-process detail (payroll API pools, the email service). Machine-wide counters stay machine-wide.
- **Both runtimes, auto-detected per process**: .NET Framework 4.7.2 (PDH `.NET CLR` counters, BL-109) and .NET 10 (EventPipe `System.Runtime`, BL-110). Detect by loaded modules (`clr.dll` vs `coreclr.dll`/`hostfxr.dll`), not by config (BL-105). Key processes on (PID, creation time).
- **No text**: no query or request text, plan XML, payloads, environment variables, command lines. Event log: IDs and numbers only.
- **SQL is read-only and local only**: fixed allowlisted statements, one grant run by the owner, never granted by the app, no stored password. Separate service `UnravisBlackboxSql` as `NT SERVICE\UnravisBlackboxSql`. Controls C1-C14 are mandatory and each one needs a test.
- **Counter engine rules (BL-121)**: English counter names, two baselines before a rate, valid-status check, multi-core CPU not capped at 100.
- **Unreadable means missing**: show "not running" or "not found", never 0.
- Do not build anything under "Backlog (deferred)" without the owner's go. No new collector or agent beyond the Blackbox without the owner's go.

## 4. Build order (one group at a time, small packets)

1. Foundation: BL-122 self-budget, BL-121 counter engine, BL-117 target selection, BL-105 resolver and runtime detection.
2. Core evidence: BL-104, BL-106, BL-108, BL-107 (with BL-100).
3. Per-process memory and CPU: BL-109, BL-110, BL-116.
4. More host evidence: BL-111, BL-112, BL-114, BL-115, BL-125 (process list: top 10 CPU + top 5 memory + every selected target).
5. SQL sampler, only after BL-123 and BL-124: BL-101, BL-102, BL-118, BL-119, then BL-103.
6. Optional: BL-113, BL-120, BL-126.

Split each group into tiny packets with a dependency graph before coding. Every packet gets tests with fake readers (tests must not need Windows counters).

## 5. Check these on a real server before building the item that needs them

- Exact PDH paths for HTTP.sys, W3SVC_W3WP, WAS_W3WP (`typeperf -q`).
- Whether SYSTEM can open a pool process's `dotnet-diagnostic-{pid}` pipe (decides BL-110).
- Which IIS hosting model (in-process or out-of-process) the .NET 10 API uses on each server.
- Snapshot freeze time and EventPipe cost (no measured figure exists).

## 6. Verify

```bash
go build ./... && go test ./internal/blackbox/... ./internal/server/... -count=1
```

Also run `go vet ./...`. Windows-only code lives in `*_windows.go` with an `*_other.go` twin; keep both building. Frontend (only if you touch it): `cd web && npm test`.

Done means live-verified: a real server, a real reading, the number matches `perfmon` or the tool it replaces. Say plainly in the final report what was verified on live data and what was only tested with fakes. Install locally for the owner to test; no tag, release or release CI until the owner says satisfied.

## 7. Review

- One reviewer per packet group, not per file. Cap review loops: one fixer pass per round, re-run only the reviewers whose findings were unclear.
- Use the strongest model for security review of anything touching SQL, credentials, service accounts, ACLs, or the HTTP endpoint. A mid model is enough for everything else.
- Review checklist: the footprint budget holds (benchmark test), nothing pauses or reads a target's memory, no text captured, missing never 0, selected targets only, runtime detection works for both runtimes, every SQL text passes the lint (C2), errors are fixed codes (C10), `/status` exposes no instance name or error text (C11).
- Add one "break the app" pass after each group (stopped target, recycled pool, PID reuse, missing counter, counters reset, permission denied, disk full).
- UI text added for this work must be understandable to a first-time investigator.

## 8. Code that must change before SQL work (BL-123)

`service.go:274` and `service_windows.go:25-27` (recordings folder readable by BUILTIN\Users), `recorder.go:149-156` (Collect runs inline in the tick loop and writes `err.Error()`), `server.go:90-101` (plain HTTP), `service_windows.go:119-123` (no service account), `config.go:206-208` and `server.go:251` (`probe.enabled` refused: keep it that way until C1-C14 pass), `config.go:317` and `pack.go:122` (`blackbox.json` is world-readable and shipped in the pack: never put credentials in it). Do not copy `internal/dbhealth` as a SQL template (it reads query text and program names).

## 9. Prompt for Qoder (or any IDE agent)

Repo: https://github.com/abhinavjp/unravis (public). Branch with all the docs: `feat/investigation-timeline`. Go, Windows-first. Use Quest mode (or the agent's long-task mode) for the plan; use normal agent mode with checkpoints for the build.

Paste this:

> Clone https://github.com/abhinavjp/unravis and check out the branch `feat/investigation-timeline`:
> `git clone -b feat/investigation-timeline https://github.com/abhinavjp/unravis.git`
> Open the cloned `unravis` folder as the workspace and work only inside it. Create your own branch from it: `git switch -c feat/blackbox-expansion`. Do not push to `feat/investigation-timeline` and do not touch `master`.
>
> Your job is the Blackbox expansion (Go, Windows service, code in `internal/blackbox/`). Read, in this order: `CLAUDE.md`, `docs/research/blackbox-expansion-handoff.md` (follow it), `docs/BACKLOG.md` (section "Blackbox expansion", BL-100 to BL-126), `docs/research/blackbox-expansion-prioritised.md`, `docs/research/blackbox-sql-security-review.md`.
>
> Step 1, no code: give me a packet plan with a dependency graph for the foundation group (BL-122, BL-121, BL-117, BL-105), the tests for each packet, and anything you could not confirm. Stop and wait for my go.
>
> Step 2, after my go: build one packet at a time, tests first, within the footprint budget. Run `go build ./...` and `go test ./internal/blackbox/... -count=1` after each packet. In your report say what was verified on a real machine and what was only tested with fakes. Do not tick anything in `docs/BACKLOG.md` unless it was verified on real data.
>
> Step 3, delivery: push your branch `feat/blackbox-expansion` and open a pull request into `feat/investigation-timeline` (not `master`). In the PR body list: what changed, what was verified on a real machine, what was only tested with fakes, and the steps a human must run to test it. Never merge the PR, never enable auto-merge, and never push to another branch. Merging waits for my explicit written approval after I have tested it myself on a real server.

Review prompt (separate session):

> In the cloned `unravis` repo, review the Blackbox changes on branch `feat/blackbox-expansion` against section 7 of `docs/research/blackbox-expansion-handoff.md`. List only real problems, ranked, with file and line. Do not change code.
