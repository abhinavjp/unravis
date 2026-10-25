# TSK-11 Master-key HTTP API and lock gate

## Status

DONE. `go test ./internal/server/... ./internal/casestore/... ./internal/masterkey/... ./cmd/... -count=1` all ok; `go vet` clean; `gofmt -l` clean for every new file. `-race` is NOT run: it is unavailable here (no gcc). Nothing committed or pushed.

## Files changed

New:
- `internal/server/masterkey.go` - session, throttle, gate, five handlers, DTOs.
- `internal/server/masterkey_test.go` - 22 tests (status in each state, setup refusals, throttle, concurrent setup, unlock shapes, encodings, body zeroing, gate over every case route, racing create/open vs lock, lock closes handles and stops jobs, rewrap, token/origin, no passphrase leak, stress lock/unlock while serving).
- `cmd/audit-explorer/masterkey_wiring_test.go` - openCases wiring test (deviation 9).

Edited (additive, exact-string edits, CRLF preserved):
- `internal/server/routes.go` - five route rows after `/api/probe/reachable`; in `dispatch`, after the not-found check and before `serveRoute`: `if h.lockedGate(rt) { writeLocked(w); return }`.
- `internal/server/routes_test.go` - five `routeCases` rows.
- `internal/server/server.go` - handler field `mk *MasterKeySession`.
- `internal/server/cases.go` - `writeStoreError` maps `casestore.ErrLocked` to 423 (deviation 2).
- `cmd/audit-explorer/cases.go` - wiring (deviation 2).

Not touched: `internal/discover`, `web/`, `internal/masterkey`, casestore internals. No bug found in `internal/masterkey`.

## Routes

All routes need the session token and a local Origin (same as every other route). Failure body: `{"error":<message>,"reason":<code>,"state":"none|locked|unlocked","retryAfterSeconds":N?}`. Success body is the status object.

Status object: `{"state":"none|locked|unlocked","dpapiCases":N}`. `dpapiCases` is the number of cases still protected by DPAPI only (candidates for rewrap); 0 when locked or when the store cannot report kinds.

### GET /api/master-key/status
200 status object.

### POST /api/master-key/setup
Request `{"passphrase":"...","confirm":"...","acknowledged":true}`. Only these three fields; `acknowledged` must be the literal true.
- 200 `{"state":"unlocked","dpapiCases":N}` (key created and unlocked)
- 400 `acknowledgement-required`, `mismatch`, `too-short` (under 12 characters), `too-long` (over 1024 bytes), `bad-request` (malformed or unknown field)
- 409 `already-set`
- 413 `too-large` (body over 16 KiB)
- 415 `bad-content-type`
- 429 `throttled` (Retry-After header and `retryAfterSeconds`)
- 500 `setup-failed`; `setup-incomplete` (key file created but not unlocked: call unlock with the same passphrase)
- 503 `master-key-unavailable`

### POST /api/master-key/unlock
Request `{"passphrase":"..."}` only.
- 200 `{"state":"unlocked","dpapiCases":N}` (also when already unlocked; nothing evaluated)
- 401 `wrong-key` (identical body for wrong, empty, missing, over-long, near-miss and damaged-file cases)
- 409 `not-set`
- 429 `throttled`
- 400 / 413 / 415 as for setup
- 500 `unlock-failed`
- 503 `master-key-unavailable`

### POST /api/master-key/lock
No body.
- 200 `{"state":"locked","dpapiCases":0}` (idempotent)
- 409 `not-set`
- 503 `master-key-unavailable`

Lock order: stop discover sampler, cancel import jobs, detach ring, close all case handles, zero the ring, close handles again (catches a case opened mid-lock).

### POST /api/master-key/rewrap
Request `{"caseId":"<id>"}` or `{"all":true}` (exactly one).
- 200 `{"results":[{"caseId":"...","status":"rewrapped|already|failed","reason":"case-not-found|locked|canceled|rewrap-failed"}]}` (`reason` only with `failed`)
- 400 `bad-request`, `bad-case-id`
- 409 `no-master-key`
- 423 locked
- 503 `rewrap-unavailable`, `master-key-unavailable`, or cases unavailable

