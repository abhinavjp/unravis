# BBX-5-06 Connection controls: session options, timeouts, one connection, local-only (C3/C4/C5/C12)
Backlog: BL-124 | Group: 5 | Model: performance | Reason: SQL session/connection security controls; high impact.
Depends on: BBX-5-05 | Blocks: BBX-5-07, BBX-5-10
Reviewer: Ultimate (SQL, credentials).

## Goal
Open exactly one local SQL connection with the mandatory session options, strict timeouts, and a local-only target check. Encrypt on. No stored password (Windows virtual account).

## Context
- New files `internal/bbsql/conn.go` + `conn_test.go`. Driver: `github.com/microsoft/go-mssqldb` (already in go.mod). Reference (do NOT copy dbhealth): `internal/dbhealth/mssql.go` ConnectionURL/OpenSQL (34-121) shows the connector pattern; it sets MaxOpenConns(1) but NO lock timeout/deadlock priority — this packet adds the C3 prefix via the statement text (BBX-5-05) and the controls below.
- C3: session options on every batch — the SET prefix is already in every allowlisted statement (BBX-5-05). A live test reads dm_exec_sessions (deadlock_priority -5, isolation 1, lock_timeout 1000).
- C4: connect 5 s; each query 3 s (15 s items) or 5 s (60 s items); whole tick 5 s. A running tick means the next is skipped and counted, never queued or run in parallel.
- C5: max open 1, lifetime 1 h, app name "Unravis Blackbox SQL"; live test: at most one session with that name (exclude own session).
- C12: target only `.`, `(local)`, `localhost`, `127.0.0.1`, `::1` or `lpc:`, plus an optional instance name. Encrypt on. Remote SQL rejected.
- Rules: **local SQL only**; **no stored password**; **one connection**; **fixed timeouts**; **encrypt on**.

## Do
1. `func CheckLocalTarget(server string) error` — accept only the C12 hosts (case-insensitive), optionally `host\instance` or `lpc:host\instance`. Reject anything else (remote names, IPs other than loopback) with a fixed code "remote-target". Pure, table-tested.
2. `type ConnConfig struct { Server string; QueryTimeout time.Duration }`. `func Open(cfg ConnConfig) (*sql.DB, error)`:
   - CheckLocalTarget first.
   - Build a go-mssqldb connector: integrated auth (no username/password), `app name=Unravis Blackbox SQL`, `connection timeout=5`, `encrypt=true` (or strict), `dial timeout=5`.
   - `db.SetMaxOpenConns(1); db.SetMaxIdleConns(1); db.SetConnMaxLifetime(time.Hour)`.
   - Ping with a 5 s context. On failure return a fixed-code error (BBX-5-07 classifier), never driver text.
3. `func Query(ctx, db, text, timeout) (...)` — only allowlisted text (call bbsql.IsAllowed; refuse otherwise); apply a per-query context timeout (3 s or 5 s passed in). The runner (BBX-5-10) enforces the whole-tick 5 s and skip-if-running.
4. Provide a `Queryer` interface (`Query(ctx, text) ([]Row, error)`) so samplers/runner test with a fake; the real impl wraps *sql.DB.

## Do not
- Do NOT accept a remote target (C12) — reject before connecting.
- Do NOT store or accept a password; integrated auth only.
- Do NOT open more than one connection; do NOT run queries in parallel.
- Do NOT send non-allowlisted text (IsAllowed gate).
- Do NOT surface driver error text (use fixed codes).
- Do NOT disable encryption.

## Tests to write first
- `TestCheckLocalTarget` — accepts ./(local)/localhost/127.0.0.1/::1/lpc: + instance; rejects "sqlserver.prod", "10.0.0.5", "host.database.windows.net".
- `TestOpenOneConnection` — fake/real db asserts MaxOpenConns==1 (use a sql.DB against a stub or assert the settings via a constructor seam).
- `TestQueryRefusesNonAllowlisted` — Query with a non-allowlisted text → refused, never sent (fake Queryer records nothing).
- `TestQueryTimeout` — a fake slow query is cancelled at the timeout (context deadline).
- `TestAppNameAndEncrypt` — the connection string contains app name=Unravis Blackbox SQL and encrypt=true (assert the built DSN via a seam).

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/bbsql/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/bbsql/...)
- [ ] Footprint: one connection; no parallel queries; no goroutine/ticker here
- [ ] No text captured; no stored password (code review)
- [ ] C12 local-only (TestCheckLocalTarget); C5 one connection (TestOpenOneConnection)
- [ ] C4 timeouts (TestQueryTimeout); C1 allowlist gate (TestQueryRefusesNonAllowlisted)
- [ ] Encrypt on; app name correct (TestAppNameAndEncrypt)

## Live-check needed?
YES — C3/C5 live tests on a real SQL Server: read dm_exec_sessions for the "Unravis Blackbox SQL" session and confirm deadlock_priority=-5, isolation=1, lock_timeout=1000, and at most one such session. Fake tests verify settings only.

## Report back
Files changed, each checklist item ticked or failed with output, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-5-06.md`.