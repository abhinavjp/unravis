# Wave 4 binding fix — W4-01, W4-02, W4-06

Scope: `internal/blackbox/resolve_map.go`, `sqlhost.go`, and ONE new test file
`target_binding_test.go` (LF). No other file was edited. `resolve_map_windows.go`,
`resolve_map_other.go`, `resolve_map_test.go`, `sqlhost_test.go` were in scope but
needed no change. No commit/push. No live process/service/counter operation, no
upload. Both build tags compile. Pre-existing files kept CRLF; the new test is LF.

## Per-finding change

### W4-01 — failed refresh permitted cross-process rebinding (High)
- Each cached mapping is now BOUND to the identity it was verified at:
  `instBinding{pid, start, gen}` for W3WP instances and `svcProc.start/gen` for
  services. `start` is `identityTicks` (lossless FILETIME, PID + creation); `gen`
  is the discovery generation. Only instances/services whose PID is live with a
  readable creation time are cached at enumeration.
- A failed enumeration INVALIDATES the cached list and its bindings (fail-closed):
  `mapInst`/`mapSvc` are cleared, so a stale name can never be rebound to a reused
  PID. At match time the bound identity must still equal the live inventory
  identity (`b.start == identityTicks(p)` / `s.start == identityTicks(p)`), a
  second fail-closed gate.
- Retry is a bounded backoff (`mapRetryDelay`, 15 s doubling to a 2 min cap)
  independent of unrelated churn: after a failure the list is re-enumerated when
  the backoff comes due even on a stable process set, and never per tick.
- Fixed availability codes (`TargetStatus`, `tickView.status`): `not-running`,
  `unavailable` (enumeration failed / unverified), `ambiguous`. No provider text.

### W4-02 — enumeration order substituted for ownership (High)
- `matchPool` collects ALL live, identity-confirmed workers of the selected pool
  (deduped by identity). More than one worker (web garden / overlap) is
  `ambiguous` — no target, never silently first-wins.
- A runtime-0 worker targets its out-of-process .NET 10 child only when the child
  was created AFTER the worker (`identityTicks(child) > identityTicks(worker)`);
  an orphan whose ParentPID was reused (child older than the current parent) is
  rejected. More than one legitimate child is `ambiguous`.
- Worker identity is kept separate from application-child identity: the worker is
  validated, the child (out-of-process) or the worker (in-process) becomes the
  target; IIS continues to carry per-worker identity from its own verified query.
- Resolution is stable under reversed enumeration order. No command-line capture.

### W4-06 — one SQL counter failure became lifetime group loss (High)
- Each discovered instance gets its OWN source. If an instance's full counter set
  cannot be added, its readable counters are preserved by probing per counter and
  building one source from the readable subset. Healthy instances/counters stay
  readable; an unreadable counter has no spec and stays missing, never 0.
- Fixed diagnostics: `errSQLHostUnavailable` (group) and `availability()`
  (instance label -> `unavailable`). No raw provider text is returned.
- Dormant bounded retry builds on the existing `lazyGroup`: only when NOTHING is
  readable does the factory return the fixed error, so `BuildGroups` keeps its
  retry slot. A partially-degraded group recovers missing counters on a bounded
  `groupRetryDelay` backoff inside `Collect`, independent of churn. The engine is
  reconciled (not rebuilt) so unchanged counters keep their rate baselines.

## Identity / generation binding contract
A cached pool/service mapping is usable only while the live inventory still
confirms the exact `(PID, StartTicks)` it was enumerated against, and it records
the discovery generation it was verified at. Identity change (PID reuse) or a
failed enumeration invalidates the mapping; the name is then reported with a fixed
code and re-resolved only after a successful re-enumeration (on process-set change
or the bounded backoff). `Start` stays unix seconds on the wire; `StartTicks` is
the lossless identity used for every binding/cache decision.

## Commands / results (Go 1.27.3, GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local)
- `go build ./...` — PASS (first attempt hit a transient `internal/server` compile
  error from another worker's in-progress refactor; re-run passed, no edit by me).
- `go vet ./internal/blackbox/` — PASS.
- `GOOS=linux go vet ./internal/blackbox/` — PASS.
- `go test ./internal/blackbox/ -count=1 -timeout 400s` — PASS (`ok`, ~124 s). One
  run hit a `TestClumsy…` "server never listened" timing flake (unrelated file);
  it passed in isolation and the full re-run was green. No pre-existing assertion
  needed updating — the whole package stayed green without editing existing tests.
- New tests (all pass): service/pool last-good + PID reuse + refresh failure emit
  NO target; bounded-backoff recovery on a stable set; ambiguous workers/children
  never first-wins (both orders); older orphan child rejected; single-worker order
  stability; one bad SQL counter keeps default+named healthy and recovers without
  restart; unavailable-instance and total-failure fixed diagnostics.

## Conflicts / constraint-driven choices (reported, not edited around)
- The `Resolver` struct lives in `resolve.go` (out of scope), so the binding /
  backoff / status state is kept in a per-resolver side table (`mapBindStates`,
  keyed by `*Resolver`, accessed only under `Resolver.mu`). One entry per resolver
  (one per recorder run); no field was added to `Resolver`.
- Resolving ALL web-garden workers (instead of marking ambiguity) would require
  `clr.go`/`proc10.go`/`procio.go`/`iis.go` to consume multiple targets per key;
  those are out of scope, so ambiguous pools fail closed (task-sanctioned).

## Live-only items (not established by these fake-seam tests)
- Which installed SQL provider/counter actually returns the add error, and the
  real per-counter probe cost on a degraded instance (footprint budget).
- Whether a deployed web garden/overlap layout needs all-worker evidence (currently
  fail-closed ambiguous) vs. the IIS group's own overlapping-worker rows.
- Non-English Windows object/instance names; real PID-reuse + enumeration-failure
  frequency; PDH rate/type/unit semantics. None of these was exercised live.