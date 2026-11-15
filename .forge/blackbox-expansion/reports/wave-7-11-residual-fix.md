# Wave 7–11 SQL residual fix — per-physical-connection ceiling, cancellable connect, statement identity

Date: 2026-11-15. Branch `feat/blackbox-expansion`. Companion to
[wave-7-8-9-control-fix.md](wave-7-8-9-control-fix.md) (the "NOT verified closed"
list) and [wave-10-11-data-fix.md](wave-10-11-data-fix.md) (conflicts/seams).

Scope: `internal/bbsql/{conn,allowlist,output,schedulers}.go` and their tests,
plus ONE new file `internal/bbsql/sqlresidual_test.go`. `ceiling.go` and
`filestats.go` needed **no change** (the C8 proof was already correct; the AG fix
is purely a SQL predicate, so the AG decode is untouched). `runner.go`,
`backoff.go`, `requests.go`, `waits.go`, `errors.go`, `folder*.go`,
`budget_test.go`, `internal/blackbox/*`, `internal/server/*`, `cmd/*`, `web/*`
were **not** edited. No live SQL/service operation, no credential access, no
upload, no commit/push. Two documentation fetches used (Microsoft Learn: the two
DMV column tables) to confirm `pool_id` and `is_local` before changing SQL.

## What was already landed vs. what this pass added

The control-plane code for W8-01/W8-02/W8-06 was already in `conn.go`
(`OpenContext`, `ceilingConnector`, `guardedConn`, `pinnedQueryer`,
`ConnectFuncContext`, `DefaultConnectContext`) with driver-boundary tests in
`sqlcontrol_conn_test.go`. The wave-7-8-9 report listed these as **unverified**,
not unimplemented. This pass (a) adds the missing **Runner-level** proof and an
end-to-end `OpenContext` seam, and (b) makes the **W11-02/W11-03 statement
changes**, which were genuinely not made before.

## Per-item changes

### W8-02 / W9-02 — per-physical-connection ceiling (proved)
- `conn.go`: added the `connectInner` test seam (a `func(mssql.Dialer)
  driver.Connector`, nil in production → real go-mssqldb) and wired it into
  `OpenContext` (`ceilingConnector{cfg: mcfg, inner: connectInner}`). This lets a
  fake `database/sql/driver` drive the **full** `OpenContext → ceilingConnector →
  guardedConn` path, not a stub that bypasses admission. Production behaviour is
  unchanged (nil seam).
- `sqlresidual_test.go` (new): a fake generation driver records a numbered
  physical connection per `Connect` and every SQL text sent on each. The **real
  Runner** with the **real production samplers** is driven over it:
  - `TestRunnerSilentExpiryEveryGenerationProvesCeiling` — `ResetSession` returns
    `driver.ErrBadConn` (a transport that died while idle: the one-hour expiry /
    pool-replacement path). Across three ticks every replacement generation is
    asserted to send **both** ceiling controls before any sampler.
  - `TestRunnerMidTickBadConnEveryGenerationProvesCeiling` — `bb:requests`
    returns a one-shot `driver.ErrBadConn` **mid-tick** (after ag/filestats ran);
    database/sql discards the generation and retries the same statement on a new
    one. Asserts the retry crossed a generation boundary (requests sent ≥ 2×),
    the tick still succeeded, and **every** generation proves the ceiling first.
  - Both assert every wire text is allowlisted (no driver Ping, no untagged SQL).
- The existing `TestEveryGenerationProvesCeilingFirst` /
  `TestOverprivilegedGenerationNeverSamples` (DBQueryer boundary) still pass.

### W8-01 — cancellable connect (conn side proved; runner seam reported)
- `conn.go` already threads the caller context through the whole connect
  lifecycle (`OpenContext` → `context.WithTimeout(ctx, connectTimeout)` →
  bounded `msdsn.Parse` → `connectGuarded` watchdog → clamped loopback dialer →
  ceiling admission). `Open` is the context-free compatibility form;
  `ConnectFuncContext` / `DefaultConnectContext` are the context-aware seam.
- `sqlresidual_test.go`: `TestOpenContextCancellableConnect` injects a driver
  whose `Connect` **ignores its context** (stuck prelogin/TLS). `OpenContext`
  returns within the caller budget with the fixed `timeout` code, and the
  orphaned connection is closed when it finally answers (not leaked).
- **The runner-side plumbing is NOT wired** (runner.go is not mine). See the
  exact seam below. Until it is, the production default connect is still rooted
  in `context.Background()` and only the post-connect `ctx.Err()` guard applies.

