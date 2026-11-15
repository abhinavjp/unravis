# BBX-5-01 Separate SQL service install + locked-down data folder
Backlog: BL-123 | Group: 5 | Model: performance | Reason: service account + ACLs; high security impact.
Depends on: none (uses existing ServiceControl/Env) | Blocks: BBX-5-02, BBX-5-04
Reviewer: Ultimate (credentials, service accounts, ACLs).

## Goal
Install a second small Windows service `UnravisBlackboxSql` (same exe, a SQL-run mode) under its own virtual account `NT SERVICE\UnravisBlackboxSql`, whose data folder `<recordings>\sql` is readable only by SYSTEM, Administrators and that service SID. The host sampler stays LocalSystem. A hung SQL call cannot stall host sampling because SQL runs in a separate process.

## Context
- New files `internal/blackbox/sqlservice.go` (install/remove logic over the existing `ServiceControl`/`Env` interfaces, fake-testable) + `internal/blackbox/sqlservice_windows.go` (SID + SDDL) + `internal/blackbox/sqlservice_other.go` (twin). Test `internal/blackbox/sqlservice_test.go` (fakeSvc/fakeEnv pattern from service_test.go).
- Existing: `ServiceSpec{Name,DisplayName,Description,ExePath,Args}` (service.go:43-49); `ServiceControl` (53-60); `Env` (63-79); `Manager.Install` (228-358) shows the install/undo pattern; `secureSDDL(usersRead)` (service_windows.go:24-30) = `D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)` + BU when usersRead. Per-service SID helper precedent: `internal/keeper/owner.go:14` (S-1-5-80 + SHA-1 of the service name). `mgr.Config{ServiceStartName}` precedent: `internal/setup/os_windows.go:361`.
- Security review (BL-123): SQL data goes to `<folder>\sql` with a DACL of SYSTEM, Administrators and the SQL service SID ONLY (today the recordings folder is readable by BUILTIN\Users — service.go:274). The host recordings folder ACL is NOT changed by this packet (only the new sql subfolder is locked down).
- Rules: **own service account, not SYSTEM, not a stored password**; **SQL data folder readable only by SYSTEM/Administrators/SQL SID**; **the app never grants SQL rights** (grant is owner-run, BBX-5-02); **no credentials in blackbox.json** (pack.go:122).

## Do
1. `sqlservice.go`:
   - Constants: `SQLServiceName = "UnravisBlackboxSql"`, `SQLServiceAccount = "NT SERVICE\\" + SQLServiceName`, `SQLDataSubdir = "sql"`.
   - `type SQLManager struct { Svc ServiceControl; Env Env }`.
   - `InstallSQL(cfg Config) error` — Validate; IsAdmin; refuse if the SQL service already exists; ensure `<cfg.Folder>\sql` exists; apply the locked DACL (below); create the service with `ServiceSpec{Name: SQLServiceName, Args: []string{"sql-service", "--config", configPath}}` and account `SQLServiceAccount`; start it. Use the undo pattern (remove the folder/service on failure).
   - `RemoveSQL(cfg, confirmName) error` — confirm name; stop+delete the service; print the revoke script (BBX-5-02 owns the text); optionally remove the sql folder only when confirmed.
2. `sqlservice_windows.go`:
   - `func sqlServiceSID(name string) (string, error)` — S-1-5-80 + SHA-1 of the UTF-16LE service name (reuse the keeper/owner.go algorithm).
   - `func sqlSDDL(sid string) string` — `D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;<sid>)` — NO BUILTIN\Users, NO Everyone.
   - `func secureSQLDir(dir, sid string) error` — SetNamedSecurityInfo with that SDDL (PROTECTED_DACL), following service_windows.go:35-47.
   - The service Create must set `ServiceStartName = SQLServiceAccount` (extend ServiceSpec with an Account field, or a SQL-specific create path; keep the host service unchanged).
3. `sqlservice_other.go`: methods return "Windows only".
4. Factor the SDDL/SID into pure funcs tested without applying ACLs (like service_windows_test.go:16-36).

## Do not
- Do NOT grant any SQL permission here (C14 grant is owner-run in SSMS, never via the app/elevate helper).
- Do NOT store a password anywhere; the virtual account has no stored secret.
- Do NOT add BUILTIN\Users or Everyone to the sql DACL.
- Do NOT change the host recordings folder ACL or the host service account.
- Do NOT put credentials in blackbox.json or the pack.
- Do NOT run SQL inline in the host recorder (separate service only).

## Tests to write first (fakes)
- `TestSQLSDDLNoUsers` — sqlSDDL contains SY, BA and the SID; asserts it does NOT contain "BU" or "S-1-1-0" (Everyone).
- `TestSQLServiceSID` — known service name → expected S-1-5-80-... format (cross-check the keeper algorithm).
- `TestInstallSQLUsesAccount` — fakeSvc records ServiceStartName == NT SERVICE\UnravisBlackboxSql.
- `TestInstallSQLLocksFolder` — fakeEnv records the sql DACL applied (no usersRead).
- `TestInstallSQLUndoOnFailure` — a failing create removes the created folder/service.
- `TestRemoveSQLConfirm` — wrong confirm name refuses before any change.

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/blackbox/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/blackbox/...)
- [ ] Footprint: install-time only; no goroutine/ticker in the sampler
- [ ] Missing reading is omitted, never 0 (n/a — install)
- [ ] No text/credentials captured; no password stored (code review)
- [ ] SQL DACL = SYSTEM + Administrators + SQL SID only (TestSQLSDDLNoUsers)
- [ ] Own service account, not SYSTEM (TestInstallSQLUsesAccount)
- [ ] App never grants SQL rights (code review)
- [ ] Host service/folder unchanged (existing service tests pass)

## Live-check needed?
YES — owner installs on a real server, then checks `sc qc UnravisBlackboxSql` shows SERVICE_START_NAME = NT SERVICE\UnravisBlackboxSql, and `icacls <folder>\sql` shows only SYSTEM/Administrators/the SQL SID. Fake tests verify logic only.

## Report back
Files changed, each checklist item ticked or failed with output, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-5-01.md`.