# P-18 `/api/activity` accepts and refuses the new filters

model: sonnet, effort: medium. Reason: mostly a contract test; a small handler change only if the fields do not pass through.
risk: medium. Review batch: RB-3, then RB-6. Wave: W2. Requirement: R-11, integration point I-2.

## Outcome
`POST /api/activity` passes `userId` and `operation` to the repository, refuses bad values with 400, and still refuses unknown fields.

## Write scope
- `internal/server/activity.go`
- `internal/server/activity_test.go` (existing or new)

## Must not change
- Strict decoding (unknown field → 400). Other handlers. The response shape (`Page`).

## Changes
1. Read `internal/server/activity.go` (handler at line 9). If it decodes straight into `audit.ActivityInput` and calls its validation, no handler code is needed: go to step 3. Otherwise copy the two new fields through and call the validation.
2. Make sure a validation failure answers 400 with the handler's usual error body and does not echo the bad value.
3. Tests with the demo repository (the pattern other handler tests in this package use):
   - body `{"sourceIds":["<demo id>"],"start":"…","end":"…","userId":<a demo user>,"operation":"<Demo.Operation>"}` → 200 and every row matches both.
   - `"userId":-1` → 400. `"operation":"Orders"` → 400. `"operation":"A.$where"` → 400. `"operation":{"$ne":1}` → 400 (type error). `"userID":7` (wrong case of an unknown key, if the decoder is case-sensitive; else `"user":7`) → 400.
   - the 400 body for `A.$where` does not contain `$where`.
   - body without the new fields → same rows as before (regression).
   This is the exact JSON the UI sends (see `activityInput` in `P-08`).

## Proof
- [ ] `export PATH="$PWD/.dev/toolchains/go/bin:$PATH" && go build ./... && go vet ./internal/server/` → no output.
- [ ] `go test ./internal/server/ -run 'Activity' -count=1` → `ok`.
- [ ] `go test ./internal/server/ -count=1` (background, about 2.5 min) → `ok`.
- [ ] Demo path verified by test. Live path verified in P-46.

## Depends on
P-13, P-17. Server package order: after P-14, before P-19.
