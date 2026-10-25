# TSK-11: Master-key HTTP API and lock gate
risk: high — the only network surface that receives the passphrase; gates every case route.

## Outcome
Routes (all need the session token and the Origin check; bodies are small JSON with `DisallowUnknownFields`):
- `GET /api/master-key/status` → `{"state":"none"|"locked"|"unlocked","dpapiCases":N}` (`dpapiCases` is how many cases could still be re-wrapped; only when unlocked or none, otherwise 0).
- `POST /api/master-key/setup` `{passphrase, confirm, acknowledged:true}` → creates the key and unlocks. Refused unless `acknowledged` is true (the no-recovery warning), the two entries match and the length is at least 12.
- `POST /api/master-key/unlock` `{passphrase}` → unlocks the keyring for this session.
- `POST /api/master-key/lock` → zeroes the keyring.
- `POST /api/master-key/rewrap` `{caseId}` or `{all:true}` → re-wraps DPAPI cases; returns a per-case result.
While the master key is `locked`, every case route (list, open, imports, export, restore, discover imports) answers 423 `{"error":"locked"}` without touching the store. `none` and `unlocked` behave as today (R14, R15).

## Write scope
- `internal/server/masterkey.go`, `internal/server/masterkey_test.go` (new)
- `internal/server/routes.go` (route entries; one lock-gate wrapper)
- `internal/server/server.go` (hold the `Keyring`; start in `locked` when `masterkey.dat` exists, `none` otherwise)

## Must not change
- Existing route behaviour when state is `none` or `unlocked`; the token, Origin, CSRF and body-size handling; `/api/connections/*` (connections vault stays DPAPI-sealed and is not gated).
- The passphrase never appears in logs, access logs, error text, panic output, URLs or responses (I5): bodies are read into a `[]byte`, zeroed after use, and the request-logging path must not record bodies.

## Changes
1. Failure throttling for `unlock` and `setup`: after 5 failures the route answers 429 with a growing delay (1 s doubling to 60 s) tracked in memory; success resets it. All wrong-key outcomes return the same status, body and message.
2. `status` never reveals more than the three states and the count above.
3. The lock gate is one wrapper applied by route table entry, so a new case route cannot forget it; a test walks the route table and fails if a case route is not wrapped.
4. `lock` also drops any open case handles held by the server (so a locked app holds no usable case key in memory).

## Proof
Tests: status in each state; setup refused without acknowledgement, mismatch or short passphrase; unlock right/wrong give identical response shape; 6th wrong attempt → 429 and stays delayed; case routes return 423 while locked and succeed after unlock; `lock` then case route → 423; missing token or bad Origin → refused; unknown JSON field → 400; passphrase absent from captured logs and response bodies (scan); route-table walk test. `go test ./internal/server/...` green.

## Depends on
TSK-10
