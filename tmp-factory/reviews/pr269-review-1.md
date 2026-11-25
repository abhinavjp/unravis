# PR #269 security review 1 (BL-349: evidence aliases, signed cursors, handle limits)

Head 583c77e4, branch `claude/mcp-aliases`, diff `origin/develop...HEAD`. Reviewer: a fresh reviewer with no earlier context, read-only. `go test -race` passes for `internal/mcp`, `internal/mcpsrv` and `internal/mcpaudit` on Linux.

**Verdict: NOT CLEAN. There is 1 MUST.**

## MUST

### M1. A signed cursor gives the raw audit record id to a host that has no identifiers grant
- `internal/mcpsrv/cursor.go:93`: `Seal` puts `Pos` (the dashboard's own cursor) into the payload as plain base64url JSON. It is signed, not encrypted.
- On the real store, the dashboard cursor holds the id of the last row of the page. See `internal/store/requests.go:20-24` and `:345`: `pageCursor{... SourceID, ObjectID: last.object.Hex() ...}`. On the demo store, `internal/audit/load.go:171` has `demoSlowCursor{... ID: last.ID}`.
- The sealed cursor comes back to the host in `nextCursor` (`internal/mcp/breakdown.go:67`, `internal/mcp/tools.go:314`, `internal/mcp/activity.go:41`).
- The attack: a host without identifiers decodes the outer payload, then `pos`, and reads the `_id` of the last row. That row also carries an `evidenceRef`. With `limit: 1` and paging, the host learns the alias-to-id link for every row. This defeats `WithHiddenRecordIDs(!identifiers)` (from #266) and the whole point of BL-349 ("a host without the identifiers grant never sees the audit record id"). The leak also existed before this PR, because the raw cursor was passed through. This PR is the one that claims to close it, and it does not.
- Proof of concept: a temporary test in a scratch copy, not committed. A host without identifiers calls `audit_get_slowest` with `limit: 2`. The decoded `pos` is `{"metric":260,...,"sourceId":"demo-west","id":"demo-west-Payroll-current-119",...}`. That is the exact `_id` the identifiers host sees for the same row, whose `evidenceRef` is `ev1_d2fg...`.
- The test does not catch it, and in fact shows the problem: `internal/mcpsrv/alias_test.go:396-425` (`TestSignedCursorPagesAndIsNotTheDashboardCursor`) decodes `inner.Pos` from the cursor and only checks that it is not empty.
- Fix: make `pos` opaque. Option 1: seal it with AEAD, using a key derived from `MAC("cursor-seal", nil)`, the profile and binding as associated data, and a random nonce. Option 2: keep `pos` in the bridge's memory under a random id and put only that id in the cursor. Add a test with a host without identifiers: no part of `nextCursor`, after any level of base64 or JSON decoding, may contain the row's `_id` or its `sourceId`. Run it on all three tools (`requests`, `slowest`, `activity`). Then correct the cursor line in spec 03 (line 404 and line 226 of 3.5): it says `pos` in the clear.

## SHOULD

### S1. "Drop hooked to ... case lock" is not true
- `internal/mcpsrv/alias.go:17`, `cmd/audit-explorer/mcpmode.go:61`, the spec 03 note (line 400) and the handoff all say aliases are dropped on the case lock.
- `Grants.CancelAll` (`internal/mcpsrv/grants.go:435`) is the case-lock path, but its only caller is the owner's shutdown (`cmd/audit-explorer/mcpmode.go:110`). Spec 03 line 340 still says "Not hooked: the case lock calling `CancelAll`".
- Also, the route `GET /api/mcp/evidence/{alias}` is not behind `lockedGate` (`internal/server/routes.go:471`, which covers `/api/cases` only). So aliases resolve while the case is locked.
- Either hook it or correct the docs. Then decide, and write down, whether the resolver should answer 423 while a master key is locked.

### S2. Shutdown deletes some aliases and keeps others
- `CancelAll` runs the revoke hooks only for profiles that have a call running (`takeAllRunning`, `internal/mcpsrv/grants.go:603-614`). So a normal exit runs `Aliases.Drop` for exactly the hosts that were mid-call (`mcpmode.go:110`), and keeps everyone else's aliases.
- This breaks the stated rule that `ResolveOwner` "survives host idle expiry; the person may read the host's report the next day" for those hosts. Nothing about their grant changed.
- Either do not run the alias drop on `CancelAll`, or say so in the docs.

### S3. A mint that races the global stop leaves aliases behind
- `MintAliases` (`internal/mcpaudit/aliases.go:144`) checks state and revision, but not the stop flag.
- `Stop` (`grants.go:399-418`) drops the aliases, and keeps profiles active at the same revision. A mint that commits after the drop leaves aliases that work again after `Resume`. Stop's own comment says "after Resume nothing from before is usable".
- The chance is small, because the cancel runs before the hook and `BeginTx` fails on a cancelled context. A real fix is to pass a `stopped` check into the minter and refuse when it is set, or to store a stop epoch on the alias.

## NIT

- N1. The comment at `internal/mcpsrv/cursor.go:78` and the test at `cursor_test.go:66-71` assume an empty `gen` gives an empty binding. It does not: `newBinding` hashes `""` into a non-empty digest, so `Seal` still works and the cursors survive a bridge rebuild. This cannot happen on Go 1.27, because `crypto/rand.Read` does not fail, so the test checks a state the code can never reach. The same applies to the zero-key fallback at `backend.go:96`. Make `newGen` failure set `binding = ""` explicitly, or drop the comment and the test.
- N2. `DB.Close` (`internal/mcpaudit/db.go:268-274`) zeroes the key in place, and `MAC` then signs with a 32-byte zero key. Any cursor seal or open after close uses a key anyone knows. Today this is unreachable, because `Authenticate` fails on a closed database. `MAC` could panic or return nil after close instead.
- N3. `Sweep` (`internal/mcpsrv/backend.go:146-170`) reads `used` and `n`, then calls `DropResult()`. A result stored on that bridge in between, which would be the newest, is dropped instead of the stale one. This only affects availability. A `DropResultIf(id)` would avoid it.
- N4. The 64 MiB cap is measured as `json.Marshal` size (`internal/mcp/tools.go:182-190`). The in-memory `audit.Analysis` can be several times larger, and the marshal briefly doubles memory. Also, the 128 MiB total is enforced only after the call returns. Between sweeps, up to 2 extra results (the global comparison limit) can be held. That is acceptable, but the docs should say so.
- N5. An alias is the same for the same row for 30 days per profile and revision (`aliases.go:149-156`). A host without identifiers can therefore link rows across different comparisons or windows and count distinct records. Timestamps already allow most of this, so it is not a new class of leak. Note it in the leak review file for BL-348/350 cross-window differencing.
- N6. `TestAliasNoRawID` (`internal/mcpaudit/aliases_test.go:64`) would also pass for any alias derived from the id through a keyed or derived function, so it does not show the alias is random. `TestAliasRecordIDNotInFile` and the code (`newAlias`, crypto/rand) show it. The test is fine as a guard.

## Checked and found sound
- **Signature check.** HMAC is checked before the payload is parsed. The compare is constant time (`hmac.Equal`, `cursor.go:111`). The domain is separated (`MAC` writes the domain, then 0x00). The 16-byte tag is fine. The cursor is bound to tool, profile, revision, sources, tier, identifiers, bridge generation and query fingerprint, and expires after 10 minutes. Cross-host and cross-grant reuse are refused (tests `TestCursorTamperReplayAndBinding` and `TestCursorEndsWithTheGrantAndTheScope`, plus the codec unit test).
- **Aliases.** 130 bits from crypto/rand. The record id is stored sealed with AES-GCM: random nonce, associated data of alias, profile and source. The lookup key is keyed. Every failure is the same `ErrAliasNotFound`. `ResolveOwner` checks that the profile is active at the same revision, the stop is off and the source is still in scope. Revoke, reissue and re-approval all bump the revision (`grants.go:301,353`), so a revoked grant never resolves, even if the delete failed. `Delete` removes the profile's aliases in the same transaction.
- **Mint.** The mint is one transaction that takes the write lock first (DELETE) and then checks the revision. It is capped at 100 per batch and 2,000 per profile (least recently used deleted).
- **Route.** It needs the token (constant-time compare), the Host and Origin allow-lists and the GET method. A wrong alias gets a fixed 404 that does not echo the input. It is not on the ag pipe allow-list (`internal/pipesrv/pipesrv.go:62-65`) and not on the bridge's seven paths (`internal/mcp/bridge.go:383-387`), so no host can resolve an alias.
- **Tier interaction.** The aggregate tier has no `audit_get_requests`, `audit_get_slowest` or `audit_get_activity` (`internal/mcpsrv/tiers.go:16-21`). So aliases appear only for evidence-tier rows and do not undo small-count suppression. The `MinCohort` option is unchanged (`backend.go:128`).
- **Handles.** One result per host bridge. It is swept after every call and every minute, with the least recently used result dropped over 128 MiB (tests use the real backend). A result over the cap is refused with `query_budget_exceeded`.
