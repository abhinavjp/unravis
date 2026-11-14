# BBX-5-02 SQL service CLI mode + owner-run grant/revoke scripts (C14)
Backlog: BL-123, BL-124 (C14) | Group: 5 | Model: performance | Reason: service control + credential-adjacent grant text; high security impact.
Depends on: BBX-5-01 | Blocks: BBX-5-10
Reviewer: Ultimate.

## Goal
Add the same-exe SQL run mode and install/remove commands, and PRINT the owner-run grant and revoke scripts. The app never executes a grant.

## Context
- Files: `cmd/unravis-blackbox/main.go` (command switch 205-268, usage 36-58, answer.ok/fail 103-146), a new `cmd/unravis-blackbox/sqlservice_windows.go` (+ `sqlservice_other.go`) for the `sql-service` run mode, following the existing `service_windows.go:21-100` pattern (`svc.Run(SQLServiceName, handler)`).
- Uses `SQLManager.InstallSQL/RemoveSQL` (BBX-5-01) and the SQL runner (BBX-5-10, internal/bbsql). Until the runner lands, the sql-service mode may call a stub that the runner packet replaces — keep it compiling.
- C14 (security review): grant run by the owner in SSMS, NEVER through the installer or the elevated helper (`elevate_windows.go:56`).
- Rules: **one grant run by the owner, never granted by the app**; **no stored password**; **local SQL only**; **fixed error codes** (answer.fail uses reason codes, not driver text).

## Do
1. `main.go`: add commands `install-sql` (calls SQLManager.InstallSQL, then PRINTS the grant script), `remove-sql` (calls RemoveSQL, PRINTS the revoke script), and hidden `sql-service` (runs the SQL service). Add usage lines. JSON answers use the existing ok/fail reason-code shape.
2. Grant script text (printed, not executed), constant in a new `cmd/unravis-blackbox/grant.go`:
   ```
   -- Run in SSMS as an admin. The Blackbox never runs this for you.
   CREATE LOGIN [NT SERVICE\UnravisBlackboxSql] FROM WINDOWS WITH DEFAULT_DATABASE=master;
   GRANT VIEW SERVER PERFORMANCE STATE TO [NT SERVICE\UnravisBlackboxSql];
   -- SQL Server 2019 and older: use VIEW SERVER STATE instead.
   ```
3. Revoke script text (printed by remove-sql):
   ```
   -- Stop the service first, then run in SSMS:
   REVOKE VIEW SERVER PERFORMANCE STATE FROM [NT SERVICE\UnravisBlackboxSql];
   DROP LOGIN [NT SERVICE\UnravisBlackboxSql];
   ```
4. `sql-service` mode: `svc.Run(SQLServiceName, handler)`; handler loads config, runs the SQL runner (BBX-5-10) under the service account; AcceptStop/Shutdown; no HTTP server in this mode.
5. install-sql must NOT call the elevate helper to run SQL, and must NOT execute the grant. It only creates the service and prints the script.

## Do not
- Do NOT execute the grant/revoke from the app or the elevated helper.
- Do NOT store a password or put credentials in blackbox.json.
- Do NOT start an HTTP server in sql-service mode.
- Do NOT write driver error text to answers/events (fixed reason codes).
- Do NOT allow a remote SQL target (C12 is enforced in the runner/conn, BBX-5-06).

## Tests to write first
- `TestGrantScriptPrintedNotExecuted` — install-sql output contains the CREATE LOGIN/GRANT text; fake executor records NO SQL execution.
- `TestRevokeScriptPrinted` — remove-sql output contains REVOKE/DROP LOGIN.
- `TestSQLServiceModeNoHTTP` — the sql-service handler builds no HTTP server (assert via a fake runner).
- `TestInstallSQLNoElevateGrant` — the elevate helper is never invoked to run SQL (fakeElev records calls).
- `TestGrantScriptLint` — the grant text contains only the allowed statements (no INSERT/UPDATE/EXEC etc. beyond CREATE LOGIN/GRANT).

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./cmd/unravis-blackbox/... ./internal/blackbox/... -count=1 passes
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./cmd/unravis-blackbox/...)
- [ ] Footprint: no goroutine/ticker beyond the service run loop
- [ ] No text/credentials captured; no stored password
- [ ] Grant/revoke printed, never executed by the app (TestGrantScriptPrintedNotExecuted, TestInstallSQLNoElevateGrant)
- [ ] No HTTP server in sql-service mode (TestSQLServiceModeNoHTTP)
- [ ] Fixed error codes in answers

## Live-check needed?
YES — owner runs install-sql on a real server, runs the printed grant in SSMS, starts the service, and confirms it connects with only the granted rights. Fake tests verify the app never grants.

## Review-pinned requirement (Ultimate SQL gate, wave 7 — MUST apply)
Wire `SQLManager` to `blackbox.WinService{}` DIRECTLY. Do NOT route through `internal/setup/plan.go:549-551`'s `bbSvc` adapter — it copies only Name/DisplayName/Description/ExePath/Args and DROPS `Account`/`SIDUnrestricted`, which would create the SQL service as LocalSystem (breaking the own-virtual-account rule and making the SID-granted DACL pointless). Add a test asserting the created SQL service spec carries `Account = NT SERVICE\UnravisBlackboxSql` and `SIDUnrestricted` (use fakeSvc to record the spec).

## Report back
Files changed, each checklist item ticked or failed with output, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-5-02.md`.