### The lock gate
Every route whose pattern is `/api/cases` or starts `/api/cases/` answers `423 {"error":"locked","reason":"locked"}` while the state is locked, before any handler or store call. This includes TSK-12's future `/api/cases/export` and `/api/cases/restore` automatically. Not gated: `/api/connections/*`, `/api/discover/*`, `/api/hosts`, `/api/export/*` (checked: does not touch the case store), `/api/master-key/*`. State "none" is not gated. A request that passes the gate and then loses the key to a concurrent lock is answered 423 via the `writeStoreError` mapping, not 500.

### Throttle
Separate counters for setup and unlock. 5 failures allowed, then 429 with delays doubling 1 s, 2 s, 4 s ... capped at 60 s. A success resets. Setup counts validation failures (acknowledgement, mismatch, too-short); not malformed bodies or 409. Argon2 runs are serialized (one at a time) by an op mutex.

## How TSK-12 gets the unlocked Keyring

`h.mk.Keyring()` on the handler's `*MasterKeySession` (installed with `server.WithMasterKey`). Returns `(*masterkey.Keyring, nil)` when unlocked, else `casestore.ErrLocked` (map to 423 through `writeStoreError`). Do not call `Lock()` on it, do not keep it past the request. Because `/api/cases/*` is gated, a TSK-12 handler under that prefix is already unlocked-or-none; when state is "none" `Keyring()` returns ErrLocked, so handle "no key" explicitly. `session.Lookup(kind)` and `session.NewCaseKind()` feed the sqlite store (wired in `cmd/audit-explorer/cases.go`).

## Secret handling
Passphrase only in setup/unlock request bodies. Decoded straight from JSON into `[]byte` by a custom `unquoteSecret` (no Go string of the passphrase is created by the server), body buffers zeroed after use, masterkey package zeroes the slice it is given. Never logged, never returned, never in a URL; the response and error text tests assert this.

## Deviations

1. Gate is a pattern-prefix check in `dispatch`, not a per-entry wrapper, so a new `/api/cases/...` route cannot forget it; a table-walk test covers every route. A mutation check (gate disabled) made the test fail.
2. Out-of-scope edits: `internal/server/cases.go` (ErrLocked -> 423 in `writeStoreError`) and `cmd/audit-explorer/cases.go` (session created before the store, passed as `Protectors`/`NewCaseKind`, `WithMasterKey` option). `gofmt -l` lists `cmd/audit-explorer/cases.go` only because the file is CRLF (0 LF-only lines); not a real issue.
3. `WithMasterKey` lives in `masterkey.go`; `server.go` only holds the field.
4. Gate body adds `reason:"locked"` next to `error:"locked"`.
5. Failure bodies: `error` is the message, `reason` the code, plus `state`.
6. `masterkey.dat` sits in the session/config dir next to `hosts.dat`, not in the cases root.
7. `/api/master-key/*` is not subject to the 423 gate.
8. Passphrases over 1024 bytes: `too-long` on setup; treated as wrong-key on unlock without hashing.
9. Added `cmd/audit-explorer/masterkey_wiring_test.go` (not in the packet): status none -> setup -> case kinds DPAPI before / master-key after -> lock gives 423 -> unlock restores listing.

## Unmeasured / known limits

- `-race` not run (no gcc). Concurrency covered by a stress test and the racing create/open test, not by the race detector.
- Residual race: a case load that began before lock and finishes after the second close pass could leave a live handle. The two-pass close narrows it; it is not eliminated and not race-tested.
- Setup runs Argon2 twice (Setup, then Unlock) because the masterkey API returns no key from Setup. Wall-clock cost on a real machine not measured.
- A lock arriving during a long sampler stop can delay its reply up to about 3 s.
- Real DPAPI behaviour and timing on an installed build not measured.
