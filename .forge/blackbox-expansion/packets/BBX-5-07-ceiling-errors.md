# BBX-5-07 Rights ceiling at every connect (C8) + fixed error codes (C10)
Backlog: BL-124 | Group: 5 | Model: performance | Reason: privilege ceiling + error classification; high security impact.
Depends on: BBX-5-06 | Blocks: BBX-5-10
Reviewer: Ultimate (SQL, privileges).

## Goal
At EVERY connect, refuse a login that has more rights than the Blackbox needs (not sysadmin, no CONTROL SERVER, permissions limited to CONNECT SQL / VIEW ANY DATABASE / VIEW SERVER [PERFORMANCE] STATE). Classify all errors into fixed codes, never driver text.

## Context
- New files `internal/bbsql/ceiling.go` + `errors.go` + tests. Uses the Queryer from BBX-5-06.
- C8: at every connect — not sysadmin, no CONTROL SERVER, and the server permissions list only CONNECT SQL, VIEW ANY DATABASE and VIEW SERVER [PERFORMANCE] STATE. Otherwise refuse with the plain reason "this login has more rights than the Blackbox needs".
- C10: fixed codes only (pattern reference `internal/dbhealth/mssql.go:199 ClassifyConnect` — write a fresh classifier, never copy dbhealth's query code); never write err.Error() to events or status.
- The ceiling check uses fixed control queries (also allowlisted, lint-clean): e.g. `SELECT IS_SRVROLEMEMBER('sysadmin') AS sysadmin, IS_SRVROLEMEMBER('securityadmin') AS securityadmin` and `SELECT permission_name FROM sys.server_permissions WHERE grantee_principal_id = DATABASE_PRINCIPAL_ID()`. These contain no C2 banned token. Add them to the allowlist as `bb:ceiling_role` / `bb:ceiling_perms` (extend BBX-5-05 Statements; keep the lint passing).
- Rules: **rights ceiling at every connect**; **fixed error codes**; **no driver text**; **the app never grants**.

## Do
1. `ceiling.go`: `func CheckCeiling(q Queryer) error` — run the two fixed control queries; in Go assert sysadmin==0, securityadmin==0, and the permission_name set ⊆ {CONNECT SQL, VIEW ANY DATABASE, VIEW SERVER STATE, VIEW SERVER PERFORMANCE STATE}. Any CONTROL SERVER or extra permission or sysadmin → return a fixed error `ErrTooManyRights` with the plain message "this login has more rights than the Blackbox needs". Called on every (re)connect by the runner.
2. `errors.go`: `type Error struct { Code string }` implementing error; `func Classify(err error) *Error` mapping driver errors to fixed codes: login-failed, database, permission, timeout, network, tls, too-many-rights, failed (fallback). NEVER include err.Error() in Code or any surfaced message. Reuse the numeric/phrase approach from mssql.go:199-232 but write fresh.
3. Add the two ceiling control statements to the allowlist (coordinate with BBX-5-05) and ensure the C2 lint still passes for them.

## Do not
- Do NOT skip the ceiling check on reconnect (every connect).
- Do NOT grant or alter rights (read-only check).
- Do NOT surface driver text, instance names, or login names in errors/status.
- Do NOT allow CONTROL SERVER or sysadmin.
- Do NOT use non-allowlisted text for the ceiling queries.

## Tests to write first (fake Queryer)
- `TestCeilingAllowsMinimal` — permissions exactly {CONNECT SQL, VIEW SERVER PERFORMANCE STATE}, not sysadmin → ok.
- `TestCeilingRefusesSysadmin` — sysadmin==1 → ErrTooManyRights.
- `TestCeilingRefusesControlServer` — permission CONTROL SERVER present → ErrTooManyRights.
- `TestCeilingRefusesExtraPermission` — an extra permission (e.g. ALTER ANY DATABASE) → ErrTooManyRights.
- `TestClassifyFixedCodes` — various driver errors → fixed codes; assert no code/message contains the raw driver text.
- `TestCeilingQueriesLintClean` — the two control statements pass the C2 lint.

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/bbsql/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/bbsql/...)
- [ ] Footprint: two small queries per connect; no goroutine/ticker
- [ ] No text captured — errors are fixed codes (TestClassifyFixedCodes)
- [ ] C8 ceiling at every connect; refuses sysadmin/CONTROL SERVER/extra (tests above)
- [ ] C10 fixed codes only; ceiling queries lint-clean
- [ ] App never grants (code review)

## Live-check needed?
YES — owner connects with the granted virtual account on a real SQL Server and confirms the ceiling passes; then with a sysadmin login confirms it refuses with the plain reason. Fake tests verify logic only.

## Report back
Files changed, each checklist item ticked or failed with output, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-5-07.md`.