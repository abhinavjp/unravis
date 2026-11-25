# mcp-dispatcher: BL-344 backend MCP endpoint and dispatcher

PR: https://github.com/abhinavjp/unravis/pull/264 (base develop, head claude/mcp-dispatcher). SECURITY-SENSITIVE, Opus review needed. CI: see the PR (lint, go, web started 2026-11-23; web green when this was written). Assignee set; reviewer request failed (same account as author).

## Built
- `internal/mcpsrv/handler.go` `Handler(Deps)`: only `GET /mcp/v1/hello`, `POST enroll`, `enroll/claim`, `call`, `cancel`; all else 404/405; any `Origin` header 403.
- `internal/mcpsrv/dispatch.go` `Dispatcher.CallAs`: authenticate (before body read) -> in-flight (4 per profile, 16 total) -> `BeginCall` -> `Scope` (tool in the grant's list; new `Grants.Scope`, `Recheck` now calls it) -> ledger admit (live classes fail closed) -> run -> `Recheck` -> ledger end -> answer. No result after a failed `Recheck` or a `CancelAll`. Call ids are per profile (cancel cannot reach another host's call).
- `internal/mcpsrv/backend.go` `LocalBackend`: one `mcp.NewLocal` bridge per profile+revision+identifiers state over the dashboard handler in process; `Forget` on revoke/reissue/stop via `OnRevoke`. Identifiers switch = profile's identifiers grant now.
- `internal/mcp/envelope.go`: `CallEnvelope` (SDK handler now uses it), `ErrorEnvelope`, `KnownTool`, `StrictJSON`; `result.go` `errorEnvelope` shared.
- `internal/mcpaudit/events.go`, schema 3: `audit_events` (admit/end/denied; tool, class, call id, dur, outcome, code, bytes, HMAC fingerprint of args), cap 100k rows, `RecentEvents`.
- `pipesrv.WithMCP`, `pipesec.IsMCPImage` (= unravis.exe, like the CLI), `cmd/audit-explorer/mcpmode.go` `openMCPEndpoint` (store with `sealedfile.CurrentUser()`; off and 404 if it cannot open; demo needs its own folder).
- Spec note in `docs/desktop/spec/03-mcp.md` (BL-344), BACKLOG "Built ... not ticked".

## Verified (Linux)
Real SQLite file, real HTTP, real dashboard handler on the demo repo, -race: enroll/approve/claim/call of ten tools; only the seven routes reached; no grant/pending/wrong secret never reach the dashboard; tool outside grant; identifiers follow the grant and its expiry; handles per host; revoke, reissue, stop, CancelAll mid-call give refusal and cancel the dashboard request; deaf backend after revoke and after expiry still withheld; bounds, cancel, strict shape, no echo; ledger digest-only (no args/results/secret in file); fail closed on ledger failure; pipesrv image gate; owner wiring. Mutation-checked 8 gates. `go test` of mcp, mcpaudit, mcpsrv, pipesrv, pipesec, clisrv, clicore, cmd/audit-explorer all green; GOOS=windows vet clean.

## NOT verified / open
- Windows: DPAPI store, pipe peer image, second account. Live PC, real hosts: nothing calls this yet (no BL-345 bridge, no BL-347 screen, so every host stays pending; nothing can approve).
- Source scope, tier projection, small-count suppression (BL-348): NOT enforced. A host approved for source A can ask for B. Do not release before BL-348 (or at least a source check).
- BL-350 budgets/cursors, BL-351 screen/prune/90 days not built. Case lock does not call `CancelAll` (dashboard gate answers 423 anyway). BL-345..351 not started.

## Decisions
- No `server.Internal`: local bridge already has the same 7-path allow-list; test records the paths.
- `hello` instead of `proof` (locator has no token per PLAN); returns pid/gen/appVersion; peer policy is the gate.
- Denials are `{ok:false,error}` (401 profile state, 403 tool, 429 bounds, 400 shape, 413, 500); tool errors are `ok:true,isError:true` with the stdio envelope. `profileRev` in the answer, not the envelope. Bridge (BL-345) must turn `ok:false` into an `ErrorEnvelope`-shaped tool error.
- Ledger table here (spec says 344b writes admit/end); schema version 3 (an older build refuses the file, develop-only).

## Leftovers for review
Opus: look at `Dispatcher.CallAs` ordering, `Grants.Scope`, `LocalBackend.bridgeFor` (bridge replaced while a call runs on the old one), unauthenticated cost (HMAC + DB read per request), `StrictJSON` on args, ledger cap/prune race.