### W8-06 — no driver Ping (proved end to end)
- `guardedConn` does not forward `driver.Pinger`; `OpenContext` acquires the
  first connection with `db.Conn` (no SQL of its own).
- `sqlresidual_test.go`: `TestOpenContextStartupTranscriptOnlyApprovedSQL` proves
  the startup transcript on the first physical connection is **exactly**
  `[bb:ceiling_role, bb:ceiling_perms]` — no `select 1;`, no untagged text —
  through the real admission path (stronger than the prior stub test).

### W11-02 — semaphore composite identity (statement + decode + output)
- `allowlist.go` `stmtSemaphores`: projects the numeric `pool_id` and orders by
  the composite — `SELECT TOP 17 pool_id, resource_semaphore_id, … ORDER BY
  pool_id, resource_semaphore_id`. TOP cap+1 (17) unchanged. Confirmed against
  the DMV doc: `resource_semaphore_id` is **Nonunique** (0 regular / 1 small-query)
  and repeats in every pool; `pool_id` is `int` NOT NULL.
- `schedulers.go`: new `semaphoreKey{poolID, semID}` baseline map (was
  `map[int32]`), `semaphorePoolID` reader, and `noPoolID = -1` sentinel. The
  numeric pool id is carried through the raw row (`semaphoresRaw.PoolID *int64`),
  the baseline key, and the output. A readable pool id keys the row to its pool;
  an absent/NULL/out-of-int32 pool id falls back to the sentinel (which cannot
  collide with a real pool id ≥ 0 and is **never emitted**). The fixed statement
  always projects the NOT NULL `pool_id`, so production rows always key
  compositely; the fallback only preserves legacy pool-less fixtures.
- `output.go`: `Semaphores.PoolID *int32 json:"poolId,omitempty"` (numeric id,
  **not** a pool name — C9); `columnKeys["pool_id"]="poolId"`; `poolId` added to
  the `Semaphores`/`semaphoresRaw` row specs as optional+nullable. Unreadable
  identity is **missing (omitted), never 0**.
- Tests: `TestSemaphoresCompositePoolIdentity` (same sem id in two pools keeps
  independent baselines/deltas), `TestSemaphoresPoolIDMissingNeverZero`,
  `TestSemaphoresPoolIDUnreadableIsMissing`; the JSON-key test now allows the
  numeric `poolId` and exercises it. Existing `prev`-index assertions updated to
  the composite key. `budget_test.go` (pool-less fixtures) still passes.

### W11-03 — AG local-row predicate (statement + lint)
- `allowlist.go` `stmtAG`: added the fixed `WHERE is_local = 1`. Confirmed against
  the DMV doc: it returns the local database row **and** extra rows for its remote
  secondary copies, so `database_id + synchronization_state` alone cannot tell
  them apart. `is_local` is a nullable bit; `= 1` excludes NULL (unreadable
  locality is never sampled). No replica server name, no `availability_replicas`,
  no new output column (the AG decode is unchanged). TOP cap+1 (65) unchanged.
- `lintStatement`: a new id-specific rule refuses `bb:ag` without the
  `is_local = 1` token sequence (`hasLocalRowPredicate`), enforced at `init` like
  the rest of the C2 grammar.
- Tests: `TestAGLocalRowPredicate` (predicate present, no replica-identity token,
  lint rejects the dropped/`= 0`/`IS NOT NULL` mutants) and
  `TestSemaphoresCompositeIdentity` (projection + composite ORDER BY).

## Connection-generation contract

A *generation* is one physical `database/sql` connection. `ceilingConnector.Connect`
admits **every** generation: it builds the clamped loopback dialer, runs the
driver connect under the caller-context watchdog, then runs `CheckCeiling`
(`bb:ceiling_role` then `bb:ceiling_perms`) **on that exact connection** via
`pinnedQueryer` before returning it to the pool. A failed/unreadable proof closes
the connection and surfaces the fixed C8-origin error; no sampler text can reach
an unproved generation. Pool expiry (`SetConnMaxLifetime`), a failed
`ResetSession`, and `ErrBadConn` retries all route through `Connect` again, so a
replacement generation is always re-admitted. The Runner's own `reconnect`
`CheckCeiling` is a second, belt-and-braces proof on the admitted connection.

