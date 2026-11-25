# mcp-aliases (BL-349, with the cursor and handle-bound parts of BL-350)

PR #269 (base develop, branch claude/mcp-aliases). SECURITY-SENSITIVE. Not ticked in BACKLOG (note added).

## Built
- Evidence aliases `ev1_`+26 base32 (130 random bits): `internal/mcpaudit/aliases.go` (table, sealed record id AES-256-GCM, keyed-digest lookup, mint in one transaction that needs the profile active at the revision, 7d idle / 30d cap / 2000 per profile), `internal/mcpsrv/alias.go` (Minter, Resolve for a host, ResolveOwner for the app, Drop hooked to revoke/reissue/stop/case lock), `internal/mcp/evidence.go` (rows carry `evidenceRef`).
- Signed cursors `internal/mcpsrv/cursor.go` (HMAC, bound to tool, profile, revision, sources/tier/identifiers/bridge generation, query fingerprint, 10 min; one fixed `result_expired` text).
- Handle limits: one comparison per host, 64 MiB per result, 128 MiB in all (LRU), Sweep after calls and every minute; 24 h prune timer. `internal/mcpsrv/backend.go`.
- App-only resolver route `GET /api/mcp/evidence/{alias}` (`internal/server/mcp_evidence.go`), wired in `cmd/audit-explorer/mcpmode.go`.
- Guide steps 6/10 cite evidenceRef; goldens updated (guide.sha256, catalog.sha256, unravis.d.ts). Spec 03 note added.

## Verified (Linux)
go test -race internal/mcp, mcpsrv, mcpaudit pass. internal/server, codemode, cmd/audit-explorer: only baseline (Windows-only) failures (checked against docs/desktop/baseline-go-tests.txt). vet and gofmt clean. golangci-lint locally cannot load the config (built with go1.25); CI lint is the check.

## Not verified / leftovers
Windows, live PC, real hosts. "Open evidence" UI field, ledger links and the Slow call screen link not built (depend on unbuilt screens). No tool accepts an alias yet, so `Aliases.Resolve` has no caller. Dedupe of in-flight comparisons not built.

## Decisions
Record id stored sealed (spec D12 had a plain column). One comparison per host (stricter than spec). ResolveOwner survives host idle expiry. Reviewer request failed (PR author is the same login); assignee set.

## Review 1 fixes (Opus review of 583c77e4, same PR)
- M1: the cursor position (holds the raw id and source of the last row) is now sealed with AES-256-GCM inside the signed cursor (`cursor.go`, field `x`); test `TestCursorShowsNoRecordIDNorSourceID` decodes every layer on all three paged tools, fails without the seal. Spec 03 lines corrected.
- S1: master-key lock now runs a hook (`server.SetMasterKeyLockHook`, wired to `Grants.CancelAll`) and the resolver route answers 423 while the key is locked or damaged (`lockedGate`); tests in `internal/server/mcp_evidence_test.go`.
- S2: `CancelAll` runs the revoke hooks for every profile (like Stop), so shutdown and lock end every host's handles and aliases (grants stay). Decision: a host's aliases do not outlive an app restart or a lock.
- S3: the minter refuses while stopped and, after its commit, drops its aliases if the stop flag is then set; test `TestMintRacingTheGlobalStopLeavesNothing` (seam `beforeMint`).
- Nits: N1 (no gen gives an empty binding), N2 (`MAC` after `Close` answers random bytes), N3 (`DropResultIf`), N4 and N5 written in spec 03 as known limits. N6 left (guard test only).
