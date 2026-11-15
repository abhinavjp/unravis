# BBX-5-04 /status SQL shape (C11) — no SQL data over HTTP
Backlog: BL-123 | Group: 5 | Model: performance | Reason: HTTP endpoint surface; high security impact.
Depends on: BBX-5-01 | Blocks: BBX-5-10
Reviewer: Ultimate (HTTP endpoint).

## Goal
Expose ONLY a minimal SQL status object on the host `/status` (enabled, state, lastOK, failures) and never serve SQL sample data over the plain-HTTP endpoint. No instance name, version, or error text.

## Context
- File `internal/blackbox/server.go`: `StatusDoc` (29-52), `handleStatus` (241-258, probe gate hardcoded false at 251), plain HTTP (90-101). Test `internal/server`... actually `internal/blackbox/server_test.go` (srvRig).
- The SQL runner (BBX-5-10) writes `<folder>\sql\status.json` containing exactly `{enabled, state, lastOK, failures}` (state ∈ on/backing-off/auto-off/denied). The host service runs as SYSTEM and can read that DACL-protected file.
- Security review C11: `/status` only `sql: {enabled, state, lastOK, failures}`. No instance name, version or error text. C12/BL-123: no SQL data over plain HTTP → decision: the SQL service has NO HTTP server, and the host endpoint serves NO SQL samples (only this status object).
- Rules: **no SQL data over plain HTTP**; **/status exposes no instance name or error text** (C11); **fixed states**; **probe.enabled stays refused** (config.go:206-208, server.go:251).

## Do
1. Add `type SQLStatus struct { Enabled bool `json:"enabled"`; State string `json:"state"`; LastOK *time.Time `json:"lastOK,omitempty"`; Failures int `json:"failures"` }`.
2. Add `SQL SQLStatus `json:"sql"`` to StatusDoc.
3. In handleStatus, read `<cfg.Folder>\sql\status.json` (best-effort): if missing/unreadable → `SQL{Enabled:false, State:"off"}`. If present, decode into SQLStatus but WHITELIST state to {on,backing-off,auto-off,denied,off}; reject any extra fields (DisallowUnknownFields) so no instance/error text can leak. Never include instance name/version/error text.
4. Do NOT add any endpoint that serves SQL sample NDJSON. The `/files/` handler must not serve the sql subfolder (it already only serves ValidFileName bb-*.ndjson from Store.Dir(); confirm the sql subfolder is excluded).
5. Keep probe gate hardcoded false (251) and the probe.enabled config refusal unchanged.

## Do not
- Do NOT serve SQL sample data over HTTP (no new endpoint, no sql files via /files/).
- Do NOT include instance name, server version, or error text in /status.
- Do NOT enable TLS here (the chosen control is "keep SQL data off the endpoint"); do not change the bind default.
- Do NOT relax the probe gate.
- Do NOT trust extra fields from status.json (whitelist + DisallowUnknownFields).

## Tests to write first
- `TestStatusSQLShape` — status.json present → /status sql has only enabled/state/lastOK/failures.
- `TestStatusSQLNoInstanceOrError` — a status.json with extra "instance"/"error" fields → rejected/ignored; /status contains no instance/error text.
- `TestStatusSQLMissingOff` — no file → sql.enabled false, state off.
- `TestStatusSQLBadState` — unknown state → normalized to off (whitelist).
- `TestFilesDoesNotServeSQL` — a request for a sql-subfolder file via /files/ is refused (404).
- `TestProbeStillRefused` — probe.enabled true still fails config validation; /status probe.enabled false.

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/blackbox/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/blackbox/...)
- [ ] Footprint: one small file read per /status (on demand), no goroutine/ticker
- [ ] Missing reading is omitted, never 0 (missing status → enabled false)
- [ ] No text captured — no instance/version/error in /status (TestStatusSQLNoInstanceOrError)
- [ ] No SQL sample data over HTTP (TestFilesDoesNotServeSQL)
- [ ] probe gate unchanged (TestProbeStillRefused)

## Live-check needed?
YES — owner curls /status on a real server with the SQL service running and confirms only the four sql fields appear, no instance/error text. Fake tests verify shape only.

## Report back
Files changed, each checklist item ticked or failed with output, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-5-04.md`.