**Observed churn (pre-existing, conservative, not changed):** `conn.go`'s
`watchBudget` retires a physical connection when the per-query context's deferred
`cancel()` races a finished statement (documented there as "only retires the
connection early … the next use re-proves the ceiling"). Under the fake driver
this mints a fresh, freshly-admitted generation for almost every statement. The
contract holds on every generation; the new tests assert the **contract**, never a
fixed generation count/index. This churn is a C13 cost question for the owner
(re-admission = 2 control queries per recycle) under a real driver, not a
correctness gap; it was left as landed.

## Exact seam runner.go needs (NOT edited here)

`runner.go` still roots the default connect in `context.Background()`:
- `runner.go:192` `type ConnectFunc func(ConnConfig) (Queryer, func() error, error)` — no ctx.
- `runner.go:194` `defaultConnect` calls `Open(cfg)` → `OpenContext(context.Background(), cfg)`.
- `runner.go:229` `Runner.Connect ConnectFunc`; `:331` `connectFn()`; `:530`
  `reconnect` calls `r.connectFn()(r.Conn)` and only checks `ctx.Err()` **after**.
- `cmd/unravis-blackbox/sqlrunner.go:82` uses `r.Run(ctx)` with `r.Connect = nil`,
  so production takes the `defaultConnect` → Background path.

**Additive change required** (the field type must NOT change: `budget_test.go:310`
and `runner_test.go:156/325/446/954/970` assign `r.Connect = func(ConnConfig)…`):
1. Add a field `ConnectContext ConnectFuncContext` to `Runner` (the context-aware
   seam already exported from `conn.go`).
2. In `reconnect`, resolve connect in this order: if `r.ConnectContext != nil`
   call `r.ConnectContext(ctx, r.Conn)`; else if `r.Connect != nil` adapt it
   (call with `r.Conn`, keep the existing post-connect `ctx.Err()` guard); else
   call `DefaultConnectContext(ctx, r.Conn)`.
3. Equivalently, make the default `defaultConnect` context-aware
   (`DefaultConnectContext`). No `cmd/*` change is then needed: `Run(ctx)`'s
   context reaches `OpenContext`, bounding `msdsn.Parse`, dial, prelogin/TLS,
   auth and ceiling admission, and service Stop cancels an in-progress connect.

Until this lands, W8-01 is proven **at the conn seam** but not threaded through
the production runner default.

## Verification (exact commands, this tree)

Go: `C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/.dev/toolchains/go/bin/go.exe`
(go1.27.3 windows/amd64), `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`.

| Command | Result |
|---|---|
| `go build ./...` | exit 0 |
| `go vet ./internal/bbsql/` | exit 0 |
| `GOOS=linux go vet ./internal/bbsql/` | exit 0 |
| `go test ./internal/bbsql/ -count=1 -timeout 400s` | ok ~36 s |
| `go test ./internal/bbsql/ -run TestSchedulersSemaphoresTempdbRegistered -count=2` | ok |
| residual tests `-count=20` (stress) | ok (no flake) |

`gofmt` clean on every edited file (verified on LF-normalized copies so CRLF is
not flagged). Endings preserved per file: control-plane `conn.go`,
`allowlist.go`, `allowlist_test.go` stay **CRLF**; data-plane `output.go`,
`schedulers.go`, `output_test.go`, `schedulers_test.go` stay **LF** (their
pre-existing convention, per wave-10-11); the new `sqlresidual_test.go` is **LF**.
No file has mixed endings. The whole repo suite was **not** run (per instruction).

## Live-only items (Unverified Assumption until the owner runs them)

- **W9-01 effective rights**: custom/nested roles, Windows groups, public and
  SQL 2022 `##MS_*##` coverage needs a real SQL Server matrix.
- **DMV result metadata**: `pool_id` int NOT NULL projection, `is_local`
  local/remote row shape and the sync-state domain were confirmed against Microsoft
  Learn, not a live instance; actual nullability/row shape under the granted
  `master` default still needs a live check.
- **Real driver timing**: the fake proves the watchdog/clamp logic, not the real
  go-mssqldb prelogin/TLS/cancel-drain latency under the 5 s budget; the
  `watchBudget` early-retirement churn's C13 cost under a real driver is unmeasured.
- **W8-03 install/activation ordering** with a real elevated install + delayed
  owner grant.
- **C13 "<2 KB per sample"** remains UNMET at the C6 row caps (owner decision open;
  not relaxed here). The added numeric `poolId` widens a semaphore line by a few
  bytes; it does not change that ruling.
- Race detector still unrun (no cgo/gcc on this host).